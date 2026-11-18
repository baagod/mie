package mie

import (
    "context"
    "net/http"
    "net/http/httptest"
    "slices"
    "strings"
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

// 锁定：Form 同名时 query 整体替换 form（含多值），只在 form 的键仍能取到。
func TestCtxFormQueryOverridesForm(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/form?page=2&tags=b&tags=d",
        strings.NewReader("page=1&tags=a&tags=c&only=form"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Form[int]("page"); got != 2 {
        t.Fatalf("query 优先: Form[int] = %d, want 2", got)
    }
    if got := c.Form[[]string]("tags"); !slices.Equal(got, []string{"b", "d"}) {
        t.Fatalf("query 多值整体替换: Form[[]string] = %v, want [b d]", got)
    }
    if got := c.Form[string]("only"); got != "form" {
        t.Fatalf("只在 form: Form[string] = %q, want %q", got, "form")
    }
    if got := c.Form[[]int]("missing"); got != nil {
        t.Fatalf("缺失: Form[[]int] = %v, want nil", got)
    }
}

// 锁定：query 里存在但为空同样覆盖 form，解析失败按零值。
func TestCtxFormEmptyQueryOverridesForm(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/form?empty=&num=",
        strings.NewReader("empty=form&num=7"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Form[string]("empty"); got != "" {
        t.Fatalf("空串覆盖: Form[string] = %q, want empty", got)
    }
    if got := c.Form[int]("num"); got != 0 {
        t.Fatalf("空串解析失败: Form[int] = %d, want 0", got)
    }
}

// 锁定：数组解析失败时 Form 返回零值，不交出半截结果。
func TestCtxFormArrayParseFailure(t *testing.T) {
    c := newTestCtx(httptest.NewRecorder(),
        httptest.NewRequest(http.MethodGet, "/form?pair=1&pair=abc", nil))

    if got := c.Form[[2]int]("pair"); got != [2]int{} {
        t.Fatalf("Form[[2]int] = %v, want [0 0]", got)
    }
}

// 锁定：Path 是实际路径，Pattern 是不带方法前缀的模板。
func TestCtxPathAndPattern(t *testing.T) {
    r := New()
    var path, pattern string
    r.Get("/user/{id}", func(c *Ctx, _ struct{}) (struct{}, error) {
        path, pattern = c.Path(), c.Pattern()
        return struct{}{}, nil
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/user/42", nil))

    if path != "/user/42" {
        t.Fatalf("Path = %q, want %q", path, "/user/42")
    }
    if pattern != "/user/{id}" {
        t.Fatalf("Pattern = %q, want %q", pattern, "/user/{id}")
    }
}
