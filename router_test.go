package mux

import (
    "context"
    "errors"
    "io"
    "log/slog"
    "net"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

func TestRouteParam(t *testing.T) {
    r := New()
    r.Get("/users/{id}", func(c *Ctx, _ any) (any, error) {
        _, _ = c.W.WriteString("user:" + c.Param("id"))
        return nil, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Body.String(); got != "user:42" {
        t.Fatalf("body = %q, want %q", got, "user:42")
    }
}

func TestUnmatchedPathSkipsErrorHandler(t *testing.T) {
    called := false
    r := New(Config{ErrorHandler: func(_ *Ctx, err error) error { called = true; return err }})
    r.Get("/health", func(*Ctx, any) (any, error) { return nil, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

    if called {
        t.Fatal("ErrorHandler called for unmatched path; want native 404")
    }
    if rec.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
    }
    if got := rec.Body.String(); got != "404 page not found\n" {
        t.Fatalf("body = %q, want native 404 body", got)
    }
}

func TestNoMatchHandler(t *testing.T) {
    calls := 0
    served := 0
    r := New(Config{NoMatchHandler: func(c *Ctx) error {
        calls++
        c.W.WriteHeader(http.StatusNotFound)
        _, _ = c.W.WriteString("hooked")
        return nil
    }})
    r.Get("/health", func(*Ctx, any) (any, error) { served++; return nil, nil })

    for _, target := range []struct{ method, path string }{
        {http.MethodPost, "/health"},
        {http.MethodGet, "/nope"},
    } {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(target.method, target.path, nil))
        if rec.Code != http.StatusNotFound || rec.Body.String() != "hooked" {
            t.Fatalf("%s %s response status=%d body=%q, want hook-written 404",
                target.method, target.path, rec.Code, rec.Body.String())
        }
    }
    if calls != 2 {
        t.Fatalf("hook calls = %d, want 2", calls)
    }

    // 配了 hook 时，命中路由仍应正常派发，不经过 hook。
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("GET /health status = %d, want %d", rec.Code, http.StatusOK)
    }
    if calls != 2 {
        t.Fatalf("hook calls after matched route = %d, want 2", calls)
    }
    if served != 1 {
        t.Fatalf("handler served %d times, want 1", served)
    }
}

func TestNoMatchHandlerReturnsError(t *testing.T) {
    r := New(Config{NoMatchHandler: func(*Ctx) error {
        return NewError(http.StatusNotFound, "gone")
    }})

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

    if rec.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
    }
    if got := rec.Body.String(); got != "gone" {
        t.Fatalf("body = %q, want %q", got, "gone")
    }
}

func TestDefaultErrorHandlerWritesResponse(t *testing.T) {
    r := New()
    r.Get("/teapot", func(*Ctx, any) (any, error) {
        return nil, NewError(http.StatusTeapot, "short and stout")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/teapot", nil))

    if rec.Code != http.StatusTeapot {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
    }
    if got := rec.Body.String(); got != "short and stout" {
        t.Fatalf("body = %q, want %q", got, "short and stout")
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainCharsetUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainCharsetUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

func TestDefaultErrorHandlerUnknownError(t *testing.T) {
    r := New()
    r.Get("/boom", func(*Ctx, any) (any, error) {
        return nil, errors.New("db down")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); got != "db down" {
        t.Fatalf("body = %q, want %q", got, "db down")
    }
}

func TestWriteErrorKeepsWrittenResponse(t *testing.T) {
    r := New()
    r.Get("/partial", func(c *Ctx, _ any) (any, error) {
        c.W.WriteHeader(http.StatusCreated)
        _, _ = c.W.WriteString("created")
        return nil, errors.New("late failure")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
    if got := rec.Body.String(); got != "createdlate failure" {
        t.Fatalf("body = %q, want %q", got, "createdlate failure")
    }
}

func TestRouteCommitsPresetStatus(t *testing.T) {
    r := New()
    r.Get("/no-content", func(c *Ctx, _ any) (any, error) {
        c.Status(http.StatusNoContent)
        return nil, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-content", nil))

    if rec.Code != http.StatusNoContent {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
    }
}

// 用户声明 text/plain 但没设 nosniff 时，框架补上。
func TestWriteErrorAddsNosniffToUserTextPlain(t *testing.T) {
    r := New()
    r.Get("/plain", func(c *Ctx, _ any) (any, error) {
        c.SetHeader(HeaderContentType, "text/plain; charset=utf-8")
        return nil, NewError(http.StatusBadRequest, "bad input")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plain", nil))

    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

// 锁定契约：ErrorHandler 返回 nil 表示已自行处理，框架不再写任何响应。
func TestErrorHandlerNilLeavesResponseUntouched(t *testing.T) {
    r := New(Config{ErrorHandler: func(*Ctx, error) error { return nil }})
    r.Get("/handled", func(*Ctx, any) (any, error) {
        return nil, errors.New("handled elsewhere")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/handled", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if rec.Body.Len() != 0 {
        t.Fatalf("body = %q, want empty", rec.Body.String())
    }
}

func TestWriteErrorKeepsCustomContentType(t *testing.T) {
    r := New()
    r.Get("/json", func(c *Ctx, _ any) (any, error) {
        c.SetHeader(HeaderContentType, "application/json")
        return nil, NewError(http.StatusBadRequest, "bad input")
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/json", nil))

    if rec.Code != http.StatusBadRequest {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
    }
    if got := rec.Header().Get(HeaderContentType); got != "application/json" {
        t.Fatalf("Content-Type = %q, want %q", got, "application/json")
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != "" {
        t.Fatalf("X-Content-Type-Options = %q, want empty", got)
    }
}

func TestPanicWritesDefault500(t *testing.T) {
    r := New()
    r.Get("/panic", func(*Ctx, any) (any, error) { panic("boom") })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); got != http.StatusText(http.StatusInternalServerError) {
        t.Fatalf("body = %q, want %q", got, http.StatusText(http.StatusInternalServerError))
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != ContentTypeOptionsNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, ContentTypeOptionsNosniff)
    }
}

func TestPanicAfterWriteAbortsResponse(t *testing.T) {
    l, buf := testLogger()
    r := New(Config{Logger: l})
    r.Get("/half", func(c *Ctx, _ any) (any, error) {
        _, _ = c.W.WriteString("half")
        panic("boom")
    })

    s := httptest.NewServer(r)
    defer s.Close()

    resp, err := http.Get(s.URL + "/half")
    if err == nil {
        defer resp.Body.Close()
        if _, rerr := io.ReadAll(resp.Body); rerr == nil {
            t.Fatal("response completed; want aborted connection")
        }
    }

    if !strings.Contains(buf.String(), "msg=access") {
        t.Fatalf("日志 = %q，缺少响应已提交后 panic 的访问日志", buf.String())
    }
}

// accessRecordHandler 在收到 msg=access 时把记录送到 channel，供异步路径断言。
type accessRecordHandler struct {
    access chan slog.Record
}

func (h *accessRecordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *accessRecordHandler) Handle(_ context.Context, r slog.Record) error {
    if r.Message == "access" {
        select {
        case h.access <- r:
        default:
        }
    }
    return nil
}

func (h *accessRecordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *accessRecordHandler) WithGroup(string) slog.Handler     { return h }

// 锁定：连接被 Hijack 后框架不再写连接，handler 写的字节原样到达客户端。
func TestWriteErrorAfterHijackLeavesConnectionAlone(t *testing.T) {
    h := &accessRecordHandler{access: make(chan slog.Record, 1)}
    r := New(Config{Logger: slog.New(h)})
    r.Get("/hijack", func(c *Ctx, _ any) (any, error) {
        conn, _, err := c.W.Hijack()
        if err != nil {
            t.Errorf("Hijack: %v", err)
            return nil, err
        }
        defer conn.Close()

        if _, err := conn.Write([]byte("hijacked\n")); err != nil {
            t.Errorf("write hijacked connection: %v", err)
        }
        return nil, NewError(http.StatusBadRequest, "late")
    })

    s := httptest.NewServer(r)
    defer s.Close()

    conn, err := net.Dial("tcp", s.Listener.Addr().String())
    if err != nil {
        t.Fatalf("dial: %v", err)
    }
    defer conn.Close()

    if _, err := io.WriteString(conn, "GET /hijack HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
        t.Fatalf("write request: %v", err)
    }

    got, err := io.ReadAll(conn)
    if err != nil {
        t.Fatalf("read: %v", err)
    }
    if string(got) != "hijacked\n" {
        t.Fatalf("connection = %q, want %q", got, "hijacked\n")
    }

    select {
    case rec := <-h.access:
        status := int64(-1)
        rec.Attrs(func(a slog.Attr) bool {
            if a.Key == "status" {
                status = a.Value.Int64()
            }
            return true
        })
        if status != 0 {
            t.Fatalf("access status = %d, want 0（Hijack 后未写状态码）", status)
        }
    case <-time.After(2 * time.Second):
        t.Fatal("没有记录 Hijack 请求的 access 日志")
    }
}

func TestNoMatchHandlerPanicLogsOnce(t *testing.T) {
    logger, buf := testLogger()
    r := New(Config{
        Logger: logger,
        NoMatchHandler: func(*Ctx) error {
            panic("boom")
        },
    })

    func() {
        defer func() { _ = recover() }()
        r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))
    }()

    var n int
    for _, line := range strings.Split(buf.String(), "\n") {
        if strings.Contains(line, "msg=access") {
            n++
        }
    }
    if n != 1 {
        t.Fatalf("access 日志条数 = %d, want 1\n%s", n, buf.String())
    }
}
