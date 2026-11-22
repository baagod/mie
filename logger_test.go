package mie

import (
    "bytes"
    "context"
    "errors"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

// testLogger 返回写入内存的 Logger 及其缓冲区，便于断言日志内容。
func testLogger() (*slog.Logger, *bytes.Buffer) {
    var buf bytes.Buffer
    return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: LevelTrace})), &buf
}

func TestAccessLogWritesRecord(t *testing.T) {
    cases := []struct {
        name    string
        pattern string
        path    string
        handler HandlerFunc[struct{}, struct{}]
        want    []string
        notWant []string
    }{
        {
            name:    "handler error",
            pattern: "/teapot",
            path:    "/teapot",
            handler: func(*Ctx, struct{}) (struct{}, error) {
                return struct{}{}, NewError(http.StatusTeapot, "short and stout")
            },
            want: []string{
                "level=WARN", "msg=access", "status=418", "path=/teapot", "pattern=/teapot",
                `error="short and stout"`, "latency=", "traceid=trace-1", "ip=192.0.2.1",
            },
            notWant: []string{"latency=2562047h"},
        },
        {
            name:    "error after commit",
            pattern: "/late",
            path:    "/late",
            handler: func(c *Ctx, _ struct{}) (struct{}, error) {
                c.W.WriteHeader(http.StatusCreated)
                _, _ = c.W.WriteString("created")
                return struct{}{}, errors.New("late failure")
            },
            want: []string{"level=ERROR", "msg=access", "status=201", `error="late failure"`, "traceid=trace-1"},
        },
        {
            name: "unmatched",
            path: "/missing",
            want: []string{"level=INFO", "msg=access", "status=404", "path=/missing", "latency=", "traceid=trace-1", "ip=192.0.2.1"},
        },
        {
            name:    "panic",
            pattern: "/panic",
            path:    "/panic",
            handler: func(*Ctx, struct{}) (struct{}, error) { panic("boom") },
            want: []string{
                "level=ERROR", "msg=access", "status=500", "latency=", "traceid=trace-1", "ip=192.0.2.1",
            },
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            l, buf := testLogger()
            r := New(Config{Logger: l})
            if tc.pattern != "" {
                r.Get(tc.pattern, tc.handler)
            }

            req := httptest.NewRequest(http.MethodGet, tc.path, nil)
            req.Header.Set(HeaderXRequestID, "trace-1")
            r.ServeHTTP(httptest.NewRecorder(), req)

            out := buf.String()
            for _, want := range tc.want {
                if !strings.Contains(out, want) {
                    t.Fatalf("日志 = %q，缺少 %q", out, want)
                }
            }
            for _, notWant := range tc.notWant {
                if strings.Contains(out, notWant) {
                    t.Fatalf("日志 = %q，不应包含 %q", out, notWant)
                }
            }
        })
    }
}

// 锁定：latency 记录的是真实耗时，不是常量。
func TestAccessLogMeasuresLatency(t *testing.T) {
    l, buf := testLogger()
    r := New(Config{Logger: l})
    r.Get("/slow", func(*Ctx, struct{}) (struct{}, error) {
        time.Sleep(2 * time.Millisecond)
        return struct{}{}, nil
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))

    out := buf.String()
    const marker = "latency="
    _, after, ok := strings.Cut(out, marker)
    if !ok {
        t.Fatalf("日志 = %q，缺少 latency", out)
    }
    field := after
    if j := strings.IndexByte(field, ' '); j >= 0 {
        field = field[:j]
    }
    d, err := time.ParseDuration(field)
    if err != nil {
        t.Fatalf("latency = %q，无法解析: %v（buf=%q）", field, err, out)
    }
    if d < time.Millisecond {
        t.Fatalf("latency = %v，sleep 2ms 后应 ≥ 1ms（buf=%q）", d, out)
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
    r := New(Config{Logger: slog.New(enrichHandler{slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: LevelTrace})})})
    r.Get("/users/{id}", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

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
    r.Get("/teapot", func(*Ctx, struct{}) (struct{}, error) {
        return struct{}{}, NewError(http.StatusTeapot, "short and stout")
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/teapot", nil))

    out := buf.String()
    for _, want := range []string{"level=WARN", "msg=access", "status=418", `error="short and stout"`} {
        if !strings.Contains(out, want) {
            t.Fatalf("日志 = %q，缺少 %q", out, want)
        }
    }
}

// 锁定：ServeMux 的规范化重定向不经过路由闭包，但访问日志仍要记一条。
func TestAccessLogCoversCanonicalRedirect(t *testing.T) {
    l, buf := testLogger()
    r := New(Config{Logger: l})
    r.Get("/x/", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

    if rec.Code != http.StatusTemporaryRedirect {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
    }
    if n := strings.Count(buf.String(), "msg=access"); n != 1 {
        t.Fatalf("access 日志条数 = %d, want 1（buf=%q）", n, buf.String())
    }
    if out := buf.String(); !strings.Contains(out, "level=DEBUG-4") {
        t.Fatalf("日志 = %q，want level=DEBUG-4", out)
    }
}

// 锁定：Ctx 没有 App 时，Log 不 panic，退回框架自己的 logger。
func TestLogWithoutAppFallsBackToDefault(t *testing.T) {
    var buf bytes.Buffer
    prev := logger
    logger = slog.New(slog.NewTextHandler(&buf, nil))
    t.Cleanup(func() { logger = prev })

    c := &Ctx{R: httptest.NewRequest(http.MethodGet, "/orphan", nil)}
    c.LogInfo("orphan log")

    if !strings.Contains(buf.String(), "orphan log") {
        t.Fatalf("日志 = %q，缺少 orphan log", buf.String())
    }
}

// 锁定：命中路由的请求只记一条访问日志（闭包记一次，ServeHTTP 不再补）。
func TestAccessLogWrittenOnceForHandledRoute(t *testing.T) {
    l, buf := testLogger()
    r := New(Config{Logger: l})
    r.Get("/ok", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))

    if n := strings.Count(buf.String(), "msg=access"); n != 1 {
        t.Fatalf("access 日志条数 = %d, want 1（buf=%q）", n, buf.String())
    }
    if out := buf.String(); !strings.Contains(out, "level=DEBUG-4") || !strings.Contains(out, "status=200") {
        t.Fatalf("日志 = %q，want level=DEBUG-4、status=200", out)
    }
}

func TestLoggerReceivesFrameworkLogs(t *testing.T) {
    l, buf := testLogger()

    r := New(Config{Logger: l})
    r.Get("/panic", func(*Ctx, struct{}) (struct{}, error) { panic("boom") })
    r.Get("/late", func(c *Ctx, _ struct{}) (struct{}, error) {
        c.W.WriteHeader(http.StatusCreated)
        _, _ = c.W.WriteString("created")
        return struct{}{}, errors.New("late failure")
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/late", nil))

    out := buf.String()
    for _, want := range []string{"PANIC RECOVERED", "msg=access", "status=201", `error="late failure"`} {
        if !strings.Contains(out, want) {
            t.Fatalf("日志 = %q，缺少 %q", out, want)
        }
    }
}
