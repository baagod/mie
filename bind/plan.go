package bind

import (
    "errors"
    "net/http"
    "reflect"
    "sync"
)

// Bind 按显式 tag 把 path/header/参数写进 IN；body 由调用方按 Content-Type 整块解码。
const (
    bindPath   = "path"
    bindHeader = "header"
    bindQuery  = "query"
    bindForm   = "form"
)

// plans 按 IN 的元素类型缓存方案：类型集合编译期固定，条目有界，不淘汰。
var plans sync.Map // map[reflect.Type]*plan

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

// plan 是 IN 类型的绑定方案，存入后不再修改。
type plan struct {
    fields  []field
    hasForm bool
    err     error
}

// planFor 取类型的方案；首次并发调用最多重复扫描一次，结果一致。
func planFor(t reflect.Type) *plan {
    if p, ok := plans.Load(t); ok {
        return p.(*plan)
    }

    p := scan(t)
    actual, _ := plans.LoadOrStore(t, p)
    return actual.(*plan)
}

// scan 检查 IN 的字段声明并生成方案；非法声明记入 err。
func scan(t reflect.Type) *plan {
    p := new(plan)

    // 非 struct 的 IN 没有字段：整个 body 就是值（由调用方的整块解码处理）。
    if t.Kind() != reflect.Struct {
        return p
    }

    for i := 0; i < t.NumField(); i++ {
        sf := t.Field(i)
        if sf.PkgPath != "" { // 未导出字段
            continue
        }

        // 同一字段的多来源生成多项：按 path → header → 参数 的顺序，后写覆盖先写；
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

            p.fields = append(p.fields, field{index: i, name: sf.Name, source: tag, key: v})
            if tag == bindQuery || tag == bindForm {
                p.hasForm = true
            }
        }
    }

    return p
}

// Bind 把请求里的位置来源写进 in；in 必须是 *IN，body 由调用方处理。
func Bind[IN any](r *http.Request, in *IN) (err error) {
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
    if p.hasForm {
        if err = r.ParseForm(); err != nil {
            return errors.New("bind params: " + err.Error())
        }
    }

    for _, f := range p.fields {
        if err = bindValue(r, v.Field(f.index), f); err != nil {
            return
        }
    }

    return
}
