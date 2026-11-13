package bind_test

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/baagod/mie/bind"
)

func TestBindRejectsUnsupportedFieldType(t *testing.T) {
    type in struct {
        M map[string]int `query:"m"`
    }

    var v in
    err := bind.Bind[in](httptest.NewRequest(http.MethodGet, "/?m=1", nil), &v)
    if err == nil || !strings.Contains(err.Error(), "unsupported binding type map[string]int") {
        t.Fatalf("err = %v, want unsupported binding type", err)
    }
}

func TestBindSkipsUnexportedAndEmptyTags(t *testing.T) {
    type in struct {
        id   int    `query:"id"`
        Name string `query:""`
    }

    var v in
    if err := bind.Bind[in](httptest.NewRequest(http.MethodGet, "/?id=5", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.Name != "" {
        t.Fatalf("Name = %q, want empty", v.Name)
    }
}

func TestBindRequiresNonNilPointer(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }

    err := bind.Bind[in](httptest.NewRequest(http.MethodGet, "/", nil), nil)
    if err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
        t.Fatalf("Bind(nil) err = %v, want non-nil pointer error", err)
    }
}

func TestBindPointerInputAllocates(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }

    var v *in
    if err := bind.Bind[*in](httptest.NewRequest(http.MethodGet, "/?id=7", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v == nil || v.ID != 7 {
        t.Fatalf("v = %#v, want non-nil with ID 7", v)
    }
}

func TestBindSliceAndArray(t *testing.T) {
    type in struct {
        Tags []string `query:"tag"`
        N    [2]int   `query:"n"`
    }

    var v in
    if err := bind.Bind[in](httptest.NewRequest(http.MethodGet, "/?tag=a&tag=b&n=1", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if len(v.Tags) != 2 || v.Tags[0] != "a" || v.Tags[1] != "b" {
        t.Fatalf("Tags = %q, want [a b]", v.Tags)
    }
    if v.N != [2]int{1, 0} {
        t.Fatalf("N = %v, want [1 0]", v.N)
    }
}

func TestBindConcurrentFirstUse(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }

    type result struct {
        v   in
        err error
    }

    const n = 16
    results := make(chan result, n)
    var wg sync.WaitGroup
    wg.Add(n)

    for range n {
        go func() {
            defer wg.Done()
            var v in
            err := bind.Bind[in](httptest.NewRequest(http.MethodGet, "/?id=7", nil), &v)
            results <- result{v, err}
        }()
    }

    wg.Wait()
    close(results)

    for r := range results {
        if r.err != nil {
            t.Fatalf("Bind: %v", r.err)
        }
        if r.v.ID != 7 {
            t.Fatalf("ID = %d, want 7", r.v.ID)
        }
    }
}
