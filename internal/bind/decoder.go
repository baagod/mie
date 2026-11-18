package bind

import (
    "errors"
    "fmt"
    "mime"
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

    t := reflect.TypeFor[IN]()
    ptr := t.Kind() == reflect.Pointer
    if ptr {
        t = t.Elem()
    }

    p := planFor(t)
    if p.err != nil {
        return p.err
    }

    v := reflect.ValueOf(in).Elem() // IN

    // IN 是 *T：请求期分配 T，handler 永远拿到非 nil 指针。
    if ptr {
        if v.IsNil() {
            v.Set(reflect.New(t))
        }
        v = v.Elem()
    }

    return p.bind(r, v)
}

func (p *plan) bind(r *http.Request, out reflect.Value) (err error) {
    var query url.Values

    for _, fs := range p.groups {
        if len(fs.fields) == 0 {
            continue
        }

        if query == nil && (fs.tag == bindForm || fs.tag == bindQuery) {
            if query, err = url.ParseQuery(r.URL.RawQuery); err != nil {
                return
            }
        }

        if err = fs.bind(fs, r, out, query); err != nil {
            return
        }
    }

    return
}

func (fs *Fields) bindPath(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    for _, sf := range fs.fields {
        var s []string
        if v := r.PathValue(sf.key); v != "" {
            s = []string{v}
        }
        if err = setValues(out.Field(sf.index), s); err != nil {
            return fs.err(sf, err)
        }
    }
    return
}

func (fs *Fields) bindHeader(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    for _, f := range fs.fields {
        s := r.Header.Values(f.key)
        if err = setValues(out.Field(f.index), s); err != nil {
            return fs.err(f, err)
        }
    }
    return
}

func (fs *Fields) bindCookie(r *http.Request, out reflect.Value, _ url.Values) (err error) {
    cookies := r.Cookies()
    for _, f := range fs.fields {
        var s []string
        for _, c := range cookies {
            if c.Name == f.key {
                s = append(s, c.Value)
            }
        }
        if err = setValues(out.Field(f.index), s); err != nil {
            return fs.err(f, err)
        }
    }
    return
}

func (fs *Fields) bindQuery(_ *http.Request, out reflect.Value, query url.Values) (err error) {
    for _, f := range fs.fields {
        if err = setValues(out.Field(f.index), query[f.key]); err != nil {
            return fs.err(f, err)
        }
    }
    return
}

func (fs *Fields) bindForm(r *http.Request, out reflect.Value, query url.Values) (err error) {
    // 请求体只在绑定 form 时解析：multipart 的值进 PostForm，文件进 MultipartForm。
    if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
        if err = r.ParseMultipartForm(multipartMemory); err != nil {
            return
        }
    } else if err = r.ParseForm(); err != nil {
        return
    }

    // form 同时接受表单参数和同名查询参数：查询权重更高，最后写覆盖表单值（含空值）。
    for _, f := range fs.fields {
        v := out.Field(f.index)

        if f.isFile { // 从已解析的文件表取字段：缺失保留 nil，单字段收到多文件报错。
            var files []*multipart.FileHeader
            if m := r.MultipartForm; m != nil {
                files = m.File[f.key]
            }

            if v.Type() == fileHeadersType {
                v.Set(reflect.ValueOf(files))
                continue
            }

            switch len(files) {
            case 0:
                v.SetZero()
            case 1:
                v.Set(reflect.ValueOf(files[0]))
            default:
                return fs.err(f, errors.New("multiple files for single field"))
            }

            continue
        }

        // 文件 part 不进 PostForm，声明成非文件类型只会静默得到零值。
        if mf := r.MultipartForm; mf != nil && len(mf.File[f.key]) > 0 {
            return fs.err(f, errors.New("file part must bind to *multipart.FileHeader"))
        }

        s := r.PostForm[f.key]
        if q, ok := query[f.key]; ok {
            s = q
        }

        if err = setValues(v, s); err != nil {
            return fs.err(f, err)
        }
    }

    return
}

// err 标注失败的来源与 IN 字段。
func (fs *Fields) err(f field, err error) error {
    return fmt.Errorf("bind: %s.%s: %w", fs.tag, f.name, err)
}
