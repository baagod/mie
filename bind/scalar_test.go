package bind_test

import (
    "net"
    "net/http"
    "net/http/httptest"
    "net/netip"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/baagod/mie/bind"
)

func TestBindRejectsUnsupportedFieldType(t *testing.T) {
    type in struct {
        M map[string]int `query:"m"`
    }

    var v in
    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?m=1", nil), &v)
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
    if err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?id=5", nil), &v); err != nil {
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

    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/", nil), nil)
    if err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
        t.Fatalf("Bind(nil) err = %v, want non-nil pointer error", err)
    }
}

func TestBindPointerInputAllocates(t *testing.T) {
    type in struct {
        ID int `query:"id"`
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
        Tags []string `query:"tag"`
        N    [2]int   `query:"n"`
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
        ID string `cookie:"id" query:"id"`
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
        At  time.Time  `query:"at"`
        IP  netip.Addr `query:"ip"`
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
        At time.Time `query:"at"`
    }

    var v in
    err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/?at=nope", nil), &v)
    if err == nil || !strings.Contains(err.Error(), "bind: query.At:") {
        t.Fatalf("err = %v, want wrapped as bind: query.At", err)
    }
}

func TestBindTextUnmarshalerSliceAndPointer(t *testing.T) {
    type in struct {
        Tags    []upperText `query:"tag"`
        First   *upperText  `query:"first"`
        Missing *upperText  `query:"none"`
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
        IP net.IP `query:"ip"`
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
        At  [2]time.Time `query:"at"`
        MAC macAddr      `query:"mac"`
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

func TestBindEmptyValueBindsZero(t *testing.T) {
    type in struct {
        Page int      `query:"page"`
        Ptr  *int     `query:"p"`
        Nums []int    `query:"n"`
        Tags []string `query:"tags"`
        IP   net.IP   `query:"ip"`
        MAC  macAddr  `query:"mac"`
    }

    decode := func(q string) (in, error) {
        var v in
        err := bind.Decode[in](httptest.NewRequest(http.MethodGet, "/e"+q, nil), &v)
        return v, err
    }

    // 标量：空串按零值绑定，不报错；后面的值不再生效（标量只取第一个）。
    if v, err := decode("?page=&page=7"); err != nil || v.Page != 0 {
        t.Fatalf("scalar empty = %v/%d, want nil/0", err, v.Page)
    }

    // 切片：空值就是一个零值元素，既不被丢弃，也不整体清零。
    if v, err := decode("?n=&n=5"); err != nil || len(v.Nums) != 2 || v.Nums[0] != 0 || v.Nums[1] != 5 {
        t.Fatalf("slice mixed = %v/%v, want nil/[0 5]", err, v.Nums)
    }
    if v, err := decode("?tags="); err != nil || len(v.Tags) != 1 || v.Tags[0] != "" {
        t.Fatalf("slice single empty = %v/%q, want nil/[\"\"]", err, v.Tags)
    }

    // 指针：空串得到指向零值的指针，而不是 nil。
    if v, err := decode("?p="); err != nil || v.Ptr == nil || *v.Ptr != 0 {
        t.Fatalf("pointer empty = %v/%v, want nil/&0", err, v.Ptr)
    }

    // 整体解析的类型（net.IP）：空串同样得到零值，而不是交给 UnmarshalText 报错。
    if v, err := decode("?ip="); err != nil || v.IP != nil {
        t.Fatalf("named slice empty = %v/%v, want nil/nil", err, v.IP)
    }

    // 空值算第一个 text：后面的值不会生效（文档承诺）。
    if v, err := decode("?ip=&ip=::1"); err != nil || v.IP != nil {
        t.Fatalf("whole empty first = %v/%v, want nil/nil", err, v.IP)
    }

    // 实现 TextUnmarshaler 但拒绝空串的类型（如 macAddr）同样得到零值，而不是 400。
    if v, err := decode("?mac="); err != nil || v.MAC != (macAddr{}) {
        t.Fatalf("whole empty = %v/%v, want nil/zero", err, v.MAC)
    }
}
