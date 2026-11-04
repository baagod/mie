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
	// panic 不经过这里：内置 Recoverer 直接写 500。
	// ServeMux 直接返回的 404/405 由 Router 接管后也走本函数。
	ErrorHandler func(c *Ctx, err error)
}

type Router struct {
	mux         *http.ServeMux
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
		if status < 100 || status > 999 {
			status = http.StatusInternalServerError
		}
		writeErrorJSON(c, status, e.Msg)
		return
	}

	// 2. 未知错误：记日志，对外只给通用 500，避免泄露内部细节。
	slog.Error(
		"request failed",
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"err", err,
	)

	writeErrorJSON(c, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
}

func writeErrorJSON(c *Ctx, status int, msg string) {
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(status)
	if err := json.NewEncoder(c.Writer).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("write error response", "path", c.Request.URL.Path, "err", err)
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
		middlewares: []Handler{Recoverer()},
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
//   - 不需要输入时 In 写 struct{}
//   - 不需要响应体时 Out 写 struct{}（或返回 nil）
func (r *Router) Get[In, Out any](pattern string, h func(*Ctx, In) (Out, error)) {
	decode, handler := typed(h)
	r.register(GET, pattern, decode, handler)
}

// Post 注册一个 POST 路由，签名规则同 Get。
func (r *Router) Post[In, Out any](pattern string, h func(*Ctx, In) (Out, error)) {
	decode, handler := typed(h)
	r.register(POST, pattern, decode, handler)
}

// typed 把具体类型的处理器适配成运行时的 Handler，并生成输入解码函数。
func typed[In, Out any](h func(*Ctx, In) (Out, error)) (func(*Ctx) (any, error), Handler) {
	decode := func(c *Ctx) (any, error) {
		var in In
		if _, ok := any(in).(struct{}); ok {
			return in, nil
		}
		if c.Request.Body == nil {
			return in, nil
		}

		dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
		if err := dec.Decode(&in); err != nil {
			if errors.Is(err, io.EOF) {
				return in, nil
			}
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, NewError(http.StatusRequestEntityTooLarge, "request body too large")
			}
			return nil, err
		}
		return in, nil
	}

	run := func(c *Ctx, v any) (any, error) {
		var zero In
		in, ok := v.(In)
		if !ok {
			return nil, fmt.Errorf("mux: input type %T does not match %T", v, zero)
		}
		out, err := h(c, in)
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	return decode, run
}

func (r *Router) register(method, pattern string, decode func(*Ctx) (any, error), h Handler) {
	r.frozen = true

	// 链在注册时构造一次，请求期间只读。Recoverer 已在 New 里放进 r.middlewares。
	chain := slices.Concat(r.middlewares, []Handler{h})

	r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c := &Ctx{
			Writer:  w,
			Request: req,
			index:   -1,
			chain:   chain,
		}

		in, err := decode(c)
		if err != nil {
			if _, ok := errors.AsType[*Error](err); ok {
				r.config.ErrorHandler(c, err)
			} else {
				r.config.ErrorHandler(c, NewError(http.StatusBadRequest, "invalid request body"))
			}
			return
		}

		out, err := c.Next(in)
		// panic 已由 Recoverer 记为 500 并处理完，这里只负责结束请求。
		if errors.Is(err, errRecovered) {
			return
		}
		if err != nil {
			// 客户端已断开：响应没人收，不进入错误处理。
			// 只认 context.Canceled；服务端超时（DeadlineExceeded）仍交给 ErrorHandler。
			if !errors.Is(c.Request.Context().Err(), context.Canceled) {
				r.config.ErrorHandler(c, err)
			}
			return
		}

		r.write(c, out)
	}))
}

// write 在整条链成功返回后统一写响应，因此处理器无法“响应后再出错”。
// 先编码到内存再提交，编码失败时响应尚未写出，可以安全交给错误漏斗。
func (r *Router) write(c *Ctx, out any) {
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}

	// 204/304 按 HTTP 语义不允许响应体，忽略返回值。
	if status == http.StatusNoContent || status == http.StatusNotModified {
		c.Writer.WriteHeader(status)
		return
	}

	if isNoOutput(out) {
		c.Writer.WriteHeader(status)
		return
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(out); err != nil {
		r.config.ErrorHandler(c, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(status)
	if _, err := c.Writer.Write(buf.Bytes()); err != nil {
		slog.Error("write response", "path", c.Request.URL.Path, "err", err)
	}
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

	r.config.ErrorHandler(&Ctx{Writer: w, Request: req, index: -1}, NewError(status, http.StatusText(status)))
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
