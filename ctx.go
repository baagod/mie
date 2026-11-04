package mux

import (
	"encoding/json"
	"net/http"
)

const (
	GET  = "GET"
	POST = "POST"
)

// Handler 是路由处理函数与中间件的统一签名。
// 中间件也是 Handler：通过 c.Next() 进入下游。
type Handler func(c *Ctx) error

// Ctx 贯穿整条请求链，路由与中间件共享同一份上下文。
type Ctx struct {
	Writer  http.ResponseWriter
	Request *http.Request

	chain []Handler
	index int
	wrote bool // 是否已经写过响应头
}

// responseWriter 记录“是否已经写过响应”，让 handler 直接写 c.Writer 也能被
// 错误漏斗看见。Unwrap 让 http.ResponseController 能取回底层 ResponseWriter。
type responseWriter struct {
	http.ResponseWriter
	c *Ctx
}

func (w *responseWriter) WriteHeader(code int) {
	w.c.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	w.c.wrote = true
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Next 执行链中的下一个 Handler，返回它的错误。
func (c *Ctx) Next() error {
	c.index++
	if c.index >= len(c.chain) {
		return nil
	}
	return c.chain[c.index](c)
}

// Param 读取路径参数（Go 1.22+ 路由模式里的 {name}）。
func (c *Ctx) Param(name string) string { return c.Request.PathValue(name) }

// JSON 写 JSON 响应。
func (c *Ctx) JSON(status int, v any) error {
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(status)
	return json.NewEncoder(c.Writer).Encode(v)
}

// String 写纯文本响应。
func (c *Ctx) String(status int, s string) error {
	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.WriteHeader(status)
	_, err := c.Writer.Write([]byte(s))
	return err
}
