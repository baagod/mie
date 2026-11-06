package mux

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "net/http"
    "time"
)

// ResponseWriter 转发底层 writer 的 HTTP 写接口与常用可选能力；底层 writer 不可达。
type ResponseWriter struct {
    w           http.ResponseWriter
    status      int   // 最近设置的状态码；0 表示未设置（默认 200）。最终状态码写出后不再更新。
    size        int64 // 已写入的响应体字节数
    wroteHeader bool
    hijacked    bool
}

// StatusCode 返回最近设置的状态码：1xx 也会反映。未设置时：未 Hijack 返回 200，已 Hijack 返回 0。
func (w *ResponseWriter) StatusCode() int {
    if w.status != 0 {
        return w.status
    }

    if w.hijacked {
        return 0
    }

    return http.StatusOK
}

// SetStatus 设置响应状态码：1xx 立即写出（101 由 WriteHeader 锁为最终状态），
// 其余最终码预设，等首次写 body、Flush 或 ReadFrom 前由 commit 提交。
// 已写出最终状态或已 Hijack 后忽略。
func (w *ResponseWriter) SetStatus(code int) {
    if w.hijacked || w.wroteHeader {
        return
    }

    if checkStatusCode(code); code < 200 {
        w.WriteHeader(code)
        return
    }

    w.status = code
}

// Size 返回已写入的响应体字节数，不含响应头；Hijack 后直接写连接的字节不计入。
func (w *ResponseWriter) Size() int64 { return w.size }

func (w *ResponseWriter) Header() http.Header { return w.w.Header() }

// WriteHeader 写出状态码。1xx（101 除外）不算提交；101 与 200–999 是最终状态，写出后不再记录后续状态码。
// 已提交后非法码交给底层忽略；已 Hijack 后直接返回，与 net/http 一致。
func (w *ResponseWriter) WriteHeader(code int) {
    if w.hijacked {
        return
    }

    if !w.wroteHeader {
        checkStatusCode(code)
        if w.status = code; code >= 200 || code == 101 {
            w.wroteHeader = true
        }
    }

    w.w.WriteHeader(code)
}

func (w *ResponseWriter) Write(p []byte) (int, error) {
    return w.track(func() (int, error) { return w.w.Write(p) })
}

func (w *ResponseWriter) WriteString(s string) (int, error) {
    return w.track(func() (int, error) { return io.WriteString(w.w, s) })
}

// ReadFrom 优先走底层 ReaderFrom（sendfile 等）；否则用 io.Copy。
// 视作一次写：即使源为空也先提交最终状态（未预设时 200）。
func (w *ResponseWriter) ReadFrom(src io.Reader) (int64, error) {
    if w.hijacked {
        return 0, http.ErrHijacked
    }

    w.commit()
    if rf, ok := w.w.(io.ReaderFrom); ok {
        n, err := rf.ReadFrom(src)
        w.size += n
        return n, err
    }

    return io.Copy(struct{ io.Writer }{w}, src) // 字节由 Write 记账
}

// Flush 丢弃底层错误；需要错误时用 FlushError。
func (w *ResponseWriter) Flush() {
    _ = w.FlushError()
}

// FlushError 供 http.ResponseController 使用；Flush 前先提交最终头。
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

// Hijack 把底层连接交给调用方（WebSocket、CONNECT 隧道等）。
// 成功后不再有 HTTP 语义：之后直接写连接的字节不计入 Size；未写出状态时 StatusCode 返回 0，
// 已记录的状态码保持原值。
func (w *ResponseWriter) Hijack() (conn net.Conn, rw *bufio.ReadWriter, err error) {
    if h, ok := w.w.(http.Hijacker); ok {
        if conn, rw, err = h.Hijack(); err == nil {
            w.hijacked = true
        }
        return
    }
    return nil, nil, http.ErrNotSupported
}

// SetReadDeadline 转发给底层连接的读超时
func (w *ResponseWriter) SetReadDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetReadDeadline(time.Time) error }); ok {
        return d.SetReadDeadline(t)
    }
    return http.ErrNotSupported
}

// SetWriteDeadline 转发给底层连接的写超时
func (w *ResponseWriter) SetWriteDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
        return d.SetWriteDeadline(t)
    }
    return http.ErrNotSupported
}

// EnableFullDuplex 允许 HTTP/1 连接上同时读写（例如边读请求体边写响应）
func (w *ResponseWriter) EnableFullDuplex() error {
    if d, ok := w.w.(interface{ EnableFullDuplex() error }); ok {
        return d.EnableFullDuplex()
    }
    return http.ErrNotSupported
}

// commit 确保首次写 body/Flush 前已写出最终头；Hijack 后不再写头。
// status 为 0 或 1xx 时补 200；101 已由 WriteHeader 锁为最终状态，不会走到这里。
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

func (w *ResponseWriter) track(f func() (int, error)) (n int, err error) {
    if w.hijacked {
        return 0, http.ErrHijacked
    }
    w.commit()
    n, err = f()
    w.size += int64(n)
    return n, err
}

// checkStatusCode 校验状态码范围；越界立即 panic。
func checkStatusCode(code int) {
    if code < 100 || code > 999 {
        panic(fmt.Sprintf("mux: invalid status code %d", code))
    }
}
