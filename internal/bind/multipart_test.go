package bind_test

import (
    "bytes"
    "github.com/baagod/mie/internal/bind"
    "io"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

// multipartRequest 构造只含文字字段的 multipart 请求；URL 固定带 ?page=2，用于验证覆盖语义。
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

func TestDecodeMultipartValues(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    var got in
    if err := bind.Decode(multipartRequest(t, map[string]string{"name": "张三"}), &got, nil); err != nil {
        t.Fatal(err)
    }

    if got.Name != "张三" {
        t.Fatalf("Name = %q, want 张三", got.Name)
    }
}

func TestDecodeMultipartQueryOverridesForm(t *testing.T) {
    type in struct {
        Page int    `form:"page"`
        Name string `form:"name"`
    }

    var got in
    if err := bind.Decode(multipartRequest(t, map[string]string{"page": "1", "name": "x"}), &got, nil); err != nil {
        t.Fatal(err)
    }

    if got.Page != 2 {
        t.Fatalf("Page = %d, want 2 (query 覆盖 form)", got.Page)
    }
    if got.Name != "x" {
        t.Fatalf("Name = %q, want x (multipart 值应能读到)", got.Name)
    }
}

func TestDecodeMultipartMalformed(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
    req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")

    var got in
    if err := bind.Decode(req, &got, nil); err == nil {
        t.Fatal("want error for malformed multipart, got nil")
    }
}

// multipartWithFiles 构造只含文件 part 的 multipart 请求，每个文件写入内容 hello。
func multipartWithFiles(t *testing.T, field string, names []string) *http.Request {
    t.Helper()

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    for _, name := range names {
        f, err := w.CreateFormFile(field, name)
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
    return req
}

func TestDecodeMultipartFile(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `form:"avatar"`
    }

    var got in
    if err := bind.Decode(multipartWithFiles(t, "avatar", []string{"a.txt"}), &got, nil); err != nil {
        t.Fatal(err)
    }
    if got.Avatar == nil {
        t.Fatal("Avatar = nil, want file header")
    }
    if got.Avatar.Filename != "a.txt" {
        t.Fatalf("Filename = %q, want a.txt", got.Avatar.Filename)
    }

    f, err := got.Avatar.Open()
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()

    body, err := io.ReadAll(f)
    if err != nil {
        t.Fatal(err)
    }
    if string(body) != "hello" {
        t.Fatalf("content = %q, want hello", body)
    }
}

func TestDecodeMultipartFiles(t *testing.T) {
    type in struct {
        Files []*multipart.FileHeader `form:"files"`
    }

    var got in
    if err := bind.Decode(multipartWithFiles(t, "files", []string{"a.txt", "b.txt"}), &got, nil); err != nil {
        t.Fatal(err)
    }

    if len(got.Files) != 2 {
        t.Fatalf("len(Files) = %d, want 2", len(got.Files))
    }
    for i, want := range []string{"a.txt", "b.txt"} {
        if got.Files[i].Filename != want {
            t.Fatalf("Files[%d].Filename = %q, want %q", i, got.Files[i].Filename, want)
        }
    }
}

// 绑定拿到的切片必须与 MultipartForm 里的列表脱钩：
// handler 替换元素不能改到框架用于清理临时文件的表。
func TestDecodeMultipartFilesCopy(t *testing.T) {
    type in struct {
        Files []*multipart.FileHeader `form:"files"`
    }

    req := multipartWithFiles(t, "files", []string{"a.txt"})
    var got in
    if err := bind.Decode(req, &got, nil); err != nil {
        t.Fatal(err)
    }

    got.Files[0] = nil
    if req.MultipartForm.File["files"][0] == nil {
        t.Fatal("bound slice aliases MultipartForm.File")
    }
}

// 文件切片之后的字段仍要绑定：切片分支曾经用 return 提前退出整个 bindForm。
func TestDecodeMultipartFilesThenValue(t *testing.T) {
    type in struct {
        Files []*multipart.FileHeader `form:"files"`
        Name  string                  `form:"name"`
    }

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    for _, name := range []string{"a.txt", "b.txt"} {
        fw, err := w.CreateFormFile("files", name)
        if err != nil {
            t.Fatal(err)
        }
        if _, err := fw.Write([]byte("hello")); err != nil {
            t.Fatal(err)
        }
    }
    if err := w.WriteField("name", "x"); err != nil {
        t.Fatal(err)
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())

    var got in
    if err := bind.Decode(req, &got, nil); err != nil {
        t.Fatal(err)
    }
    if len(got.Files) != 2 {
        t.Fatalf("len(Files) = %d, want 2", len(got.Files))
    }
    if got.Name != "x" {
        t.Fatalf("Name = %q, want x", got.Name)
    }
}

func TestDecodeMultipartFileMissing(t *testing.T) {
    type in struct {
        Name   string                `form:"name"`
        Avatar *multipart.FileHeader `form:"avatar"`
    }

    var got in
    if err := bind.Decode(multipartRequest(t, map[string]string{"name": "x"}), &got, nil); err != nil {
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
    err := bind.Decode(req, &got, nil)
    if err == nil {
        t.Fatal("want error for multiple files into single field, got nil")
    }
    if !strings.Contains(err.Error(), "multiple files") {
        t.Fatalf("error = %q, want mentioning multiple files", err)
    }
}

func TestDecodeMultipartFileDeclaredAsString(t *testing.T) {
    type in struct {
        Avatar string `form:"avatar"`
    }

    req := multipartWithFiles(t, "avatar", []string{"a.txt"})
    var got in
    if err := bind.Decode(req, &got, nil); err == nil {
        t.Fatal("want error for file part bound to string, got nil")
    }
}

// multipartWithFileAndQuery 复用 multipartWithFiles，再给 URL 补一个参数。
func multipartWithFileAndQuery(t *testing.T, query, field, name string) *http.Request {
    t.Helper()

    req := multipartWithFiles(t, field, []string{name})
    req.URL.RawQuery = query
    return req
}

// 锁定：URL 命中同名参数时不读 body，同名文件 part 被忽略而不是 400。
func TestDecodeMultipartURLHitSkipsFilePart(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    var got in
    if err := bind.Decode(multipartWithFileAndQuery(t, "name=query", "name", "a.txt"), &got, nil); err != nil {
        t.Fatalf("Decode = %v, want nil（URL 命中不读 body）", err)
    }
    if got.Name != "query" {
        t.Fatalf("Name = %q, want query", got.Name)
    }
}

// 文件字段只认文件 part：同名 URL 参数被忽略。
func TestDecodeMultipartFileIgnoresQuery(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `form:"avatar"`
    }

    var got in
    if err := bind.Decode(multipartWithFileAndQuery(t, "avatar=x", "avatar", "a.txt"), &got, nil); err != nil {
        t.Fatal(err)
    }
    if got.Avatar == nil {
        t.Fatal("Avatar = nil, want file header")
    }
    if got.Avatar.Filename != "a.txt" {
        t.Fatalf("Filename = %q, want a.txt", got.Avatar.Filename)
    }
}
