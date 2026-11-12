package mux

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestSendJSON(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/user", func(c *Ctx) error {
        return c.Send(JSON(map[string]string{"name": "alice"}))
    })

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
    r.Handle(http.MethodGet, "/created", func(c *Ctx) error {
        c.Status(http.StatusCreated)
        return c.Send(JSON(map[string]int{"id": 1}))
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/created", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
}

func TestSendJSONMarshalError(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/bad", func(c *Ctx) error {
        return c.Send(JSON(make(chan int)))
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

type emptyRenderer struct{}

func (emptyRenderer) Render(*Ctx) error { return nil }

// 锁定：renderer 不写 body 时，预设状态由 finish 提交。
func TestSendEmptyRendererKeepsPresetStatus(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/empty", func(c *Ctx) error {
        c.Status(http.StatusNoContent)
        return c.Send(emptyRenderer{})
    })

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
    r.Handle(http.MethodGet, "/csv", func(c *Ctx) error {
        return c.Send(csvRenderer{body: "a,b\n"})
    })

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
