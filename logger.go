package mux

import (
    "log/slog"
    "os"
    "strings"
    "time"
)

// logger 是框架自己的日志器，不跟随应用对 slog.Default() 的修改。
// 直接构造而非取 slog.Default()：后者取决于 init 顺序，若应用在 init 里
// SetDefault，框架就会拿到应用的 logger。
var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

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

    if c.router.config.LogHandler != nil {
        c.router.config.LogHandler(c, level, "access", attrs)
        return
    }

    logger.LogAttrs(c, level, "access", attrs...)
}
