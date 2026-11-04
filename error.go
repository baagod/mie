package mux

// Error 让 handler 返回一个带状态码的错误。
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func NewError(status int, msg string) *Error {
	return &Error{Status: status, Msg: msg}
}
