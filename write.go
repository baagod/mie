package mux

import (
    "bufio"
    "io"
    "net"
    "net/http"
    "time"
)

// ResponseWriter 是处理器可用的响应写入器：只暴露 Header、Write、WriteHeader，
// 底层 http.ResponseWriter 不可达。自己用它写响应，就等于由处理器负责这次响应；
// 这种情况下处理器应返回 nil，让链尾不再写。
// 同时转发底层常见能力：Flush、Hijack、ReadFrom、读写 deadline、全双工。
type ResponseWriter struct {
    w http.ResponseWriter
}

func (w *ResponseWriter) Header() http.Header { return w.w.Header() }

func (w *ResponseWriter) WriteHeader(code int) { w.w.WriteHeader(code) }

func (w *ResponseWriter) Write(p []byte) (int, error) { return w.w.Write(p) }

// Flush 把缓冲的响应发给客户端；底层不支持时什么也不做。
func (w *ResponseWriter) Flush() { _ = w.FlushError() }

// FlushError 是 Flush 的带错误版本，http.ResponseController 优先使用它。
func (w *ResponseWriter) FlushError() error {
    switch f := w.w.(type) {
    case interface{ FlushError() error }:
        return f.FlushError()
    case http.Flusher:
        f.Flush()
        return nil
    default:
        return http.ErrNotSupported
    }
}

// Hijack 接管底层连接，交给调用方自行读写（WebSocket 等）。
func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    h, ok := w.w.(http.Hijacker)
    if !ok {
        return nil, nil, http.ErrNotSupported
    }
    return h.Hijack()
}

// ReadFrom 优先使用底层的 io.ReaderFrom（sendfile 等零拷贝路径）。
func (w *ResponseWriter) ReadFrom(src io.Reader) (int64, error) {
    if rf, ok := w.w.(io.ReaderFrom); ok {
        return rf.ReadFrom(src)
    }
    // 只暴露 Write 的包装，避免 io.Copy 再次命中 ReaderFrom 递归。
    return io.Copy(struct{ io.Writer }{w}, src)
}

// SetReadDeadline 转发给底层的连接读超时；不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) SetReadDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetReadDeadline(time.Time) error }); ok {
        return d.SetReadDeadline(t)
    }
    return http.ErrNotSupported
}

// SetWriteDeadline 转发给底层的连接写超时；不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) SetWriteDeadline(t time.Time) error {
    if d, ok := w.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
        return d.SetWriteDeadline(t)
    }
    return http.ErrNotSupported
}

// EnableFullDuplex 允许在 HTTP/1 连接上同时读写；不支持时返回 http.ErrNotSupported。
func (w *ResponseWriter) EnableFullDuplex() error {
    if d, ok := w.w.(interface{ EnableFullDuplex() error }); ok {
        return d.EnableFullDuplex()
    }
    return http.ErrNotSupported
}
