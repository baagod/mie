package mie

import "net/http"

// 内置媒体类型的响应头值。每个响应都要设一次 Content-Type，
// 复用这些切片省掉每响应一次 16B 分配。
// 切片全进程共享：写响应头只能整体替换（Set 或 h[键] = 新切片）；
// 就地改元素、或 append(h[键][:0], …)，改的就是这里，会污染之后所有响应。
var (
	mediaJSON      = []string{MIMEJSON}
	mediaPlainUTF8 = []string{MIMETextPlainUTF8}
	mediaXML       = []string{MIMEXML}
	mediaTOML      = []string{MIMETOML}
	mediaYAML      = []string{MIMEYAML}
	mediaMsgPack   = []string{MIMEMsgPack}
	mediaProtobuf  = []string{MIMEProtobuf}
	mediaCBOR      = []string{MIMECBOR}
	mediaBSON      = []string{MIMEBSON}
	mediaHTMLUTF8  = []string{MIMETextHTMLUTF8}
	mediaPlain     = []string{MIMETextPlain}
	mediaNosniff   = []string{MIMEContentTypeNosniff}
)

// canonHeader 读取已规范形式的键，跳过 Header.Get 的键规范化；key 必须是框架里的常量。
func canonHeader(h http.Header, key string) string {
	if values := h[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// mediaHeader 返回可直接写入响应头的媒体值；内置媒体复用同一份切片，其余按需分配。
// 新增内置媒体时在这里补一个 case 和变量，否则只是退回分配，结果仍然正确。
func mediaHeader(media string) []string {
	switch media {
	case MIMEJSON:
		return mediaJSON
	case MIMETextPlainUTF8:
		return mediaPlainUTF8
	case MIMEXML:
		return mediaXML
	case MIMETOML:
		return mediaTOML
	case MIMEYAML:
		return mediaYAML
	case MIMEMsgPack:
		return mediaMsgPack
	case MIMEProtobuf:
		return mediaProtobuf
	case MIMECBOR:
		return mediaCBOR
	case MIMEBSON:
		return mediaBSON
	case MIMETextHTMLUTF8:
		return mediaHTMLUTF8
	case MIMETextPlain:
		return mediaPlain
	}
	return []string{media}
}
