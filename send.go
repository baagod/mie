package mux

import "encoding/json/v2"

// Renderer 把一个值写成响应。实现负责在写 body 前设置 Content-Type；
// 失败时不得修改响应头，错误交给调用方返回。
type Renderer interface {
    Render(c *Ctx) error
}

// Send 渲染 r。响应提交由请求收尾（Ctx.finish）统一负责：
// 写 body 时已提交，空 body 时 finish 提交 c.Status 预设的状态。
// 返回的错误应作为 handler 的 error 返回，由 ErrorHandler 与 writeError 统一处理。
func (c *Ctx) Send(r Renderer) error {
    return r.Render(c)
}

type jsonRenderer struct{ v any }

// JSON 把 v 编码成 JSON；marshal 成功后才设置 Content-Type。
func JSON(v any) Renderer { return jsonRenderer{v: v} }

func (r jsonRenderer) Render(c *Ctx) error {
    b, err := json.Marshal(r.v)
    if err != nil {
        return err
    }
    c.Type(MIMEApplicationJSON)
    _, err = c.W.Write(b)
    return err
}
