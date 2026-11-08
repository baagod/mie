package mux

import (
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "strings"
    "time"
)

// logger 是框架自身的记录器：New 用它填充 Config.Logger 的零值，
// 手工构造的 Ctx（没有 Router）也用它兜底。
// 不跟随 slog.Default()，应用改全局级别、ReplaceAttr、WithGroup 不应影响框架输出。
var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// logAccess 在响应结束后调用一次，此时状态码与已写字节数都已定稿。
// Hijack 后连接已交给 handler，StatusCode 为 0。
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

// Log 是框架所有日志的统一出口，落到 Router 配置的 Logger。
// 手工构造的 Ctx（没有 Router）退回包级 logger，避免 nil 解引用。
func (c *Ctx) Log(level slog.Level, msg string, args ...any) {
    if c.router == nil {
        logger.Log(c, level, msg, args...)
        return
    }
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
