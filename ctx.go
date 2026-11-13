package mie

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "mime"
    "net"
    "net/http"
    "time"

    "github.com/baagod/mie/bind"
)

type (
    // ctxKey 是请求 context 中存放 *Ctx 的键。
    ctxKey struct{}
    // bodyKey 是请求 context 中缓存 Body 读取结果的键。
    bodyKey struct{}

    bindInKey struct{}
)

// reqBody 缓存 Body 的读取结果：字节与错误一起保存，保证重复调用语义一致。
type reqBody struct {
    b   []byte
    err error
}

// Handler 是框架内部执行的处理器形状：链元素与中间件。
type Handler func(c *Ctx) error

// HandlerFunc 是注册口使用的处理器形状：IN 由请求绑定，OUT 渲染为响应。
type HandlerFunc[IN, OUT any] func(c *Ctx, in IN) (OUT, error)

// Ctx 是一次请求的上下文，包装响应写入器与请求，并承载中间件游标。
type Ctx struct {
    W *ResponseWriter // 响应写入器
    R *http.Request   // 带 Ctx 的请求副本

    router  *Router
    chain   []Handler
    index   int
    traceid string
    start   time.Time
}

// newCtx 创建请求上下文，并把 *Ctx 放进请求 context。
// TraceID 复用合法的 X-Request-Id（不超过 128 字节），否则生成 32 位十六进制随机值。
func newCtx(w http.ResponseWriter, req *http.Request, r *Router) *Ctx {
    c := &Ctx{
        index:   -1,
        traceid: req.Header.Get(HeaderXRequestID),
        start:   time.Now(),
        router:  r,
    }

    // 路由闭包靠 context 取回 Ctx；ServeMux 把 Pattern/PathValue 写进请求副本，
    // 所以必须用 WithContext 的副本。
    c.R = req.WithContext(context.WithValue(req.Context(), ctxKey{}, c))
    c.W = &ResponseWriter{w: w}

    if c.traceid == "" || len(c.traceid) > 128 {
        var b [16]byte
        _, _ = rand.Read(b[:])
        c.traceid = hex.EncodeToString(b[:])
    }

    return c
}

// Body 返回请求体：首次调用读取并缓存，后续调用返回同一份结果（包括读取错误）。
func (c *Ctx) Body() (b []byte, err error) {
    if cached, ok := c.Value(bodyKey{}).(reqBody); ok {
        return cached.b, cached.err
    }

    if b, err = io.ReadAll(c.R.Body); err != nil {
        if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
            b, err = nil, Status413RequestEntityTooLarge("mie: request body too large")
        }
    }

    c.Set(bodyKey{}, reqBody{b: b, err: err})
    return
}

// Set 把请求级键值写入请求 context；之后用 c.Value 读取。
func (c *Ctx) Set(key, value any) *Ctx {
    c.R = c.R.WithContext(context.WithValue(c.R.Context(), key, value))
    return c
}

// Deadline 转发请求 context 的截止时间。
func (c *Ctx) Deadline() (time.Time, bool) { return c.R.Context().Deadline() }

// Done 返回请求 context 的取消信号。
func (c *Ctx) Done() <-chan struct{} { return c.R.Context().Done() }

// Err 返回请求 context 的错误。
func (c *Ctx) Err() error { return c.R.Context().Err() }

// Value 读取请求 context 中的值。
func (c *Ctx) Value(key any) any { return c.R.Context().Value(key) }

// IsCanceled 判断请求是否因客户端取消而结束；不覆盖 deadline 超时。
func (c *Ctx) IsCanceled() bool { return errors.Is(c.R.Context().Err(), context.Canceled) }

// Param 返回路径参数的值。
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// GetHeader 读取请求头；为空时返回第一个默认值。
func (c *Ctx) GetHeader(name string, or ...string) string {
    h := c.R.Header.Get(name)
    if h == "" && len(or) > 0 {
        h = or[0]
    }
    return h
}

// ContentType 返回请求头的内容类型
func (c *Ctx) ContentType() string {
    return c.GetHeader(HeaderContentType)
}

// RemoteIP 取 RemoteAddr 的主机部分；解析失败时返回原值。
func (c *Ctx) RemoteIP() string {
    if host, _, err := net.SplitHostPort(c.R.RemoteAddr); err == nil {
        return host
    }
    return c.R.RemoteAddr
}

// TraceID 返回请求的追踪 ID：复用 X-Request-Id 或自动生成。
func (c *Ctx) TraceID() string { return c.traceid }

// Header 设置响应头，返回 Ctx 以便链式调用。
func (c *Ctx) Header(name, value string) *Ctx {
    c.W.Header().Set(name, value)
    return c
}

// Type 设置 Content-Type 响应头。
func (c *Ctx) Type(value string) *Ctx {
    c.W.Header().Set(HeaderContentType, value)
    return c
}

// Status 预设响应状态码，返回 Ctx 以便链式调用。
// 1xx 立即写出；其余状态码在首次写 body 或请求收尾时提交。
func (c *Ctx) Status(code int) *Ctx {
    c.W.SetStatus(code)
    return c
}

// StatusCode 返回将要写出的状态码；未设置且未 Hijack 时为 200，Hijack 后为 0。
func (c *Ctx) StatusCode() int {
    return c.W.StatusCode()
}

// Send 渲染 r；提交由请求收尾统一负责：写 body 时已提交，空 body 时 finish 提交 c.Status 预设状态。
// 返回的错误应作为 handler 返回值，交给 ErrorHandler/writeError 处理。
func (c *Ctx) Send(r Renderer) error {
    return r.Render(c)
}

// Next 把游标推进到下一个链元素并调用它；越过链尾后再调用返回错误。
func (c *Ctx) Next() error {
    c.index++
    if c.index >= len(c.chain) {
        return errors.New("mie: Next called after the end of the chain")
    }
    return c.chain[c.index](c)
}

// Bind 绑定 IN：先位置来源，再按 Content-Type 解码 body 覆盖；同一请求只绑一次。
func (c *Ctx) Bind[IN any](in *IN) (err error) {
    if v := c.Value(bindInKey{}); v != nil {
        cached, ok := v.(IN)
        if !ok {
            return errors.New("mie: bound input type mismatch")
        }
        *in = cached
        return
    }

    if err = bind.Decode(c.R, in); err != nil {
        return Status400BadRequest(err.Error())
    }

    if err = c.BindBody(in); err != nil {
        return err
    }

    c.Set(bindInKey{}, *in)
    return
}

// BindBody 按 Content-Type 解码 body；GET/HEAD 不读 body。
func (c *Ctx) BindBody[IN any](in *IN) (err error) {
    if c.R.Method == http.MethodGet || c.R.Method == http.MethodHead {
        return
    }

    ct, _, _ := mime.ParseMediaType(c.ContentType())
    if dec, ok := bind.Lookup(ct); ok {
        var b []byte
        if b, err = c.Body(); err != nil {
            return fmt.Errorf("bind body: %w", err)
        }
        if len(b) > 0 {
            err = dec.Decode(b, in)
        }
    }

    if err != nil {
        if _, ok := errors.AsType[*Error](err); !ok {
            return Status400BadRequest("bind body: " + err.Error())
        }
    }

    return
}

// finish 是请求的统一收尾：写错误响应、提交预设状态、记一条访问日志。
// 取消导致的错误不写响应；handlePanic 的中止路径不经过 finish。
func (c *Ctx) finish(err error) {
    if err != nil && !c.IsCanceled() {
        c.writeError(err)
    }
    c.W.commit()
    c.logAccess(err)
}

// writeError 是所有错误响应的唯一出口；错误文本会原样写给客户端，内部细节不要放进 error。
func (c *Ctx) writeError(err error) {
    if err == nil {
        return
    }

    c.index = len(c.chain)
    if c.W.hijacked {
        c.LogWarn("mie: error after hijack", "error", err)
        return
    }

    status := http.StatusInternalServerError
    if e, ok := errors.AsType[*Error](err); ok && e != nil {
        status = e.Status
    }

    h := c.W.Header()
    if ct := h.Get(HeaderContentType); ct == "" {
        // 没有 Content-Type 时选 text/plain，并加 nosniff，避免错误文本被当成 HTML 渲染。
        h.Set(HeaderContentType, MIMETextPlainUTF8)
        h.Set(HeaderXContentTypeOptions, MIMEContentTypeNosniff)
    } else if t, _, e := mime.ParseMediaType(ct); e == nil && t == "text/plain" {
        // 只有 text/plain 会被嗅探成 HTML；用户已声明该类型时补 nosniff。
        if h.Get(HeaderXContentTypeOptions) == "" {
            h.Set(HeaderXContentTypeOptions, MIMEContentTypeNosniff)
        }
    }

    if c.W.wroteHeader {
        c.LogWarn("mie: error after response committed", "error", err)
    } else {
        c.W.WriteHeader(status)
    }

    if _, e := c.W.WriteString(err.Error()); e != nil {
        c.LogWarn("mie: write error response", "error", e)
    }
}
