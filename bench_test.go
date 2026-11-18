package mie

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type benchOut struct {
	OK bool `json:"ok"`
}

type benchJSONIn struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
	City  string `json:"city"`
	Note  string `json:"note"`
	Score int    `json:"score"`
	Tags  string `json:"tags"`
	Flag  bool   `json:"flag"`
}

type benchFullIn struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
	City  string `json:"city"`
	Note  string `json:"note"`
	Score int    `json:"score"`
	Tags  string `json:"tags"`
	Flag  bool   `json:"flag"`

	Page int    `query:"page"`
	Sort string `query:"sort"`
	Tag  string `header:"X-Tag"`
	SID  string `cookie:"sid"`
}

var benchBody = []byte(`{"name":"bob","email":"bob@example.com","age":30,"city":"paris","note":"hello","score":7,"tags":"a,b","flag":true}`)

// nopWriter 丢掉响应体，只保留头和状态码，避免 recorder 的分配糊住框架本身的数字。
type nopWriter struct {
	h    http.Header
	code int
}

func newNopWriter() *nopWriter { return &nopWriter{h: make(http.Header)} }

func (w *nopWriter) reset()                      { clear(w.h); w.code = 0 }
func (w *nopWriter) Header() http.Header         { return w.h }
func (w *nopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *nopWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func newBenchApp() *App {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Get("/hello", func(_ *Ctx, _ struct{}) (benchOut, error) {
		return benchOut{OK: true}, nil
	})
	app.Post("/json", func(_ *Ctx, in benchJSONIn) (benchOut, error) {
		return benchOut{OK: in.Name != ""}, nil
	})
	app.Post("/full", func(_ *Ctx, in benchFullIn) (benchOut, error) {
		return benchOut{OK: in.Page == 3 && in.SID == "abc"}, nil
	})
	return app
}

func resetBody(req *http.Request) {
	req.Body = io.NopCloser(bytes.NewReader(benchBody))
}

func BenchmarkPlaintext(b *testing.B) {
	app := newBenchApp()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	w := newNopWriter()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.reset()
		app.ServeHTTP(w, req)
	}
}

func BenchmarkJSONBody(b *testing.B) {
	app := newBenchApp()
	req := httptest.NewRequest(http.MethodPost, "/json", bytes.NewReader(benchBody))
	req.Header.Set(HeaderContentType, MIMEJSON)
	w := newNopWriter()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.reset()
		resetBody(req)
		app.ServeHTTP(w, req)
	}
}

func BenchmarkBindSources(b *testing.B) {
	app := newBenchApp()
	req := httptest.NewRequest(http.MethodPost, "/full?page=3&sort=name", bytes.NewReader(benchBody))
	req.Header.Set(HeaderContentType, MIMEJSON)
	req.Header.Set("X-Tag", "t")
	req.Header.Set("Cookie", "sid=abc")
	w := newNopWriter()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.reset()
		resetBody(req)
		app.ServeHTTP(w, req)
	}
}
