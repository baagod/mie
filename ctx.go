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
    "reflect"
    "time"

    "github.com/baagod/mie/internal/bind"
)

type (
    // ctxKey 是请求 context 中存放 *Ctx 的键
    ctxKey struct{}
    // bodyKey 是 store 中缓存 Body 读取结果的键
    bodyKey struct{}
    // bindKey 是 store 中缓存已绑定 IN 的键；按 IN 类型分别缓存
    bindKey[IN any] struct{}
)

// reqBody 缓存 Body 的读取结果：字节与错误一起保存，保证重复调用语义一致。
type reqBody struct {
    b   []byte
    err error
}

// Ctx 是一次请求的上下文，包装响应写入器与请求，并承载中间件游标。
// Ctx 只在处理本次请求的 goroutine 内使用，不要跨 goroutine 共享。
type Ctx struct {
    W *ResponseWriter // 响应写入器
    R *http.Request   // 带 Ctx 的请求副本

    app     *App
    chain   []Handler
    index   int
    traceid string
    start   time.Time
    store   map[any]any // 请求级键值：Body/Bind 缓存与 Set 写入的值
}

// newCtx 创建请求上下文，并把 *Ctx 放进请求 context。
// TraceID 复用合法的 X-Request-Id（不超过 128 字节），否则生成 32 位十六进制随机值。
func newCtx(w http.ResponseWriter, req *http.Request, a *App) *Ctx {
    c := &Ctx{
        index:   -1,
        store:   map[any]any{},
        traceid: req.Header.Get(HeaderXRequestID),
        start:   time.Now(),
        app:     a,
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
    if v, ok := c.store[bodyKey{}]; ok {
        rb := v.(reqBody)
        return rb.b, rb.err
    }

    if b, err = io.ReadAll(c.R.Body); err != nil {
        if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
            b, err = nil, Status413RequestEntityTooLarge("mie: request body too large")
        }
    }

    c.Set(bodyKey{}, reqBody{b: b, err: err})
    return
}

// Set 把请求级键值写入 Ctx 的容器，不替换 c.R；c.R.Context() 里读不到这些值。
func (c *Ctx) Set(key, value any) *Ctx {
    c.store[key] = value
    return c
}

// Get 读取 Set 存入的 key 值；未设置或类型不符时返回零值。
func (c *Ctx) Get[T any](key any) T {
    v, _ := c.Value(key).(T)
    return v
}

// Deadline 转发请求 context 的截止时间。
func (c *Ctx) Deadline() (time.Time, bool) { return c.R.Context().Deadline() }

// Done 返回请求 context 的取消信号。
func (c *Ctx) Done() <-chan struct{} { return c.R.Context().Done() }

// Err 返回请求 context 的错误。
func (c *Ctx) Err() error { return c.R.Context().Err() }

// Value 先读 Ctx 的容器，再回退到请求 context；实现 context.Context 契约。
func (c *Ctx) Value(key any) any {
    if v, ok := c.store[key]; ok {
        return v
    }
    return c.R.Context().Value(key)
}

// IsCanceled 判断请求是否因客户端取消而结束；不覆盖 deadline 超时。
func (c *Ctx) IsCanceled() bool { return errors.Is(c.R.Context().Err(), context.Canceled) }

// Param 返回路径参数的值。
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

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

// HeaderGet 读取请求头；为空时返回第一个默认值。
func (c *Ctx) HeaderGet(name string, or ...string) string {
    h := c.R.Header.Get(name)
    if h == "" && len(or) > 0 {
        h = or[0]
    }
    return h
}

// Type 设置 Content-Type 响应头。
func (c *Ctx) Type(value string) *Ctx {
    c.W.Header().Set(HeaderContentType, value)
    return c
}

// ContentType 返回请求头的内容类型
func (c *Ctx) ContentType() string {
    return c.HeaderGet(HeaderContentType)
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

// Send 输出 v：Formatted 显式声明格式，必须有对应编码器；处理器事先设了 Content-Type 时以它为准；
// 裸值按 Accept 从已注册格式里选（默认 JSON，可用 Codecs.Order 改），没有编码器时 string/[]byte 按原字节写出。
// 无法满足时 406；提交由请求收尾负责，返回的错误交给 ErrorHandler。
func (c *Ctx) Send(v any) error {
    if v == nil {
        return nil // 无 body：收尾时由 finish 按 c.Status 提交
    }

    if r, ok := v.(Formatted); ok {
        media, value := r.Format()
        return c.sendFormat(value, media, false) // 显式格式覆盖 Accept，不设 Vary。
    }

    // 处理器自己设了响应 Content-Type：它就是声明的格式，不再按 Accept 协商。
    media := c.W.Header().Get(HeaderContentType)
    if media == "" {
        // 要在任何 return 之前设：406 也要带，否则缓存会把一次 406 当成对所有 Accept 都成立。
        c.W.Header().Add(HeaderVary, HeaderAccept)
        if media = negotiate(c.HeaderGet(HeaderAccept), mediaOf(v), c.app.config.Codecs.parsed); media == "" {
            return Status406NotAcceptable() // 唯一表示「没有可满足类型」的信号
        }
    }

    return c.sendFormat(v, media, true)
}

// Bind 绑定 IN：先按 Content-Type 解码 body（权重最低），再由位置来源覆盖；同一请求同一 IN 类型只绑一次。
// 绑定失败时 err 非 nil，in 可能已被部分字段写入，调用方应以 err 为准并丢弃 in。
func (c *Ctx) Bind[IN any](in *IN) (err error) {
    if v, ok := c.store[bindKey[IN]{}]; ok {
        *in = v.(IN)
        return
    }

    if err = c.BindBody(in); err != nil {
        return err
    }

    if err = bind.Decode(c.R, in); err != nil {
        if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
            return Status413RequestEntityTooLarge()
        }
        return Status400BadRequest(err.Error())
    }

    c.Set(bindKey[IN]{}, *in)
    return
}

// BindBody 按 Content-Type 解码 body；GET/HEAD 不读 body。
func (c *Ctx) BindBody[IN any](in *IN) (err error) {
    if c.R.Method == http.MethodGet || c.R.Method == http.MethodHead || c.R.ContentLength == 0 {
        return
    }

    ct := c.ContentType()
    if ct == "" {
        return Status415UnsupportedMediaType()
    }

    ct, _, _ = mime.ParseMediaType(ct)
    if ct == MIMEFormUrlencoded || ct == MIMEMultipartForm {
        return
    }

    dec, ok := c.app.config.Codecs.decoder(ct)
    if !ok {
        return Status415UnsupportedMediaType()
    }

    var b []byte
    if b, err = c.Body(); err != nil {
        return fmt.Errorf("bind body: %w", err)
    }

    // chunked 请求的 ContentLength 是 -1，早退判断拦不住零字节 body；
    // 读完为空视同无 body，不交给解码器。
    if len(b) == 0 {
        return
    }

    // IN 为指针时 in 是 **T，而解码器只该收到可写的 *T：先分配内层指针再脱一层；
    // 值类型 IN 的 in 已是 *T，原样传入。
    var target any = in
    if rv := reflect.ValueOf(in).Elem(); rv.Kind() == reflect.Pointer {
        if rv.IsNil() {
            rv.Set(reflect.New(rv.Type().Elem()))
        }
        target = rv.Interface()
    }

    if err = dec(b, target); err != nil {
        if _, ok = errors.AsType[*Error](err); !ok {
            return Status400BadRequest("bind body: " + err.Error())
        }
    }

    return
}

// Next 把游标推进到下一个链元素并调用它；越过链尾后再调用返回错误。
func (c *Ctx) Next() error {
    c.index++
    if c.index >= len(c.chain) {
        return errors.New("mie: Next called after the end of the chain")
    }
    return c.chain[c.index](c)
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

// sendFormat 用 media 的编码器编码 v；没有编码器时，raw 允许 string/[]byte 原样写出，其余报错。
func (c *Ctx) sendFormat(v any, media string, raw bool) error {
    if enc, ok := c.app.config.Codecs.encoder(media); ok {
        b, err := enc(v)
        if err == nil {
            _, err = c.Type(media).W.Write(b)
        }
        return err
    }

    if raw {
        switch x := v.(type) {
        case string:
            return c.Type(media).writeString(x)
        case []byte:
            return c.Type(media).writeBytes(x)
        }
    }

    return errors.New("mie: no encoder for " + media)
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

// ======== Ctx 辅助方法 ========

func (c *Ctx) writeBytes(b []byte) (err error) {
    _, err = c.W.Write(b)
    return
}

func (c *Ctx) writeString(s string) (err error) {
    _, err = c.W.WriteString(s)
    return
}
