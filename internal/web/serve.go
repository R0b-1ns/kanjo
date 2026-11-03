package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// ListenLoopback listens on 127.0.0.1 only; there is no way to choose another
// address. Port 0 picks a free port. A busy port is an error.
func ListenLoopback(port int) (net.Listener, error) {
	return net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// NewServer wraps h with bounded timeouts and sizes, and no error log.
func NewServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
}

// Serve answers on ln until ctx ends. It refuses a listener that is not on
// 127.0.0.1, whatever the caller built.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return fmt.Errorf("refusing to serve on %s: only 127.0.0.1 is allowed", ln.Addr())
	}
	srv := NewServer(h)
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(stop)
		close(done)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
