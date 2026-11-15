package mie

import (
    "github.com/baagod/mie/internal/xtype"
)

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

// handler 把 HandlerFunc 适配成链元素 Handler：绑定输入、调用处理器、渲染输出。
func handler[IN, OUT any](f HandlerFunc[IN, OUT]) Handler {
    noInput := xtype.Eq[IN, struct{}]()

    return func(c *Ctx) error {
        var in IN

        if !noInput {
            if err := c.Bind(&in); err != nil {
                return err
            }
        }

        out, err := f(c, in)
        if err != nil {
            return err
        }

        return c.Send(out)
    }
}
