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
    status      int   // 最近一次写出的状态码
    size        int64 // 已写入的响应体字节数
    wroteHeader bool
    hijacked    bool
}

// StatusCode 返回最近一次写出的状态码；未写出时返回 200，Hijack 且未写出时返回 0。
func (w *ResponseWriter) StatusCode() int {
    if w.status != 0 {
        return w.status
    }
    if w.hijacked {
        return 0
    }
    return http.StatusOK
}

// Size 返回已写入的响应体字节数，不含响应头；Hijack 后直接写连接的字节不计入。
func (w *ResponseWriter) Size() int64 { return w.size }

func (w *ResponseWriter) Header() http.Header { return w.w.Header() }

// WriteHeader 写出状态码。1xx（101 除外）不算提交；101 与 200–999 是最终状态，写出后不再记录后续状态码。
// 已提交或已 Hijack 后传入非法码时交给底层忽略，与 net/http 一致。
func (w *ResponseWriter) WriteHeader(code int) {
    if !w.hijacked && !w.wroteHeader {
        if code < 100 || code > 999 {
            panic(fmt.Sprintf("mux: invalid status code %d", code))
        }
        if w.status = code; code >= 200 || code == 101 {
            w.wroteHeader = true
        }
    }
    w.w.WriteHeader(code)
}

// trackWrite 记录一次写出的字节数，并把底层写结果原样返回。
func (w *ResponseWriter) trackWrite[T ~int | ~int64](n T, err error) (T, error) {
    w.size += int64(n)
    return n, err
}

func (w *ResponseWriter) Write(p []byte) (int, error) {
    w.commit()
    return w.trackWrite(w.w.Write(p))
}

func (w *ResponseWriter) WriteString(s string) (int, error) {
    w.commit()
    return w.trackWrite(io.WriteString(w.w, s))
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
// 成功后不再有 HTTP 语义：之后直接写连接的字节不计入 Size，StatusCode 也停在 Hijack 前的值。
func (w *ResponseWriter) Hijack() (conn net.Conn, rw *bufio.ReadWriter, err error) {
    if h, ok := w.w.(http.Hijacker); ok {
        if conn, rw, err = h.Hijack(); err == nil {
            w.hijacked = true
        }
        return
    }
    return nil, nil, http.ErrNotSupported
}

// ReadFrom 优先走底层 ReaderFrom（sendfile 等）；否则用 io.Copy。
func (w *ResponseWriter) ReadFrom(src io.Reader) (int64, error) {
    w.commit()
    if rf, ok := w.w.(io.ReaderFrom); ok {
        return w.trackWrite(rf.ReadFrom(src))
    }
    // fallback 的字节数由 Write 累加，这里不能重复加。
    return io.Copy(struct{ io.Writer }{w}, src)
}

// SetReadDeadline 转发给底层连接的读超时；底层不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) SetReadDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetReadDeadline(time.Time) error }); ok {
        return d.SetReadDeadline(t)
    }
    return http.ErrNotSupported
}

// SetWriteDeadline 转发给底层连接的写超时；底层不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) SetWriteDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
        return d.SetWriteDeadline(t)
    }
    return http.ErrNotSupported
}

// EnableFullDuplex 允许 HTTP/1 连接上同时读写（例如边读请求体边写响应）；
// 底层不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) EnableFullDuplex() error {
    if d, ok := w.w.(interface{ EnableFullDuplex() error }); ok {
        return d.EnableFullDuplex()
    }
    return http.ErrNotSupported
}

// commit 在第一次写 body 或 Flush 前补最终 200。
func (w *ResponseWriter) commit() {
    if !w.hijacked && !w.wroteHeader {
        w.WriteHeader(http.StatusOK)
    }
}
