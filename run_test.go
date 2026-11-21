package mie

import (
    "net"
    "net/http"
    "testing"
    "time"
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

// RunListen 在给定 listener 上提供服务，并在开始服务前应用 config 回调。
func TestRunListenServesAndAppliesConfig(t *testing.T) {
    a := New()
    a.Handle(http.MethodGet, "/ping", He(func(c *Ctx, _ struct{}) error {
        _, _ = c.W.WriteString("pong")
        return nil
    }))

    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("listen: %v", err)
    }

    cfg := make(chan *http.Server, 1)
    done := make(chan error, 1)
    go func() {
        done <- a.RunListen(ln, func(s *http.Server) {
            s.ReadHeaderTimeout = 0
            cfg <- s
        })
    }()

    select {
    case s := <-cfg:
        if s.ReadHeaderTimeout != 0 {
            t.Fatalf("ReadHeaderTimeout = %v, want 0 set by config", s.ReadHeaderTimeout)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("config callback was not called")
    }

    client := &http.Client{Timeout: 5 * time.Second}
    resp, err := client.Get("http://" + ln.Addr().String() + "/ping")
    if err != nil {
        t.Fatalf("get: %v", err)
    }
    _ = resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
    }

    // 关掉 listener 让 Serve 返回，避开信号（Windows 上发不了 SIGTERM）。
    _ = ln.Close()

    select {
    case err := <-done:
        if err == nil {
            t.Fatal("RunListen returned nil, want accept error")
        }
    case <-time.After(5 * time.Second):
        t.Fatal("RunListen did not return after listener closed")
    }
}
