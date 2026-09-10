package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCurrencyHistoricalObservationNeverWritesOrProjects(t *testing.T) {
	for _, status := range []string{"confirmed", "cancelled", "missing"} {
		t.Run(status, func(t *testing.T) {
			request := validExcelPricingWritebackRequest("historical-currency-01", "yuan_price", 29500)
			var writes, reads atomic.Int32
			remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes.Add(1)
				}
				if !strings.Contains(r.URL.Path, "/currency/requests/") {
					reads.Add(1)
				}
				if status == "missing" {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]any{"success": false, "code": "digitalogic_currency_async_request_not_found"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"job_id": strings.Repeat("d", 32), "generation": 1, "request_id": request.RequestID, "status": status, "desired_currency": map[string]string{"yuan_price": "29500"}}})
			}))
			defer remote.Close()
			t.Setenv("DIGITALOGIC_PRICING_WRITE_KEY", "test-key")
			t.Setenv("DIGITALOGIC_PRICING_WRITE_SECRET", "test-secret")
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			server.excelPricing.client = remote.Client()
			dir := t.TempDir()
			now := time.Now().UTC()
			q := journalTestQueue(dir, now)
			q.server = server
			old, err := q.enqueue(request)
			if err != nil {
				t.Fatal(err)
			}
			q.jobs[old.JobID].Attempts = 1
			q.jobs[old.JobID].Status = "observation_required"
			latest, err := q.enqueue(validExcelPricingWritebackRequest("historical-currency-new", "yuan_price", 30000))
			if err != nil {
				t.Fatal(err)
			}
			restored := journalTestQueue(dir, now)
			restored.server = server
			if err = restored.loadCurrencyJournal(); err != nil {
				t.Fatal(err)
			}
			if restored.jobs[old.JobID].Status != "observation_required" {
				t.Fatal("unresolved historical intent hidden")
			}
			if _, err = restored.observeCurrency(old.JobID); err != nil {
				t.Fatal(err)
			}
			selected, _ := restored.next()
			if selected == nil || selected.JobID != old.JobID || !selected.ownerObserveOnly {
				t.Fatal("historical observation not scheduled")
			}
			result := restored.processOwnerCurrency(context.Background(), selected)
			restored.finish(selected, result)
			got := restored.jobs[old.JobID]
			if writes.Load() != 0 || reads.Load() != 0 || got.ConfirmedSettings != nil || len(got.ConfirmedValues) != 0 || restored.latestByKey["yuan_price"] != latest.JobID || restored.jobs[latest.JobID].Status != "observation_required" {
				t.Fatal("historical observation wrote or projected latest settings")
			}
			entries, err := restored.readCurrencyJournal()
			if err != nil {
				t.Fatal(err)
			}
			var terminal *currencyTerminalRecord
			for _, entry := range entries {
				if entry.record.JobID == old.JobID {
					terminal = entry.terminal
				}
			}
			if status == "missing" {
				if got.Status != "observation_required" || terminal != nil {
					t.Fatal("missing owner manufactured completion")
				}
			} else {
				if got.Status != "owner_terminal" || got.OwnerStatus != status || terminal == nil || terminal.OwnerStatus != status {
					t.Fatalf("terminal evidence lost: %+v", got)
				}
				if err = restored.pruneCurrencyJournal(now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				entries, err = restored.readCurrencyJournal()
				if err != nil || len(entries) != 1 {
					t.Fatalf("terminal retention not released: %d %v", len(entries), err)
				}
			}
		})
	}
}

func TestCurrencyConfirmedReadbackDriftRetainsCompletionAcrossRestart(t *testing.T) {
	request := validExcelPricingWritebackRequest("currency-readback-drift", "yuan_price", 29500)
	changed := request.Settings
	changed.YuanPrice = 31000
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected write")
		}
		if strings.Contains(r.URL.Path, "/currency/requests/") {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"job_id": strings.Repeat("d", 32), "generation": 1, "request_id": request.RequestID, "status": "confirmed", "desired_currency": map[string]string{"yuan_price": "29500"}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"state_revision": excelPricingRevisionForTest("changed"), "settings": changed}})
	}))
	defer remote.Close()
	t.Setenv("DIGITALOGIC_PRICING_WRITE_KEY", "test-key")
	t.Setenv("DIGITALOGIC_PRICING_WRITE_SECRET", "test-secret")
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.client = remote.Client()
	now := time.Now().UTC()
	q := journalTestQueue(t.TempDir(), now)
	q.server = server
	admitted, err := q.enqueue(request)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := q.next()
	result := q.processOwnerCurrency(context.Background(), selected)
	q.finish(selected, result)
	got := q.jobs[admitted.JobID]
	if got.Status != "owner_terminal" || got.OwnerStatus != "confirmed" || got.Code != "currency_owner_readback_changed" || got.ConfirmedSettings != nil {
		t.Fatalf("completion conflated with freshness: %+v", got)
	}
	restored := journalTestQueue(q.currencyJournalDir, now)
	restored.server = server
	if err = restored.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	if restored.jobs[got.JobID].OwnerStatus != "confirmed" || restored.jobs[got.JobID].Status != "owner_terminal" {
		t.Fatal("restart lost owner completion")
	}
	if err = restored.pruneCurrencyJournal(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, err := restored.readCurrencyJournal()
	if err != nil || len(entries) != 0 {
		t.Fatal("confirmed journal pinned by readback drift")
	}
}
