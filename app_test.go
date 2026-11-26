package mie

import (
	"context"
	"encoding/json/v2"
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

func TestCtxReuseResetsState(t *testing.T) {
	r := New()

	requests := 0
	var firstTrace string
	seen := make(map[*Ctx]struct{})
	r.Get("/pool", He(func(c *Ctx, _ struct{}) error {
		requests++
		seen[c] = struct{}{}
		if requests == 1 {
			firstTrace = c.TraceID()
			c.Set("leak", "x")
			c.Status(http.StatusCreated)
			return c.Send("one")
		}

		if v := c.Get[string]("leak"); v != "" {
			t.Errorf("store leaked into next request: %q", v)
		}
		if c.TraceID() == firstTrace {
			t.Error("trace id reused across requests")
		}
		if got := c.StatusCode(); got != http.StatusOK {
			t.Errorf("status = %d, want 200", got)
		}
		return c.Send("two")
	}))

	for i := 0; i < 50; i++ {
		want := "two"
		if i == 0 {
			want = "one"
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pool", nil))
		if got := rec.Body.String(); got != want {
			t.Fatalf("request %d: body = %q, want %q", i, got, want)
		}
	}

	// sync.Pool 会被 GC 清空，goroutine 也可能在两次请求间换 P，复用次数不稳定；
	// 这里只断言复用路径确实生效：完全不复用时 50 次请求会拿到 50 个不同的 *Ctx。
	if len(seen) == 50 {
		t.Fatalf("50 requests used 50 distinct *Ctx; pooling is not in the request path")
	}
}

// panic 请求也要把上下文清零后归还，不能把半截状态带进下一个请求。
func TestCtxReuseAfterPanic(t *testing.T) {
	r := New(Config{RecoverHandler: func(*Ctx, *Panic) error { return Status500InternalServerError() }})

	r.Get("/panics", He(func(c *Ctx, _ struct{}) error {
		c.Set("leak", "x")
		c.Status(http.StatusCreated)
		panic("boom")
	}))
	r.Get("/clean", He(func(c *Ctx, _ struct{}) error {
		if v := c.Get[string]("leak"); v != "" {
			t.Errorf("store leaked after panic: %q", v)
		}
		if got := c.StatusCode(); got != http.StatusOK {
			t.Errorf("status after panic = %d, want 200", got)
		}
		return c.Send("ok")
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panics", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic request status = %d, want 500", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clean", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("after panic: status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

func TestCtxReuseAfterAbort(t *testing.T) {
	r := New()

	var seen []*Ctx
	r.Get("/abort", He(func(c *Ctx, _ struct{}) error {
		seen = append(seen, c)
		panic(http.ErrAbortHandler)
	}))

	for range 20 {
		func() {
			defer func() {
				if p := recover(); p != http.ErrAbortHandler {
					t.Fatalf("panic = %v, want http.ErrAbortHandler", p)
				}
			}()
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
		}()
	}

	distinct := make(map[*Ctx]struct{}, len(seen))
	for _, c := range seen {
		distinct[c] = struct{}{}
	}
	// 同 TestCtxReuseResetsState：GC 清空池、换 P 都会让复用次数波动，只断言存在复用。
	if len(distinct) == 20 {
		t.Fatalf("20 aborted requests used 20 distinct *Ctx; aborted contexts are not returned to the pool")
	}
}

func TestRouteParam(t *testing.T) {
	r := New()
	r.Handle(http.MethodGet, "/users/{id}", He(func(c *Ctx, _ struct{}) error {
		_, _ = c.W.WriteString("user:" + c.Param("id"))
		return nil
	}))

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
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

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
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { served++; return struct{}{}, nil })

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

func TestNoMatchHandlerWritesNothing(t *testing.T) {
	r := New(Config{NoMatchHandler: func(*Ctx) error { return nil }})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	// 405：保留 Allow，body 为空。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("body = %q, want empty（默认 405 文本已被丢弃）", got)
	}
	if got := rec.Result().Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if got := rec.Result().Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", got, "text/plain; charset=utf-8")
	}
	if got := rec.Result().Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}

	// 404：body 为空，无 Allow。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("body = %q, want empty", got)
	}
	if got := rec.Result().Header.Get("Allow"); got != "" {
		t.Fatalf("Allow = %q, want empty", got)
	}
}

// 钩子只设置状态、不写 body：按设置的状态提交。
func TestNoMatchHandlerStatusOnly(t *testing.T) {
	r := New(Config{NoMatchHandler: func(c *Ctx) error {
		c.Status(http.StatusNoContent)
		return nil
	}})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("body = %q, want empty", got)
	}
}

func TestNoMatchHandlerAccessLog(t *testing.T) {
	lg, buf := testLogger()
	r := New(Config{
		NoMatchHandler: func(*Ctx) error { return nil },
		Logger:         lg,
	})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/health"},
		{http.MethodGet, "/nope"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
	}

	logs := buf.String()
	if n := strings.Count(logs, "msg=access"); n != 2 {
		t.Fatalf("access logs = %d, want 2\n%s", n, logs)
	}
	// 钩子接管后的 404/405 与框架自己的 404/405 同档：Trace 下可见，Info 下静默。
	for _, want := range []string{"level=DEBUG-4", "status=405", "status=404"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestNoMatchHandlerPanicWrites500(t *testing.T) {
	lg, _ := testLogger()
	r := New(Config{
		NoMatchHandler: func(*Ctx) error { panic("hook boom") },
		Logger:         lg,
	})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); body != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("body = %q, want %q", body, http.StatusText(http.StatusInternalServerError))
	}
}

func TestNoMatchHandlerMayRewriteStatus(t *testing.T) {
	r := New(Config{NoMatchHandler: func(c *Ctx) error {
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.WriteString("ok")
		return nil
	}})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status=%d body=%q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestNoMatchHandlerErrorIsNotInterceptedTwice(t *testing.T) {
	calls := 0
	r := New(Config{NoMatchHandler: func(*Ctx) error {
		calls++
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
	if calls != 1 {
		t.Fatalf("NoMatchHandler calls = %d, want 1", calls)
	}
}

func TestNoMatchHandlerSeesStatus(t *testing.T) {
	var statuses []int
	r := New(Config{NoMatchHandler: func(c *Ctx) error {
		statuses = append(statuses, c.StatusCode())
		_, _ = c.W.WriteString("hooked")
		return nil
	}})
	r.Get("/health", func(*Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })

	// 路径存在、方法不对：405，Allow 头保留。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	if got := rec.Result().Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if got := rec.Result().Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", got, "text/plain; charset=utf-8")
	}
	if got := rec.Result().Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := rec.Body.String(); got != "hooked" {
		t.Fatalf("body = %q, want hook-written body without native 405 text", got)
	}

	// 路径不存在：404。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if got := rec.Body.String(); got != "hooked" {
		t.Fatalf("body = %q, want hook-written body without native 404 text", got)
	}

	if len(statuses) != 2 || statuses[0] != http.StatusMethodNotAllowed || statuses[1] != http.StatusNotFound {
		t.Fatalf("hook statuses = %v, want [405 404]", statuses)
	}
}

func TestNoMatchHandlerIgnoresBusiness404(t *testing.T) {
	calls := 0
	r := New(Config{NoMatchHandler: func(*Ctx) error {
		calls++
		return nil
	}})
	r.Get("/missing", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, NewError(http.StatusNotFound, "account gone")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if calls != 0 {
		t.Fatalf("hook calls = %d, want 0（业务 404 不是未命中）", calls)
	}
	if rec.Code != http.StatusNotFound || rec.Body.String() != "account gone" {
		t.Fatalf("status=%d body=%q, want business 404", rec.Code, rec.Body.String())
	}
}

func TestNoMatchHandlerIgnoresManual404(t *testing.T) {
	calls := 0
	r := New(Config{NoMatchHandler: func(*Ctx) error {
		calls++
		return nil
	}})
	r.Handle(http.MethodGet, "/manual", He(func(c *Ctx, _ struct{}) error {
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.WriteString("manual 404")
		return nil
	}))
	r.Handle(http.MethodGet, "/teapot", He(func(c *Ctx, _ struct{}) error {
		http.Error(c.W, "nope", http.StatusMethodNotAllowed)
		return nil
	}))

	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/manual", http.StatusNotFound, "manual 404"},
		{"/teapot", http.StatusMethodNotAllowed, "nope\n"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status || rec.Body.String() != tc.body {
			t.Fatalf("%s status=%d body=%q, want %d %q", tc.path, rec.Code, rec.Body.String(), tc.status, tc.body)
		}
	}

	if calls != 0 {
		t.Fatalf("hook calls = %d, want 0（业务自己写的 404/405 不应被接管）", calls)
	}
}

func TestDefaultErrorHandlerWritesResponse(t *testing.T) {
	r := New()
	r.Get("/teapot", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, NewError(http.StatusTeapot, "short and stout")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/teapot", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Body.String(); got != "short and stout" {
		t.Fatalf("body = %q, want %q", got, "short and stout")
	}
	if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
		t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
	}
	if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
	}
}

func TestDefaultErrorHandlerUnknownError(t *testing.T) {
	r := New()
	r.Get("/boom", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, errors.New("db down")
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
	r.Get("/partial", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.W.WriteHeader(http.StatusCreated)
		_, _ = c.W.WriteString("created")
		return struct{}{}, errors.New("late failure")
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

func TestSendSkipsBodyForStatusesWithoutBody(t *testing.T) {
	for _, status := range []int{http.StatusSwitchingProtocols, http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			r := New()
			r.Get("/no-content", func(c *Ctx, _ struct{}) (struct{}, error) {
				c.Status(status)
				return struct{}{}, nil
			})

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-content", nil))

			if rec.Code != status {
				t.Fatalf("status = %d, want %d", rec.Code, status)
			}
			if got := rec.Body.String(); got != "" {
				t.Fatalf("body = %q, want empty", got)
			}
		})
	}
}

// 用户声明 text/plain 但没设 nosniff 时，框架补上。
func TestWriteErrorAddsNosniffToUserTextPlain(t *testing.T) {
	r := New()
	r.Get("/plain", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Header(HeaderContentType, "text/plain; charset=utf-8")
		return struct{}{}, NewError(http.StatusBadRequest, "bad input")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plain", nil))

	if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
	}
}

// 锁定契约：ErrorHandler 返回 nil 表示已自行处理，框架不再写任何响应。
func TestErrorHandlerNilLeavesResponseUntouched(t *testing.T) {
	r := New(Config{ErrorHandler: func(*Ctx, error) error { return nil }})
	r.Get("/handled", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, errors.New("handled elsewhere")
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

// 锁定：取消引起的错误不写响应体，状态记 499，访问日志仍记一条。
func TestCanceledRequestWritesNoBody(t *testing.T) {
	l, buf := testLogger()
	r := New(Config{Logger: l})
	r.Get("/canceled", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, context.Canceled
	})

	req := httptest.NewRequest(http.MethodGet, "/canceled", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != Status499ClientClosedRequest {
		t.Fatalf("status = %d, want %d", rec.Code, Status499ClientClosedRequest)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
	if out := buf.String(); !strings.Contains(out, "msg=access") || !strings.Contains(out, "status=499") || !strings.Contains(out, "level=INFO") {
		t.Fatalf("日志 = %q，want msg=access、status=499、level=INFO", out)
	}
}

// 锁定：取消时 handler 报的真实错误照常写，状态码保留给日志与指标。
func TestCanceledRealErrorKeepsStatus(t *testing.T) {
	l, buf := testLogger()
	r := New(Config{Logger: l})
	r.Get("/failed", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, NewError(http.StatusServiceUnavailable, "db down")
	})

	req := httptest.NewRequest(http.MethodGet, "/failed", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("body = %q，want 错误文本", rec.Body.String())
	}
	if out := buf.String(); !strings.Contains(out, "status=503") || !strings.Contains(out, "level=ERROR") {
		t.Fatalf("日志 = %q，want status=503、level=ERROR", out)
	}
}

// 锁定：取消错误不进入用户 ErrorHandler；普通错误照常进入。
func TestCanceledErrorSkipsErrorHandler(t *testing.T) {
	var calls int
	r := New(Config{ErrorHandler: func(_ *Ctx, err error) error {
		calls++
		return err
	}})
	r.Get("/canceled", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, context.Canceled
	})
	r.Get("/failed", func(*Ctx, struct{}) (struct{}, error) {
		return struct{}{}, errors.New("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/canceled", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if calls != 0 {
		t.Fatalf("ErrorHandler calls = %d, want 0 for canceled request", calls)
	}

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/failed", nil))

	if calls != 1 {
		t.Fatalf("ErrorHandler calls = %d, want 1 for failed request", calls)
	}
}

func TestWriteErrorOverridesContentType(t *testing.T) {
	r := New()
	r.Get("/html", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Header(HeaderContentType, MIMETextHTMLUTF8)
		return struct{}{}, NewError(http.StatusBadRequest, "<script>alert(1)</script>")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
		t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
	}
	if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
	}
	if got := rec.Body.String(); got != "<script>alert(1)</script>" {
		t.Fatalf("body = %q, want %q", got, "<script>alert(1)</script>")
	}
}

func TestPanicWritesDefault500(t *testing.T) {
	r := New()
	r.Get("/panic", func(*Ctx, struct{}) (struct{}, error) { panic("boom") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Body.String(); got != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("body = %q, want %q", got, http.StatusText(http.StatusInternalServerError))
	}
	if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
	}
}

func TestPanicAfterWriteAbortsResponse(t *testing.T) {
	l, buf := testLogger()
	r := New(Config{Logger: l})
	r.Get("/half", func(c *Ctx, _ struct{}) (struct{}, error) {
		_, _ = c.W.WriteString("half")
		panic("boom")
	})

	s := httptest.NewTestServer(t, r)

	resp, err := s.Client().Get(s.URL + "/half")
	if err == nil {
		defer resp.Body.Close()
		if _, rerr := io.ReadAll(resp.Body); rerr == nil {
			t.Fatal("response completed; want aborted connection")
		}
	}

	if n := strings.Count(buf.String(), "msg=access"); n != 1 {
		t.Fatalf("访问日志条数 = %d, want 1", n)
	}
}

// 锁定：RecoverHandler 自己 panic 时，请求仍留一条访问日志。
func TestPanicRecoverHandlerPanicStillLogs(t *testing.T) {
	l, buf := testLogger()
	r := New(Config{
		Logger:         l,
		RecoverHandler: func(*Ctx, *Panic) error { panic("recover handler boom") },
	})
	r.Get("/boom", func(*Ctx, struct{}) (struct{}, error) { panic("boom") })

	s := httptest.NewServer(r)
	defer s.Close()

	if resp, err := http.Get(s.URL + "/boom"); err == nil {
		defer resp.Body.Close()
		if _, rerr := io.ReadAll(resp.Body); rerr == nil {
			t.Fatal("response completed; want aborted connection")
		}
	}

	if !strings.Contains(buf.String(), "msg=access") {
		t.Fatalf("日志 = %q，缺少 RecoverHandler panic 的访问日志", buf.String())
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
func (h *accessRecordHandler) WithGroup(string) slog.Handler      { return h }

// 锁定：连接被 Hijack 后框架不再写连接，handler 写的字节原样到达客户端。
func TestWriteErrorAfterHijackLeavesConnectionAlone(t *testing.T) {
	h := &accessRecordHandler{access: make(chan slog.Record, 1)}
	r := New(Config{Logger: slog.New(h)})
	r.Get("/hijack", func(c *Ctx, _ struct{}) (struct{}, error) {
		conn, _, err := c.W.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return struct{}{}, err
		}
		defer conn.Close()

		if _, err := conn.Write([]byte("hijacked\n")); err != nil {
			t.Errorf("write hijacked connection: %v", err)
		}
		return struct{}{}, NewError(http.StatusBadRequest, "late")
	})

	s := httptest.NewTestServer(t, r)
	s.Start()

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
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "msg=access") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("access 日志条数 = %d, want 1\n%s", n, buf.String())
	}
}

// 非法 UTF-8 不能把整条 JSON 吞掉。
func TestPanicJSONKeepsInvalidUTF8(t *testing.T) {
	p := &Panic{Error: "boom \xff", Path: "/\xff"}

	raw := p.JSON()

	var got struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("JSON() = %q, err = %v", raw, err)
	}
	if !strings.Contains(got.Error, "boom") || got.Path == "" {
		t.Fatalf("字段丢了：%+v", got)
	}
}
