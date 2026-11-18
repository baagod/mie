package bind

import (
    "errors"
    "fmt"
    "mime"
    "net/http"
    "net/url"
    "reflect"
)

// multipartMemory = 32M：multipart 的值与小文件驻留内存的上限，
// 超出部分标准库写到磁盘临时文件。数值取标准库内部默认值 defaultMaxMemory（32 << 20，未导出）；
// 传 0 不会回落到该默认值，而是全部写盘。
const multipartMemory = 32 << 20

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

    // 只有存在参数来源时才解析 body，避免多读/多解析。
    // query 单独取 URL 参数：r.Form 把 body 参数排在前面，直接读它会被 body 覆盖。
    var query url.Values
    var cookies []*http.Cookie

    if p.hasParams {
        // multipart 的值只能经 ParseMultipartForm 进入 PostForm；其它类型继续走 ParseForm。
        if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
            err = r.ParseMultipartForm(multipartMemory)
        } else {
            err = r.ParseForm()
        }

        if err != nil {
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
