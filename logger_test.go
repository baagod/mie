package mux

import (
    "bytes"
    "context"
    "errors"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

// testLogger 返回写入内存的 Logger 及其缓冲区，便于断言日志内容。
func testLogger() (*slog.Logger, *bytes.Buffer) {
    var buf bytes.Buffer
    return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestAccessLogWritesRecord(t *testing.T) {
    cases := []struct {
        name    string
        pattern string
        path    string
        handler Handler
        want    []string
    }{
        {
            name:    "handler error",
            pattern: "/teapot",
            path:    "/teapot",
            handler: func(*Ctx, any) (any, error) {
                return nil, NewError(http.StatusTeapot, "short and stout")
            },
            want: []string{"level=WARN", "msg=access", "status=418", "path=/teapot", "pattern=/teapot", `error="short and stout"`},
        },
        {
            name: "unmatched",
            path: "/missing",
            want: []string{"level=WARN", "msg=access", "status=404", "path=/missing"},
        },
        {
            name:    "panic",
            pattern: "/panic",
            path:    "/panic",
            handler: func(*Ctx, any) (any, error) { panic("boom") },
            want:    []string{"level=ERROR", "msg=access", "status=500"},
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            l, buf := testLogger()
            r := New(Config{Logger: l})
            if tc.pattern != "" {
                r.Get(tc.pattern, tc.handler)
            }

            r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))

            out := buf.String()
            for _, want := range tc.want {
                if !strings.Contains(out, want) {
                    t.Fatalf("日志 = %q，缺少 %q", out, want)
                }
            }
        })
    }
}

// enrichHandler 验证自定义 Handler 能通过 ctx 取回 *Ctx。
type enrichHandler struct{ slog.Handler }

func (h enrichHandler) Handle(ctx context.Context, r slog.Record) error {
    if c, ok := ctx.(*Ctx); ok {
        r.AddAttrs(slog.String("route", c.Param("id")))
    }
    return h.Handler.Handle(ctx, r)
}

func (h enrichHandler) WithAttrs(as []slog.Attr) slog.Handler {
    return enrichHandler{h.Handler.WithAttrs(as)}
}

func (h enrichHandler) WithGroup(name string) slog.Handler {
    return enrichHandler{h.Handler.WithGroup(name)}
}

func TestHandlerCanReadCtx(t *testing.T) {
    var buf bytes.Buffer
    r := New(Config{Logger: slog.New(enrichHandler{slog.NewTextHandler(&buf, nil)})})
    r.Get("/users/{id}", func(*Ctx, any) (any, error) { return nil, nil })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/42", nil))

    if out := buf.String(); !strings.Contains(out, "route=42") {
        t.Fatalf("日志 = %q，缺少自定义 Handler 添加的 route=42", out)
    }
}

func TestNewDefaultsLogger(t *testing.T) {
    r := New()
    if r.config.Logger == nil {
        t.Fatal("New 应填入默认 Logger")
    }

    l, _ := testLogger()
    r = New(Config{Logger: l})
    if r.config.Logger != l {
        t.Fatal("New 不应覆盖用户传入的 Logger")
    }
}

func TestLoggerOverridesDefault(t *testing.T) {
    l, buf := testLogger()

    r := New(Config{Logger: l})
    r.Get("/teapot", func(*Ctx, any) (any, error) {
        return nil, NewError(http.StatusTeapot, "short and stout")
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/teapot", nil))

    out := buf.String()
    for _, want := range []string{"level=WARN", "msg=access", "status=418", `error="short and stout"`} {
        if !strings.Contains(out, want) {
            t.Fatalf("日志 = %q，缺少 %q", out, want)
        }
    }
}

func TestLoggerReceivesFrameworkLogs(t *testing.T) {
    l, buf := testLogger()

    r := New(Config{Logger: l})
    r.Get("/panic", func(*Ctx, any) (any, error) { panic("boom") })
    r.Get("/late", func(c *Ctx, _ any) (any, error) {
        c.W.WriteHeader(http.StatusCreated)
        _, _ = c.W.WriteString("created")
        return nil, errors.New("late failure")
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/late", nil))

    out := buf.String()
    for _, want := range []string{"PANIC RECOVERED", "mux: error after response committed", "msg=access"} {
        if !strings.Contains(out, want) {
            t.Fatalf("日志 = %q，缺少 %q", out, want)
        }
    }
}
