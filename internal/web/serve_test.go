package web_test

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/web"
)

func TestListenLoopbackIsLoopbackOnly(t *testing.T) {
	ln, err := web.ListenLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port == 0 {
		t.Fatalf("listening on %v", addr)
	}
	addrs, _ := net.InterfaceAddrs()
	tried := false
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		tried = true
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ipn.IP.String(), strconv.Itoa(addr.Port)), time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("the page answers on %v, not only on 127.0.0.1", ipn.IP)
		}
	}
	if !tried {
		t.Log("no non-loopback IPv4 address on this machine: only the bind address was checked")
	}
}

func TestListenLoopbackRefusesABusyPort(t *testing.T) {
	first, err := web.ListenLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := web.ListenLoopback(first.Addr().(*net.TCPAddr).Port); err == nil {
		second.Close()
		t.Fatal("a busy port must be an error, not a fallback")
	}
}

func TestServeRefusesANonLoopbackListener(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skip("cannot listen on 0.0.0.0")
	}
	defer ln.Close()
	if err := web.Serve(context.Background(), ln, handler()); err == nil {
		t.Fatal("Serve must refuse a listener that is not on 127.0.0.1")
	}
}

func TestServerLimits(t *testing.T) {
	s := web.NewServer(handler())
	if s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 10*time.Second || s.WriteTimeout != 30*time.Second ||
		s.IdleTimeout != 30*time.Second || s.MaxHeaderBytes != 8<<10 || s.ErrorLog == nil {
		t.Fatalf("server = %+v", s)
	}
}

func TestServeStopsWhenTheContextEnds(t *testing.T) {
	ln, err := web.ListenLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- web.Serve(ctx, ln, web.NewHandler(ln.Addr().(*net.TCPAddr).Port, emptyLoader, book{})) }()
	url := "http://" + ln.Addr().String() + "/api/data"
	var resp *http.Response
	for i := 0; i < 50; i++ {
		if resp, err = http.Get(url); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %v %v", url, resp, err)
	}
	resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil after a clean stop", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

func TestServeLeavesNothingBehindWhenTheListenerFails(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		ln, err := web.ListenLoopback(0)
		if err != nil {
			t.Fatal(err)
		}
		ln.Close() // Serve fails at once with an accept error, the context never ends
		if err := web.Serve(context.Background(), ln, handler()); err == nil {
			t.Fatal("serving on a closed listener must fail")
		}
	}
	for i := 0; i < 50 && runtime.NumGoroutine() > before; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines: %d before, %d after: Serve leaves its shutdown goroutine waiting", before, after)
	}
}
