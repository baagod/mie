package bind

import (
    "errors"
    "fmt"
    "mime/multipart"
    "net/http"
    "net/url"
    "reflect"
)

// Decode 把请求里的位置来源写进 in；in 必须是 *IN，body 由调用方处理。
func Decode[IN any](r *http.Request, in *IN) error {
    if in == nil {
        return errors.New("bind: destination must be a non-nil pointer to IN")
    }

    p, err := planFor(reflect.TypeFor[IN]())
    if err != nil {
        return err
    }

    v := reflect.ValueOf(in).Elem() // IN

    // IN 是 *T：请求期分配 T，handler 永远拿到非 nil 指针。
    if v.Kind() == reflect.Pointer {
        if v.IsNil() {
            v.Set(reflect.New(v.Type().Elem()))
        }
        v = v.Elem()
    }

    return p.bind(r, v)
}

func (p *plan) bind(r *http.Request, out reflect.Value) (err error) {
    var query url.Values

    for _, s := range p.sources {
        if len(s.fields) == 0 {
            continue
        }

        if query == nil && (s.tag == bindForm || s.tag == bindQuery) {
            if query, err = url.ParseQuery(r.URL.RawQuery); err != nil {
                return
            }
        }

        if err = s.bind(s, r, out, query); err != nil {
            return
        }
    }

    return
}

func (s *source) bindPath(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    for _, sf := range s.fields {
        var ss []string
        if v := r.PathValue(sf.key); v != "" {
            ss = []string{v}
        }
        if err = setValues(out.Field(sf.index), ss); err != nil {
            return s.err(sf, err)
        }
    }
    return
}

func (s *source) bindHeader(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    for _, f := range s.fields {
        ss := r.Header.Values(f.key)
        if err = setValues(out.Field(f.index), ss); err != nil {
            return s.err(f, err)
        }
    }
    return
}

func (s *source) bindCookie(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    cookies := r.Cookies()
    for _, f := range s.fields {
        var ss []string
        for _, c := range cookies {
            if c.Name == f.key {
                ss = append(ss, c.Value)
            }
        }
        if err = setValues(out.Field(f.index), ss); err != nil {
            return s.err(f, err)
        }
    }
    return
}

func (s *source) bindQuery(_ *http.Request, out reflect.Value, query url.Values) (err error) {
    for _, f := range s.fields {
        if err = setValues(out.Field(f.index), query[f.key]); err != nil {
            return s.err(f, err)
        }
    }
    return
}

func (s *source) bindForm(r *http.Request, out reflect.Value, query url.Values) (err error) {
    if err = ParseForm(r); err != nil {
        return
    }

    // form 同时接受表单参数和同名查询参数：查询权重更高，最后写覆盖表单值（含空值）。
    for _, f := range s.fields {
        v := out.Field(f.index)

        if f.isFile { // 从已解析的文件表取字段：缺失保留 nil，单字段收到多文件报错。
            var files []*multipart.FileHeader
            if m := r.MultipartForm; m != nil {
                files = m.File[f.key]
            }

            if err = setFiles(v, files); err != nil {
                return s.err(f, err)
            }

            continue
        }

        // 文件 part 不进 PostForm，声明成非文件类型只会静默得到零值。
        if mf := r.MultipartForm; mf != nil && len(mf.File[f.key]) > 0 {
            return s.err(f, errors.New("file part must bind to *multipart.FileHeader"))
        }

        ss := r.PostForm[f.key]
        if q, ok := query[f.key]; ok {
            ss = q
        }

        if err = setValues(v, ss); err != nil {
            return s.err(f, err)
        }
    }

    return
}

// err 标注失败的来源与 IN 字段。
func (s *source) err(f field, err error) error {
    return fmt.Errorf("bind: %s.%s: %w", s.tag, f.name, err)
}
