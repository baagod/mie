package mie

import (
    "bytes"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "testing"
)

// multipartFileRequest 构造含一个文件 part 的请求。
func multipartFileRequest(t *testing.T, field, name, content string) *http.Request {
    t.Helper()

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    f, err := w.CreateFormFile(field, name)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := f.Write([]byte(content)); err != nil {
        t.Fatal(err)
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    return req
}

func TestServeHTTPRemovesMultipartTempFiles(t *testing.T) {
    req := multipartFileRequest(t, "file", "a.txt", "hello")
    // 进框架前先解析，maxMemory 给 1 字节，强制文件落到磁盘临时文件。
    if err := req.ParseMultipartForm(1); err != nil {
        t.Fatal(err)
    }
    fh := req.MultipartForm.File["file"][0]

    r := New()
    r.Post[struct{}, struct{}]("/upload", func(c *Ctx, _ struct{}) (struct{}, error) {
        return struct{}{}, nil
    })
    r.ServeHTTP(httptest.NewRecorder(), req)

    if _, err := fh.Open(); err == nil {
        t.Fatal("want error opening temp file after request, got nil")
    }
}

func TestServeHTTPWithoutMultipartDoesNotPanic(t *testing.T) {
    r := New()
    r.Get[struct{}, struct{}]("/", func(c *Ctx, _ struct{}) (struct{}, error) {
        return struct{}{}, nil
    })
    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
