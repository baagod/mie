package mie

import "reflect"

// Handler 是框架内部执行的处理器形状：链元素与中间件。
type Handler func(c *Ctx) error

// HandlerFunc 是注册口使用的处理器形状：IN 由请求绑定，OUT 渲染为响应。
type HandlerFunc[IN, OUT any] func(c *Ctx, in IN) (OUT, error)

// He 把只返回 error 的处理器适配成 HandlerFunc，OUT 固定为 nil（不写 body）。
func He[IN any](f func(c *Ctx, in IN) error) HandlerFunc[IN, any] {
    return func(c *Ctx, in IN) (any, error) {
        return nil, f(c, in)
    }
}

// endpoint 是注册好的处理器；IN 的绑定由 Ctx.Bind 负责。
type endpoint[IN, OUT any] struct {
    f       HandlerFunc[IN, OUT]
    noInput bool // IN 是 struct{}：不绑定
}

// handler 把 HandlerFunc 适配成链元素 Handler。
func handler[IN, OUT any](f HandlerFunc[IN, OUT]) Handler {
    e := &endpoint[IN, OUT]{
        f:       f,
        noInput: reflect.TypeFor[IN]() == reflect.TypeFor[struct{}](),
    }
    return e.serve
}

// serve 是链元素：绑定输入后调用处理器。
func (e *endpoint[IN, OUT]) serve(c *Ctx) error {
    var in IN

    if !e.noInput {
        if err := c.Bind(&in); err != nil {
            return err
        }
    }

    out, err := e.f(c, in)
    if err != nil {
        return err
    }

    if any(out) == nil {
        return nil
    }

    return c.Send(JSON(out))
}
