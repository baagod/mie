package bind

import (
    "bytes"
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
    if err := Decode(multipartRequest(t, map[string]string{"name": "张三"}), &got); err != nil {
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
    if err := Decode(multipartRequest(t, map[string]string{"page": "1", "name": "x"}), &got); err != nil {
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
    if err := Decode(req, &got); err == nil {
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
    if err := Decode(multipartWithFiles(t, "avatar", []string{"a.txt"}), &got); err != nil {
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
    if err := Decode(multipartWithFiles(t, "files", []string{"a.txt", "b.txt"}), &got); err != nil {
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
    if err := Decode(req, &got); err != nil {
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
    err := Decode(req, &got)
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
    if err := Decode(req, &got); err == nil {
        t.Fatal("want error for file part bound to string, got nil")
    }
}

func TestDecodeMultipartFileWithQueryTagIgnored(t *testing.T) {
    type in struct {
        Avatar *multipart.FileHeader `query:"avatar"`
    }

    req := multipartWithFiles(t, "avatar", []string{"a.txt"})
    var got in
    if err := Decode(req, &got); err != nil {
        t.Fatalf("err = %v, want nil: query tag on file field is ignored", err)
    }
    if got.Avatar != nil {
        t.Fatalf("Avatar = %+v, want nil", got.Avatar)
    }
}
