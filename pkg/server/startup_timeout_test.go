package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

type canonicalProjectionTestSource struct {
	path string
	rows []map[string]interface{}
}

type blockingCanonicalPricingProvider struct {
	started      chan struct{}
	canceled     chan struct{}
	startedOnce  sync.Once
	canceledOnce sync.Once
}

func (provider *blockingCanonicalPricingProvider) Prefetch(ctx context.Context, _ []string) pricingcatalog.Provider {
	provider.startedOnce.Do(func() { close(provider.started) })
	<-ctx.Done()
	provider.canceledOnce.Do(func() { close(provider.canceled) })
	return provider
}

func (*blockingCanonicalPricingProvider) Resolve(context.Context, string) pricingcatalog.Resolution {
	return pricingcatalog.Resolution{}
}

func (source *canonicalProjectionTestSource) GetRecords() ([]map[string]interface{}, error) {
	return source.GetRawRecords()
}

func (source *canonicalProjectionTestSource) GetRawRecords() ([]map[string]interface{}, error) {
	return source.GetRawRecordsContext(context.Background())
}

func (source *canonicalProjectionTestSource) GetRawRecordsContext(ctx context.Context) ([]map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return source.rows, nil
}

func (source *canonicalProjectionTestSource) GetPath() string {
	return source.path
}

func (source *canonicalProjectionTestSource) Close() error {
	return nil
}

func TestStartWatchingDoesNotWaitForInitialDigitalogicProjection(t *testing.T) {
	batchStarted := make(chan struct{})
	releaseBatch := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/integration/catalog":
			writeCanonicalProjectionCatalog(w)
		case "/integration/pricing-assignments/batch":
			startedOnce.Do(func() { close(batchStarted) })
			select {
			case <-r.Context().Done():
			case <-releaseBatch:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	defer releaseOnce.Do(func() { close(releaseBatch) })

	srv := newCanonicalProjectionTestServer(t, remote.URL, "5s", true, true)
	startedAt := time.Now()
	if err := srv.StartWatching(0); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("watch startup waited for initial pricing projection: %s", elapsed)
	}

	select {
	case <-batchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background initial pricing projection did not start")
	}

	appRequest := httptest.NewRequest(http.MethodGet, "/api/app", nil)
	appRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(appRecorder, appRequest)
	if appRecorder.Code != http.StatusOK {
		t.Fatalf("health-independent app endpoint status = %d: %s", appRecorder.Code, appRecorder.Body.String())
	}

	closeStartedAt := time.Now()
	if err := srv.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
	if elapsed := time.Since(closeStartedAt); elapsed > time.Second {
		t.Fatalf("server close did not cancel initial pricing projection: %s", elapsed)
	}
	releaseOnce.Do(func() { close(releaseBatch) })
}

func TestStartWatchingSkipsDisabledInitialProjection(t *testing.T) {
	tests := []struct {
		name        string
		sendEnabled bool
		sendInitial bool
	}{
		{name: "updates disabled", sendEnabled: false, sendInitial: true},
		{name: "initial delivery disabled", sendEnabled: true, sendInitial: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "initial projection should not run", http.StatusInternalServerError)
			}))
			defer remote.Close()

			srv := newCanonicalProjectionTestServer(
				t, remote.URL, "5s", test.sendEnabled, test.sendInitial,
			)
			if err := srv.StartWatching(0); err != nil {
				t.Fatalf("start watcher: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
			if got := requests.Load(); got != 0 {
				t.Fatalf("disabled initial delivery issued %d pricing requests", got)
			}
			if err := srv.Close(); err != nil {
				t.Fatalf("close server: %v", err)
			}
		})
	}
}

func TestInitialProjectionUsesCanonicalRequestDeadline(t *testing.T) {
	srv := newCanonicalProjectionTestServer(
		t, "https://digitalogic.example", "100ms", true, true,
	)
	provider := &blockingCanonicalPricingProvider{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	cfg := srv.Config()
	material, err := json.Marshal(cfg.Canonical.Pricing)
	if err != nil {
		t.Fatalf("marshal pricing config key: %v", err)
	}
	srv.catalogProviderMu.Lock()
	srv.catalogProvider = provider
	srv.catalogProviderKey = string(material)
	srv.catalogProviderMu.Unlock()

	startedAt := time.Now()
	if err := srv.StartWatching(0); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("enabled initial projection did not start")
	}
	select {
	case <-provider.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("background initial projection outlived the canonical request ceiling")
	}
	if elapsed := time.Since(startedAt); elapsed > 1500*time.Millisecond {
		t.Fatalf("background initial projection cancellation took %s, want at most 1.5s", elapsed)
	}
	srv.backgroundWG.Wait()
	if err := srv.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
}

func newCanonicalProjectionTestServer(
	t *testing.T,
	baseURL, timeout string,
	sendEnabled, sendInitial bool,
) *Server {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	cfg := manager.Get()
	cfg.Canonical.Pricing = pricingcatalog.Config{
		Mode: pricingcatalog.ModeDigitalogic,
		Digitalogic: pricingcatalog.DigitalogicConfig{
			BaseURL:   baseURL,
			BatchSize: 1,
			Timeout:   timeout,
		},
	}
	cfg.SendUpdates.Enabled = sendEnabled
	cfg.SendUpdates.Initial = sendInitial
	if err := manager.Replace(cfg); err != nil {
		t.Fatalf("store test config: %v", err)
	}

	sourcePath := filepath.Join(tempDir, "source.json")
	if err := os.WriteFile(sourcePath, []byte(`{}`), 0600); err != nil {
		t.Fatalf("create bootstrap source: %v", err)
	}
	srv, err := NewServerWithOptions(sourcePath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	kalaPath := filepath.Join(tempDir, "kala.db")
	if err := os.WriteFile(kalaPath, nil, 0600); err != nil {
		srv.Close()
		t.Fatalf("create watched kala path: %v", err)
	}
	source := &canonicalProjectionTestSource{
		path: kalaPath,
		rows: []map[string]interface{}{{
			"Code":   "102001011",
			"Name":   "Test module",
			"Serial": "SKU-1",
			"Sharh1": "1 2 3 240",
			"Sharh2": "100 g",
			"ANBAR1": 1,
		}},
	}
	srv.dataSourceMu.Lock()
	bootstrap := srv.dataSource
	srv.dataSource = source
	srv.dbPath = kalaPath
	srv.dataSourceMu.Unlock()
	if err := bootstrap.Close(); err != nil {
		srv.Close()
		t.Fatalf("close bootstrap source: %v", err)
	}
	return srv
}

func writeCanonicalProjectionCatalog(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price"},"selected_warehouses":[],"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY"}]}}`)
}
