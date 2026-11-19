package bind_test

import (
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/baagod/mie/internal/bind"
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

func TestParseFile(t *testing.T) {
    req := multipartWithFiles(t, "avatar", []string{"a.txt", "b.txt"})

    files, err := bind.ParseFile[[]*multipart.FileHeader](req, "avatar")
    if err != nil {
        t.Fatalf("ParseFile: %v", err)
    }
    if len(files) != 2 {
        t.Fatalf("want 2 files, got %d", len(files))
    }

    if _, err := bind.ParseFile[*multipart.FileHeader](req, "avatar"); err == nil {
        t.Fatal("want error for two files into single file type")
    }

    single, err := bind.ParseFile[*multipart.FileHeader](req, "missing")
    if err != nil {
        t.Fatalf("missing file: %v", err)
    }
    if single != nil {
        t.Fatalf("want nil for missing file, got %v", single)
    }

    if _, err := bind.ParseFile[string](req, "avatar"); err == nil {
        t.Fatal("want error for non-file type")
    }

    if !bind.IsFile[*multipart.FileHeader]() || !bind.IsFile[[]*multipart.FileHeader]() {
        t.Fatal("IsFile should recognize file types")
    }
    if bind.IsFile[string]() {
        t.Fatal("IsFile should not recognize string")
    }
}
