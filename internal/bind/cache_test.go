package bind_test

import (
    "io"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/baagod/mie/internal/bind"
)

func TestFormCacheQuery(t *testing.T) {
    req := httptest.NewRequest(http.MethodGet, "/?a=1&b=%zz", nil)

    var cache bind.FormCache
    if got := cache.Query(req).Get("a"); got != "1" {
        t.Fatalf("Query[a] = %q, want 1", got)
    }
    if got := cache.Query(req).Get("a"); got != "1" {
        t.Fatalf("cached Query[a] = %q, want 1", got)
    }
}

func TestFormCacheForm(t *testing.T) {
    req := multipartRequest(t, map[string]string{"name": "mux"})

    var cache bind.FormCache
    if got := cache.Form(req).Get("name"); got != "mux" {
        t.Fatalf("Form[name] = %q, want mux", got)
    }
    if req.MultipartForm == nil {
        t.Fatal("MultipartForm not parsed")
    }
}

func TestFormCacheKeepsFirstError(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/?x=1", strings.NewReader("junk"))
    req.Header.Set("Content-Type", "multipart/form-data")

    var cache bind.FormCache
    var fileIn struct {
        F *multipart.FileHeader `form:"f"`
    }
    if err := bind.Decode(req, &fileIn, &cache); err == nil {
        t.Fatal("first decode: want parse error")
    }

    var in struct {
        A string `form:"a"`
    }
    if err := bind.Decode(req, &in, &cache); err == nil {
        t.Fatal("second decode: parse error was cleared")
    }
}

func TestFormCacheQueryAfterForm(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/?name=query", strings.NewReader("name=form"))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    var cache bind.FormCache
    cache.Form(req)
    if got := cache.Query(req).Get("name"); got != "query" {
        t.Fatalf("Query after Form = %q, want query", got)
    }
}

type countingBody struct {
    r io.Reader
    n int
}

func (b *countingBody) Read(p []byte) (int, error) {
    n, err := b.r.Read(p)
    b.n += n
    return n, err
}

func TestFormCacheCachesError(t *testing.T) {
    const body = "a=%zz"
    cb := &countingBody{r: strings.NewReader(body)}
    req := httptest.NewRequest(http.MethodPost, "/", cb)
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    var cache bind.FormCache
    var in struct {
        A string `form:"a"`
    }

    if err := bind.Decode(req, &in, &cache); err == nil {
        t.Fatal("want parse error")
    }
    if cb.n != len(body) {
        t.Fatalf("body read = %d, want %d", cb.n, len(body))
    }

    if err := bind.Decode(req, &in, &cache); err == nil {
        t.Fatal("want cached parse error on second call")
    }
    if cb.n != len(body) {
        t.Fatalf("body read again: %d, want %d", cb.n, len(body))
    }
}

func TestDecodeSharesCache(t *testing.T) {
    const body = "name=mux"
    cb := &countingBody{r: strings.NewReader(body)}
    req := httptest.NewRequest(http.MethodPost, "/", cb)
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    var cache bind.FormCache
    var in struct {
        Name string `form:"name"`
    }

    if err := bind.Decode(req, &in, &cache); err != nil {
        t.Fatal(err)
    }
    if in.Name != "mux" {
        t.Fatalf("Name = %q, want mux", in.Name)
    }
    if err := bind.Decode(req, &in, &cache); err != nil {
        t.Fatal(err)
    }
    if cb.n != len(body) {
        t.Fatalf("body read = %d, want %d", cb.n, len(body))
    }
}
