package bind

import (
    "errors"
    "github.com/baagod/mie/internal/xsync"
    "reflect"
)

const (
    bindPath   = "path"
    bindHeader = "header"
    bindQuery  = "query"
    bindForm   = "form"
    bindCookie = "cookie"
)

// plans 按 IN 的元素类型缓存方案：类型集合编译期固定，条目有界，不淘汰。
var plans xsync.Map[reflect.Type, *plan] // map[reflect.Type]*plan

// posTags 是位置来源的 tag 名，顺序即绑定顺序，后写覆盖先写，信任等级从低到高：
// form（表单）< query（URL 参数）< cookie（HttpOnly 会话 cookie 不能被诱导链接的 ?sid= 顶掉）< header（网关注入的身份头、显式 Authorization）< path（路由参数）。
var posTags = [...]string{bindForm, bindQuery, bindCookie, bindHeader, bindPath}

// bindField 是 struct IN 的一个取值项；同一字段声明多个来源时会有多项。
type bindField struct {
    index  int    // 字段下标
    name   string // 仅用于错误文案
    source string // 来自哪个 tag
    key    string // tag 值
}

// plan 是 IN 类型的绑定方案，存入后不再修改。
type plan struct {
    forms, queries, cookies, headers, paths []bindField
    err                                     error
}

func (p *plan) add(f bindField) {
    switch f.source {
    case bindForm:
        p.forms = append(p.forms, f)
    case bindQuery:
        p.queries = append(p.queries, f)
    case bindCookie:
        p.cookies = append(p.cookies, f)
    case bindHeader:
        p.headers = append(p.headers, f)
    case bindPath:
        p.paths = append(p.paths, f)
    }
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

// scan 检查 IN 的字段声明并生成方案；非法声明记入 err。
func scan(t reflect.Type) *plan {
    p := new(plan)

    // 非 struct 的 IN 没有字段：整个 body 就是值（由调用方的整块解码处理）。
    if t.Kind() != reflect.Struct {
        return p
    }

    for i := 0; i < t.NumField(); i++ {
        sf := t.Field(i)
        if !sf.IsExported() { // 未导出字段
            continue
        }

        // 同一字段的多来源生成多项：按 form → query → cookie → header → path 的顺序，后写覆盖先写。
        for _, tag := range posTags {
            v, _ := sf.Tag.Lookup(tag)
            if v == "" {
                continue
            }

            if fileKind(sf.Type) && tag != bindForm {
                // 文件只从 multipart 的 form part 来；其它 tag 忽略，让同一个 IN 能复用到多种来源。
                continue
            }

            if !fileKind(sf.Type) && !scalarKind(sf.Type) {
                p.err = fieldDeclError(t, sf, "unsupported binding type "+sf.Type.String())
                return p
            }

            p.add(bindField{index: i, name: sf.Name, source: tag, key: v})
        }
    }

    return p
}

// fieldDeclError 标注 IN 字段的声明错误，注册期暴露。
func fieldDeclError(t reflect.Type, sf reflect.StructField, msg string) error {
    return errors.New("field " + t.String() + "." + sf.Name + ": " + msg)
}
