package mux

import (
    "net/http"
)

// Error 让 handler 返回一个带状态码的错误
type Error struct {
    Status int
    Text   string
}

func (e *Error) Error() string { return e.Text }

func NewError(code int, text ...string) *Error {
    if len(text) > 0 {
        return &Error{code, text[0]}
    }
    return &Error{Status: code, Text: http.StatusText(code)}
}

func Status500InternalServerError(text ...string) *Error {
    return NewError(http.StatusInternalServerError, text...)
}
