package mie

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

type typedApp struct {
    *Ctx
    User string
}

func TestWithContextFactoryRunsOnce(t *testing.T) {
    runs := 0
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp {
        runs++
        return &typedApp{Ctx: c}
    })
    app.Use(func(a *typedApp, _ struct{}) (any, error) { return nil, a.Next() })
    app.Get("/once", func(a *typedApp, _ struct{}) (string, error) { return "ok", nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/once", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if runs != 1 {
        t.Fatalf("factory runs = %d, want 1", runs)
    }
}

func TestTypedMiddlewareSharesPointer(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Use(func(a *typedApp, _ struct{}) (any, error) {
        a.User = "alice"
        return nil, a.Next()
    })
    app.Get("/me", func(a *typedApp, _ struct{}) (string, error) { return a.User, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))

    if rec.Body.String() != "alice" {
        t.Fatalf("body = %s, want alice", rec.Body.String())
    }
}

func TestTypedRouterUsePlainMiddleware(t *testing.T) {
    var order []string
    runs := 0
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp {
        runs++
        order = append(order, "factory")
        return &typedApp{Ctx: c, User: c.Get[string]("user")}
    })
    app.Use(func(c *Ctx, _ struct{}) (any, error) {
        order = append(order, "plain")
        c.Set("user", "alice")
        return nil, c.Next()
    })
    app.Use(HandlerFunc[struct{}, any](func(c *Ctx, _ struct{}) (any, error) {
        order = append(order, "named")
        return nil, c.Next()
    }))
    app.Use(func(a *typedApp, _ struct{}) (any, error) {
        order = append(order, "typed")
        return nil, a.Next()
    })
    app.Get("/mixed", func(a *typedApp, _ struct{}) (string, error) { return a.User, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mixed", nil))

    if got := strings.Join(order, ","); got != "plain,named,factory,typed" {
        t.Fatalf("order = %s, want plain,named,factory,typed", got)
    }
    if rec.Body.String() != "alice" {
        t.Fatalf("body = %q, want alice", rec.Body.String())
    }
    if runs != 1 {
        t.Fatalf("factory runs = %d, want 1", runs)
    }
}

func TestTypedRouterUseRejectsForeignContext(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })

    defer func() {
        got, _ := recover().(string)
        if !strings.Contains(got, "*Ctx or *mie.typedApp") {
            t.Fatalf("panic = %q, want a message naming both context types", got)
        }
    }()

    app.Use(func(x int, _ struct{}) (any, error) { return nil, nil })
}

func TestTypedHandlerCachesContextPerRequest(t *testing.T) {
    r := New()
    runs := 0
    app := r.WithContext(func(c *Ctx) *typedApp {
        runs++
        return &typedApp{Ctx: c}
    })
    app.Use(func(a *typedApp, _ struct{}) (any, error) {
        a.User = "alice"
        return nil, a.Next()
    })
    app.Get("/app", func(a *typedApp, _ struct{}) (string, error) { return a.User, nil })

    for range 2 {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app", nil))

        if body := rec.Body.String(); body != "alice" {
            t.Fatalf("body = %q, want %q", body, "alice")
        }
    }

    if runs != 2 {
        t.Fatalf("factory runs = %d, want 2 (once per request, not once per use)", runs)
    }
}

func TestTypedRouterUseInputConflict(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Use(func(a *typedApp, in routerInput) (any, error) { return nil, a.Next() })

    defer func() {
        if recover() == nil {
            t.Fatal("want panic on input mismatch")
        }
    }()

    app.Get("/x", func(a *typedApp, _ struct{}) (string, error) { return "", nil })
}

func TestTypedRouterGroupKeepsFactory(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c, User: "bob"} })
    app.Group("/api").Get("/me", func(a *typedApp, _ struct{}) (string, error) { return a.User, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "bob" {
        t.Fatalf("status = %d, body = %s, want 200 and bob", rec.Code, rec.Body.String())
    }
}

func TestTypedRouterPerRequestIsolation(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Get("/n", func(a *typedApp, _ struct{}) (string, error) {
        a.User += "x"
        return a.User, nil
    })

    for i := 0; i < 2; i++ {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/n", nil))

        if rec.Body.String() != "x" {
            t.Fatalf("request %d body = %s, want x", i, rec.Body.String())
        }
    }
}

func TestTypedRouterCoexistsWithPlain(t *testing.T) {
    r := New()
    r.Get("/plain", func(c *Ctx, _ struct{}) (string, error) { return "p", nil })
    r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} }).
        Get("/typed", func(a *typedApp, _ struct{}) (string, error) { return "t", nil })

    cases := []struct{ path, body string }{
        {"/plain", "p"},
        {"/typed", "t"},
    }

    for _, tc := range cases {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

        if rec.Code != http.StatusOK || rec.Body.String() != tc.body {
            t.Fatalf("GET %s: status = %d, body = %s, want 200 and %s", tc.path, rec.Code, rec.Body.String(), tc.body)
        }
    }
}

func TestTypedMiddlewareBindsInput(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Use(func(a *typedApp, in routerInput) (any, error) {
        a.User = in.Name
        return nil, a.Next()
    })
    app.Get("/in", func(a *typedApp, in routerInput) (string, error) {
        return a.User + "/" + in.Name, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/in?name=alice", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "alice/alice" {
        t.Fatalf("status = %d, body = %s, want 200 and alice/alice", rec.Code, rec.Body.String())
    }
}

func TestWithContextRequiresFactory(t *testing.T) {
    defer func() {
        if recover() == nil {
            t.Fatal("want panic on nil factory")
        }
    }()

    New().WithContext[Ctx](nil)
}

// 工厂必须返回 *C，值类型的上下文已经写不出来；这里覆盖其余三种：没嵌入 *Ctx、C 是 *Ctx、C 是指向接口的指针。
func TestWithContextRequiresEmbeddedCtx(t *testing.T) {
    const want = "mie: WithContext requires C to be a struct embedding *Ctx"

    cases := []struct {
        name string
        f    func()
    }{
        {"wrapper", func() { New().WithContext(func(c *Ctx) *typedWrapper { return &typedWrapper{Ctx: c} }) }},
        {"identity", func() { New().WithContext(func(c *Ctx) *Ctx { return c }) }},
        {"interface", func() { New().WithContext(func(*Ctx) *error { return nil }) }},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            defer func() {
                if p := recover(); p != want {
                    t.Fatalf("panic = %v, want %q", p, want)
                }
            }()

            tc.f()
        })
    }
}

// typedWrapper 持有 *Ctx 但不是嵌入，注册期应被拒绝。
type typedWrapper struct {
    Ctx *Ctx
}

func TestTypedRouterInheritsParentMiddleware(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, _ struct{}) (any, error) {
        c.Set("mw", true)
        return nil, c.Next()
    })
    r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} }).
        Get("/p", func(a *typedApp, _ struct{}) (string, error) {
            if a.Get[bool]("mw") {
                return "seen", nil
            }
            return "missed", nil
        })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p", nil))

    if rec.Body.String() != "seen" {
        t.Fatalf("body = %s, want seen", rec.Body.String())
    }
}

// TypedRouter 也是新的注册面：不继承父层中间件的 IN，父层中间件照常运行。
func TestTypedRouterDoesNotInheritParentInput(t *testing.T) {
    var seen string

    r := New()
    r.Use(func(c *Ctx, in routerInput) (any, error) {
        seen = in.Name
        return nil, c.Next()
    })

    r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} }).
        Get("/x", func(_ *typedApp, in otherInput) (otherInput, error) { return in, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x?id=7&name=bob", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    if seen != "bob" {
        t.Fatalf("middleware input = %q, want bob", seen)
    }
    if got := rec.Body.String(); got != `{"ID":7}` {
        t.Fatalf("body = %s, want %s", got, `{"ID":7}`)
    }
}

func TestTypedRouterHandleCustomMethod(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Use(func(a *typedApp, _ struct{}) (any, error) {
        a.User = "alice"
        return nil, a.Next()
    })
    app.Handle("PROPFIND", "/p", func(a *typedApp, _ struct{}) (string, error) { return a.User, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/p", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
        t.Fatalf("status = %d, body = %s, want 200 and alice", rec.Code, rec.Body.String())
    }
}

// 五个动词必须各自注册到对应方法：写错一个，那条请求会掉进 405。
func TestTypedRouterVerbs(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} })
    app.Use(func(a *typedApp, _ struct{}) (any, error) {
        a.User = "alice"
        return nil, a.Next()
    })

    h := func(a *typedApp, _ struct{}) (string, error) { return a.User, nil }
    app.Get("/verb", h)
    app.Post("/verb", h)
    app.Put("/verb", h)
    app.Patch("/verb", h)
    app.Delete("/verb", h)

    for _, method := range []string{
        http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
    } {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(method, "/verb", nil))

        if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
            t.Fatalf("%s /verb: status = %d, body = %s, want 200 and alice", method, rec.Code, rec.Body.String())
        }
    }
}
