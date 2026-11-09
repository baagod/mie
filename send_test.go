package mux

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestSendJSON(t *testing.T) {
    r := New()
    r.Get("/user", func(c *Ctx, _ any) (any, error) {
        return nil, c.Send(JSON(map[string]string{"name": "alice"}))
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEApplicationJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEApplicationJSON)
    }
    if got := rec.Body.String(); got != `{"name":"alice"}` {
        t.Fatalf("body = %q", got)
    }
}

func TestSendCommitsPresetStatus(t *testing.T) {
    r := New()
    r.Get("/created", func(c *Ctx, _ any) (any, error) {
        c.Status(http.StatusCreated)
        return nil, c.Send(JSON(map[string]int{"id": 1}))
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/created", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
}

func TestSendJSONMarshalError(t *testing.T) {
    r := New()
    r.Get("/bad", func(c *Ctx, _ any) (any, error) {
        return nil, c.Send(JSON(make(chan int)))
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainCharsetUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainCharsetUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
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
    r.Get("/csv", func(c *Ctx, _ any) (any, error) {
        return nil, c.Send(csvRenderer{body: "a,b\n"})
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
