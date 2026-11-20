package mie

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

type typedApp struct {
    *Ctx
    User string
}

func TestWithContextIdentity(t *testing.T) {
    r := New()
    r.WithContext(func(c *Ctx) *Ctx { return c }).
        Get("/id", func(c *Ctx, _ struct{}) (string, error) { return "ok", nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/id", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
        t.Fatalf("status = %d, body = %s, want 200 and ok", rec.Code, rec.Body.String())
    }
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

    New().WithContext[*Ctx](nil)
}

func TestTypedValueContextIsSnapshot(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) typedApp { return typedApp{Ctx: c} })
    app.Use(func(a typedApp, _ struct{}) (any, error) {
        a.User = "alice"
        return nil, a.Next()
    })
    app.Get("/v", func(a typedApp, _ struct{}) (string, error) { return "user=" + a.User, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "user=" {
        t.Fatalf("status = %d, body = %s, want 200 and user=", rec.Code, rec.Body.String())
    }
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

func TestTypedRouterInheritsParentInput(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, in routerInput) (any, error) { return nil, c.Next() })

    defer func() {
        if recover() == nil {
            t.Fatal("want panic when typed route input differs from parent middleware input")
        }
    }()

    r.WithContext(func(c *Ctx) *typedApp { return &typedApp{Ctx: c} }).
        Get("/x", func(a *typedApp, _ struct{}) (string, error) { return "", nil })
}

// typedIface 用来验证 C 是接口类型时也能注册中间件：接口自己声明了 Next()，满足 TypedContext。
type typedIface interface {
    Next() error
    User() string
    SetUser(string)
}

type typedIfaceImpl struct {
    *Ctx
    user string
}

func (a *typedIfaceImpl) User() string     { return a.user }
func (a *typedIfaceImpl) SetUser(u string) { a.user = u }

func TestTypedRouterAcceptsInterfaceContext(t *testing.T) {
    r := New()
    app := r.WithContext(func(c *Ctx) typedIface { return &typedIfaceImpl{Ctx: c} })
    app.Use(func(a typedIface, _ struct{}) (any, error) {
        a.SetUser("alice")
        return nil, a.Next()
    })
    app.Get("/i", func(a typedIface, _ struct{}) (string, error) { return a.User(), nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/i", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
        t.Fatalf("status = %d, body = %s, want 200 and alice", rec.Code, rec.Body.String())
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

// 指针或接口类型的 C 都可能返回 nil；指针类型的 nil 装进接口后不为 nil，
// typedHandler 里的检查只覆盖接口类型的 nil，指针类型的 nil 会落到下游解引用。
func TestWithContextNilFactoryResult(t *testing.T) {
    var got any
    r := New(Config{
        RecoverHandler: func(_ *Ctx, p *Panic) error {
            got = p.Value
            return NewError(http.StatusInternalServerError, "boom")
        },
    })
    app := r.WithContext(func(*Ctx) typedIface { return nil })
    app.Use(func(a typedIface, _ struct{}) (any, error) { return nil, a.Next() })
    app.Get("/nil", func(typedIface, struct{}) (string, error) { return "ok", nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nil", nil))

    if got != "mie: WithContext factory returned nil" {
        t.Fatalf("panic value = %v, want the factory nil message", got)
    }
    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want 500", rec.Code)
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
