package mie

import (
	"net/http"
)

// Error 让 handler 返回指定 HTTP 状态码的错误。
// Text 会原样写给客户端。
type Error struct {
	Status int
	Text   string
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.Text }

// NewError 构造 Error；省略 text 时用 http.StatusText(code)。
func NewError(code int, text ...string) *Error {
	if len(text) > 0 {
		return &Error{code, text[0]}
	}
	return &Error{Status: code, Text: http.StatusText(code)}
}

func Status100Continue(text ...string) *Error { return NewError(http.StatusContinue, text...) }
func Status101SwitchingProtocols(text ...string) *Error {
	return NewError(http.StatusSwitchingProtocols, text...)
}
func Status102Processing(text ...string) *Error { return NewError(http.StatusProcessing, text...) }
func Status103EarlyHints(text ...string) *Error { return NewError(http.StatusEarlyHints, text...) }

func Status200OK(text ...string) *Error       { return NewError(http.StatusOK, text...) }
func Status201Created(text ...string) *Error  { return NewError(http.StatusCreated, text...) }
func Status202Accepted(text ...string) *Error { return NewError(http.StatusAccepted, text...) }
func Status203NonAuthoritativeInfo(text ...string) *Error {
	return NewError(http.StatusNonAuthoritativeInfo, text...)
}
func Status204NoContent(text ...string) *Error    { return NewError(http.StatusNoContent, text...) }
func Status205ResetContent(text ...string) *Error { return NewError(http.StatusResetContent, text...) }
func Status206PartialContent(text ...string) *Error {
	return NewError(http.StatusPartialContent, text...)
}
func Status207MultiStatus(text ...string) *Error { return NewError(http.StatusMultiStatus, text...) }
func Status208AlreadyReported(text ...string) *Error {
	return NewError(http.StatusAlreadyReported, text...)
}
func Status226IMUsed(text ...string) *Error { return NewError(http.StatusIMUsed, text...) }

func Status300MultipleChoices(text ...string) *Error {
	return NewError(http.StatusMultipleChoices, text...)
}
func Status301MovedPermanently(text ...string) *Error {
	return NewError(http.StatusMovedPermanently, text...)
}
func Status302Found(text ...string) *Error       { return NewError(http.StatusFound, text...) }
func Status303SeeOther(text ...string) *Error    { return NewError(http.StatusSeeOther, text...) }
func Status304NotModified(text ...string) *Error { return NewError(http.StatusNotModified, text...) }
func Status305UseProxy(text ...string) *Error    { return NewError(http.StatusUseProxy, text...) }
func Status307TemporaryRedirect(text ...string) *Error {
	return NewError(http.StatusTemporaryRedirect, text...)
}
func Status308PermanentRedirect(text ...string) *Error {
	return NewError(http.StatusPermanentRedirect, text...)
}

func Status400BadRequest(text ...string) *Error   { return NewError(http.StatusBadRequest, text...) }
func Status401Unauthorized(text ...string) *Error { return NewError(http.StatusUnauthorized, text...) }
func Status402PaymentRequired(text ...string) *Error {
	return NewError(http.StatusPaymentRequired, text...)
}
func Status403Forbidden(text ...string) *Error { return NewError(http.StatusForbidden, text...) }
func Status404NotFound(text ...string) *Error  { return NewError(http.StatusNotFound, text...) }
func Status405MethodNotAllowed(text ...string) *Error {
	return NewError(http.StatusMethodNotAllowed, text...)
}
func Status406NotAcceptable(text ...string) *Error {
	return NewError(http.StatusNotAcceptable, text...)
}
func Status407ProxyAuthRequired(text ...string) *Error {
	return NewError(http.StatusProxyAuthRequired, text...)
}
func Status408RequestTimeout(text ...string) *Error {
	return NewError(http.StatusRequestTimeout, text...)
}
func Status409Conflict(text ...string) *Error { return NewError(http.StatusConflict, text...) }
func Status410Gone(text ...string) *Error     { return NewError(http.StatusGone, text...) }
func Status411LengthRequired(text ...string) *Error {
	return NewError(http.StatusLengthRequired, text...)
}
func Status412PreconditionFailed(text ...string) *Error {
	return NewError(http.StatusPreconditionFailed, text...)
}

func Status413RequestEntityTooLarge(text ...string) *Error {
	return NewError(http.StatusRequestEntityTooLarge, text...)
}

func Status414RequestURITooLong(text ...string) *Error {
	return NewError(http.StatusRequestURITooLong, text...)
}
func Status415UnsupportedMediaType(text ...string) *Error {
	return NewError(http.StatusUnsupportedMediaType, text...)
}

func Status416RequestedRangeNotSatisfiable(text ...string) *Error {
	return NewError(http.StatusRequestedRangeNotSatisfiable, text...)
}

func Status417ExpectationFailed(text ...string) *Error {
	return NewError(http.StatusExpectationFailed, text...)
}
func Status418Teapot(text ...string) *Error { return NewError(http.StatusTeapot, text...) }
func Status421MisdirectedRequest(text ...string) *Error {
	return NewError(http.StatusMisdirectedRequest, text...)
}
func Status422UnprocessableEntity(text ...string) *Error {
	return NewError(http.StatusUnprocessableEntity, text...)
}
func Status423Locked(text ...string) *Error { return NewError(http.StatusLocked, text...) }
func Status424FailedDependency(text ...string) *Error {
	return NewError(http.StatusFailedDependency, text...)
}
func Status425TooEarly(text ...string) *Error { return NewError(http.StatusTooEarly, text...) }
func Status426UpgradeRequired(text ...string) *Error {
	return NewError(http.StatusUpgradeRequired, text...)
}
func Status428PreconditionRequired(text ...string) *Error {
	return NewError(http.StatusPreconditionRequired, text...)
}
func Status429TooManyRequests(text ...string) *Error {
	return NewError(http.StatusTooManyRequests, text...)
}

func Status431RequestHeaderFieldsTooLarge(text ...string) *Error {
	return NewError(http.StatusRequestHeaderFieldsTooLarge, text...)
}

func Status451UnavailableForLegalReasons(text ...string) *Error {
	return NewError(http.StatusUnavailableForLegalReasons, text...)
}

func Status500InternalServerError(text ...string) *Error {
	return NewError(http.StatusInternalServerError, text...)
}
func Status501NotImplemented(text ...string) *Error {
	return NewError(http.StatusNotImplemented, text...)
}
func Status502BadGateway(text ...string) *Error { return NewError(http.StatusBadGateway, text...) }
func Status503ServiceUnavailable(text ...string) *Error {
	return NewError(http.StatusServiceUnavailable, text...)
}
func Status504GatewayTimeout(text ...string) *Error {
	return NewError(http.StatusGatewayTimeout, text...)
}

func Status505HTTPVersionNotSupported(text ...string) *Error {
	return NewError(http.StatusHTTPVersionNotSupported, text...)
}

func Status506VariantAlsoNegotiates(text ...string) *Error {
	return NewError(http.StatusVariantAlsoNegotiates, text...)
}

func Status507InsufficientStorage(text ...string) *Error {
	return NewError(http.StatusInsufficientStorage, text...)
}
func Status508LoopDetected(text ...string) *Error { return NewError(http.StatusLoopDetected, text...) }
func Status510NotExtended(text ...string) *Error  { return NewError(http.StatusNotExtended, text...) }

func Status511NetworkAuthenticationRequired(text ...string) *Error {
	return NewError(http.StatusNetworkAuthenticationRequired, text...)
}
