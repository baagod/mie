package bind_test

import (
    "net"
    "net/http"
    "net/http/httptest"
    "net/netip"
    "reflect"
    "slices"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/baagod/mie/internal/bind"
)

func TestBindRejectsUnsupportedFieldType(t *testing.T) {
    type in struct {
        M map[string]int `form:"m"`
    }

    var v in
    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?m=1", nil), &v)
    if err == nil || !strings.Contains(err.Error(), "unsupported binding type map[string]int") {
        t.Fatalf("err = %v, want unsupported binding type", err)
    }
}

// 声明校验与解析器必须同口径：Parse 能写多级指针，Validate 就不能拒绝它。
func TestValidateAcceptsMultiLevelPointer(t *testing.T) {
    withFormTag := func(typ reflect.Type) reflect.Type {
        return reflect.StructOf([]reflect.StructField{{Name: "F", Type: typ, Tag: `form:"f"`}})
    }

    for _, typ := range []reflect.Type{reflect.TypeFor[*int](), reflect.TypeFor[**int]()} {
        if err := bind.Validate(withFormTag(typ)); err != nil {
            t.Errorf("Validate(%v) = %v, want nil", typ, err)
        }
    }

    if err := bind.Validate(withFormTag(reflect.TypeFor[map[string]int]())); err == nil {
        t.Error("Validate(map[string]int) = nil, want error")
    }
}

func TestBindRejectsDoublePointerIN(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    err := bind.Validate(reflect.TypeFor[**in]())
    if err == nil {
        t.Fatal("Validate(**in) = nil, want error")
    }
    if !strings.Contains(err.Error(), "pointer to a pointer") {
        t.Errorf("Validate(**in) = %v, want error about pointer to a pointer", err)
    }

    var dst **in
    req := httptest.NewRequest(http.MethodPost, "/?name=x", nil)
    err = bind.Decode(req, &dst)
    if err == nil {
        t.Fatal("Decode(**in) = nil, want error")
    }
    if !strings.Contains(err.Error(), "pointer to a pointer") {
        t.Errorf("Decode(**in) = %v, want error about pointer to a pointer", err)
    }
}

func TestBindSkipsUnexportedAndEmptyTags(t *testing.T) {
    type in struct {
        id   int    `form:"id"`
        Name string `form:""`
    }

    var v in
    if err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?id=5", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.Name != "" {
        t.Fatalf("Name = %q, want empty", v.Name)
    }
}

func TestBindRequiresNonNilPointer(t *testing.T) {
    type in struct {
        ID int `form:"id"`
    }

    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/", nil), nil)
    if err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
        t.Fatalf("Bind(nil) err = %v, want non-nil pointer error", err)
    }
}

func TestBindPointerInputAllocates(t *testing.T) {
    type in struct {
        ID int `form:"id"`
    }

    var v *in
    if err := bind.Decode[*in](httptest.NewRequest(http.MethodGet, "/?id=7", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v == nil || v.ID != 7 {
        t.Fatalf("v = %#v, want non-nil with ID 7", v)
    }
}

func TestBindSliceAndArray(t *testing.T) {
    type in struct {
        Tags []string `form:"tag"`
        N    [2]int   `form:"n"`
    }

    var v in
    if err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?tag=a&tag=b&n=1", nil), &v); err != nil {
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
        ID int `form:"id"`
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
            err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?id=7", nil), &v)
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

func TestBindCookie(t *testing.T) {
    type in struct {
        Session string   `cookie:"sid"`
        Codes   []string `cookie:"code"`
        Missing string   `cookie:"none"`
        Cleared string   `cookie:"clear"`
    }

    r := httptest.NewRequest(http.MethodGet, "/", nil)
    r.AddCookie(&http.Cookie{Name: "sid", Value: "abc"})
    r.AddCookie(&http.Cookie{Name: "code", Value: "a"})
    r.AddCookie(&http.Cookie{Name: "code", Value: "b"})
    r.AddCookie(&http.Cookie{Name: "clear", Value: ""})

    v := in{Cleared: "old"}
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.Session != "abc" {
        t.Fatalf("Session = %q, want abc", v.Session)
    }
    if len(v.Codes) != 2 || v.Codes[0] != "a" || v.Codes[1] != "b" {
        t.Fatalf("Codes = %q, want [a b]", v.Codes)
    }
    if v.Missing != "" {
        t.Fatalf("Missing = %q, want empty", v.Missing)
    }
    if v.Cleared != "" {
        t.Fatalf("Cleared = %q, want empty", v.Cleared)
    }
}

func TestBindCookieInvalidValue(t *testing.T) {
    type in struct {
        N int `cookie:"n"`
    }

    r := httptest.NewRequest(http.MethodGet, "/", nil)
    r.AddCookie(&http.Cookie{Name: "n", Value: "abc"})

    var v in
    err := bind.Decode(r, &v)
    if err == nil || !strings.Contains(err.Error(), "bind: cookie.N") {
        t.Fatalf("err = %v, want bind: cookie.N", err)
    }
}

func TestBindCookieOverridesQuery(t *testing.T) {
    type in struct {
        ID string `cookie:"id" form:"id"`
    }

    r := httptest.NewRequest(http.MethodGet, "/?id=from-query", nil)
    r.AddCookie(&http.Cookie{Name: "id", Value: "from-cookie"})

    var v in
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.ID != "from-cookie" {
        t.Fatalf("ID = %q, want from-cookie", v.ID)
    }
}

func TestBindHeaderOverridesCookie(t *testing.T) {
    type in struct {
        User string `header:"X-User" cookie:"user"`
    }

    r := httptest.NewRequest(http.MethodGet, "/", nil)
    r.Header.Set("X-User", "from-header")
    r.AddCookie(&http.Cookie{Name: "user", Value: "from-cookie"})

    var v in
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.User != "from-header" {
        t.Fatalf("User = %q, want from-header", v.User)
    }
}

func TestBindFormReadsBodyAndQueryWins(t *testing.T) {
    r := httptest.NewRequest(http.MethodPost, "/?name=from-query&page=3", strings.NewReader("name=from-form&city=Paris"))
    r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    var v struct {
        Name string `form:"name"`
        Page int    `form:"page"`
        City string `form:"city"`
    }
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.Name != "from-query" || v.Page != 3 || v.City != "Paris" {
        t.Fatalf("v = %+v, want query over form and body-only field", v)
    }
}

// upperText 是测试用的 TextUnmarshaler 实现。
type upperText string

func (u *upperText) UnmarshalText(b []byte) error {
    *u = upperText(strings.ToUpper(string(b)))
    return nil
}

func TestBindTextUnmarshaler(t *testing.T) {
    type in struct {
        At  time.Time  `form:"at"`
        IP  netip.Addr `form:"ip"`
        Tag upperText  `header:"X-Tag"`
    }

    r := httptest.NewRequest(http.MethodGet, "/?at=2026-11-18T03:00:00Z&ip=127.0.0.1", nil)
    r.Header.Set("X-Tag", "low")

    var v in
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if want := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC); !v.At.Equal(want) {
        t.Fatalf("At = %v, want %v", v.At, want)
    }
    if v.IP != netip.MustParseAddr("127.0.0.1") {
        t.Fatalf("IP = %v, want 127.0.0.1", v.IP)
    }
    if v.Tag != "LOW" {
        t.Fatalf("Tag = %q, want LOW", v.Tag)
    }
}

func TestBindTextUnmarshalerError(t *testing.T) {
    type in struct {
        At time.Time `form:"at"`
    }

    var v in
    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?at=nope", nil), &v)
    if err == nil || !strings.Contains(err.Error(), "bind: form.At:") {
        t.Fatalf("err = %v, want wrapped as bind: form.At", err)
    }
}

func TestBindTextUnmarshalerSliceAndPointer(t *testing.T) {
    type in struct {
        Tags    []upperText `form:"tag"`
        First   *upperText  `form:"first"`
        Missing *upperText  `form:"none"`
    }

    r := httptest.NewRequest(http.MethodGet, "/?tag=a&tag=b&first=hi", nil)
    var v in
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if len(v.Tags) != 2 || v.Tags[0] != "A" || v.Tags[1] != "B" {
        t.Fatalf("Tags = %v, want [A B]", v.Tags)
    }
    if v.First == nil || *v.First != "HI" {
        t.Fatalf("First = %v, want HI", v.First)
    }
    if v.Missing != nil {
        t.Fatalf("Missing = %v, want nil", v.Missing)
    }
}

func TestBindTextUnmarshalerNamedSlice(t *testing.T) {
    type in struct {
        IP net.IP `form:"ip"`
    }

    var v in
    if err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?ip=::1", nil), &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if !v.IP.Equal(net.IPv6loopback) {
        t.Fatalf("IP = %v, want ::1", v.IP)
    }
}

// macAddr 是测试用的命名数组 TextUnmarshaler：走数组整值解析路径。
type macAddr [6]byte

func (m *macAddr) UnmarshalText(b []byte) error {
    hw, err := net.ParseMAC(string(b))
    if err != nil {
        return err
    }
    copy(m[:], hw)
    return nil
}

func TestBindTextUnmarshalerArray(t *testing.T) {
    type in struct {
        At  [2]time.Time `form:"at"`
        MAC macAddr      `form:"mac"`
    }

    r := httptest.NewRequest(http.MethodGet,
        "/?at=2026-11-18T03:00:00Z&at=2026-11-19T03:00:00Z&mac=00:11:22:33:44:55", nil)

    var v in
    if err := bind.Decode(r, &v); err != nil {
        t.Fatalf("Bind: %v", err)
    }
    if v.At[0].IsZero() || v.At[1].IsZero() {
        t.Fatalf("At = %v, want both elements parsed", v.At)
    }
    if v.MAC != (macAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}) {
        t.Fatalf("MAC = %v, want 00:11:22:33:44:55", v.MAC)
    }
}

// 空串没有特殊语法：能否绑定由目标类型自己的解析器决定。
func TestBindEmptyValueFollowsTypeParser(t *testing.T) {
    type in struct {
        Page int       `form:"page"`
        Ptr  *int      `form:"p"`
        Nums []int     `form:"n"`
        Tags []string  `form:"tags"`
        At   time.Time `form:"at"`
        IP   net.IP    `form:"ip"`
        MAC  macAddr   `form:"mac"`
    }

    decode := func(q string) (in, error) {
        var v in
        err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/e"+q, nil), &v)
        return v, err
    }

    for _, q := range []string{"?page=", "?page=&page=7", "?p=", "?n=", "?at=", "?mac="} {
        if _, err := decode(q); err == nil {
            t.Fatalf("%s: want 400, got nil", q)
        }
    }

    // 解析失败不分配指针。
    if v, err := decode("?p="); err == nil || v.Ptr != nil {
        t.Fatalf("pointer empty = %v/%v, want error/nil", err, v.Ptr)
    }

    // 空串是合法 string，切片保留空元素。
    if v, err := decode("?tags="); err != nil || len(v.Tags) != 1 || v.Tags[0] != "" {
        t.Fatalf("string slice empty = %v/%q, want nil/[\"\"]", err, v.Tags)
    }

    // net.IP 的 UnmarshalText 把空串解析成零值，这是类型自己的决定。
    if v, err := decode("?ip=&ip=::1"); err != nil || v.IP != nil {
        t.Fatalf("net.IP empty = %v/%v, want nil/nil", err, v.IP)
    }
}

// 锁定：Parse 单值得长度 1 的切片、多值按序、缺失为零值、标量取第一个。
func TestParse(t *testing.T) {
    if got, err := bind.Parse[[]int]([]string{"1"}); err != nil || !slices.Equal(got, []int{1}) {
        t.Fatalf("单值 = %v/%v, want [1]/nil", got, err)
    }
    if got, err := bind.Parse[[]int]([]string{"1", "2"}); err != nil || !slices.Equal(got, []int{1, 2}) {
        t.Fatalf("多值 = %v/%v, want [1 2]/nil", got, err)
    }
    if got, err := bind.Parse[int]([]string{"7", "8"}); err != nil || got != 7 {
        t.Fatalf("标量 = %d/%v, want 7/nil", got, err)
    }
    if got, err := bind.Parse[[]int](nil); err != nil || got != nil {
        t.Fatalf("缺失 = %v/%v, want nil/nil", got, err)
    }
    if got, err := bind.Parse[*string]([]string{""}); err != nil || got == nil || *got != "" {
        t.Fatalf("空串指针 = %v/%v, want 指向空串", got, err)
    }
    if got, err := bind.Parse[**int]([]string{"7"}); err != nil || got == nil || **got != 7 {
        t.Fatalf("多层指针 = %v/%v, want 7/nil", got, err)
    }
    // 数组是逐元素写入的，失败后不能把前几个元素交出去。
    if got, err := bind.Parse[[2]int]([]string{"1", "abc"}); err == nil || got != [2]int{} {
        t.Fatalf("数组半截 = %v/%v, want [0 0]/err", got, err)
    }
}
