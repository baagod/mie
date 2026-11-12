package mie

import (
    "errors"
    "net/http"
    "net/http/httptest"
    "slices"
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

type bodyIn struct {
    Name  string `json:"name"`
    Extra string // 无 tag：按字段名精确匹配 "Extra"，小写 "extra" 不写
}

func TestBindBodyOnlyExplicitJSONFields(t *testing.T) {
    var got bodyIn
    r := New()
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    // 只有 CT 明确是 JSON 才解 body；键名精确匹配：Extra 写字段，extra/unknown 忽略。
    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"alice","Extra":"hack","extra":"low","unknown":1}`))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if got.Name != "alice" || got.Extra != "hack" {
        t.Fatalf("bound = %+v, want Name=alice、Extra=hack", got)
    }

    // CT 不是 JSON：不读 body，字段保持零值。
    got = bodyIn{}
    req = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"alice"}`))
    req.Header.Set(HeaderContentType, "text/plain")
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || got != (bodyIn{}) {
        t.Fatalf("text/plain: status=%d in=%+v, want 200 且零值", rec.Code, got)
    }
}

func TestBindBodyKeyIsCaseSensitive(t *testing.T) {
    var got bodyIn
    r := New()
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"Name":"alice"}`))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "" {
        t.Fatalf("Name = %q, want 空（键名精确匹配）", got.Name)
    }
}

func TestBindJSONContentTypeWithParams(t *testing.T) {
    for _, ct := range []string{
        MIMEAppJSON + "; charset=utf-8",
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

func TestBindBodyOverridesLocationTag(t *testing.T) {
    type in struct {
        ID int `path:"id" json:"id"`
    }

    t.Run("body last", func(t *testing.T) {
        var got in
        r := New()
        r.Post("/items/{id}", func(c *Ctx, v in) (struct{}, error) {
            got = v
            return struct{}{}, nil
        })

        req := httptest.NewRequest(http.MethodPost, "/items/7", strings.NewReader(`{"id":999}`))
        req.Header.Set(HeaderContentType, MIMEAppJSON)
        r.ServeHTTP(httptest.NewRecorder(), req)

        if got.ID != 999 {
            t.Fatalf("ID = %d, want 999（body 最后绑定）", got.ID)
        }
    })

    t.Run("missing body key falls back to path", func(t *testing.T) {
        var got in
        r := New()
        r.Post("/items/{id}", func(c *Ctx, v in) (struct{}, error) {
            got = v
            return struct{}{}, nil
        })

        req := httptest.NewRequest(http.MethodPost, "/items/7", strings.NewReader(`{}`))
        req.Header.Set(HeaderContentType, MIMEAppJSON)
        r.ServeHTTP(httptest.NewRecorder(), req)

        if got.ID != 7 {
            t.Fatalf("ID = %d, want 7（body 缺键回退 path）", got.ID)
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got == nil {
        t.Fatal("空 body 时 *IN 仍应分配")
    }
    if got.Name != "" {
        t.Fatalf("Name = %q, want 空", got.Name)
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "6" {
        t.Fatalf("status=%d body=%q, want 200 6", rec.Code, rec.Body.String())
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
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

    if rec.Code != http.StatusOK || rec.Body.String() != `""` {
        t.Fatalf("status=%d body=%q, want 200 空字符串（CT 不是 JSON 不解析）", rec.Code, rec.Body.String())
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || got.Name != "" {
        t.Fatalf("status=%d bound=%+v, want 200 与零值", rec.Code, got)
    }
}

func TestEmptyStructInputSkipsBody(t *testing.T) {
    served := false
    r := New()
    r.Post("/noin", func(c *Ctx, _ struct{}) (struct{}, error) {
        served = true
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/noin", strings.NewReader("not json"))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || !served || rec.Body.String() != "{}" {
        t.Fatalf("status=%d served=%v body=%q, want 200、handler 执行、body {}", rec.Code, served, rec.Body.String())
    }
}

func TestBindBodyNonObjectAndNull(t *testing.T) {
    var got bodyIn
    r := New()
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    // 数组不是对象：解进 map 失败，仍然是 400。
    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`[1,2]`))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind body") {
        t.Fatalf("array: status=%d body=%q, want 400 且含 bind body", rec.Code, rec.Body.String())
    }

    // null 等同空 body：handler 拿到零值 IN。
    got = bodyIn{}
    req = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`null`))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || got != (bodyIn{}) {
        t.Fatalf("null: status=%d in=%+v, want 200 且零值", rec.Code, got)
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != `"alice|a,b"` {
        t.Fatalf("status/body = %d/%q, want 200 with \"alice|a,b\"", rec.Code, rec.Body.String())
    }
}

func TestBindBodyTooLargeByMiddleware(t *testing.T) {
    r := New()
    r.Use(func(c *Ctx, _ struct{}) (any, error) {
        c.R.Body = http.MaxBytesReader(c.W, c.R.Body, 8)
        return nil, c.Next()
    })
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"0123456789"}`))
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusRequestEntityTooLarge {
        t.Fatalf("status = %d, want 413", rec.Code)
    }
}

type bodyErrorReader struct{}

func (bodyErrorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestBindBodyReadErrorIs500(t *testing.T) {
    r := New()
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/users", bodyErrorReader{})
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want 500", rec.Code)
    }
}

func TestBodyIsCached(t *testing.T) {
    r := New()
    r.Post("/echo", func(c *Ctx, _ struct{}) (string, error) {
        first, err := c.Body()
        if err != nil {
            return "", err
        }
        second, err := c.Body()
        if err != nil {
            return "", err
        }
        return string(first) + "|" + string(second), nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("hi")))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if got := rec.Body.String(); got != `"hi|hi"` {
        t.Fatalf("body = %s, want %q", got, `"hi|hi"`)
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
    req.Header.Set(HeaderContentType, MIMEAppFormUrlencoded)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "alice" || got.Page != 3 || got.Q != "url" {
        t.Fatalf("bound = %+v", got)
    }
}

func TestBindFormBodyWinsOverQuery(t *testing.T) {
    var got formIn
    r := New()
    r.Post("/form", func(c *Ctx, in formIn) (struct{}, error) {
        got = in
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/form?name=query", strings.NewReader("name=body"))
    req.Header.Set(HeaderContentType, MIMEAppFormUrlencoded)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "body" {
        t.Fatalf("Name = %q, want body", got.Name)
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
    req.Header.Set(HeaderContentType, MIMEAppFormUrlencoded+"; charset=utf-8")
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
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

func TestHeAdapter(t *testing.T) {
    type in struct {
        ID int `path:"id"`
    }

    r := New()
    r.Delete("/he/{id}", He(func(c *Ctx, v in) error {
        if v.ID == 0 {
            return NewError(http.StatusBadRequest, "bad id")
        }
        return nil
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/he/0", nil))
    if rec.Code != http.StatusBadRequest || rec.Body.String() != "bad id" {
        t.Fatalf("status=%d body=%q, want 400 bad id", rec.Code, rec.Body.String())
    }

    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/he/5", nil))
    if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
        t.Fatalf("status=%d body=%q, want 200 空 body", rec.Code, rec.Body.String())
    }
}

func TestOUTRules(t *testing.T) {
    r := New()
    r.Get("/zero", func(c *Ctx, _ struct{}) (int, error) { return 0, nil })
    r.Get("/nil", func(c *Ctx, _ struct{}) (*int, error) { return nil, nil })
    r.Get("/none", func(c *Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })
    r.Get("/bad", func(c *Ctx, _ struct{}) (chan int, error) { return make(chan int), nil })

    cases := []struct {
        path   string
        status int
        body   string
        ctype  string
    }{
        {"/zero", http.StatusOK, "0", MIMEAppJSON},
        {"/nil", http.StatusOK, "null", MIMEAppJSON},
        {"/none", http.StatusOK, "{}", MIMEAppJSON},
        {"/bad", http.StatusInternalServerError, "", MIMETextPlainUTF8},
    }

    for _, tc := range cases {
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
        if rec.Code != tc.status {
            t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.status)
        }
        if tc.path == "/bad" {
            if rec.Body.Len() == 0 {
                t.Fatalf("%s body 为空，want 编码错误文本", tc.path)
            }
        } else if got := rec.Body.String(); got != tc.body {
            t.Fatalf("%s body = %q, want %q", tc.path, got, tc.body)
        }
        if got := rec.Header().Get(HeaderContentType); got != tc.ctype {
            t.Fatalf("%s Content-Type = %q, want %q", tc.path, got, tc.ctype)
        }
    }
}

func TestMiddlewareRunsAroundHandler(t *testing.T) {
    var steps []string
    r := New()
    var mw HandlerFunc[struct{}, any] = He(func(c *Ctx, _ struct{}) error {
        steps = append(steps, "before")
        err := c.Next()
        steps = append(steps, "after")
        return err
    })
    r.Use(mw)
    r.Get("/m", func(c *Ctx, _ struct{}) (struct{}, error) {
        steps = append(steps, "handler")
        return struct{}{}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/m", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if want := []string{"before", "handler", "after"}; !slices.Equal(steps, want) {
        t.Fatalf("steps = %v, want %v", steps, want)
    }
    if got := rec.Body.String(); got != "{}" {
        t.Fatalf("body = %q, want %q（中间件不额外写 body）", got, "{}")
    }
}

func TestHeAsNoInputHandler(t *testing.T) {
    r := New()
    r.Get("/health", He(func(c *Ctx, _ struct{}) error {
        c.Status(http.StatusNoContent)
        return nil
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

    if rec.Code != http.StatusNoContent {
        t.Fatalf("status = %d, want 204", rec.Code)
    }
    if rec.Body.Len() != 0 {
        t.Fatalf("body = %q, want 空", rec.Body.String())
    }
}

func TestBindSourceOrder(t *testing.T) {
    t.Run("query overrides path", func(t *testing.T) {
        type in struct {
            ID int `path:"id" query:"id"`
        }
        r := New()
        r.Get("/p/{id}", func(c *Ctx, v in) (int, error) { return v.ID, nil })

        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/7?id=9", nil))

        if rec.Code != http.StatusOK || rec.Body.String() != "9" {
            t.Fatalf("status/body = %d/%q, want 200/\"9\"", rec.Code, rec.Body.String())
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

    t.Run("query overrides header", func(t *testing.T) {
        type in struct {
            ID int `header:"X-Id" query:"id"`
        }
        r := New()
        r.Get("/h", func(c *Ctx, v in) (int, error) { return v.ID, nil })

        req := httptest.NewRequest(http.MethodGet, "/h?id=9", nil)
        req.Header.Set("X-Id", "7")
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != "9" {
            t.Fatalf("status/body = %d/%q, want 200/\"9\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("form overrides query", func(t *testing.T) {
        type in struct {
            Name string `query:"name" form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=query", strings.NewReader("name=form"))
        req.Header.Set("Content-Type", MIMEAppFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != `"form"` {
            t.Fatalf("status/body = %d/%q, want 200/\"form\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("form falls back to query", func(t *testing.T) {
        type in struct {
            Name string `query:"name" form:"name"`
        }
        r := New()
        r.Post("/q", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/q?name=query", strings.NewReader("x=1"))
        req.Header.Set("Content-Type", MIMEAppFormUrlencoded)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != `"query"` {
            t.Fatalf("status/body = %d/%q, want 200/\"query\"", rec.Code, rec.Body.String())
        }
    })

    t.Run("body overrides form", func(t *testing.T) {
        type in struct {
            Name string `form:"name" json:"name"`
        }
        r := New()
        r.Post("/f", func(c *Ctx, v in) (string, error) { return v.Name, nil })

        req := httptest.NewRequest(http.MethodPost, "/f?name=query", strings.NewReader(`{"name":"json"}`))
        req.Header.Set(HeaderContentType, MIMEAppJSON)
        rec := httptest.NewRecorder()
        r.ServeHTTP(rec, req)

        if rec.Code != http.StatusOK || rec.Body.String() != `"json"` {
            t.Fatalf("status/body = %d/%q, want 200/\"json\"", rec.Code, rec.Body.String())
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.ID != 7 || got.id != 0 || got.Empty != 0 || got.Name != "alice" {
        t.Fatalf("got = %+v, want ID=7 id=0 Empty=0 Name=alice", got)
    }
}

func TestPlanErrorIsReportedOnFirstRequest(t *testing.T) {
    type in struct {
        Items map[string]int `query:"items"`
    }

    r := New()
    r.Get("/lazy", func(c *Ctx, v in) (struct{}, error) { return struct{}{}, nil })

    // 注册不报错：方案在首次请求时才编译，错误以 500 返回。
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/lazy?items=1", nil))
    if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "unsupported binding type map[string]int") {
        t.Fatalf("first status/body = %d/%q, want 500 with unsupported binding type", rec.Code, rec.Body.String())
    }

    // 错误同样缓存：第二次请求不再重复编译。
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/lazy?items=1", nil))
    if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "unsupported binding type map[string]int") {
        t.Fatalf("second status/body = %d/%q, want cached 500", rec.Code, rec.Body.String())
    }
}

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
        Name string `path:"name" query:"name"`
    }

    r := New()
    r.Get("/x/{name}", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    // 显式空值：query 覆盖 path，字段被清零。
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/abc?name=", nil))
    if rec.Code != http.StatusOK || rec.Body.String() != `""` {
        t.Fatalf("explicit empty status/body = %d/%q, want 200/\"\"", rec.Code, rec.Body.String())
    }

    // 缺失：保留 path 写入的值。
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/abc", nil))
    if rec.Code != http.StatusOK || rec.Body.String() != `"abc"` {
        t.Fatalf("missing status/body = %d/%q, want 200/\"abc\"", rec.Code, rec.Body.String())
    }
}

func TestBindBodyByContentType(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    r := New()
    h := func(c *Ctx, v in) (string, error) { return v.Name, nil }
    r.Post("/p", h)
    r.Put("/p", h)
    r.Patch("/p", h)
    r.Get("/p", h)

    body := `{"name":"alice"}`
    cases := []struct {
        method string
        want   string
    }{
        {http.MethodPost, `"alice"`},
        {http.MethodPut, `"alice"`},
        {http.MethodPatch, `"alice"`},
        {http.MethodGet, `""`}, // GET 没有 body 可绑
    }
    for _, tc := range cases {
        rec := httptest.NewRecorder()
        req := httptest.NewRequest(tc.method, "/p", strings.NewReader(body))
        req.Header.Set(HeaderContentType, MIMEAppJSON)
        r.ServeHTTP(rec, req)
        if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
            t.Fatalf("%s status/body = %d/%q, want 200/%q", tc.method, rec.Code, rec.Body.String(), tc.want)
        }
    }
}

func TestBindParseFormError(t *testing.T) {
    type in struct {
        Page int `query:"page"`
    }

    r := New()
    r.Post("/f", func(c *Ctx, v in) (int, error) { return v.Page, nil })

    req := httptest.NewRequest(http.MethodPost, "/f", strings.NewReader("a=%zz"))
    req.Header.Set(HeaderContentType, MIMEAppFormUrlencoded)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind params:") {
        t.Fatalf("status/body = %d/%q, want 400 with bind params:", rec.Code, rec.Body.String())
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
        {"empty first clears", "/s?tag=&tag=b", `{"Tags":[],"IDs":[]}`},
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

    req := httptest.NewRequest(http.MethodGet, "/a?p=9", nil)
    req.Header.Add("X-P", "1")
    req.Header.Add("X-P", "2")
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
    req.Header.Set(HeaderContentType, MIMEAppJSON)
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
