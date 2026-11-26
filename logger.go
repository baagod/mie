package mie

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// LevelTrace 是访问日志的级别，比 Debug 低一档。
const LevelTrace = slog.LevelDebug - 4

// logAccess 每个请求调用一次，按状态码分级写访问日志。
// Hijack 后 StatusCode 为 0；panicked 表示响应已发出后 panic，状态不可信但必须留下一条。
func (c *Ctx) logAccess(err error, panicked bool) {
	level := LevelTrace
	status := c.StatusCode()

	switch {
	case panicked || status >= 500:
		level = slog.LevelError
	case status == 0, status == Status499ClientClosedRequest:
		level = slog.LevelInfo
	case status == http.StatusNotFound, status == http.StatusMethodNotAllowed:
		// 命中路由的 404/405 是处理器自己判的失败，升到 Info；没命中的留在 LevelTrace。
		if c.Pattern() != "" {
			level = slog.LevelInfo
		}
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
