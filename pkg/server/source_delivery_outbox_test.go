package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const sourceDeliveryOutboxTestSecretEnv = "PATRIS_SOURCE_DELIVERY_OUTBOX_TEST_SECRET"

func sourceDeliveryOutboxTestServer(t *testing.T, outboxPath string) (*Server, *sourceDeliveryOutbox, appconfig.Config) {
	t.Helper()
	t.Setenv(sourceDeliveryOutboxTestSecretEnv, "test-product-sync-secret")
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := manager.Get()
	cfg.Canonical.SourceID = "patris-office"
	cfg.SendUpdates = updateout.Config{
		Enabled:              true,
		URL:                  "https://digitalogic.invalid/wp-json/digitalogic/patris/product-sync",
		Method:               "POST",
		Format:               "json",
		Mode:                 "changes",
		Initial:              true,
		Timeout:              "1s",
		RetryAttempts:        1,
		ProductSyncSecretEnv: sourceDeliveryOutboxTestSecretEnv,
	}
	if err := manager.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	server := &Server{config: manager, backgroundCtx: context.Background(), dbPath: "kala.db"}
	outbox, err := newSourceDeliveryOutbox(server, outboxPath)
	if err != nil {
		t.Fatal(err)
	}
	server.sourceDeliveryOutbox = outbox
	return server, outbox, cfg
}

func sourceDeliveryOutboxTestEvent(t *testing.T, products []canonical.Product) updateout.Event {
	t.Helper()
	snapshot := freshAckSnapshot(t, products, "patris-office")
	return updateout.Event{
		Type:             "initial",
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:           "kala.db",
		KeyField:         "Code",
		Contract:         snapshot,
		SnapshotContract: snapshot,
	}
}

func enqueueSourceDeliveryOutboxTestEvent(t *testing.T, outbox *sourceDeliveryOutbox, cfg appconfig.Config, event updateout.Event) {
	t.Helper()
	preparedAt := time.Now().UTC().Add(-time.Second)
	if err := outbox.enqueue(cfg, event, preparedAt, preparedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}

func completeSourceDeliveryResult(input *canonical.Envelope) updateout.DeliveryResult {
	return verifiedSourceAcknowledgement(input)
}

func TestSourceDeliveryOutboxRetriesDefinitiveTransientFailure(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "one"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)

	deliveries := 0
	known := false
	outbox.deliver = func(_ appconfig.Config, event updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		deliveries++
		if deliveries == 1 {
			return updateout.DeliveryResult{FailureCode: "receiver_http_status", OutcomeUnknown: &known, Attempts: 1}, "delivery_failed", errors.New("known failure")
		}
		return completeSourceDeliveryResult(event.Contract), "receipt_received", nil
	}
	outbox.probe = func(context.Context, appconfig.Config, *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		t.Fatal("definitive failure must not require a receipt probe")
		return sourceDeliveryReceiptProbe{}, nil
	}

	if pending, _ := outbox.processOnce(); !pending {
		t.Fatal("transient failure was discarded")
	}
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("successful retry did not clear the outbox")
	}
	if deliveries != 2 {
		t.Fatalf("deliveries=%d, want 2", deliveries)
	}
}

func TestSourceDeliveryOutboxUnknownOutcomeProbesBeforeAnotherWrite(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	cfg.SendUpdates.RetryAttempts = 7
	if err := outbox.server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "one"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)

	unknown := true
	deliveries := 0
	outbox.deliver = func(deliveryConfig appconfig.Config, _ updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		if deliveryConfig.SendUpdates.RetryAttempts != 1 {
			t.Fatalf("transport retry attempts=%d, want 1 under outbox ownership", deliveryConfig.SendUpdates.RetryAttempts)
		}
		deliveries++
		return updateout.DeliveryResult{FailureCode: "response_read_failed", OutcomeUnknown: &unknown, Attempts: 1}, "delivery_outcome_unknown", errors.New("response lost")
	}
	probes := 0
	outbox.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		probes++
		if probes == 1 {
			return sourceDeliveryReceiptProbe{}, errors.New("probe unavailable")
		}
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptApplied, EventID: input.EventID, Source: input.Source}, nil
	}

	if pending, _ := outbox.processOnce(); !pending {
		t.Fatal("unknown delivery disappeared")
	}
	if pending, _ := outbox.processOnce(); !pending {
		t.Fatal("inconclusive probe discarded the event")
	}
	if deliveries != 1 {
		t.Fatalf("inconclusive probe caused %d writes, want 1", deliveries)
	}
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("authoritative applied receipt did not clear the event")
	}
	if deliveries != 1 || probes != 2 {
		t.Fatalf("writes=%d probes=%d, want 1 and 2", deliveries, probes)
	}
}

func TestSourceDeliveryOutboxFullModeProbesActualPostedSnapshotIdentity(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	cfg.SendUpdates.Mode = "full"
	if err := outbox.server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	snapshot := freshAckSnapshot(t, []canonical.Product{{ProductCode: "A", Name: "one"}, {ProductCode: "116038", Name: "GL850"}}, "patris-office")
	delta := canonical.ChangeEnvelope(snapshot, &recorddiff.ChangeSet{KeyField: "product_code", Added: []map[string]interface{}{{"product_code": "116038"}}})
	if delta.EventID == snapshot.EventID {
		t.Fatal("fixture delta and snapshot identities unexpectedly match")
	}
	event := updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Source: "kala.db", KeyField: "Code", Contract: delta, SnapshotContract: snapshot}
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)
	status := outbox.status()
	if status["event_id"] != snapshot.EventID || status["generated_at"] != snapshot.GeneratedAt {
		t.Fatalf("status identity=%v generated_at=%v, want posted snapshot %s at %s", status["event_id"], status["generated_at"], snapshot.EventID, snapshot.GeneratedAt)
	}

	unknown := true
	writes := 0
	postedEventID := ""
	outbox.deliver = func(deliveryConfig appconfig.Config, got updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		writes++
		posted := got.Contract
		if updateout.Normalize(deliveryConfig.SendUpdates).Mode == "full" || got.Type == "initial" {
			posted = got.SnapshotContract
		}
		postedEventID = posted.EventID
		return updateout.DeliveryResult{FailureCode: "response_read_failed", OutcomeUnknown: &unknown, Attempts: 1}, "delivery_outcome_unknown", errors.New("response lost")
	}
	probes := 0
	outbox.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		probes++
		if input.EventID != postedEventID || input.EventID != snapshot.EventID {
			t.Fatalf("probe event_id=%s posted=%s snapshot=%s delta=%s", input.EventID, postedEventID, snapshot.EventID, delta.EventID)
		}
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptApplied, EventID: input.EventID, Source: input.Source}, nil
	}

	if pending, _ := outbox.processOnce(); !pending {
		t.Fatal("unknown full-snapshot delivery disappeared")
	}
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("exact snapshot receipt did not clear the outbox")
	}
	if writes != 1 || probes != 1 {
		t.Fatalf("writes=%d probes=%d, want exactly 1 and 1", writes, probes)
	}
}

func TestSourceDeliveryOutboxMigratesLegacyUnknownEntryBeforeAnyReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, path)
	cfg.SendUpdates.Mode = "full"
	if err := server.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	snapshot := freshAckSnapshot(t, []canonical.Product{{ProductCode: "116038", Name: "GL850"}}, "patris-office")
	delta := canonical.ChangeEnvelope(snapshot, &recorddiff.ChangeSet{KeyField: "product_code", Added: []map[string]interface{}{{"product_code": "116038"}}})
	event := updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Source: "kala.db", KeyField: "Code", Contract: delta, SnapshotContract: snapshot}
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)
	outbox.state.Active.TransmittedContract = nil
	outbox.state.Active.State = "outcome_unknown"
	outbox.state.Active.OutcomeUnknown = true
	if err := outbox.persistLocked(); err != nil {
		t.Fatal(err)
	}

	restarted, err := newSourceDeliveryOutbox(server, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceDeliveryEntryID(restarted.state.Active); got != snapshot.EventID {
		t.Fatalf("migrated transmitted event_id=%s, want snapshot %s", got, snapshot.EventID)
	}
	restarted.deliver = func(appconfig.Config, updateout.Event, time.Time, time.Time) (updateout.DeliveryResult, string, error) {
		t.Fatal("legacy uncertain entry was replayed before receipt reconciliation")
		return updateout.DeliveryResult{}, "", nil
	}
	restarted.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		if input.EventID != snapshot.EventID {
			t.Fatalf("legacy probe event_id=%s, want posted snapshot %s", input.EventID, snapshot.EventID)
		}
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptApplied, EventID: input.EventID, Source: input.Source}, nil
	}
	if pending, _ := restarted.processOnce(); pending {
		t.Fatal("applied legacy snapshot receipt did not clear the outbox")
	}
}

func TestSourceDeliveryOutboxPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	server, outbox, cfg := sourceDeliveryOutboxTestServer(t, path)
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "one"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)

	restarted, err := newSourceDeliveryOutbox(server, path)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	restarted.deliver = func(_ appconfig.Config, got updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		deliveries++
		if got.Contract.EventID != event.Contract.EventID {
			t.Fatalf("restarted event=%s, want %s", got.Contract.EventID, event.Contract.EventID)
		}
		return completeSourceDeliveryResult(got.Contract), "receipt_received", nil
	}
	if pending, _ := restarted.processOnce(); pending {
		t.Fatal("restart recovery did not clear the event")
	}
	if deliveries != 1 {
		t.Fatalf("deliveries=%d, want 1", deliveries)
	}
}

func TestSourceDeliveryOutboxRecoversIdempotentlyFromAcceptedResponseLoss(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "one"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)

	unknown := true
	writes := 0
	outbox.deliver = func(_ appconfig.Config, _ updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		writes++
		return updateout.DeliveryResult{FailureCode: "response_read_failed", OutcomeUnknown: &unknown}, "delivery_outcome_unknown", errors.New("accepted response lost")
	}
	outbox.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptSuperseded, EventID: input.EventID, Source: input.Source}, nil
	}
	outbox.processOnce()
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("superseded exact receipt did not acknowledge durable acceptance")
	}
	if writes != 1 {
		t.Fatalf("accepted event was written %d times, want exactly once", writes)
	}
}

func TestSourceDeliveryOutboxPendingReceiptSuppressesAnotherWrite(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "one"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)
	writes := 0
	outbox.deliver = func(_ appconfig.Config, _ updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		writes++
		return updateout.DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1, Delivery: &updateout.DeliveryReceipt{
			Status: "pending", EventID: event.Contract.EventID, Source: event.Contract.Source, InputSource: event.Contract.Source, PendingProducts: 1,
		}}, "delivery_pending", nil
	}
	probes := 0
	outbox.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		probes++
		if probes == 1 {
			return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptPending, EventID: input.EventID, Source: input.Source, PendingProducts: 1}, nil
		}
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptApplied, EventID: input.EventID, Source: input.Source}, nil
	}
	outbox.processOnce()
	outbox.processOnce()
	if writes != 1 {
		t.Fatalf("pending receipt caused %d writes, want 1", writes)
	}
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("applied receipt did not clear pending delivery")
	}
	if writes != 1 || probes != 2 {
		t.Fatalf("writes=%d probes=%d, want 1 and 2", writes, probes)
	}
}

func TestSourceDeliveryOutboxCoalescesGL850CreationIntoFullSnapshot(t *testing.T) {
	_, outbox, cfg := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	first := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "existing"}})
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, first)
	unknown := true
	var delivered []*canonical.Envelope
	outbox.deliver = func(_ appconfig.Config, event updateout.Event, _, _ time.Time) (updateout.DeliveryResult, string, error) {
		delivered = append(delivered, event.Contract)
		if len(delivered) == 1 {
			return updateout.DeliveryResult{FailureCode: "response_read_failed", OutcomeUnknown: &unknown}, "delivery_outcome_unknown", errors.New("response lost")
		}
		return completeSourceDeliveryResult(event.Contract), "receipt_received", nil
	}
	outbox.probe = func(_ context.Context, _ appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
		return sourceDeliveryReceiptProbe{Status: sourceDeliveryReceiptApplied, EventID: input.EventID, Source: input.Source}, nil
	}
	outbox.processOnce()

	canonicalConfig := canonical.DefaultConfig()
	canonicalConfig.SourceID = "patris-office"
	_, snapshot, err := canonical.TransformContext(context.Background(), []map[string]interface{}{
		{"Code": "A", "name": "existing", "ALLANBAR": 1},
		{"Code": "116038", "name": "GL850G 4 PORT USB2/0 HUB", "ANBAR1": 1, "ALLANBAR": 1},
	}, "kala.db", canonicalConfig, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changes := &recorddiff.ChangeSet{KeyField: "Code", Added: []map[string]interface{}{{"Code": "116038"}}}
	delta := canonical.ChangeEnvelope(snapshot, changes)
	event := updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Source: "kala.db", KeyField: "Code", Contract: delta, SnapshotContract: snapshot}
	enqueueSourceDeliveryOutboxTestEvent(t, outbox, cfg, event)

	if pending, _ := outbox.processOnce(); !pending {
		t.Fatal("coalesced snapshot was dropped after acknowledging the older event")
	}
	if pending, _ := outbox.processOnce(); pending {
		t.Fatal("coalesced snapshot did not complete")
	}
	if len(delivered) != 2 {
		t.Fatalf("deliveries=%d, want 2", len(delivered))
	}
	full := delivered[1]
	if full.EventType != "snapshot" {
		t.Fatalf("fallback event_type=%q, want snapshot", full.EventType)
	}
	found := false
	for _, product := range full.Products {
		if product.ProductCode == "116038" {
			found = true
		}
	}
	if !found {
		t.Fatalf("GL850-like creation is absent from the full-snapshot fallback: %#v", full.Products)
	}
}

func TestSourceDeliveryOutboxWriteFailureDoesNotAdvanceWatcherBaseline(t *testing.T) {
	root := t.TempDir()
	blockedParent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	server, outbox, _ := sourceDeliveryOutboxTestServer(t, filepath.Join(root, "initial-outbox.json"))
	// Construct against a valid empty path first, then make the next atomic
	// persist fail. On Unix, Lstat beneath a regular file correctly returns
	// ENOTDIR, so constructing directly at the blocked path would test loading
	// an invalid configured path rather than a runtime write failure.
	outbox.path = filepath.Join(blockedParent, "outbox.json")
	server.sourceDeliveryOutbox = outbox
	server.seedLastSnapshot([]map[string]interface{}{{"Code": "A"}}, "sha256:before")
	event := sourceDeliveryOutboxTestEvent(t, []canonical.Product{{ProductCode: "A", Name: "old"}, {ProductCode: "B", Name: "new"}})
	result := recordpipe.Result{Rows: []map[string]interface{}{{"Code": "A"}, {"Code": "B"}}, KeyField: "Code", Contract: event.SnapshotContract}

	if err := server.dispatchUpdateAndCommitBaseline(result, event, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("unwritable outbox was accepted")
	}
	server.lastRecordsMu.RLock()
	defer server.lastRecordsMu.RUnlock()
	if len(server.lastRecords) != 1 || server.lastRecords[0]["Code"] != "A" || server.lastContractRevision != "sha256:before" {
		t.Fatal("watcher baseline advanced despite outbox persistence failure")
	}
	if outbox.state.Active == nil {
		t.Fatal("failed persistence did not retain a recoverable in-memory event")
	}
}

func TestSourceDeliveryReceiptProbeMatchesAuthenticatedWordPressContract(t *testing.T) {
	t.Setenv(sourceDeliveryOutboxTestSecretEnv, "test-product-sync-secret")
	input := freshAckSnapshot(t, []canonical.Product{{ProductCode: "116038", Name: "GL850"}}, "patris-office")
	requests := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost || request.URL.Path != "/wp-json/digitalogic/patris/product-sync/receipt" || request.URL.RawQuery != "" {
			t.Errorf("unexpected probe target: %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get(updateout.ProductSyncSecretHeader) != "test-product-sync-secret" {
			t.Error("receipt probe omitted the product-sync secret boundary")
		}
		var body sourceDeliveryReceiptRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.EventID != input.EventID || body.GeneratedAt != input.GeneratedAt || !body.Source.SameIdentity(input.Source) {
			t.Errorf("receipt identity mismatch: %#v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sourceDeliveryReceiptResponse{Success: true, Data: sourceDeliveryReceiptProbe{
			Schema: sourceDeliveryReceiptSchema, Status: sourceDeliveryReceiptApplied,
			EventID: input.EventID, GeneratedAt: input.GeneratedAt, Source: input.Source, ObservedAt: time.Now().UTC().Format(time.RFC3339),
		}})
	}))
	defer receiver.Close()

	cfg := appconfig.Default()
	cfg.SendUpdates = updateout.Config{Enabled: true, URL: receiver.URL + "/wp-json/digitalogic/patris/product-sync", Method: "POST", Format: "json", Mode: "changes", Timeout: "1s", RetryAttempts: 1, ProductSyncSecretEnv: sourceDeliveryOutboxTestSecretEnv}
	receipt, err := probeSourceDeliveryReceipt(context.Background(), cfg, input)
	if err != nil || receipt.Status != sourceDeliveryReceiptApplied || requests != 1 {
		t.Fatalf("receipt=%#v requests=%d err=%v", receipt, requests, err)
	}
}

func TestSourceDeliveryReceiptProbeFailsClosedOnInconclusiveHistory(t *testing.T) {
	t.Setenv(sourceDeliveryOutboxTestSecretEnv, "test-product-sync-secret")
	input := freshAckSnapshot(t, []canonical.Product{{ProductCode: "A", Name: "one"}}, "patris-office")
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"code":"digitalogic_product_sync_receipt_history_inconclusive"}`, http.StatusServiceUnavailable)
	}))
	defer receiver.Close()
	cfg := appconfig.Default()
	cfg.SendUpdates = updateout.Config{Enabled: true, URL: receiver.URL + "/wp-json/digitalogic/patris/product-sync", Method: "POST", Format: "json", Timeout: "1s", RetryAttempts: 1, ProductSyncSecretEnv: sourceDeliveryOutboxTestSecretEnv}
	if _, err := probeSourceDeliveryReceipt(context.Background(), cfg, input); !errors.Is(err, errSourceDeliveryReceiptProbe) {
		t.Fatalf("503 receipt history was not inconclusive: %v", err)
	}
}

func TestSourceDeliveryReceiptProbeRequiresExactGeneratedAtEcho(t *testing.T) {
	t.Setenv(sourceDeliveryOutboxTestSecretEnv, "test-product-sync-secret")
	input := freshAckSnapshot(t, []canonical.Product{{ProductCode: "A", Name: "one"}}, "patris-office")
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sourceDeliveryReceiptResponse{Success: true, Data: sourceDeliveryReceiptProbe{
			Schema: sourceDeliveryReceiptSchema, Status: sourceDeliveryReceiptApplied,
			EventID: input.EventID, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: input.Source,
			ObservedAt: time.Now().UTC().Format(time.RFC3339),
		}})
	}))
	defer receiver.Close()
	cfg := appconfig.Default()
	cfg.SendUpdates = updateout.Config{Enabled: true, URL: receiver.URL + "/wp-json/digitalogic/patris/product-sync", Method: "POST", Format: "json", Timeout: "1s", RetryAttempts: 1, ProductSyncSecretEnv: sourceDeliveryOutboxTestSecretEnv}
	if _, err := probeSourceDeliveryReceipt(context.Background(), cfg, input); !errors.Is(err, errSourceDeliveryReceiptProbe) {
		t.Fatalf("mismatched generated_at echo was accepted: %v", err)
	}
}

func TestSourceDeliveryPeriodicFullSnapshotReconciliationRunsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{backgroundCtx: ctx, sourceDeliveryOutbox: &sourceDeliveryOutbox{}}
	var calls atomic.Int32
	called := make(chan struct{}, 1)
	server.startSourceDeliveryReconciliation(5*time.Millisecond, func(context.Context) {
		calls.Add(1)
		select {
		case called <- struct{}{}:
		default:
		}
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("periodic full-snapshot reconciliation did not run")
	}
	cancel()
	server.serviceWG.Wait()
	before := calls.Load()
	time.Sleep(15 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("reconciliation continued after cancellation")
	}
}
