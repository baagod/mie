package mux

import (
    "encoding/json"
    "errors"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "time"
)

const (
    panicTimeLayout = "2006-01-02 15:04:05"
    maxPanicFrames  = 3
    sourceRadius    = 3
    maxSourceFile   = 1 << 20
)

// RecoverHandler 处理一次已捕获的 panic。实现可以写自定义响应；
// 若没有写任何响应，Recoverer 会补一个通用 500。
type RecoverHandler func(c *Ctx, p *Panic)

// Panic 是一次 panic 的事件快照，只用于日志与自定义响应，不会自动发给客户端。
type Panic struct {
    Time        time.Time
    Method      string
    Host        string
    Path        string
    ContentType string
    IP          string
    TraceID     string
    Error       string
    Stacks      []Frame
}

// Frame 是调用栈中的一帧。Source 是 panic 行附近的源码（读不到时为空）。
type Frame struct {
    File   string
    Line   int
    Func   string
    Source []string
}

func newPanic(c *Ctx, value any) *Panic {
    return &Panic{
        Time:        time.Now(),
        Method:      c.R.Method,
        Host:        c.R.Host,
        Path:        c.R.URL.Path,
        ContentType: c.R.Header.Get(HeaderContentType),
        IP:          c.RemoteIP(),
        TraceID:     c.TraceID(),
        Error:       fmt.Sprint(value),
        Stacks:      captureFrames(),
    }
}

// captureFrames 从 recover 点向上取前几个用户帧，跳过运行时、net/http 与 mux 自身的帧。
func captureFrames() []Frame {
    pcs := make([]uintptr, 64)
    n := runtime.Callers(3, pcs[:])
    iter := runtime.CallersFrames(pcs[:n])
    muxDir := packageDir()

    out := make([]Frame, 0, maxPanicFrames)
    for len(out) < maxPanicFrames {
        f, more := iter.Next()
        if f.Function == "" {
            break
        }
        if !skipPanicFrame(f.Function, f.File, muxDir) {
            out = append(out, Frame{
                File:   displayFile(f.File),
                Line:   f.Line,
                Func:   shortFunc(f.Function),
                Source: readSource(f.File, f.Line),
            })
        }
        if !more {
            break
        }
    }
    return out
}

func skipPanicFrame(fn, file, muxDir string) bool {
    for _, prefix := range []string{"runtime.", "net/http.", "testing."} {
        if strings.HasPrefix(fn, prefix) {
            return true
        }
    }
    if filepath.Dir(file) != muxDir {
        return false
    }
    switch filepath.Base(file) {
    case "recoverer.go", "ctx.go", "router.go":
        return true
    }
    return false
}

func packageDir() string {
    _, file, _, ok := runtime.Caller(0)
    if !ok {
        return ""
    }
    return filepath.Dir(file)
}

func displayFile(file string) string {
    wd, err := os.Getwd()
    if err != nil {
        return file
    }
    rel, err := filepath.Rel(wd, file)
    if err != nil || strings.HasPrefix(rel, "..") {
        return file
    }
    return rel
}

func shortFunc(fn string) string {
    if i := strings.LastIndex(fn, "/"); i >= 0 {
        fn = fn[i+1:]
    }
    if i := strings.Index(fn, "."); i >= 0 {
        fn = fn[i+1:]
    }
    return fn
}

func readSource(file string, line int) []string {
    if file == "" || line <= 0 {
        return nil
    }
    info, err := os.Stat(file)
    if err != nil || info.Size() > maxSourceFile {
        return nil
    }
    data, err := os.ReadFile(file)
    if err != nil {
        return nil
    }

    lines := strings.Split(string(data), "\n")
    start := line - sourceRadius
    if start < 1 {
        start = 1
    }
    end := line + sourceRadius
    if end > len(lines) {
        end = len(lines)
    }

    out := make([]string, 0, end-start+1)
    for i := start; i <= end; i++ {
        out = append(out, strings.TrimRight(lines[i-1], "\r"))
    }
    return out
}

// Text 返回给人看的块状日志：请求信息、错误、三条调用栈与源码片段。
func (p *Panic) Text() string {
    var b strings.Builder
    b.WriteString("PANIC RECOVERED\n")

    label := func(k, v string) {
        fmt.Fprintf(&b, "%-13s%s\n", k+":", v)
    }
    label("Time", p.Time.Format(panicTimeLayout))
    label("Request", p.Method+" "+p.Path)
    label("Host", p.Host)
    label("ContentType", orDash(p.ContentType))
    label("IP", p.IP)
    label("TraceID", orDash(p.TraceID))
    label("Error", p.Error)

    for _, f := range p.Stacks {
        fmt.Fprintf(&b, "\nFile: %s:%d %s()\n", f.File, f.Line, f.Func)
        writeSource(&b, f)
    }
    return b.String()
}

// JSON 返回单行 JSON 日志；字段全部可序列化，不存在失败路径。
func (p *Panic) JSON() string {
    type jsonFrame struct {
        File   string   `json:"file"`
        Line   int      `json:"line"`
        Func   string   `json:"func"`
        Source []string `json:"source,omitempty"`
    }
    type payload struct {
        Time    string      `json:"time"`
        Method  string      `json:"method"`
        Host    string      `json:"host"`
        Path    string      `json:"path"`
        IP      string      `json:"ip"`
        TraceID string      `json:"traceid"`
        Error   string      `json:"error"`
        Stack   []jsonFrame `json:"stack"`
    }

    v := payload{
        Time:    p.Time.Format(panicTimeLayout),
        Method:  p.Method,
        Host:    p.Host,
        Path:    p.Path,
        IP:      p.IP,
        TraceID: p.TraceID,
        Error:   p.Error,
    }
    for _, f := range p.Stacks {
        v.Stack = append(v.Stack, jsonFrame{File: f.File, Line: f.Line, Func: f.Func, Source: f.Source})
    }

    b, err := json.Marshal(v)
    if err != nil {
        return `{"error":"panic info marshal failed"}`
    }
    return string(b)
}

func writeSource(b *strings.Builder, f Frame) {
    start := f.Line - sourceRadius
    if start < 1 {
        start = 1
    }

    for i, line := range f.Source {
        n := start + i
        line = expandTabs(line)
        marker := " "
        if n == f.Line {
            marker = ">"
        }
        // Go 的栈只有 file:line，没有列号；用行号前的 > 标记整行，长行也不会破坏对齐。
        fmt.Fprintf(b, "%s%4d  %s\n", marker, n, line)
    }
}

func expandTabs(s string) string {
    return strings.ReplaceAll(s, "\t", "    ")
}

func orDash(s string) string {
    if s == "" {
        return "-"
    }
    return s
}

// errRecovered 表示 panic 已经被 Recoverer 处理。register 收到它后直接结束请求，
// 不进入 Config.ErrorHandler：panic 与响应错误是两类不同的事件。
var errRecovered = errors.New("mux: recovered panic")

// Recoverer 返回恢复中间件：接住 panic，构造 Panic 交给 h 处理。
// h 为 nil 时记默认日志（Panic.Text）；h 非 nil 时由 h 自己处理。
// panic 不经过 Config.ErrorHandler。
//
// New 默认把 Recoverer 装在中间件链最前面。
func Recoverer(h RecoverHandler) Handler {
    return func(c *Ctx, in any) (out any, err error) {
        defer func() {
            if p := recover(); p != nil {
                // net/http 约定：panic(http.ErrAbortHandler) 用于静默中止响应，
                // 既不该记日志也不该写响应。继续抛回给 net/http 处理。
                if p == http.ErrAbortHandler {
                    panic(p)
                }

                if info := newPanic(c, p); h != nil {
                    h(c, info)
                } else {
                    slog.Error("panic recovered", "detail", info.Text())
                }

                out, err = nil, errRecovered
            }
        }()

        return c.Next(in)
    }
}
