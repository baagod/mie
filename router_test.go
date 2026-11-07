package mux

import (
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestRouteParam(t *testing.T) {
    r := New()
    r.Get("/users/{id}", func(c *Ctx, _ any) (any, error) {
        _, _ = c.W.WriteString("user:" + c.Param("id"))
        return nil, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Body.String(); got != "user:42" {
        t.Fatalf("body = %q, want %q", got, "user:42")
    }
}

func TestUnmatchedPathSkipsErrorHandler(t *testing.T) {
    called := false
    r := New(Config{ErrorHandler: func(_ *Ctx, err error) error { called = true; return err }})
    r.Get("/health", func(*Ctx, any) (any, error) { return nil, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

    if called {
        t.Fatal("ErrorHandler called for unmatched path; want native 404")
    }
    if rec.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
    }
    if got := rec.Body.String(); got != "404 page not found\n" {
        t.Fatalf("body = %q, want native 404 body", got)
    }
}

func TestNoMatchHandler(t *testing.T) {
    var statuses []int
    var allow string
    r := New(Config{NoMatchHandler: func(c *Ctx, status int) {
        statuses = append(statuses, status)
        allow = c.W.Header().Get("Allow")
    }})
    r.Get("/health", func(*Ctx, any) (any, error) { return nil, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
    if rec.Code != http.StatusMethodNotAllowed || rec.Body.String() != "Method Not Allowed\n" {
        t.Fatalf("405 response status=%d body=%q, want native 405", rec.Code, rec.Body.String())
    }
    if len(statuses) != 1 || statuses[0] != http.StatusMethodNotAllowed || allow != "GET, HEAD" {
        t.Fatalf("fallback got statuses=%v allow=%q, want [405] with GET, HEAD", statuses, allow)
    }

    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
    if rec.Code != http.StatusNotFound || rec.Body.String() != "404 page not found\n" {
        t.Fatalf("404 response status=%d body=%q, want native 404", rec.Code, rec.Body.String())
    }
    if len(statuses) != 2 || statuses[1] != http.StatusNotFound {
        t.Fatalf("fallback statuses=%v, want [405 404]", statuses)
    }
}

func TestDefaultErrorHandlerWritesResponse(t *testing.T) {
    r := New()
    r.Get("/teapot", func(*Ctx, any) (any, error) {
        return nil, NewError(http.StatusTeapot, "short and stout")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/teapot", nil))

    if rec.Code != http.StatusTeapot {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
    }
    if got := rec.Body.String(); got != "short and stout" {
        t.Fatalf("body = %q, want %q", got, "short and stout")
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainCharsetUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainCharsetUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

func TestDefaultErrorHandlerUnknownError(t *testing.T) {
    r := New()
    r.Get("/boom", func(*Ctx, any) (any, error) {
        return nil, errors.New("db down")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); got != "db down" {
        t.Fatalf("body = %q, want %q", got, "db down")
    }
}

func TestWriteErrorKeepsWrittenResponse(t *testing.T) {
    r := New()
    r.Get("/partial", func(c *Ctx, _ any) (any, error) {
        c.W.WriteHeader(http.StatusCreated)
        _, _ = c.W.WriteString("created")
        return nil, errors.New("late failure")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
    if got := rec.Body.String(); got != "createdlate failure" {
        t.Fatalf("body = %q, want %q", got, "createdlate failure")
    }
}

func TestRouteCommitsPresetStatus(t *testing.T) {
    r := New()
    r.Get("/no-content", func(c *Ctx, _ any) (any, error) {
        c.Status(http.StatusNoContent)
        return nil, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-content", nil))

    if rec.Code != http.StatusNoContent {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
    }
}

func TestWriteErrorKeepsCustomContentType(t *testing.T) {
    r := New()
    r.Get("/json", func(c *Ctx, _ any) (any, error) {
        c.SetHeader(HeaderContentType, "application/json")
        return nil, NewError(http.StatusBadRequest, "bad input")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/json", nil))

    if rec.Code != http.StatusBadRequest {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
    }
    if got := rec.Header().Get(HeaderContentType); got != "application/json" {
        t.Fatalf("Content-Type = %q, want %q", got, "application/json")
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != "" {
        t.Fatalf("X-Content-Type-Options = %q, want empty", got)
    }
}

func TestPanicWritesDefault500(t *testing.T) {
    r := New()
    r.Get("/panic", func(*Ctx, any) (any, error) { panic("boom") })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); got != http.StatusText(http.StatusInternalServerError) {
        t.Fatalf("body = %q, want %q", got, http.StatusText(http.StatusInternalServerError))
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

func TestPanicAfterWriteAbortsResponse(t *testing.T) {
    r := New()
    r.Get("/half", func(c *Ctx, _ any) (any, error) {
        _, _ = c.W.WriteString("half")
        panic("boom")
    })

    s := httptest.NewServer(r)
    defer s.Close()

    resp, err := http.Get(s.URL + "/half")
    if err != nil {
        return
    }
    defer resp.Body.Close()
    if _, rerr := io.ReadAll(resp.Body); rerr == nil {
        t.Fatal("response completed; want aborted connection")
    }
}
