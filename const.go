package mie

// 协议头名。
const (
    HeaderVary                = "Vary"
    HeaderAccept              = "Accept"
    HeaderXRequestID          = "X-Request-Id"
    HeaderContentType         = "Content-Type"
    HeaderXContentTypeOptions = "X-Content-Type-Options"
)

// MIME 与选项值；text 族带 UTF8 的常量用于响应头，不带的是类型本身。
const (
    MIMEAny                = "*/*"
    MIMEAppAny             = "application/*"
    MIMEXML                = "application/xml"
    MIMEJSON               = "application/json"
    MIMETOML               = "application/toml"
    MIMEYAML               = "application/yaml"
    MIMETextCSS            = "text/css"
    MIMETextCSV            = "text/csv"
    MIMEImagePNG           = "image/png"
    MIMEImageSVG           = "image/svg+xml"
    MIMETextHTML           = "text/html"
    MIMEImageJPEG          = "image/jpeg"
    MIMEImageWebP          = "image/webp"
    MIMETextPlain          = "text/plain"
    MIMEMsgPack            = "application/msgpack"
    MIMEProtobuf           = "application/x-protobuf"
    MIMECBOR               = "application/cbor"
    MIMEBSON               = "application/bson"
    MIMETextHTMLUTF8       = MIMETextHTML + "; charset=utf-8"
    MIMEMultipartForm      = "multipart/form-data"
    MIMETextPlainUTF8      = MIMETextPlain + "; charset=utf-8"
    MIMEOctetStream        = "application/octet-stream"
    MIMETextJavaScript     = "text/javascript"
    MIMETextEventStream    = "text/event-stream"
    MIMEFormUrlencoded     = "application/x-www-form-urlencoded"
    MIMEContentTypeNosniff = "nosniff"
)
