package mux

// Error 让 handler 返回一个带状态码的错误。
type Error struct {
    Status  int
    Message string
}

func (e *Error) Error() string { return e.Message }

func NewError(code int, msg string) *Error {
    return &Error{Status: code, Message: msg}
}
