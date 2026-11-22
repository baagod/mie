package bind

import (
	"net/http"
	"net/url"
)

// FormCache 缓存一次请求的 query 与表单体，两张表各解析一次。
type FormCache struct {
	query, form url.Values
	err         error
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
func (c *FormCache) Form(r *http.Request) url.Values {
	if c.form == nil {
		if c.err != nil {
			c.form = url.Values{}
		} else {
			c.err, c.form = ParseForm(r), r.PostForm
		}
	}
	return c.form
}
