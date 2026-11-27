package mie

import "github.com/baagod/mie/render"

// Status499ClientClosedRequest 是客户端在响应完成前断开时记的状态码（nginx 的 499）。
// 由 finish 在请求已取消且未设置状态时补上，随 commit 写出。
const Status499ClientClosedRequest = 499

// 协议头名
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
	MIMEXML                = render.MediaXML
	MIMEJSON               = render.MediaJSON
	MIMETOML               = render.MediaTOML
	MIMEYAML               = render.MediaYAML
	MIMETextCSS            = "text/css"
	MIMETextCSV            = "text/csv"
	MIMEImagePNG           = "image/png"
	MIMEImageSVG           = "image/svg+xml"
	MIMETextHTML           = "text/html"
	MIMEImageJPEG          = "image/jpeg"
	MIMEImageWebP          = "image/webp"
	MIMETextPlain          = "text/plain"
	MIMEMsgPack            = render.MediaMsgPack
	MIMEProtobuf           = render.MediaProtobuf
	MIMECBOR               = render.MediaCBOR
	MIMEBSON               = render.MediaBSON
	MIMETextHTMLUTF8       = MIMETextHTML + "; charset=utf-8"
	MIMEMultipartForm      = "multipart/form-data"
	MIMETextPlainUTF8      = MIMETextPlain + "; charset=utf-8"
	MIMEOctetStream        = "application/octet-stream"
	MIMETextJavaScript     = "text/javascript"
	MIMETextEventStream    = "text/event-stream"
	MIMEFormUrlencoded     = "application/x-www-form-urlencoded"
	MIMEContentTypeNosniff = "nosniff"
)
