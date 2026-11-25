package mie

import (
	"bufio"
	"fmt"
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

// testApp 只给写层测试提供 Logger 与 context 注入，不参与路由。
var testApp = New()

func newTestCtx(w http.ResponseWriter, req *http.Request) *Ctx {
	return newCtx(w, req, testApp)
}

func newCapabilityCtx() (*Ctx, *capabilityWriter) {
	cw := &capabilityWriter{ResponseRecorder: httptest.NewRecorder()}
	return newTestCtx(cw, httptest.NewRequest(http.MethodGet, "/", nil)), cw
}

type plainWriter struct{ http.ResponseWriter }

// headerCodeSpy 只记录状态码；httptest.ResponseRecorder 在 101 之后会拒绝 body。
type headerCodeSpy struct {
	h     http.Header
	codes []int
}

func (s *headerCodeSpy) Header() http.Header         { return s.h }
func (s *headerCodeSpy) WriteHeader(code int)        { s.codes = append(s.codes, code) }
func (s *headerCodeSpy) Write(p []byte) (int, error) { return len(p), nil }

func TestWriterFlushForwards(t *testing.T) {
	c, cw := newCapabilityCtx()
	c.W.Flush()

	if !cw.flushed {
		t.Fatal("underlying Flush was not called")
	}
	if got := c.W.StatusCode(); got != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", got)
	}
	if cw.ResponseRecorder.Code != http.StatusOK {
		t.Fatalf("wire status = %d, want 200", cw.ResponseRecorder.Code)
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
	c := newTestCtx(&plainWriter{rec}, httptest.NewRequest(http.MethodGet, "/", nil))

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
	c := newTestCtx(&plainWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/", nil))

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
			c := newTestCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

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

func TestWriterStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

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
	c := newTestCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	c.W.WriteHeader(http.StatusEarlyHints)
	if got := c.W.StatusCode(); got != http.StatusEarlyHints {
		t.Fatalf("StatusCode after 1xx = %d, want 103", got)
	}

	c.W.WriteHeader(http.StatusOK)
	if got := c.W.StatusCode(); got != http.StatusOK {
		t.Fatalf("StatusCode after final = %d, want 200", got)
	}
}

// 101 是最终状态：之后再写 body 不能再补一个 200 头。
func TestWriterSwitchingProtocolsIsFinal(t *testing.T) {
	spy := &headerCodeSpy{h: http.Header{}}
	c := newTestCtx(spy, httptest.NewRequest(http.MethodGet, "/", nil))

	c.W.WriteHeader(http.StatusSwitchingProtocols)
	if _, err := c.W.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	if got := c.W.StatusCode(); got != http.StatusSwitchingProtocols {
		t.Fatalf("StatusCode after 101 then Write = %d, want 101", got)
	}
	if len(spy.codes) != 1 || spy.codes[0] != http.StatusSwitchingProtocols {
		t.Fatalf("wire codes = %v, want [101]", spy.codes)
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

	c3, _ := newCapabilityCtx()
	c3.W.SetStatus(http.StatusCreated)
	if _, _, err := c3.W.Hijack(); err != nil {
		t.Fatal(err)
	}
	if got := c3.W.StatusCode(); got != 0 {
		t.Fatalf("StatusCode after SetStatus then Hijack = %d, want 0", got)
	}

	c4, _ := newCapabilityCtx()
	c4.W.WriteHeader(http.StatusEarlyHints)
	if _, _, err := c4.W.Hijack(); err != nil {
		t.Fatal(err)
	}
	if got := c4.W.StatusCode(); got != 0 {
		t.Fatalf("StatusCode after 103 then Hijack = %d, want 0", got)
	}
}

func TestCtxStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestCtx(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := c.StatusCode(); got != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", got)
	}

	if got := c.Status(http.StatusNoContent); got != c {
		t.Fatalf("Status should return the same Ctx for chaining")
	}
	if got := c.StatusCode(); got != http.StatusNoContent {
		t.Fatalf("StatusCode = %d, want 204", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("preset must not be written before commit: wire = %d, want 200", rec.Code)
	}

	c.Status(http.StatusInternalServerError)
	if got := c.StatusCode(); got != http.StatusInternalServerError {
		t.Fatalf("StatusCode after overwrite = %d, want 500", got)
	}

	c.W.commit()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("wire status after commit = %d, want 500", rec.Code)
	}
}

func TestCtxStatusInformationalIsImmediate(t *testing.T) {
	spy := &headerCodeSpy{h: http.Header{}}
	c := newTestCtx(spy, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := c.Status(http.StatusEarlyHints); got != c {
		t.Fatalf("Status should return the same Ctx for chaining")
	}
	if len(spy.codes) != 1 || spy.codes[0] != http.StatusEarlyHints {
		t.Fatalf("wire codes = %v, want [103]", spy.codes)
	}
	if got := c.StatusCode(); got != http.StatusEarlyHints {
		t.Fatalf("StatusCode = %d, want 103", got)
	}
	if c.W.written {
		t.Fatal("1xx must not commit the final header")
	}

	c.Status(http.StatusNoContent)
	c.W.commit()
	if len(spy.codes) != 2 || spy.codes[1] != http.StatusNoContent {
		t.Fatalf("wire codes = %v, want [103 204]", spy.codes)
	}
}

func TestSetStatusForwardsInformational(t *testing.T) {
	spy := &headerCodeSpy{h: http.Header{}}
	c := newTestCtx(spy, httptest.NewRequest(http.MethodGet, "/", nil))

	c.W.SetStatus(http.StatusEarlyHints)
	if len(spy.codes) != 1 || spy.codes[0] != http.StatusEarlyHints {
		t.Fatalf("wire codes = %v, want [103]", spy.codes)
	}
	if got := c.W.StatusCode(); got != http.StatusEarlyHints {
		t.Fatalf("StatusCode = %d, want 103", got)
	}
}

func TestStatusRejectsInvalidCode(t *testing.T) {
	for _, code := range []int{99, 1000} {
		t.Run(fmt.Sprintf("code=%d", code), func(t *testing.T) {
			c := newTestCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			defer func() {
				if recover() == nil {
					t.Fatalf("Status(%d) did not panic", code)
				}
			}()
			c.Status(code)
		})
	}
}

func TestWriteHeaderInvalidCodeAfterCommitDoesNotPanic(t *testing.T) {
	c := newTestCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	c.W.WriteHeader(http.StatusCreated)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WriteHeader(1000) after commit panicked: %v", r)
		}
	}()
	c.W.WriteHeader(1000)

	if got := c.W.StatusCode(); got != http.StatusCreated {
		t.Fatalf("StatusCode = %d, want %d", got, http.StatusCreated)
	}
}

func TestWriterWriteAfterHijack(t *testing.T) {
	c, cw := newCapabilityCtx()
	if _, _, err := c.W.Hijack(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		call func() (int64, error)
	}{
		{"Write", func() (int64, error) { n, err := c.W.Write([]byte("x")); return int64(n), err }},
		{"WriteString", func() (int64, error) { n, err := c.W.WriteString("x"); return int64(n), err }},
		{"ReadFrom", func() (int64, error) { return c.W.ReadFrom(strings.NewReader("x")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := tt.call()
			if n != 0 || err != http.ErrHijacked {
				t.Fatalf("n = %d, err = %v, want 0, ErrHijacked", n, err)
			}
		})
	}

	if cw.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", cw.Body.String())
	}
}

func TestWriterWriteHeaderAfterHijack(t *testing.T) {
	c, cw := newCapabilityCtx()
	if _, _, err := c.W.Hijack(); err != nil {
		t.Fatal(err)
	}

	c.W.WriteHeader(http.StatusCreated)
	c.W.WriteHeader(1000) // 非法码也不 panic

	if got := cw.ResponseRecorder.Code; got != http.StatusOK {
		t.Fatalf("wire status = %d, want 200 (underlying must not be called)", got)
	}
	if got := c.W.StatusCode(); got != 0 {
		t.Fatalf("StatusCode = %d, want 0", got)
	}
}

func TestWriterFlushAfterHijackForwards(t *testing.T) {
	c, cw := newCapabilityCtx()
	if _, _, err := c.W.Hijack(); err != nil {
		t.Fatal(err)
	}

	if err := c.W.FlushError(); err != nil {
		t.Fatalf("FlushError() = %v, want nil", err)
	}
	if !cw.flushed {
		t.Fatal("underlying Flush must still be called after Hijack")
	}
}

func TestPresetStatusCommitsOnWrite(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Ctx) error
	}{
		{"WriteString", func(c *Ctx) error {
			_, err := c.W.WriteString("ok")
			return err
		}},
		{"Flush", func(c *Ctx) error {
			c.W.Flush()
			return nil
		}},
		{"ReadFrom", func(c *Ctx) error {
			_, err := c.W.ReadFrom(strings.NewReader("ok"))
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, cw := newCapabilityCtx()
			c.Status(http.StatusCreated)

			if err := tt.write(c); err != nil {
				t.Fatal(err)
			}
			if got := cw.ResponseRecorder.Code; got != http.StatusCreated {
				t.Fatalf("wire status = %d, want %d", got, http.StatusCreated)
			}
		})
	}
}

func TestCommitPromotesInformationalStatus(t *testing.T) {
	spy := &headerCodeSpy{h: make(http.Header)}
	c := newTestCtx(spy, httptest.NewRequest(http.MethodGet, "/", nil))

	c.Status(http.StatusEarlyHints)
	if _, err := c.W.WriteString("ok"); err != nil {
		t.Fatal(err)
	}

	if len(spy.codes) != 2 || spy.codes[0] != http.StatusEarlyHints || spy.codes[1] != http.StatusOK {
		t.Fatalf("wire codes = %v, want [%d %d]", spy.codes, http.StatusEarlyHints, http.StatusOK)
	}
	if got := c.W.StatusCode(); got != http.StatusOK {
		t.Fatalf("StatusCode = %d, want %d", got, http.StatusOK)
	}
}
