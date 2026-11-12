package mux

import (
    "encoding/json/v2"
    "errors"
    "mime"
    "net/http"
    "reflect"
    "strconv"
)

// 绑定实现：字段按显式 tag 从 path/header/参数取值，POST + JSON 时再整块解码 body。
// 每个处理器首次请求编译一次 bindPlan（含错误），请求期只按方案赋值。
const (
    bindPath   = "path"
    bindHeader = "header"
    bindQuery  = "query"
    bindForm   = "form"
)

// posTags 是位置来源的 tag 名，顺序即绑定顺序，后写覆盖先写。
// query 与 form 都从 ParseForm 后的参数表取值。
var posTags = [...]string{bindPath, bindHeader, bindQuery, bindForm}

// bindField 是 struct IN 的一个取值项；同一字段声明多个来源时会有多项。
type bindField struct {
    index  int    // 字段下标
    name   string // 仅用于错误文案
    source string
    key    string
}

// bindPlan 是首次请求编译出的赋值方案；请求期不再解析 tag。
type bindPlan struct {
    fields  []bindField
    ptr     bool  // IN 是 *T：请求期分配，handler 永远拿到非 nil
    hasForm bool  // 有参数来源：请求期先 ParseForm
    err     error // 非 nil 时 serve 直接返回 500
}

// newBindPlan 检查 IN 类型并生成绑定方案；非法声明记入 p.err，不 panic。
func newBindPlan(t reflect.Type) *bindPlan {
    p := &bindPlan{}

    if p.ptr = t.Kind() == reflect.Pointer; p.ptr {
        t = t.Elem()
    }

    // 非 struct 的 IN 没有字段：整个 body 就是值（由结尾的整块解码处理）。
    if t.Kind() != reflect.Struct {
        return p
    }

    for i := 0; i < t.NumField(); i++ {
        sf := t.Field(i)
        if sf.PkgPath != "" { // 未导出字段
            continue
        }

        // 同一字段的多来源生成多项：按 path→header→参数 的顺序，后写覆盖先写；
        // query 与 form 同源，都读 ParseForm 后的参数表。
        for _, tag := range posTags {
            v, _ := sf.Tag.Lookup(tag)
            if v == "" {
                continue
            }

            if !scalarKind(sf.Type) {
                p.err = errors.New("field " + t.String() + "." + sf.Name + ": unsupported binding type " + sf.Type.String())
                return p
            }

            p.fields = append(p.fields, bindField{index: i, name: sf.Name, source: tag, key: v})
            if tag == bindQuery || tag == bindForm {
                p.hasForm = true
            }
        }
    }

    return p
}

// bind 按方案把请求值写入 in；in 必须可设置。
func (p *bindPlan) bind(c *Ctx, in reflect.Value) (err error) {
    // *T：请求期分配，handler 永远拿到非 nil 指针。
    if p.ptr {
        v := reflect.New(in.Type().Elem())
        in.Set(v)
        in = v.Elem()
    }

    // query/form 同源：GET 只取 URL query，POST/PUT/PATCH 的 urlencoded body 也合并进来。
    if p.hasForm {
        if err = c.R.ParseForm(); err != nil {
            return Status400BadRequest("bind params: " + err.Error())
        }
    }

    for _, f := range p.fields {
        if err = bindValue(c, in.Field(f.index), f); err != nil {
            return err
        }
    }

    if c.R.Method != http.MethodPost {
        return
    }

    // body 仅 POST + JSON：整块解码，覆盖前面写入的值；CT 按 media type 比较（忽略大小写与 charset）。
    ct, _, _ := mime.ParseMediaType(c.ContentType())
    if ct == MIMEAppJSON {
        var b []byte
        if b, err = bindBytes(c); err != nil {
            return err
        }
        if len(b) > 0 {
            if err = json.Unmarshal(b, in.Addr().Interface()); err != nil {
                return Status400BadRequest("bind body: " + err.Error())
            }
        }
    }

    return
}

// bindBytes 经 Ctx.Body 取请求体；读取错误转成 bind 错误，已是 *Error 的（如 413）原样返回。
// 不设内置上限，应用可用 http.MaxBytesReader 收紧。
func bindBytes(c *Ctx) (b []byte, err error) {
    if b, err = c.Body(); err != nil {
        if _, ok := errors.AsType[*Error](err); !ok {
            return nil, Status400BadRequest("bind body: " + err.Error())
        }
    }
    return
}

// bindValue 把该字段来源的值写进去：缺失（没有该参数/头）跳过，保留前面来源的值；
// 存在但首个值为空串（?x=、空 header）则清零字段。切片/数组写入全部值，标量取第一个。
func bindValue(c *Ctx, v reflect.Value, f bindField) (err error) {
    var s []string
    switch f.source {
    case bindPath:
        if p := c.R.PathValue(f.key); p != "" {
            s = []string{p}
        }
    case bindHeader:
        s = c.R.Header.Values(f.key)
    case bindQuery, bindForm:
        s, _ = c.R.Form[f.key]
    }

    if len(s) == 0 {
        return
    }

    if err = setValues(v, s); err != nil {
        return Status400BadRequest("bind: " + f.source + "." + f.name + ": " + err.Error())
    }

    return
}

// setValues 写字段：标量取第一个；切片整体替换；数组按序覆盖，多余丢弃、不足补零。
func setValues(v reflect.Value, s []string) (err error) {
    if s[0] == "" { // 存在但为空：清零，与标量语义一致
        v.SetZero()
        return
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
func scalarKind(t reflect.Type) bool {
    if t.Kind() == reflect.Pointer {
        t = t.Elem()
    }

    if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
        t = t.Elem()
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
