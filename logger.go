package mie

import (
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "strings"
    "time"
)

// logger 是包级兜底 Logger：New 用它填充 Config.Logger 的零值，手工构造的 Ctx（没有 Router）也用它。
// 不跟随 slog.Default()，避免应用的全局配置影响框架输出。
var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// logAccess 每个请求调用一次，按最终状态码分级写出访问日志。
// Hijack 后 StatusCode 为 0。
func (c *Ctx) logAccess(err error) {
    status := c.StatusCode()
    level := slog.LevelInfo

    switch {
    case status >= 500:
        level = slog.LevelError
    case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
        level = slog.LevelInfo
    case status >= 400:
        level = slog.LevelWarn
    }

    attrs := []slog.Attr{
        slog.String("method", c.R.Method),
        slog.Int("status", status),
        slog.String("path", c.R.URL.Path),
        slog.String("pattern", strings.TrimPrefix(c.R.Pattern, c.R.Method+" ")),
        slog.Duration("latency", time.Since(c.start)),
        slog.String("traceid", c.TraceID()),
        slog.String("ip", c.RemoteIP()),
    }

    if err != nil {
        attrs = append(attrs, slog.Any("error", err))
    }

    c.router.config.Logger.LogAttrs(c, level, "access", attrs...)
}

// Log 是框架日志的统一出口，落到 Config.Logger；手工构造的 Ctx 退回包级 logger。
func (c *Ctx) Log(level slog.Level, msg string, args ...any) {
    if c.router == nil {
        logger.Log(c, level, msg, args...)
        return
    }
    c.router.config.Logger.Log(c, level, msg, args...)
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
