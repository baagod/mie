package mie

// Formatted 声明响应用哪种格式，编码交给框架；媒体类型必须是 Config.Codecs 提供的媒体，否则报错。
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
func JSON(v any) Formatted { return formatted{MIMEJSON, v} }

// XML 强制按 XML 输出 v，忽略 Accept。
func XML(v any) Formatted { return formatted{MIMEXML, v} }
