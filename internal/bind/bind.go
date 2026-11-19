package bind

import (
    "encoding"
    "errors"
    "mime"
    "mime/multipart"
    "net/http"
    "reflect"
    "strconv"
)

// multipartMemory = 32M：multipart 的值与小文件驻留内存的上限，
// 超出部分标准库写到磁盘临时文件。数值取标准库内部默认值 defaultMaxMemory（32 << 20，未导出）；
// 传 0 不会回落到该默认值，而是全部写盘。
const multipartMemory = 32 << 20

var (
    fileHeaderType  = reflect.TypeFor[*multipart.FileHeader]()
    fileHeadersType = reflect.TypeFor[[]*multipart.FileHeader]()
    textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// Parse 把一组参数值解析成 T：标量取第一个，切片/数组写入全部，指针按需分配。
// 失败返回零值：数组是逐元素写入的，半截结果不能交给丢弃 err 的调用方。
func Parse[T any](s []string) (v T, err error) {
    if err = setValues(reflect.ValueOf(&v).Elem(), s); err != nil {
        var zero T
        return zero, err
    }
    return
}

// ParseForm 解析表单请求体：multipart 的值进 PostForm、文件进 MultipartForm，其余按 urlencoded 解析。
// bindForm 与 Ctx.Form 共用这一入口。
func ParseForm(r *http.Request) error {
    if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
        return r.ParseMultipartForm(multipartMemory)
    }
    return r.ParseForm()
}

// ParseFile 取 multipart 文件字段并解析成 T。
// T 必须是 *multipart.FileHeader 或 []*multipart.FileHeader；缺失返回零值。
func ParseFile[T any](r *http.Request, name string) (v T, err error) {
    dst := reflect.ValueOf(&v).Elem()
    if !fileKind(dst.Type()) {
        return v, errors.New("ParseFile requires *multipart.FileHeader or []*multipart.FileHeader")
    }

    if err = ParseForm(r); err != nil {
        return v, err
    }

    if m := r.MultipartForm; m != nil {
        if err = setFiles(dst, m.File[name]); err != nil {
            return v, err
        }
    }

    return
}

// IsFile 报告 T 是否为文件类型：*multipart.FileHeader 或 []*multipart.FileHeader。
func IsFile[T any]() bool {
    return fileKind(reflect.TypeFor[T]())
}

// Validate 报告 IN 的绑定声明是否可用；注册期调用，让写错的字段类型在启动时暴露。
func Validate(t reflect.Type) (err error) {
    _, err = planFor(t)
    return
}

// setFiles 把文件列表写入 v：文件切片整体替换，单文件要求至多一个。
func setFiles(v reflect.Value, files []*multipart.FileHeader) (err error) {
    if v.Type() == fileHeadersType {
        v.Set(reflect.ValueOf(files))
        return
    }

    switch len(files) {
    case 0:
        v.SetZero()
    case 1:
        v.Set(reflect.ValueOf(files[0]))
    default:
        return errors.New("multiple files for single field")
    }

    return
}

// setValues 把一组字符串写入 v：命名类型交给 TextUnmarshaler 整体解析、指针按需分配，
// 其余按形状分发——标量取第一个，切片整体替换，数组按序覆盖（多余丢弃、不足补零）。
func setValues(v reflect.Value, s []string) (err error) {
    if len(s) == 0 {
        return
    }

    // 命名切片/数组（如 net.IP）必须整体解析，不能进 Slice 分支被拆成元素；
    // 其它形状由 setScalar 自己判定，这里跳过，免得每个标量多付一次 Implements。
    if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
        if ok, e := unmarshalText(v, s[0]); ok {
            return e
        }
    }

    if v.Kind() == reflect.Pointer { // 解析成功后再分配
        p := reflect.New(v.Type().Elem())
        if err = setValues(p.Elem(), s); err == nil {
            v.Set(p)
        }
        return
    }

    switch v.Kind() {
    case reflect.Slice:
        ss := reflect.MakeSlice(v.Type(), len(s), len(s))
        for i := range s {
            if err = setScalar(ss.Index(i), s[i]); err != nil {
                return
            }
        }
        v.Set(ss)
    case reflect.Array:
        // 先清零：来源值不足时尾部保持零，也清掉前一个来源写的残留。
        v.SetZero()
        for i := range min(len(s), v.Len()) {
            if err = setScalar(v.Index(i), s[i]); err != nil {
                return
            }
        }
    default:
        return setScalar(v, s[0])
    }

    return
}

// setScalar 把字符串解析成标量写入；内置类型解析失败不写，TextUnmarshaler 是否已写由实现决定。
func setScalar(v reflect.Value, s string) (err error) {
    // 逐元素路径（[]time.Time 等）与标量路径共用同一判定。
    if ok, e := unmarshalText(v, s); ok {
        return e
    }

    switch v.Kind() {
    case reflect.String:
        v.SetString(s)
    case reflect.Bool:
        var b bool
        if b, err = strconv.ParseBool(s); err == nil {
            v.SetBool(b)
        }
    case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
        var n int64
        if n, err = strconv.ParseInt(s, 10, v.Type().Bits()); err == nil {
            v.SetInt(n)
        }
    case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
        var n uint64
        if n, err = strconv.ParseUint(s, 10, v.Type().Bits()); err == nil {
            v.SetUint(n)
        }
    case reflect.Float32, reflect.Float64:
        var f float64
        if f, err = strconv.ParseFloat(s, v.Type().Bits()); err == nil {
            v.SetFloat(f)
        }
    default:
        return errors.New("unsupported scalar kind " + v.Kind().String())
    }

    return
}

// scalarKind 判断字段类型能否逐元素解析；指针逐层拆开，切片、数组看元素类型。
// 实现 encoding.TextUnmarshaler 的类型（time.Time、netip.Addr、net.IP 等）也算标量。
func scalarKind(t reflect.Type) bool {
    if t.Kind() == reflect.Pointer {
        return scalarKind(t.Elem())
    }

    if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
        if reflect.PointerTo(t).Implements(textUnmarshaler) {
            return true // 命名切片/数组（net.IP）整体解析
        }
        t = t.Elem()
    }

    switch t.Kind() {
    case reflect.String, reflect.Bool,
        reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
        reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
        reflect.Float32, reflect.Float64:
        return true
    default:
        return reflect.PointerTo(t).Implements(textUnmarshaler)
    }
}

// fileKind 判断字段是否承载上传文件：单文件或文件切片。
func fileKind(t reflect.Type) bool {
    return t == fileHeaderType || t == fileHeadersType
}

// unmarshalText 报告 v 能否整体交给 TextUnmarshaler，并返回其结果。
// 只有命名类型可能有方法集，PkgPath 为空先挡掉，省掉每值一次 Implements。
func unmarshalText(v reflect.Value, s string) (bool, error) {
    if v.Type().PkgPath() == "" || !v.CanAddr() || !v.Addr().Type().Implements(textUnmarshaler) {
        return false, nil
    }
    return true, v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
}
