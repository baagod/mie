package mie

import (
    "context"
    "errors"
    "log/slog"
    "mime"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"
)

// 未配置 MaxBodyBytes 时的请求 body 上限。
const defaultMaxBodyBytes = 1 << 20

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

    // Codecs 声明可提供的格式与优先级；零值只提供内置 JSON、XML。
    Codecs Codecs

    // MaxBodyBytes 是非 multipart 请求的 body 上限，超出时读 body 返回 413。
    // 零值在 New 中补 1MiB；负值关闭这层保护。multipart 不适用：上传大小由上传路由自行限制。
    MaxBodyBytes int64
}

// App 持有 ServeMux、配置与生命周期方法；注册面见 Router。
type App struct {
    *Router
    mux    *http.ServeMux
    config Config
}

// New 创建 App；最多一个 Config，多传会 panic。panic 的恢复见 handlePanic。
func New(cfg ...Config) *App {
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

    if c.MaxBodyBytes == 0 {
        c.MaxBodyBytes = defaultMaxBodyBytes
    }

    c.Codecs.resolve()

    a := &App{mux: http.NewServeMux(), config: c}
    a.Router = &Router{app: a}
    return a
}

// ServeHTTP 包装 body 上限、创建 Ctx、安装 NoMatch 钩子、执行 ServeMux，并为未进入用户链的请求补收尾。
func (a *App) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    if req.Body != nil && req.Body != http.NoBody && a.config.MaxBodyBytes > 0 {
        if ct, _, _ := mime.ParseMediaType(req.Header.Get(HeaderContentType)); ct != MIMEMultipartForm {
            req.Body = http.MaxBytesReader(w, req.Body, a.config.MaxBodyBytes)
        }
    }

    c := newCtx(w, req, a)

    defer func() {
        // multipart 的文件可能落到磁盘临时文件，请求结束（含 panic）先删。
        if f := c.R.MultipartForm; f != nil {
            _ = f.RemoveAll()
        }
        if p := recover(); p != nil {
            a.handlePanic(c, p)
        }
    }()

    // 挂上 NoMatchHandler：ServeMux 自己写出的 404/405 会在 writer 层被截下来交给钩子，
    // 默认错误体不再发出。
    var err error
    if a.config.NoMatchHandler != nil {
        c.W.noMatch = func() {
            err = a.config.NoMatchHandler(c)
        }
    }

    a.mux.ServeHTTP(c.W, c.R)

    // c.chain 只由命中路由的闭包赋值；为 nil 说明请求没进用户链
    // （404/405/规范化重定向），在这里补访问日志。
    if c.chain == nil {
        c.finish(err)
    }
}

// Run 启动 HTTP 服务，收到 Interrupt/SIGTERM 后优雅关停。
func (a *App) Run(addr string) error {
    s := &http.Server{
        Addr:              addr,
        Handler:           a,
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
func (a *App) handlePanic(c *Ctx, p any) {
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

    err := a.config.RecoverHandler(c, NewPanic(c, p))
    if c.W.wroteHeader || c.W.hijacked {
        // 响应已经发出，状态不可信：再写只会污染半截响应，按中止交给 net/http。
        panic(http.ErrAbortHandler)
    }

    // 响应还没写：恢复器给出的错误交 finish 写响应并收尾（默认 500）。
    c.finish(err)
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
