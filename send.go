package mux

import "encoding/json/v2"

// Renderer 把值写进响应。实现自己负责在写 body 前设置 Content-Type；
// 失败时不得改动响应头，错误交回调用方。
type Renderer interface {
    Render(c *Ctx) error
}

// jsonRenderer 把任意值序列化为 JSON 响应。
type jsonRenderer struct{ v any }

// JSON 返回编码 v 的 Renderer；编码成功后才设置 Content-Type，失败不会污染响应头。
func JSON(v any) Renderer { return jsonRenderer{v: v} }

// Render 先编码，成功后才写头与 body。
func (r jsonRenderer) Render(c *Ctx) error {
    b, err := json.Marshal(r.v)
    if err != nil {
        return err
    }
    c.Type(MIMEApplicationJSON)
    _, err = c.W.Write(b)
    return err
}
