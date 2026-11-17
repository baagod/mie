package bind

import (
    "errors"
    "fmt"
    "net/http"
    "net/url"
    "reflect"
)

// Decode 把请求里的位置来源写进 in；in 必须是 *IN，body 由调用方处理。
func Decode[IN any](r *http.Request, in *IN) (err error) {
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

    // 只有存在参数来源时才 ParseForm，避免多读/多解析 body。
    // query 单独取 URL 参数：r.Form 把 body 参数排在前面，直接读它会被 body 覆盖。
    // ParseForm 已校验过 query，这里的解析错误忽略即可。
    var query url.Values
    var cookies []*http.Cookie
    if p.hasParams {
        if err = r.ParseForm(); err != nil {
            return fmt.Errorf("bind params: %w", err)
        }
        query = r.URL.Query()
    }

    if p.hasCookie {
        cookies = r.Cookies()
    }

    for _, f := range p.fields {
        if err = bindValue(r, query, cookies, v.Field(f.index), f); err != nil {
            return
        }
    }

    return
}
