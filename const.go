package mie

// 协议头名。
const (
    HeaderContentType         = "Content-Type"
    HeaderXRequestID          = "X-Request-Id"
    HeaderXContentTypeOptions = "X-Content-Type-Options"
)

// 框架自行选择类型时使用的 MIME 与选项值。
const (
    MIMETextPlainUTF8         = "text/plain; charset=utf-8"
    MIMEAppJSON               = "application/json"
    MIMEAppFormUrlencoded     = "application/x-www-form-urlencoded"
    ContentTypeOptionsNosniff = "nosniff"
)
