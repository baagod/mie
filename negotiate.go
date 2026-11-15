package mie

import (
    "net/http"
    "strings"
)

// negotiate 按 Accept 的书写顺序选出第一个能提供的媒体类型，q 值不参与排序。
// 能提供的是：值自身的类型（natural），以及 app 配置过的编码器格式；
// Accept 只能选择，不能把值改标成别的类型。
func (c *Ctx) negotiate(natural string) string {
    accept := c.HeaderGet(HeaderAccept)
    if accept == "" || accept == MIMEAny {
        return natural
    }

    base, _, _ := strings.Cut(natural, ";")

    for part := range strings.SplitSeq(accept, ",") {
        media, _, _ := strings.Cut(part, ";")
        switch media = strings.ToLower(strings.TrimSpace(media)); {
        case covers(media, base):
            return natural
        case media == MIMEAppAny:
            return MIMEAppJSON
        case media == MIMEAny:
            return natural
        default:
            if _, ok := c.app.encoder(media); ok {
                return media
            }
        }
    }

    return ""
}

// covers 报告 Accept 项是否覆盖 natural：完全相等，或 type/* 覆盖同族。
func covers(media, natural string) bool {
    if media == natural {
        return true
    }
    return strings.HasSuffix(media, "/*") && strings.HasPrefix(natural, media[:len(media)-1])
}

// valueMedia 是客户端没表态时的响应类型，无匹配时返回 application/json。
func valueMedia(v any) string {
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
