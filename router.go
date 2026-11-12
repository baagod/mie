package mie

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "reflect"
    "slices"
    "syscall"
    "time"
)

// Config 零值即可使用；缺省项在 New 中补齐。
type Config struct {
    // ErrorHandler 处理用户链返回的错误。panic 与 404/405 有独立出口，不经过这里。
    ErrorHandler func(c *Ctx, err error) error

    // RecoverHandler 把恢复到的 panic 转成错误（交给 writeError）；
    // 未配置时默认记日志并写 500。响应已提交时不再写，框架直接中止连接。
    RecoverHandler func(c *Ctx, p *Panic) error

    // NoMatchHandler 在 net/http 写 404/405 之前接管。
    // 写 body 时自行设置 Content-Type，不需要时用 c.W.Header().Del 删除。
    // 未配置时保留 net/http 默认的 404/405。
    NoMatchHandler func(c *Ctx) error

    // Logger 是访问日志与框架诊断日志的落点；nil 时 New 用写 stderr 的默认 Logger。
    // 需要按请求过滤或附加字段时，请包一层 slog.Handler。
    Logger *slog.Logger
}

// Router 包装 http.ServeMux，保存中间件与配置。
type Router struct {
    mux         *http.ServeMux
    middlewares []Handler
    config      Config
}

// defaultErrorHandler 原样放行，真正的响应由 writeError 写出。
func defaultErrorHandler(_ *Ctx, err error) error {
    return err
}

// defaultRecoverHandler 记录 Panic 并返回 500 错误。
func defaultRecoverHandler(c *Ctx, info *Panic) error {
    c.LogError("PANIC RECOVERED", "detail", info.Text())
    return Status500InternalServerError()
}

// New 创建 Router；最多一个 Config，多传会 panic。panic 的恢复见 handlePanic。
func New(cfg ...Config) *Router {
    if len(cfg) > 1 {
        panic("mie: New accepts at most one Config")
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

    return &Router{mux: http.NewServeMux(), config: c}
}

// Use 追加中间件，只影响之后注册的路由；服务启动后不要再调用。
// 中间件是 HandlerFunc[IN, any]：必须调用 c.Next() 推进链；返回 nil 表示不写 body。
// 带 IN 的中间件（typed middleware）尚未支持。
func (r *Router) Use[IN any](m HandlerFunc[IN, any]) error {
    if reflect.TypeFor[IN]() != reflect.TypeFor[struct{}]() {
        return errors.New("mie: typed middleware is not supported")
    }
    r.middlewares = append(r.middlewares, handler(m))
    return nil
}

// Get 注册 GET 路由。
func (r *Router) Get[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) {
    r.Handle(http.MethodGet, pattern, handler(h))
}

// Post 注册 POST 路由。
func (r *Router) Post[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) {
    r.Handle(http.MethodPost, pattern, handler(h))
}

// Put 注册 PUT 路由。
func (r *Router) Put[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) {
    r.Handle(http.MethodPut, pattern, handler(h))
}

// Patch 注册 PATCH 路由。
func (r *Router) Patch[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) {
    r.Handle(http.MethodPatch, pattern, handler(h))
}

// Delete 注册 DELETE 路由。
func (r *Router) Delete[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) {
    r.Handle(http.MethodDelete, pattern, handler(h))
}

// Handle 把中间件与最终处理器拼成链，注册到 ServeMux。
func (r *Router) Handle(method, pattern string, h Handler) {
    chain := slices.Concat(r.middlewares, []Handler{h})

    r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
        // Ctx 由 ServeHTTP 创建并挂在 context 上；这里只负责跑链。
        c := req.Context().Value(ctxKey{}).(*Ctx)
        c.chain = chain
        c.W.noMatch = nil // 进入路由闭包后，404/405 都算业务响应。

        err := c.Next()
        if err != nil && !c.IsCanceled() {
            err = r.config.ErrorHandler(c, err)
        }

        c.finish(err)
    }))
}

// ServeHTTP 创建 Ctx、安装 NoMatch 钩子、执行 ServeMux，并为未进入用户链的请求补收尾。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    c := newCtx(w, req, r)

    defer func() {
        if p := recover(); p != nil {
            r.handlePanic(c, p)
        }
    }()

    // 挂上 NoMatchHandler：ServeMux 自己写出的 404/405 会在 writer 层被截下来交给钩子，
    // 默认错误体不再发出。
    var err error
    if r.config.NoMatchHandler != nil {
        c.W.noMatch = func() {
            err = r.config.NoMatchHandler(c)
        }
    }

    r.mux.ServeHTTP(c.W, c.R)

    // c.chain 只由命中路由的闭包赋值；为 nil 说明请求没进用户链
    // （404/405/规范化重定向），在这里补访问日志。
    if c.chain == nil {
        c.finish(err)
    }
}

// Run 启动 HTTP 服务，收到 Interrupt/SIGTERM 后优雅关停。
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

// handlePanic 是请求边界的最后一道恢复：接住用户链、ErrorHandler 与框架自身逃出的 panic。
// 只能由 defer 直接调用——recover 在嵌套调用里拿不到 panic。
func (r *Router) handlePanic(c *Ctx, p any) {
    // 从这里逃出的 panic（包括 RecoverHandler 自己崩了）仍要留下访问日志，
    // 否则请求会从日志里消失；记完按原值抛回，让 net/http 继续兜底。
    defer func() {
        if err := recover(); err != nil {
            c.logAccess(nil)
            panic(err)
        }
    }()

    // 链已被 panic 终止：把游标推到链尾，恢复再调 Next() 会直接报错。
    c.index = len(c.chain)

    if p == http.ErrAbortHandler {
        panic(p) // 调用方要求中止连接，不写响应，原样抛回。
    }

    err := r.config.RecoverHandler(c, NewPanic(c, p))
    if c.W.wroteHeader || c.W.hijacked {
        // 响应已经发出，状态不可信：再写只会污染半截响应，按中止交给 net/http。
        panic(http.ErrAbortHandler)
    }

    // 响应还没写：恢复器给出的错误交 finish 写响应并收尾（默认 500）。
    c.finish(err)
}
