package mux

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// errRecovered 表示 panic 已经被 Recoverer 处理。register 收到它后直接结束请求，
// 不进入 Config.ErrorHandler：panic 与响应错误是两类不同的事件。
var errRecovered = errors.New("mux: recovered panic")

// Recoverer 返回恢复中间件：接住 panic、记录日志，并直接写 500。
// 它不经过 Config.ErrorHandler，只负责 panic 自己的处理。
//
// New 默认把 Recoverer 装在中间件链最前面，无需手动 Use。
func Recoverer() Handler {
	return func(c *Ctx, in any) (out any, err error) {
		defer func() {
			if p := recover(); p != nil {
				// net/http 约定：panic(http.ErrAbortHandler) 用于静默中止响应，
				// 既不该记日志也不该写响应。继续抛回给 net/http 处理。
				if p == http.ErrAbortHandler {
					panic(p)
				}

				slog.Error(
					"panic",
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"panic", p,
					"stack", string(debug.Stack()),
				)

				writeErrorJSON(c, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
				out, err = nil, errRecovered
			}
		}()

		return c.Next(in)
	}
}
