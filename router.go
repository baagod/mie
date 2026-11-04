package mux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"time"
)

// Config 是 Router 的全部配置。零值可用：未设置的字段走默认行为。
type Config struct {
	// ErrorHandler 是匹配到路由后的唯一错误漏斗：分类、日志、渲染都只在这里发生。
	// 为 nil 时使用 defaultErrorHandler。
	//
	// 注意：ServeMux 直接返回的 404/405 不经过这里，它们由 net/http 自己写响应。
	ErrorHandler func(c *Ctx, err error)
}

type Router struct {
	mux      *http.ServeMux
	handlers []Handler
	config   Config
}

// defaultErrorHandler 是错误处理的唯一入口：分类、日志、写响应都只在这里。
// 想换 JSON 错误体 / 业务错误码映射，在 New 时传入 Config.ErrorHandler。
func defaultErrorHandler(c *Ctx, err error) {
	// 已经写过响应（例如流式输出到一半出错）：只能记日志，不能再写头。
	if c.wrote {
		slog.Error("error after response written", "path", c.Request.URL.Path, "err", err)
		return
	}

	// 1. 业务错误：带状态码，直接把状态码和消息写出去。
	// 状态码非法时降级为 500：http.Error 在状态码不在 100..999 时会 panic，
	// 而这里已经出了 recoverer，panic 会直接打断连接。
	if he, ok := errors.AsType[*Error](err); ok && he != nil {
		status := he.Status
		if status < 100 || status > 999 {
			slog.Error("invalid error status", "status", status, "path", c.Request.URL.Path)
			status = http.StatusInternalServerError
		}
		http.Error(c.Writer, he.Msg, status)
		return
	}

	// 2. 客户端取消 / 超时：连接已经不可用，不需要再写响应。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}

	// 3. 未知错误：记日志，对外只给通用 500，避免泄露内部细节。
	slog.Error(
		"request failed",
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"err", err,
	)

	http.Error(c.Writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// New 创建 Router。Config 可选：不传用零值默认配置，传则用传入的那份。
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
		mux:    http.NewServeMux(),
		config: c,
	}
}

// Use 注册全局中间件。必须在注册路由 / Run 之前调用。
func (r *Router) Use(h Handler) { r.handlers = append(r.handlers, h) }

func (r *Router) Get(pattern string, h Handler)  { r.handle(GET, pattern, h) }
func (r *Router) Post(pattern string, h Handler) { r.handle(POST, pattern, h) }

func (r *Router) handle(method, pattern string, h Handler) {
	r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c := &Ctx{
			Request: req,
			index:   -1,
			chain:   r.chain(h),
		}
		// 包一层 ResponseWriter，让 handler 直接写 c.Writer 也能被错误漏斗看见，
		// 否则写出响应后再返回 error 会二次写头、污染响应。
		c.Writer = &responseWriter{ResponseWriter: w, c: c}

		if err := c.Next(); err != nil {
			r.config.ErrorHandler(c, err)
		}
	}))
}

// chain 固定为：recoverer → 用户中间件 → 路由 handler。
// recoverer 把 panic 转成普通 error，panic 因此和其它错误走同一条路。
func (r *Router) chain(h Handler) []Handler {
	return slices.Concat([]Handler{recoverer}, r.handlers, []Handler{h})
}

// recoverer 是内置的第一道中间件：接住 panic，记日志，返回 error。
// 它自己不做渲染，渲染统一交给 Config.ErrorHandler。
func recoverer(c *Ctx) error {
	var err error

	func() {
		defer func() {
			if p := recover(); p != nil {
				// net/http 约定：panic(http.ErrAbortHandler) 用于静默中止响应，
				// 既不该记日志也不该写响应。继续抛回给 net/http 处理。
				if p == http.ErrAbortHandler {
					panic(p)
				}

				slog.Error(
					"panic",
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"panic", p,
					"stack", string(debug.Stack()),
				)
				err = fmt.Errorf("panic: %v", p)
			}
		}()

		err = c.Next()
	}()

	return err
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Run 便利启动。
func (r *Router) Run(addr string) error {
	s := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s.ListenAndServe()
}
