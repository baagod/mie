package mux

import (
    "context"
    "errors"
    "net/http"
    "os"
    "os/signal"
    "slices"
    "syscall"
    "time"
)

// Config 零值可用：未配置的字段走默认行为。
type Config struct {
    // ErrorHandler 处理 handler 链返回的 error；nil 时写默认错误响应。
    // panic 与 404/405 不经过这里。
    ErrorHandler func(c *Ctx, err error)

    // RecoverHandler 定制路由边界捕获的 panic；nil 时记日志，响应未提交则写 500，已提交则断连。
    RecoverHandler func(c *Ctx, p *Panic)

    // NoMatchHandler 在 net/http 写完 404/405 后被调用，status 只会是这两个值。
    // 此时响应已提交，只能记录；为 nil 时不做任何额外工作。
    NoMatchHandler func(c *Ctx, status int)
}

type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 是未配置 Config.ErrorHandler 时的兜底：
// 把 handler 返回的错误写成状态码与文本。
func defaultErrorHandler(c *Ctx, err error) {
    status := http.StatusInternalServerError
    if e, ok := errors.AsType[*Error](err); ok && e != nil {
        status = e.Status
    }

    header := c.W.Header()
    header.Set(HeaderContentType, MIMETextPlainCharsetUTF8)
    header.Set(HeaderXContentTypeOptions, ContentTypeOptionsNosniff)

    c.W.WriteHeader(status)
    _, _ = c.W.WriteString(err.Error())
}

// New 创建 Router。panic 由路由闭包中的 defer 恢复，见 handlePanic。
// 最多接受一个 Config；多传 panic，避免误以为多个配置会合并。
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

// Use 注册中间件，只影响此后注册的路由；应在服务启动前调用，不要与请求并发。
func (r *Router) Use(h Handler) {
    r.middlewares = append(r.middlewares, h)
}

func (r *Router) Get(pattern string, h Handler) {
    r.handler(http.MethodGet, pattern, h)
}

func (r *Router) Post(pattern string, h Handler) {
    r.handler(http.MethodPost, pattern, h)
}

func (r *Router) handler(method, pattern string, h Handler) {
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
        // 无 hook：完全交给 net/http，404/405/307 保持原生。
        r.mux.ServeHTTP(w, req)
        return
    }

    if _, pattern := r.mux.Handler(req); pattern != "" {
        // 命中也要走 ServeMux.ServeHTTP：PathValue 由它填充，Ctx.Param 才有值；
        // 代价是命中路径匹配两次。
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

    code := c.W.StatusCode()
    if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
        r.config.NoMatchHandler(c, code)
    }
}

// Run 启动服务，收到 Interrupt/SIGTERM 后优雅关停。
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
// 必须由 defer 直接调用 recover 后转入；recover 在嵌套调用中不生效。
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
