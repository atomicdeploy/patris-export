package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
)

// Request is a single JSON Lines IPC request.
type Request struct {
	ID     interface{}     `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a single JSON Lines IPC response.
type Response struct {
	ID     interface{} `json:"id,omitempty"`
	OK     bool        `json:"ok"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// Handler executes IPC requests and optionally exposes the live event stream.
type Handler interface {
	Call(ctx context.Context, req Request) Response
	Subscribe(ctx context.Context, buffer int) (<-chan map[string]interface{}, func())
}

// Server serves the patris-export IPC JSON Lines protocol on a local transport.
type Server struct {
	path    string
	handler Handler
}

func NewServer(path string, handler Handler) *Server {
	return &Server{
		path:    NormalizePath(path),
		handler: handler,
	}
}

func (s *Server) Path() string {
	return s.path
}

func (s *Server) Serve(ctx context.Context) error {
	if s.handler == nil {
		return errors.New("ipc handler is required")
	}
	ln, err := listenLocal(s.path)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer cleanupLocal(s.path)

	go func() {
		<-ctx.Done()
		ln.Close()
		cleanupLocal(s.path)
	}()

	log.Printf("IPC listening on %s", s.path)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosedListenerError(err) {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var writerMu sync.Mutex
	write := func(v interface{}) {
		writerMu.Lock()
		defer writerMu.Unlock()
		if err := json.NewEncoder(conn).Encode(v); err != nil {
			cancel()
		}
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			write(Response{OK: false, Error: fmt.Sprintf("invalid JSON request: %v", err)})
			continue
		}
		if strings.EqualFold(req.Method, "subscribe") {
			events, unsubscribe := s.handler.Subscribe(connCtx, 64)
			defer unsubscribe()
			write(Response{ID: req.ID, OK: true, Result: map[string]interface{}{"subscribed": true}})
			go func() {
				for {
					select {
					case <-connCtx.Done():
						return
					case event, ok := <-events:
						if !ok {
							return
						}
						write(map[string]interface{}{
							"type":  "event",
							"event": event,
						})
					}
				}
			}()
			continue
		}
		write(s.handler.Call(connCtx, req))
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("IPC client error: %v", err)
	}
}

func isClosedListenerError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "file has already been closed")
}
