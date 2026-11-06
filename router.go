package mux

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "slices"
    "syscall"
    "time"
)

// Config 是 Router 的全部配置。零值可用：未设置的字段走默认行为。
type Config struct {
    // ErrorHandler 是 handler 返回错误的唯一出口：分类与日志都只在这里发生。
    // 为 nil 时使用 defaultErrorHandler。
    //
    // panic 不经过这里：由 Router 在路由边界统一恢复，可用 RecoverHandler 定制。
    // 404/405 也不经过这里：由 net/http 写出，NoMatchHandler 只收到通知。
    ErrorHandler func(c *Ctx, err error)

    // RecoverHandler 处理路由边界捕获的 panic，可自行写响应。
    // 为 nil 时记默认日志，并在响应未提交时写 500。
    RecoverHandler func(c *Ctx, p *Panic)

    // NoMatchHandler 在未命中路由被 net/http 写成 404/405 之后收到通知。
    // status 只会是 404 或 405；状态、Allow 与 body 都由 net/http 决定。
    // 此时响应已提交：只能记录/打点，不要再写响应（写入只会追加到已发送的 body）。
    // 为 nil 时不做任何额外工作，404/405 完全走 net/http 原生路径。
    NoMatchHandler func(c *Ctx, status int)
}

type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 是响应错误的唯一入口：分类与日志都只在这里。
// panic 不经过这里，由 Router 边界恢复。
// 想换错误格式，在 New 时传入 Config.ErrorHandler。
func defaultErrorHandler(c *Ctx, err error) {
    // 1. 业务错误：带状态码，记录后结束。
    if e, ok := errors.AsType[*Error](err); ok && e != nil {
        slog.Error(
            "request failed",
            "method", c.R.Method,
            "path", c.R.URL.Path,
            "status", e.Status,
            "err", e,
        )
        return
    }

    // 2. 未知错误：记日志，避免泄露内部细节。
    slog.Error(
        "request failed",
        "method", c.R.Method,
        "path", c.R.URL.Path,
        "err", err,
    )
}

// New 创建 Router。panic 在路由闭包的 defer 中统一恢复，见 handlePanic。
// Config 可选：不传用零值默认配置，传则用传入的那份。
// 最多接受一个 Config，多传会 panic——避免调用方误以为多个配置会合并。
func New(cfg ...Config) *Router {
    if len(cfg) > 1 {
        panic("mux: New accepts at most one Config")
    }

    var c Config
    if len(cfg) == 1 {
        c = cfg[0]
    }

    if c.ErrorHandler == nil {
        c.ErrorHandler = defaultErrorHandler
    }

    return &Router{
        mux:         http.NewServeMux(),
        middlewares: nil,
        config:      c,
    }
}

// Use 注册一个中间件。中间件会进入此后注册的路由的链；对已注册的路由无效。
// 应在服务启动前完成注册，不要与请求并发调用。
func (r *Router) Use(h Handler) {
    r.middlewares = append(r.middlewares, h)
}

// Get 注册一个 GET 路由。
func (r *Router) Get(pattern string, h Handler) {
    r.register(http.MethodGet, pattern, h)
}

// Post 注册一个 POST 路由。
func (r *Router) Post(pattern string, h Handler) {
    r.register(http.MethodPost, pattern, h)
}

func (r *Router) register(method, pattern string, h Handler) {
    // 链在注册时构造一次，请求期间只读。
    chain := slices.Concat(r.middlewares, []Handler{h})

    r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        c := NewCtx(w, req)
        c.chain = chain

        defer func() {
            if p := recover(); p != nil {
                r.handlePanic(c, p)
            }
        }()

        _, err := c.Next(nil)
        if err != nil && !errors.Is(c.R.Context().Err(), context.Canceled) {
            r.config.ErrorHandler(c, err)
        }
    }))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    if r.config.NoMatchHandler == nil {
        // 没配 hook 时完全交给 net/http：404/405/307 都是原生行为。
        r.mux.ServeHTTP(w, req)
        return
    }

    if _, pattern := r.mux.Handler(req); pattern != "" {
        // 命中：仍然交给 ServeMux.ServeHTTP，由它设置 PathValue，
        // Ctx.Param 才有值；代价是命中路径会匹配两次。
        r.mux.ServeHTTP(w, req)
        return
    }

    // 未命中且配了 hook：让 net/http 正常写 404/405，c.W 只负责记账。
    c := NewCtx(w, req)
    defer func() {
        if p := recover(); p != nil {
            r.handlePanic(c, p)
        }
    }()

    r.mux.ServeHTTP(c.W, req)

    // hook 在响应写出后收到通知，只能记录；不能再改状态、头或 body。
    if code := c.W.StatusCode(); code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
        r.config.NoMatchHandler(c, code)
    }
}

// Run 启动服务并在收到中断信号后优雅关停。
func (r *Router) Run(addr string) error {
    s := &http.Server{
        Addr:              addr,
        Handler:           r,
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       10 * time.Second,
        WriteTimeout:      15 * time.Second,
        IdleTimeout:       60 * time.Second,
    }

    errCh := make(chan error, 1)
    go func() { errCh <- s.ListenAndServe() }()

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    select {
    case err := <-errCh:
        if errors.Is(err, http.ErrServerClosed) {
            return nil
        }
        return err
    case <-ctx.Done():
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        return s.Shutdown(shutdownCtx)
    }
}

// handlePanic 是路由边界的恢复点：接住链、ErrorHandler 与框架自身逃出的 panic。
// 必须由 defer 直接 recover 后调用；recover 只能在 deferred 函数里直接生效。
func (r *Router) handlePanic(c *Ctx, p any) {
    // net/http 约定：ErrAbortHandler 静默中止响应，不记日志、不再写响应。
    if p == http.ErrAbortHandler {
        panic(p)
    }

    info := NewPanic(c, p)
    if f := r.config.RecoverHandler; f != nil {
        f(c, info)
        return
    }

    defaultRecoverHandler(c, info)
}
