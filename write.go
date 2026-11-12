package mie

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "net/http"
    "time"
)

// ResponseWriter 包装底层 http.ResponseWriter：跟踪状态码与提交状态，并承载 NoMatch 接管。
type ResponseWriter struct {
    w           http.ResponseWriter
    status      int  // 最近设置的状态码；0 表示未设置（默认 200）
    wroteHeader bool // 最终状态是否已写出
    hijacked    bool // 连接是否已交给调用方

    // noMatch 由 Router 在启用 NoMatchHandler 时挂上：ServeMux 自写的 404/405 先交给钩子，
    // 默认错误体不再发给客户端；钩子从 w.status/StatusCode() 读状态码。
    noMatch func()

    // discard 在钩子接管后置位：http.Error 紧随 WriteHeader 的默认文本要被丢弃。
    discard bool
}

// StatusCode 返回最近设置的状态码（1xx 也反映）；未设置且未 Hijack 为 200，Hijack 后为 0。
func (w *ResponseWriter) StatusCode() int {
    if w.status != 0 {
        return w.status
    }

    if w.hijacked {
        return 0
    }

    return http.StatusOK
}

// SetStatus 设置状态码：1xx 立即写出（101 会被 WriteHeader 锁成最终状态），
// 其余码先记下，等写 body/Flush/ReadFrom 再提交；已出最终头或已 Hijack 后忽略。
func (w *ResponseWriter) SetStatus(code int) {
    if w.hijacked || w.wroteHeader {
        return
    }

    if checkStatus(code); code < 200 {
        w.WriteHeader(code)
        return
    }

    w.status = code
}

// Header 返回响应头，可直接修改。
func (w *ResponseWriter) Header() http.Header { return w.w.Header() }

// WriteHeader 写出状态码：101 与 200–999 是最终状态，其余 1xx 不算提交。
// 最终状态只认第一次，但重复调用仍转发给 net/http（由它记噪音日志）；Hijack 后直接返回。
func (w *ResponseWriter) WriteHeader(code int) {
    if w.hijacked {
        return
    }

    // ServeMux 自己写 404/405 时交给钩子：先摘掉钩子再调，避免钩子里 writeError
    // 再写 404/405 时重新进入本分支递归；http.Error 预置的 Content-Type/nosniff
    // 与 405 的 Allow 保留不动，接管后响应头归钩子。
    if f := w.noMatch; f != nil && (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) {
        w.noMatch = nil
        w.status = code
        f()

        w.discard = true
        // 这里不提交：钩子静默时交给 ServeHTTP 的 finish 提交拦截到的 404/405；
        // 钩子写过响应时 commit 自然是 no-op。
        return
    }

    if !w.wroteHeader {
        checkStatus(code)
        if w.status = code; code >= 200 || code == 101 {
            w.wroteHeader = true
        }
    }

    w.w.WriteHeader(code)
}

// Write 写响应体；接管期间丢弃默认错误体。
func (w *ResponseWriter) Write(p []byte) (int, error) {
    if w.discard {
        // http.Error 在 WriteHeader 后还会 Fprintln 一段默认文本，丢弃它。
        return len(p), nil
    }
    return w.track(func() (int, error) { return w.w.Write(p) })
}

// WriteString 写字符串响应体。
func (w *ResponseWriter) WriteString(s string) (int, error) {
    return w.track(func() (int, error) { return io.WriteString(w.w, s) })
}

// ReadFrom 优先用底层 ReaderFrom（sendfile）；否则回退 io.Copy。
// 即使源为空也先提交最终状态（未预设时 200），保证头先于 body 发出。
func (w *ResponseWriter) ReadFrom(src io.Reader) (int64, error) {
    if w.hijacked {
        return 0, http.ErrHijacked
    }

    w.commit()
    if rf, ok := w.w.(io.ReaderFrom); ok {
        return rf.ReadFrom(src)
    }

    return io.Copy(struct{ io.Writer }{w}, src)
}

// Flush 忽略底层错误；要拿到错误请用 FlushError。
func (w *ResponseWriter) Flush() {
    _ = w.FlushError()
}

// FlushError 先提交最终头再 Flush，供 http.ResponseController 使用；
// Hijack 后仍转发（连接已归调用方，Flush 与否由它决定）。
func (w *ResponseWriter) FlushError() error {
    switch f := w.w.(type) {
    case interface{ FlushError() error }:
        w.commit()
        return f.FlushError()
    case http.Flusher:
        w.commit()
        f.Flush()
        return nil
    default:
        return http.ErrNotSupported
    }
}

// Hijack 把连接交给调用方（WebSocket、CONNECT 隧道等）：此后响应不再有 HTTP 语义。
// StatusCode 保持已记录值，未记录时为 0。
func (w *ResponseWriter) Hijack() (conn net.Conn, rw *bufio.ReadWriter, err error) {
    if h, ok := w.w.(http.Hijacker); ok {
        if conn, rw, err = h.Hijack(); err == nil {
            w.hijacked = true
        }
        return
    }
    return nil, nil, http.ErrNotSupported
}

// SetReadDeadline 转发底层连接的读超时。
func (w *ResponseWriter) SetReadDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetReadDeadline(time.Time) error }); ok {
        return d.SetReadDeadline(t)
    }
    return http.ErrNotSupported
}

// SetWriteDeadline 转发底层连接的写超时。
func (w *ResponseWriter) SetWriteDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
        return d.SetWriteDeadline(t)
    }
    return http.ErrNotSupported
}

// EnableFullDuplex 允许 HTTP/1 连接上同时读写（例如边读请求体边写响应）。
func (w *ResponseWriter) EnableFullDuplex() error {
    if d, ok := w.w.(interface{ EnableFullDuplex() error }); ok {
        return d.EnableFullDuplex()
    }
    return http.ErrNotSupported
}

// commit 保证写 body/Flush 前先出最终头（0 或 1xx 补 200）；Hijack 后不再写头。
// 101 已由 WriteHeader 锁成最终状态，不会走到这里。
func (w *ResponseWriter) commit() {
    if w.hijacked || w.wroteHeader {
        return
    }

    code := w.status
    if code < 200 {
        code = http.StatusOK
    }

    w.WriteHeader(code)
}

// track 先提交最终头，再执行写函数。
func (w *ResponseWriter) track(f func() (int, error)) (int, error) {
    if w.hijacked {
        return 0, http.ErrHijacked
    }
    w.commit()
    return f()
}

// checkStatus 在状态码越界（<100 或 >999）时 panic，不做静默纠正。
func checkStatus(code int) {
    if code < 100 || code > 999 {
        panic(fmt.Sprintf("mie: invalid status code %d", code))
    }
}
