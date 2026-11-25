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

	Page int    `form:"page"`
	Sort string `form:"sort"`
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

// benchLogger 代表开箱配置：访问日志真的格式化一遍（7 个属性、TextHandler），再把字节丢掉。
func benchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: LevelTrace}))
}

// benchServe 只对带 body 的请求复位 body，免得给 GET 凭空塞一个 body 触发 MaxBytesReader。
func benchServe(b *testing.B, app *App, req *http.Request) {
	w := newNopWriter()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.reset()
		if req.Body != http.NoBody {
			resetBody(req)
		}
		app.ServeHTTP(w, req)
	}
}

func newBenchApp(l *slog.Logger) *App {
	app := New(Config{Logger: l})
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

func benchPlainReq() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/hello", nil)
}

func benchJSONReq() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/json", bytes.NewReader(benchBody))
	req.Header.Set(HeaderContentType, MIMEJSON)
	return req
}

func benchFullReq() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/full?page=3&sort=name", bytes.NewReader(benchBody))
	req.Header.Set(HeaderContentType, MIMEJSON)
	req.Header.Set("X-Tag", "t")
	req.Header.Set("Cookie", "sid=abc")
	return req
}

// 同一负载测两档 Logger：开箱配置（访问日志全量格式化）与 slog.DiscardHandler（Enabled 恒 false）。

func BenchmarkPlaintext(b *testing.B) {
	benchServe(b, newBenchApp(benchLogger()), benchPlainReq())
}

func BenchmarkPlaintextNoLog(b *testing.B) {
	benchServe(b, newBenchApp(slog.New(slog.DiscardHandler)), benchPlainReq())
}

func BenchmarkJSONBody(b *testing.B) {
	benchServe(b, newBenchApp(benchLogger()), benchJSONReq())
}

func BenchmarkJSONBodyNoLog(b *testing.B) {
	benchServe(b, newBenchApp(slog.New(slog.DiscardHandler)), benchJSONReq())
}

func BenchmarkBindSources(b *testing.B) {
	benchServe(b, newBenchApp(benchLogger()), benchFullReq())
}

func BenchmarkBindSourcesNoLog(b *testing.B) {
	benchServe(b, newBenchApp(slog.New(slog.DiscardHandler)), benchFullReq())
}
