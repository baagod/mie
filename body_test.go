package mie

import (
    "errors"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

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
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
    if got.Name != "alice" || got.Extra != "hack" {
        t.Fatalf("bound = %+v, want Name=alice、Extra=hack", got)
    }

    // CT 没有解码器：返回 415，不读 body，字段保持零值。
    got = bodyIn{}
    req = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"alice"}`))
    req.Header.Set(HeaderContentType, "text/plain")
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusUnsupportedMediaType || got != (bodyIn{}) {
        t.Fatalf("text/plain: status=%d in=%+v, want 415 且零值", rec.Code, got)
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
    req.Header.Set(HeaderContentType, MIMEJSON)
    r.ServeHTTP(httptest.NewRecorder(), req)

    if got.Name != "" {
        t.Fatalf("Name = %q, want 空（键名精确匹配）", got.Name)
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
    req.Header.Set(HeaderContentType, MIMEJSON)
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
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bind body") {
        t.Fatalf("array: status=%d body=%q, want 400 且含 bind body", rec.Code, rec.Body.String())
    }

    // null 等同空 body：handler 拿到零值 IN。
    got = bodyIn{}
    req = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`null`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec = httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || got != (bodyIn{}) {
        t.Fatalf("null: status=%d in=%+v, want 200 且零值", rec.Code, got)
    }
}

func TestBindBodyTooLargeByMiddleware(t *testing.T) {
    r := New()
    r.Use(He(func(c *Ctx, _ struct{}) error {
        c.R.Body = http.MaxBytesReader(c.W, c.R.Body, 8)
        return c.Next()
    }))
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"0123456789"}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusRequestEntityTooLarge {
        t.Fatalf("status = %d, want 413", rec.Code)
    }
}

func TestMaxBodyBytesRejects(t *testing.T) {
    cases := []struct {
        name string
        ct   string
        body string
    }{
        {"json", MIMEJSON, `{"name":"0123456789"}`},
        {"form", MIMEFormUrlencoded, "name=" + strings.Repeat("a", 100)},
    }

    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            r := New(Config{MaxBodyBytes: 8})
            r.Post("/users", func(ctx *Ctx, in formIn) (struct{}, error) {
                return struct{}{}, nil
            })

            req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(c.body))
            req.Header.Set(HeaderContentType, c.ct)
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != http.StatusRequestEntityTooLarge {
                t.Fatalf("status = %d, want 413", rec.Code)
            }
        })
    }
}

func TestMaxBodyBytesDefaultIs1MiB(t *testing.T) {
    // 行为测试只能证明默认值落在一个窗口内，这里直接钉住配置值本身。
    if got, want := New().config.MaxBodyBytes, int64(1<<20); got != want {
        t.Fatalf("default MaxBodyBytes = %d, want %d", got, want)
    }

    r := New()
    r.Post("/users", func(c *Ctx, in bodyIn) (struct{}, error) {
        return struct{}{}, nil
    })

    body := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
    req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusRequestEntityTooLarge {
        t.Fatalf("status = %d, want 413", rec.Code)
    }
}

func TestMaxBodyBytesDisabled(t *testing.T) {
    r := New(Config{MaxBodyBytes: -1})
    r.Post("/echo", func(c *Ctx, in bodyIn) (struct{}, error) {
        return struct{}{}, nil
    })

    req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"name":"0123456789"}`))
    req.Header.Set(HeaderContentType, MIMEJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
}

func TestMaxBodyBytesSkipsMultipart(t *testing.T) {
    r := New(Config{MaxBodyBytes: 8})
    r.Post("/upload", func(c *Ctx, _ struct{}) (int, error) {
        b, err := c.Body()
        return len(b), err
    })

    body := "--x\r\nContent-Disposition: form-data; name=\"f\"\r\n\r\n" + strings.Repeat("a", 64) + "\r\n--x--\r\n"
    req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(body))
    req.Header.Set(HeaderContentType, "multipart/form-data; boundary=x")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
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
    req.Header.Set(HeaderContentType, MIMEJSON)
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
    if got := rec.Body.String(); got != "hi|hi" {
        t.Fatalf("body = %s, want %q", got, "hi|hi")
    }
}

// 锁定：Body 读取失败也缓存，第二次调用返回同一个错误，不再读已耗尽的流。

func TestBodyCachesReadError(t *testing.T) {
    c := newTestCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", bodyErrorReader{}))

    _, first := c.Body()
    if first == nil {
        t.Fatal("第一次 Body 应返回读取错误")
    }

    second, err := c.Body()
    if len(second) != 0 || err != first {
        t.Fatalf("第二次 Body = %q / %v, want 缓存错误 %v", second, err, first)
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
        ct     string
        want   string
    }{
        {http.MethodPost, MIMEJSON, "alice"},
        {http.MethodPut, MIMEJSON, "alice"},
        {http.MethodPatch, MIMEJSON, "alice"},
        {http.MethodGet, MIMEJSON, ""}, // GET 没有 body 可绑
        {http.MethodPost, "application/vnd.api+json", "alice"},
    }
    for _, tc := range cases {
        rec := httptest.NewRecorder()
        req := httptest.NewRequest(tc.method, "/p", strings.NewReader(body))
        req.Header.Set(HeaderContentType, tc.ct)
        r.ServeHTTP(rec, req)
        if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
            t.Fatalf("%s %s status/body = %d/%q, want 200/%q", tc.method, tc.ct, rec.Code, rec.Body.String(), tc.want)
        }
    }
}

// 有 body 但该 Content-Type 没有解码器时报 415，而不是静默丢弃。

func TestBindBodyUnregisteredContentTypeIsUnsupported(t *testing.T) {
    type in struct {
        Name string `json:"name"`
    }

    r := New()
    r.Post("/p", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    req := httptest.NewRequest(http.MethodPost, "/p", strings.NewReader("alice"))
    req.Header.Set(HeaderContentType, MIMETextPlainUTF8)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusUnsupportedMediaType {
        t.Fatalf("status = %d, want 415", rec.Code)
    }
}

// XML 也内置解码：application/xml 的 body 默认按 XML 解析。

func TestBindBodyDecodesXMLByDefault(t *testing.T) {
    type in struct {
        Name string
    }

    r := New()
    r.Post("/p", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    req := httptest.NewRequest(http.MethodPost, "/p", strings.NewReader("<in><Name>alice</Name></in>"))
    req.Header.Set(HeaderContentType, MIMEXML)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
        t.Fatalf("status/body = %d/%q, want 200/alice", rec.Code, rec.Body.String())
    }
}

// multipart 归 Bind，BindBody 直接跳过：不读 body，也不报 415。

func TestBindBodySkipsMultipart(t *testing.T) {
    type in struct {
        Name string
    }

    r := New()
    r.Post("/p", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    req := httptest.NewRequest(http.MethodPost, "/p", strings.NewReader("--x--"))
    req.Header.Set(HeaderContentType, MIMEMultipartForm+"; boundary=x")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "" {
        t.Fatalf("status/body = %d/%q, want 200/empty", rec.Code, rec.Body.String())
    }
}

// 有 body 但 Content-Type 缺失：没有解码器，415。

func TestBindBodyMissingContentTypeIsUnsupported(t *testing.T) {
    type in struct {
        Name string
    }

    r := New()
    r.Post("/p", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    req := httptest.NewRequest(http.MethodPost, "/p", strings.NewReader(`{"name":"alice"}`))
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusUnsupportedMediaType {
        t.Fatalf("status = %d, want 415", rec.Code)
    }
}

// chunked 请求的 ContentLength 是 -1，零字节 body 不能被当成非法 JSON。
func TestBindBodyEmptyChunkedIsIgnored(t *testing.T) {
    r := New()
    r.Post("/p", func(c *Ctx, v bodyIn) (struct{}, error) { return struct{}{}, nil })

    cases := []struct {
        name string
        body string
        want int
    }{
        {"empty", "", http.StatusOK},
        {"blank", "  ", http.StatusBadRequest},
    }

    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodPost, "/p", strings.NewReader(c.body))
            req.Header.Set(HeaderContentType, MIMEJSON)
            req.ContentLength = -1
            req.TransferEncoding = []string{"chunked"}
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != c.want {
                t.Fatalf("status = %d, want %d", rec.Code, c.want)
            }
        })
    }
}

func TestBindBodyStructuredSuffixUsesBaseDecoder(t *testing.T) {
    type in struct {
        Name string
    }

    decode := func(b []byte, v any) error {
        v.(*in).Name = strings.TrimSpace(string(b))
        return nil
    }
    r := New(Config{Codecs: Codecs{XML: Codec{Decode: decode}}})
    r.Post("/s", func(c *Ctx, v in) (string, error) { return v.Name, nil })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/s", strings.NewReader("alice"))
    req.Header.Set(HeaderContentType, "application/atom+xml")
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
        t.Fatalf("status/body = %d/%q, want 200/alice", rec.Code, rec.Body.String())
    }
}

func TestBindBodyUsesConfiguredDecoder(t *testing.T) {
    r := New(Config{Codecs: Codecs{MsgPack: Codec{
        Decode: func(b []byte, in any) error {
            p, ok := in.(*string)
            if !ok {
                return errors.New("decoder: want *string")
            }
            *p = "decoded:" + string(b)
            return nil
        },
    }}})
    r.Post("/decode", func(c *Ctx, in string) (string, error) { return in, nil })

    req := httptest.NewRequest(http.MethodPost, "/decode", strings.NewReader("raw"))
    req.Header.Set(HeaderContentType, MIMEMsgPack)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK || rec.Body.String() != "decoded:raw" {
        t.Fatalf("status/body = %d/%q, want 200 with decoded body", rec.Code, rec.Body.String())
    }
}
