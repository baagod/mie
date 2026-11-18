package bind

import (
    "errors"
    "mime/multipart"
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

// fieldDeclError 标注 IN 字段的声明错误，注册期暴露。
func fieldDeclError(t reflect.Type, sf reflect.StructField, msg string) error {
    return errors.New("field " + t.String() + "." + sf.Name + ": " + msg)
}

// Validate 报告 IN 的绑定声明是否可用；注册期调用，让写错的字段类型在启动时暴露。
func Validate(t reflect.Type) error {
    return planFor(t).err
}
