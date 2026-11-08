package mux

import (
    "cmp"
    "encoding/json/v2"
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "time"
)

const (
    maxPanicFrames = 3
    sourceRadius   = 3
    maxSourceFile  = 1 << 20
)

// Panic 是一次 panic 的事件快照，只用于日志与自定义响应，不会自动发给客户端。
type Panic struct {
    Time        time.Time `json:"time"`
    Method      string    `json:"method"`
    Host        string    `json:"host"`
    Path        string    `json:"path"`
    ContentType string    `json:"content_type"`
    IP          string    `json:"ip"`
    TraceID     string    `json:"traceid"`
    Error       string    `json:"error"`
    Value       any       `json:"-"` // Error 的原始值
    Stacks      []Frame   `json:"stack"`
}

// Frame 是调用栈中的一帧。Source 是 panic 行附近的源码（读不到时为空）。
type Frame struct {
    File   string   `json:"file"`
    Line   int      `json:"line"`
    Func   string   `json:"func"`
    Source []string `json:"source,omitempty"`
}

// NewPanic 在 c 上构造 panic 快照；err 是 recover 得到的原始值。
func NewPanic(c *Ctx, err any) *Panic {
    return &Panic{
        Time:        time.Now(),
        Method:      c.R.Method,
        Host:        c.R.Host,
        Path:        c.R.URL.Path,
        ContentType: c.R.Header.Get(HeaderContentType),
        IP:          c.RemoteIP(),
        TraceID:     c.TraceID(),
        Error:       fmt.Sprint(err),
        Value:       err,
        Stacks:      captureFrames(),
    }
}

// Text 返回给人看的块状日志：请求信息、错误、三条调用栈与源码片段。
func (p *Panic) Text() string {
    var b strings.Builder
    b.WriteString("PANIC RECOVERED\n")

    label(&b, "Time", p.Time.Format(time.DateTime))
    label(&b, "Request", p.Method+" "+p.Path)
    label(&b, "Host", p.Host)
    label(&b, "ContentType", cmp.Or(p.ContentType, "-"))
    label(&b, "IP", p.IP)
    label(&b, "TraceID", cmp.Or(p.TraceID, "-"))
    label(&b, "Error", p.Error)

    for _, f := range p.Stacks {
        _, _ = fmt.Fprintf(&b, "\nFile: %s:%d %s()\n", f.File, f.Line, f.Func)
        writeSource(&b, f)
    }

    return b.String()
}

// JSON 返回单行 JSON 日志。
// v2 默认拒绝非法 UTF-8；此时返回空串，而不是半截 JSON。
func (p *Panic) JSON() string {
    b, err := json.Marshal(p)
    if err != nil {
        return ""
    }
    return string(b)
}

// defaultRecoverHandler 是未配置 Config.RecoverHandler 时的兜底：记录 Panic 并返回 500。
func defaultRecoverHandler(_ *Ctx, info *Panic) error {
    logger.Error("PANIC RECOVERED", "detail", info.Text())
    return Status500InternalServerError()
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
    // 框架帧由包边界决定：同目录的非测试文件都算 mux 自身。
    return filepath.Dir(file) == muxDir && !strings.HasSuffix(file, "_test.go")
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
    if err != nil || !filepath.IsLocal(rel) {
        return file
    }
    return rel
}

func shortFunc(fn string) string {
    if _, after, ok := strings.CutLast(fn, "/"); ok {
        fn = after
    }
    if _, after, ok := strings.Cut(fn, "."); ok {
        fn = after
    }
    return fn
}

func sourceStart(line int) int {
    return max(line-sourceRadius, 1)
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
    start := sourceStart(line)
    end := min(line+sourceRadius, len(lines))
    if start > len(lines) {
        return nil
    }

    for i := start - 1; i < end; i++ {
        lines[i] = strings.TrimRight(lines[i], "\r")
    }

    return lines[start-1 : end]
}

func writeSource(b *strings.Builder, f Frame) {
    start := sourceStart(f.Line)

    for i, line := range f.Source {
        n := start + i
        line = strings.ReplaceAll(line, "\t", "    ")
        marker := " "
        if n == f.Line {
            marker = ">"
        }
        // Go 的栈只有 file:line，没有列号；用行号前的 > 标记整行，长行也不会破坏对齐。
        _, _ = fmt.Fprintf(b, "%s%4d  %s\n", marker, n, line)
    }
}

func label(b *strings.Builder, k, v string) {
    _, _ = fmt.Fprintf(b, "%-13s%s\n", k+":", v)
}
