package mie

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func routerOK(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil }

func TestGroupPrefix(t *testing.T) {
    cases := []struct {
        name    string
        group   string
        pattern string
        path    string
    }{
        {"plain", "/api", "/users", "/api/users"},
        {"no leading slash", "api", "/users", "/api/users"},
        {"root group", "/", "/health", "/health"},
        {"subtree", "/api", "/static/", "/api/static/a/b"},
        {"empty pattern", "/api", "", "/api"},
        {"empty pattern after trailing slash", "/api/", "", "/api/x"},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            r := New()
            r.Group(tc.group).Get(tc.pattern, routerOK)

            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

            if rec.Code != http.StatusOK {
                t.Fatalf("GET %s: status = %d, want %d", tc.path, rec.Code, http.StatusOK)
            }
        })
    }
}

func TestGroupNests(t *testing.T) {
    r := New()
    r.Group("/api").Group("v1").Get("/users", routerOK)

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
}

func TestGroupMiddlewareSnapshot(t *testing.T) {
    t.Run("inherits existing", func(t *testing.T) {
        hits := 0
        r := New()
        r.Use(counting(&hits))
        r.Group("/api").Get("/users", routerOK)

        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))

        if rec.Code != http.StatusOK || hits != 1 {
            t.Fatalf("status = %d, hits = %d, want 200 and 1", rec.Code, hits)
        }
    })

    t.Run("ignores later use", func(t *testing.T) {
        hits := 0
        r := New()
        api := r.Group("/api")
        r.Use(counting(&hits))
        api.Get("/users", routerOK)

        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))

        if rec.Code != http.StatusOK || hits != 0 {
            t.Fatalf("status = %d, hits = %d, want 200 and 0", rec.Code, hits)
        }
    })
}

func TestGroupMiddlewareIsolation(t *testing.T) {
    hits := 0
    r := New()
    api := r.Group("/api")
    api.Use(counting(&hits))
    api.Get("/a", routerOK)
    r.Group("/other").Get("/c", routerOK)
    r.Get("/b", routerOK)

    for _, path := range []string{"/b", "/api/a", "/other/c"} {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
        if rec.Code != http.StatusOK {
            t.Fatalf("GET %s: status = %d, want %d", path, rec.Code, http.StatusOK)
        }
    }

    if hits != 1 {
        t.Fatalf("hits = %d, want 1 (only /api/a)", hits)
    }
}

func TestChainedRegistration(t *testing.T) {
    r := New()
    r.Get("/a", routerOK).Post("/a", routerOK)

    for _, method := range []string{http.MethodGet, http.MethodPost} {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(method, "/a", nil))
        if rec.Code != http.StatusOK {
            t.Fatalf("%s /a: status = %d, want %d", method, rec.Code, http.StatusOK)
        }
    }
}

type routerInput struct {
    Name string `form:"name"`
}

type otherInput struct {
    ID int `form:"id"`
}

func TestUseTypedMiddleware(t *testing.T) {
    var seen string

    r := New()
    api := r.Group("/api")
    api.Use(func(c *Ctx, in routerInput) (any, error) {
        seen = in.Name
        return nil, c.Next()
    })
    api.Get("/users", func(c *Ctx, in routerInput) (routerInput, error) {
        return in, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users?name=bob", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    if seen != "bob" {
        t.Fatalf("middleware input = %q, want bob", seen)
    }
    if got := rec.Body.String(); got != `{"Name":"bob"}` {
        t.Fatalf("body = %s, want %s", got, `{"Name":"bob"}`)
    }
}

// 指针 IN 在整条链上共享同一个对象：中间件写入的字段处理器能看到。
func TestUsePointerInputShared(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, in *routerInput) (any, error) {
        in.Name = "rewritten"
        return nil, c.Next()
    })
    r.Get("/ptr", func(c *Ctx, in *routerInput) (string, error) { return in.Name, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ptr?name=bob", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "rewritten" {
        t.Fatalf("status = %d, body = %s, want 200 and rewritten", rec.Code, rec.Body.String())
    }
}

// 值 IN 是快照：中间件改自己的参数不会传下去，处理器仍看到首次绑定的值。
func TestUseValueInputIsSnapshot(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, in routerInput) (any, error) {
        in.Name = "rewritten"
        return nil, c.Next()
    })
    r.Get("/val", func(c *Ctx, in routerInput) (string, error) { return in.Name, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/val?name=bob", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "bob" {
        t.Fatalf("status = %d, body = %s, want 200 and bob", rec.Code, rec.Body.String())
    }
}

// 组前缀带不带尾斜杠，决定组根是精确路径还是子树；两者可以同时注册。
func TestGroupRootTrailingSlash(t *testing.T) {
    r := New()
    r.Group("/api").Get("", func(*Ctx, struct{}) (string, error) { return "exact", nil })
    r.Group("/api/").Get("", func(*Ctx, struct{}) (string, error) { return "subtree", nil })

    cases := []struct{ path, body string }{
        {"/api", "exact"},
        {"/api/", "subtree"},
        {"/api/x", "subtree"},
    }

    for _, tc := range cases {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

        if rec.Code != http.StatusOK || rec.Body.String() != tc.body {
            t.Fatalf("GET %s: status = %d, body = %s, want 200 and %s", tc.path, rec.Code, rec.Body.String(), tc.body)
        }
    }
}

func TestUseInputMismatchPanics(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, _ routerInput) (any, error) { return nil, c.Next() })

    mustPanicWith(t, "does not match middleware input", func() {
        r.Get("/users", func(_ *Ctx, in otherInput) (otherInput, error) { return in, nil })
    })
}

func TestUseInputConflictPanics(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, _ routerInput) (any, error) { return nil, c.Next() })

    mustPanicWith(t, "conflicts with", func() {
        r.Use(func(c *Ctx, _ otherInput) (any, error) { return nil, c.Next() })
    })
}

// 多层指针 IN 在注册期就 panic，不该留给首个请求。
func TestRouteDoublePointerInputPanics(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    mustPanicWith(t, "pointer to a pointer", func() {
        New().Post[**in, struct{}]("/x", func(*Ctx, **in) (struct{}, error) { return struct{}{}, nil })
    })
}

func mustPanicWith(t *testing.T, want string, f func()) {
    t.Helper()
    defer func() {
        v := recover()
        if v == nil {
            t.Fatalf("want panic containing %q", want)
        }
        if got, ok := v.(string); !ok || !strings.Contains(got, want) {
            t.Fatalf("panic = %v, want containing %q", v, want)
        }
    }()
    f()
}

// 注册之后再加中间件，不能改动已注册路由的链。
func TestRegisteredRouteKeepsChain(t *testing.T) {
    var calls []string

    r := New()
    r.Use(recording(&calls, "a")).Use(recording(&calls, "b")).Use(recording(&calls, "c"))
    r.Get("/x", func(*Ctx, struct{}) (string, error) {
        calls = append(calls, "handler")
        return "ok", nil
    })
    r.Use(recording(&calls, "d"))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

    if got := strings.Join(calls, ","); got != "a,b,c,handler" {
        t.Fatalf("calls = %q, want %q", got, "a,b,c,handler")
    }
}

// 子组持有自己的中间件快照：父层在它之后再加，不能串进来。
func TestGroupKeepsOwnMiddleware(t *testing.T) {
    var calls []string

    r := New()
    r.Use(recording(&calls, "a")).Use(recording(&calls, "b")).Use(recording(&calls, "c"))
    api := r.Group("/api")
    api.Use(recording(&calls, "d"))
    r.Use(recording(&calls, "e"))
    api.Get("/x", routerOK)

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))

    if got := strings.Join(calls, ","); got != "a,b,c,d" {
        t.Fatalf("calls = %q, want %q", got, "a,b,c,d")
    }
}

// 子组继承父层中间件的 IN，路由 IN 不一致同样在注册期 panic。
func TestGroupInheritsMiddlewareInput(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, _ routerInput) (any, error) { return nil, c.Next() })

    mustPanicWith(t, "does not match middleware input", func() {
        r.Group("/api").Get("/x", func(_ *Ctx, in otherInput) (otherInput, error) { return in, nil })
    })
}

// 根上 pattern 为空，落到 "/"。
func TestRootEmptyPattern(t *testing.T) {
    r := New()
    r.Get("", routerOK)

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("GET /: status = %d, want %d", rec.Code, http.StatusOK)
    }
}

func recording(calls *[]string, name string) HandlerFunc[struct{}, any] {
    return func(c *Ctx, _ struct{}) (any, error) {
        *calls = append(*calls, name)
        return nil, c.Next()
    }
}

func counting(hits *int) HandlerFunc[struct{}, any] {
    return func(c *Ctx, _ struct{}) (any, error) {
        *hits = *hits + 1
        return nil, c.Next()
    }
}
