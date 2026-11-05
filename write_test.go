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

type plainWriter struct{ http.ResponseWriter }

func TestWriterFlushForwards(t *testing.T) {
    c, cw := newCapabilityCtx()
    c.W.Flush()

    if !cw.flushed {
        t.Fatal("underlying Flush was not called")
    }
    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode = %d, want 200", got)
    }
}

func TestWriterHijackForwards(t *testing.T) {
    c, cw := newCapabilityCtx()
    if _, _, err := c.W.Hijack(); err != nil {
        t.Fatal(err)
    }
    if !cw.hijacked {
        t.Fatal("underlying Hijack was not called")
    }
}

func TestWriterReadFromForwards(t *testing.T) {
    c, cw := newCapabilityCtx()
    n, err := c.W.ReadFrom(strings.NewReader("hello"))
    if err != nil {
        t.Fatal(err)
    }
    if n != 5 || !cw.readFrom {
        t.Fatalf("n = %d, readFrom = %v", n, cw.readFrom)
    }
    if got := cw.Body.String(); got != "hello" {
        t.Fatalf("body = %q", got)
    }
    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode = %d, want 200", got)
    }
}

func TestWriterReadFromFallback(t *testing.T) {
    rec := httptest.NewRecorder()
    c := NewCtx(&plainWriter{rec}, httptest.NewRequest(http.MethodGet, "/", nil))

    n, err := c.W.ReadFrom(strings.NewReader("fallback"))
    if err != nil {
        t.Fatal(err)
    }
    if n != 8 {
        t.Fatalf("n = %d, want 8", n)
    }
    if got := rec.Body.String(); got != "fallback" {
        t.Fatalf("body = %q", got)
    }
    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode = %d, want 200", got)
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
    if err := rc.Flush(); err != nil {
        t.Fatal(err)
    }

    if !cw.readDeadline || !cw.writeDeadline || !cw.fullDuplex || !cw.flushed {
        t.Fatalf("read=%v write=%v duplex=%v flushed=%v", cw.readDeadline, cw.writeDeadline, cw.fullDuplex, cw.flushed)
    }
}

func TestWriterUnsupportedCapabilities(t *testing.T) {
    c := NewCtx(&plainWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/", nil))

    if _, _, err := c.W.Hijack(); err != http.ErrNotSupported {
        t.Fatalf("Hijack error = %v, want ErrNotSupported", err)
    }
    if err := c.W.SetReadDeadline(time.Now()); err != http.ErrNotSupported {
        t.Fatalf("SetReadDeadline error = %v", err)
    }
    if err := c.W.SetWriteDeadline(time.Now()); err != http.ErrNotSupported {
        t.Fatalf("SetWriteDeadline error = %v", err)
    }
    if err := c.W.EnableFullDuplex(); err != http.ErrNotSupported {
        t.Fatalf("EnableFullDuplex error = %v", err)
    }
    if err := c.W.FlushError(); err != http.ErrNotSupported {
        t.Fatalf("FlushError = %v, want ErrNotSupported", err)
    }
    c.W.Flush()
}

func TestWriterImplicitOK(t *testing.T) {
    tests := []struct {
        name  string
        write func(c *Ctx)
    }{
        {"Write", func(c *Ctx) { _, _ = c.W.Write([]byte("x")) }},
        {"WriteString", func(c *Ctx) { _, _ = c.W.WriteString("x") }},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            rec := httptest.NewRecorder()
            c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

            tt.write(c)

            if got := c.W.StatusCode(); got != http.StatusOK {
                t.Fatalf("StatusCode = %d, want 200", got)
            }
            if rec.Code != http.StatusOK {
                t.Fatalf("wire status = %d, want 200", rec.Code)
            }
            if got := rec.Body.String(); got != "x" {
                t.Fatalf("body = %q, want x", got)
            }
        })
    }
}

func TestWriterFlushImplicitOK(t *testing.T) {
    c, cw := newCapabilityCtx()
    c.W.Flush()

    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode = %d, want 200", got)
    }
    if cw.ResponseRecorder.Code != http.StatusOK {
        t.Fatalf("wire status = %d, want 200", cw.ResponseRecorder.Code)
    }
}

func TestWriterStatusCode(t *testing.T) {
    rec := httptest.NewRecorder()
    c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode before write = %d, want 200", got)
    }

    c.W.WriteHeader(http.StatusCreated)
    if got := c.W.StatusCode(); got != http.StatusCreated {
        t.Fatalf("StatusCode = %d, want 201", got)
    }
    if rec.Code != http.StatusCreated {
        t.Fatalf("wire status = %d, want 201", rec.Code)
    }

    c.W.WriteHeader(http.StatusInternalServerError)
    if got := c.W.StatusCode(); got != http.StatusCreated {
        t.Fatalf("StatusCode after ignored write = %d, want 201", got)
    }
    if rec.Code != http.StatusCreated {
        t.Fatalf("wire status after ignored write = %d, want 201", rec.Code)
    }
}

func TestWriterStatusEarlyHints(t *testing.T) {
    rec := httptest.NewRecorder()
    c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

    c.W.WriteHeader(http.StatusEarlyHints)
    if got := c.W.StatusCode(); got != http.StatusEarlyHints {
        t.Fatalf("StatusCode after 1xx = %d, want 103", got)
    }

    c.W.WriteHeader(http.StatusOK)
    if got := c.W.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode after final = %d, want 200", got)
    }
}

func TestWriterStatusSwitchingProtocols(t *testing.T) {
    c := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

    c.W.WriteHeader(http.StatusSwitchingProtocols)
    if got := c.W.StatusCode(); got != http.StatusSwitchingProtocols {
        t.Fatalf("StatusCode = %d, want 101", got)
    }
}

func TestWriterHijackStatus(t *testing.T) {
    c, _ := newCapabilityCtx()
    if _, _, err := c.W.Hijack(); err != nil {
        t.Fatal(err)
    }
    if got := c.W.StatusCode(); got != 0 {
        t.Fatalf("StatusCode after Hijack without write = %d, want 0", got)
    }

    c2, _ := newCapabilityCtx()
    c2.W.WriteHeader(http.StatusSwitchingProtocols)
    if _, _, err := c2.W.Hijack(); err != nil {
        t.Fatal(err)
    }
    if got := c2.W.StatusCode(); got != http.StatusSwitchingProtocols {
        t.Fatalf("StatusCode after 101 then Hijack = %d, want 101", got)
    }
}

func TestCtxStatusCode(t *testing.T) {
    rec := httptest.NewRecorder()
    c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

    if got := c.StatusCode(); got != http.StatusOK {
        t.Fatalf("StatusCode = %d, want 200", got)
    }

    c.Status(http.StatusNoContent)
    if got := c.StatusCode(); got != http.StatusNoContent {
        t.Fatalf("StatusCode = %d, want 204", got)
    }
    if rec.Code != http.StatusNoContent {
        t.Fatalf("wire status = %d, want 204", rec.Code)
    }

    c.Status(http.StatusInternalServerError)
    if got := c.StatusCode(); got != http.StatusNoContent {
        t.Fatalf("StatusCode after ignored write = %d, want 204", got)
    }
}

func TestStatusRejectsInvalidCode(t *testing.T) {
    for _, code := range []int{99, 1000} {
        t.Run(http.StatusText(code), func(t *testing.T) {
            c := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
            defer func() {
                if recover() == nil {
                    t.Fatalf("Status(%d) did not panic", code)
                }
            }()
            c.Status(code)
        })
    }
}
