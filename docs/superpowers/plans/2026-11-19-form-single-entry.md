# form 解析唯一出口 + Ctx.Form multipart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 form 请求体的解析收成 `bind.ParseForm` 一个入口，并让 `Ctx.Form[T]` 在 `multipart/form-data` 下能读到值与文件。

**Architecture:** 从 `internal/bind/decoder.go` 的 `bindForm` 抽出 `ParseForm`（multipart → `ParseMultipartForm(multipartMemory)`，其余 → `ParseForm`），`bindForm` 与 `Ctx.Form` 共用；`Ctx.Form` 保留「query 优先、失败零值」，文件类型 `T` 从 `MultipartForm.File` 取且与 struct 绑定共用同一段取文件逻辑；multipart 临时文件清理改为盯 `c.R`。

**Tech Stack:** Go 标准库 `net/http`、`mime`、`mime/multipart`；测试 `testing` + `httptest`。

**Spec:** `docs/superpowers/specs/2026-11-18-multipart-form-design.md`（D1–D3 仍有效；D5 已补，D4 已被 D5 反转）

## Global Constraints

- 唯一解析入口：生产代码里除 `bind.ParseForm` 外不得再直接调用 `r.ParseForm()` / `r.ParseMultipartForm(...)`。
- D2 继续有效：multipart 豁免 `MaxBodyBytes`；`multipartMemory` 保持 `32 << 20`，不新增配置项。
- 仓库未发布，不写兼容层；沿用现有空格缩进，不跑 gofmt 重排。
- 未经用户明确说「提交」，不执行 `git add` / `git commit`；本计划不写提交步骤。
- 每个任务结束跑 `go build ./... && go vet ./... && go test ./...`。

## Review Focus

- 坏 multipart（缺 boundary、传输截断）：`Ctx.Form` 返回零值、不 panic（Q2 已定）。
- 同名 query 命中：`Ctx.Form` 不得为此解析 body（Q4 已定）。
- 文件 part：值类型遇到文件 part 返回零值、不 panic；文件用文件类型取（Q3 已定）。
- 临时文件：解析发生在 `c.R` 上时，请求结束（含 panic）必须删除。
- `Bind` 与 `Ctx.Form` 先后调用同一请求：不得重复解析，也不得互相清空 `PostForm`。

## 已定决策

- **D5（2026-11-19）**：`Ctx.Form[T]` 支持 `multipart/form-data`；spec 的 D4「本次不改」作废。
- **Q1**：不新增第二套解析。把 `internal/bind/decoder.go:109-115` 的分支抽成 `ParseForm`，`bindForm` 与 `Ctx.Form` 共用；只共享「选解析器 + 调用」，不共享取值与 query 覆盖，`bindForm` 的 per-field 语义零改动。
- **Q2**：坏 multipart 时 `Ctx.Form` 返回零值、不报错；签名不变，严格路径用 `Bind`（400）。
- **Q3**：`Ctx.Form[T]` 支持文件类型。`T` 为 `*multipart.FileHeader` 或 `[]*multipart.FileHeader` 时从 `MultipartForm.File` 取；文件类型不走 query，失败仍返回零值。取文件逻辑从 `bindForm` 抽出，由 `bind.FormFile[T]` 与 struct 绑定共用，不在 `Ctx` 复制第二份。
- **Q4**：值类型 query 命中即返回，不解析 body；文件类型没有 query 对应，总是解析 multipart。
- **Q5**：资源策略沿用 D2——multipart 不受 `MaxBodyBytes` 限制，`multipartMemory` 保持 `32 << 20`，不新增配置项；清理由 Task 2 修。
- **清理事实**：`newCtx` 在 `ctx.go:62` 复制了 `*http.Request`，解析发生在 `c.R` 上；`app.go:97` 检查 `req.MultipartForm` 永远是 nil，临时文件不会被删。由 Task 2 修正。

## 待定决策

无：Q1–Q5 已全部落定。

---

### Task 1: 抽出 `bind.ParseForm`

**Files:**
- Modify: `internal/bind/decoder.go:107-115`
- Test: `internal/bind/form_test.go`（新建）

**Interfaces:**
- Produces: `func ParseForm(r *http.Request) error`
- Consumes: `multipartMemory`（`internal/bind/bind.go:14`）

- [ ] **Step 1: 写失败测试**

```go
package bind_test

import (
    "github.com/baagod/mie/internal/bind"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestParseFormURLEncoded(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=%E5%BC%A0%E4%B8%89"))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    if err := bind.ParseForm(req); err != nil {
        t.Fatal(err)
    }
    if got := req.PostForm.Get("name"); got != "张三" {
        t.Fatalf("PostForm[name] = %q, want 张三", got)
    }
}

func TestParseFormMultipart(t *testing.T) {
    req := multipartRequest(t, map[string]string{"name": "张三"})

    if err := bind.ParseForm(req); err != nil {
        t.Fatal(err)
    }
    if got := req.PostForm.Get("name"); got != "张三" {
        t.Fatalf("PostForm[name] = %q, want 张三", got)
    }
}

func TestParseFormMalformedMultipart(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
    req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")

    if err := bind.ParseForm(req); err == nil {
        t.Fatal("want error for malformed multipart, got nil")
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bind -run 'TestParseForm' -v`
Expected: 编译失败 `undefined: bind.ParseForm`

- [ ] **Step 3: 实现**

在 `internal/bind/decoder.go` 的 `bindForm` 上方加：

```go
// ParseForm 解析表单请求体：multipart 的值进 PostForm、文件进 MultipartForm，其余按 urlencoded 解析。
// bindForm 与 Ctx.Form 共用这一入口。
func ParseForm(r *http.Request) error {
    if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
        return r.ParseMultipartForm(multipartMemory)
    }
    return r.ParseForm()
}
```

`bindForm` 开头替换为：

```go
if err = ParseForm(r); err != nil {
    return
}
```

删除原来的 Content-Type 分支和三行注释（「请求体只在绑定 form 时解析」并入 `ParseForm` 的注释）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bind -run 'TestParseForm' -v && go test ./internal/bind ./...`
Expected: PASS + 全绿

---

### Task 2: 清理挂点改盯 `c.R`

**Files:**
- Modify: `app.go:95-103`
- Test: `multipart_test.go`

**Interfaces:**
- Consumes: `newCtx` 返回的 `*Ctx`（`c.R` 正是 handler 解析的请求）

- [ ] **Step 1: 写失败测试**

```go
func TestServeHTTPCleansMultipart(t *testing.T) {
    var fh *multipart.FileHeader
    r := New()
    r.Handle(http.MethodPost, "/upload", He(func(c *Ctx, _ struct{}) error {
        // 0 让标准库把小文件也写到磁盘临时文件，便于观察清理。
        if err := c.R.ParseMultipartForm(0); err != nil {
            return err
        }
        fh = c.R.MultipartForm.File["f"][0]
        return nil
    }))

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    part, err := w.CreateFormFile("f", "a.txt")
    if err != nil {
        t.Fatal(err)
    }
    if _, err = part.Write([]byte("hello")); err != nil {
        t.Fatal(err)
    }
    if err = w.Close(); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/upload", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    r.ServeHTTP(httptest.NewRecorder(), req)

    if fh == nil {
        t.Fatal("handler did not run")
    }
    if _, err := fh.Open(); err == nil {
        t.Fatal("temp file still readable after ServeHTTP")
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test . -run 'TestServeHTTPCleansMultipart' -v`
Expected: FAIL `temp file still readable after ServeHTTP`——defer 检查的是原始 `req`

- [ ] **Step 3: 实现**

`app.go` 的 defer 里把 `req` 改成 `c.R`：

```go
if f := c.R.MultipartForm; f != nil {
    _ = f.RemoveAll()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test . -run 'TestServeHTTPCleansMultipart' -v && go build ./... && go vet ./... && go test ./...`
Expected: PASS + 全绿

---

---

### Task 3: `Ctx.Form` 支持 multipart 值与文件

**Files:**
- Modify: `internal/bind/decoder.go`（抽 `setFiles`、加 `FormFile`）
- Modify: `internal/bind/bind.go`（加 `IsFile`）
- Modify: `ctx.go:140-150`
- Test: `internal/bind/form_test.go`、`ctx_test.go`（追加），`multipart_test.go`（加 helper）

**Interfaces:**
- Produces: `func IsFile[T any]() bool`、`func FormFile[T any](r *http.Request, name string) (T, error)`
- Consumes: `ParseForm`（Task 1）、`fileKind` / `fileHeaderType` / `fileHeadersType`（`internal/bind/bind.go:16-20`、`:147-150`）

- [ ] **Step 1: 写失败测试**

`internal/bind/form_test.go` 追加（复用 `multipartWithFiles`，`internal/bind/multipart_test.go:83`）：

```go
func TestFormFile(t *testing.T) {
    req := multipartWithFiles(t, "f", []string{"a.txt", "b.txt"})
    if err := bind.ParseForm(req); err != nil {
        t.Fatal(err)
    }

    if fhs, err := bind.FormFile[[]*multipart.FileHeader](req, "f"); err != nil || len(fhs) != 2 {
        t.Fatalf("FormFile[[]*multipart.FileHeader] = %d, %v; want 2, nil", len(fhs), err)
    }
    if _, err := bind.FormFile[*multipart.FileHeader](req, "f"); err == nil {
        t.Fatal("want error for multiple files into single field")
    }
    if fh, err := bind.FormFile[*multipart.FileHeader](req, "missing"); fh != nil || err != nil {
        t.Fatalf("missing = %v, %v; want nil, nil", fh, err)
    }
    if _, err := bind.FormFile[string](req, "f"); err == nil {
        t.Fatal("want error for non-file type")
    }
    if !bind.IsFile[*multipart.FileHeader]() || !bind.IsFile[[]*multipart.FileHeader]() || bind.IsFile[string]() {
        t.Fatal("IsFile misclassifies types")
    }
}
```

`ctx_test.go` 追加（`newTestCtx` 见 `ctx_test.go:136`，`multipartFileRequest` 见 `multipart_test.go:12`；补 imports：`bytes`、`io`、`mime/multipart`）：

```go
func TestCtxFormMultipartValue(t *testing.T) {
    c := newTestCtx(httptest.NewRecorder(), multipartValueRequest(t, map[string]string{"name": "张三"}))

    if got := c.Form[string]("name"); got != "张三" {
        t.Fatalf("Form[string] = %q, want 张三", got)
    }
}

func TestCtxFormMultipartFile(t *testing.T) {
    c := newTestCtx(httptest.NewRecorder(), multipartFileRequest(t, "avatar", "a.txt", "hello"))

    if fh := c.Form[*multipart.FileHeader]("avatar"); fh == nil || fh.Filename != "a.txt" {
        t.Fatalf("Form[*multipart.FileHeader] = %v, want a.txt", fh)
    }
    if fhs := c.Form[[]*multipart.FileHeader]("avatar"); len(fhs) != 1 {
        t.Fatalf("Form[[]*multipart.FileHeader] = %v, want 1", fhs)
    }
}

func TestCtxFormMultipartMultipleFiles(t *testing.T) {
    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    for _, name := range []string{"a.txt", "b.txt"} {
        f, err := w.CreateFormFile("f", name)
        if err != nil {
            t.Fatal(err)
        }
        if _, err := f.Write([]byte("hello")); err != nil {
            t.Fatal(err)
        }
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }
    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Form[*multipart.FileHeader]("f"); got != nil {
        t.Fatalf("Form[*multipart.FileHeader] = %v, want nil", got)
    }
}

func TestCtxFormMalformedMultipart(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
    req.Header.Set(HeaderContentType, "multipart/form-data; boundary=missing")
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Form[string]("name"); got != "" {
        t.Fatalf("Form[string] = %q, want empty", got)
    }
}

// panicReader 让「不读 body」变成可观测断言。
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("body must not be read") }

func TestCtxFormQuerySkipsBody(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/form?name=张三", strings.NewReader("name=李四"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    req.Body = io.NopCloser(panicReader{})
    c := newTestCtx(httptest.NewRecorder(), req)

    if got := c.Form[string]("name"); got != "张三" {
        t.Fatalf("Form[string] = %q, want 张三", got)
    }
}
```

`multipart_test.go` 加 helper（与 `multipartFileRequest` 并列）：

```go
// multipartValueRequest 构造只含值 part 的 multipart 请求。
func multipartValueRequest(t *testing.T, kv map[string]string) *http.Request {
    t.Helper()

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    for k, v := range kv {
        if err := w.WriteField(k, v); err != nil {
            t.Fatal(err)
        }
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    return req
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bind -run 'TestFormFile' -v && go test . -run 'TestCtxForm' -v`
Expected: 编译失败 `undefined: bind.FormFile` / `undefined: bind.IsFile` / `undefined: multipartValueRequest`

- [ ] **Step 3: 实现**

`internal/bind/decoder.go` 在 `bindForm` 前加：

```go
// FormFile 取 multipart 文件字段，语义与 struct 的 form 绑定一致：
// 单文件类型收到多个文件报错，缺失返回零值。
func FormFile[T any](r *http.Request, name string) (v T, err error) {
    dst := reflect.ValueOf(&v).Elem()
    if !fileKind(dst.Type()) {
        return v, fmt.Errorf("bind: %s is not a file type", dst.Type())
    }
    if r.MultipartForm == nil {
        return
    }
    if err = setFiles(dst, r.MultipartForm.File[name]); err != nil {
        var zero T
        return zero, err
    }
    return
}

// setFiles 把文件表写入 dst（*multipart.FileHeader 或 []*multipart.FileHeader）。
func setFiles(dst reflect.Value, files []*multipart.FileHeader) error {
    if dst.Type() == fileHeadersType {
        dst.Set(reflect.ValueOf(files))
        return nil
    }

    switch len(files) {
    case 0:
        dst.SetZero()
    case 1:
        dst.Set(reflect.ValueOf(files[0]))
    default:
        return errors.New("multiple files for single field")
    }
    return nil
}
```

`bindForm` 的 121-141 行整块替换为：

```go
if f.isFile { // 从已解析的文件表取字段：缺失保留 nil，单字段收到多文件报错。
    var files []*multipart.FileHeader
    if m := r.MultipartForm; m != nil {
        files = m.File[f.key]
    }
    if err = setFiles(v, files); err != nil {
        return s.err(f, err)
    }
    continue
}
```

`internal/bind/bind.go` 在 `fileKind` 旁加：

```go
// IsFile 报告 T 是否按 multipart 文件字段解析。
func IsFile[T any]() bool {
    return fileKind(reflect.TypeFor[T]())
}
```

`ctx.go:140-150` 整体替换为：

```go
// Form 取参数并解析成 T；同名 query 优先，缺失或解析失败返回零值。
// T 可为标量、TextUnmarshaler 或它们的切片/数组/指针；元素层再套指针不支持，按零值返回。
// multipart 下文件用 *multipart.FileHeader / []*multipart.FileHeader 取（不看 query），其余类型读表单值。
func (c *Ctx) Form[T any](name string) (v T) {
    if bind.IsFile[T]() {
        _ = bind.ParseForm(c.R)
        v, _ = bind.FormFile[T](c.R, name)
        return
    }

    ss, ok := c.R.URL.Query()[name]
    if !ok {
        _ = bind.ParseForm(c.R)
        ss = c.R.PostForm[name]
    }
    v, _ = bind.Parse[T](ss)
    return
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bind -run 'TestParseForm|TestFormFile' -v && go test . -run 'TestCtxForm|TestServeHTTP' -v && go build ./... && go vet ./... && go test ./...`
Expected: PASS + 全绿

注意：`TestServeHTTPRemovesMultipartTempFiles`（`multipart_test.go:33`）在进框架前先 `ParseMultipartForm(1)`，Task 2 改完仍应通过；Task 2 新增的用例才真正锁住清理。

---

### Task 4: 文档同步

**Files:**
- Modify: `docs/superpowers/specs/2026-11-18-multipart-form-design.md`
- Modify: `docs/design-handler.md:33`、`:54`

- [ ] **Step 1: spec 加 D5**

在决策表 D4 行后加：

| D5 | `Ctx.Form` 是否同步支持（2026-11-19，反转 D4） | 支持：multipart 的值与文件都走 `bind.ParseForm`；文件类型 `*multipart.FileHeader` / `[]*multipart.FileHeader`，单字段多文件返回零值；坏 multipart 返回零值、不报错 | 用户：`Ctx.Form` 也要能取 multipart；D4 记下的不对称不再接受 |
|---|---|---|---|

同时把 §3 非目标里的「`Ctx.Form` 的 multipart 支持（D4：本次不做）」删掉，D4 行保留并标注「已被 D5 反转」。

- [ ] **Step 2: `design-handler.md`**

`:33` 末句「multipart 下不读文件、返回零值」改为：

multipart 下值与文件都能取：文件用 `*multipart.FileHeader` / `[]*multipart.FileHeader`，单字段收到多个文件返回零值；声明成其它类型时同名文件 part 不参与，得到零值。

`:54` 删掉「`Ctx.Form[T]` 不读 multipart 文件（`Bind` 支持）」这一项，保留其余延后项。

---

## 下一步

Task 1–4 已实施完成（2026-11-19）：

- Task 1：`internal/bind/decoder.go` 新增 `ParseForm`，`bindForm` 改调它；`internal/bind/form_test.go` 三个测试。
- Task 2：`app.go` 清理挂点改 `c.R.MultipartForm`；`multipart_test.go` 加 `TestServeHTTPCleansMultipart`（修复前 `temp file still readable after ServeHTTP`）。
- Task 3：`bind.FormFile[T]` / `bind.IsFile[T]` / `setFiles`；`Ctx.Form` 文件分支委托 `FormFile`（解析在 `FormFile` 内部完成，不在 `Ctx` 重复调用 `ParseForm`）；`ctx_test.go` 补多值、单类型多文件、坏请求、query 短路（`panicReader`）。
- Task 4：spec 加 D5、改非目标段；`docs/design-handler.md` 的 `Ctx.Form` 与临时文件清理说明同步。

验证：`go build ./...`、`go vet ./...`、`go test ./...` 全绿。未经用户明确说「提交」不执行 git。
