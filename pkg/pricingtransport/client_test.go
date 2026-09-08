package pricingtransport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https"), Secret: "test-secret", SourceID: "source", SourceDataset: "kala", OwnerToken: "test-token", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client.dialer.TLSClientConfig = &tls.Config{RootCAs: pool}
	t.Cleanup(func() { _ = client.Close() })
	return client, server
}

func TestConnectionReuseExactEnvelopeAndAuthentication(t *testing.T) {
	var connections atomic.Int32
	envelope := "{\n  \"event_id\": \"exact<>&\", \"price\": 1.00\n}"
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Patris-Product-Sync-Secret") != "test-secret" || r.Header.Get("X-Patris-Source-ID") != "source" || r.Header.Get("X-Patris-Source-Dataset") != "kala" {
			t.Error("authentication/source headers missing")
			http.Error(w, "invalid", 401)
			return
		}
		conn, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		for {
			var request struct {
				ID, Command string
				Data        struct {
					JSON string `json:"json"`
				}
			}
			if conn.ReadJSON(&request) != nil {
				return
			}
			if request.Command == receiveCommand && request.Data.JSON != envelope {
				t.Error("envelope bytes changed")
			}
			_ = conn.WriteJSON(map[string]any{"id": request.ID, "event": "response", "command": request.Command, "success": true, "data": map[string]string{"status": "ok"}})
		}
	})
	if _, err := client.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Receive(context.Background(), []byte(envelope)); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 1 {
		t.Fatalf("connections=%d", connections.Load())
	}
}

func TestCancelledWriteClosesConnectionWithoutRetry(t *testing.T) {
	var commands atomic.Int32
	read := make(chan struct{})
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		commands.Add(1)
		close(read)
		_, _, _ = conn.ReadMessage() // waits for cancellation to close connection
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-read; cancel() }()
	_, err := client.Receive(ctx, []byte(`{"event_id":"one"}`))
	var safe *Error
	if !errors.As(err, &safe) || !safe.OutcomeUnknown {
		t.Fatalf("expected ambiguous write, got %v", err)
	}
	if commands.Load() != 1 {
		t.Fatalf("commands=%d", commands.Load())
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn != nil {
		t.Fatal("cancelled connection retained")
	}
}

func TestCorrelationMismatchIsAmbiguousAndDiscardsConnection(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if conn.ReadJSON(&request) != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"id": "wrong", "event": "response", "command": receiveCommand, "success": true, "data": json.RawMessage(`{}`)})
	})
	_, err := client.Receive(context.Background(), []byte(`{}`))
	var safe *Error
	if !errors.As(err, &safe) || safe.Code != "response_mismatch" || !safe.OutcomeUnknown {
		t.Fatalf("unexpected error: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn != nil {
		t.Fatal("mismatched connection retained")
	}
}

func TestRejectsPlaintextAndUntrustedTLS(t *testing.T) {
	if _, err := New(Config{URL: "ws://localhost"}); err == nil {
		t.Fatal("plaintext accepted")
	}
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted connection reached HTTP handler") })
	client.dialer.TLSClientConfig = nil
	if _, err := client.Catalog(context.Background()); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
}

func TestIdleCatalogReconnectsOnce(t *testing.T) {
	var connections atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		var request struct{ ID, Command string }
		if conn.ReadJSON(&request) != nil {
			return
		}
		if request.Command != catalogCommand {
			t.Error("unexpected source write")
		}
		_ = conn.WriteJSON(map[string]any{"id": request.ID, "event": "response", "command": request.Command, "success": true, "data": map[string]any{}})
	})
	if _, err := client.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatalf("connections=%d", connections.Load())
	}
}

func TestLargeReceiveActuallyUsesContinuationFrames(t *testing.T) {
	envelope := []byte(`{"data":"` + strings.Repeat("x", 32768) + `"}`)
	frames := make(chan int, 1)
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var message []byte
		count := 0
		for {
			var header [2]byte
			if _, err := io.ReadFull(conn.UnderlyingConn(), header[:]); err != nil {
				return
			}
			opcode := header[0] & 15
			if (count == 0 && opcode != 1) || (count > 0 && opcode != 0) || header[1]&128 == 0 {
				t.Error("invalid masked continuation sequence")
				return
			}
			size := uint64(header[1] & 127)
			if size == 126 {
				var n uint16
				if binary.Read(conn.UnderlyingConn(), binary.BigEndian, &n) != nil {
					return
				}
				size = uint64(n)
			}
			if size == 127 {
				if binary.Read(conn.UnderlyingConn(), binary.BigEndian, &size) != nil {
					return
				}
			}
			if size > maxFrameBytes {
				t.Error("oversized frame")
				return
			}
			var mask [4]byte
			if _, err := io.ReadFull(conn.UnderlyingConn(), mask[:]); err != nil {
				return
			}
			body := make([]byte, int(size))
			if _, err := io.ReadFull(conn.UnderlyingConn(), body); err != nil {
				return
			}
			for i := range body {
				body[i] ^= mask[i%4]
			}
			message = append(message, body...)
			count++
			if header[0]&128 != 0 {
				break
			}
		}
		var request struct {
			ID, Command string
			Data        struct {
				JSON string `json:"json"`
			}
		}
		if json.Unmarshal(message, &request) != nil || request.Data.JSON != string(envelope) {
			t.Error("reassembled envelope mismatch")
			return
		}
		frames <- count
		_ = conn.WriteJSON(map[string]any{"id": request.ID, "event": "response", "command": request.Command, "success": true, "data": map[string]any{}})
	})
	if _, err := client.Receive(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if count := <-frames; count < 2 {
		t.Fatalf("expected fragmented client write, frames=%d", count)
	} else {
		t.Logf("default Gorilla client emitted %d masked data frames for %d-byte envelope", count, len(envelope))
	}
}
