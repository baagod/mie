package mie

import (
    "net"
    "net/http"
    "strings"
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

// 只给一个证书文件时必须报错，不能静默降级成 HTTP。
func TestRunRejectsHalfCertPair(t *testing.T) {
    a := New()

    done := make(chan error, 1)
    go func() { done <- a.Run("127.0.0.1:0", "cert.pem") }()

    select {
    case err := <-done:
        if err == nil || !strings.Contains(err.Error(), "cert files") {
            t.Fatalf("Run error = %v, want cert files count error", err)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("Run blocked instead of rejecting the cert file count")
    }
}

// Run 不传证书时必须真的开始服务，而不是立刻返回。
func TestRunServesHTTP(t *testing.T) {
    done := make(chan error, 1)
    go func() { done <- New().Run("127.0.0.1:0") }()

    select {
    case err := <-done:
        t.Fatalf("Run returned early: %v", err)
    case <-time.After(100 * time.Millisecond):
    }
}

// 证书加载失败时 Run 必须关掉已建好的 listener，否则端口一直被占。
func TestRunClosesListenerWhenTLSFails(t *testing.T) {
    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("listen: %v", err)
    }
    addr := ln.Addr().String()
    _ = ln.Close()

    a := New()

    if err := a.Run(addr, "nope.pem", "nope.key"); err == nil {
        t.Fatal("Run returned nil, want cert load error")
    }

    again, err := net.Listen("tcp", addr)
    if err != nil {
        t.Fatalf("listener was not closed after Run failed: %v", err)
    }
    _ = again.Close()
}

// RunListen 在给定 listener 上提供服务，并在开始服务前应用 ServerConfig。
func TestRunListenServesAndAppliesConfig(t *testing.T) {
    cfg := make(chan *http.Server, 1)
    a := New(Config{ServerConfig: func(s *http.Server) {
        s.ReadHeaderTimeout = 0
        cfg <- s
    }})
    a.Handle(http.MethodGet, "/ping", He(func(c *Ctx, _ struct{}) error {
        _, _ = c.W.WriteString("pong")
        return nil
    }))

    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("listen: %v", err)
    }

    done := make(chan error, 1)
    go func() {
        done <- a.RunListen(ln)
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
