package mux

import (
    "crypto/rand"
    "encoding/hex"
    "errors"
    "log/slog"
    "mime"
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

// writeError 是所有错误响应的唯一出口。
func (c *Ctx) writeError(err error) {
    if err == nil {
        return
    }

    if c.index = len(c.chain); c.W.hijacked {
        slog.Warn("mux: error after hijack", "err", err)
        return
    }

    status := http.StatusInternalServerError
    if e, ok := errors.AsType[*Error](err); ok && e != nil {
        status = e.Status
    }

    h := c.W.Header()
    if ct := h.Get(HeaderContentType); ct == "" {
        // 框架自己选类型：text/plain，配套 nosniff
        h.Set(HeaderContentType, MIMETextPlainCharsetUTF8)
        h.Set(HeaderXContentTypeOptions, ContentTypeOptionsNosniff)
    } else if t, _, e := mime.ParseMediaType(ct); e == nil && t == "text/plain" {
        // 用户声明了 text/plain：这是唯一会被嗅探成 HTML 的类型，补 nosniff
        if h.Get(HeaderXContentTypeOptions) == "" {
            h.Set(HeaderXContentTypeOptions, ContentTypeOptionsNosniff)
        }
    }

    c.W.WriteHeader(status)
    if _, e := c.W.WriteString(err.Error()); e != nil {
        slog.Warn("mux: write error response", "err", e)
    }
}
