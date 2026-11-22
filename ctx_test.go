package mie

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// 锁定：Header/Type 返回 Ctx，可以链式调用。
func TestHeaderAndTypeChain(t *testing.T) {
	r := New()
	r.Get("/chain", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.Type("application/json").Header("X-Test", "1")
		return struct{}{}, nil
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chain", nil))

	if got := rec.Header().Get(HeaderContentType); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := rec.Header().Get("X-Test"); got != "1" {
		t.Fatalf("X-Test = %q, want %q", got, "1")
	}
}

// 锁定：TraceID 自动生成 32 位十六进制，不往响应里补头。
func TestTraceIDGeneratedWithoutResponseHeader(t *testing.T) {
	r := New()
	var id string
	r.Get("/trace", func(c *Ctx, _ struct{}) (struct{}, error) {
		id = c.TraceID()
		return struct{}{}, nil
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trace", nil))

	if got := rec.Header().Get(HeaderXRequestID); got != "" {
		t.Fatalf("响应头 X-Request-Id = %q, want empty", got)
	}
	if len(id) != 32 {
		t.Fatalf("TraceID len = %d, want 32", len(id))
	}
}

// 锁定：客户端传的 X-Request-Id 不会被当成 TraceID，也不往响应里重复写。
func TestTraceIDIgnoresRequestHeader(t *testing.T) {
	r := New()
	var id string
	r.Get("/trace", func(c *Ctx, _ struct{}) (struct{}, error) {
		id = c.TraceID()
		return struct{}{}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(HeaderXRequestID, "trace-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get(HeaderXRequestID); got != "" {
		t.Fatalf("响应头 X-Request-Id = %q, want empty", got)
	}
	if id == "trace-1" || len(id) != 32 {
		t.Fatalf("TraceID = %q, want 32 位生成的 ID", id)
	}
}

// 锁定：中间件可以用 SetTraceID 接入自己的追踪格式。
func TestSetTraceID(t *testing.T) {
	r := New()
	var id string
	r.Get("/trace", func(c *Ctx, _ struct{}) (struct{}, error) {
		c.SetTraceID("my-format:42")
		id = c.TraceID()
		return struct{}{}, nil
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/trace", nil))

	if id != "my-format:42" {
		t.Fatalf("TraceID = %q, want %q", id, "my-format:42")
	}
}

// 锁定：Get 按类型读回 Set 写入的值；未设置或类型不符时返回零值，不 panic。
func TestCtxGet(t *testing.T) {
	type user struct{ ID int }
	var userKey struct{}

	newC := func() *Ctx {
		return newTestCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}

	t.Run("未设置返回零值", func(t *testing.T) {
		c := newC()
		if got := c.Get[*user](userKey); got != nil {
			t.Fatalf("Get = %v, want nil", got)
		}
		if got := c.Get[int](userKey); got != 0 {
			t.Fatalf("Get = %d, want 0", got)
		}
	})

	t.Run("命中返回写入的值", func(t *testing.T) {
		c := newC()
		u := &user{ID: 7}
		c.Set(userKey, u)
		if got := c.Get[*user](userKey); got != u {
			t.Fatalf("Get = %p, want %p", got, u)
		}
	})

	t.Run("类型不符返回零值", func(t *testing.T) {
		c := newC()
		c.Set(userKey, &user{ID: 7})
		if got := c.Get[string](userKey); got != "" {
			t.Fatalf("Get = %q, want empty", got)
		}
		if got := c.Get[user](userKey); got.ID != 0 {
			t.Fatalf("Get = %+v, want zero", got)
		}
	})
}

// 锁定：Get 容器优先、未命中回退请求 context；Value 只读请求 context。
func TestCtxValueFallsBackToRequestContext(t *testing.T) {
	type outerKey struct{}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), outerKey{}, "outer"))
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Get[string](outerKey{}); got != "outer" {
		t.Fatalf("Get = %q, want %q", got, "outer")
	}

	c.Set(outerKey{}, "inner")
	if got := c.Get[string](outerKey{}); got != "inner" {
		t.Fatalf("Get = %q, want %q", got, "inner")
	}
	if got := c.Value(outerKey{}); got != "outer" {
		t.Fatalf("Value = %v, want %q", got, "outer")
	}
}

// 锁定：Form 同名时 query 整体替换 form（含多值），只在 form 的键仍能取到。
func TestCtxFormQueryOverridesForm(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/form?page=2&tags=b&tags=d",
		strings.NewReader("page=1&tags=a&tags=c&only=form"))
	req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[int]("page"); got != 2 {
		t.Fatalf("query 优先: Form[int] = %d, want 2", got)
	}
	if got := c.Form[[]string]("tags"); !slices.Equal(got, []string{"b", "d"}) {
		t.Fatalf("query 多值整体替换: Form[[]string] = %v, want [b d]", got)
	}
	if got := c.Form[string]("only"); got != "form" {
		t.Fatalf("只在 form: Form[string] = %q, want %q", got, "form")
	}
	if got := c.Form[[]int]("missing"); got != nil {
		t.Fatalf("缺失: Form[[]int] = %v, want nil", got)
	}
}

// 锁定：query 里存在但为空同样覆盖 form，解析失败按零值。
func TestCtxFormEmptyQueryOverridesForm(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/form?empty=&num=",
		strings.NewReader("empty=form&num=7"))
	req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[string]("empty"); got != "" {
		t.Fatalf("空串覆盖: Form[string] = %q, want empty", got)
	}
	if got := c.Form[int]("num"); got != 0 {
		t.Fatalf("空串解析失败: Form[int] = %d, want 0", got)
	}
}

// 锁定：数组解析失败时 Form 返回零值，不交出半截结果。
func TestCtxFormArrayParseFailure(t *testing.T) {
	c := newTestCtx(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/form?pair=1&pair=abc", nil))

	if got := c.Form[[2]int]("pair"); got != [2]int{} {
		t.Fatalf("Form[[2]int] = %v, want [0 0]", got)
	}
}

// 锁定：Path 是实际路径，Pattern 是不带方法前缀的模板；HEAD 命中 GET 路由时同样不带前缀。
func TestCtxPathAndPattern(t *testing.T) {
	r := New()
	var path, pattern string
	r.Get("/user/{id}", func(c *Ctx, _ struct{}) (struct{}, error) {
		path, pattern = c.Path(), c.Pattern()
		return struct{}{}, nil
	})

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/user/42", nil))

		if path != "/user/42" {
			t.Fatalf("%s: Path = %q, want %q", method, path, "/user/42")
		}
		if pattern != "/user/{id}" {
			t.Fatalf("%s: Pattern = %q, want %q", method, pattern, "/user/{id}")
		}
	}
}

func TestCtxFormMultipartValue(t *testing.T) {
	c := newTestCtx(httptest.NewRecorder(), multipartValueRequest(t, "name", "mux"))
	if got := c.Form[string]("name"); got != "mux" {
		t.Fatalf("Form[string] = %q, want mux", got)
	}
}

// 锁定：Bind 与 Form 共用同一次解析，先 Bind 再 Form 仍能读到值。
func TestCtxFormAfterBindMultipart(t *testing.T) {
	c := newTestCtx(httptest.NewRecorder(), multipartValueRequest(t, "name", "mux"))

	var in struct {
		Name string `form:"name"`
	}
	if err := c.Bind(&in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "mux" {
		t.Fatalf("Bind Name = %q, want mux", in.Name)
	}
	if got := c.Form[string]("name"); got != "mux" {
		t.Fatalf("Form after Bind = %q, want mux", got)
	}
}

func TestCtxFormMultipartFile(t *testing.T) {
	c := newTestCtx(httptest.NewRecorder(), multipartFileRequest(t, "avatar", "a.txt", "hello"))

	fh := c.Form[*multipart.FileHeader]("avatar")
	if fh == nil || fh.Filename != "a.txt" {
		t.Fatalf("Form[*multipart.FileHeader] = %v, want a.txt", fh)
	}
	if got := c.Form[string]("avatar"); got != "" {
		t.Fatalf("value type on file part = %q, want empty", got)
	}
}

func TestCtxFormMultipartMultipleFiles(t *testing.T) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := w.CreateFormFile("docs", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", &b)
	req.Header.Set(HeaderContentType, w.FormDataContentType())
	c := newTestCtx(httptest.NewRecorder(), req)

	if files := c.Form[[]*multipart.FileHeader]("docs"); len(files) != 2 {
		t.Fatalf("Form[[]*multipart.FileHeader] = %d files, want 2", len(files))
	}
	if fh := c.Form[*multipart.FileHeader]("docs"); fh != nil {
		t.Fatalf("single file type on two files = %v, want nil", fh)
	}
}

func TestCtxFormMalformedMultipart(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
	req.Header.Set(HeaderContentType, "multipart/form-data; boundary=missing")
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[string]("name"); got != "" {
		t.Fatalf("malformed multipart = %q, want zero", got)
	}
	if fh := c.Form[*multipart.FileHeader]("name"); fh != nil {
		t.Fatalf("malformed multipart file = %v, want nil", fh)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("body must not be read") }

// 锁定：query 命中时连 body 都不碰。
func TestCtxFormQuerySkipsBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/?name=mux", panicReader{})
	req.Header.Set(HeaderContentType, MIMEMultipartForm)
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[string]("name"); got != "mux" {
		t.Fatalf("Form[string] = %q, want mux", got)
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

// 锁定：Form 吞掉坏 body 的错误后，随后的 Bind 仍要 400，不能返回 200 空值。
func TestCtxFormBadBodyThenBind(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=%zz"))
	req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[string]("a"); got != "" {
		t.Fatalf("Form = %q, want zero", got)
	}

	var in struct {
		A string `form:"a"`
	}
	err := c.Bind(&in)
	if err == nil || !strings.Contains(err.Error(), "invalid URL escape") {
		t.Fatalf("Bind err = %v, want parse error", err)
	}
}

// 锁定：先 Form 后 Bind 共用同一份解析结果。
func TestCtxFormThenBindSharesValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=form"))
	req.Header.Set(HeaderContentType, MIMEFormUrlencoded)
	c := newTestCtx(httptest.NewRecorder(), req)

	if got := c.Form[string]("name"); got != "form" {
		t.Fatalf("Form = %q, want form", got)
	}

	var in struct {
		Name string `form:"name"`
	}
	if err := c.Bind(&in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "form" {
		t.Fatalf("Bind Name = %q, want form", in.Name)
	}
}

// 锁定：只绑 path 的 multipart 请求不读 body。
func TestBindPathOnlySkipsMultipartBody(t *testing.T) {
	type in struct {
		ID string `path:"id"`
	}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile("f", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	body := &countingBody{r: bytes.NewReader(b.Bytes())}
	req := httptest.NewRequest(http.MethodPost, "/u/7", body)
	req.Header.Set(HeaderContentType, w.FormDataContentType())

	r := New()
	r.Post("/u/{id}", func(c *Ctx, v in) (string, error) { return v.ID, nil })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "7" {
		t.Fatalf("status/body = %d/%q, want 200/7", rec.Code, rec.Body.String())
	}
	if body.n != 0 {
		t.Fatalf("body read = %d bytes, want 0", body.n)
	}
}
