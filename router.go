package mux

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "runtime/debug"
    "slices"
    "syscall"
    "time"
)

// Config 是 Router 的全部配置。零值可用：未设置的字段走默认行为。
type Config struct {
    // ErrorHandler 是响应错误的唯一出口：分类与日志都只在这里发生。
    // 为 nil 时使用 defaultErrorHandler。
    //
    // panic 不经过这里：由内置 Recoverer 处理，可用 RecoverHandler 定制。
    // ServeMux 直接返回的 404/405 由 Router 接管后也走本函数。
    ErrorHandler func(c *Ctx, err error)

    // RecoverHandler 处理已捕获的 panic，可自行写响应。
    // 为 nil 时只记录默认日志（Panic.Text）。
    RecoverHandler func(c *Ctx, p *Panic)
}

type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 是响应错误的唯一入口：分类与日志都只在这里。
// panic 不经过这里，由内置 Recoverer 处理。
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

// New 创建 Router。Recoverer 在注册路由时装为链首，见 register。
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
    // 顺序：Recoverer → 用户中间件 → 路由处理器。
    chain := slices.Concat([]Handler{Recoverer(r.config.RecoverHandler)}, r.middlewares, []Handler{h})

    r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        c := NewCtx(w, req)
        c.chain = chain

        // Recoverer 只覆盖链内 panic；写响应与错误处理器里的 panic 在这一层兜底。
        defer func() {
            if p := recover(); p != nil {
                // net/http 约定：ErrAbortHandler 静默中止响应，继续抛回给 net/http。
                if p == http.ErrAbortHandler {
                    panic(p)
                }
                slog.Error(
                    "panic outside handler chain",
                    "method", req.Method,
                    "path", req.URL.Path,
                    "panic", p,
                    "stack", string(debug.Stack()),
                )
            }
        }()

        _, err := c.Next(nil)
        // panic 已由 Recoverer 记录并处理完；业务错误只记日志。
        if err != nil && !errors.Is(err, RecoverError) && !errors.Is(c.R.Context().Err(), context.Canceled) {
            r.config.ErrorHandler(c, err)
        }
    }))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    _, pattern := r.mux.Handler(req)
    if pattern == "" {
        // 暂不区分 404/405，也不发送响应体。
        r.config.ErrorHandler(NewCtx(w, req), NewError(http.StatusNotFound, http.StatusText(http.StatusNotFound)))
        return
    }
    // Handler 只用于判断是否命中，它不会填充路径通配符（Go 文档明确
    // r.PathValue 会返回空串）。命中后交给 ServeMux.ServeHTTP，由它设置
    // 匹配结果，这样 Ctx.Param 才能拿到值；代价是命中路径会匹配两次。
    r.mux.ServeHTTP(w, req)
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
