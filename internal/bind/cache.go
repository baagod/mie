package bind

import (
	"mime"
	"net/http"
	"net/url"
)

// FormCache 缓存一次请求的 query 与表单体，两张表各解析一次。
type FormCache struct {
	query, form url.Values
	ct          string
	err         error
}

// ContentType 预置已解析的媒体类型并返回自身，预置后不再解析请求头。
func (c *FormCache) ContentType(ct string) *FormCache {
	c.ct = ct
	return c
}

// Get 按 query、form、file 的优先级取第一个命中的值并转换，file 字段不查 query。
// 解析失败记在缓存上由 Decode 统一报，这里只返回值转换的错误。
func (c *FormCache) Get[T any](r *http.Request, name string) (v T, err error) {
	isFile := IsFile[T]()
	if !isFile {
		q := c.Query(r)
		if ss, ok := q[name]; ok {
			return Parse[T](ss)
		}
	}

	p := c.Form(r)
	if isFile {
		v, _ = ParseFile[T](r.MultipartForm, name)
		return
	}

	return Parse[T](p[name])
}

// Query 解析 URL 参数并缓存；错误存入 err，不打断取值。
func (c *FormCache) Query(r *http.Request) url.Values {
	if c.query == nil {
		if r.URL.RawQuery == "" || c.err != nil {
			c.query = url.Values{}
		} else {
			c.query, c.err = url.ParseQuery(r.URL.RawQuery)
		}
	}
	return c.query
}

// Form 解析 body 并缓存 r.PostForm；query 由调用方按需通过 Query 取。
// ct 由 ContentType 预置，已解析的媒体类型不会在这里重复解析；为空时按请求头补一次。
func (c *FormCache) Form(r *http.Request) url.Values {
	if c.form == nil {
		if c.err != nil {
			c.form = url.Values{}
		} else {
			if c.ct == "" {
				c.ct, _, _ = mime.ParseMediaType(r.Header.Get("Content-Type"))
			}

			if c.ct == "multipart/form-data" {
				c.err = r.ParseMultipartForm(multipartMemory)
			} else {
				c.err = r.ParseForm()
			}

			c.form = r.PostForm
		}
	}

	return c.form
}
