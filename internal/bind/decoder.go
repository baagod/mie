package bind

import (
    "errors"
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
    // 顺序必须与 posTags 一致，后写覆盖先写。
    var query url.Values
    if len(p.forms) > 0 || len(p.queries) > 0 {
        if query, err = parseQuery(r); err != nil {
            return
        }
    }

    if err = p.bindForm(r, out, query); err != nil {
        return
    }

    if err = p.bindQuery(out, query); err != nil {
        return
    }

    if err = p.bindCookie(r, out); err != nil {
        return
    }

    if err = p.bindHeader(r, out); err != nil {
        return
    }

    return p.bindPath(r, out)
}

func (p *plan) bindPath(r *http.Request, out reflect.Value) (err error) {
    if len(p.paths) == 0 {
        return
    }

    for _, f := range p.paths {
        var s []string
        if v := r.PathValue(f.key); v != "" {
            s = []string{v}
        }
        if err = setValues(out.Field(f.index), s); err != nil {
            return bindError(f, err)
        }
    }

    return
}

func (p *plan) bindHeader(r *http.Request, out reflect.Value) (err error) {
    if len(p.headers) == 0 {
        return
    }

    for _, f := range p.headers {
        s := r.Header.Values(f.key)
        if err = setValues(out.Field(f.index), s); err != nil {
            return bindError(f, err)
        }
    }

    return
}

func (p *plan) bindCookie(r *http.Request, out reflect.Value) (err error) {
    if len(p.cookies) == 0 {
        return
    }

    cookies := r.Cookies()
    for _, f := range p.cookies {
        var s []string
        for _, c := range cookies {
            if c.Name == f.key {
                s = append(s, c.Value)
            }
        }
        if err = setValues(out.Field(f.index), s); err != nil {
            return bindError(f, err)
        }
    }

    return
}

func (p *plan) bindQuery(out reflect.Value, query url.Values) (err error) {
    if len(p.queries) == 0 {
        return
    }

    for _, f := range p.queries {
        if err = setValues(out.Field(f.index), query[f.key]); err != nil {
            return bindError(f, err)
        }
    }

    return
}

func (p *plan) bindForm(r *http.Request, out reflect.Value, query url.Values) (err error) {
    if len(p.forms) == 0 {
        return
    }

    if err = parseForm(r); err != nil {
        return
    }

    // form 同时接受表单参数和同名查询参数：查询权重更高，最后写覆盖表单值（含空值）。
    for _, f := range p.forms {
        v := out.Field(f.index)
        if fileKind(v.Type()) {
            if err = bindFile(r, v, f); err != nil {
                return
            }
            continue
        }

        // 文件 part 不进 PostForm，声明成非文件类型只会静默得到零值。
        if mf := r.MultipartForm; mf != nil && len(mf.File[f.key]) > 0 {
            return bindError(f, errors.New("file part must bind to *multipart.FileHeader"))
        }

        s := r.PostForm[f.key]
        if q, ok := query[f.key]; ok {
            s = q
        }

        if err = setValues(v, s); err != nil {
            return bindError(f, err)
        }
    }

    return
}
