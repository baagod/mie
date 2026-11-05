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

func TestUnmatchedPathUsesErrorHandler(t *testing.T) {
    var got error
    r := New(Config{ErrorHandler: func(_ *Ctx, err error) { got = err }})
    r.Get("/health", func(*Ctx, any) (any, error) { return nil, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

    var target *Error
    if !errors.As(got, &target) || target.Status != http.StatusNotFound {
        t.Fatalf("ErrorHandler got %v, want *Error with status 404", got)
    }
    if rec.Body.Len() != 0 {
        t.Fatalf("body = %q, want empty", rec.Body.String())
    }
}