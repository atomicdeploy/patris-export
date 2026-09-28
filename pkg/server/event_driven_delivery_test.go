package server

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

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

func TestEventDrivenCatalogDeliveryRejectsRemotePolling(t *testing.T) {
	server := &Server{dbPath: "https://example.invalid/kala.db"}
	err := server.StartWatching(time.Second)
	if err == nil || !strings.Contains(err.Error(), "requires a local database source") {
		t.Fatalf("remote catalog polling was not rejected: %v", err)
	}
	if server.watcher != nil {
		t.Fatal("remote source rejection left a watcher or poller running")
	}
}

func TestStartupCatchUpPersistsSnapshotBeforeBaseline(t *testing.T) {
	server, outbox, _ := sourceDeliveryOutboxTestServer(t, filepath.Join(t.TempDir(), "outbox.json"))
	server.dataSource = &canonicalProjectionTestSource{path: "kala.db", rows: []map[string]interface{}{
		{"Code": "A", "name": "existing", "ANBAR1": 1, "ALLANBAR": 1},
		{"Code": "116038", "name": "GL850", "ANBAR1": 1, "ALLANBAR": 1},
	}}
	server.seedLastSnapshot([]map[string]interface{}{{"product_code": "A"}}, "sha256:before")

	if err := server.dispatchSourceSnapshot(context.Background(), time.Now(), "source_startup_prepare"); err != nil {
		t.Fatal(err)
	}
	outbox.mu.Lock()
	active := outbox.state.Active
	outbox.mu.Unlock()
	if active == nil || active.Event.SnapshotContract == nil {
		t.Fatal("startup catch-up did not durably enqueue a snapshot")
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
