package mie

import (
    "bytes"
    "encoding/base64"
    "errors"
    "net/http"
    "net/http/httptest"
    "slices"
    "strings"
    "testing"
)

func TestSendJSON(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/user", He(func(c *Ctx, _ struct{}) error {
        return c.Send(map[string]string{"name": "alice"})
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Body.String(); got != `{"name":"alice"}` {
        t.Fatalf("body = %q", got)
    }
}

func TestSendCommitsPresetStatus(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/created", He(func(c *Ctx, _ struct{}) error {
        c.Status(http.StatusCreated)
        return c.Send(map[string]int{"id": 1})
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/created", nil))

    if rec.Code != http.StatusCreated {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
    }
}

func TestSendJSONMarshalError(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/bad", He(func(c *Ctx, _ struct{}) error {
        return c.Send(make(chan int))
    }))

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
    }
    if got := rec.Header().Get(HeaderXContentTypeOptions); got != MIMEContentTypeNosniff {
        t.Fatalf("X-Content-Type-Options = %q, want %q", got, MIMEContentTypeNosniff)
    }
}

type customFormatted struct {
    media string
    value any
}

func (r customFormatted) Format() (string, any) { return r.media, r.value }

type xmlUser struct{ Name string }

// 自定义渲染器只声明格式：框架用该格式的编码器编码，Accept 被忽略，也不设 Vary。
func TestSendFormattedDeclaresFormat(t *testing.T) {
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return customFormatted{media: MIMEAppXML, value: xmlUser{Name: "bob"}}, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/user", nil)
    req.Header.Set("Accept", "text/html")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppXML {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppXML)
    }
    if got := rec.Header().Get(HeaderVary); got != "" {
        t.Fatalf("Vary = %q, want empty", got)
    }
    if got := rec.Body.String(); got != "<xmlUser><Name>bob</Name></xmlUser>" {
        t.Fatalf("body = %q", got)
    }
}

// 声明的格式没有编码器、值也不是字节：这是代码写错了，报错而不是悄悄换个格式。
func TestSendFormattedWithoutEncoderFails(t *testing.T) {
    r := New()
    r.Get("/raw", func(c *Ctx, _ struct{}) (Formatted, error) {
        return customFormatted{media: "application/x-unknown", value: struct{ A int }{1}}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); !strings.Contains(got, "no encoder for application/x-unknown") {
        t.Fatalf("body = %q", got)
    }
}

// 渲染器声明了没有编码器的格式时，string/[]byte 也不回落：回落只属于裸值。
func TestSendFormattedBytesWithoutEncoderFails(t *testing.T) {
    for name, value := range map[string]any{
        "string": "a,b",
        "bytes":  []byte("a,b"),
    } {
        t.Run(name, func(t *testing.T) {
            r := New()
            r.Get("/raw", func(c *Ctx, _ struct{}) (Formatted, error) {
                return customFormatted{media: "text/csv", value: value}, nil
            })

            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw", nil))

            if rec.Code != http.StatusInternalServerError {
                t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
            }
            if got := rec.Body.String(); !strings.Contains(got, "no encoder for text/csv") {
                t.Fatalf("body = %q", got)
            }
        })
    }
}

// failOnceWriter 第一次写失败，之后正常写：确认写错误交给 writeError，而不是被 Send 吞掉。
type failOnceWriter struct {
    h      http.Header
    body   string
    failed bool
}

func (w *failOnceWriter) Header() http.Header { return w.h }

func (w *failOnceWriter) WriteHeader(int) {}

func (w *failOnceWriter) Write(p []byte) (int, error) {
    if w.failed {
        w.body += string(p)
        return len(p), nil
    }
    w.failed = true
    return 0, errors.New("write failed")
}

func TestSendWriteErrorIsNotSwallowed(t *testing.T) {
    r := New()
    r.Handle(http.MethodGet, "/broken", He(func(c *Ctx, _ struct{}) error {
        return c.Send(map[string]string{"name": "alice"})
    }))

    w := &failOnceWriter{h: make(http.Header)}
    r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/broken", nil))

    if got := w.body; got != "write failed" {
        t.Fatalf("body = %q, want %q（写错误应交给 writeError，而不是被吞掉）", got, "write failed")
    }
}

func TestSendStringWritesText(t *testing.T) {
    r := New()
    r.Get("/text", func(c *Ctx, _ struct{}) (string, error) { return "ok", nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/text", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMETextPlainUTF8 {
        t.Fatalf("Content-Type = %q, want %q", got, MIMETextPlainUTF8)
    }
    if got := rec.Body.String(); got != "ok" {
        t.Fatalf("body = %q, want %q", got, "ok")
    }
}

func TestSendBytesWritesRaw(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    r := New()
    r.Get("/img", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/img", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != "image/png" {
        t.Fatalf("Content-Type = %q, want image/png", got)
    }
    if !bytes.Equal(rec.Body.Bytes(), png) {
        t.Fatalf("body = %q, want %q", rec.Body.Bytes(), png)
    }
}

func TestSendNegotiatesStructByAccept(t *testing.T) {
    type user struct{ Name string }
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (user, error) { return user{Name: "alice"}, nil })

    cases := []struct {
        name   string
        accept string
        want   int
        wantCT string
    }{
        {"missing", "", http.StatusOK, MIMEAppJSON},
        {"any", "*/*", http.StatusOK, MIMEAppJSON},
        {"json", "application/json", http.StatusOK, MIMEAppJSON},
        {"json uppercase", "Application/JSON", http.StatusOK, MIMEAppJSON},
        {"json with q", "application/json;q=0.8", http.StatusOK, MIMEAppJSON},
        {"json after miss with q", "text/html, application/json;q=0.9", http.StatusOK, MIMEAppJSON},
        {"xml", MIMEAppXML, http.StatusOK, MIMEAppXML},
        {"mixed", "text/html, */*", http.StatusOK, MIMEAppJSON},
        {"application wildcard", "application/*", http.StatusOK, MIMEAppJSON},
        {"text wildcard", "text/*", http.StatusNotAcceptable, MIMETextPlainUTF8},
        {"html only", "text/html", http.StatusNotAcceptable, MIMETextPlainUTF8},
        {"other type", "text/csv", http.StatusNotAcceptable, MIMETextPlainUTF8},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodGet, "/user", nil)
            if tc.accept != "" {
                req.Header.Set("Accept", tc.accept)
            }
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != tc.want {
                t.Fatalf("status = %d, want %d", rec.Code, tc.want)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if got := rec.Header().Get("Vary"); got != "Accept" {
                t.Fatalf("Vary = %q, want Accept", got)
            }
        })
    }
}

// 值只能产出自己的类型：accept 只能接受或拒绝，不能把它改标成别的类型。
func TestSendBytesFeedAccept(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    pdf := []byte("%PDF-1.7\nraw-bytes")
    r := New()
    r.Get("/png", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })
    r.Get("/pdf", func(c *Ctx, _ struct{}) ([]byte, error) { return pdf, nil })
    r.Get("/b", func(c *Ctx, _ struct{}) ([]byte, error) { return []byte("hello"), nil })
    r.Get("/s", func(c *Ctx, _ struct{}) (string, error) { return "ok", nil })

    cases := []struct {
        name     string
        path     string
        accept   string
        want     int
        wantCT   string
        wantBody string
    }{
        {"json bytes", "/b", MIMEAppJSON, http.StatusOK, MIMEAppJSON, `"aGVsbG8="`},
        {"json string", "/s", MIMEAppJSON, http.StatusOK, MIMEAppJSON, `"ok"`},
        {"png exact", "/png", "image/png", http.StatusOK, "image/png", string(png)},
        {"png wildcard", "/png", "image/*", http.StatusOK, "image/png", string(png)},
        {"png mismatched", "/png", "image/webp", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
        {"pdf exact", "/pdf", "application/pdf", http.StatusOK, "application/pdf", string(pdf)},
        {"pdf wildcard app", "/pdf", "application/*", http.StatusOK, "application/pdf", string(pdf)},
        {"pdf mismatched", "/pdf", "image/*", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
        {"text exact", "/b", "text/plain", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"text uppercase", "/b", "TEXT/PLAIN", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"text wildcard", "/b", "text/*", http.StatusOK, MIMETextPlainUTF8, "hello"},
        {"string named csv", "/s", "text/csv", http.StatusNotAcceptable, MIMETextPlainUTF8, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodGet, tc.path, nil)
            req.Header.Set("Accept", tc.accept)
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != tc.want {
                t.Fatalf("status = %d, want %d", rec.Code, tc.want)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
                t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
            }
        })
    }
}

func TestSendKeepsExistingVary(t *testing.T) {
    r := New()
    r.Get("/text", func(c *Ctx, _ struct{}) (string, error) {
        c.Header(HeaderVary, "Accept-Encoding")
        return "ok", nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/text", nil))

    if got := rec.Header().Values(HeaderVary); len(got) != 2 || got[0] != "Accept-Encoding" || got[1] != "Accept" {
        t.Fatalf("Vary = %q, want [Accept-Encoding Accept]", got)
    }
}

func xmlEncoder(any) ([]byte, error) { return []byte("<user>bob</user>"), nil }

func TestSendUsesConfiguredEncoder(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppXML: xmlEncoder}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (map[string]string, error) {
        return map[string]string{"name": "bob"}, nil
    })

    tests := []struct {
        name     string
        accept   string
        wantCT   string
        wantBody string
    }{
        {"exact", MIMEAppXML, MIMEAppXML, "<user>bob</user>"},
        {"uppercase", "Application/XML", MIMEAppXML, "<user>bob</user>"},
        {"wildcard keeps json", MIMEAny, MIMEAppJSON, `{"name":"bob"}`},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodGet, "/user", nil)
            req.Header.Set("Accept", tc.accept)
            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != http.StatusOK {
                t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if got := rec.Body.String(); got != tc.wantBody {
                t.Fatalf("body = %q, want %q", got, tc.wantBody)
            }
        })
    }
}

// 注册即新增格式：协商与编码都用这张表，框架不需要认识这个媒体类型。
func TestSendRegisteredCustomFormat(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppMsgPack: func(any) ([]byte, error) {
        return []byte("msgpack"), nil
    }}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (map[string]string, error) {
        return map[string]string{"name": "bob"}, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/user", nil)
    req.Header.Set(HeaderAccept, MIMEAppMsgPack)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppMsgPack {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppMsgPack)
    }
    if got := rec.Body.String(); got != "msgpack" {
        t.Fatalf("body = %q, want msgpack", got)
    }
}

// Formatted 声明的媒体类型同样走注册表编码，与 Accept 无关。
func TestSendFormattedUsesRegisteredCustomEncoder(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppMsgPack: func(any) ([]byte, error) {
        return []byte("msgpack"), nil
    }}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return customFormatted{media: MIMEAppMsgPack, value: map[string]string{"name": "bob"}}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppMsgPack {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppMsgPack)
    }
    if got := rec.Body.String(); got != "msgpack" {
        t.Fatalf("body = %q, want msgpack", got)
    }
}

// Order 决定通配符下谁先命中。
func TestEncodersOrderControlsPriority(t *testing.T) {
    first := func(any) ([]byte, error) { return []byte("first"), nil }
    second := func(any) ([]byte, error) { return []byte("second"), nil }
    r := New(Config{Formats: Formats{
        Encoders: Encoders{"application/first": first, "application/second": second},
        Order:    []string{"application/second", "application/first"},
    }})
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    r.Get("/png", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })

    req := httptest.NewRequest(http.MethodGet, "/png", nil)
    req.Header.Set(HeaderAccept, MIMEAppAny)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if got := rec.Header().Get(HeaderContentType); got != "application/second" {
        t.Fatalf("Content-Type = %q, want application/second", got)
    }
    if got := rec.Body.String(); got != "second" {
        t.Fatalf("body = %q, want second", got)
    }
}

// resolve 的优先级：Order 里列出的在前，其余按内置默认序（JSON、XML、媒体名字典序）。
func TestEncodersResolveOrder(t *testing.T) {
    msgpack := Encoder(func(any) ([]byte, error) { return nil, nil })

    tests := []struct {
        name string
        enc  Encoders
        ord  []string
        want []string
    }{
        {"default", nil, nil, []string{MIMEAppJSON, MIMEAppXML}},
        {"custom appended", Encoders{MIMEAppMsgPack: msgpack}, nil, []string{MIMEAppJSON, MIMEAppXML, MIMEAppMsgPack}},
        {"override keeps position", Encoders{MIMEAppJSON: xmlEncoder}, nil, []string{MIMEAppJSON, MIMEAppXML}},
        {"order pulls custom forward", Encoders{MIMEAppMsgPack: msgpack}, []string{MIMEAppMsgPack}, []string{MIMEAppMsgPack, MIMEAppJSON, MIMEAppXML}},
        {"order reorders builtins", nil, []string{MIMEAppXML, MIMEAppJSON}, []string{MIMEAppXML, MIMEAppJSON}},
        {"keys normalized", Encoders{"Application/MsgPack": msgpack}, nil, []string{MIMEAppJSON, MIMEAppXML, MIMEAppMsgPack}},
        {"customs sorted", Encoders{"text/c": msgpack, "text/a": msgpack, "text/b": msgpack}, nil, []string{MIMEAppJSON, MIMEAppXML, "text/a", "text/b", "text/c"}},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            f := Formats{Encoders: tc.enc, Order: tc.ord}
            f.resolve()
            if !slices.Equal(f.parsed, tc.want) {
                t.Fatalf("media = %v, want %v", f.parsed, tc.want)
            }
        })
    }
}

// Order 引用未注册或重复的媒体是配置错误，构造时就 panic。
func TestEncodersOrderRejectsBadInput(t *testing.T) {
    for _, tc := range []struct {
        name string
        cfg  Config
    }{
        {"unknown media", Config{Formats: Formats{Order: []string{"application/x-unknown"}}}},
        {"duplicate media", Config{Formats: Formats{Order: []string{MIMEAppJSON, "Application/JSON"}}}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            defer func() {
                if recover() == nil {
                    t.Fatalf("New(%+v) did not panic", tc.cfg)
                }
            }()
            New(tc.cfg)
        })
    }
}

// 同一个媒体写成两种形式（大小写不同）等于给同一格式两个实现，构造时就 panic。
func TestEncodersRejectsDuplicateMedia(t *testing.T) {
    defer func() {
        if recover() == nil {
            t.Fatal("New did not panic")
        }
    }()
    New(Config{Formats: Formats{Encoders: Encoders{
        "Application/JSON": jsonEncode,
        MIMEAppJSON:        xmlEncoder,
    }}})
}

// 编码器为 nil 或媒体名为空都是配置错误，构造时就 panic，避免「配了却永远 406」。
func TestEncodersRejectsBadEntries(t *testing.T) {
    for name, enc := range map[string]Encoders{
        "nil encoder": {MIMEAppMsgPack: nil},
        "empty media": {"  ": jsonEncode},
    } {
        t.Run(name, func(t *testing.T) {
            defer func() {
                if recover() == nil {
                    t.Fatalf("New(%v) did not panic", enc)
                }
            }()
            New(Config{Formats: Formats{Encoders: enc}})
        })
    }
}

func TestSendEncoderOverridesBuiltinJSON(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppJSON: func(any) ([]byte, error) {
        return []byte(`{"custom":true}`), nil
    }}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (map[string]string, error) {
        return map[string]string{"name": "bob"}, nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Body.String(); got != `{"custom":true}` {
        t.Fatalf("body = %q", got)
    }
}

func TestSendUnregisteredFormatIs406(t *testing.T) {
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (map[string]string, error) {
        return map[string]string{"name": "bob"}, nil
    })

    req := httptest.NewRequest(http.MethodGet, "/user", nil)
    req.Header.Set("Accept", "image/tiff")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusNotAcceptable {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotAcceptable)
    }
}

func TestJSONForcesJSON(t *testing.T) {
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return JSON(map[string]string{"name": "bob"}), nil
    })

    req := httptest.NewRequest(http.MethodGet, "/user", nil)
    req.Header.Set("Accept", "text/html")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Header().Get(HeaderVary); got != "" {
        t.Fatalf("Vary = %q, want empty", got)
    }
    if got := rec.Body.String(); got != `{"name":"bob"}` {
        t.Fatalf("body = %q", got)
    }
}

func TestJSONUsesConfiguredEncoder(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppJSON: func(any) ([]byte, error) {
        return []byte(`custom`), nil
    }}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return JSON(struct{}{}), nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Body.String(); got != "custom" {
        t.Fatalf("body = %q, want custom", got)
    }
}

func TestXMLForcesXML(t *testing.T) {
    r := New()
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return XML(xmlUser{Name: "bob"}), nil
    })

    req := httptest.NewRequest(http.MethodGet, "/user", nil)
    req.Header.Set("Accept", MIMEAppJSON)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if got := rec.Header().Get(HeaderContentType); got != MIMEAppXML {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppXML)
    }
    if got := rec.Body.String(); got != "<xmlUser><Name>bob</Name></xmlUser>" {
        t.Fatalf("body = %q", got)
    }
}

func TestXMLUsesConfiguredEncoder(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppXML: func(any) ([]byte, error) {
        return []byte("<custom/>"), nil
    }}}})
    r.Get("/user", func(c *Ctx, _ struct{}) (Formatted, error) {
        return XML(xmlUser{Name: "bob"}), nil
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user", nil))

    if got := rec.Header().Get(HeaderContentType); got != MIMEAppXML {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppXML)
    }
    if got := rec.Body.String(); got != "<custom/>" {
        t.Fatalf("body = %q, want <custom/>", got)
    }
}

// 字节的 natural 不在 application 族时，application/* 覆盖不了它，落到 JSON 变 base64。
func TestSendBytesOutsideFamilyGetJSON(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")
    r := New()
    r.Get("/png", func(c *Ctx, _ struct{}) ([]byte, error) { return png, nil })

    req := httptest.NewRequest(http.MethodGet, "/png", nil)
    req.Header.Set("Accept", MIMEAppAny)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
    }
    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    want := `"` + base64.StdEncoding.EncodeToString(png) + `"`
    if got := rec.Body.String(); got != want {
        t.Fatalf("body = %q, want %q", got, want)
    }
}

// 处理器事先设了 Content-Type 时，它就是声明的格式：不再按 Accept 协商，也不加 Vary。
func TestSendRespectsPresetContentType(t *testing.T) {
    png := []byte("\x89PNG\r\n\x1a\nraw-bytes")

    tests := []struct {
        name   string
        accept string
        send   func(c *Ctx) error
        wantCT string
        body   string
    }{
        {
            name:   "plain string",
            send:   func(c *Ctx) error { c.Type(MIMETextPlainUTF8); return c.Send("ok") },
            wantCT: MIMETextPlainUTF8,
            body:   "ok",
        },
        {
            name:   "json keeps encoder",
            send:   func(c *Ctx) error { c.Type(MIMEAppJSON); return c.Send("ok") },
            wantCT: MIMEAppJSON,
            body:   `"ok"`,
        },
        {
            name:   "raw bytes",
            send:   func(c *Ctx) error { c.Type("image/png"); return c.Send(png) },
            wantCT: "image/png",
            body:   string(png),
        },
        {
            name:   "json with parameters",
            send:   func(c *Ctx) error { c.Type(MIMEAppJSON + "; charset=utf-8"); return c.Send(struct{ Name string }{"bob"}) },
            wantCT: MIMEAppJSON + "; charset=utf-8",
            body:   `{"Name":"bob"}`,
        },
        {
            name:   "json uppercase",
            send:   func(c *Ctx) error { c.Type("Application/JSON"); return c.Send(struct{ Name string }{"bob"}) },
            wantCT: "Application/JSON",
            body:   `{"Name":"bob"}`,
        },
        {
            name:   "preset wins over accept",
            accept: MIMEAppJSON,
            send:   func(c *Ctx) error { c.Type(MIMETextHTMLUTF8); return c.Send("<b>x</b>") },
            wantCT: MIMETextHTMLUTF8,
            body:   "<b>x</b>",
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            r := New()
            r.Get("/x", He(func(c *Ctx, _ struct{}) error {
                return tc.send(c)
            }))

            req := httptest.NewRequest(http.MethodGet, "/x", nil)
            if tc.accept != "" {
                req.Header.Set(HeaderAccept, tc.accept)
            }

            rec := httptest.NewRecorder()
            r.ServeHTTP(rec, req)

            if rec.Code != http.StatusOK {
                t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
            }
            if got := rec.Header().Get(HeaderContentType); got != tc.wantCT {
                t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
            }
            if got := rec.Header().Get(HeaderVary); got != "" {
                t.Fatalf("Vary = %q, want empty", got)
            }
            if got := rec.Body.String(); got != tc.body {
                t.Fatalf("body = %q, want %q", got, tc.body)
            }
        })
    }
}

// 预设的格式没有编码器、值也不能原样写：报错，不换成别的格式。
func TestSendPresetContentTypeWithoutEncoderFails(t *testing.T) {
    r := New()
    r.Get("/html", func(c *Ctx, _ struct{}) (struct{}, error) {
        c.Type(MIMETextHTMLUTF8)
        return struct{}{}, c.Send(struct{ A int }{1})
    })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html", nil))

    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
    }
    if got := rec.Body.String(); !strings.Contains(got, "no encoder for "+MIMETextHTMLUTF8) {
        t.Fatalf("body = %q, want contains %q", got, "no encoder for "+MIMETextHTMLUTF8)
    }
}

// 只配一个格式不会清掉另一个：内置格式始终存在。
func TestNewKeepsDefaultJSONWhenOnlyXMLConfigured(t *testing.T) {
    r := New(Config{Formats: Formats{Encoders: Encoders{MIMEAppXML: func(any) ([]byte, error) {
        return []byte("<x/>"), nil
    }}}})
    r.Get("/u", func(c *Ctx, _ struct{}) (xmlUser, error) { return xmlUser{Name: "bob"}, nil })

    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u", nil))

    if got := rec.Header().Get(HeaderContentType); got != MIMEAppJSON {
        t.Fatalf("Content-Type = %q, want %q", got, MIMEAppJSON)
    }
    if got := rec.Body.String(); got != `{"Name":"bob"}` {
        t.Fatalf("body = %q", got)
    }
}
