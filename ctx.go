package mux

import (
    "crypto/rand"
    "encoding/hex"
    "errors"
    "net"
    "net/http"
)

// Handler 是链上的统一签名；路由的泛型参数在注册时转成 any。
type Handler func(c *Ctx, in any) (any, error)

type Ctx struct {
    W *ResponseWriter
    R *http.Request

    chain   []Handler
    index   int
    traceid string
}

func (c *Ctx) TraceID() string { return c.traceid }

// RemoteIP 从 RemoteAddr 取 host；解析失败时原样返回。
func (c *Ctx) RemoteIP() string {
    if host, _, err := net.SplitHostPort(c.R.RemoteAddr); err == nil {
        return host
    }
    return c.R.RemoteAddr
}

// Header 读取请求头；为空时返回第一个默认值，没有默认值则返回空串。
func (c *Ctx) Header(name string, v ...string) string {
    h := c.R.Header.Get(name)
    if h == "" && len(v) > 0 {
        h = v[0]
    }
    return h
}

// SetHeader 设置响应头。
func (c *Ctx) SetHeader(name, value string) {
    c.W.Header().Set(name, value)
}

// NewCtx 包装 ResponseWriter，确定 TraceID 并回写响应头。
// TraceID 复用 X-Request-Id；缺失或超过 maxTraceIDLen 时生成 32 位十六进制随机值。
func NewCtx(w http.ResponseWriter, req *http.Request) *Ctx {
    c := &Ctx{R: req, index: -1, traceid: req.Header.Get(HeaderXRequestID)}
    c.W = &ResponseWriter{w: w}

    if c.traceid == "" || len(c.traceid) > 128 {
        var b [16]byte
        _, _ = rand.Read(b[:])
        c.traceid = hex.EncodeToString(b[:])
    }

    c.SetHeader(HeaderXRequestID, c.traceid)
    return c
}

func (c *Ctx) Next(in any) (any, error) {
    c.index++
    if c.index >= len(c.chain) {
        return nil, errors.New("mux: Next called after the end of the chain")
    }
    return c.chain[c.index](c, in)
}

// Status 设置响应状态码，返回 Ctx 以便链式调用。
// 1xx 由 SetStatus 立即写出；其余状态码预设，等首次写 body 或链尾提交。
func (c *Ctx) Status(code int) *Ctx {
    c.W.SetStatus(code)
    return c
}

func (c *Ctx) StatusCode() int {
    return c.W.StatusCode()
}

// Size 返回已写入的响应体字节数，不含响应头。
func (c *Ctx) Size() int64 {
    return c.W.Size()
}

func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }
