package mux

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

// 锁定：Header/Type 返回 Ctx，可以链式调用。
func TestHeaderAndTypeChain(t *testing.T) {
    r := New()
    r.Get("/chain", func(c *Ctx, _ any) (any, error) {
        c.Type("application/json").Header("X-Test", "1")
        return nil, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chain", nil))

    if got := rec.Header().Get(HeaderContentType); got != "application/json" {
        t.Fatalf("Content-Type = %q, want %q", got, "application/json")
    }
    if got := rec.Header().Get("X-Test"); got != "1" {
        t.Fatalf("X-Test = %q, want %q", got, "1")
    }
}
