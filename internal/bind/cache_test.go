package bind_test

import (
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/baagod/mie/internal/bind"
)

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
