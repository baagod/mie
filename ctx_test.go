package mie

import (
    "context"
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

// 锁定：Get 按类型读回 Set 写入的值；未设置或类型不符时返回零值，不 panic。
func TestCtxGet(t *testing.T) {
    type user struct{ ID int }
    var userKey struct{}

    newC := func() *Ctx {
        return newTestCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
    }

    t.Run("未设置返回零值", func(t *testing.T) {
        c := newC()
        if got := c.Get[*user](userKey); got != nil {
            t.Fatalf("Get = %v, want nil", got)
        }
        if got := c.Get[int](userKey); got != 0 {
            t.Fatalf("Get = %d, want 0", got)
        }
    })

    t.Run("命中返回写入的值", func(t *testing.T) {
        c := newC()
        u := &user{ID: 7}
        c.Set(userKey, u)
        if got := c.Get[*user](userKey); got != u {
            t.Fatalf("Get = %p, want %p", got, u)
        }
    })

    t.Run("类型不符返回零值", func(t *testing.T) {
        c := newC()
        c.Set(userKey, &user{ID: 7})
        if got := c.Get[string](userKey); got != "" {
            t.Fatalf("Get = %q, want empty", got)
        }
        if got := c.Get[user](userKey); got.ID != 0 {
            t.Fatalf("Get = %+v, want zero", got)
        }
    })
}

// 锁定：Value 容器未命中时回退请求 context；同键同时存在时容器优先。
func TestCtxValueFallsBackToRequestContext(t *testing.T) {
    type outerKey struct{}

    req := httptest.NewRequest(http.MethodGet, "/", nil)
    req = req.WithContext(context.WithValue(req.Context(), outerKey{}, "outer"))
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Get[string](outerKey{}); got != "outer" {
        t.Fatalf("Get = %q, want %q", got, "outer")
    }

    c.Set(outerKey{}, "inner")
    if got := c.Value(outerKey{}); got != "inner" {
        t.Fatalf("Value = %v, want %q", got, "inner")
    }
}
