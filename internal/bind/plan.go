package bind

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/baagod/mie/internal/xsync"
)

const (
	bindPath   = "path"
	bindHeader = "header"
	bindForm   = "form"
	bindCookie = "cookie"
)

// plans 按 IN 的元素类型缓存方案：类型集合编译期固定，条目有界，不淘汰。
var plans xsync.Map[reflect.Type, *plan]

// field 是 struct IN 的一个取值项；同一字段声明多个来源时会有多项。
type field struct {
	index  int    // 字段下标
	name   string // 仅用于错误文案
	key    string // tag 值
	isFile bool   // 字段承载上传文件
}

// source 是一个来源：顺序即绑定顺序，后写覆盖先写，信任等级从低到高：
// form < query < cookie（HttpOnly 会话 cookie 不能被诱导链接的 ?sid= 顶掉）< header（网关注入的身份头、显式 Authorization）< path（路由参数）。
type source struct {
	tag    string
	fields []field
	bind   func(*source, *http.Request, reflect.Value, *FormCache) error
}

// plan 是 IN 类型的绑定方案，存入后不再修改。
type plan struct {
	sources []*source
	err     error
}

// planFor 取类型的方案；指针按元素类型规划，多层指针是声明错误。并发首批请求可能各自扫描一次，结果一致。
func planFor(t reflect.Type) (p *plan, err error) {
	if t.Kind() == reflect.Pointer {
		if t = t.Elem(); t.Kind() == reflect.Pointer {
			return nil, errors.New("bind: IN must not be a pointer to a pointer")
		}
	}

	if p, _ = plans.Load(t); p == nil {
		p, _ = plans.LoadOrStore(t, scan(t))
	}

	if p.err != nil {
		return nil, p.err
	}

	return
}

// scan 检查 IN 的字段声明并生成方案；非法声明记入 err。
func scan(t reflect.Type) *plan {
	p := &plan{sources: []*source{
		{tag: bindForm, bind: (*source).bindForm},
		{tag: bindCookie, bind: (*source).bindCookie},
		{tag: bindHeader, bind: (*source).bindHeader},
		{tag: bindPath, bind: (*source).bindPath},
	}}

	// 非 struct 的 IN 没有字段：整个 body 就是值（由调用方的整块解码处理）。
	if t.Kind() != reflect.Struct {
		return p
	}

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() { // 未导出字段
			continue
		}

		// 同一字段声明多个来源时生成多项，各自进入对应分组。
		isFile := fileKind(sf.Type)
		for _, s := range p.sources {
			tag := s.tag
			v, _ := sf.Tag.Lookup(tag)
			if v == "" {
				continue
			}

			if isFile && tag != bindForm {
				// 文件只从 multipart 的 form part 来；其它 tag 忽略，让同一个 IN 能复用到多种来源。
				continue
			}

			if !isFile && !scalarKind(sf.Type) {
				p.err = errors.New("field " + t.String() + "." + sf.Name + ": unsupported binding type " + sf.Type.String())
				return p
			}

			s.fields = append(s.fields, field{index: i, name: sf.Name, key: v, isFile: isFile})
		}
	}

	return p
}
