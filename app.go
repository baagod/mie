package mie

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/baagod/mie/internal/xslices"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const defaultMaxBodyBytes = 1 << 20

// 零值可用，缺省项在 New 补齐。
type Config struct {
	// ErrorHandler 只处理用户链返回的错误；panic 与 404/405 各有独立出口。
	ErrorHandler func(c *Ctx, err error) error

	// RecoverHandler 把恢复到的 panic 转成错误，仍交给 writeError 写出。
	// 缺省记日志并返回 500；响应已提交时不再写，框架直接中止连接。
	RecoverHandler func(c *Ctx, p *Panic) error

	// NoMatchHandler 在 net/http 写出 404/405 之前接管。
	// 接管时 Content-Type 已是 text/plain，要别的类型自行覆盖；缺省保留 net/http 的 404/405。
	NoMatchHandler func(c *Ctx) error

	// Logger 是访问日志与框架日志的落点，nil 时写 stderr。
	// 默认开在 LevelTrace：访问日志可见；Handler 调到 Debug 只剩自己的日志，调到 Info 则全部静默。
	Logger *slog.Logger

	// Codecs 声明可提供的格式与优先级；零值只提供内置 JSON、XML。
	Codecs Codecs

	// MaxBodyBytes 是非 multipart 请求的 body 上限，超出时读 body 返回 413。
	// 零值补 1MiB，负值关闭；multipart 不经过这里，由上传路由用中间件包 http.MaxBytesReader 限制。
	MaxBodyBytes int64

	// ServerConfig 在 Server 建好后、开始服务前调用。
	ServerConfig func(*http.Server)
}

type App struct {
	*Router
	mux    *http.ServeMux
	config Config
}

// New 最多接受一个 Config，多传 panic。
func New(cfg ...Config) *App {
	if len(cfg) > 1 {
		panic("mie: New accepts at most one Config")
	}

	c := xslices.First(cfg)

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

func (a *App) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Body != nil && req.Body != http.NoBody && a.config.MaxBodyBytes > 0 {
		if ct, _, _ := mime.ParseMediaType(req.Header.Get(HeaderContentType)); ct != MIMEMultipartForm {
			req.Body = http.MaxBytesReader(w, req.Body, a.config.MaxBodyBytes)
		}
	}

	c := newCtx(w, req, a)

	defer func() {
		// multipart 文件可能落到磁盘，请求结束（含 panic）先删。
		if f := c.R.MultipartForm; f != nil {
			_ = f.RemoveAll()
		}
		if p := recover(); p != nil {
			a.handlePanic(c, p)
		}
	}()

	// ServeMux 自己写的 404/405 在 writer 层被截下交给钩子，默认错误体不再发出。
	var err error
	if a.config.NoMatchHandler != nil {
		c.W.noMatch = func() {
			err = a.config.NoMatchHandler(c)
		}
	}

	a.mux.ServeHTTP(c.W, c.R)

	// chain 只由命中路由的闭包赋值；nil 表示请求没进用户链（404/405/重定向），在这里补收尾。
	if c.chain == nil {
		c.finish(err)
	}
}

// Run 在 addr 上监听，空则 ":8080"。certFiles 传两个路径（cert、key）时走 HTTPS。
func (a *App) Run(addr string, certFiles ...string) error {
	if len(certFiles) != 0 && len(certFiles) != 2 {
		return fmt.Errorf("mie: Run needs 0 or 2 cert files (cert, key), got %d", len(certFiles))
	}

	l, err := net.Listen("tcp", cmp.Or(addr, ":8080"))
	if err != nil {
		return err // 启动失败在这里就返回，不进 serve。
	}

	// Serve 自己也会关；ServeTLS 在包 listener 前失败时靠这里兜住。
	//goland:noinspection GoUnhandledErrorResult
	defer l.Close()

	if len(certFiles) == 2 {
		return a.serve(func(s *http.Server) error { return s.ServeTLS(l, certFiles[0], certFiles[1]) })
	}

	return a.RunListen(l)
}

// RunListen 在 l 上服务，收到 Interrupt/SIGTERM 后优雅关停，返回时 l 已关闭。
// TLS 请用 tls.NewListener 包好再传。
func (a *App) RunListen(l net.Listener) error {
	return a.serve(func(s *http.Server) error { return s.Serve(l) })
}

func (a *App) serve(run func(*http.Server) error) error {
	s := &http.Server{
		Handler:           a,
		ErrorLog:          slog.NewLogLogger(a.config.Logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		// 必须大于前置代理的空闲超时（ALB 与 nginx 后端侧默认都是 60s），
		// 否则代理仍可能把请求发到我们已经关掉的连接上。
		IdleTimeout: 90 * time.Second,
	}

	if c := a.config.ServerConfig; c != nil {
		c(s)
	}

	// 先注册信号再开始服务，避免启动瞬间的 SIGTERM 直接杀掉进程。
	sysCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 信号只负责关停；停机结果单独通过 shutdownErr 带回。run 自己报错时以它为准，不再等排空。
	// 缓冲 1：外部关闭时 serve 不等这个结果，无缓冲会让 goroutine 永久阻塞。
	shutdownErr := make(chan error, 1)
	go func() {
		<-sysCtx.Done()
		stop() // 第二次 Ctrl+C 回到默认行为：直接终止

		// 停机最多等 8s。必须小于平台宽限期（docker stop 默认 10s），
		// 否则到点被 SIGKILL，收尾代码和 defer 都来不及执行。
		// 不能用 sysCtx：它此刻已经取消，有在途请求时 Shutdown 会立刻返回。
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		// Shutdown 超时只表示不再等，不关活跃连接，这里关闭。
		// Close 也不管 Hijack 出去的连接（WebSocket/SSE），只能靠进程退出或调用方自己关。
		err := s.Shutdown(ctx)
		if err != nil {
			_ = s.Close()
		}

		shutdownErr <- err
	}()

	if err := run(s); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = s.Close() // run 自己失败，不会有人再来关连接
		return err
	}

	if sysCtx.Err() != nil {
		// 等它排空完再返回；排空超时算停机失败，不是干净退出。
		if err := <-shutdownErr; err != nil {
			return fmt.Errorf("mie: shutdown: %w", err)
		}
	}

	return nil
}

// handlePanic 是请求边界的最后一道恢复，接住用户链、ErrorHandler 与框架自己逃出的 panic。
// 只能由 defer 直接调用：嵌套调用里的 recover 拿不到 panic。
func (a *App) handlePanic(c *Ctx, p any) {
	// 这里逃出的 panic（含 RecoverHandler 自己崩）也要留访问日志，否则请求会从日志里消失；
	// 记完按原值抛回，交给 net/http 兜底。
	defer func() {
		if err := recover(); err != nil {
			c.logAccess(nil, true)
			panic(err)
		}
	}()

	// 链已终止：游标推到链尾，恢复后再调 Next() 会直接报错。
	c.index = len(c.chain)

	if p == http.ErrAbortHandler {
		panic(p) // 调用方要求中止连接，不写响应。
	}

	err := a.config.RecoverHandler(c, NewPanic(c, p))
	if c.W.wroteHeader || c.W.hijacked {
		// 响应已发出，状态不可信：再写只会污染半截响应，按中止交给 net/http。
		panic(http.ErrAbortHandler)
	}

	c.finish(err)
}

// defaultErrorHandler 原样放行，响应由 writeError 写出。
func defaultErrorHandler(_ *Ctx, err error) error {
	return err
}

func defaultRecoverHandler(c *Ctx, info *Panic) error {
	c.LogError("PANIC RECOVERED", "detail", info.Text())
	return Status500InternalServerError()
}
