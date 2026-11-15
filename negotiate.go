package mie

import (
    "net/http"
    "strings"

    "github.com/baagod/mie/internal/xtype"
)

// negotiate 按 Accept 的书写顺序选出第一个能提供的媒体类型，q 值不参与排序。
// 一个值只能产出一种类型：JSON、string 的 text/plain、[]byte 的嗅探结果。
// Accept 只能接受或拒绝它，不能把它改标成别的类型。
func negotiate(accept string, v any) string {
    natural := defaultMedia(v)
    if accept == "" || accept == MIMEAny {
        return natural
    }

    base, _, _ := strings.Cut(natural, ";")
    b := xtype.Is[string](v) || xtype.Is[[]byte](v)

    for part := range strings.SplitSeq(accept, ",") {
        media, _, _ := strings.Cut(part, ";")
        switch media = strings.TrimSpace(media); {
        case b && covers(media, base):
            return natural
        case strings.EqualFold(media, MIMEAppJSON) || strings.EqualFold(media, MIMEAppAny):
            return MIMEAppJSON
        case media == MIMEAny:
            return natural
        }
    }

    return ""
}

// covers 报告 Accept 项是否覆盖 natural：完全相等，或 type/* 覆盖同族。
func covers(media, natural string) bool {
    if strings.EqualFold(media, natural) {
        return true
    }
    return strings.HasSuffix(media, "/*") && strings.HasPrefix(natural, strings.ToLower(media[:len(media)-1]))
}

// defaultMedia 是客户端没表态时的响应类型。
func defaultMedia(v any) string {
    switch v := v.(type) {
    case string:
        return MIMETextPlainUTF8
    case []byte:
        // 头在写 body 前已提交（write.go 的 commit），net/http 不会自动嗅探，必须自己来。
        return http.DetectContentType(v)
    default:
        return MIMEAppJSON
    }
}
