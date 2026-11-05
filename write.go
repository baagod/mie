package mux

import "net/http"

// ResponseWriter 是处理器可用的响应写入器：只暴露 Header、Write、WriteHeader，
// 底层 http.ResponseWriter 不可达，因此每次写入都会记录到 Ctx.wrote。
// 1xx 是临时响应，不算提交；>=200 的 WriteHeader 或任意 Write 才算已提交。
type ResponseWriter struct {
	w http.ResponseWriter
	c *Ctx
}

func (w *ResponseWriter) Header() http.Header { return w.w.Header() }

func (w *ResponseWriter) WriteHeader(code int) {
	if code >= 200 {
		w.c.wrote = true
	}
	w.w.WriteHeader(code)
}

func (w *ResponseWriter) Write(p []byte) (int, error) {
	w.c.wrote = true
	return w.w.Write(p)
}
