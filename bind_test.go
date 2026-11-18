package mie

import (
    "bytes"
    "errors"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "reflect"
    "strings"
    "sync"
    "testing"
)

type bindSources struct {
    ID     int     `path:"id"`
    Page   int     `query:"page"`
    Trace  string  `header:"X-Trace-Id"`
    Active bool    `query:"active"`
    Ratio  float64 `query:"ratio"`
    Count  uint    `query:"count"`
    Limit  *int    `query:"limit"`
}

func TestBindPathQueryHeader(t *testing.T) {
    var got bindSources
    r := New()
    r.Get("/items/{id}", func(c *Ctx, in bindSources) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/items/42?page=3&active=true&ratio=0.5&count=7&limit=9", nil)
    req.Header.Set("X-Trace-Id", "t-1")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if got.ID != 42 || got.Page != 3 || got.Trace != "t-1" || !got.Active || got.Ratio != 0.5 || got.Count != 7 {
        t.Fatalf("bound = %+v", got)
    }
    if got.Limit == nil || *got.Limit != 9 {
        t.Fatalf("Limit = %v, want 9", got.Limit)
    }
}

func TestBindMissingSourceKeepsZero(t *testing.T) {
    var got bindSources
    r := New()
    r.Get("/items/{id}", func(c *Ctx, in bindSources) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/7", nil))

    if got.ID != 7 || got.Page != 0 || got.Trace != "" || got.Active || got.Limit != nil {
        t.Fatalf("bound = %+v", got)
    }
}

func TestBindJSONContentTypeWithParams(t *testing.T) {
    for _, ct := range []string{
        MIMEJSON + "; charset=utf-8",
        "Application/JSON",
    } {
        t.Run(ct, func(t *testing.T) {
            var got bodyIn
            r := New()
            r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
                got = in
                return struct{}{}, nil
            })

            req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"alice"}`))
            req.Header.Set(HeaderContentType, ct)
            r.ServeHTTP(httptest.NewRecorder(), req)

            if got.Name != "alice" {
                t.Fatalf("CT %q: Name = %q, want alice", ct, got.Name)
            }
        })
    }
}

func TestBindLocationOverridesBody(t *testing.T) {
    type in struct {
        ID int `path:"id" json:"id"`
    }

    t.Run("location last", func(t *testing.T) {
        var got in
        r := New()
        r.Post("/items/{id}", func(c *Ctx, v in) (struct{}, error) {
            got = v
            return struct{}{}, nil
        })

        req := httptest.NewRequest(http.MethodPost, "/items/7", strings.NewReader(`{"id":999}`))
        req.Header.Set(HeaderContentType, MIMEJSON)
        r.ServeHTTP(httptest.NewRecorder(), req)

        if got.ID != 7 {
            t.Fatalf("ID = %d, want 7（位置来源最后绑定，覆盖 body）", got.ID)
        }
    })

    t.Run("path fills key omitted by body", func(t *testing.T) {
        var got in
        r := New()
        r.Post("/items/{id}", func(c *Ctx, v in) (struct{}, error) {
            got = v
            return struct{}{}, nil
        })

        req := httptest.NewRequest(http.MethodPost, "/items/7", strings.NewReader(`{}`))
        req.Header.Set(HeaderContentType, MIMEJSON)
        r.ServeHTTP(httptest.NewRecorder(), req)

        if got.ID != 7 {
            t.Fatalf("ID = %d, want 7（body 缺键，path 值保留）", got.ID)
        }
    })
}

func TestBindPointerIN(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    var got *in
    r := New()
    r.Post("/ptr", func(c *Ctx, v *in) (struct{}, error) {
        got = v
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/ptr", strings.NewReader(`{"name":"bob"}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got == nil || got.Name != "bob" {
        t.Fatalf("bound = %+v, want 非 nil 且 Name=bob", got)
    }
}

func TestBindPointerINWithEmptyBodyIsAllocated(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    var got *in
    r := New()
    r.Post("/ptr", func(c *Ctx, v *in) (struct{}, error) {
        got = v
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/ptr", nil)
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got == nil {
        t.Fatal("空 body 时 *IN 仍应分配")
    }
    if got.Name != "" {
        t.Fatalf("Name = %q, want 空", got.Name)
    }
}

func TestBindBodyDecoderReceivesPointer(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    var got *in
    var target reflect.Type
    r := New(Config{Codecs: Codecs{JSON: Codec{Decode: func(b []byte, v any) error {
        target = reflect.TypeOf(v)
        return nil
    }}}})
    r.Post("/ptr", func(c *Ctx, v *in) (struct{}, error) {
        got = v
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/ptr", strings.NewReader(`null`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if want := reflect.TypeOf((*in)(nil)); target != want {
        t.Fatalf("decoder in = %v, want %v", target, want)
    }
    if got == nil {
        t.Fatal("null body 时 *IN 仍应分配")
    }
}

func TestBindPointerINWithNullBodyIsAllocated(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    var got *in
    r := New()
    r.Post("/ptr", func(c *Ctx, v *in) (struct{}, error) {
        got = v
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/ptr", strings.NewReader(`null`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if got == nil || got.Name != "" {
        t.Fatalf("got = %+v, want 非 nil 零值", got)
    }
}

func TestBindNonStructIN(t *testing.T) {
    r := New()
    r.Post("/sum", func(c *Ctx, in []int) (int, error) {
        n := 0
        for _, v := range in {
            n += v
        }
        return n, nil
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/sum", strings.NewReader(`[1,2,3]`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "6" {
        t.Fatalf("status=%d body=%q, want 200 6", rec.Code, rec.Body.String())
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEJSON)
    }
}

func TestBindNonStructINRequiresJSONContentType(t *testing.T) {
    r := New()
    r.Post("/echo", func(_ *Ctx, in string) (string, error) {
        return in, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`"hi"`))
    req.Header.Set(HeaderContentType, "text/plain")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusUnsupportedMediaType {
        t.Fatalf("status=%d, want 415（CT 不是 JSON 不解析）", rec.Code)
    }
}

func TestBindPointerNonStructIN(t *testing.T) {
    r := New()
    r.Post("/sum", func(_ *Ctx, in *[]int) (int, error) {
        n := 0
        for _, v := range *in {
            n += v
        }
        return n, nil
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/sum", strings.NewReader(`[1,2,3]`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "6" {
        t.Fatalf("status=%d body=%q, want 200 6", rec.Code, rec.Body.String())
    }
}

func TestBindEmptyBodyKeepsZero(t *testing.T) {
    var got bodyIn
    r := New()
    r.Post("/empty", func(c *Ctx, in bodyIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/empty", nil)
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || got.Name != "" {
        t.Fatalf("status=%d bound=%+v, want 200 与零值", rec.Code, got)
    }
}

func TestBindNestedStructBody(t *testing.T) {
    type profile struct {
        Name string `json:"name"`
    }
    type in struct {
        Profile profile  `json:"profile"`
        Tags    []string `json:"tags"`
    }

    r := New()
    r.Post("/nested", func(c *Ctx, v in) (string, error) {
        return v.Profile.Name + "|" + strings.Join(v.Tags, ","), nil
    })

    req := httptest.NewRequest(http.MethodPost, "/nested", strings.NewReader(`{"profile":{"name":"alice"},"tags":["a","b"]}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "alice|a,b" {
        t.Fatalf("status/body = %d/%q, want 200 with alice|a,b", rec.Code, rec.Body.String())
    }
}

type formIn struct {
    Name string `form:"name"`
    Page int    `form:"page"`
    Q    string `query:"q"`
}

func TestBindFormGETUsesQuery(t *testing.T) {
    var got formIn
    r := New()
    r.Get("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/form?name=alice&page=2&q=url", nil))

    if got.Name != "alice" || got.Page != 2 || got.Q != "url" {
        t.Fatalf("bound = %+v", got)
    }
}

func TestBindFormPOSTUsesBody(t *testing.T) {
    var got formIn
    r := New()
    r.Post("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/form?q=url", strings.NewReader("name=alice&page=3"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "alice" || got.Page != 3 || got.Q != "url" {
        t.Fatalf("bound = %+v", got)
    }
}

func TestBindFormQueryOverridesBody(t *testing.T) {
    var got formIn
    r := New()
    r.Post("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/form?name=query", strings.NewReader("name=body"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "query" {
        t.Fatalf("Name = %q, want query", got.Name)
    }
}

func TestBindFormQueryFallback(t *testing.T) {
    var got formIn
    r := New()
    r.Post("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/form?name=alice", strings.NewReader("page=3"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded+"; charset=utf-8")
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "alice" || got.Page != 3 {
        t.Fatalf("bound = %+v, want Name 回退 query、Page 来自 body", got)
    }
}

func TestBindFormContentTypeMismatchIsIgnored(t *testing.T) {
    var got formIn
    served := false
    r := New()
    r.Post("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        served = true
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(`{"name":"json"}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || !served {
        t.Fatalf("status=%d served=%v, want 200 且 handler 执行", rec.Code, served)
    }
    if got.Name != "" {
        t.Fatalf("Name = %q, want 零值（不读 body、不报错）", got.Name)
    }
}

func TestBindConversionErrorGoesThroughErrorHandler(t *testing.T) {
    var seen error
    r := New(Config{ErrorHandler: func(c *Ctx, err error) error {
        seen = err
        return err
    }})
    r.Get("/list", func(c *Ctx, in bindSources) (struct{}, error) {
        return struct{}{}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/list?page=abc", nil))

    if rec.Code != http.StatusBadRequest {
        t.Fatalf("status = %d, want 400", rec.Code)
    }
    if got := rec.Body.String(); !strings.Contains(got, "bind: query.Page") {
        t.Fatalf("body = %q, want 来源与字段名", got)
    }

    var e *Error
    if !errors.As(seen, &e) || e.Status != http.StatusBadRequest {
        t.Fatalf("ErrorHandler 收到 %v, want *Error 400", seen)
    }
}

func TestBindSourceOrder(t *testing.T) {
    t.Run("path overrides query", func(t *testing.T) {
        type in struct {
            ID int `path:"id" query:"id"`
        }
        r := New()
        r.Get("/p/{id}", func(c *Ctx, v in) (int, error) { return v.ID, nil })

        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/7?id=9", nil))

        if rec.Code != http.StatusOK || rec.Body.String() != "7" {
            t.Fatalf("status/body = %d/%q, want 200/\"7\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("missing query keeps path", func(t *testing.T) {
        type in struct {
            ID int `path:"id" query:"id"`
        }
        r := New()
        r.Get("/p/{id}", func(c *Ctx, v in) (int, error) { return v.ID, nil })

        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/7", nil))

        if rec.Code != http.StatusOK || rec.Body.String() != "7" {
            t.Fatalf("status/body = %d/%q, want 200/\"7\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("header overrides query", func(t *testing.T) {
        type in struct {
            ID int `header:"X-Id" query:"id"`
        }
        r := New()
        r.Get("/h", func(c *Ctx, v in) (int, error) { return v.ID, nil })

        req := httptest.NewRequest(http.MethodGet, "/h?id=9", nil)
        req.Header.Set("X-Id", "7")
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "7" {
            t.Fatalf("status/body = %d/%q, want 200/\"7\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("query overrides form", func(t *testing.T) {
        type in struct {
            Name string `query:"name" form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=query", strings.NewReader("name=form"))
        req.Header.Set("Content-Type", MIMEFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "query" {
            t.Fatalf("status/body = %d/%q, want 200/query", rec.Code, rec.Body.String())
        }
    })

    t.Run("query ignores form body", func(t *testing.T) {
        type in struct {
            Name string `query:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q", strings.NewReader("name=form"))
        req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "" {
            t.Fatalf("status/body = %d/%q, want 200/\"\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("form multivalue overridden by query", func(t *testing.T) {
        type in struct {
            Name []string `form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) ([]string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=q", strings.NewReader("name=a&name=b"))
        req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != `["q"]` {
            t.Fatalf("status/body = %d/%q, want 200/%q", rec.Code, rec.Body.String(), `["q"]`)
        }
    })

    t.Run("empty query clears form value", func(t *testing.T) {
        type in struct {
            Name string `form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=", strings.NewReader("name=form"))
        req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "" {
            t.Fatalf("status/body = %d/%q, want 200/\"\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("form falls back to query", func(t *testing.T) {
        type in struct {
            Name string `query:"name" form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=query", strings.NewReader("x=1"))
        req.Header.Set("Content-Type", MIMEFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "query" {
            t.Fatalf("status/body = %d/%q, want 200/query", rec.Code, rec.Body.String())
        }
    })

    t.Run("location overrides body", func(t *testing.T) {
        type in struct {
            Name string `form:"name" json:"name"`
        }
        r := New()
        r.Post("/f", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/f?name=query", strings.NewReader(`{"name":"json"}`))
        req.Header.Set(HeaderContentType, MIMEJSON)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "query" {
            t.Fatalf("status/body = %d/%q, want 200/query", rec.Code, rec.Body.String())
        }
    })
}

func TestBindSkipsUnexportedAndEmptyTags(t *testing.T) {
    type in struct {
        ID    int    `path:"id"`
        id    int    `path:"id"` // 未导出：带 tag 也跳过
        Empty int    `query:""`  // 空 tag：等同没写
        Name  string `json:"name"`
    }

    var got in
    r := New()
    r.Post("/items/{id}", func(c *Ctx, v in) (struct{}, error) {
        got = v
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/items/7?Empty=9", strings.NewReader(`{"name":"alice"}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.ID != 7 || got.id != 0 || got.Empty != 0 || got.Name != "alice" {
        t.Fatalf("got = %+v, want ID=7 id=0 Empty=0 Name=alice", got)
    }
}

func TestBindIndependentInputsUseSeparateCache(t *testing.T) {
    type nameIn struct {
        Name string `query:"name"`
    }
    type idIn struct {
        ID int `query:"id"`
    }

    r := New()
    r.Get("/multi", func(c *Ctx, _ nameIn) (int, error) {
        var first idIn
        if err := c.Bind(&first); err != nil {
            return 0, err
        }
        var again idIn
        if err := c.Bind(&again); err != nil {
            return 0, err
        }
        return first.ID*10 + again.ID, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/multi?id=3", nil))

    if rec.Code != http.StatusOK || rec.Body.String() != "33" {
        t.Fatalf("status/body = %d/%q, want 200 with 33", rec.Code, rec.Body.String())
    }
}

func TestBindTwiceReusesCachedInput(t *testing.T) {
    type in struct {
        ID int `header:"X-Id"`
    }

    r := New()
    r.Get("/twice", func(c *Ctx, v in) (int, error) {
        c.R.Header.Set("X-Id", "5")

        var again in
        if err := c.Bind(&again); err != nil {
            return 0, err
        }
        return again.ID, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/twice", nil)
    req.Header.Set("X-Id", "9")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != "9" {
        t.Fatalf("status/body = %d/%q, want 200 with cached 9", rec.Code, rec.Body.String())
    }
}

func TestConcurrentFirstRequest(t *testing.T) {
    type in struct {
        ID int `query:"id"`
    }

    r := New()
    r.Get("/c", func(c *Ctx, v in) (int, error) { return v.ID, nil })

    const n = 16
    var wg sync.WaitGroup
    wg.Add(n)
    for range n {
        go func() {
            defer wg.Done()
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/c?id=7", nil))
            if rec.Code != http.StatusOK || rec.Body.String() != "7" {
                t.Errorf("status/body = %d/%q, want 200/\"7\"", rec.Code, rec.Body.String())
            }
        }()
    }
    wg.Wait()
}

func TestBindExplicitEmptyClearsValue(t *testing.T) {
    type in struct {
        Name string `query:"name" header:"X-Name"`
    }

    r := New()
    r.Get("/x", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    // 显式空值：后写来源的清零语义不变，空 header 覆盖 query。
    req := httptest.NewRequest(http.MethodGet, "/x?name=abc", nil)
    req.Header.Set("X-Name", "")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != "" {
        t.Fatalf("explicit empty status/body = %d/%q, want 200 empty", rec.Code, rec.Body.String())
    }

    // 缺失：保留前面来源写入的值。
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x?name=abc", nil))
    if rec.Code != http.StatusOK || rec.Body.String() != "abc" {
        t.Fatalf("missing status/body = %d/%q, want 200/abc", rec.Code, rec.Body.String())
    }
}

func TestBindParseFormError(t *testing.T) {
    type in struct {
        Page int `query:"page"`
    }

    r := New()
    r.Post("/f", func(c *Ctx, v in) (int, error) { return v.Page, nil })

    req := httptest.NewRequest(http.MethodPost, "/f", strings.NewReader("a=%zz"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind params:") {
        t.Fatalf("status/body = %d/%q, want 400 with bind params:", rec.Code, rec.Body.String())
    }
}

func TestBindQueryParseError(t *testing.T) {
    type in struct {
        Page int `query:"page"`
    }

    r := New()
    r.Get("/f", func(c *Ctx, v in) (int, error) { return v.Page, nil })

    req := httptest.NewRequest(http.MethodGet, "/f?page=%zz", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind params:") {
        t.Fatalf("status/body = %d/%q, want 400 with bind params:", rec.Code, rec.Body.String())
    }
}

func TestBindCookieOnlyPOSTLeavesBody(t *testing.T) {
    type in struct {
        SID string `cookie:"sid"`
    }

    r := New()
    var got, gotSID string
    r.Post("/c", func(c *Ctx, v in) (struct{}, error) {
        b, err := c.Body()
        got, gotSID = string(b), v.SID
        return struct{}{}, err
    })

    req := httptest.NewRequest(http.MethodPost, "/c", strings.NewReader("raw=body"))
    req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
    req.AddCookie(&http.Cookie{Name: "sid", Value: "abc"})
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || got != "raw=body" || gotSID != "abc" {
        t.Fatalf("status/body/cookie = %d/%q/%q, want 200/raw=body/abc", rec.Code, got, gotSID)
    }
}

func TestBindSliceValues(t *testing.T) {
    type in struct {
        Tags []string `query:"tag"`
        IDs  []int    `query:"id"`
    }

    r := New()
    r.Get("/s", func(c *Ctx, v in) (in, error) { return v, nil })

    cases := []struct{ name, url, want string }{
        {"all", "/s?tag=a&tag=b&id=1&id=2", `{"Tags":["a","b"],"IDs":[1,2]}`},
        {"missing", "/s", `{"Tags":[],"IDs":[]}`},
        {"empty first is zero element", "/s?tag=&tag=b", `{"Tags":["","b"],"IDs":[]}`},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
            if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
                t.Fatalf("status/body = %d/%q, want 200/%q", rec.Code, rec.Body.String(), tc.want)
            }
        })
    }
}

func TestBindSliceHeaderValues(t *testing.T) {
    type in struct {
        Tags []string `header:"X-Tag"`
    }

    r := New()
    r.Get("/h", func(c *Ctx, v in) ([]string, error) { return v.Tags, nil })

    req := httptest.NewRequest(http.MethodGet, "/h", nil)
    req.Header.Add("X-Tag", "a")
    req.Header.Add("X-Tag", "b")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != `["a","b"]` {
        t.Fatalf("status/body = %d/%q, want 200/[\"a\",\"b\"]", rec.Code, rec.Body.String())
    }
}

func TestBindArrayValues(t *testing.T) {
    type in struct {
        Pair [2]int `query:"p"`
    }

    r := New()
    r.Get("/a", func(c *Ctx, v in) ([2]int, error) { return v.Pair, nil })

    cases := []struct{ name, url, want string }{
        {"exact", "/a?p=1&p=2", `[1,2]`},
        {"more than length", "/a?p=1&p=2&p=3", `[1,2]`},
        {"less than length", "/a?p=1", `[1,0]`},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
            if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
                t.Fatalf("status/body = %d/%q, want 200/%q", rec.Code, rec.Body.String(), tc.want)
            }
        })
    }
}

// 前一个来源写满数组后，后一个来源值更少：尾部必须清掉旧值，而不是保留成合并结果。

func TestBindArrayMultiSourceClearsTail(t *testing.T) {
    type in struct {
        Pair [2]int `header:"X-P" query:"p"`
    }

    r := New()
    r.Get("/a", func(c *Ctx, v in) ([2]int, error) { return v.Pair, nil })

    req := httptest.NewRequest(http.MethodGet, "/a?p=1&p=2", nil)
    req.Header.Set("X-P", "9")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != "[9,0]" {
        t.Fatalf("status/body = %d/%q, want 200/[9,0]", rec.Code, rec.Body.String())
    }
}

func TestBindSliceBody(t *testing.T) {
    type in struct {
        Tags []string `json:"tags"`
    }

    r := New()
    r.Post("/b", func(c *Ctx, v in) ([]string, error) { return v.Tags, nil })

    req := httptest.NewRequest(http.MethodPost, "/b", strings.NewReader(`{"tags":["a","b"]}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != `["a","b"]` {
        t.Fatalf("status/body = %d/%q, want 200/[\"a\",\"b\"]", rec.Code, rec.Body.String())
    }
}

func TestBindSliceConversionError(t *testing.T) {
    type in struct {
        IDs []int `query:"id"`
    }

    r := New()
    r.Get("/e", func(c *Ctx, v in) (in, error) { return v, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/e?id=1&id=x", nil))
    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind: query.IDs") {
        t.Fatalf("status/body = %d/%q, want 400 with bind: query.IDs", rec.Code, rec.Body.String())
    }
}

// 锁定：不同 IN 类型各自缓存；同一类型重复绑定命中缓存。
func TestDuplicateBindingSharesValue(t *testing.T) {
    type in struct {
        A int `query:"page"`
        B int `query:"page"`
    }

    r := New()
    r.Get("/dup", func(c *Ctx, v in) (int, error) { return v.A*10 + v.B, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dup?page=1", nil))
    if rec.Code != http.StatusOK || rec.Body.String() != "11" {
        t.Fatalf("status/body = %d/%q, want 200 with 11", rec.Code, rec.Body.String())
    }
}

func TestBindMultipartFileAndValue(t *testing.T) {
    type in struct {
        Name   string                `form:"name"`
        Avatar *multipart.FileHeader `form:"avatar"`
    }

    var got in
    r := New()
    r.Post("/upload", func(c *Ctx, i in) (struct{}, error) {
        got = i
        return struct{}{}, nil
    })

    var b bytes.Buffer
    w := multipart.NewWriter(&b)
    if err := w.WriteField("name", "x"); err != nil {
        t.Fatal(err)
    }
    f, err := w.CreateFormFile("avatar", "a.txt")
    if err != nil {
        t.Fatal(err)
    }
    if _, err := f.Write([]byte("hello")); err != nil {
        t.Fatal(err)
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/upload", &b)
    req.Header.Set("Content-Type", w.FormDataContentType())
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
    }
    if got.Name != "x" {
        t.Fatalf("Name = %q, want x", got.Name)
    }
    if got.Avatar == nil || got.Avatar.Filename != "a.txt" {
        t.Fatalf("Avatar = %+v, want a.txt", got.Avatar)
    }
}

func TestBindMultipartMalformed400(t *testing.T) {
    type in struct {
        Name string `form:"name"`
    }

    r := New()
    r.Post("/upload", func(c *Ctx, i in) (struct{}, error) {
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("not multipart"))
    req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusBadRequest {
        t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
    }
}