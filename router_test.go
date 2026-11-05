package mux

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func panicLeaf() { panic("boom") }

func panicMiddle() { panicLeaf() }

func panicTop() { panicMiddle() }

func TestRouterResponses(t *testing.T) {
	r := New()
	r.Use(func(c *Ctx, in any) (any, error) { return c.Next(in) })

	r.Get("/ok", func(c *Ctx, _ struct{}) (string, error) {
		return "ok", nil
	})
	r.Get("/bad", func(c *Ctx, _ struct{}) (string, error) {
		return "", NewError(http.StatusBadRequest, "bad request")
	})
	r.Get("/boom", func(c *Ctx, _ struct{}) (struct{}, error) {
		panic("boom")
	})
	r.Get("/fail", func(c *Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, errors.New("db down")
	})

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"ok", "GET", "/ok", http.StatusOK, "\"ok\"\n"},
		{"http error", "GET", "/bad", http.StatusBadRequest, "{\"error\":\"bad request\"}\n"},
		{"panic", "GET", "/boom", http.StatusInternalServerError, "{\"error\":\"Internal Server Error\"}\n"},
		{"plain error", "GET", "/fail", http.StatusInternalServerError, "{\"error\":\"Internal Server Error\"}\n"},
		{"not found", "GET", "/nope", http.StatusNotFound, "{\"error\":\"Not Found\"}\n"},
		{"method not allowed", "POST", "/ok", http.StatusMethodNotAllowed, "{\"error\":\"Method Not Allowed\"}\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, r, tc.method, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if tc.wantStatus == http.StatusMethodNotAllowed && rec.Header().Get("Allow") == "" {
				t.Fatal("405 response missing Allow header")
			}
		})
	}
}

func TestTypedInputAndOutput(t *testing.T) {
	type createUser struct {
		Name string `json:"name"`
	}
	type user struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	r := New()
	r.Post("/users", func(c *Ctx, in createUser) (user, error) {
		return user{ID: 7, Name: in.Name}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"ada"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got user
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != 7 || got.Name != "ada" {
		t.Fatalf("got %+v, want {ID:7 Name:ada}", got)
	}
}

func TestInvalidBodyIs400(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}

	r := New()
	r.Post("/users", func(c *Ctx, i in) (struct{}, error) {
		return struct{}{}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStatusOverride(t *testing.T) {
	r := New()
	r.Get("/created", func(c *Ctx, _ struct{}) (string, error) {
		c.Status(http.StatusCreated)
		return "created", nil
	})

	rec := do(t, r, http.MethodGet, "/created")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
}

func TestUseAfterRoutePanics(t *testing.T) {
	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })

	defer func() {
		if p := recover(); p == nil {
			t.Fatal("Use after registering a route should panic")
		}
	}()
	r.Use(func(c *Ctx, in any) (any, error) { return c.Next(in) })
}

func TestInvalidStatusIsCaughtByRecoverer(t *testing.T) {
	r := New()
	r.Get("/bad-status", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Status(99)
		return struct{}{}, nil
	})

	rec := do(t, r, http.MethodGet, "/bad-status")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestEncodeFailureUsesFunnel(t *testing.T) {
	r := New()
	r.Get("/undecodable", func(c *Ctx, _ struct{}) (chan int, error) {
		return make(chan int), nil
	})

	rec := do(t, r, http.MethodGet, "/undecodable")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "{\"error\":\"Internal Server Error\"}\n" {
		t.Fatalf("body = %q, want generic 500", rec.Body.String())
	}
}

func TestMiddlewareOrder(t *testing.T) {
	var got []string
	r := New()

	r.Use(func(c *Ctx, in any) (any, error) {
		got = append(got, "outer-before")
		out, err := c.Next(in)
		got = append(got, "outer-after")
		return out, err
	})
	r.Use(func(c *Ctx, in any) (any, error) {
		got = append(got, "inner-before")
		out, err := c.Next(in)
		got = append(got, "inner-after")
		return out, err
	})
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) {
		got = append(got, "handler")
		return "x", nil
	})

	do(t, r, http.MethodGet, "/x")

	want := []string{"outer-before", "inner-before", "handler", "inner-after", "outer-after"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestMiddlewareShortCircuit(t *testing.T) {
	called := false
	r := New()
	r.Use(func(c *Ctx, in any) (any, error) {
		return nil, NewError(http.StatusUnauthorized, "unauthorized")
	})
	r.Get("/secret", func(c *Ctx, _ struct{}) (struct{}, error) {
		called = true
		return struct{}{}, nil
	})

	rec := do(t, r, http.MethodGet, "/secret")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("handler should not run when middleware short-circuits")
	}
}

func TestCustomErrorHandler(t *testing.T) {
	var gotErr error
	r := New(Config{ErrorHandler: func(c *Ctx, err error) {
		gotErr = err
		c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
		c.W.WriteHeader(http.StatusTeapot)
		c.W.Write([]byte("custom"))
	}})
	r.Get("/fail", func(c *Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, errors.New("db down")
	})
	r.Get("/boom", func(c *Ctx, _ struct{}) (struct{}, error) {
		panic("boom")
	})

	gotErr = nil
	rec := do(t, r, http.MethodGet, "/fail")
	if rec.Code != http.StatusTeapot || rec.Body.String() != "custom" {
		t.Fatalf("/fail: status = %d body = %q, want 418 custom", rec.Code, rec.Body.String())
	}
	if gotErr == nil {
		t.Fatal("/fail: ErrorHandler did not receive an error")
	}

	// panic 不经过 ErrorHandler：由 Recoverer 直接写 500。
	gotErr = nil
	rec = do(t, r, http.MethodGet, "/boom")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "{\"error\":\"Internal Server Error\"}\n" {
		t.Fatalf("/boom: status = %d body = %q, want generic 500", rec.Code, rec.Body.String())
	}
	if gotErr != nil {
		t.Fatalf("/boom: panic should not reach ErrorHandler, got %v", gotErr)
	}
}

func TestNoContentStatus(t *testing.T) {
	r := New()
	r.Get("/no-content", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Status(http.StatusNoContent)
		return struct{}{}, nil
	})
	r.Get("/no-content-body", func(c *Ctx, _ struct{}) (string, error) {
		c.Status(http.StatusNoContent)
		return "ignored", nil
	})

	for _, path := range []string{"/no-content", "/no-content-body"} {
		rec := do(t, r, http.MethodGet, path)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204", path, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("%s: body = %q, want empty", path, rec.Body.String())
		}
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}

	r := New()
	r.Post("/users", func(c *Ctx, i in) (struct{}, error) { return struct{}{}, nil })

	body := `{"name":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if rec.Body.String() != "{\"error\":\"request body too large\"}\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestNextAfterEnd(t *testing.T) {
	r := New()
	r.Use(func(c *Ctx, in any) (any, error) {
		if _, err := c.Next(in); err != nil {
			return nil, err
		}
		return c.Next(in)
	})
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) { return "x", nil })

	rec := do(t, r, http.MethodGet, "/x")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestNotFoundUsesErrorHandler(t *testing.T) {
	var got error
	r := New(Config{ErrorHandler: func(c *Ctx, err error) {
		got = err
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write([]byte("custom 404"))
	}})

	do(t, r, http.MethodGet, "/nope")

	he, ok := got.(*Error)
	if !ok || he.Status != http.StatusNotFound {
		t.Fatalf("error = %v, want *Error 404", got)
	}
}

func TestEmptyStructSentinel(t *testing.T) {
	type empty struct{}

	r := New()
	r.Get("/named", func(c *Ctx, _ struct{}) (empty, error) { return empty{}, nil })
	r.Get("/any", func(c *Ctx, _ struct{}) (any, error) { return struct{}{}, nil })
	r.Post("/no-input", func(c *Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })

	if rec := do(t, r, http.MethodGet, "/named"); rec.Body.String() != "{}\n" {
		t.Fatalf("named empty struct body = %q, want {}\\n", rec.Body.String())
	}
	if rec := do(t, r, http.MethodGet, "/any"); rec.Body.String() != "" {
		t.Fatalf("any(struct{}) body = %q, want empty", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/no-input", strings.NewReader(`{bad`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("In=struct{} status = %d, want 200 (body skipped)", rec.Code)
	}
}

func TestErrorFunnelEdgeCases(t *testing.T) {
	t.Run("canceled request skips ErrorHandler", func(t *testing.T) {
		called := false
		r := New(Config{ErrorHandler: func(c *Ctx, err error) { called = true }})
		r.Get("/canceled", func(c *Ctx, _ struct{}) (struct{}, error) {
			return struct{}{}, c.R.Context().Err()
		})

		req := httptest.NewRequest(http.MethodGet, "/canceled", nil)
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if called {
			t.Fatal("ErrorHandler called for canceled request")
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("body = %q, want empty", rec.Body.String())
		}
	})

	t.Run("deadline exceeded still goes through ErrorHandler", func(t *testing.T) {
		called := false
		r := New(Config{ErrorHandler: func(c *Ctx, err error) { called = true; http.Error(c.W, "timeout", http.StatusGatewayTimeout) }})
		r.Get("/timeout", func(c *Ctx, _ struct{}) (struct{}, error) {
			return struct{}{}, errors.New("upstream slow")
		})

		req := httptest.NewRequest(http.MethodGet, "/timeout", nil)
		ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
		defer cancel()
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if !called {
			t.Fatal("ErrorHandler skipped for DeadlineExceeded request")
		}
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want 504", rec.Code)
		}
	})

	t.Run("context.Canceled with live request is a normal error", func(t *testing.T) {
		r := New()
		r.Get("/canceled", func(c *Ctx, _ struct{}) (struct{}, error) {
			return struct{}{}, context.Canceled
		})

		rec := do(t, r, http.MethodGet, "/canceled")

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("invalid status falls back to 500", func(t *testing.T) {
		r := New()
		r.Get("/bad-status", func(c *Ctx, _ struct{}) (struct{}, error) {
			return struct{}{}, NewError(0, "oops")
		})

		rec := do(t, r, http.MethodGet, "/bad-status")

		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "{\"error\":\"oops\"}\n" {
			t.Fatalf("got status %d body %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("typed nil error falls back to 500", func(t *testing.T) {
		r := New()
		r.Get("/typed-nil", func(c *Ctx, _ struct{}) (struct{}, error) {
			var e *Error
			return struct{}{}, e
		})

		rec := do(t, r, http.MethodGet, "/typed-nil")

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("http.ErrAbortHandler is re-panicked", func(t *testing.T) {
		r := New()
		r.Get("/abort", func(c *Ctx, _ struct{}) (struct{}, error) {
			panic(http.ErrAbortHandler)
		})

		panicked := false
		func() {
			defer func() {
				if p := recover(); p != nil {
					panicked = true
					if p != http.ErrAbortHandler {
						t.Fatalf("recovered %v, want http.ErrAbortHandler", p)
					}
				}
			}()
			do(t, r, http.MethodGet, "/abort")
		}()
		if !panicked {
			t.Fatal("expected http.ErrAbortHandler to propagate")
		}
	})
}

func TestRecovererMiddleware(t *testing.T) {
	rec := httptest.NewRecorder()
	c := NewCtx(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	c.chain = []Handler{func(*Ctx, any) (any, error) {
		panic("boom")
	}}

	out, err := Recoverer(nil)(c, struct{}{})

	if out != nil {
		t.Fatalf("out = %v, want nil", out)
	}
	if !errors.Is(err, errRecovered) {
		t.Fatalf("err = %v, want errRecovered", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "{\"error\":\"Internal Server Error\"}\n" {
		t.Fatalf("body = %q, want generic 500", rec.Body.String())
	}
}

func TestRecovererRepanicsErrAbortHandler(t *testing.T) {
	c := NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	c.chain = []Handler{func(*Ctx, any) (any, error) {
		panic(http.ErrAbortHandler)
	}}

	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", p)
		}
	}()

	_, _ = Recoverer(nil)(c, struct{}{})
}

func TestRecoverHandlerReceivesPanicInfo(t *testing.T) {
	var info *Panic
	r := New(Config{RecoverHandler: func(c *Ctx, p *Panic) {
		info = p
		c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
		c.W.WriteHeader(http.StatusTeapot)
		_, _ = c.W.Write([]byte("custom panic"))
	}})
	r.Get("/boom", func(c *Ctx, _ struct{}) (struct{}, error) {
		panicTop()
		return struct{}{}, nil
	})

	rec := do(t, r, http.MethodGet, "/boom")

	if rec.Code != http.StatusTeapot || rec.Body.String() != "custom panic" {
		t.Fatalf("status = %d body = %q, want 418 custom panic", rec.Code, rec.Body.String())
	}
	if info == nil {
		t.Fatal("RecoverHandler did not receive PanicInfo")
	}
	if info.Method != http.MethodGet || info.Path != "/boom" {
		t.Fatalf("method/path = %s %s", info.Method, info.Path)
	}
	if info.TraceID == "" || info.TraceID != rec.Header().Get(HeaderXRequestID) {
		t.Fatalf("trace id = %q, response header = %q", info.TraceID, rec.Header().Get(HeaderXRequestID))
	}
	if len(info.Stacks) != maxPanicFrames {
		t.Fatalf("stack frames = %d, want %d", len(info.Stacks), maxPanicFrames)
	}
	for i, f := range info.Stacks {
		if f.File == "" || f.Line <= 0 || f.Func == "" {
			t.Fatalf("frame %d incomplete: %+v", i, f)
		}
		if len(f.Source) == 0 {
			t.Fatalf("frame %d has no source: %+v", i, f)
		}
	}

	text := info.Text()
	for _, want := range []string{"PANIC RECOVERED", "ContentType:", "Error:", "boom", "File: ", "> "} {
		if !strings.Contains(text, want) {
			t.Fatalf("Text() missing %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, "File: "); got != maxPanicFrames {
		t.Fatalf("Text() frame headers = %d, want %d", got, maxPanicFrames)
	}

	var got struct {
		Time   string `json:"time"`
		Method string `json:"method"`
		Path   string `json:"path"`
		Error  string `json:"error"`
		Stack  []struct {
			File string `json:"file"`
			Line int    `json:"line"`
			Func string `json:"func"`
		} `json:"stack"`
	}
	if err := json.Unmarshal([]byte(info.JSON()), &got); err != nil {
		t.Fatalf("JSON() is not valid JSON: %v", err)
	}
	if got.Method != http.MethodGet || got.Path != "/boom" || got.Error != "boom" {
		t.Fatalf("json = %+v", got)
	}
	if len(got.Stack) != len(info.Stacks) {
		t.Fatalf("json stack = %d, want %d", len(got.Stack), len(info.Stacks))
	}
}

func TestRecoverHandlerFallsBackTo500(t *testing.T) {
	r := New(Config{RecoverHandler: func(c *Ctx, p *Panic) {}})
	r.Get("/boom", func(c *Ctx, _ struct{}) (struct{}, error) {
		panic("boom")
	})

	rec := do(t, r, http.MethodGet, "/boom")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "{\"error\":\"Internal Server Error\"}\n" {
		t.Fatalf("body = %q, want generic 500", rec.Body.String())
	}
}

func TestMiddlewareReadingBodyDoesNotBreakDecode(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	r := New()
	r.Use(func(c *Ctx, v any) (any, error) {
		// 中间件读 body 不应影响输入解码：解码已经在中间件之前完成。
		_, _ = io.ReadAll(c.R.Body)
		return c.Next(v)
	})
	r.Post("/users", func(c *Ctx, i in) (string, error) { return i.Name, nil })

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"ada"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "\"ada\"\n" {
		t.Fatalf("status = %d body = %q, want 200 \"ada\"", rec.Code, rec.Body.String())
	}
}

type panicMarshaler struct{}

func (panicMarshaler) MarshalJSON() ([]byte, error) { panic("marshal boom") }

func TestWritePanicIsRecovered(t *testing.T) {
	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (panicMarshaler, error) {
		return panicMarshaler{}, nil
	})

	rec := do(t, r, http.MethodGet, "/x")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "{\"error\":\"Internal Server Error\"}\n" {
		t.Fatalf("body = %q, want generic 500", rec.Body.String())
	}
}

func TestCommittedResponseSkipsErrorHandler(t *testing.T) {
	called := false
	r := New(Config{ErrorHandler: func(c *Ctx, err error) { called = true }})
	r.Use(func(c *Ctx, in any) (any, error) {
		c.W.WriteHeader(http.StatusAccepted)
		return nil, errors.New("late error")
	})
	r.Get("/x", func(c *Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })

	rec := do(t, r, http.MethodGet, "/x")

	if called {
		t.Fatal("ErrorHandler must not run after the response is committed")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestStatusOutOfRangeIsRejected(t *testing.T) {
	r := New()
	r.Get("/interim", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Status(http.StatusEarlyHints)
		return struct{}{}, nil
	})
	r.Get("/bad-error", func(c *Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, NewError(http.StatusEarlyHints, "interim")
	})

	rec := do(t, r, http.MethodGet, "/interim")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("1xx status = %d, want 500", rec.Code)
	}

	rec = do(t, r, http.MethodGet, "/bad-error")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "{\"error\":\"interim\"}\n" {
		t.Fatalf("1xx error = %d %q, want 500 with message", rec.Code, rec.Body.String())
	}
}

func TestTraceIDReusesRequestHeader(t *testing.T) {
	var seen string
	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) {
		seen = c.TraceID()
		return c.TraceID(), nil
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderXRequestID, "req-123")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if seen != "req-123" {
		t.Fatalf("TraceID() = %q, want req-123", seen)
	}
	if rec.Header().Get(HeaderXRequestID) != "req-123" {
		t.Fatalf("response header = %q, want req-123", rec.Header().Get(HeaderXRequestID))
	}
	if rec.Body.String() != "\"req-123\"\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestTraceIDIsGeneratedAndEchoed(t *testing.T) {
	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) { return c.TraceID(), nil })

	var ids []string
	for range 2 {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

		header := rec.Header().Get(HeaderXRequestID)
		if header == "" {
			t.Fatal("generated trace id missing from response header")
		}
		if len(header) != 32 {
			t.Fatalf("trace id %q length = %d, want 32", header, len(header))
		}
		if _, err := hex.DecodeString(header); err != nil {
			t.Fatalf("trace id %q is not hex: %v", header, err)
		}
		var body string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body: %v", err)
		}
		if body != header {
			t.Fatalf("body trace id %q != header %q", body, header)
		}
		ids = append(ids, header)
	}
	if ids[0] == ids[1] {
		t.Fatal("two requests got the same generated trace id")
	}
}

func TestTraceIDOnNotFound(t *testing.T) {
	r := New()
	rec := do(t, r, http.MethodGet, "/nope")

	if rec.Header().Get(HeaderXRequestID) == "" {
		t.Fatal("404 response missing trace id header")
	}
}

func TestHeaderAccess(t *testing.T) {
	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) {
		c.SetHeader("X-Resp", "set")
		return c.Header("X-Req"), nil
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Req", "req")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Resp"); got != "set" {
		t.Fatalf("response header = %q, want set", got)
	}

	var body string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body != "req" {
		t.Fatalf("body = %q, want req", body)
	}
}

func TestHeaderDefault(t *testing.T) {
	r := New()
	r.Get("/fallback", func(c *Ctx, _ struct{}) (string, error) {
		return c.Header("X-Missing", "fallback"), nil
	})
	r.Get("/empty", func(c *Ctx, _ struct{}) (string, error) {
		return c.Header("X-Missing"), nil
	})

	cases := []struct {
		path string
		want string
	}{
		{"/fallback", "\"fallback\"\n"},
		{"/empty", "\"\"\n"},
	}
	for _, tc := range cases {
		rec := do(t, r, http.MethodGet, tc.path)
		if got := rec.Body.String(); got != tc.want {
			t.Fatalf("%s body = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTraceIDTooLongIsReplaced(t *testing.T) {
	long := strings.Repeat("a", maxTraceIDLen+1)

	r := New()
	r.Get("/x", func(c *Ctx, _ struct{}) (string, error) { return c.TraceID(), nil })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderXRequestID, long)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	header := rec.Header().Get(HeaderXRequestID)
	if header == long {
		t.Fatal("too-long client id was reused")
	}
	if len(header) != 32 {
		t.Fatalf("trace id %q length = %d, want 32", header, len(header))
	}

	var body string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body != header {
		t.Fatalf("body trace id %q != header %q", body, header)
	}
}

func TestRemoteIP(t *testing.T) {
	c := &Ctx{R: httptest.NewRequest(http.MethodGet, "/", nil)}

	c.R.RemoteAddr = "192.0.2.10:5678"
	if got := c.RemoteIP(); got != "192.0.2.10" {
		t.Fatalf("RemoteIP() = %q, want 192.0.2.10", got)
	}

	c.R.RemoteAddr = "192.0.2.10"
	if got := c.RemoteIP(); got != "192.0.2.10" {
		t.Fatalf("RemoteIP() = %q, want bare address unchanged", got)
	}
}
