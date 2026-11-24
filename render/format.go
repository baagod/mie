// Package render 提供响应格式声明：把值和格式一起交给框架编码。
package render

const (
	MediaJSON     = "application/json"
	MediaXML      = "application/xml"
	MediaTOML     = "application/toml"
	MediaYAML     = "application/yaml"
	MediaMsgPack  = "application/msgpack"
	MediaProtobuf = "application/x-protobuf"
	MediaCBOR     = "application/cbor"
	MediaBSON     = "application/bson"
)

// Formatted 声明响应用哪种格式，编码交给框架；媒体类型必须是框架配置的媒体表提供的媒体，否则报错。
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
func JSON(v any) Formatted { return formatted{MediaJSON, v} }

// XML 强制按 XML 输出 v，忽略 Accept。
func XML(v any) Formatted { return formatted{MediaXML, v} }
