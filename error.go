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

func Status500InternalServerError(text ...string) *Error {
	return NewError(http.StatusInternalServerError, text...)
}

func Status413RequestEntityTooLarge(text ...string) *Error {
	return NewError(http.StatusRequestEntityTooLarge, text...)
}

func Status400BadRequest(text ...string) *Error {
	return NewError(http.StatusBadRequest, text...)
}

func Status406NotAcceptable(text ...string) *Error {
	return NewError(http.StatusNotAcceptable, text...)
}

func Status415UnsupportedMediaType(text ...string) *Error {
	return NewError(http.StatusUnsupportedMediaType, text...)
}
