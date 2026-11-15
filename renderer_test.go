package mie

import (
    "bytes"
    "errors"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestSendJSON(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/user", He(func(c *Ctx, _ struct{}) error {
        return c.Send(JSON(map[string]string{"name": "alice"}))
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Body.String(); got != `{"name":"alice"}` {
        t.Fatalf("body = %q", got)
    }
}

func TestSendCommitsPresetStatus(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/created", He(func(c *Ctx, _ struct{}) error {
        c.Status(http.StatusCreated)
        return c.Send(JSON(map[string]int{"id": 1}))
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/created", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
}

func TestSendJSONMarshalError(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/bad", He(func(c *Ctx, _ struct{}) error {
        return c.Send(JSON(make(chan int)))
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
    }
}

type partialRenderer struct{}

// 写一半就失败：状态码已定，错误文本追加到 body 尾部。
func (partialRenderer) Render(c *Ctx) error {
    c.Status(http.StatusCreated)
    if _, err := c.W.WriteString("{"); err != nil {
        return err
    }
    return errors.New("render failed")
}

func TestSendErrorAfterPartialWrite(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/partial", He(func(c *Ctx, _ struct{}) error {
        return c.Send(partialRenderer{})
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
    if got := rec.Body.String(); got != "{render failed" {
        t.Fatalf("body = %q, want %q", got, "{render failed")
    }
}

type emptyRenderer struct{}

func (emptyRenderer) Render(*Ctx) error { return nil }

// 锁定：renderer 不写 body 时，预设状态由 finish 提交。
func TestSendEmptyRendererKeepsPresetStatus(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/empty", He(func(c *Ctx, _ struct{}) error {
        c.Status(http.StatusNoContent)
        return c.Send(emptyRenderer{})
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/empty", nil))

    if rec.Code != http.StatusNoContent {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
    }
    if rec.Body.Len() != 0 {
        t.Fatalf("body = %q, want empty", rec.Body.String())
    }
}

type csvRenderer struct{ body string }

func (r csvRenderer) Render(c *Ctx) error {
    c.Header(HeaderContentType, "text/csv; charset=utf-8")
    _, err := c.W.WriteString(r.body)
    return err
}

func TestSendCustomRenderer(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/csv", He(func(c *Ctx, _ struct{}) error {
        return c.Send(csvRenderer{body: "a,b\n"})
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/csv", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != "text/csv; charset=utf-8" {
        t.Fatalf("Content-Type = %q", got)
    }
    if got := rec.Body.String(); got != "a,b\n" {
        t.Fatalf("body = %q", got)
    }
}

// failOnceWriter 第一次写失败，之后正常写：确认写错误交给 writeError，而不是被 Send 吞掉。
type failOnceWriter struct {
    h      http.Header
    body   string
    failed bool
}

func (w *failOnceWriter) Header() http.Header { return w.h }

func (w *failOnceWriter) WriteHeader(int) {}

func (w *failOnceWriter) Write(p []byte) (int, error) {
    if w.failed {
        w.body += string(p)
        return len(p), nil
    }
    w.failed = true
    return 0, errors.New("write failed")
}

func TestSendWriteErrorIsNotSwallowed(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/broken", He(func(c *Ctx, _ struct{}) error {
        return c.Send(JSON(map[string]string{"name": "alice"}))
    }))

    w := &failOnceWriter{h: make(http.Header)}
    r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/broken", nil))

    if got := w.body; got != "write failed" {
        t.Fatalf("body = %q, want %q（写错误应交给 writeError，而不是被吞掉）", got, "write failed")
    }
}

func TestSendStringWritesText(t *testing.T) {
    r := New()
    r.Get("/text", func(c *Ctx, _ struct{}) (string, error) { return "ok", nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/text", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
    }
    if got := rec.Body.String(); got != "ok" {
        t.Fatalf("body = %q, want %q", got, "ok")
    }
}

func TestSendBytesWritesRaw(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    r := New()
    r.Get("/img", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/img", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != "image/png" {
        t.Fatalf("Content-Type = %q, want image/png", got)
    }
    if !bytes.Equal(rec.Body.Bytes(), png) {
        t.Fatalf("body = %q, want %q", rec.Body.Bytes(), png)
    }
}

func TestSendNegotiatesStructByAccept(t *testing.T) {
    type user struct{ Name string }
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (user, error) { return user{Name: "alice"}, nil })

    cases := []struct {
        name   string
        accept string
        want   int
        wantCT string
    }{
        {"missing", "", http.StatusOK, MIMEAppJSON},
        {"any", "*/*", http.StatusOK, MIMEAppJSON},
        {"json", "application/json", http.StatusOK, MIMEAppJSON},
        {"json uppercase", "Application/JSON", http.StatusOK, MIMEAppJSON},
        {"mixed", "text/html, */*", http.StatusOK, MIMEAppJSON},
        {"application wildcard", "application/*", http.StatusOK, MIMEAppJSON},
        {"text wildcard", "text/*", http.StatusNotAcceptable, MIMETextPlainUTF8},
        {"html only", "text/html", http.StatusNotAcceptable, MIMETextPlainUTF8},
        {"other type", "text/csv", http.StatusNotAcceptable, MIMETextPlainUTF8},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodGet, "/user", nil)
            if tc.accept != "" {
                req.Header.Set("Accept", tc.accept)
            }
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != tc.want {
                t.Fatalf("status = %d, want %d", rec.Code, tc.want)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if got := rec.Header().Get("Vary"); got != "Accept" {
                t.Fatalf("Vary = %q, want Accept", got)
            }
        })
    }
}

// 值只能产出自己的类型：accept 只能接受或拒绝，不能把它改标成别的类型。
func TestSendBytesFeedAccept(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    pdf := []byte("%PDF-1.7\nraw-bytes")
    r := New()
    r.Get("/png", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })
    r.Get("/pdf", func(c *Ctx, _ struct{}) ([]byte, error) { return pdf, nil })
    r.Get("/b", func(c *Ctx, _ struct{}) ([]byte, error) { return []byte("hello"), nil })
    r.Get("/s", func(c *Ctx, _ struct{}) (string, error) { return "ok", nil })

    cases := []struct {
        name     string
        path     string
        accept   string
        want     int
        wantCT   string
        wantBody string
    }{
        {"json bytes", "/b", MIMEAppJSON, http.StatusOK, MIMEAppJSON, `"aGVsbG8="`},
        {"json string", "/s", MIMEAppJSON, http.StatusOK, MIMEAppJSON, `"ok"`},
        {"png exact", "/png", "image/png", http.StatusOK, "image/png", string(png)},
        {"png wildcard", "/png", "image/*", http.StatusOK, "image/png", string(png)},
        {"png mismatched", "/png", "image/webp", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
        {"pdf exact", "/pdf", "application/pdf", http.StatusOK, "application/pdf", string(pdf)},
        {"pdf wildcard app", "/pdf", "application/*", http.StatusOK, "application/pdf", string(pdf)},
        {"pdf mismatched", "/pdf", "image/*", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
        {"text exact", "/b", "text/plain", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"text uppercase", "/b", "TEXT/PLAIN", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"text wildcard", "/b", "text/*", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"string named csv", "/s", "text/csv", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodGet, tc.path, nil)
            req.Header.Set("Accept", tc.accept)
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != tc.want {
                t.Fatalf("status = %d, want %d", rec.Code, tc.want)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
                t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
            }
        })
    }
}

func TestSendRendererIgnoresAccept(t *testing.T) {
    r := New()
    r.Get("/json", func(c *Ctx, _ struct{}) (Renderer, error) {
        return JSON(map[string]int{"id": 1}), nil
    })

    req := httptest.NewRequest(http.MethodGet, "/json", nil)
    req.Header.Set("Accept", "text/html")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get("Vary"); got != "" {
        t.Fatalf("Vary = %q, want empty", got)
    }
    if got := rec.Body.String(); got != `{"id":1}` {
        t.Fatalf("body = %q", got)
    }
}

func TestSendKeepsExistingVary(t *testing.T) {
    r := New()
    r.Get("/text", func(c *Ctx, _ struct{}) (string, error) {
        c.Header(HeaderVary, "Accept-Encoding")
        return "ok", nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/text", nil))

    if got := rec.Header().Values(HeaderVary); len(got) != 2 || got[0] != "Accept-Encoding" || got[1] != "Accept" {
        t.Fatalf("Vary = %q, want [Accept-Encoding Accept]", got)
    }
}
