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
