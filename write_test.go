package mux

import (
    "bufio"
    "io"
    "net"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

type capabilityWriter struct {
    *httptest.ResponseRecorder
    flushed       bool
    hijacked      bool
    readFrom      bool
    readDeadline  bool
    writeDeadline bool
    fullDuplex    bool
}

func (w *capabilityWriter) Flush() {
    w.flushed = true
    w.ResponseRecorder.Flush()
}

func (w *capabilityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    w.hijacked = true
    return nil, nil, nil
}

func (w *capabilityWriter) ReadFrom(r io.Reader) (int64, error) {
    w.readFrom = true
    return io.Copy(w.ResponseRecorder, r)
}

func (w *capabilityWriter) SetReadDeadline(time.Time) error  { w.readDeadline = true; return nil }
func (w *capabilityWriter) SetWriteDeadline(time.Time) error { w.writeDeadline = true; return nil }
func (w *capabilityWriter) EnableFullDuplex() error          { w.fullDuplex = true; return nil }

func newCapabilityCtx() (*Ctx, *capabilityWriter) {
    cw := &capabilityWriter{ResponseRecorder: httptest.NewRecorder()}
    return NewCtx(cw, httptest.NewRequest(http.MethodGet, "/", nil)), cw
}

func TestResponseWriterFlush(t *testing.T) {
    c, cw := newCapabilityCtx()
    if _, ok := any(c.W).(http.Flusher); !ok {
        t.Fatal("ResponseWriter does not implement http.Flusher")
    }
    c.W.Flush()
    if !cw.flushed {
        t.Fatal("underlying Flush was not called")
    }
}

func TestResponseWriterHijack(t *testing.T) {
    c, cw := newCapabilityCtx()
    if _, ok := any(c.W).(http.Hijacker); !ok {
        t.Fatal("ResponseWriter does not implement http.Hijacker")
    }
    if _, _, err := c.W.Hijack(); err != nil {
        t.Fatal(err)
    }
    if !cw.hijacked {
        t.Fatal("underlying Hijack was not called")
    }
}

func TestResponseWriterReadFrom(t *testing.T) {
    c, cw := newCapabilityCtx()
    n, err := c.W.ReadFrom(strings.NewReader("hello"))
    if err != nil {
        t.Fatal(err)
    }
    if n != 5 || !cw.readFrom {
        t.Fatalf("n=%d readFrom=%v", n, cw.readFrom)
    }
    if got := cw.Body.String(); got != "hello" {
        t.Fatalf("body = %q", got)
    }
}

func TestResponseControllerCapabilities(t *testing.T) {
    c, cw := newCapabilityCtx()
    rc := http.NewResponseController(c.W)
    if err := rc.SetReadDeadline(time.Now()); err != nil {
        t.Fatal(err)
    }
    if err := rc.SetWriteDeadline(time.Now()); err != nil {
        t.Fatal(err)
    }
    if err := rc.EnableFullDuplex(); err != nil {
        t.Fatal(err)
    }
    if !cw.readDeadline || !cw.writeDeadline || !cw.fullDuplex {
        t.Fatalf("read=%v write=%v duplex=%v", cw.readDeadline, cw.writeDeadline, cw.fullDuplex)
    }
    if err := rc.Flush(); err != nil {
        t.Fatal(err)
    }
    if !cw.flushed {
        t.Fatal("Flush was not called")
    }
}

type plainWriter struct{ http.ResponseWriter }

func TestResponseWriterUnsupportedCapabilities(t *testing.T) {
    c := NewCtx(&plainWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/", nil))
    if _, _, err := c.W.Hijack(); err != http.ErrNotSupported {
        t.Fatalf("Hijack error = %v", err)
    }
    if err := c.W.SetWriteDeadline(time.Now()); err != http.ErrNotSupported {
        t.Fatalf("SetWriteDeadline error = %v", err)
    }
    c.W.Flush()
}

func TestResponseWriterStatus(t *testing.T) {
    rec := httptest.NewRecorder()
    c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

    if got := c.W.StatusCode(); got != 0 {
        t.Fatalf("Status before write = %d, want 0", got)
    }
    c.W.WriteHeader(http.StatusCreated)
    if got := c.W.StatusCode(); got != http.StatusCreated {
        t.Fatalf("Status after WriteHeader = %d, want 201", got)
    }

    // 1xx 是临时响应，不改变最终状态。
    c2 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    c2.W.WriteHeader(http.StatusEarlyHints)
    if got := c2.W.StatusCode(); got != 0 {
        t.Fatalf("Status after 1xx = %d, want 0", got)
    }
    c2.W.WriteHeader(http.StatusOK)
    if got := c2.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("Status after final = %d, want 200", got)
    }

    // 101 是协议切换，属于最终响应，必须记录。
    c5 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    c5.W.WriteHeader(http.StatusSwitchingProtocols)
    if got := c5.W.StatusCode(); got != http.StatusSwitchingProtocols {
        t.Fatalf("Status after 101 = %d, want 101", got)
    }

    // 第一个最终状态胜出：之后的最终 WriteHeader 会被 net/http 忽略。
    rec6 := httptest.NewRecorder()
    c6 := NewCtx(rec6, httptest.NewRequest(http.MethodGet, "/", nil))
    c6.W.WriteHeader(http.StatusCreated)
    c6.W.WriteHeader(http.StatusInternalServerError)
    if got := c6.W.StatusCode(); got != http.StatusCreated {
        t.Fatalf("Status after ignored second WriteHeader = %d, want 201", got)
    }
    if rec6.Code != http.StatusCreated {
        t.Fatalf("wire status = %d, want 201", rec6.Code)
    }

    // 直接 Write / Flush 是隐式 200，Status 只记录显式设置的最终状态。
    c3 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    if _, err := c3.W.Write([]byte("ok")); err != nil {
        t.Fatal(err)
    }
    if got := c3.W.StatusCode(); got != 0 {
        t.Fatalf("Status after implicit Write = %d, want 0", got)
    }

    c4 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    c4.W.Flush()
    if got := c4.W.StatusCode(); got != 0 {
        t.Fatalf("Status after implicit Flush = %d, want 0", got)
    }
}

func TestStatusCode(t *testing.T) {
    c := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    if got := c.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode before write = %d, want 200", got)
    }

    c.Status(http.StatusAccepted)
    if got := c.StatusCode(); got != http.StatusAccepted {
        t.Fatalf("StatusCode after Status = %d, want 202", got)
    }

    c.W.WriteHeader(http.StatusNoContent)
    if got := c.StatusCode(); got != http.StatusNoContent {
        t.Fatalf("StatusCode after write = %d, want 204", got)
    }

    // 隐式 200：只写 body，不调 WriteHeader。
    c2 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    if _, err := c2.W.Write([]byte("ok")); err != nil {
        t.Fatal(err)
    }
    if got := c2.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode after implicit Write = %d, want 200", got)
    }

    // 只发过临时 1xx、没有最终状态：net/http 收尾时补 200。
    c3 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    c3.W.WriteHeader(http.StatusEarlyHints)
    if got := c3.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode after 1xx only = %d, want 200", got)
    }

    // 已经提交后，c.Status 不再生效。
    c4 := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    c4.W.WriteHeader(http.StatusCreated)
    c4.Status(http.StatusInternalServerError)
    if got := c4.StatusCode(); got != http.StatusCreated {
        t.Fatalf("StatusCode after sent then Status = %d, want 201", got)
    }
}
