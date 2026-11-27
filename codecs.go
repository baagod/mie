package mie

import (
	"encoding/json/v2"
	"encoding/xml"
	"slices"
)

var mediaOffers = [8]string{
	MIMEJSON, MIMEXML, MIMETOML, MIMEYAML,
	MIMEMsgPack, MIMEProtobuf, MIMECBOR, MIMEBSON,
}

// Encoder 把值编码成媒体类型的字节；Content-Type 由格式的媒体类型决定。
type Encoder func(v any) ([]byte, error)

// Decoder 把媒体类型的字节解码进 in。
type Decoder func(b []byte, in any) error

// Codec 是一种格式的编解码实现；零值表示不提供该格式。
type Codec struct {
	Encode Encoder
	Decode Decoder
}

// Codecs 声明能提供哪些媒体及其编解码实现；字段不为零值即提供该媒体。
type Codecs struct {
	JSON     Codec
	XML      Codec
	TOML     Codec
	YAML     Codec
	MsgPack  Codec
	Protobuf Codec
	CBOR     Codec
	BSON     Codec

	// Order 是 Accept 通配时的优先级：列出的媒体按此顺序排在前，其余按声明序
	// （JSON、XML、TOML、YAML、MsgPack、Protobuf、CBOR、BSON）追加。
	// 条目必须是裸媒体类型（小写、不带参数），否则 New 会 panic。
	Order []string

	parsed  []string         // 由 New 填充的有序媒体名，运行时只读它。
	entries map[string]Codec // 由 New 填充的媒体表，运行时只读它。
}

// encoder 查媒体对应的编码器；媒体类型去掉参数并忽略大小写。
// 先按原值查一次：协商结果多是表里的裸媒体，省掉 baseMedia 的切分与转小写。
func (c *Codecs) encoder(media string) (Encoder, bool) {
	if codec, ok := c.entries[media]; ok && codec.Encode != nil {
		return codec.Encode, true
	}

	codec, ok := c.entries[baseMedia(media)]
	if !ok || codec.Encode == nil {
		return nil, false
	}

	return codec.Encode, true
}

// decoder 查媒体类型对应的解码器；精确媒体没有解码器时，按结构化后缀找同 top-level 的基类型媒体。
func (c *Codecs) decoder(media string) (Decoder, bool) {
	if codec, ok := c.entries[media]; ok && codec.Decode != nil {
		return codec.Decode, true
	}

	media = baseMedia(media)
	if codec, ok := c.entries[media]; ok && codec.Decode != nil {
		return codec.Decode, true
	}

	for base, codec := range c.entries {
		if codec.Decode != nil && covers(base, media) {
			return codec.Decode, true
		}
	}

	return nil, false
}

// resolve 补齐内置媒体并生成有序媒体名：Order 列出的媒体在前（未实现的跳过），其余按声明序追加。
// Order 引用未知媒体或重复媒体时 panic，避免静默错序。
func (c *Codecs) resolve() {
	if c.JSON.Encode == nil {
		c.JSON.Encode = jsonEncode
	}

	if c.JSON.Decode == nil {
		c.JSON.Decode = jsonDecode
	}

	if c.XML.Encode == nil {
		c.XML.Encode = xml.Marshal
	}

	if c.XML.Decode == nil {
		c.XML.Decode = xml.Unmarshal
	}

	parsed := make([]string, 0, len(mediaOffers))
	c.entries = map[string]Codec{
		MIMEJSON: c.JSON, MIMEXML: c.XML, MIMETOML: c.TOML,
		MIMEYAML: c.YAML, MIMEMsgPack: c.MsgPack, MIMEProtobuf: c.Protobuf,
		MIMECBOR: c.CBOR, MIMEBSON: c.BSON,
	}

	for i, media := range c.Order {
		codec, ok := c.entries[media]

		if !ok {
			panic("mie: Order lists unknown media " + media)
		}

		if slices.Contains(c.Order[:i], media) {
			panic("mie: Order lists " + media + " twice")
		}

		if codec.Encode != nil {
			parsed = append(parsed, media)
		}
	}

	for _, media := range mediaOffers {
		if !slices.Contains(c.Order, media) && c.entries[media].Encode != nil {
			parsed = append(parsed, media)
		}
	}

	c.parsed = parsed
}

// jsonEncode 是内置 JSON 编码器：json.Marshal 有可选的 Options 参数，不能直接赋给 Encoder。
func jsonEncode(v any) ([]byte, error) {
	return json.Marshal(v)
}

// jsonDecode 是内置 JSON 解码器：json.Unmarshal 有可选的 Options 参数，不能直接赋给 Decoder。
func jsonDecode(b []byte, in any) error {
	return json.Unmarshal(b, in)
}
