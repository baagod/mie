package mux

const (
	// maxTraceIDLen 限制沿用的客户端 TraceID 长度：超长值不再回写响应头、
	// 也不再进入 panic 日志，避免客户端用一个超长头放大日志与带宽。
	maxTraceIDLen = 128
)

const (
	HeaderContentType = "Content-Type"
	HeaderXRequestID  = "X-Request-Id"
)
