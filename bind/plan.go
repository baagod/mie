package bind

import (
    "errors"
    "reflect"

    "github.com/baagod/mie/internal/xsync"
)

// Decode 按显式 tag 把 path/header/参数写进 IN；body 由调用方按 Content-Type 整块解码。
const (
    bindPath   = "path"
    bindHeader = "header"
    bindQuery  = "query"
    bindForm   = "form"
)

// plans 按 IN 的元素类型缓存方案：类型集合编译期固定，条目有界，不淘汰。
var plans xsync.Map[reflect.Type, *plan] // map[reflect.Type]*plan

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

// planFor 取类型的方案；指针按元素类型规划，并发首批请求可能各自扫描一次，结果一致。
func planFor(t reflect.Type) *plan {
    if t.Kind() == reflect.Pointer {
        t = t.Elem()
    }

    if p, ok := plans.Load(t); ok {
        return p
    }

    p := scan(t)
    actual, _ := plans.LoadOrStore(t, p)
    return actual
}

// Validate 报告 IN 的绑定声明是否可用；注册期调用，让写错的字段类型在启动时暴露。
func Validate(t reflect.Type) error {
    return planFor(t).err
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
