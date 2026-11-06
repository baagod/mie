package mux

import "errors"

// RecoverError 表示 panic 已经被 Recoverer 处理。
// register 收到它后直接结束请求，不进入 ErrorHandler：panic 与响应错误是两类事件。
var RecoverError = errors.New("mux: recovered panic")

// Error 让 handler 返回一个带状态码的错误。
type Error struct {
    Status int
    Msg    string
}

func (e *Error) Error() string { return e.Msg }

func NewError(code int, msg string) *Error {
    return &Error{Status: code, Msg: msg}
}
