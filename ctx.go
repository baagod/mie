package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	c.wrote = true
	return json.NewEncoder(c.Writer).Encode(v)
}

// String 写纯文本响应。
func (c *Ctx) String(status int, s string) error {
	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.WriteHeader(status)
	c.wrote = true
	_, err := c.Writer.Write([]byte(s))
	return err
}

// HTTPError 让 handler 返回一个带状态码的错误。
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }

func NewHTTPError(status int, msg string) *HTTPError {
	return &HTTPError{Status: status, Msg: msg}
}

// defaultErrorHandler 是错误处理的唯一入口：分类、日志、写响应都只在这里。
// 想换 JSON 错误体 / 业务错误码映射，用 Router.OnError 整体替换。
func defaultErrorHandler(c *Ctx, err error) {
	// 已经写过响应（例如流式输出到一半出错）：只能记日志，不能再写头。
	if c.wrote {
		slog.Error("error after response written", "path", c.Request.URL.Path, "err", err)
		return
	}

	// 1. 业务错误：带状态码，直接把状态码和消息写出去。
	var he *HTTPError
	if errors.As(err, &he) {
		c.wrote = true
		http.Error(c.Writer, he.Msg, he.Status)
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
	c.wrote = true
	http.Error(c.Writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
