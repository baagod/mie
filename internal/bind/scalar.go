package bind

import (
    "encoding"
    "errors"
    "net/http"
    "net/url"
    "reflect"
    "strconv"
)

// textUnmarshaler 是标量解析的扩展点：类型的指针实现它时，字符串交给它解析。
var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// bindValue 把该字段来源的值写进去：缺失（没有该参数/头/cookie）跳过，保留前面来源的值；
// 存在但首个值为空串（?x=、空 header）则清零字段。切片/数组写入全部值，标量取第一个。
func bindValue(r *http.Request, query url.Values, cookies []*http.Cookie, v reflect.Value, f field) (err error) {
    var s []string
    switch f.source {
    case bindPath:
        if p := r.PathValue(f.key); p != "" {
            s = []string{p}
        }
    case bindHeader:
        s = r.Header.Values(f.key)
    case bindQuery:
        s = query[f.key]
    case bindForm:
        // form 同时接受表单参数和同名查询参数：查询权重更高，最后写覆盖表单值（含空值）。
        s = r.PostForm[f.key]
        if q, ok := query[f.key]; ok {
            s = q
        }
    case bindCookie:
        for _, c := range cookies {
            if c.Name == f.key {
                s = append(s, c.Value)
            }
        }
    }

    if err = setValues(v, s); err != nil {
        return errors.New("bind: " + f.source + "." + f.name + ": " + err.Error())
    }

    return
}

// setValues 写字段：标量取第一个；切片整体替换；数组按序覆盖，多余丢弃、不足补零。
func setValues(v reflect.Value, s []string) (err error) {
    if len(s) == 0 {
        return
    }

    // 命名切片/数组类型（如 net.IP）实现 TextUnmarshaler 时要整体解析，
    // 不能掉进下面的 Slice 分支被逐元素处理；其余值类型统一走 setScalar。
    // 内置类型没有方法集，PkgPath 为空先挡掉，省掉每值一次 Implements。
    if v.Type().PkgPath() != "" && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) &&
        v.CanAddr() && v.Addr().Type().Implements(textUnmarshaler) {
        return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s[0]))
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

// setScalar 把字符串解析成标量写入；解析失败不写。
func setScalar(v reflect.Value, s string) (err error) {
    // 逐元素路径（[]time.Time 等）：元素指针实现 TextUnmarshaler 时交给它解析。
    // 内置类型没有方法集，PkgPath 为空先挡掉，省掉每值一次 Implements。
    if v.Type().PkgPath() != "" && v.CanAddr() && v.Addr().Type().Implements(textUnmarshaler) {
        return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
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

// scalarKind 判断字段类型能否逐元素解析；指针、切片、数组看元素类型。
// 实现 encoding.TextUnmarshaler 的类型（time.Time、netip.Addr、net.IP 等）也算标量。
func scalarKind(t reflect.Type) bool {
    if t.Kind() == reflect.Pointer {
        t = t.Elem()
    }

    if reflect.PointerTo(t).Implements(textUnmarshaler) {
        return true
    }

    if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
        t = t.Elem()
        if reflect.PointerTo(t).Implements(textUnmarshaler) {
            return true
        }
    }

    switch t.Kind() {
    case reflect.String, reflect.Bool,
        reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
        reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
        reflect.Float32, reflect.Float64:
        return true
    default:
        return false
    }
}
