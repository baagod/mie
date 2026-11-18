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

// multipartMemory = 32M：multipart 的值与小文件驻留内存的上限，
// 超出部分标准库写到磁盘临时文件。数值取标准库内部默认值 defaultMaxMemory（32 << 20，未导出）；
// 传 0 不会回落到该默认值，而是全部写盘。
const multipartMemory = 32 << 20

var (
    fileHeaderType  = reflect.TypeFor[*multipart.FileHeader]()
    fileHeadersType = reflect.TypeFor[[]*multipart.FileHeader]()
)

// fileKind 判断字段是否承载上传文件：单文件或文件切片。
func fileKind(t reflect.Type) bool {
    return t == fileHeaderType || t == fileHeadersType
}

// bindFile 从已解析的文件表取字段：缺失保留 nil，单字段收到多个文件报错。
func bindFile(r *http.Request, v reflect.Value, f bindField) (err error) {
    var hs []*multipart.FileHeader
    if mf := r.MultipartForm; mf != nil {
        hs = mf.File[f.key]
    }

    if v.Type() == fileHeadersType {
        v.Set(reflect.ValueOf(hs))
        return
    }

    switch len(hs) {
    case 0:
        v.SetZero()
    case 1:
        v.Set(reflect.ValueOf(hs[0]))
    default:
        return bindError(f, errors.New("multiple files for single field"))
    }

    return
}

// parseQuery 解析 URL 参数，失败返回解析器原始错误。
func parseQuery(r *http.Request) (url.Values, error) {
    q, err := url.ParseQuery(r.URL.RawQuery)
    if err != nil {
        return nil, err
    }
    return q, nil
}

// parseForm 解析表单参数：multipart 的值经 ParseMultipartForm 进入 PostForm，其它类型走 ParseForm。
func parseForm(r *http.Request) error {
    if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
        return r.ParseMultipartForm(multipartMemory)
    }
    return r.ParseForm()
}

// bindError 标注失败的来源与 IN 字段。
func bindError(f bindField, err error) error {
    return fmt.Errorf("bind: %s.%s: %w", f.source, f.name, err)
}

// Validate 报告 IN 的绑定声明是否可用；注册期调用，让写错的字段类型在启动时暴露。
func Validate(t reflect.Type) error {
    return planFor(t).err
}
