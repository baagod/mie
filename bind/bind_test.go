package bind_test

import (
    "net/http"
    "net/http/httptest"
    "reflect"
    "strings"
    "testing"

    "github.com/baagod/mie/bind"
)

func TestNewPlanRejectsUnsupportedFieldType(t *testing.T) {
    type in struct {
        M map[string]int `query:"m"`
    }

    _, err := bind.NewPlan(reflect.TypeFor[in]())
    if err == nil || !strings.Contains(err.Error(), "unsupported binding type map[string]int") {
        t.Fatalf("err = %v, want unsupported binding type", err)
    }
}

func TestNewPlanSkipsUnexportedAndEmptyTags(t *testing.T) {
    type in struct {
        id   int    `query:"id"`
        Name string `query:""`
    }

    p, err := bind.NewPlan(reflect.TypeFor[in]())
    if err != nil {
        t.Fatalf("NewPlan: %v", err)
    }
    var v in
    if err := p.Bind(httptest.NewRequest(http.MethodGet, "/?id=5", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.Name != "" {
        t.Fatalf("Name = %q, want empty", v.Name)
    }
}

func TestBindRequiresNonNilPointer(t *testing.T) {
    p, err := bind.NewPlan(reflect.TypeFor[struct{}]())
    if err != nil {
        t.Fatal(err)
    }

    for _, dst := range []any{struct{}{}, (*struct{})(nil), &struct{ ID int }{}} {
        err := p.Bind(httptest.NewRequest(http.MethodGet, "/", nil), dst)
        if err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
            t.Fatalf("Bind(%T) err = %v, want non-nil pointer error", dst, err)
        }
    }
}

func TestBindPointerInputAllocates(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }
    p, err := bind.NewPlan(reflect.TypeFor[*in]())
    if err != nil {
        t.Fatal(err)
    }

    var v *in
    if err := p.Bind(httptest.NewRequest(http.MethodGet, "/?id=7", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v == nil || v.ID != 7 {
        t.Fatalf("v = %#v, want non-nil with ID 7", v)
    }
}

func TestBindPointerInputRejectsValue(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }
    p, err := bind.NewPlan(reflect.TypeFor[*in]())
    if err != nil {
        t.Fatal(err)
    }

    v := in{}
    err = p.Bind(httptest.NewRequest(http.MethodGet, "/", nil), &v)
    if err == nil || !strings.Contains(err.Error(), "pointer to IN") {
        t.Fatalf("Bind(*T) err = %v, want pointer-to-IN error", err)
    }
}

func TestBindSliceAndArray(t *testing.T) {
    type in struct {
        Tags []string `query:"tag"`
        N    [2]int   `query:"n"`
    }
    p, err := bind.NewPlan(reflect.TypeFor[in]())
    if err != nil {
        t.Fatal(err)
    }

    var v in
    if err := p.Bind(httptest.NewRequest(http.MethodGet, "/?tag=a&tag=b&n=1", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if len(v.Tags) != 2 || v.Tags[0] != "a" || v.Tags[1] != "b" {
        t.Fatalf("Tags = %q, want [a b]", v.Tags)
    }
    if v.N != [2]int{1, 0} {
        t.Fatalf("N = %v, want [1 0]", v.N)
    }
}

func TestBindJSONDecodes(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }
    p, err := bind.NewPlan(reflect.TypeFor[in]())
    if err != nil {
        t.Fatal(err)
    }

    var v in
    if err := p.BindJSON([]byte(`{"name":"alice"}`), &v); err != nil {
        t.Fatalf("BindJSON: %v", err)
    }
    if v.Name != "alice" {
        t.Fatalf("Name = %q, want alice", v.Name)
    }
}
