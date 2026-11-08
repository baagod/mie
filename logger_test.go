package mux

import (
    "bytes"
    "context"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func captureLogs(t *testing.T) *bytes.Buffer {
    t.Helper()
    return captureLogsWith(t, nil)
}

// captureLogsWith 把框架 logger 换成写入内存的 TextHandler（可包一层自定义 Handler），
// 测试结束后恢复。
func captureLogsWith(t *testing.T, wrap func(slog.Handler) slog.Handler) *bytes.Buffer {
    t.Helper()
    var buf bytes.Buffer
    var h slog.Handler = slog.NewTextHandler(&buf, nil)
    if wrap != nil {
        h = wrap(h)
    }
    prev := logger
    logger = slog.New(h)
    t.Cleanup(func() { logger = prev })
    return &buf
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
            buf := captureLogs(t)
            r := New()
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
    buf := captureLogsWith(t, func(h slog.Handler) slog.Handler { return enrichHandler{h} })
    r := New()
    r.Get("/users/{id}", func(*Ctx, any) (any, error) { return nil, nil })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/42", nil))

    if out := buf.String(); !strings.Contains(out, "route=42") {
        t.Fatalf("日志 = %q，缺少自定义 Handler 添加的 route=42", out)
    }
}

func TestLogHandlerOverridesDefault(t *testing.T) {
    buf := captureLogs(t)
    userLogger := slog.New(slog.NewTextHandler(buf, nil))
    var calls int

    r := New(Config{
        LogHandler: func(c *Ctx, level slog.Level, msg string, attrs []slog.Attr) {
            calls++
            if c.R.URL.Path == "/health" {
                return
            }
            attrs = append(attrs, slog.String("tenant", "acme"))
            userLogger.LogAttrs(c, level, msg, attrs...)
        },
    })
    r.Get("/health", func(*Ctx, any) (any, error) { return nil, nil })
    r.Get("/teapot", func(*Ctx, any) (any, error) {
        return nil, NewError(http.StatusTeapot, "short and stout")
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/teapot", nil))

    if calls != 2 {
        t.Fatalf("LogHandler 调用次数 = %d，期望 2", calls)
    }

    out := buf.String()
    if strings.Contains(out, "path=/health") {
        t.Fatalf("LogHandler 跳过的请求不应写出日志: %q", out)
    }
    for _, want := range []string{"msg=access", "level=WARN", "status=418", "tenant=acme"} {
        if !strings.Contains(out, want) {
            t.Fatalf("日志 = %q，缺少 %q", out, want)
        }
    }
    if n := strings.Count(out, "msg=access"); n != 1 {
        t.Fatalf("access 记录应只由 LogHandler 写出一次，实际 %d 条: %q", n, out)
    }
}
