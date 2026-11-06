package mux

import (
    "errors"
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
    r := New(Config{ErrorHandler: func(*Ctx, error) { called = true }})
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

func TestDefaultErrorHandlerAppendsAfterWrittenResponse(t *testing.T) {
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
