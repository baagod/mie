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

// 锁定：handler 自己解析 multipart 时，临时文件也必须由框架清理。
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

// multipartValueRequest 构造含一个普通字段 part 的请求。
func multipartValueRequest(t *testing.T, field, value string) *http.Request {
	t.Helper()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := w.WriteField(field, value); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}
