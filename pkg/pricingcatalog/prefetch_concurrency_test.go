package pricingcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPProviderPrefetches939CodesConcurrentlyAndMergesDeterministically(t *testing.T) {
	var batchRequests atomic.Int32
	var activeRequests atomic.Int32
	var maximumActive atomic.Int32
	bothPagesStarted := make(chan struct{})
	secondPageFinished := make(chan struct{})
	completionOrder := make(chan string, 2)
	var startedOnce sync.Once
	var secondOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			writeConcurrentPricingCatalog(w)
			return
		}
		if r.URL.Path != "/integration/pricing-assignments/batch" {
			http.NotFound(w, r)
			return
		}

		batchRequests.Add(1)
		var request struct {
			Codes []string `json:"codes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode batch request: %v", err)
			return
		}
		active := activeRequests.Add(1)
		defer activeRequests.Add(-1)
		for {
			previous := maximumActive.Load()
			if active <= previous || maximumActive.CompareAndSwap(previous, active) {
				break
			}
		}
		if active >= 2 {
			startedOnce.Do(func() { close(bothPagesStarted) })
		}

		page := "first"
		if len(request.Codes) > 0 && request.Codes[0] == "P-0500" {
			page = "second"
		}
		select {
		case <-bothPagesStarted:
		case <-r.Context().Done():
			return
		}
		if page == "first" {
			select {
			case <-secondPageFinished:
			case <-r.Context().Done():
				return
			}
		}
		writeConcurrentAssignmentPage(w, request.Codes)
		completionOrder <- page
		if page == "second" {
			secondOnce.Do(func() { close(secondPageFinished) })
		}
	}))
	defer server.Close()

	codes := make([]string, 939)
	for index := range codes {
		codes[index] = fmt.Sprintf("P-%04d", index)
	}
	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: server.URL, BatchSize: 500, BatchConcurrency: 2, MaxEntries: 1,
	}, server.Client(), time.Now)

	startedAt := time.Now()
	scoped := provider.Prefetch(context.Background(), codes)
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("synthetic 939-Code concurrent prefetch took %s", elapsed)
	}
	if got := batchRequests.Load(); got != 2 {
		t.Fatalf("batch requests = %d, want two bounded pages", got)
	}
	if got := maximumActive.Load(); got != 2 {
		t.Fatalf("maximum concurrent pages = %d, want 2", got)
	}
	if first, second := <-completionOrder, <-completionOrder; first != "second" || second != "first" {
		t.Fatalf("test did not force out-of-order completion: %q then %q", first, second)
	}
	for _, code := range []string{"P-0000", "P-0499", "P-0500", "P-0938"} {
		resolution := scoped.Resolve(context.Background(), code)
		if resolution.MethodID != "air" || decimalText(resolution.MarkupPercent) != "30" {
			t.Fatalf("request-order merge lost %s: %+v", code, resolution)
		}
	}
}

func TestHTTPProviderTerminalPageFailureCancelsSiblingAndRedactsDetails(t *testing.T) {
	var batchStarted atomic.Int32
	bothPagesStarted := make(chan struct{})
	siblingCanceled := make(chan struct{})
	var startedOnce sync.Once
	var canceledOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			writeConcurrentPricingCatalog(w)
			return
		}
		var request struct {
			Codes []string `json:"codes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode batch request: %v", err)
			return
		}
		if batchStarted.Add(1) == 2 {
			startedOnce.Do(func() { close(bothPagesStarted) })
		}
		select {
		case <-bothPagesStarted:
		case <-r.Context().Done():
			return
		}
		if request.Codes[0] == "A" {
			http.Error(w, "https://private.invalid Authorization: Bearer super-secret product A", http.StatusInternalServerError)
			return
		}
		<-r.Context().Done()
		canceledOnce.Do(func() { close(siblingCanceled) })
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: server.URL, BatchSize: 1, BatchConcurrency: 2, Timeout: "2s",
	}, server.Client(), time.Now)
	startedAt := time.Now()
	scoped := provider.Prefetch(context.Background(), []string{"A", "B"})
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("terminal page failure did not cancel promptly: %s", elapsed)
	}
	select {
	case <-siblingCanceled:
	case <-time.After(time.Second):
		t.Fatal("sibling batch request did not observe cancellation")
	}
	for _, code := range []string{"A", "B"} {
		resolution := scoped.Resolve(context.Background(), code)
		if !contains(resolution.Warnings, "pricing_assignment_batch_http_failed") {
			t.Fatalf("typed terminal warning missing for %s: %+v", code, resolution)
		}
		diagnostic := strings.Join(resolution.Warnings, " ")
		for _, forbidden := range []string{"private.invalid", "super-secret", "Bearer", "product A"} {
			if strings.Contains(diagnostic, forbidden) {
				t.Fatalf("diagnostic exposed %q for %s: %q", forbidden, code, diagnostic)
			}
		}
	}
}

func TestHTTPProviderConcurrentPrefetchHonorsCallerCancellation(t *testing.T) {
	var batchStarted atomic.Int32
	bothPagesStarted := make(chan struct{})
	requestCanceled := make(chan struct{}, 2)
	var startedOnce sync.Once

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/integration/catalog" {
			body := `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price"},"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY"}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}
		if batchStarted.Add(1) == 2 {
			startedOnce.Do(func() { close(bothPagesStarted) })
		}
		<-r.Context().Done()
		requestCanceled <- struct{}{}
		return nil, r.Context().Err()
	})}

	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: "https://digitalogic.example", BatchSize: 1, BatchConcurrency: 2, Timeout: "2s",
	}, client, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		provider.Prefetch(ctx, []string{"A", "B"})
		close(done)
	}()
	select {
	case <-bothPagesStarted:
	case <-time.After(time.Second):
		t.Fatal("bounded page workers did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not stop prefetch")
	}
	for index := 0; index < 2; index++ {
		select {
		case <-requestCanceled:
		case <-time.After(time.Second):
			t.Fatal("in-flight request did not observe caller cancellation")
		}
	}
	provider.mu.Lock()
	committed := len(provider.assignments)
	provider.mu.Unlock()
	if committed != 0 {
		t.Fatalf("caller-canceled prefetch committed %d assignments", committed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func writeConcurrentPricingCatalog(w http.ResponseWriter) {
	fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price"},"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY"}]}}`)
}

func writeConcurrentAssignmentPage(w http.ResponseWriter, codes []string) {
	results := make([]map[string]interface{}, 0, len(codes))
	for _, code := range codes {
		results = append(results, map[string]interface{}{
			"code":   code,
			"status": "ok",
			"assignment": map[string]interface{}{
				"code": code, "shipping_method_id": "air", "profit_percent": "30",
				"profit_percent_source": "global_default", "pricing_warnings": []string{},
			},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
		"schema":          "digitalogic.pricing-assignment-batch",
		"requested_count": len(codes), "resolved_count": len(codes), "error_count": 0, "maximum_codes": 500,
		"default_percentage_markup": map[string]interface{}{
			"schema": "digitalogic.default-percentage-markup", "configured": true, "type": "percentage",
			"profit_percent": "30", "source": "global_default", "revision": "rev-30",
		},
		"results": results,
	}})
}
