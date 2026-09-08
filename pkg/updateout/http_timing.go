package updateout

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

// HTTPAttemptTiming describes only the final attempted HTTP request. Event
// fields are millisecond offsets from attempt start, not additive durations.
// Missing callbacks remain nil (for example DNS/TLS on a reused connection).
// For parallel connection attempts only the first start and completion are
// retained; those offsets do not describe an individual socket's duration.
// ResponseWaitMS includes network and server time, not PHP execution alone.
type HTTPAttemptTiming struct {
	DNSStartMS       *float64 `json:"dns_start_ms,omitempty"`
	DNSDoneMS        *float64 `json:"dns_done_ms,omitempty"`
	ConnectStartMS   *float64 `json:"connect_start_ms,omitempty"`
	ConnectDoneMS    *float64 `json:"connect_done_ms,omitempty"`
	TLSStartMS       *float64 `json:"tls_start_ms,omitempty"`
	TLSDoneMS        *float64 `json:"tls_done_ms,omitempty"`
	GotConnMS        *float64 `json:"got_conn_ms,omitempty"`
	RequestWrittenMS *float64 `json:"request_written_ms,omitempty"`
	FirstByteMS      *float64 `json:"first_byte_ms,omitempty"`
	ResponseWaitMS   *float64 `json:"response_wait_ms,omitempty"`
	BodyReadMS       *float64 `json:"body_read_ms,omitempty"`
	TotalMS          float64  `json:"total_ms"`
	ConnectionReused *bool    `json:"connection_reused,omitempty"`
}

// Trace callbacks can overlap or arrive after RoundTrip returns. Keep bounded
// scalar state under a mutex, then return an independent immutable snapshot.
type httpAttemptTrace struct {
	mu      sync.Mutex
	started time.Time
	timing  HTTPAttemptTiming
}

func newHTTPAttemptTrace() *httpAttemptTrace {
	return &httpAttemptTrace{started: time.Now()}
}

func (t *httpAttemptTrace) mark(field **float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if *field == nil {
		ms := float64(time.Since(t.started)) / float64(time.Millisecond)
		*field = &ms
	}
}

func (t *httpAttemptTrace) context(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { t.mark(&t.timing.DNSStartMS) },
		DNSDone:           func(httptrace.DNSDoneInfo) { t.mark(&t.timing.DNSDoneMS) },
		ConnectStart:      func(string, string) { t.mark(&t.timing.ConnectStartMS) },
		ConnectDone:       func(string, string, error) { t.mark(&t.timing.ConnectDoneMS) },
		TLSHandshakeStart: func() { t.mark(&t.timing.TLSStartMS) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { t.mark(&t.timing.TLSDoneMS) },
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.timing.GotConnMS == nil {
				ms := float64(time.Since(t.started)) / float64(time.Millisecond)
				reused := info.Reused
				t.timing.GotConnMS, t.timing.ConnectionReused = &ms, &reused
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				t.mark(&t.timing.RequestWrittenMS)
			}
		},
		GotFirstResponseByte: func() { t.mark(&t.timing.FirstByteMS) },
	})
}

func (t *httpAttemptTrace) snapshot(bodyStarted *time.Time) *HTTPAttemptTiming {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.timing
	result.TotalMS = float64(time.Since(t.started)) / float64(time.Millisecond)
	if bodyStarted != nil {
		ms := float64(time.Since(*bodyStarted)) / float64(time.Millisecond)
		result.BodyReadMS = &ms
	}
	if result.RequestWrittenMS != nil && result.FirstByteMS != nil && *result.FirstByteMS >= *result.RequestWrittenMS {
		ms := *result.FirstByteMS - *result.RequestWrittenMS
		result.ResponseWaitMS = &ms
	}
	return &result
}
