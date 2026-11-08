package mux

import (
    "fmt"
    "log/slog"
    "strings"
    "time"
)

// logAccess 在响应结束后调用一次，此时状态码与已写字节数都已定稿。
// Hijack 后连接已交给 handler，StatusCode 为 0。
func (c *Ctx) logAccess(err error) {
    status := c.StatusCode()
    level := slog.LevelInfo

    switch {
    case status >= 500:
        level = slog.LevelError
    case status >= 400:
        level = slog.LevelWarn
    }

    args := []any{
        "method", c.R.Method,
        "status", status,
        "path", c.R.URL.Path,
        "pattern", strings.TrimPrefix(c.R.Pattern, c.R.Method+" "),
        "latency", time.Since(c.start),
        "traceid", c.TraceID(),
        "ip", c.RemoteIP(),
    }

    if err != nil {
        args = append(args, "error", err)
    }

    c.Log(level, "access", args...)
}

// Log 是框架所有日志的统一出口，落到 Router 配置的 Logger。
func (c *Ctx) Log(level slog.Level, msg string, args ...any) {
    c.router.config.Logger.Log(c, level, msg, args...)
}

func (c *Ctx) LogWarn(msg string, args ...any)  { c.Log(slog.LevelWarn, msg, args...) }
func (c *Ctx) LogError(msg string, args ...any) { c.Log(slog.LevelError, msg, args...) }
func (c *Ctx) LogInfo(msg string, args ...any)  { c.Log(slog.LevelInfo, msg, args...) }

func (c *Ctx) LogWarnf(format string, args ...any) {
    c.Log(slog.LevelWarn, fmt.Sprintf(format, args...))
}

func (c *Ctx) LogErrorf(format string, args ...any) {
    c.Log(slog.LevelError, fmt.Sprintf(format, args...))
}

func (c *Ctx) LogInfof(format string, args ...any) {
    c.Log(slog.LevelInfo, fmt.Sprintf(format, args...))
}
