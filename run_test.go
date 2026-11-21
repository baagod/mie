package mie

import (
    "net"
    "testing"
)

// 端口被占用时 Run 必须立刻返回错误，而不是挂住等信号。
func TestRunReturnsListenError(t *testing.T) {
    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("listen: %v", err)
    }
    defer ln.Close()

    a := New()

    if err := a.Run(ln.Addr().String()); err == nil {
        t.Fatal("Run returned nil, want bind error")
    }
}
