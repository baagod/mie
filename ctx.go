package mux

import (
	"errors"
	"fmt"
	"net/http"
)

// Handler 是中间件与路由在运行时的统一签名：输入与输出都用 any 传输。
// 路由用泛型方法（Get/Post）注册时，In/Out 会被替换成具体类型。
//
//	func(c *Ctx, in any) (any, error)
type Handler func(c *Ctx, in any) (any, error)

// Ctx 贯穿整条请求链。
type Ctx struct {
	W http.ResponseWriter
	R *http.Request

	chain  []Handler
	index  int
	status int
	wrote  bool
}

// Next 执行链上的下一个 Handler，并把输入原样传下去。
func (c *Ctx) Next(in any) (any, error) {
	c.index++
	if c.index >= len(c.chain) {
		return nil, errors.New("mux: Next called after the end of the chain")
	}
	return c.chain[c.index](c, in)
}

// Status 覆盖默认的 200。非法状态码立即 panic，由 recoverer 转成 error，
// 避免走到 WriteHeader 时才在链外 panic。
func (c *Ctx) Status(code int) {
	if code < 200 || code > 599 {
		panic(fmt.Sprintf("mux: invalid status code %d", code))
	}
	c.status = code
}

// Param 读取路径参数（Go 1.22+ 路由模式里的 {name}）。
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// committedWriter 记录响应是否已经提交：处理器直接写 W 后再返回 error 时，
// 错误漏斗据此跳过第二次写，避免状态码与 body 被拼坏。
type committedWriter struct {
	http.ResponseWriter
	c *Ctx
}

func (w *committedWriter) WriteHeader(code int) {
	w.c.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *committedWriter) Write(p []byte) (int, error) {
	w.c.wrote = true
	return w.ResponseWriter.Write(p)
}

func (w *committedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *committedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
