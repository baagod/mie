package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"syscall"
	"time"
)

// maxBodyBytes 是请求体默认上限：1 MiB。超过时返回 413。
const maxBodyBytes = 1 << 20

// Config 是 Router 的全部配置。零值可用：未设置的字段走默认行为。
type Config struct {
	// ErrorHandler 是响应错误的唯一出口：分类、日志、渲染都只在这里发生。
	// 为 nil 时使用 defaultErrorHandler。
	//
	// panic 不经过这里：由内置 Recoverer 处理，可用 RecoverHandler 定制。
	// ServeMux 直接返回的 404/405 由 Router 接管后也走本函数。
	ErrorHandler func(c *Ctx, err error)

	// RecoverHandler 处理已捕获的 panic，可写自定义响应；不写响应时补通用 500。
	// 为 nil 时记录默认日志（Panic.Text）并补通用 500。
	RecoverHandler RecoverHandler
}

type Router struct {
	mux         *http.ServeMux
	recoverer   Handler
	middlewares []Handler
	config      Config
	frozen      bool
}

// defaultErrorHandler 是响应错误的唯一入口：分类、日志、写响应都只在这里。
// panic 不经过这里，由内置 Recoverer 直接写 500。
// 想换错误格式，在 New 时传入 Config.ErrorHandler。
func defaultErrorHandler(c *Ctx, err error) {
	// 1. 业务错误：带状态码，直接把状态码和消息写出去。
	// 状态码非法时降级为 500：WriteHeader 在状态码不在 100..999 时会 panic。
	if e, ok := errors.AsType[*Error](err); ok && e != nil {
		status := e.Status
		if status < 200 || status > 599 {
			status = http.StatusInternalServerError
		}
		respond(c, status, map[string]string{"error": e.Msg})
		return
	}

	// 2. 未知错误：记日志，对外只给通用 500，避免泄露内部细节。
	slog.Error(
		"request failed",
		"method", c.R.Method,
		"path", c.R.URL.Path,
		"err", err,
	)

	respond(c, http.StatusInternalServerError, map[string]string{"error": http.StatusText(http.StatusInternalServerError)})
}

// respond 是唯一的 JSON 响应写法，成功与错误路径共用。
// 先编码到内存，编码成功后才提交状态码与响应体：编码失败时响应尚未写出，
// 可以安全降级为通用 500；204/304 按 HTTP 语义不写响应体。
func respond(c *Ctx, status int, v any) {
	// 没有显式状态、也没有可写内容：不写。让 net/http 在收尾时补隐式 200，
	// 这样「处理器自己写完响应、返回 nil」不会被再补一次。
	if status == 0 && isNoOutput(v) {
		return
	}

	if status == 0 {
		status = http.StatusOK
	}

	if status == http.StatusNoContent || status == http.StatusNotModified || isNoOutput(v) {
		c.W.WriteHeader(status)
		return
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		slog.Error("encode response", "path", c.R.URL.Path, "err", err)
		if status != http.StatusInternalServerError {
			respond(c, http.StatusInternalServerError, map[string]string{"error": http.StatusText(http.StatusInternalServerError)})
		}
		return
	}

	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(status)
	if _, err := c.W.Write(buf.Bytes()); err != nil {
		slog.Error("write response", "path", c.R.URL.Path, "err", err)
	}
}

// New 创建 Router，并把 Recoverer 装为第一个中间件。
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
		recoverer:   Recoverer(c.RecoverHandler),
		middlewares: nil,
		config:      c,
	}
}

// Use 注册全局中间件。必须在注册路由 / Run 之前调用；注册首个路由后再调用会 panic。
func (r *Router) Use(h Handler) {
	if r.frozen {
		panic("mux: Use must be called before routes are registered")
	}
	r.middlewares = append(r.middlewares, h)
}

// Get 注册一个 GET 路由。
// 处理器签名统一为 func(c *Ctx, in In) (Out, error)：
//   - In 写 struct{} 表示不读取请求体（跳过解码，坏 body 也不会报 400）
//   - Out 写 struct{} 或返回 nil 表示不写响应体；命名空结构体会正常序列化为 {}
func (r *Router) Get[In, Out any](pattern string, h func(*Ctx, In) (Out, error)) {
	r.register(http.MethodGet, pattern, decode[In](), typed(h))
}

// Post 注册一个 POST 路由，签名规则同 Get。
func (r *Router) Post[In, Out any](pattern string, h func(*Ctx, In) (Out, error)) {
	r.register(http.MethodPost, pattern, decode[In](), typed(h))
}

// decode 把请求体解码成 In，并把结果作为输入传给下一环。
// In=struct{} 时不读请求体；没有 body 或 body 为空时保留零值。
func decode[In any]() Handler {
	return func(c *Ctx, _ any) (any, error) {
		var in In
		if _, ok := any(in).(struct{}); ok || c.R.Body == nil {
			return c.Next(in)
		}

		dec := json.NewDecoder(http.MaxBytesReader(c.W, c.R.Body, maxBodyBytes))
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, NewError(http.StatusRequestEntityTooLarge, "request body too large")
			}
			return nil, NewError(http.StatusBadRequest, "invalid request body")
		}
		return c.Next(in)
	}
}

// typed 把具体类型的处理器适配成链上的 Handler；输入已经由 decode 解好。
func typed[In, Out any](h func(*Ctx, In) (Out, error)) Handler {
	return func(c *Ctx, v any) (any, error) {
		in, ok := v.(In)
		if !ok {
			var zero In
			return nil, NewError(http.StatusInternalServerError, fmt.Sprintf("mux: input type %T does not match %T", v, zero))
		}
		out, err := h(c, in)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

func (r *Router) register(method, pattern string, decode Handler, h Handler) {
	r.frozen = true

	// 链在注册时构造一次，请求期间只读。
	// 顺序：Recoverer → 输入解码 → 用户中间件 → 路由处理器。
	chain := slices.Concat([]Handler{r.recoverer, decode}, r.middlewares, []Handler{h})

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
				respond(c, http.StatusInternalServerError, map[string]string{"error": http.StatusText(http.StatusInternalServerError)})
			}
		}()

		out, err := c.Next(nil)
		// panic 已由 Recoverer 记为 500 并处理完，这里只负责结束请求。
		if errors.Is(err, errRecovered) {
			return
		}
		if err != nil {
			// 客户端已断开：响应没人收，不进入错误处理。
			// 只认 context.Canceled；服务端超时（DeadlineExceeded）仍交给 ErrorHandler。
			if !errors.Is(c.R.Context().Err(), context.Canceled) {
				r.config.ErrorHandler(c, err)
			}
			return
		}

		respond(c, c.status, out)
	}))
}

func isNoOutput(v any) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case struct{}:
		return true
	}
	return false
}

// bufferedResponse 先接住 ServeMux 自己写的响应，用于接管 404/405。
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h, pattern := r.mux.Handler(req)
	if pattern != "" {
		h.ServeHTTP(w, req)
		return
	}

	b := &bufferedResponse{header: make(http.Header)}
	h.ServeHTTP(b, req)

	status := b.status
	if status == 0 {
		status = http.StatusInternalServerError
	}

	// 只接管 404/405；重定向等其它响应原样回放。
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		for k, vv := range b.header {
			w.Header()[k] = vv
		}
		w.WriteHeader(status)
		_, _ = w.Write(b.body.Bytes())
		return
	}

	for k, vv := range b.header {
		if k == "Content-Type" || k == "Content-Length" {
			continue
		}
		w.Header()[k] = vv
	}

	r.config.ErrorHandler(NewCtx(w, req), NewError(status, http.StatusText(status)))
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
