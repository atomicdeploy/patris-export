package updateout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

func TestHTTPAttemptTimingFreshAndReusedTLSConnection(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	client := server.Client()
	defer client.CloseIdleConnections()
	for attempt := 0; attempt < 2; attempt++ {
		result, err := sendHTTPAttempt(context.Background(), client, time.Second,
			Config{Method: http.MethodGet, URL: server.URL}, Event{}, nil, nil, "application/json", "", DeliveryResult{Attempts: attempt + 1})
		if err != nil {
			t.Fatal(err)
		}
		trace := result.HTTPTrace
		if trace == nil || trace.ConnectionReused == nil || *trace.ConnectionReused != (attempt == 1) {
			t.Fatalf("attempt %d connection reuse unavailable or incorrect: %+v", attempt, trace)
		}
		if trace.RequestWrittenMS == nil || trace.FirstByteMS == nil || trace.ResponseWaitMS == nil || trace.BodyReadMS == nil || trace.GotConnMS == nil {
			t.Fatalf("attempt %d incomplete timing: %+v", attempt, trace)
		}
		if trace.TotalMS < *trace.FirstByteMS || *trace.BodyReadMS < 0 || *trace.ResponseWaitMS < 0 {
			t.Fatalf("attempt %d invalid timing order: %+v", attempt, trace)
		}
		if attempt == 0 && (trace.TLSStartMS == nil || trace.TLSDoneMS == nil || trace.ConnectStartMS == nil || trace.ConnectDoneMS == nil) {
			t.Fatalf("fresh TLS connection has no bootstrap timing: %+v", trace)
		}
		if attempt == 1 && (trace.TLSStartMS != nil || trace.TLSDoneMS != nil || trace.ConnectStartMS != nil || trace.ConnectDoneMS != nil) {
			t.Fatalf("reused connection inherited previous attempt timing: %+v", trace)
		}
	}
}

func TestHTTPAttemptTimingSnapshotIgnoresLateCallbacks(t *testing.T) {
	trace := newHTTPAttemptTrace()
	callbacks := httptrace.ContextClientTrace(trace.context(context.Background()))
	callbacks.WroteRequest(httptrace.WroteRequestInfo{})
	snapshot := trace.snapshot(nil)
	callbacks.GotFirstResponseByte()
	if snapshot.FirstByteMS != nil || snapshot.ResponseWaitMS != nil || snapshot.BodyReadMS != nil {
		t.Fatalf("late callback changed completed snapshot: %+v", snapshot)
	}
	if trace.snapshot(nil).ResponseWaitMS == nil {
		t.Fatal("subsequent snapshot did not observe completed response wait")
	}
}
