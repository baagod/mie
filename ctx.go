package mux

import (
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "net"
    "net/http"
)

// Handler 是中间件与路由在运行时的统一签名：输入与输出都用 any 传输。
// 路由用泛型方法（Get/Post）注册时，In/Out 会被替换成具体类型。
//
//	func(c *Ctx, in any) (any, error)
type Handler func(c *Ctx, in any) (any, error)

// Ctx 贯穿整条请求链。
type Ctx struct {
    W *ResponseWriter
    R *http.Request

    chain   []Handler
    index   int
    status  int
    traceid string
}

// TraceID 返回本次请求的关联 ID：优先用请求头 X-Request-Id，
// 没有（或过长）时生成本次请求专属的随机 ID。
func (c *Ctx) TraceID() string { return c.traceid }

// RemoteIP 返回客户端 IP：去掉 RemoteAddr 的端口；解析失败时返回原始值。
func (c *Ctx) RemoteIP() string {
    if host, _, err := net.SplitHostPort(c.R.RemoteAddr); err == nil {
        return host
    }
    return c.R.RemoteAddr
}

// Header 读取请求头；头不存在（或值为空）时返回 d 的第一个值，未传则返回空串。
func (c *Ctx) Header(name string, d ...string) string {
    v := c.R.Header.Get(name)
    if v == "" && len(d) > 0 {
        v = d[0]
    }
    return v
}

// SetHeader 设置响应头，例如 c.SetHeader("X-Request-Id", c.TraceID())。
func (c *Ctx) SetHeader(name, value string) { c.W.Header().Set(name, value) }

// NewCtx 创建请求上下文：确定 TraceID、包装 ResponseWriter、回写响应头。
// TraceID 优先复用请求头 X-Request-Id；缺失或超过 maxTraceIDLen 时取 16 字节
// 随机数，编码为 32 位小写十六进制（与 W3C trace-id 格式一致）。
func NewCtx(w http.ResponseWriter, req *http.Request) *Ctx {
    c := &Ctx{R: req, index: -1, traceid: req.Header.Get(HeaderXRequestID)}
    c.W = &ResponseWriter{w: w}

    if c.traceid == "" || len(c.traceid) > maxTraceIDLen {
        var b [16]byte
        _, _ = rand.Read(b[:])
        c.traceid = hex.EncodeToString(b[:])
    }

    c.SetHeader(HeaderXRequestID, c.traceid)
    return c
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
