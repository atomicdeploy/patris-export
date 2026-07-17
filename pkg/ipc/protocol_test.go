package ipc

import (
	"context"
	"net"
	"testing"
	"time"
)

type testHandler struct{}

func (testHandler) Call(context.Context, Request) Response {
	return Response{OK: true}
}

func (testHandler) Subscribe(context.Context, int) (<-chan map[string]interface{}, func()) {
	ch := make(chan map[string]interface{})
	return ch, func() { close(ch) }
}

func TestHandleConnStopsWhenServerContextIsCanceled(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	server := NewServer("test", testHandler{})
	go func() {
		server.handleConn(ctx, serverConn)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection handler remained blocked after server context cancellation")
	}
}
