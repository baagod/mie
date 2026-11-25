package mie

import (
	"net/http"
	"reflect"
)

// typedCtxKey 是 store 中缓存本次请求 C 的键；与 bindKey[IN]{} 同构，类型即键。
type typedCtxKey[C any] struct{}

// TypedHandler 是 typed 面的处理器形状：第一个参数是应用上下文 C，IN 由请求绑定，OUT 渲染为响应。
// 与 HandlerFunc 只差第一个参数由 *Ctx 换成 C，这两种处理器不能互相替换。
type TypedHandler[C, IN, OUT any] func(C, IN) (OUT, error)

// TypedRouter 是带应用上下文的注册面：处理器的第一个参数是 C，而不是 *Ctx。
// 它是 Router 的快照，Use 与 Group 只影响自身。
type TypedRouter[C any] struct {
	r       *Router
	factory func(*Ctx) C
}

// mieCtxCarrier 判定 C 是否指针嵌入了 *Ctx：值嵌入时方法只提升给 *C，值 C 拿不到 mieCtx。
type mieCtxCarrier interface{ mieCtx() *Ctx }

func (c *Ctx) mieCtx() *Ctx { return c }

// WithContext 为每个请求用 factory 构造上下文 C，处理器接收 *C。
// C 必须嵌入 *Ctx 结构体。
func (r *Router) WithContext[C mieCtxCarrier](factory func(*Ctx) *C) *TypedRouter[*C] {
	if factory == nil || reflect.TypeFor[C]().Kind() != reflect.Struct {
		panic("mie: WithContext requires a non-nil factory and a struct context")
	}
	return &TypedRouter[*C]{r: r.snapshot(""), factory: func(c *Ctx) *C {
		if tc := factory(c); tc != nil && (*tc).mieCtx() == c {
			return tc
		}
		panic("mie: WithContext factory returned nil or a *C with wrong *Ctx")
	}}
}

// Use 追加中间件，只影响之后注册的路由。
// 两种形状：func(*Ctx, IN) 直接进链，不构造 C；func(P, IN) 在本位置构造 C 后调用。
// 其余形状在注册期 panic。
func (r *TypedRouter[C]) Use[P, IN any](m func(P, IN) (any, error)) *TypedRouter[C] {
	switch h := any(m).(type) {
	case func(*Ctx, IN) (any, error):
		r.r.Use(h)
	case func(C, IN) (any, error):
		r.r.Use(typedHandler(r.factory, h))
	default:
		panic("mie: Use: want a middleware on *Ctx or " + reflect.TypeFor[C]().String())
	}
	return r
}

// Group 返回带前缀的子注册面，工厂跟随。
func (r *TypedRouter[C]) Group(prefix string) *TypedRouter[C] {
	return &TypedRouter[C]{r: r.r.Group(prefix), factory: r.factory}
}

// Get 注册 GET 路由
func (r *TypedRouter[C]) Get[IN, OUT any](pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	return r.Handle(http.MethodGet, pattern, h)
}

// Post 注册 POST 路由
func (r *TypedRouter[C]) Post[IN, OUT any](pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	return r.Handle(http.MethodPost, pattern, h)
}

// Put 注册 PUT 路由
func (r *TypedRouter[C]) Put[IN, OUT any](pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	return r.Handle(http.MethodPut, pattern, h)
}

// Patch 注册 PATCH 路由
func (r *TypedRouter[C]) Patch[IN, OUT any](pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	return r.Handle(http.MethodPatch, pattern, h)
}

// Delete 注册 DELETE 路由
func (r *TypedRouter[C]) Delete[IN, OUT any](pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	return r.Handle(http.MethodDelete, pattern, h)
}

// Handle 注册任意方法：五个动词之外的（PROPFIND 这类）走这里
func (r *TypedRouter[C]) Handle[IN, OUT any](method, pattern string, h TypedHandler[C, IN, OUT]) *TypedRouter[C] {
	r.r.Handle(method, pattern, typedHandler(r.factory, h))
	return r
}

// typedHandler 把应用签名的处理器包成普通处理器：C 由工厂构造并缓存，IN 用绑定好的那份。
// 缓存键是 typedCtxKey[C]：C 恒为指针，整条链共享同一对象。
func typedHandler[C any, IN, OUT any](factory func(*Ctx) C, h TypedHandler[C, IN, OUT]) HandlerFunc[IN, OUT] {
	if h == nil {
		panic("mie: TypedHandler must not be nil")
	}
	return func(c *Ctx, in IN) (OUT, error) {
		if tc, ok := c.store[typedCtxKey[C]{}]; ok {
			return h(tc.(C), in)
		}
		tc := factory(c)
		c.Set(typedCtxKey[C]{}, tc)
		return h(tc, in)
	}
}
