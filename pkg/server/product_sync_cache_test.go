package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

func TestCanonicalProjectionCacheCoalescesConcurrentBuildsAndClonesHits(t *testing.T) {
	cache := newCanonicalProjectionCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	build := func(context.Context) (recordpipe.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return canonicalProjectionCacheFixture("coalesced"), nil
	}

	const readers = 12
	results := make(chan recordpipe.Result, readers)
	errors := make(chan error, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for index := 0; index < readers; index++ {
		go func() {
			defer wait.Done()
			result, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
			results <- result
			errors <- err
		}()
	}
	<-started
	close(release)
	wait.Wait()
	close(results)
	close(errors)

	if calls.Load() != 1 {
		t.Fatalf("canonical builds=%d, want 1", calls.Load())
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.Contract == nil || result.Contract.Source.Revision != "sha256:coalesced" {
			t.Fatalf("unexpected coalesced projection: %+v", result.Contract)
		}
	}

	first, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
	if err != nil {
		t.Fatal(err)
	}
	first.Rows[0]["name"] = "mutated"
	second, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
	if err != nil {
		t.Fatal(err)
	}
	if second.Rows[0]["name"] != "coalesced" || calls.Load() != 1 {
		t.Fatalf("cache returned aliased data or rebuilt: rows=%v calls=%d", second.Rows, calls.Load())
	}
}

func TestCanonicalProjectionCacheInvalidationFencesInFlightBuild(t *testing.T) {
	cache := newCanonicalProjectionCache()
	oldStarted := make(chan struct{})
	oldCancelled := make(chan struct{})
	var calls atomic.Int32
	build := func(ctx context.Context) (recordpipe.Result, error) {
		switch calls.Add(1) {
		case 1:
			close(oldStarted)
			<-ctx.Done()
			close(oldCancelled)
			return recordpipe.Result{}, ctx.Err()
		case 2:
			return canonicalProjectionCacheFixture("new"), nil
		default:
			return recordpipe.Result{}, errors.New("unexpected extra build")
		}
	}

	firstResult := make(chan recordpipe.Result, 1)
	firstError := make(chan error, 1)
	go func() {
		result, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
		firstResult <- result
		firstError <- err
	}()
	<-oldStarted
	cache.invalidate()

	second, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldCancelled:
	case <-time.After(time.Second):
		t.Fatal("invalidation did not cancel the obsolete build")
	}
	first := <-firstResult
	if err := <-firstError; err != nil {
		t.Fatal(err)
	}
	if first.Contract.Source.Revision != "sha256:new" || second.Contract.Source.Revision != "sha256:new" || calls.Load() != 2 {
		t.Fatalf("in-flight result crossed generation: first=%q second=%q calls=%d",
			first.Contract.Source.Revision, second.Contract.Source.Revision, calls.Load())
	}
}

func TestCanonicalProjectionCacheFailsClosedPastMaxStale(t *testing.T) {
	cache := newCanonicalProjectionCache()
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	var calls atomic.Int32
	expected := errors.New("fresh projection failed")
	build := func(context.Context) (recordpipe.Result, error) {
		if calls.Add(1) == 1 {
			return canonicalProjectionCacheFixture("bounded"), nil
		}
		return recordpipe.Result{}, expected
	}
	maximum := func() time.Duration { return time.Hour }

	if _, err := cache.get(context.Background(), maximum, build); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour + time.Nanosecond)
	if _, err := cache.get(context.Background(), maximum, build); !errors.Is(err, expected) {
		t.Fatalf("expired cache returned stale data or wrong error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expired cache builds=%d, want 2", calls.Load())
	}
}

func TestCanonicalProjectionMaxAgeRequiresExplicitValidDigitalogicBound(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Canonical.Pricing.Mode = "static"
	cfg.Canonical.Pricing.Digitalogic.MaxStale = "1h"
	if got := canonicalProjectionMaxAge(cfg); got != 0 {
		t.Fatalf("non-Digitalogic max age=%s, want disabled", got)
	}
	cfg.Canonical.Pricing.Mode = "digitalogic"
	cfg.Canonical.Pricing.Digitalogic.MaxStale = "invalid"
	if got := canonicalProjectionMaxAge(cfg); got != 0 {
		t.Fatalf("invalid max age=%s, want fail-closed disabled", got)
	}
	cfg.Canonical.Pricing.Digitalogic.MaxStale = "1h"
	if got := canonicalProjectionMaxAge(cfg); got != time.Hour {
		t.Fatalf("Digitalogic max age=%s, want 1h", got)
	}
}

func TestCanonicalProjectionCacheSourceConfigAndRemoteEventsInvalidate(t *testing.T) {
	tmpDir := t.TempDir()
	dataset := filepath.Join(tmpDir, "records.json")
	if err := os.WriteFile(dataset, []byte(`{"1":{"Code":"1","Name":"Fixture"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := appconfig.Load(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.Canonical.Pricing.Mode = "digitalogic"
		cfg.Canonical.Pricing.Digitalogic.MaxStale = "1h"
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithOptions(dataset, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if server.configWatcher != nil {
		if err := server.configWatcher.Close(); err != nil {
			t.Fatal(err)
		}
		server.configWatcher = nil
	}

	seedCanonicalProjectionCacheForTest(server, "source")
	sourceGeneration := canonicalProjectionCacheGenerationForTest(server)
	server.notifyExcelPricingSourceChanged("sha256:source-change")
	assertCanonicalProjectionInvalidatedForTest(t, server, sourceGeneration, "source")

	seedCanonicalProjectionCacheForTest(server, "config")
	configGeneration := canonicalProjectionCacheGenerationForTest(server)
	cfg := manager.Get()
	cfg.UI.Theme = "dark"
	if _, err := server.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	assertCanonicalProjectionInvalidatedForTest(t, server, configGeneration, "config")

	seedCanonicalProjectionCacheForTest(server, "remote")
	server.catalogProviderMu.Lock()
	server.catalogProvider = pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone})
	server.catalogProviderMu.Unlock()
	remoteGeneration := canonicalProjectionCacheGenerationForTest(server)
	stateRevision := excelPricingRevisionForTest("remote-state")
	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source: canonical.Source{
			ID:       "source",
			Dataset:  "records.json",
			Revision: excelPricingRevisionForTest("remote-source"),
		},
		CatalogRevision: excelPricingRevisionForTest("remote-catalog"),
		StateRevision:   stateRevision,
		ETag:            `"sha256:invalid"`,
	}); err == nil {
		t.Fatal("invalid remote revision was accepted")
	}
	server.canonicalProjection.mu.Lock()
	invalidEventPreserved := server.canonicalProjection.cached != nil &&
		server.canonicalProjection.generation == remoteGeneration
	server.canonicalProjection.mu.Unlock()
	server.catalogProviderMu.Lock()
	invalidEventProviderPreserved := server.catalogProvider != nil
	server.catalogProviderMu.Unlock()
	if !invalidEventPreserved || !invalidEventProviderPreserved {
		t.Fatal("invalid remote revision evicted verified local cache state")
	}
	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source: canonical.Source{
			ID:       "source",
			Dataset:  "records.json",
			Revision: excelPricingRevisionForTest("remote-source"),
		},
		CatalogRevision: excelPricingRevisionForTest("remote-catalog"),
		StateRevision:   stateRevision,
		ETag:            `"` + stateRevision + `"`,
	}); err != nil {
		t.Fatal(err)
	}
	assertCanonicalProjectionInvalidatedForTest(t, server, remoteGeneration, "remote")
	server.catalogProviderMu.Lock()
	provider := server.catalogProvider
	server.catalogProviderMu.Unlock()
	if provider != nil {
		t.Fatal("remote revision retained the stale pricing provider")
	}
}

func canonicalProjectionCacheFixture(revision string) recordpipe.Result {
	contract := &canonical.Envelope{
		Schema:      canonical.ContractName,
		EventType:   "snapshot",
		EventID:     "sha256:event-" + revision,
		Source:      canonical.Source{ID: "source", Dataset: "dataset", Revision: "sha256:" + revision},
		GeneratedAt: "2026-08-23T00:00:00Z",
		Products:    []canonical.Product{},
		Categories:  []canonical.Category{},
		Warnings:    []string{},
	}
	return recordpipe.Result{
		Rows:     []map[string]interface{}{{"name": revision}},
		Payload:  contract,
		KeyField: "product_code",
		Contract: contract,
	}
}

func seedCanonicalProjectionCacheForTest(server *Server, revision string) {
	server.canonicalProjection.mu.Lock()
	server.canonicalProjection.cached = &cachedCanonicalProjection{
		result:    canonicalProjectionCacheFixture(revision),
		createdAt: time.Now().UTC(),
	}
	server.canonicalProjection.mu.Unlock()
}

func canonicalProjectionCacheGenerationForTest(server *Server) uint64 {
	server.canonicalProjection.mu.Lock()
	defer server.canonicalProjection.mu.Unlock()
	return server.canonicalProjection.generation
}

func assertCanonicalProjectionInvalidatedForTest(
	t *testing.T,
	server *Server,
	previousGeneration uint64,
	reason string,
) {
	t.Helper()
	server.canonicalProjection.mu.Lock()
	defer server.canonicalProjection.mu.Unlock()
	if server.canonicalProjection.generation <= previousGeneration || server.canonicalProjection.cached != nil {
		t.Fatalf("%s invalidation generation=%d previous=%d cached=%t",
			reason,
			server.canonicalProjection.generation,
			previousGeneration,
			server.canonicalProjection.cached != nil,
		)
	}
}
