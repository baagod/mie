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

    // NoMatchHandler 接管未命中响应：在 net/http 写 404/405 之前调用。
    // 此时 c.StatusCode() 已是 404（路径不存在）或 405（方法不允许），405 的 Allow 头也已写好。
    // 返回 nil 表示接管完成：写过响应就按写的发；没写则提交拦截到的 404/405（空 body）。
    // 返回 error 交给 writeError 写出。
    // 自行写 body 时请自行设置 Content-Type：标准库已预置的
    // "text/plain; charset=utf-8" 与 nosniff 会被原样保留；改写 Allow 请用 SetHeader——
    // 用 Add 追加的值在非 405 响应里会随 mux 预置值一起被清掉。
    // 为 nil 时保留 net/http 的默认 404/405。
    NoMatchHandler func(c *Ctx) error

    // Logger 是框架日志的落地位置：访问日志与框架自身的诊断日志都写到这里；
    // nil 时 New 填入写 stderr 的默认 Logger。按请求过滤或加字段，请包一层 slog.Handler。
    Logger *slog.Logger
}

type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 原样放行错误；响应头与 body 由 writeError 统一写出。
func defaultErrorHandler(_ *Ctx, err error) error {
    return err
}

// defaultRecoverHandler 是未配置 Config.RecoverHandler 时的兜底：记录 Panic 并返回 500。
func defaultRecoverHandler(c *Ctx, info *Panic) error {
    c.LogError("PANIC RECOVERED", "detail", info.Text())
    return Status500InternalServerError()
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

    if c.Logger == nil {
        c.Logger = logger
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
        // panic 的恢复与补记在 ServeHTTP 的 defer 里处理。
        c := req.Context().Value(ctxKey{}).(*Ctx)
        c.chain = chain
        c.W.noMatch = nil // 已进入路由闭包，此后的 404/405 都不算未命中。

        _, err := c.Next(nil)
        if err != nil && !c.IsCanceled() {
            err = r.config.ErrorHandler(c, err)
        }

        c.finish(err)
    }))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    c := newCtx(w, req, r)

    defer func() {
        if p := recover(); p != nil {
            r.handlePanic(c, p)
        }
    }()

    // 配了 NoMatchHandler 时把它挂到 writer 上：ServeMux 自己写 404/405 时
    // 由 writer 就地交给钩子，默认错误体不再发出去。
    var err error
    if r.config.NoMatchHandler != nil {
        c.W.noMatch = func() {
            err = r.config.NoMatchHandler(c)
        }
    }

    r.mux.ServeHTTP(c.W, c.R)

    // c.chain 只在命中路由的闭包入口赋值；仍为 nil 说明请求没进用户链
    // （404/405/规范化重定向），访问日志在这里补一条。
    if c.chain == nil {
        c.finish(err)
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

    var err error
    abort := p == http.ErrAbortHandler
    if !abort {
        err = r.config.RecoverHandler(c, NewPanic(c, p))
        abort = c.W.wroteHeader || c.W.hijacked
    }

    // 中止信号 / 已提交断连：都不再写响应，只记一条访问日志。
    if abort {
        c.logAccess(nil)
        panic(http.ErrAbortHandler)
    }

    c.finish(err) // 正常恢复：写 500、commit、记日志
}
