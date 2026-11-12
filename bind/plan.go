package bind

import (
    "encoding/json/v2"
    "errors"
    "net/http"
    "reflect"
)

// 绑定实现：字段按显式 tag 从 path/header/参数取值；body 由 Ctx.Bind 按 Content-Type 整块解码。
// 每个处理器首次请求编译一次 Plan（含错误），请求期只按方案赋值。
const (
    bindPath   = "path"
    bindHeader = "header"
    bindQuery  = "query"
    bindForm   = "form"
)

// posTags 是位置来源的 tag 名，顺序即绑定顺序，后写覆盖先写。
// query 与 form 都从 ParseForm 后的参数表取值。
var posTags = [...]string{bindPath, bindHeader, bindQuery, bindForm}

// field 是 struct IN 的一个取值项；同一字段声明多个来源时会有多项。
type field struct {
    index  int    // 字段下标
    name   string // 仅用于错误文案
    source string // 来自哪个 tag
    key    string // tag 值
}

// Plan 是首次请求编译出的赋值方案；请求期不再解析 tag。
type Plan struct {
    t       reflect.Type // IN 类型
    fields  []field
    ptr     bool // IN 是 *T：请求期分配，handler 永远拿到非 nil
    hasForm bool // 有参数来源：请求期先 ParseForm
}

// NewPlan 检查 IN 类型并生成绑定方案；非法声明记入 p.err，不 panic。
func NewPlan(t reflect.Type) (p *Plan, err error) {
    p = &Plan{t: t}
    if p.ptr = t.Kind() == reflect.Pointer; p.ptr {
        t = t.Elem()
    }

    // 非 struct 的 IN 没有字段：整个 body 就是值（由结尾的整块解码处理）。
    if t.Kind() != reflect.Struct {
        return
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
                return nil, errors.New("field " + t.String() + "." + sf.Name + ": unsupported binding type " + sf.Type.String())
            }

            p.fields = append(p.fields, field{index: i, name: sf.Name, source: tag, key: v})
            if tag == bindQuery || tag == bindForm {
                p.hasForm = true
            }
        }
    }

    return
}

// Bind 按方案把请求值写入 in；in 必须是 *IN。
func (p *Plan) Bind(r *http.Request, in any) (err error) {
    v := reflect.ValueOf(in)
    if v.Kind() != reflect.Pointer || v.IsNil() || v.Type().Elem() != p.t {
        return errors.New("bind: destination must be a non-nil pointer to IN")
    }
    v = v.Elem() // IN

    // IN 是 *T：请求期分配 T，handler 永远拿到非 nil 指针。
    if p.ptr {
        if v.IsNil() { // IN 是 *T 时，该值可能是 nil。
            v.Set(reflect.New(p.t.Elem()))
        }
        v = v.Elem()
    }

    // 只有存在参数来源时才 ParseForm，避免多读/多解析 body。
    if p.hasForm {
        if err = r.ParseForm(); err != nil {
            return errors.New("bind params: " + err.Error())
        }
    }

    for _, f := range p.fields {
        if err = bindValue(r, v.Field(f.index), f); err != nil {
            return err
        }
    }

    return
}

func (p *Plan) BindJSON(b []byte, in any) error {
    return json.Unmarshal(b, in)
}
