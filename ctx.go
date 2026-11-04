package mux

import (
	"errors"
	"fmt"
	"net/http"
)

const (
	GET  = "GET"
	POST = "POST"
)

// Handler 是中间件与路由在运行时的统一签名：输入与输出都用 any 传输。
// 路由用泛型方法（Get/Post）注册时，In/Out 会被替换成具体类型。
//
//	func(c *Ctx, in any) (any, error)
type Handler func(c *Ctx, in any) (any, error)

// Ctx 贯穿整条请求链。
type Ctx struct {
	Writer  http.ResponseWriter
	Request *http.Request

	chain  []Handler
	index  int
	status int
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
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("mux: invalid status code %d", code))
	}
	c.status = code
}

// Param 读取路径参数（Go 1.22+ 路由模式里的 {name}）。
func (c *Ctx) Param(name string) string { return c.Request.PathValue(name) }
