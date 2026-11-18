package bind

import (
    "errors"
    "mime/multipart"
    "net/http"
    "reflect"
)

var (
    fileHeaderType  = reflect.TypeFor[*multipart.FileHeader]()
    fileHeadersType = reflect.TypeFor[[]*multipart.FileHeader]()
)

// fileKind 判断字段是否承载上传文件：单文件或文件切片。
func fileKind(t reflect.Type) bool {
    return t == fileHeaderType || t == fileHeadersType
}

// bindFile 从已解析的文件表取字段：缺失保留 nil，单字段收到多个文件报错。
func bindFile(r *http.Request, v reflect.Value, f field) error {
    var hs []*multipart.FileHeader
    if mf := r.MultipartForm; mf != nil {
        hs = mf.File[f.key]
    }

    if v.Type() == fileHeadersType {
        v.Set(reflect.ValueOf(hs))
        return nil
    }

    switch len(hs) {
    case 0:
        v.SetZero()
    case 1:
        v.Set(reflect.ValueOf(hs[0]))
    default:
        return errors.New("bind: " + f.source + "." + f.name + ": multiple files for single field")
    }
    return nil
}
