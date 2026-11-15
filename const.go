package mie

import "github.com/baagod/mie/bind"

// 协议头名。
const (
    HeaderContentType         = "Content-Type"
    HeaderXRequestID          = "X-Request-Id"
    HeaderXContentTypeOptions = "X-Content-Type-Options"
    HeaderAccept              = "Accept"
    HeaderVary                = "Vary"
)

// 框架自行选择类型时使用的 MIME 与选项值。
const (
    MIMEAny                = "*/*"
    MIMEAppAny             = "application/*"
    MIMEAppJSON            = bind.MIMEAppJSON
    MIMETextPlainUTF8      = "text/plain; charset=utf-8"
    MIMEAppFormUrlencoded  = bind.MIMEAppFormUrlencoded
    MIMEContentTypeNosniff = "nosniff"
)
