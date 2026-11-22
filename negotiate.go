package mie

import (
	"net/http"
	"strings"
)

// negotiate 按 Accept 的书写顺序选出第一个能产出的媒体类型，q 值不参与排序。
// 能产出的是：string/[]byte 的天然类型（ownMedia），以及 offers 里已注册格式的媒体类型；
// Accept 只能选择，不能把值改标成别的类型。ownMedia 为空表示值没有天然类型，完全由 offers 决定。
func negotiate(accept, ownMedia string, offers []string) string {
	fallback := ownMedia
	if fallback == "" && len(offers) > 0 {
		fallback = offers[0]
	}

	if accept == "" || accept == MIMEAny {
		return fallback
	}

	base, _, _ := strings.Cut(ownMedia, ";")

	for part := range strings.SplitSeq(accept, ",") {
		media := baseMedia(part)

		if ownMedia != "" && covers(base, media) {
			return ownMedia
		}

		for _, o := range offers {
			if covers(o, media) {
				return o
			}
		}
	}

	return ""
}

// baseMedia 取媒体类型本身：去掉 ; 后的参数，忽略大小写与首尾空白。
func baseMedia(media string) string {
	base, _, _ := strings.Cut(media, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// covers 报告 Accept 项 media 是否可由 base 满足：完全相等、*/*、type/* 同族，
// 或同 top-level 的结构化后缀（application/vnd.api+json 可由 application/json 满足）。
func covers(base, media string) bool {
	if base == "" || media == "" {
		return false
	}

	if media == MIMEAny || media == base {
		return true
	}

	if strings.HasSuffix(media, "/*") && strings.HasPrefix(base, media[:len(media)-1]) {
		return true
	}

	major, subtype, ok := strings.Cut(base, "/")
	return ok && strings.HasPrefix(media, major+"/") && strings.HasSuffix(media, "+"+subtype)
}

// mediaOf 是值的天然媒体类型：string 是 text/plain，[]byte 靠嗅探；
// 其余类型没有天然类型，返回空串交给 offers（Codecs.Order）决定。
func mediaOf(v any) string {
	switch v := v.(type) {
	case string:
		return MIMETextPlainUTF8
	case []byte:
		// 头在写 body 前已提交（write.go 的 commit），net/http 不会自动嗅探，必须自己来。
		return http.DetectContentType(v)
	default:
		return ""
	}
}
