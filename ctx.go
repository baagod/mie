package mux

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "mime"
    "net"
    "net/http"
    "time"
)

// ctxKey 把 Ctx 挂到请求 context 上，供路由闭包取回。
type ctxKey struct{}

// Handler 是链上的统一签名；路由的泛型参数在注册时转成 any。
type Handler func(c *Ctx, in any) (any, error)

type Ctx struct {
    W *ResponseWriter
    R *http.Request

    router  *Router
    chain   []Handler
    index   int
    traceid string
    start   time.Time
}

// newCtx 包装 ResponseWriter，确定 TraceID 并回写响应头。
// TraceID 复用 X-Request-Id；缺失或超过 maxTraceIDLen 时生成 32 位十六进制随机值。
func newCtx(w http.ResponseWriter, req *http.Request, r *Router) *Ctx {
    c := &Ctx{
        index:   -1,
        traceid: req.Header.Get(HeaderXRequestID),
        start:   time.Now(),
        router:  r,
    }

    // 把 Ctx 塞回请求：路由闭包从 context 取回同一个实例。
    // Pattern 与 PathValue 由 ServeMux 写在这个副本上，未命中时 Pattern 为空。
    c.R = req.WithContext(context.WithValue(req.Context(), ctxKey{}, c))
    c.W = &ResponseWriter{w: w}

    if c.traceid == "" || len(c.traceid) > 128 {
        var b [16]byte
        _, _ = rand.Read(b[:])
        c.traceid = hex.EncodeToString(b[:])
    }

    c.Header(HeaderXRequestID, c.traceid)
    return c
}

func (c *Ctx) TraceID() string { return c.traceid }

// RemoteIP 从 RemoteAddr 取 host；解析失败时原样返回。
func (c *Ctx) RemoteIP() string {
    if host, _, err := net.SplitHostPort(c.R.RemoteAddr); err == nil {
        return host
    }
    return c.R.RemoteAddr
}

// GetHeader 读取请求头；为空时返回第一个默认值，没有默认值则返回空串。
func (c *Ctx) GetHeader(name string, or ...string) string {
    h := c.R.Header.Get(name)
    if h == "" && len(or) > 0 {
        h = or[0]
    }
    return h
}

// Header 设置响应头，返回 Ctx 便于链式调用
func (c *Ctx) Header(name, value string) *Ctx {
    c.W.Header().Set(name, value)
    return c
}

// Type 设置响应头的内容类型，返回 Ctx 便于链式调用
func (c *Ctx) Type(value string) *Ctx {
    c.W.Header().Set(HeaderContentType, value)
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

func (c *Ctx) Deadline() (time.Time, bool) { return c.R.Context().Deadline() }
func (c *Ctx) Done() <-chan struct{}       { return c.R.Context().Done() }
func (c *Ctx) Err() error                  { return c.R.Context().Err() }
func (c *Ctx) Value(key any) any           { return c.R.Context().Value(key) }
func (c *Ctx) IsCanceled() bool            { return errors.Is(c.R.Context().Err(), context.Canceled) }

// finish 是请求的统一收尾：写错误响应（writeError）、提交预设状态、记一条访问日志。
// 取消导致的错误不写响应；handlePanic 的中止路径不经 finish。
func (c *Ctx) finish(err error) {
    if err != nil && !c.IsCanceled() {
        c.writeError(err)
    }
    c.W.commit()
    c.logAccess(err)
}

// writeError 是所有错误响应的唯一出口。
// 错误文本会原样写给客户端：内部细节放日志，不要放进 error 文本。
func (c *Ctx) writeError(err error) {
    if err == nil {
        return
    }

    c.index = len(c.chain)
    if c.W.hijacked {
        c.LogWarn("mux: error after hijack", "error", err)
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

    if c.W.wroteHeader {
        c.LogWarn("mux: error after response committed", "error", err)
    } else {
        c.W.WriteHeader(status)
    }

    if _, e := c.W.WriteString(err.Error()); e != nil {
        c.LogWarn("mux: write error response", "error", e)
    }
}
