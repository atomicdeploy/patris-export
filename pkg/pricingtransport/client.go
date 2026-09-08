// Package pricingtransport provides a serialized authenticated pricing command
// connection. Only an idle connection's failed catalog read may be retried;
// receive commands are never retried, including after an ambiguous write.
package pricingtransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const Subprotocol = "digitalogic.pricing.commands.v1"
const MaxEnvelopeBytes = 8 << 20
const maxFrameBytes = 6*MaxEnvelopeBytes + 1024 // JSON string escaping can expand each byte sixfold.
const maxResponseBytes = 16 << 20

const catalogCommand = "digitalogic_get_integration_catalog"
const receiveCommand = "digitalogic_receive_patris_product_sync"

type Config struct {
	URL, Secret, SourceID, SourceDataset, OwnerToken string
	Timeout                                          time.Duration
}

// Error excludes remote messages, transport errors, endpoint and credentials.
// OutcomeUnknown means a receive command may have reached the receiver; the
// caller must reconcile its exact event receipt before considering redelivery.
type Error struct {
	Code           string
	OutcomeUnknown bool
	retryRead      bool
}

func (e *Error) Error() string { return "pricing command: " + e.Code }

// SafeTransportFailure allows delivery diagnostics to preserve bounded codes
// and ambiguity without importing or printing remote/transport error strings.
func (e *Error) SafeTransportFailure() (string, bool) { return safeCode(e.Code), e.OutcomeUnknown }

type Client struct {
	cfg    Config
	dialer websocket.Dialer
	gate   chan struct{}
	mu     sync.Mutex
	conn   *websocket.Conn
	closed bool
	nextID uint64 // guarded by gate
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &Error{Code: "invalid_configuration"}
	}
	for _, value := range []string{cfg.Secret, cfg.SourceID, cfg.SourceDataset, cfg.OwnerToken} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return nil, &Error{Code: "invalid_configuration"}
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.Subprotocols = []string{Subprotocol}
	return &Client{cfg: cfg, dialer: dialer, gate: make(chan struct{}, 1)}, nil
}

func (c *Client) Catalog(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	c.mu.Lock()
	reused := c.conn != nil
	c.mu.Unlock()
	data, err := c.call(ctx, catalogCommand, struct{}{}, false)
	var failure *Error
	if reused && ctx.Err() == nil && errors.As(err, &failure) && failure.retryRead {
		// call discarded the failed connection. A catalog read has no source
		// mutation, so one reconnect is safe; service rejections are not retried.
		return c.call(ctx, catalogCommand, struct{}{}, false)
	}
	return data, err
}

// Receive transmits the exact original envelope as data.json, not a decoded and
// re-serialized envelope. It performs no automatic retries or HTTP fallback.
func (c *Client) Receive(ctx context.Context, envelope []byte) (json.RawMessage, error) {
	if len(envelope) > MaxEnvelopeBytes || !json.Valid(envelope) {
		return nil, &Error{Code: "invalid_envelope"}
	}
	return c.call(ctx, receiveCommand, struct {
		JSON string `json:"json"`
	}{string(envelope)}, true)
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	return nil
}

func (c *Client) discard(conn *websocket.Conn) {
	_ = conn.Close()
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) connection(ctx context.Context) (*websocket.Conn, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &Error{Code: "closed"}
	}
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	headers := http.Header{}
	headers.Set("X-Patris-Product-Sync-Secret", c.cfg.Secret)
	headers.Set("X-Patris-Source-ID", c.cfg.SourceID)
	headers.Set("X-Patris-Source-Dataset", c.cfg.SourceDataset)
	headers.Set("Authorization", "Bearer "+c.cfg.OwnerToken)
	conn, response, err := c.dialer.DialContext(ctx, c.cfg.URL, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, &Error{Code: "connection_failed"}
	}
	if conn.Subprotocol() != Subprotocol {
		_ = conn.Close()
		return nil, &Error{Code: "subprotocol_mismatch"}
	}
	conn.SetReadLimit(maxResponseBytes)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = conn.Close()
		return nil, &Error{Code: "closed"}
	}
	c.conn = conn
	return conn, nil
}

func (c *Client) call(ctx context.Context, command string, data any, write bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Code: "request_cancelled"}
	}
	defer func() { <-c.gate }()
	if ctx.Err() != nil {
		return nil, &Error{Code: "request_cancelled"}
	}
	c.nextID++
	id := strconv.FormatUint(c.nextID, 10)
	frame, err := json.Marshal(struct {
		ID      string `json:"id"`
		Command string `json:"command"`
		Data    any    `json:"data"`
	}{id, command, data})
	if err != nil || len(frame) > maxFrameBytes {
		return nil, &Error{Code: "invalid_request"}
	}
	conn, err := c.connection(ctx)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetWriteDeadline(deadline)
	_ = conn.SetReadDeadline(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.discard(conn); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
	}()
	if ctx.Err() != nil {
		c.discard(conn)
		return nil, &Error{Code: "request_cancelled"}
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		c.discard(conn)
		return nil, &Error{Code: "request_failed", OutcomeUnknown: write, retryRead: true}
	}
	typeID, payload, err := conn.ReadMessage()
	if err != nil {
		c.discard(conn)
		return nil, &Error{Code: "response_failed", OutcomeUnknown: write, retryRead: true}
	}
	var response struct {
		ID      string          `json:"id"`
		Event   string          `json:"event"`
		Command string          `json:"command"`
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if typeID != websocket.TextMessage || json.Unmarshal(payload, &response) != nil || response.ID != id || response.Event != "response" || response.Success == nil || (*response.Success && response.Command != command) || (response.Command != "" && response.Command != command) {
		c.discard(conn)
		return nil, &Error{Code: "response_mismatch", OutcomeUnknown: write}
	}
	if !*response.Success {
		return nil, &Error{Code: safeCode(response.Error.Code)}
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		c.discard(conn)
		return nil, &Error{Code: "response_data_missing", OutcomeUnknown: write}
	}
	return response.Data, nil
}

func safeCode(code string) string {
	if len(code) == 0 || len(code) > 96 {
		return "receiver_rejected"
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "receiver_rejected"
		}
	}
	return code
}
