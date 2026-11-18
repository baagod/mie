package mie

import (
    "cmp"
    "net/http"
    "path"
    "reflect"
    "slices"
    "strings"

    "github.com/baagod/mie/internal/bind"
)

// Router 是注册面：前缀、本层中间件，以及最终落到的 ServeMux（由 App 持有）。
// Group 返回的 Router 没有 Run，分组只影响注册，不影响服务生命周期。
type Router struct {
    app         *App
    prefix      string
    middlewares []Handler
    in          reflect.Type // 本层中间件声明的 IN，nil 表示没有
}

// Use 追加中间件，只影响之后注册的路由。
// 中间件是 HandlerFunc[IN, any]：必须调用 c.Next() 推进链；返回 nil 表示不写 body。
// 同层中间件的 IN 必须相同；带 IN 的 Use 之后注册的每条路由（含无输入路由）IN 必须与之一致，否则 panic。
func (r *Router) Use[I any](m HandlerFunc[I, any]) *Router {
    if in := reflect.TypeFor[I](); in != reflect.TypeFor[struct{}]() {
        if r.in != nil && r.in != in {
            panic("mie: middleware input " + in.String() + " conflicts with " + r.in.String())
        }
        r.in = in
    }

    r.middlewares = append(r.middlewares, handler(m))
    return r
}

// Group 返回带前缀的子注册面。中间件取当前层的快照：父层之后再 Use 不影响已创建的子组，
// 子层再 Use 也不影响兄弟。
func (r *Router) Group(prefix string) *Router {
    return &Router{
        app:         r.app,
        prefix:      r.join(prefix),
        middlewares: slices.Clone(r.middlewares),
        in:          r.in,
    }
}

// Get 注册 GET 路由。
func (r *Router) Get[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router {
    return r.Handle(http.MethodGet, pattern, h)
}

// Post 注册 POST 路由。
func (r *Router) Post[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router {
    return r.Handle(http.MethodPost, pattern, h)
}

// Put 注册 PUT 路由。
func (r *Router) Put[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router {
    return r.Handle(http.MethodPut, pattern, h)
}

// Patch 注册 PATCH 路由。
func (r *Router) Patch[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router {
    return r.Handle(http.MethodPatch, pattern, h)
}

// Delete 注册 DELETE 路由。
func (r *Router) Delete[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router {
    return r.Handle(http.MethodDelete, pattern, h)
}

// Handle 校验中间件输入与 IN 的绑定声明，然后把链注册到 ServeMux。
func (r *Router) Handle[IN, OUT any](method, pattern string, h HandlerFunc[IN, OUT]) *Router {
    in := reflect.TypeFor[IN]()
    if r.in != nil && r.in != in {
        panic("mie: " + method + " " + pattern + " input " + in.String() +
            " does not match middleware input " + r.in.String())
    }

    if err := bind.Validate(in); err != nil {
        panic("mie: " + method + " " + pattern + ": " + err.Error())
    }

    chain := slices.Concat(r.middlewares, []Handler{handler(h)})

    r.app.mux.Handle(method+" "+r.join(pattern), http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
        // Ctx 由 ServeHTTP 创建并挂在 context 上；这里只负责跑链。
        c := req.Context().Value(ctxKey{}).(*Ctx)
        c.chain = chain
        c.W.noMatch = nil // 进入路由闭包后，404/405 都算业务响应。

        err := c.Next()
        if err != nil && !c.IsCanceled() {
            err = r.app.config.ErrorHandler(c, err)
        }

        c.finish(err)
    }))

    return r
}

// join 把本层前缀与注册 pattern 合成 ServeMux 的 pattern。
// 空 pattern 就是前缀本身（保留末尾的 "/"，它表示子树）；根上前缀为空，落到 "/"。
// 其余用 path.Join 归一并保证有前导 "/"，它还会吃掉 pattern 末尾的 "/"，补回来。
func (r *Router) join(pattern string) string {
    if pattern == "" {
        return cmp.Or(r.prefix, "/")
    }

    joined := path.Join(r.prefix, "/"+pattern)
    if strings.HasSuffix(pattern, "/") && !strings.HasSuffix(joined, "/") {
        joined += "/"
    }

    return joined
}
