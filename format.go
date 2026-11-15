package mie

import (
    "encoding/json/v2"
    "encoding/xml"
)

// Renderer 声明响应用哪种格式，编码交给框架；格式限于内置的 JSON、XML，自定义媒体类型没有编码器。
type Renderer interface {
    Format() (media string, value any)
}

// format 是内置格式的载体：媒体类型固定，载荷交给该格式的编码器。
type format struct {
    media string
    value any
}

func (f format) Format() (string, any) { return f.media, f.value }

// JSON 强制按 JSON 输出 v，忽略 Accept。
func JSON(v any) Renderer { return format{MIMEAppJSON, v} }

// XML 强制按 XML 输出 v，忽略 Accept。
func XML(v any) Renderer { return format{MIMEAppXML, v} }

// Encoder 把值编码成媒体类型的字节；Content-Type 由注册的格式决定。
type Encoder func(v any) ([]byte, error)

// Encoders 按格式注册编码器，格式名由框架定义；New 会把 nil 字段填成默认实现。
type Encoders struct {
    JSON Encoder
    XML  Encoder
}

// jsonEncode 是内置 JSON 编码器：json.Marshal 带选项参数，签名与 Encoder 不同。
func jsonEncode(v any) ([]byte, error) {
    return json.Marshal(v)
}

// xmlEncode 是内置 XML 编码器：encoding/xml 的 Marshal 只返回 error，签名与 Encoder 不同。
func xmlEncode(v any) ([]byte, error) {
    return xml.Marshal(v)
}
