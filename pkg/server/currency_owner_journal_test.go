package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func journalTestQueue(dir string, now time.Time) *excelPricingWritebackQueue {
	q := newExcelPricingWritebackQueue(nil)
	q.currencyJournalDir = dir
	q.now = func() time.Time { return now }
	return q
}

func TestCurrencyJournalRestartPreservesIdentityAndSupersession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "currency-intents")
	now := time.Now().UTC()
	q := journalTestQueue(dir, now)
	first, err := q.enqueue(validExcelPricingWritebackRequest("journal-first-01", "yuan_price", 29500))
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := validExcelPricingWritebackRequest("journal-second-02", "yuan_price", 29600)
	second, err := q.enqueue(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	restored := journalTestQueue(dir, now.Add(time.Hour))
	if err = restored.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	job := restored.jobs[second.JobID]
	if job == nil || job.RequestID != second.RequestID || job.settings != secondRequest.Settings || job.expectedStateRevision != secondRequest.ExpectedStateRevision || !job.createdAt.Equal(now) || job.sequence != second.sequence || !job.ownerObserveOnly || job.Status != "observation_required" {
		t.Fatalf("intent not restored: %+v", job)
	}
	if restored.jobs[first.JobID].Status != "superseded" || restored.latestByKey["yuan_price"] != second.JobID {
		t.Fatal("latest sequence lost")
	}
	if next, _ := restored.next(); next != nil {
		t.Fatal("restart dispatched a write automatically")
	}
	restored.purgeLocked(now.Add(2 * time.Hour))
	if restored.jobs[second.JobID] == nil {
		t.Fatal("unresolved currency lost after TTL")
	}
}

func TestCurrencyJournalCorruptionBlocksOnlyCurrencyAdmission(t *testing.T) {
	dir := t.TempDir()
	q := journalTestQueue(dir, time.Now().UTC())
	if err := os.WriteFile(filepath.Join(dir, strings.Repeat("a", 32)+".json"), []byte(`{"schema":`), 0600); err != nil {
		t.Fatal(err)
	}
	q.currencyJournalError = q.loadCurrencyJournal()
	if q.currencyJournalError == nil || len(q.jobs) != 0 {
		t.Fatal("partial corrupt restore accepted")
	}
	if _, err := q.enqueue(validExcelPricingWritebackRequest("journal-blocked-01", "yuan_price", 29500)); err == nil {
		t.Fatal("corrupt journal allowed currency admission")
	}
	request := validExcelPricingWritebackRequest("journal-other-02", "profit_margin_percent", 29500)
	if _, err := q.enqueue(request); err != nil {
		t.Fatalf("unrelated path blocked: %v", err)
	}
}

func TestCurrencyJournalRetentionKeepsUnknownAndPrunesTerminal(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	q := journalTestQueue(dir, now)
	confirmed, err := q.enqueue(validExcelPricingWritebackRequest("journal-confirmed-01", "yuan_price", 29500))
	if err != nil {
		t.Fatal(err)
	}
	q.jobs[confirmed.JobID].Status = "confirmed"
	if err = q.markCurrencyJournalTerminal(q.jobs[confirmed.JobID]); err != nil {
		t.Fatal(err)
	}
	unknown, err := q.enqueue(validExcelPricingWritebackRequest("journal-unknown-02", "dollar_price", 29500))
	if err != nil {
		t.Fatal(err)
	}
	if err = q.pruneCurrencyJournal(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, confirmed.JobID+".json")); !os.IsNotExist(err) {
		t.Fatal("terminal journal retained")
	}
	if _, err = os.Stat(filepath.Join(dir, unknown.JobID+".json")); err != nil {
		t.Fatal("unresolved journal expired")
	}
}

func TestCurrencyJournalRestoredMissingOwnerNeverPosts(t *testing.T) {
	var writes atomic.Int32
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes.Add(1)
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"success":false,"code":"digitalogic_currency_async_request_not_found"}`)
	}))
	defer remote.Close()
	t.Setenv("DIGITALOGIC_PRICING_WRITE_KEY", "test-key")
	t.Setenv("DIGITALOGIC_PRICING_WRITE_SECRET", "test-secret")
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.client = remote.Client()
	dir := t.TempDir()
	q := journalTestQueue(dir, time.Now().UTC())
	q.server = server
	admitted, err := q.enqueue(validExcelPricingWritebackRequest("journal-no-repost-01", "yuan_price", 29500))
	if err != nil {
		t.Fatal(err)
	}
	restored := journalTestQueue(dir, time.Now().UTC())
	restored.server = server
	if err = restored.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	result := restored.processOwnerCurrency(context.Background(), restored.jobs[admitted.JobID])
	if result.status != "observation_required" || writes.Load() != 0 {
		t.Fatalf("restarted request re-submitted: %+v writes=%d", result, writes.Load())
	}
}

func TestCurrencyJournalCapacityDoesNotEvictUnresolved(t *testing.T) {
	q := journalTestQueue(t.TempDir(), time.Now().UTC())
	for i := 1; i <= excelPricingWritebackMaxJobs; i++ {
		id := fmt.Sprintf("%032x", i)
		record := currencyIntentRecord{Schema: currencyJournalSchema, JobID: id, Sequence: uint64(i), CreatedAt: q.now().Add(-time.Hour), Request: validExcelPricingWritebackRequest(fmt.Sprintf("journal-capacity-%03d", i), "yuan_price", 29500)}
		if err := writeCurrencyJournalJSON(filepath.Join(q.currencyJournalDir, id+".json"), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	if _, err := q.enqueue(validExcelPricingWritebackRequest("journal-capacity-overflow", "dollar_price", 29500)); err == nil {
		t.Fatal("unresolved journals evicted for admission")
	}
	entries, err := q.readCurrencyJournal()
	if err != nil || len(entries) != excelPricingWritebackMaxJobs {
		t.Fatalf("capacity modified journals: %d %v", len(entries), err)
	}
}
