package mux

import (
    "encoding/json/v2"
    "errors"
    "mime"
    "net/http"
    "reflect"
    "strconv"
)

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
    index  int    // 字段下标，reflect.Value.Field(index) 用
    name   string // 字段名，只用于错误文案
    source string // 从哪取值
    key    string // 取值的键：path 参数名、header 名，或 query/form 的参数名
}

// bindPlan 是首次请求时算好的 IN 绑定方案；请求期只按它填值，不再解析 tag。
type bindPlan struct {
    fields  []bindField // 绑定项，按 struct 声明顺序、字段内按来源顺序排列
    ptr     bool        // IN 是指针：请求期先分配，handler 永远拿到非 nil 指针
    hasForm bool        // 有 query/form 字段：请求期要先 ParseForm
    err     error       // 编译失败：fields 为空；serve 在绑定前拦截
}

// newBindPlan 在首次请求时把 IN 的类型检查一遍并生成绑定方案。
// 非法声明写进 p.err，不 panic；每个 IN 类型只调用一次。
func newBindPlan(t reflect.Type) *bindPlan {
    p := &bindPlan{}

    if p.ptr = t.Kind() == reflect.Pointer; p.ptr {
        t = t.Elem()
    }

    // 非 struct 的 IN（map/切片/标量）：没有字段可绑，整个 body 就是值。
    if t.Kind() != reflect.Struct {
        return p
    }

    for i := 0; i < t.NumField(); i++ {
        sf := t.Field(i)
        if sf.PkgPath != "" { // 跳过未导出字段
            continue
        }

        // 同一字段可以声明多个来源：按 posTags 的顺序依次生成绑定项，
        // 请求期后写的覆盖先写的；某个来源没提供值就跳过，保留前面写进去的值。
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

// bind 把请求中的值写进 IN；in 必须是可设置的 IN 值。
// 顺序：分配指针 → 逐字段填位置来源 → 最后整块解 JSON body（覆盖前面的值）。
func (p *bindPlan) bind(c *Ctx, in reflect.Value) (err error) {
    // *struct：请求期在这里分配，handler 永远拿到非 nil 指针。
    if p.ptr { // p.ptr 在首次请求编译时由 newBindPlan 标记：IN 是 *T
        v := reflect.New(in.Type().Elem()) // 分配一个新的 T（零值），得到 *T
        in.Set(v)                          // 把 IN 指向这个新分配的 *T
        in = v.Elem()                      // 后续统一操作 T 本体，避免到处判断指针
    }

    // query 与 form 合并：ParseForm 对 GET 只取 URL，对 POST/PUT/PATCH + urlencoded 读 body。
    if p.hasForm {
        if err = c.R.ParseForm(); err != nil {
            return Status400BadRequest("bind params: " + err.Error())
        }
    }

    for _, f := range p.fields {
        // 位置来源统一走字符串解析
        if err = bindScalar(c, in.Field(f.index), f); err != nil {
            return err
        }
    }

    // body 只在 POST + JSON 时读；整块解，覆盖路径/参数写入的值。
    // 按 media type 比较：忽略大小写和 charset 等参数。
    ct, _, _ := mime.ParseMediaType(c.ContentType())
    if c.R.Method == http.MethodPost && ct == MIMEAppJSON {
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

// bindBytes 取请求体字节（经 Ctx.Body 缓存），并把读取错误转换成 bind 错误。
// 不设内置上限；应用可用 http.MaxBytesReader 中间件限制，超限时这里返回 413。
func bindBytes(c *Ctx) (b []byte, err error) {
    if b, err = c.Body(); err != nil {
        if _, ok := errors.AsType[*Error](err); !ok {
            return nil, Status400BadRequest("bind body: " + err.Error())
        }
    }
    return
}

// bindScalar 把位置来源的值写进字段。
// 来源缺失（没有这个参数/头）就跳过，保留前面来源写进去的值；
// 来源存在但值为空串（?x=、空 header）则把字段清零。
func bindScalar(c *Ctx, v reflect.Value, f bindField) (err error) {
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

    if s[0] == "" {
        v.SetZero()
        return
    }

    if err = setScalar(v, s[0]); err != nil {
        return Status400BadRequest("mux: bind " + f.source + "." + f.name + ": " + err.Error())
    }

    return
}

func setScalar(v reflect.Value, s string) (err error) {
    if v.Kind() == reflect.Pointer { // 指针字段先解析成功再分配
        p := reflect.New(v.Type().Elem())
        if err = setScalar(p.Elem(), s); err == nil {
            v.Set(p)
        }
        return
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

// scalarKind 判断字段类型能否从字符串转换；指针取其元素类型。
func scalarKind(t reflect.Type) bool {
    if t.Kind() == reflect.Pointer {
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
