package mie

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// LevelTrace 是访问日志的级别，比 Debug 更低一档。
// 默认 Logger 开在这一档，开箱就能看到访问日志；Handler 调到 Debug 只剩用户自己的日志，
// 调到 Info 连访问日志也静默。
const LevelTrace = slog.LevelDebug - 4

// logAccess 每个请求调用一次，按最终状态码与是否带错误分级写出访问日志。
// 正常请求记在 LevelTrace，不跟用户自己的 Debug 日志抢同一档。
// Hijack 后 StatusCode 为 0；panicked 表示响应已发出后 panic，状态不可信但必须留下一条。
// 客户端中途断开时状态记 499，按客户端行为算 Info。
func (c *Ctx) logAccess(err error, panicked bool) {
	level := LevelTrace
	status := c.StatusCode()

	switch {
	case panicked || status >= 500:
		level = slog.LevelError
	case status == 0, status == http.StatusNotFound, status == http.StatusMethodNotAllowed, status == Status499ClientClosedRequest:
		level = slog.LevelInfo
	case status >= 400:
		level = slog.LevelWarn
	case err != nil:
		// 状态已是 2xx/3xx 却带 error：handler 自己写成功又报错，属服务端缺陷。
		level = slog.LevelError
	}

	if !c.app.config.Logger.Enabled(c, level) {
		return
	}

	attrs := []slog.Attr{
		slog.String("method", c.R.Method),
		slog.Int("status", status),
		slog.String("path", c.Path()),
		slog.String("pattern", c.Pattern()),
		slog.Duration("latency", time.Since(c.start)),
		slog.String("traceid", c.TraceID()),
		slog.String("ip", c.RemoteIP()),
	}

	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}

	c.app.config.Logger.LogAttrs(c, level, "access", attrs...)
}

// Log 是框架日志的统一出口，落到 Config.Logger。
func (c *Ctx) Log(level slog.Level, msg string, args ...any) {
	c.app.config.Logger.Log(c, level, msg, args...)
}

// LogWarn 写一条 Warn 级别日志。
func (c *Ctx) LogWarn(msg string, args ...any) { c.Log(slog.LevelWarn, msg, args...) }

// LogError 写一条 Error 级别日志。
func (c *Ctx) LogError(msg string, args ...any) { c.Log(slog.LevelError, msg, args...) }

// LogInfo 写一条 Info 级别日志。
func (c *Ctx) LogInfo(msg string, args ...any) { c.Log(slog.LevelInfo, msg, args...) }

// LogWarnf 按格式写一条 Warn 级别日志，不附加结构化字段。
func (c *Ctx) LogWarnf(format string, args ...any) {
	c.Log(slog.LevelWarn, fmt.Sprintf(format, args...))
}

// LogErrorf 按格式写一条 Error 级别日志，不附加结构化字段。
func (c *Ctx) LogErrorf(format string, args ...any) {
	c.Log(slog.LevelError, fmt.Sprintf(format, args...))
}

// LogInfof 按格式写一条 Info 级别日志，不附加结构化字段。
func (c *Ctx) LogInfof(format string, args ...any) {
	c.Log(slog.LevelInfo, fmt.Sprintf(format, args...))
}
