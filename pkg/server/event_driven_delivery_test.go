package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
)

type cancelAwareSource struct {
	path         string
	rows         []map[string]interface{}
	blockFrom    int32
	calls        atomic.Int32
	started      chan struct{}
	canceled     chan struct{}
	startedOnce  sync.Once
	canceledOnce sync.Once
}

func (source *cancelAwareSource) GetRecords() ([]map[string]interface{}, error) {
	return source.GetRawRecords()
}

func (source *cancelAwareSource) GetRawRecords() ([]map[string]interface{}, error) {
	return source.GetRawRecordsContext(context.Background())
}

func (source *cancelAwareSource) GetRawRecordsContext(ctx context.Context) ([]map[string]interface{}, error) {
	if source.calls.Add(1) < source.blockFrom {
		return source.rows, nil
	}
	source.startedOnce.Do(func() { close(source.started) })
	<-ctx.Done()
	source.canceledOnce.Do(func() { close(source.canceled) })
	return nil, ctx.Err()
}

func (source *cancelAwareSource) GetPath() string { return source.path }
func (source *cancelAwareSource) Close() error    { return nil }

func TestEventDrivenDeltaCapturesCreateCNYAndWeightUpdates(t *testing.T) {
	oldPrice := pricingcatalog.DecimalFromFloat(10)
	oldWeight := pricingcatalog.DecimalFromFloat(100)
	newPrice := pricingcatalog.DecimalFromFloat(12.5)
	newWeight := pricingcatalog.DecimalFromFloat(125)
	baselineProducts := []canonical.Product{{ProductCode: "A", Name: "existing", ForeignPrice: oldPrice, WeightGrams: oldWeight}}
	currentProducts := []canonical.Product{
		{ProductCode: "A", Name: "existing", ForeignPrice: newPrice, WeightGrams: newWeight},
		{ProductCode: "116038", Name: "GL850", ForeignPrice: pricingcatalog.DecimalFromFloat(4.25), WeightGrams: pricingcatalog.DecimalFromFloat(80)},
	}
	server := &Server{}
	baseline := canonical.NewEnvelope(baselineProducts, "kala.db", "patris-office", time.Unix(1, 0).UTC())
	server.seedLastSnapshot(canonical.ProductsToRows(baselineProducts), baseline.Source.Revision)
	current := canonical.NewEnvelope(currentProducts, "kala.db", "patris-office", time.Unix(2, 0).UTC())
	result := recordpipe.Result{Rows: canonical.ProductsToRows(currentProducts), KeyField: "product_code", Contract: current}

	changes, contractChanged := server.computeRecordUpdate(result)
	if !contractChanged || len(changes.Added) != 1 || len(changes.Modified) != 1 || len(changes.Deleted) != 0 {
		t.Fatalf("unexpected exact delta: changed=%t changes=%#v", contractChanged, changes)
	}
	if got := changes.Added[0]["product_code"]; got != "116038" {
		t.Fatalf("created product_code=%v, want 116038", got)
	}
	modified := changes.Modified[0]
	if modified.Code != "A" || !slices.Contains(modified.ChangedFields, "foreign_price") || !slices.Contains(modified.ChangedFields, "weight_grams") {
		t.Fatalf("CNY/weight fields missing from update delta: %#v", modified)
	}
	delta := result.SyncEnvelope(&changes)
	if delta == nil || delta.EventType != "update" || len(delta.Products) != 2 {
		t.Fatalf("canonical delta did not carry create and update products: %#v", delta)
	}
	byCode := map[string]canonical.Product{}
	for _, product := range delta.Products {
		byCode[product.ProductCode] = product
	}
	updated := byCode["A"]
	if updated.ForeignPrice == nil || updated.ForeignPrice.String() != "12.5" || updated.WeightGrams == nil || updated.WeightGrams.String() != "125" {
		t.Fatalf("updated CNY/weight values changed in envelope: %#v", updated)
	}
}

func TestSourceUpdateEventPreservesPublicMaterializationTimestamp(t *testing.T) {
	observedAt := time.Now().UTC().Add(-time.Second)
	changes := &ChangeSet{Timestamp: "2026-11-18T14:00:00Z", KeyField: "Code"}
	result := recordpipe.Result{Rows: []map[string]interface{}{{"Code": "A"}}, KeyField: "Code"}
	event := sourceUpdateEvent(result, changes, "kala.db", observedAt)
	if event.Timestamp != changes.Timestamp {
		t.Fatalf("public timestamp=%q, want change materialization %q", event.Timestamp, changes.Timestamp)
	}
	if !event.SourceObservedAt.Equal(observedAt) {
		t.Fatalf("private observation=%s, want %s", event.SourceObservedAt, observedAt)
	}
}

func TestEventDrivenCatalogDeliveryRejectsRemotePolling(t *testing.T) {
	server := &Server{dbPath: "https://example.invalid/kala.db"}
	err := server.StartWatching(time.Second)
	if err == nil || !strings.Contains(err.Error(), "requires a local database source") {
		t.Fatalf("remote catalog polling was not rejected: %v", err)
	}
	if server.sourceWatcherActive() {
		t.Fatal("remote source rejection left a watcher or poller running")
	}
}

func TestReplaceConfigCannotPersistWatchDisabledForRequiredDelivery(t *testing.T) {
	server, _, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install test watcher")
	}
	defer server.removeSourceWatcher(active)
	if !cfg.Server.Watch {
		t.Fatal("fixture must begin with restart-safe watching enabled")
	}
	candidate := cfg
	candidate.Server.Watch = false
	if _, err := server.ReplaceConfig(candidate); err == nil {
		t.Fatal("required delivery persisted server.watch=false while a watcher was active")
	}
	if !server.Config().Server.Watch {
		t.Fatal("rejected runtime config changed the persisted watch setting")
	}
	status := server.Status()["source_delivery_watch"].(map[string]interface{})
	if status["required"] != true || status["active"] != true {
		t.Fatalf("watch status=%v, want required and active", status)
	}
}

func TestReplaceConfigRejectsRuntimeDatabasePathChange(t *testing.T) {
	server, _, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	cfg.Database.Path = server.currentDBPath()
	if err := server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	server.rememberRuntimeConfig(cfg)
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install test watcher")
	}
	defer server.removeSourceWatcher(active)
	candidate := cfg
	candidate.Database.Path = filepath.Join(t.TempDir(), "replacement.db")
	if _, err := server.ReplaceConfig(candidate); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("runtime path change error=%v, want restart-required rejection", err)
	}
	if got := server.Config().Database.Path; got != cfg.Database.Path {
		t.Fatalf("rejected path change persisted %q, want %q", got, cfg.Database.Path)
	}
	if got := server.currentDBPath(); got != cfg.Database.Path {
		t.Fatalf("active datasource changed to %q, want %q", got, cfg.Database.Path)
	}
}

func TestReplaceConfigRollsBackWhenCatchUpCannotBeQueued(t *testing.T) {
	server, _, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	cfg.Database.Path = server.currentDBPath()
	if err := server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	server.rememberRuntimeConfig(cfg)
	// An installed but unregistered watcher passes the active-lane preflight
	// and deterministically rejects the forced catch-up.
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install test watcher")
	}
	defer server.removeSourceWatcher(active)
	candidate := cfg
	candidate.SendUpdates.URL = "https://receiver.invalid/replacement-product-sync"
	if _, err := server.ReplaceConfig(candidate); err == nil || !strings.Contains(err.Error(), "catch-up") {
		t.Fatalf("catch-up failure error=%v", err)
	}
	_, effective := server.config.Snapshots()
	if effective.SendUpdates.URL != cfg.SendUpdates.URL {
		t.Fatalf("failed catch-up left committed URL %q, want %q", effective.SendUpdates.URL, cfg.SendUpdates.URL)
	}
	previous, ok := server.previousRuntimeConfig()
	if !ok || previous.SendUpdates.URL != cfg.SendUpdates.URL {
		t.Fatalf("failed catch-up advanced remembered config: ok=%t url=%q", ok, previous.SendUpdates.URL)
	}
}

func TestBroadcastUpdateContextCancelsEveryOrdinarySourceRead(t *testing.T) {
	for _, test := range []struct {
		name      string
		raw       bool
		blockFrom int32
	}{
		{name: "configured_projection", blockFrom: 1},
		{name: "raw_viewer_canonical_delivery", raw: true, blockFrom: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
			cfg.Database.Raw = test.raw
			if err := server.config.Replace(cfg); err != nil {
				t.Fatal(err)
			}
			source := &cancelAwareSource{
				path:      "kala.db",
				rows:      []map[string]interface{}{{"Code": "116038", "Name": "GL850G 4 PORT USB2/0 HUB", "ANBAR1": 1, "ALLANBAR": 1}},
				blockFrom: test.blockFrom,
				started:   make(chan struct{}),
				canceled:  make(chan struct{}),
			}
			server.dataSource = source
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- server.broadcastUpdateContext(ctx, time.Now().UTC()) }()
			select {
			case <-source.started:
			case <-time.After(2 * time.Second):
				t.Fatal("bounded source read did not start")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("broadcast error=%v, want deadline exceeded", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("ordinary source update ignored its bounded context")
			}
			select {
			case <-source.canceled:
			case <-time.After(time.Second):
				t.Fatal("source did not observe cancellation")
			}
		})
	}
}

func TestRawViewerStillQueuesCanonicalSourceSnapshot(t *testing.T) {
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	cfg.Database.Raw = true
	if err := server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	server.dataSource = &canonicalProjectionTestSource{path: "kala.db", rows: []map[string]interface{}{
		{"Code": "116038", "name": "GL850G 4 PORT USB2/0 HUB", "ANBAR1": 1, "ALLANBAR": 1},
	}}
	server.dbPath = "kala.db"
	if err := server.dispatchSourceSnapshot(context.Background(), time.Now().UTC().Add(-time.Second), "source_prepare", time.Now().UTC().Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	outbox.mu.Lock()
	active := outbox.state.Active
	outbox.mu.Unlock()
	if active == nil || active.Event.SnapshotContract == nil || active.Event.Raw || active.Event.SnapshotContract.Schema != canonical.ContractName {
		t.Fatalf("raw viewer did not produce canonical external delivery: %#v", active)
	}
	server.lastRecordsMu.RLock()
	defer server.lastRecordsMu.RUnlock()
	if len(server.lastRecords) != 1 || server.lastRecords[0]["Code"] != "116038" || server.lastRecords[0]["product_code"] != nil {
		t.Fatalf("canonical delivery replaced the raw viewer baseline: %#v", server.lastRecords)
	}
}

func TestRequiredDeliveryFailsSynchronouslyWithoutCanonicalContract(t *testing.T) {
	server, outbox, _ := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	if err := server.dispatchUpdateEvent(updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339)}, time.Now().UTC()); err == nil {
		t.Fatal("required canonical delivery advanced asynchronously without a contract")
	}
	if outbox.state.Active != nil {
		t.Fatal("contractless required delivery entered the durable outbox")
	}
}

func TestSourceDeliveryCatchUpTransitionAndTrigger(t *testing.T) {
	previous := appconfig.Default()
	current := previous
	current.SendUpdates.Enabled = true
	current.SendUpdates.RequireContract = true
	current.SendUpdates.URL = "https://receiver.invalid/product-sync"
	current.SendUpdates.ProductSyncSecretEnv = "PATRIS_CATCH_UP_TEST_SECRET"
	if !sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("enabling required delivery did not require a full catch-up")
	}
	previous = current
	current.UI.Language = "fa"
	if sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("unrelated UI config change queued a catalog catch-up")
	}
	previous = current
	current.Database.RTLConversion = !current.Database.RTLConversion
	if !sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("RTL projection change did not require a full catch-up")
	}
	previous = current
	current.SendUpdates.URL = "https://receiver.invalid/new-product-sync"
	if !sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("destination change did not require a full catch-up")
	}

	path := filepath.Join(t.TempDir(), "kala.db")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	fw, err := watcher.NewFileWatcher()
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan watcher.Event, 1)
	if err := fw.WatchEvents(path, func(event watcher.Event) error {
		observed <- event
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	server := &Server{dbPath: path}
	if !server.installSourceWatcher(fw) {
		t.Fatal("failed to install catch-up watcher")
	}
	defer func() {
		server.removeSourceWatcher(fw)
		_ = fw.Close()
	}()
	if err := server.triggerSourceCatchUp(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-observed:
		if event.Reason != watcher.EventStartup {
			t.Fatalf("catch-up reason=%q, want startup", event.Reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("full source catch-up was not queued")
	}
}

func TestGenericContractWithoutInitialSnapshotsDoesNotPromoteDelta(t *testing.T) {
	previous := appconfig.Default()
	current := previous
	current.SendUpdates = updateout.Config{
		Enabled:         true,
		URL:             "https://receiver.invalid/generic-contract",
		Method:          "POST",
		Format:          "json",
		RequireContract: true,
		Initial:         false,
	}
	if sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("generic initial=false delivery would promote and then skip its next delta")
	}
	current.SendUpdates.Initial = true
	if !sourceDeliveryCatchUpRequired(previous, current, true) {
		t.Fatal("generic initial=true delivery did not request its configured snapshot")
	}
}

func TestPendingRuntimeCatchUpPromotesReservedChangeToSnapshot(t *testing.T) {
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	server.dataSource = &canonicalProjectionTestSource{path: "kala.db", rows: []map[string]interface{}{
		{"Code": "A", "name": "one", "ANBAR1": 1, "ALLANBAR": 1},
	}}
	server.rememberRuntimeConfig(cfg)
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install active watcher marker")
	}
	defer server.removeSourceWatcher(active)
	candidate := cfg
	candidate.SendUpdates.URL = "https://replacement.invalid/wp-json/digitalogic/patris/product-sync"
	if err := server.config.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	_ = server.markSourceDeliveryCatchUp(candidate)
	server.rememberRuntimeConfig(candidate)
	if err := server.handleSourceWatchEvent(watcher.Event{Path: "kala.db", Reason: watcher.EventChange, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	outbox.mu.Lock()
	queued := outbox.state.Active
	outbox.mu.Unlock()
	if queued == nil || queued.Event.Type != "initial" || queued.Event.Contract.EventType != "snapshot" {
		t.Fatalf("reserved change crossed new destination as a delta: %#v", queued)
	}
	if server.sourceDeliveryCatchUpPending(candidate) {
		t.Fatal("successful durable snapshot did not clear pending catch-up marker")
	}
}

func TestExternalReloadWindowDefersSourceEventUntilValidatedCatchUp(t *testing.T) {
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	server.rememberRuntimeConfig(cfg)
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install active watcher marker")
	}
	defer server.removeSourceWatcher(active)
	candidate := cfg
	candidate.SendUpdates.URL = "https://replacement.invalid/wp-json/digitalogic/patris/product-sync"
	if err := server.config.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	err := server.handleSourceWatchEvent(watcher.Event{Path: "kala.db", Reason: watcher.EventChange, ObservedAt: time.Now().UTC()})
	if !errors.Is(err, errRuntimeConfigAwaitingApply) {
		t.Fatalf("intervening reload event error=%v, want guarded retry", err)
	}
	if outbox.state.Active != nil {
		t.Fatal("intervening reload event enqueued a delta before validated catch-up")
	}
}

func TestFailedPendingCatchUpRetainsRuntimeMarker(t *testing.T) {
	root := t.TempDir()
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(root, "outbox.json"))
	server.dataSource = &canonicalProjectionTestSource{path: "kala.db", rows: []map[string]interface{}{
		{"Code": "A", "name": "one", "ANBAR1": 1, "ALLANBAR": 1},
	}}
	server.rememberRuntimeConfig(cfg)
	active := &watcher.FileWatcher{}
	if !server.installSourceWatcher(active) {
		t.Fatal("failed to install active watcher marker")
	}
	defer server.removeSourceWatcher(active)
	candidate := cfg
	candidate.SendUpdates.URL = "https://replacement.invalid/wp-json/digitalogic/patris/product-sync"
	if err := server.config.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	_ = server.markSourceDeliveryCatchUp(candidate)
	server.rememberRuntimeConfig(candidate)
	blockedParent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	outbox.path = filepath.Join(blockedParent, "outbox.json")
	if err := server.handleSourceWatchEvent(watcher.Event{Path: "kala.db", Reason: watcher.EventChange, ObservedAt: time.Now().UTC()}); err == nil {
		t.Fatal("unwritable catch-up outbox was acknowledged")
	}
	if !server.sourceDeliveryCatchUpPending(candidate) {
		t.Fatal("failed catch-up cleared its mandatory full-snapshot marker")
	}
}

func TestStartupCatchUpPersistsSnapshotBeforeBaseline(t *testing.T) {
	server, outbox, _ := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	server.dataSource = &canonicalProjectionTestSource{path: "kala.db", rows: []map[string]interface{}{
		{"Code": "A", "name": "existing", "ANBAR1": 1, "ALLANBAR": 1},
		{"Code": "116038", "name": "GL850", "ANBAR1": 1, "ALLANBAR": 1},
	}}
	server.seedLastSnapshot([]map[string]interface{}{{"product_code": "A"}}, "sha256:before")

	preparedAt := time.Now().UTC()
	observedAt := preparedAt.Add(-750 * time.Millisecond)
	if err := server.dispatchSourceSnapshot(context.Background(), preparedAt, "source_startup_prepare", observedAt); err != nil {
		t.Fatal(err)
	}
	outbox.mu.Lock()
	active := outbox.state.Active
	outbox.mu.Unlock()
	if active == nil || active.Event.SnapshotContract == nil {
		t.Fatal("startup catch-up did not durably enqueue a snapshot")
	}
	if !active.SourceObservedAt.Equal(observedAt) {
		t.Fatalf("startup observation timestamp=%s, want %s", active.SourceObservedAt, observedAt)
	}
	materializedAt, err := time.Parse(time.RFC3339Nano, active.Event.Timestamp)
	if err != nil || materializedAt.Before(preparedAt) {
		t.Fatalf("public event timestamp=%q is not the post-read materialization time: %v", active.Event.Timestamp, err)
	}
	found := false
	for _, product := range active.Event.SnapshotContract.Products {
		if product.ProductCode == "116038" {
			found = true
		}
	}
	if !found {
		t.Fatal("process-down creation was absent from startup catch-up snapshot")
	}
	server.lastRecordsMu.RLock()
	baselineCount := len(server.lastRecords)
	baselineRevision := server.lastContractRevision
	server.lastRecordsMu.RUnlock()
	if baselineCount != 2 || baselineRevision != active.Event.SnapshotContract.Source.Revision {
		t.Fatalf("startup baseline was not committed after durable enqueue: count=%d revision=%s", baselineCount, baselineRevision)
	}
}
