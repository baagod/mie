package mie

import (
    "encoding/json/v2"
    "encoding/xml"
    "maps"
    "slices"
)

// Formatted 声明响应用哪种格式，编码交给框架；自定义媒体类型要先在 Config.Formats.Encoders 注册编码器。
type Formatted interface {
    Format() (media string, value any)
}

// formatted 是内置格式的载体：媒体类型固定，载荷交给该格式的编码器。
type formatted struct {
    media string
    value any
}

func (f formatted) Format() (string, any) { return f.media, f.value }

// JSON 强制按 JSON 输出 v，忽略 Accept。
func JSON(v any) Formatted { return formatted{MIMEAppJSON, v} }

// XML 强制按 XML 输出 v，忽略 Accept。
func XML(v any) Formatted { return formatted{MIMEAppXML, v} }

// Encoder 把值编码成媒体类型的字节；Content-Type 由格式的媒体类型决定。
type Encoder func(v any) ([]byte, error)

// Encoders 是媒体类型到编码器的映射：覆盖内置实现改同名键，新的键就是新增格式。
type Encoders map[string]Encoder

// builtinFormat 是内置格式项：媒体类型与默认编码器。
type builtinFormat struct {
    media string
    enc   Encoder
}

// builtinFormats 是内置格式，顺序即默认优先级。
var builtinFormats = [...]builtinFormat{
    {MIMEAppJSON, jsonEncode},
    {MIMEAppXML, xml.Marshal},
}

// Formats 声明能提供哪些格式及其优先级。
type Formats struct {
    Encoders Encoders

    // Order 是 Accept 通配时的优先级：列出的媒体按此顺序排在前，其余按内置默认序
    // （JSON、XML、媒体名字典序）追加。引用未注册或重复的媒体时 New 会 panic。
    Order []string

    parsed []string // 由 New 填充的有序媒体名，运行时只读它。
}

// resolve 原地归一化并合并内置格式：Encoders 变成归一化后的编码器表，parsed 变成有序媒体名。
// Order 列出的在前，其余按内置默认序（JSON、XML、媒体名字典序）追加。编码器为 nil、媒体名为空、
// Order 引用未注册或重复的媒体、Encoders 归一化后撞同一媒体时 panic，避免静默缺格式或静默错序。
func (f *Formats) resolve() {
    enc := make(Encoders, len(f.Encoders)+len(builtinFormats))
    for media, fn := range f.Encoders {
        if media = baseMedia(media); media == "" {
            panic("mie: Encoders lists empty media")
        }
        if fn == nil {
            panic("mie: Encoders lists " + media + " with nil encoder")
        }
        if _, dup := enc[media]; dup {
            panic("mie: Encoders lists " + media + " twice")
        }
        enc[media] = fn
    }

    var builtins []string
    order := make([]string, 0, len(enc))

    for _, bf := range builtinFormats {
        builtins = append(builtins, bf.media)
        if _, ok := enc[bf.media]; !ok {
            enc[bf.media] = bf.enc
        }
    }

    for _, media := range f.Order {
        if media = baseMedia(media); slices.Contains(order, media) {
            panic("mie: Order lists " + media + " twice")
        }
        if _, ok := enc[media]; !ok {
            panic("mie: Order lists unregistered media " + media)
        }
        order = append(order, media)
    }

    for _, media := range slices.Concat(builtins, slices.Sorted(maps.Keys(enc))) {
        if !slices.Contains(order, media) {
            order = append(order, media)
        }
    }

    f.Encoders = enc
    f.parsed = order
}

// jsonEncode 是内置 JSON 编码器：json.Marshal 带选项参数，签名与 Encoder 不同。
func jsonEncode(v any) ([]byte, error) {
    return json.Marshal(v)
}
