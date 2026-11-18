# multipart/form-data 绑定 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `Ctx.Bind` 能绑定 `multipart/form-data` 的表单值，并把 `*multipart.FileHeader` / `[]*multipart.FileHeader` 字段填上。

**Architecture:** 解析入口只在 `internal/bind/decoder.go` 分支（multipart 走 `ParseMultipartForm`，其余维持 `ParseForm`），值因此自动进入 `r.PostForm`，`bind` 的 form 源与「query 覆盖 form」语义零改动；文件作为新的字段类型通道接在 `bindValue` 之前；临时文件清理挂在请求级 `defer`。

**Tech Stack:** Go 标准库 `net/http`、`mime/multipart`、`reflect`；测试用 `testing` + `httptest`。

**Spec:** docs/superpowers/specs/2026-11-18-multipart-form-design.md

## Global Constraints

- D2：multipart 继续豁免 `MaxBodyBytes`；`maxMemory` 用常量 `32 << 20`，不新增配置项。
- D3：multipart 解析失败 → `Bind` 返回 400（沿用 `bind.Decode` 的 `fmt.Errorf("bind params: %w", err)` → `ctx.Bind` 现有 400 映射）。
- D1：不新增公开方法（不加 `Ctx.FormFile`），文件只经 struct 字段绑定。
- D4：`Ctx.Form[T]` 本次不改，multipart 下仍返回零值——这是已接受的不对称，只在文档说明，不写测试锁定（测一个已知缺口等于把它固定死）。
- 已排除的复杂度：不加公开方法（`Ctx.FormFile`）、不加配置项、不给 `field`/`plan` 加新字段、不新增错误类型、不缓存解析结果、不写流式解析。

- 仓库未发布，不需要兼容层；全部新增代码沿用现有空格缩进风格（仓库未 gofmt，不要顺手重排）。
- 每个任务结束跑 `go build ./... && go vet ./... && go test ./...`，全绿后才提交。

## Review Focus

- 缺 `boundary` 或 part 损坏：必须 400，不能静默零值。
- 文件字段缺失 vs 传了空文件：缺失给 `nil`；空文件给 `FileHeader`（`Size == 0`）。
- 单文件字段收到多个同名文件：400，不能静默取第一个。
- 临时文件清理：handler 返回后（panic 路径也是）必须删掉，否则磁盘泄漏；handler 期间 `Open()` 必须仍可用。
- multipart 下 query 覆盖 form：值必须走同一套 form 源，语义与 urlencoded 一致。

---

### Task 1: 解析入口按 Content-Type 分支

**Files:**
- Modify: `internal/bind/decoder.go:38-48`
- Create: `internal/bind/multipart_test.go`

**Interfaces:**
- Produces: `const multipartMemory = 32 << 20`（包内私有）
- Consumes: `p.hasParams`（`internal/bind/plan.go:36`）

- [ ] **Step 1: 写失败测试**

在 `internal/bind/multipart_test.go`（`package bind`）写三个测试。构造请求的辅助函数：

```go
func multipartRequest(t *testing.T, fields map[string]string) *http.Request {
    t.Helper()
    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    for k, v := range fields {
        if err := w.WriteField(k, v); err != nil {
            t.Fatal(err)
        }
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }
    req := httptest.NewRequest(http.MethodPost, "/?page=2", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    return req
}
```

```go
func TestDecodeMultipartValues(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }
    req := multipartRequest(t, map[string]string{"name": "张三"})
    var got in
    if err := Decode(req, &got); err != nil {
        t.Fatal(err)
    }
    if got.Name != "张三" {
        t.Fatalf("Name = %q, want 张三", got.Name)
    }
}

func TestDecodeMultipartQueryOverridesForm(t *testing.T) {
    type in struct {
        Page int `form:"page"`
    }
    req := multipartRequest(t, map[string]string{"page": "1"}) // URL 里有 ?page=2
    var got in
    if err := Decode(req, &got); err != nil {
        t.Fatal(err)
    }
    if got.Page != 2 {
        t.Fatalf("Page = %d, want 2 (query 覆盖 form)", got.Page)
    }
}

func TestDecodeMultipartMalformed(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }
    req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
    req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
    var got in
    if err := Decode(req, &got); err == nil {
        t.Fatal("want error for malformed multipart, got nil")
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bind -run 'TestDecodeMultipart' -v`
Expected: 三个都 FAIL——`ParseForm` 对 multipart 直接返回 nil 但不解析，值全空、坏请求也不报错。

- [ ] **Step 3: 实现分支**

`internal/bind/decoder.go` 顶部加常量（`mime` 加入 import）：

```go
// multipartMemory 是 multipart 值/小文件驻留内存的上限，超出部分标准库写到磁盘临时文件；
// 数值取标准库内部默认值（defaultMaxMemory 未导出）。传 0 不会回落到默认值，而是全部写盘。
const multipartMemory = 32 << 20
```

`Decode` 里原本的 `r.ParseForm()` 就地改为按 Content-Type 分支（不引入 helper，避免多一个只被调用一次的函数）：

```go
if p.hasParams {
    // multipart 的值只能经 ParseMultipartForm 进入 PostForm；其它类型继续走 ParseForm。
    if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
        err = r.ParseMultipartForm(multipartMemory)
    } else {
        err = r.ParseForm()
    }
    if err != nil {
        return fmt.Errorf("bind params: %w", err)
    }
    query = r.URL.Query()
}
```

（`mime.ParseMediaType` 解析失败时返回空 ct，走 `ParseForm`，与现有行为一致。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bind -run 'TestDecodeMultipart' -v`
Expected: 三个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/bind/decoder.go internal/bind/multipart_test.go
git commit -m "feat(bind): 按 Content-Type 选择 ParseMultipartForm"
```

---

### Task 2: 文件字段绑定

**Files:**
- Create: `internal/bind/file.go`
- Modify: `internal/bind/plan.go:83-86`（类型校验与文件字段的 tag 限制）、`internal/bind/scalar.go:37`（`bindValue` 的 `bindForm` 分支）
- Test: `internal/bind/multipart_test.go`

**Interfaces:**
- Produces: `func fileKind(t reflect.Type) bool`、`func bindFile(r *http.Request, v reflect.Value, f field) error`
- Consumes: `field{source, key, name}`、`r.MultipartForm.File`

- [ ] **Step 1: 写失败测试**

```go
func multipartWithFile(t *testing.T, field, filename, content string) *http.Request {
    t.Helper()
    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    part, err := w.CreateFormFile(field, filename)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = io.WriteString(part, content); err != nil {
        t.Fatal(err)
    }
    if err = w.Close(); err != nil {
        t.Fatal(err)
    }
    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    return req
}

func TestDecodeMultipartFile(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `form:"avatar"`
    }
    req := multipartWithFile(t, "avatar", "a.txt", "hello")
    var got in
    if err := Decode(req, &got); err != nil {
        t.Fatal(err)
    }
    if got.Avatar == nil || got.Avatar.Filename != "a.txt" {
        t.Fatalf("Avatar = %+v, want filename a.txt", got.Avatar)
    }
    f, err := got.Avatar.Open()
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()
    body, _ := io.ReadAll(f)
    if string(body) != "hello" {
        t.Fatalf("content = %q, want hello", body)
    }
}

func TestDecodeMultipartFiles(t *testing.T) {
    type in struct {
        Docs []*multipart.FileHeader `form:"docs"`
    }
    req := multipartWithFiles(t, "docs", []string{"a.txt", "b.txt"})
    var got in
    if err := Decode(req, &got); err != nil {
        t.Fatal(err)
    }
    if len(got.Docs) != 2 || got.Docs[0].Filename != "a.txt" || got.Docs[1].Filename != "b.txt" {
        t.Fatalf("Docs = %+v, want a.txt,b.txt", got.Docs)
    }
}

func TestDecodeMultipartFileMissing(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `form:"avatar"`
    }
    var got in
    if err := Decode(multipartRequest(t, map[string]string{"name": "x"}), &got); err != nil {
        t.Fatal(err)
    }
    if got.Avatar != nil {
        t.Fatalf("Avatar = %+v, want nil", got.Avatar)
    }
}

func TestDecodeMultipartSingleFileFieldRejectsMultiple(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `form:"avatar"`
    }
    req := multipartWithFiles(t, "avatar", []string{"a.txt", "b.txt"})
    var got in
    if err := Decode(req, &got); err == nil {
        t.Fatal("want error for multiple files into single field, got nil")
    }
}

func TestDecodeMultipartFileDeclaredAsString(t *testing.T) {
    type in struct {
        Avatar string `form:"avatar"`
    }
    var got in
    if err := Decode(multipartWithFiles(t, "avatar", []string{"a.txt"}), &got); err == nil {
        t.Fatal("want error for file part bound to string, got nil")
    }
}
```

`multipartWithFiles(t, field, names)` 的写法：循环 `w.CreateFormFile(field, name)`，每个写一行内容。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bind -run 'TestDecodeMultipartFile' -v`
Expected: 编译失败或 `Plan` 校验报 `unsupported binding type *multipart.FileHeader`——四种情况都不通过。

- [ ] **Step 3: 实现**

`internal/bind/file.go`：

```go
package bind

import (
    "errors"
    "net/http"
    "reflect"
    "mime/multipart"
)

var (
    fileHeaderType  = reflect.TypeFor[*multipart.FileHeader]()
    fileHeadersType = reflect.TypeFor[[]*multipart.FileHeader]()
)

func fileKind(t reflect.Type) bool { return t == fileHeaderType || t == fileHeadersType }

// bindFile 从已解析的 multipart 文件表取字段：缺失保留 nil，单字段收到多个文件报错。
func bindFile(r *http.Request, v reflect.Value, f field) error {
    var hs []*multipart.FileHeader
    if mf := r.MultipartForm; mf != nil {
        hs = mf.File[f.key]
    }

    if v.Type() == fileHeadersType {
        if hs == nil {
            v.SetZero()
            return nil
        }
        v.Set(reflect.ValueOf(hs))
        return nil
    }

    switch len(hs) {
    case 0:
        v.SetZero()
    case 1:
        v.Set(reflect.ValueOf(hs[0]))
    default:
        return errors.New("bind: " + f.source + "." + f.name + ": multiple files for single field")
    }
    return nil
}
```

`internal/bind/plan.go:83` 的校验改为（文件字段只认 `form` tag）：

```go
if fileKind(sf.Type) {
    if tag != bindForm {
        p.err = errors.New("field " + t.String() + "." + sf.Name + ": file field only supports form tag")
        return p
    }
} else if !scalarKind(sf.Type) {
```

`internal/bind/scalar.go` 的 `bindValue` 里 `case bindForm:` 开头加：

```go
if fileKind(v.Type()) {
    return bindFile(r, v, f)
}
// 文件 part 不进 PostForm，声明成非文件类型只会静默得到零值。
if mf := r.MultipartForm; mf != nil && len(mf.File[f.key]) > 0 {
    return errors.New("bind: " + f.source + "." + f.name + ": file part must bind to *multipart.FileHeader")
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bind -run 'TestDecodeMultipart' -v`
Expected: 全部 PASS；再跑 `go test ./internal/bind ./...`。

- [ ] **Step 5: 提交**

```bash
git add internal/bind/file.go internal/bind/plan.go internal/bind/scalar.go internal/bind/multipart_test.go
git commit -m "feat(bind): 支持 *multipart.FileHeader 字段绑定"
```

---

### Task 3: 请求级清理与文档

**Files:**
- Modify: `app.go`（`ServeHTTP` 里 `newCtx` 之后加 defer）
- Test: `app_test.go`
- Modify: `docs/design-handler.md`（§3 或 §5 的 multipart 说明）

**Interfaces:**
- Produces: `func cleanupMultipart(r *http.Request)`（包内私有）

- [ ] **Step 1: 写失败测试**

`app_test.go`：

```go
func TestCleanupMultipart(t *testing.T) {
    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    part, _ := w.CreateFormFile("f", "a.txt")
    part.Write([]byte("0123456789"))
    w.Close()

    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    if err := req.ParseMultipartForm(1); err != nil { // 1 字节内存 → 落磁盘临时文件
        t.Fatal(err)
    }
    fh := req.MultipartForm.File["f"][0]

    cleanupMultipart(req)

    if _, err := fh.Open(); err == nil {
        t.Fatal("temp file still readable after cleanup")
    }

    cleanupMultipart(httptest.NewRequest(http.MethodGet, "/", nil)) // 非 multipart：不应 panic
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test . -run 'TestCleanupMultipart' -v`
Expected: 编译失败：`undefined: cleanupMultipart`。

- [ ] **Step 3: 实现**

`app.go` 加：

```go
// cleanupMultipart 删除 ParseMultipartForm 落在磁盘上的临时文件；非 multipart 请求是空操作。
func cleanupMultipart(r *http.Request) {
    if r.MultipartForm != nil {
        _ = r.MultipartForm.RemoveAll()
    }
}
```

`ServeHTTP` 里 `c := newCtx(...)` 之后立刻：

```go
defer cleanupMultipart(c.R)
```

`docs/design-handler.md` §5（multipart 那条）改为：`Bind` 已支持 `multipart/form-data` 的值与 `*multipart.FileHeader` 字段；`Ctx.Form[T]` 仍只读 form/query（D4）；multipart 不受 `MaxBodyBytes` 约束，`maxMemory` 固定 32 MiB，请求结束框架删除临时文件。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test . -run 'TestCleanupMultipart' -v && go build ./... && go vet ./... && go test ./...`
Expected: PASS + 全绿。

- [ ] **Step 5: 提交**

```bash
git add app.go app_test.go docs/design-handler.md
git commit -m "fix(mie): 请求结束清理 multipart 临时文件"
```

---

## 回归锁定（并入 Task 1 的测试文件）

- [ ] `TestDecodeMultipartFormQueryPriority`：`?page=2` + form `page=1` → 2（Task 1 Step 1 已有）。
- [ ] `TestBindMultipartMalformed400`（`ctx_test.go`）：`c.Bind` 遇坏 multipart 返回 `*Error` 且 `Status == 400`。
