package mux

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRouterResponses(t *testing.T) {
	r := New()
	r.Use(func(c *Ctx) error { return c.Next() })

	r.Get("/ok", func(c *Ctx) error { return c.String(http.StatusOK, "ok") })
	r.Get("/bad", func(c *Ctx) error {
		return NewError(http.StatusBadRequest, "bad request")
	})
	r.Get("/boom", func(c *Ctx) error { panic("boom") })
	r.Get("/fail", func(c *Ctx) error { return errors.New("db down") })

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"ok", "GET", "/ok", http.StatusOK, "ok"},
		{"http error", "GET", "/bad", http.StatusBadRequest, "bad request\n"},
		{"panic", "GET", "/boom", http.StatusInternalServerError, "Internal Server Error\n"},
		{"plain error", "GET", "/fail", http.StatusInternalServerError, "Internal Server Error\n"},
		{"not found", "GET", "/nope", http.StatusNotFound, "404 page not found\n"},
		{"method not allowed", "POST", "/ok", http.StatusMethodNotAllowed, ""},
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

func TestMiddlewareOrder(t *testing.T) {
	var got []string
	r := New()

	r.Use(func(c *Ctx) error {
		got = append(got, "outer-before")
		err := c.Next()
		got = append(got, "outer-after")
		return err
	})
	r.Use(func(c *Ctx) error {
		got = append(got, "inner-before")
		err := c.Next()
		got = append(got, "inner-after")
		return err
	})
	r.Get("/x", func(c *Ctx) error {
		got = append(got, "handler")
		return c.String(http.StatusOK, "x")
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
	r.Use(func(c *Ctx) error {
		return NewError(http.StatusUnauthorized, "unauthorized")
	})
	r.Get("/secret", func(c *Ctx) error {
		called = true
		return nil
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
	r.Get("/fail", func(c *Ctx) error { return errors.New("db down") })
	r.Get("/boom", func(c *Ctx) error { panic("boom") })

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

func TestErrorFunnelEdgeCases(t *testing.T) {
	t.Run("client canceled writes nothing", func(t *testing.T) {
		r := New()
		r.Get("/canceled", func(c *Ctx) error { return context.Canceled })

		rec := do(t, r, http.MethodGet, "/canceled")

		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("got status %d body %q, want no response", rec.Code, rec.Body.String())
		}
	})

	t.Run("error after direct write does not write again", func(t *testing.T) {
		r := New()
		r.Get("/partial", func(c *Ctx) error {
			if _, err := c.Writer.Write([]byte("partial")); err != nil {
				return err
			}
			return errors.New("boom")
		})

		rec := do(t, r, http.MethodGet, "/partial")

		if rec.Code != http.StatusOK || rec.Body.String() != "partial" {
			t.Fatalf("got status %d body %q, want 200 %q", rec.Code, rec.Body.String(), "partial")
		}
	})

	t.Run("invalid status falls back to 500", func(t *testing.T) {
		r := New()
		r.Get("/bad-status", func(c *Ctx) error { return NewError(0, "oops") })

		rec := do(t, r, http.MethodGet, "/bad-status")

		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "oops\n" {
			t.Fatalf("got status %d body %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("typed nil error falls back to 500", func(t *testing.T) {
		r := New()
		r.Get("/typed-nil", func(c *Ctx) error {
			var e *Error
			return e
		})

		rec := do(t, r, http.MethodGet, "/typed-nil")

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("http.ErrAbortHandler is re-panicked", func(t *testing.T) {
		r := New()
		r.Get("/abort", func(c *Ctx) error { panic(http.ErrAbortHandler) })

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
