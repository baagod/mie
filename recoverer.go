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

// 日志中捕获的帧上限、每帧源码的上下行数与单文件读取上限。
const (
    maxPanicFrames = 3
    sourceRadius   = 3
    maxSourceFile  = 1 << 20
)

// Panic 记录一次 panic 的请求上下文、错误值与调用栈；只供日志和 RecoverHandler 使用，
// 框架不会自动把它写给客户端。
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

// Frame 是调用栈的一帧；Source 保存 panic 行附近的源码，文件读不到时为空。
type Frame struct {
    File   string   `json:"file"`
    Line   int      `json:"line"`
    Func   string   `json:"func"`
    Source []string `json:"source,omitempty"`
}

// NewPanic 记录 c 的请求信息与 err（recover 原值），并抓取调用栈。
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

// Text 返回给人看的块状日志：请求信息、错误、调用栈与源码片段。
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

// JSON 返回单行 JSON；非法 UTF-8 会让 v2 编码失败，此时返回空串而不是半截 JSON。
func (p *Panic) JSON() string {
    b, err := json.Marshal(p)
    if err != nil {
        return ""
    }
    return string(b)
}

// captureFrames 从 recover 点向上收集业务帧；运行时、net/http、testing 与 mux 自身的帧不算。
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

// skipPanicFrame 判断一帧是否属于运行时、net/http、测试或框架自身。
func skipPanicFrame(fn, file, muxDir string) bool {
    for _, prefix := range []string{"runtime.", "net/http.", "testing."} {
        if strings.HasPrefix(fn, prefix) {
            return true
        }
    }
    // 同目录的非测试文件即 mux 自身；不按函数名判断，避免 rename 后失效。
    return filepath.Dir(file) == muxDir && !strings.HasSuffix(file, "_test.go")
}

// packageDir 返回 mux 源码目录，用于识别框架自身的帧。
func packageDir() string {
    _, file, _, ok := runtime.Caller(0)
    if !ok {
        return ""
    }
    return filepath.Dir(file)
}

// displayFile 尽量把绝对路径转成相对工作目录的路径。
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

// shortFunc 去掉包路径与方法接收者，只保留函数名。
func shortFunc(fn string) string {
    if _, after, ok := strings.CutLast(fn, "/"); ok {
        fn = after
    }
    if _, after, ok := strings.Cut(fn, "."); ok {
        fn = after
    }
    return fn
}

// sourceStart 返回源码片段的起始行，最小为 1。
func sourceStart(line int) int {
    return max(line-sourceRadius, 1)
}

// readSource 读取 panic 行附近的源码；文件不可读或过大时返回 nil。
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

// writeSource 把一帧源码写入日志，panic 行前用 > 标记。
func writeSource(b *strings.Builder, f Frame) {
    start := sourceStart(f.Line)

    for i, line := range f.Source {
        n := start + i
        line = strings.ReplaceAll(line, "\t", "    ")
        marker := " "
        if n == f.Line {
            marker = ">"
        }
        // 栈里只有 file:line 没有列号，所以用 > 标记整行：长行也不会把标记挤出可视区。
        _, _ = fmt.Fprintf(b, "%s%4d  %s\n", marker, n, line)
    }
}

// label 输出一行对齐的 key: value。
func label(b *strings.Builder, k, v string) {
    _, _ = fmt.Fprintf(b, "%-13s%s\n", k+":", v)
}
