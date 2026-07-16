package updateout

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
)

func TestEncodeCSVFullPayload(t *testing.T) {
	body, contentType, err := encode(Config{Format: "csv", Mode: "full"}, Event{
		Type:     "initial",
		KeyField: "sku",
		Records: []map[string]interface{}{
			{"sku": "100", "title": "Bolt"},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("expected CSV content type, got %q", contentType)
	}
	if !strings.Contains(string(body), "sku,title") || !strings.Contains(string(body), "100,Bolt") {
		t.Fatalf("unexpected CSV body: %s", body)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	cfg := Normalize(Config{})
	if cfg.Method != "POST" || cfg.Format != "json" || cfg.Mode != "changes" || cfg.Timeout != "10s" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestEncodeJSONChangesKeepsChangeSetAndDropsFullRecords(t *testing.T) {
	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 2,
		Added:      []map[string]interface{}{{"sku": "200", "title": "Nut"}},
	}
	body, contentType, err := encode(Config{Format: "json", Mode: "changes"}, Event{
		Type:     "update",
		Records:  []map[string]interface{}{{"sku": "100", "title": "Bolt"}},
		Changes:  &changes,
		KeyField: "sku",
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, exists := envelope["records"]; exists {
		t.Fatalf("change mode must omit the full records payload: %s", body)
	}
	encodedChanges, ok := envelope["changes"].(map[string]interface{})
	if !ok || len(encodedChanges["added"].([]interface{})) != 1 {
		t.Fatalf("expected a non-empty changeset: %s", body)
	}
}

func TestEncodeCSVChangesIncludesTypedModifiedRowsAndDeletionTombstones(t *testing.T) {
	changes := recorddiff.ChangeSet{
		Type:       "update",
		KeyField:   "sku",
		TotalCount: 2,
		Added:      []map[string]interface{}{{"sku": "300", "title": "Washer", "price": 3}},
		Modified: []recorddiff.RecordChange{{
			Code:          "100",
			ChangeType:    "modified",
			ChangedFields: []string{"price"},
			NewValues:     map[string]interface{}{"price": 12},
			Record:        map[string]interface{}{"sku": "100", "title": "Bolt", "price": 12},
		}},
		Deleted: []string{"200"},
	}
	body, _, err := encode(Config{Format: "csv", Mode: "changes"}, Event{
		Type:     "update",
		Changes:  &changes,
		KeyField: "sku",
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	text := string(body)
	for _, expected := range []string{"_change_type", "_changed_fields", "added", "modified", "deleted", "100", "200", "300", "Bolt", "price"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("CSV changes are missing %q:\n%s", expected, text)
		}
	}
}

func TestDispatchHTTPChangePayloadAndHeaders(t *testing.T) {
	var method string
	var eventHeader string
	var sourceHeader string
	var customHeader string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		eventHeader = r.Header.Get("X-Patris-Event")
		sourceHeader = r.Header.Get("X-Patris-Source")
		customHeader = r.Header.Get("X-Integration-Test")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 1,
		Modified: []recorddiff.RecordChange{{
			Code:          "100",
			ChangeType:    "modified",
			ChangedFields: []string{"price"},
			NewValues:     map[string]interface{}{"price": 12},
			Record:        map[string]interface{}{"sku": "100", "title": "Bolt", "price": 12},
		}},
	}
	err := Dispatch(t.Context(), Config{
		Enabled: true,
		URL:     server.URL,
		Method:  http.MethodPut,
		Format:  "json",
		Mode:    "changes",
		Headers: map[string]string{"X-Integration-Test": "yes"},
	}, Event{
		Type:      "update",
		Timestamp: changes.Timestamp,
		Source:    "kala.db",
		Records:   []map[string]interface{}{{"sku": "100", "price": 12}},
		Changes:   &changes,
		KeyField:  "sku",
	})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if method != http.MethodPut || eventHeader != "update" || sourceHeader != "kala.db" || customHeader != "yes" {
		t.Fatalf("unexpected request metadata: method=%q event=%q source=%q custom=%q", method, eventHeader, sourceHeader, customHeader)
	}
	if !strings.Contains(string(body), `"modified"`) || !strings.Contains(string(body), `"sku": "100"`) {
		t.Fatalf("HTTP body did not contain the typed changeset: %s", body)
	}
	if strings.Contains(string(body), `"records"`) {
		t.Fatalf("change-mode HTTP body included the full records snapshot: %s", body)
	}
}

func TestDispatchHTTPReportsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := Dispatch(t.Context(), Config{Enabled: true, URL: server.URL}, Event{Type: "update"})
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("expected non-2xx error, got %v", err)
	}
}

func TestDispatchCanonicalContractUsesDirectDigitalogicEnvelope(t *testing.T) {
	var body []byte
	headers := http.Header{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	product := canonical.Product{ProductCode: "113007045", FormulaVersion: canonical.FormulaVersion, RecordHash: "sha256:record"}
	contract := canonical.NewEnvelope([]canonical.Product{product}, "kala.db", "patris-office", time.Unix(1, 0))
	err := Dispatch(t.Context(), Config{Enabled: true, URL: server.URL, Format: "json", Mode: "changes"}, Event{
		Type: "update", Source: "kala.db", KeyField: "product_code",
		Records:  []map[string]interface{}{{"Code": "113007045", "Sharh1": "must-not-cross"}},
		Contract: contract,
	})
	if err != nil {
		t.Fatalf("canonical dispatch failed: %v", err)
	}
	if headers.Get("X-Patris-Contract") != canonical.ContractName || headers.Get("X-Patris-Contract-Version") != canonical.ContractVersion || headers.Get("X-Patris-Event-ID") != contract.EventID {
		t.Fatalf("contract identity headers missing: %v", headers)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("webhook body is not a direct contract: %v\n%s", err, body)
	}
	if decoded.Schema != canonical.ContractName || len(decoded.Products) != 1 || decoded.Products[0].ProductCode != "113007045" {
		t.Fatalf("unexpected webhook contract: %+v", decoded)
	}
	text := string(body)
	for _, forbidden := range []string{"\"records\"", "Sharh1", "must-not-cross", "FOROSH", "KHARYD", "ALLANBAR"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("canonical webhook leaked %q: %s", forbidden, body)
		}
	}
}

func TestCanonicalFullModeSelectsSnapshotInsteadOfDelta(t *testing.T) {
	generated := time.Unix(1, 0)
	snapshot := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "A", RecordHash: "sha256:a"},
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", generated)
	delta := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", generated)
	delta.EventType = "update"
	body, _, err := encode(Config{Format: "json", Mode: "full"}, Event{
		Type: "update", Contract: delta, SnapshotContract: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Products) != 2 || decoded.EventID != snapshot.EventID {
		t.Fatalf("full mode did not select the snapshot: %+v", decoded)
	}
}

func TestCanonicalChangeModeCarriesDeletedCodeTombstone(t *testing.T) {
	snapshot := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", time.Unix(1, 0))
	changes := recorddiff.ChangeSet{Type: "update", KeyField: "product_code", Deleted: []string{"A"}}
	delta := canonical.ChangeEnvelope(snapshot, &changes)
	body, _, err := encode(Config{Format: "json", Mode: "changes"}, Event{
		Type: "update", Contract: delta, SnapshotContract: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventType != "update" || len(decoded.DeletedCodes) != 1 || decoded.DeletedCodes[0].ProductCode != "A" || !decoded.DeletedCodes[0].Deleted {
		t.Fatalf("canonical webhook tombstone missing: %+v", decoded)
	}
}

func TestDispatchRejectsRawOutboundUnlessExplicitlyAllowed(t *testing.T) {
	cfg := Config{Enabled: true, Command: []string{"must-not-run"}}
	err := Dispatch(t.Context(), cfg, Event{Type: "update", Raw: true, Records: []map[string]interface{}{{"Sharh1": "secret"}}})
	if err == nil || !strings.Contains(err.Error(), "raw outbound updates are disabled") {
		t.Fatalf("raw outbound event was not rejected: %v", err)
	}
}

func TestDispatchRequireContractRejectsGenericNonRawPayload(t *testing.T) {
	cfg := Config{Enabled: true, RequireContract: true, Command: []string{"must-not-run"}}
	err := Dispatch(t.Context(), cfg, Event{Type: "update", Records: []map[string]interface{}{{"Sharh1": "must-not-cross"}}})
	if err == nil || !strings.Contains(err.Error(), "requires a canonical contract") {
		t.Fatalf("generic payload bypassed require_contract: %v", err)
	}

	cfg.Format = "csv"
	err = Dispatch(t.Context(), cfg, Event{Type: "update", Contract: &canonical.Envelope{Schema: canonical.ContractName}})
	if err == nil || !strings.Contains(err.Error(), "requires JSON") {
		t.Fatalf("CSV bypassed require_contract: %v", err)
	}
}

func TestDispatchCommandReceivesChangePayloadAndMetadata(t *testing.T) {
	t.Setenv("GO_WANT_UPDATEOUT_HELPER", "1")
	output := filepath.Join(t.TempDir(), "payload.json")
	t.Setenv("UPDATEOUT_HELPER_FILE", output)

	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 1,
		Added:      []map[string]interface{}{{"sku": "100", "title": "Bolt"}},
	}
	err := Dispatch(t.Context(), Config{
		Enabled: true,
		Command: []string{os.Args[0], "-test.run=TestUpdateoutHelperProcess", "--"},
		Format:  "json",
		Mode:    "changes",
	}, Event{
		Type:      "update",
		Timestamp: changes.Timestamp,
		Source:    "kala.db",
		Changes:   &changes,
		KeyField:  "sku",
	})
	if err != nil {
		t.Fatalf("command dispatch failed: %v", err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read helper payload: %v", err)
	}
	if !strings.Contains(string(body), `"added"`) || !strings.Contains(string(body), `"100"`) {
		t.Fatalf("command received an incomplete payload: %s", body)
	}
}

func TestUpdateoutHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_UPDATEOUT_HELPER") != "1" {
		return
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if os.Getenv("PATRIS_EXPORT_EVENT_TYPE") != "update" || os.Getenv("PATRIS_EXPORT_EVENT_KEY_FIELD") != "sku" {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("UPDATEOUT_HELPER_FILE"), payload, 0o600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
