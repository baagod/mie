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

// Config 零值可用：未配置的字段走默认行为。
type Config struct {
    // ErrorHandler 处理 handler 链返回的 error，返回值交给 writeError 写响应；
    // nil 时原样放行。panic 与 404/405 不经过这里。
    ErrorHandler func(c *Ctx, err error) error

    // RecoverHandler 定制路由边界捕获的 panic，返回值交给 writeError 写响应；
    // 未配置时记日志并写 500。响应已提交时框架中止连接，不再进入写出口。
    RecoverHandler func(c *Ctx, p *Panic) error

    // NoMatchHandler 在 net/http 写完 404/405 后被调用，status 只会是这两个值。
    // 此时响应已提交，只能记录；为 nil 时不做任何额外工作。
    NoMatchHandler func(c *Ctx, status int)

    // LogHandler 接管访问日志：配置后框架不再打印，由回调决定记什么、记到哪；
    // nil 时框架用自己的 logger 输出一条 access 记录（stderr 文本）。
    // level 由状态码换算：5xx 为 Error、4xx 为 Warn、其余 Info。
    LogHandler func(c *Ctx, level slog.Level, msg string, attrs []slog.Attr)
}

// ctxKey 把 Ctx 挂到请求 context 上，供路由闭包取回。
type ctxKey struct{}

type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 原样放行错误；响应头与 body 由 writeError 统一写出。
func defaultErrorHandler(_ *Ctx, err error) error {
    return err
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

    if c.RecoverHandler == nil {
        c.RecoverHandler = defaultRecoverHandler
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

    r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
        // Ctx 由 ServeHTTP 创建并挂在 context 上，这里只负责跑链；
        // recover 与访问日志统一在 ServeHTTP 的 defer 里处理。
        c := req.Context().Value(ctxKey{}).(*Ctx)
        c.chain = chain

        _, err := c.Next(nil)
        if err != nil {
            if !errors.Is(c.R.Context().Err(), context.Canceled) {
                c.writeError(r.config.ErrorHandler(c, err))
            }
            c.logAccess(err)
            return
        }

        // 路由边界统一收口：commit 内部已判断是否写过和 hijacked
        c.W.commit()
        c.logAccess(nil)
    }))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    c := NewCtx(w, req)
    c.router = r
    // 把 Ctx 塞回请求：路由闭包从 context 取回同一个实例。
    // Pattern 与 PathValue 由 ServeMux 写在这个副本上，未命中时 Pattern 为空。
    c.R = req.WithContext(context.WithValue(req.Context(), ctxKey{}, c))

    defer func() {
        if p := recover(); p != nil {
            r.handlePanic(c, p)
            c.logAccess(nil)
        }
    }()

    r.mux.ServeHTTP(c.W, c.R)

    // Pattern 为空说明响应由 ServeMux 自己写出（404/405），没有经过路由闭包。
    if c.R.Pattern == "" {
        c.logAccess(nil)
        if r.config.NoMatchHandler != nil {
            if code := c.StatusCode(); code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
                r.config.NoMatchHandler(c, code)
            }
        }
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
    // 判定前先把游标推到链尾：panic 已终止链，恢复回调再调 Next 也不会续跑。
    c.index = len(c.chain)

    // net/http 约定：ErrAbortHandler 静默中止响应，不记日志、不再写响应。
    if p == http.ErrAbortHandler {
        panic(p)
    }

    err := r.config.RecoverHandler(c, NewPanic(c, p))
    if c.W.wroteHeader || c.W.hijacked {
        panic(http.ErrAbortHandler)
    }

    c.writeError(err)
}
