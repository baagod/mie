package mie

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

// 锁定：Header/Type 返回 Ctx，可以链式调用。
func TestHeaderAndTypeChain(t *testing.T) {
    r := New()
    r.Get("/chain", func(c *Ctx, _ struct{}) (struct{}, error) {
        c.Type("application/json").Header("X-Test", "1")
        return struct{}{}, nil
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

// 锁定：请求没带 X-Request-Id 时只生成 TraceID，不往响应里补头。
func TestTraceIDGeneratedWithoutResponseHeader(t *testing.T) {
    r := New()
    var id string
    r.Get("/trace", func(c *Ctx, _ struct{}) (struct{}, error) {
        id = c.TraceID()
        return struct{}{}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trace", nil))

    if got := rec.Header().Get(HeaderXRequestID); got != "" {
        t.Fatalf("响应头 %s = %q, want empty", HeaderXRequestID, got)
    }
    if len(id) != 32 {
        t.Fatalf("TraceID len = %d, want 32", len(id))
    }
}

// 锁定：请求带了 X-Request-Id 时复用，但不往响应里重复写。
func TestTraceIDReusedWithoutResponseHeader(t *testing.T) {
    r := New()
    var id string
    r.Get("/trace", func(c *Ctx, _ struct{}) (struct{}, error) {
        id = c.TraceID()
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/trace", nil)
    req.Header.Set(HeaderXRequestID, "trace-1")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if got := rec.Header().Get(HeaderXRequestID); got != "" {
        t.Fatalf("响应头 %s = %q, want empty", HeaderXRequestID, got)
    }
    if id != "trace-1" {
        t.Fatalf("TraceID = %q, want %q", id, "trace-1")
    }
}
