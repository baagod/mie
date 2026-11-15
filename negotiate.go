package mie

import (
    "net/http"
    "strings"
)

// negotiate 按 Accept 的书写顺序选出第一个能产出的媒体类型，q 值不参与排序。
// 能产出的是：值自身的类型（ownMedia），以及 offers 里的格式；
// Accept 只能选择，不能把值改标成别的类型。
func negotiate(accept, ownMedia string, offers []string) string {
    if accept == "" || accept == MIMEAny {
        return ownMedia
    }

    base, _, _ := strings.Cut(ownMedia, ";")

    for part := range strings.SplitSeq(accept, ",") {
        media, _, _ := strings.Cut(part, ";")
        media = strings.ToLower(strings.TrimSpace(media))

        if covers(media, base) {
            return ownMedia
        }

        for _, o := range offers {
            if covers(media, o) {
                return o
            }
        }
    }

    return ""
}

// covers 报告 Accept 项是否覆盖基类型 base：完全相等、*/*，或 type/* 覆盖同族。
func covers(media, base string) bool {
    return media == MIMEAny || media == base ||
        (strings.HasSuffix(media, "/*") && strings.HasPrefix(base, media[:len(media)-1]))
}

// mediaOf 是客户端没表态时的响应类型，无匹配时返回 application/json。
func mediaOf(v any) string {
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
