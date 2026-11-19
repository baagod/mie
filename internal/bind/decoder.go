package bind

import (
    "errors"
    "fmt"
    "mime/multipart"
    "net/http"
    "reflect"
)

// Decode 把请求里的位置来源写进 in；in 必须是 *IN。
// cache 为 nil 时用一次性缓存，传入时解析结果缓存在其中。
func Decode[IN any](r *http.Request, in *IN, cache *FormCache) (err error) {
    if in == nil {
        return errors.New("bind: destination must be a non-nil pointer to IN")
    }

    p, e := planFor(reflect.TypeFor[IN]())
    if e != nil {
        return e
    }

    v := reflect.ValueOf(in).Elem()  // IN
    if v.Kind() == reflect.Pointer { // 请求期分配 T，handler 永远拿到非 nil 指针。
        if v.IsNil() {
            v.Set(reflect.New(v.Type().Elem()))
        }
        v = v.Elem()
    }

    if cache == nil {
        cache = &FormCache{}
    }

    for _, s := range p.sources {
        if len(s.fields) == 0 {
            continue
        }
        if err = s.bind(s, r, v, cache); err != nil {
            return err
        }
    }

    return
}

func (s *source) bindPath(r *http.Request, out reflect.Value, _ *FormCache) (err error) {
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

func (s *source) bindHeader(r *http.Request, out reflect.Value, _ *FormCache) (err error) {
    for _, f := range s.fields {
        ss := r.Header.Values(f.key)
        if err = setValues(out.Field(f.index), ss); err != nil {
            return s.err(f, err)
        }
    }
    return
}

func (s *source) bindCookie(r *http.Request, out reflect.Value, _ *FormCache) (err error) {
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

func (s *source) bindForm(r *http.Request, out reflect.Value, cache *FormCache) (err error) {
    for _, f := range s.fields {
        v := out.Field(f.index)

        if !f.isFile {
            query := cache.Query(r)
            if cache.err != nil {
                return cache.err
            }
            if ss, ok := query[f.key]; ok { // URL 命中时不读 body
                if err = setValues(v, ss); err != nil {
                    return s.err(f, err)
                }
                continue
            }
        }

        if cache.Form(r); cache.err != nil {
            return cache.err
        }

        if f.isFile { // 文件需要先解析 body；缺失保留 nil，单字段收到多文件报错。
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
        if m := r.MultipartForm; m != nil && len(m.File[f.key]) > 0 {
            return s.err(f, errors.New("file part must bind to *multipart.FileHeader"))
        }

        if err = setValues(v, r.PostForm[f.key]); err != nil {
            return s.err(f, err)
        }
    }

    return
}

// err 标注失败的来源与 IN 字段。
func (s *source) err(f field, err error) error {
    return fmt.Errorf("bind: %s.%s: %w", s.tag, f.name, err)
}
