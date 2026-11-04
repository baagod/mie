package mux

import (
	"context"
	"encoding/json"
	"errors"
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

func TestInvalidStatusIsCaughtByFunnel(t *testing.T) {
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
		c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		c.Writer.WriteHeader(http.StatusTeapot)
		c.Writer.Write([]byte("custom"))
	}})
	r.Get("/fail", func(c *Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, errors.New("db down")
	})
	r.Get("/boom", func(c *Ctx, _ struct{}) (struct{}, error) {
		panic("boom")
	})

	for _, path := range []string{"/fail", "/boom"} {
		gotErr = nil
		rec := do(t, r, http.MethodGet, path)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusTeapot)
		}
		if rec.Body.String() != "custom" {
			t.Fatalf("%s: body = %q, want %q", path, rec.Body.String(), "custom")
		}
		if gotErr == nil {
			t.Fatalf("%s: ErrorHandler did not receive an error", path)
		}
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
		c.Writer.WriteHeader(http.StatusNotFound)
		_, _ = c.Writer.Write([]byte("custom 404"))
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
			return struct{}{}, c.Request.Context().Err()
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
		r := New(Config{ErrorHandler: func(c *Ctx, err error) { called = true; http.Error(c.Writer, "timeout", http.StatusGatewayTimeout) }})
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
