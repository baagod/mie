package mux

import (
    "reflect"
    "sync/atomic"
)

// He 把只返回 error 的处理器适配成 HandlerFunc，OUT 固定为 nil（不写 body）。
func He[IN any](f func(c *Ctx, in IN) error) HandlerFunc[IN, any] {
    return func(c *Ctx, in IN) (any, error) {
        return nil, f(c, in)
    }
}

// endpoint 是注册好的处理器：绑定方案延迟到首次请求才编译，后续请求只按方案填值。
type endpoint[IN, OUT any] struct {
    f       HandlerFunc[IN, OUT]
    noInput bool                     // IN 是 struct{}：不编译方案、不绑定
    comp    atomic.Pointer[bindPlan] // 首次请求编译；失败时 plan.err 非 nil，一并缓存
}

// handler 把 HandlerFunc 适配成链元素 Handler。
func handler[IN, OUT any](f HandlerFunc[IN, OUT]) Handler {
    e := &endpoint[IN, OUT]{
        f:       f,
        noInput: reflect.TypeFor[IN]() == reflect.TypeFor[struct{}](),
    }
    return e.serve
}

// serve 是链元素：首次请求编译方案，之后每个请求只做绑定与调用。
func (e *endpoint[IN, OUT]) serve(c *Ctx) error {
    var in IN

    if !e.noInput {
        plan := e.comp.Load()
        if plan == nil {
            plan = newBindPlan(reflect.TypeFor[IN]())
            e.comp.Store(plan)
        }

        if plan.err != nil {
            return plan.err
        }

        if err := plan.bind(c, reflect.ValueOf(&in).Elem()); err != nil {
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
