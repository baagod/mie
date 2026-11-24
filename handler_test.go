package mie

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestHeAdapter(t *testing.T) {
	type in struct {
		ID int `path:"id"`
	}

	r := New()
	r.Delete("/he/{id}", He(func(c *Ctx, v in) error {
		if v.ID == 0 {
			return NewError(http.StatusBadRequest, "bad id")
		}
		return nil
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/he/0", nil))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "bad id" {
		t.Fatalf("status=%d body=%q, want 400 bad id", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/he/5", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 200 空 body", rec.Code, rec.Body.String())
	}
}

func TestHoAdapter(t *testing.T) {
	type in struct {
		ID int `path:"id"`
	}

	r := New()
	r.Get("/ho/{id}", Ho(func(c *Ctx, v in) int {
		return v.ID * 2
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ho/7", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "14" {
		t.Fatalf("status=%d body=%q, want 200 14", rec.Code, rec.Body.String())
	}
}

func TestOUTRules(t *testing.T) {
	r := New()
	r.Get("/zero", func(c *Ctx, _ struct{}) (int, error) { return 0, nil })
	r.Get("/nil", func(c *Ctx, _ struct{}) (*int, error) { return nil, nil })
	r.Get("/none", func(c *Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })
	r.Get("/bad", func(c *Ctx, _ struct{}) (chan int, error) { return make(chan int), nil })

	cases := []struct {
		path   string
		status int
		body   string
		ctype  string
	}{
		{"/zero", http.StatusOK, "0", MIMEJSON},
		{"/nil", http.StatusOK, "null", MIMEJSON},
		{"/none", http.StatusOK, "{}", MIMEJSON},
		{"/bad", http.StatusInternalServerError, "", MIMETextPlainUTF8},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status {
			t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.status)
		}
		if tc.path == "/bad" {
			if rec.Body.Len() == 0 {
				t.Fatalf("%s body 为空，want 编码错误文本", tc.path)
			}
		} else if got := rec.Body.String(); got != tc.body {
			t.Fatalf("%s body = %q, want %q", tc.path, got, tc.body)
		}
		if got := rec.Header().Get(HeaderContentType); got != tc.ctype {
			t.Fatalf("%s Content-Type = %q, want %q", tc.path, got, tc.ctype)
		}
	}
}

func TestMiddlewareRunsAroundHandler(t *testing.T) {
	var steps []string
	r := New()
	r.Use(He(func(c *Ctx, _ struct{}) error {
		steps = append(steps, "before")
		err := c.Next()
		steps = append(steps, "after")
		return err
	}))
	r.Get("/m", func(c *Ctx, _ struct{}) (struct{}, error) {
		steps = append(steps, "handler")
		return struct{}{}, nil
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/m", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if want := []string{"before", "handler", "after"}; !slices.Equal(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if got := rec.Body.String(); got != "{}" {
		t.Fatalf("body = %q, want %q（中间件不额外写 body）", got, "{}")
	}
}

func TestHeAsNoInputHandler(t *testing.T) {
	r := New()
	r.Get("/health", He(func(c *Ctx, _ struct{}) error {
		c.Status(http.StatusNoContent)
		return nil
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want 空", rec.Body.String())
	}
}

func TestRegisterRejectsUnbindableInput(t *testing.T) {
	type bad struct {
		Items map[string]int `form:"items"`
	}

	// 字段类型非法是服务端声明错误：注册时 panic，避免每个请求都拿到 400。
	t.Run("value", func(t *testing.T) {
		mustPanicWith(t, "unsupported binding type map[string]int", func() {
			New().Get("/lazy", func(c *Ctx, v bad) (struct{}, error) { return struct{}{}, nil })
		})
	})

	t.Run("pointer", func(t *testing.T) {
		mustPanicWith(t, "unsupported binding type map[string]int", func() {
			New().Get("/lazy", func(c *Ctx, v *bad) (struct{}, error) { return struct{}{}, nil })
		})
	})
}
