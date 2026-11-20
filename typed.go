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

// WithContext 绑定应用上下文工厂，返回以 C 为处理器参数类型的注册面，C 恒为 *T。
// T 必须是直接嵌入 *Ctx 的结构体：嵌入把 Next()、Header() 这些方法提升上来，
// 中间件才能推进链，处理器才能直接用它们。T 不是结构体、没嵌入 *Ctx、或 T 就是 Ctx 本身时注册期 panic。
// 工厂每请求最多执行一次，且在首次用到 C 的位置才执行。
func (r *Router) WithContext[T any](factory func(*Ctx) *T) *TypedRouter[*T] {
    if factory == nil {
        panic("mie: WithContext requires a non-nil factory")
    }

    if t := reflect.TypeFor[T](); t.Kind() == reflect.Struct {
        for f := range t.Fields() {
            if f.Anonymous && f.Type == reflect.TypeFor[*Ctx]() {
                return &TypedRouter[*T]{r: r.snapshot(""), factory: factory}
            }
        }
    }

    panic("mie: WithContext requires C to be a pointer to a struct embedding *Ctx")
}

// Use 追加 typed 中间件，只影响之后注册的路由。
func (r *TypedRouter[C]) Use[IN any](m TypedHandler[C, IN, any]) *TypedRouter[C] {
    r.r.Use(typedHandler(r.factory, m))
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
    return func(c *Ctx, in IN) (OUT, error) {
        if tc, ok := c.store[typedCtxKey[C]{}]; ok {
            return h(tc.(C), in)
        }

        tc := factory(c)
        c.Set(typedCtxKey[C]{}, tc)
        return h(tc, in)
    }
}
