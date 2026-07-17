package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/gorilla/websocket"
	"github.com/xuri/excelize/v2"
)

// TestServerJSON tests the server with a JSON file
func TestServerJSON(t *testing.T) {
	// Create a temporary JSON file with test data
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := map[string]interface{}{
		"101": map[string]interface{}{
			"Code":    "101",
			"Name":    "Test Record 1",
			"ANBAR":   []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates":   "00.01.01",
			"FOROSH":  0,
			"Invahed": 1,
		},
		"102": map[string]interface{}{
			"Code":    "102",
			"Name":    "Test Record 2",
			"ANBAR":   []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates":   "00.01.01",
			"FOROSH":  0,
			"Invahed": 1,
		},
	}

	data, err := json.MarshalIndent(testData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	if err := os.WriteFile(jsonFile, data, 0644); err != nil {
		t.Fatalf("Failed to write test JSON file: %v", err)
	}

	// Create server
	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	t.Run("POST /api/refresh", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("failed to decode refresh response: %v", err)
		}
		if response["refreshed"] != true {
			t.Fatalf("expected refreshed=true, got %#v", response)
		}
	})

	// Test GET /api/records
	t.Run("GET /api/records", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		// The endpoint returns Code-keyed records (same format as convert command)
		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Check that we got 2 records (keyed by Code)
		if len(response) != 2 {
			t.Errorf("Expected 2 records, got %d", len(response))
		}

		// Verify the records have the expected Codes
		if _, ok := response["101"]; !ok {
			t.Error("Expected record with Code=101")
		}
		if _, ok := response["102"]; !ok {
			t.Error("Expected record with Code=102")
		}
	})

	t.Run("GET /api/records.csv", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records.csv", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Fatalf("Expected text/csv Content-Type, got %s", ct)
		}
		rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("Failed to parse CSV: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("Expected header plus 2 rows, got %d rows: %#v", len(rows), rows)
		}
		if rows[0][0] != "Code" {
			t.Fatalf("Expected Code as first CSV header, got %#v", rows[0])
		}
		codes := map[string]bool{rows[1][0]: true, rows[2][0]: true}
		if !codes["101"] || !codes["102"] {
			t.Fatalf("Expected CSV rows for codes 101 and 102, got %#v", codes)
		}
	})

	t.Run("GET /api/records?format=csv download", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records?format=csv&download=1", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "test.csv") {
			t.Fatalf("Expected CSV attachment filename, got %q", cd)
		}
	})

	t.Run("GET /api/records with CSV accept", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records", nil)
		req.Header.Set("Accept", "text/csv")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Fatalf("Expected text/csv Content-Type, got %s", ct)
		}
	})

	t.Run("GET /api/records.xlsx", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records?format=xlsx", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
			t.Fatalf("Expected XLSX Content-Type, got %s", ct)
		}
		if w.Body.Len() == 0 {
			t.Fatal("Expected non-empty XLSX body")
		}
	})

	t.Run("GET /api/records invalid format", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records?format=parquet", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("Expected status 400, got %d", w.Code)
		}
	})

	t.Run("GET /api/product-sync unavailable for generic dataset", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/product-sync", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Test GET / (welcome page)
	t.Run("GET /", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", ct)
		}
	})

	// Test GET /viewer
	t.Run("GET /viewer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/viewer", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", ct)
		}

		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Expected Cache-Control no-cache, got %s", cc)
		}
		body := w.Body.String()
		for _, marker := range []string{`id="exportXLSX"`, `role="menuitem"`, `/api/records.xlsx`} {
			if !strings.Contains(body, marker) {
				t.Errorf("viewer is missing accessible XLSX export marker %q", marker)
			}
		}
	})

	t.Run("GET /partials/welcome", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/partials/welcome", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", ct)
		}
		body := w.Body.String()
		if strings.Contains(strings.ToLower(body), "<html") || strings.Contains(strings.ToLower(body), "<body") {
			t.Fatalf("Expected partial HTML without document wrapper, got %q", body[:min(80, len(body))])
		}
		if !strings.Contains(body, "Launch Visualizer") {
			t.Fatalf("Expected welcome partial content")
		}
	})

	t.Run("GET /partials/charmap", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/partials/charmap", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
		body := w.Body.String()
		if strings.Contains(strings.ToLower(body), "<html") || strings.Contains(strings.ToLower(body), "<body") {
			t.Fatalf("Expected partial HTML without document wrapper, got %q", body[:min(80, len(body))])
		}
		if !strings.Contains(body, "Character Map Viewer") {
			t.Fatalf("Expected charmap partial content")
		}
	})

	t.Run("GET /api/app includes resources", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/app", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Expected Cache-Control no-store, got %s", cc)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		resources, ok := response["resources"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected resources object, got %T", response["resources"])
		}
		if resources["version"] == "" {
			t.Error("Expected non-empty resources.version")
		}
	})

	t.Run("GET /favicon.ico", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/favicon.ico", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "image/x-icon" {
			t.Errorf("Expected Content-Type image/x-icon, got %s", ct)
		}

		if w.Body.Len() == 0 {
			t.Error("Expected non-empty favicon")
		}
	})

	t.Run("GET /api/update/manifest", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/update/manifest", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Expected Cache-Control no-store, got %s", cc)
		}

		var manifest map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&manifest); err != nil {
			t.Fatalf("Failed to decode manifest: %v", err)
		}
		if manifest["filename"] == "" {
			t.Error("Expected manifest filename")
		}
		if manifest["sha256"] == "" {
			t.Error("Expected manifest sha256")
		}
		if manifest["size"].(float64) <= 0 {
			t.Error("Expected positive manifest size")
		}
		if !strings.Contains(manifest["download_url"].(string), "/api/update/executable") {
			t.Errorf("Expected manifest download URL to point at executable endpoint, got %s", manifest["download_url"])
		}
		if _, err := time.Parse(time.RFC3339, manifest["last_modified"].(string)); err != nil {
			t.Fatalf("Expected RFC3339 last_modified, got %v", manifest["last_modified"])
		}
	})

	t.Run("HEAD /api/update/executable has static headers", func(t *testing.T) {
		req := httptest.NewRequest("HEAD", "/api/update/executable", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}
		for _, header := range []string{"Content-Length", "Last-Modified", "Date", "ETag", "X-Checksum-SHA256", "X-Executable-Size", "X-Executable-Modified"} {
			if got := w.Header().Get(header); got == "" {
				t.Errorf("Expected %s header", header)
			}
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Expected executable content type, got %s", ct)
		}
		if w.Body.Len() != 0 {
			t.Errorf("Expected empty HEAD body, got %d bytes", w.Body.Len())
		}
	})

	t.Run("GET /api/update/executable supports ranges", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/update/executable", nil)
		req.Header.Set("Range", "bytes=0-15")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusPartialContent {
			t.Fatalf("Expected status 206, got %d", w.Code)
		}
		if w.Body.Len() != 16 {
			t.Errorf("Expected 16 response bytes, got %d", w.Body.Len())
		}
		if got := w.Header().Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-15/") {
			t.Errorf("Unexpected Content-Range: %s", got)
		}
	})

	t.Run("GET /api/source/manifest", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/source/manifest", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Expected Cache-Control no-store, got %s", cc)
		}

		var manifest map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&manifest); err != nil {
			t.Fatalf("Failed to decode manifest: %v", err)
		}
		if manifest["filename"] != filepath.Base(jsonFile) {
			t.Fatalf("Expected source filename %q, got %v", filepath.Base(jsonFile), manifest["filename"])
		}
		if manifest["sha256"] == "" {
			t.Error("Expected source sha256")
		}
		if manifest["size"].(float64) <= 0 {
			t.Error("Expected positive source size")
		}
		if !strings.Contains(manifest["download_url"].(string), "/api/source/file") {
			t.Errorf("Expected source download URL, got %s", manifest["download_url"])
		}
	})

	t.Run("HEAD /api/source/file has static headers", func(t *testing.T) {
		req := httptest.NewRequest("HEAD", "/api/source/file", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}
		for _, header := range []string{"Content-Length", "Last-Modified", "Date", "ETag", "X-Checksum-SHA256", "X-Source-File", "X-Source-Size", "X-Source-Modified"} {
			if got := w.Header().Get(header); got == "" {
				t.Errorf("Expected %s header", header)
			}
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Expected source file content type, got %s", ct)
		}
		if w.Body.Len() != 0 {
			t.Errorf("Expected empty HEAD body, got %d bytes", w.Body.Len())
		}
	})

	t.Run("GET /api/source/file supports ranges", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/source/file", nil)
		req.Header.Set("Range", "bytes=0-9")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusPartialContent {
			t.Fatalf("Expected status 206, got %d", w.Code)
		}
		if w.Body.Len() != 10 {
			t.Errorf("Expected 10 response bytes, got %d", w.Body.Len())
		}
		if got := w.Header().Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-9/") {
			t.Errorf("Unexpected Content-Range: %s", got)
		}
	})

	t.Run("POST /api/edge/upload switches active source", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("source_id", "test-edge"); err != nil {
			t.Fatalf("write source_id: %v", err)
		}
		if err := writer.WriteField("file_name", "edge.json"); err != nil {
			t.Fatalf("write file_name: %v", err)
		}
		part, err := writer.CreateFormFile("file", "edge.json")
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write([]byte(`{"777":{"Code":"777","Name":"Uploaded Edge Record"}}`)); err != nil {
			t.Fatalf("write file part: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/edge/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode upload response: %v", err)
		}
		if response["success"] != true {
			t.Fatalf("Expected success=true, got %v", response["success"])
		}
		if response["records"].(float64) != 1 {
			t.Fatalf("Expected records=1, got %v", response["records"])
		}

		req = httptest.NewRequest("GET", "/api/records", nil)
		w = httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected records status 200, got %d", w.Code)
		}
		var records map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
			t.Fatalf("decode records: %v", err)
		}
		if _, ok := records["777"]; !ok {
			t.Fatalf("Expected uploaded record 777, got keys %+v", records)
		}
	})
}

func TestCanonicalKalaParityAcrossRESTCSVXLSXAndWebSocket(t *testing.T) {
	configPath := filepath.Join("..", "..", "testdata", "canonical-static-config.json")
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("load canonical fixture config: %v", err)
	}
	dbPath := filepath.Join("..", "..", "testdata", "kala.db")
	srv, err := NewServerWithOptions(dbPath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create canonical server: %v", err)
	}
	defer srv.Close()

	request := httptest.NewRequest(http.MethodGet, "/api/records", nil)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("REST status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var keyedProducts map[string]map[string]interface{}
	if err := json.NewDecoder(recorder.Body).Decode(&keyedProducts); err != nil {
		t.Fatalf("decode canonical REST product rows: %v", err)
	}
	if len(keyedProducts) != 354 {
		t.Fatalf("canonical /api/records returned %d top-level entries, want 354 product rows", len(keyedProducts))
	}
	for _, metadata := range []string{"schema", "schema_version", "event", "event_id", "products"} {
		if _, leaked := keyedProducts[metadata]; leaked {
			t.Fatalf("canonical envelope metadata %q was exposed as a viewer row", metadata)
		}
	}
	restProduct, ok := keyedProducts["102001011"]
	if !ok {
		t.Fatalf("canonical product Code 102001011 missing from keyed REST rows")
	}
	restProduct["product_code"] = "102001011"
	assertCanonicalFixtureValues(t, "REST", restProduct)

	contractRequest := httptest.NewRequest(http.MethodGet, "/api/product-sync", nil)
	contractRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(contractRecorder, contractRequest)
	if contractRecorder.Code != http.StatusOK {
		t.Fatalf("contract status = %d: %s", contractRecorder.Code, contractRecorder.Body.String())
	}
	if contentType := contractRecorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/vnd.digitalogic.product-sync+json") {
		t.Fatalf("unexpected contract content type %q", contentType)
	}
	var envelope canonical.Envelope
	if err := json.NewDecoder(contractRecorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode dedicated canonical REST envelope: %v", err)
	}
	contractRESTProduct := canonicalTypedProductByCode(t, envelope.Products, "102001011")
	assertJSONEquivalent(t, "records and dedicated contract", restProduct, contractRESTProduct)

	csvRequest := httptest.NewRequest(http.MethodGet, "/api/records.csv", nil)
	csvRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(csvRecorder, csvRequest)
	csvRows, err := csv.NewReader(strings.NewReader(csvRecorder.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse canonical CSV: %v", err)
	}
	assertTabularCanonicalFixture(t, "CSV", csvRows)

	xlsxRequest := httptest.NewRequest(http.MethodGet, "/api/records.xlsx?download=1&rtl=1", nil)
	xlsxRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(xlsxRecorder, xlsxRequest)
	if contentDisposition := xlsxRecorder.Header().Get("Content-Disposition"); !strings.Contains(contentDisposition, "kala.xlsx") {
		t.Fatalf("canonical XLSX attachment filename = %q", contentDisposition)
	}
	book, err := excelize.OpenReader(bytes.NewReader(xlsxRecorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("open canonical XLSX: %v", err)
	}
	xlsxRows, err := book.GetRows("Records", excelize.Options{RawCellValue: true})
	if err != nil {
		_ = book.Close()
		t.Fatalf("read canonical XLSX: %v", err)
	}
	assertTabularCanonicalFixture(t, "XLSX", xlsxRows)
	metadataRows, err := book.GetRows("Metadata")
	if err != nil {
		_ = book.Close()
		t.Fatalf("read canonical XLSX metadata: %v", err)
	}
	metadata := map[string]string{}
	for _, row := range metadataRows[1:] {
		if len(row) >= 2 {
			metadata[row[0]] = row[1]
		}
	}
	for key, want := range map[string]string{
		"schema":           envelope.Schema,
		"schema_version":   envelope.SchemaVersion,
		"formula_id":       envelope.FormulaID,
		"formula_revision": envelope.FormulaRevision,
		"source_dataset":   envelope.Source.Dataset,
		"source_revision":  envelope.Source.Revision,
	} {
		if metadata[key] != want {
			_ = book.Close()
			t.Fatalf("canonical XLSX metadata[%s] = %q, want %q", key, metadata[key], want)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, metadata["generated_at"]); err != nil {
		_ = book.Close()
		t.Fatalf("canonical XLSX generated_at = %q: %v", metadata["generated_at"], err)
	}
	view, err := book.GetSheetView("Records", 0)
	if err != nil || view.RightToLeft == nil || !*view.RightToLeft {
		_ = book.Close()
		t.Fatalf("canonical XLSX RTL view = %+v, err=%v", view.RightToLeft, err)
	}
	metadataJSON, _ := json.Marshal(metadata)
	for _, forbidden := range []string{"mysql_dsn", "password", "secret", "Sharh1", "Sharh2", `C:\\Patris`} {
		if strings.Contains(strings.ToLower(string(metadataJSON)), strings.ToLower(forbidden)) {
			_ = book.Close()
			t.Fatalf("canonical XLSX metadata leaked %q: %s", forbidden, metadataJSON)
		}
	}
	_ = book.Close()

	httpServer := httptest.NewServer(srv.router)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial canonical websocket: %v", err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var message struct {
		Type     string                   `json:"type"`
		Added    []map[string]interface{} `json:"added"`
		Contract canonical.Envelope       `json:"contract"`
	}
	if err := ws.ReadJSON(&message); err != nil {
		t.Fatalf("read canonical websocket snapshot: %v", err)
	}
	if message.Type != "initial" || message.Contract.Schema != canonical.ContractName {
		t.Fatalf("websocket contract metadata missing: %+v", message)
	}
	wsProduct := canonicalProductByCode(t, message.Added, "102001011")
	assertCanonicalFixtureValues(t, "WebSocket", wsProduct)
	contractProduct := canonicalTypedProductByCode(t, message.Contract.Products, "102001011")
	assertJSONEquivalent(t, "REST and WebSocket contract", restProduct, contractProduct)
}

func assertJSONEquivalent(t *testing.T, source string, left, right interface{}) {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal %s left value: %v", source, err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("marshal %s right value: %v", source, err)
	}
	if !bytes.Equal(leftJSON, rightJSON) {
		t.Fatalf("%s values differ:\nleft=%s\nright=%s", source, leftJSON, rightJSON)
	}
}

func canonicalProductByCode(t *testing.T, products []map[string]interface{}, code string) map[string]interface{} {
	t.Helper()
	for _, product := range products {
		if product["product_code"] == code {
			return product
		}
	}
	t.Fatalf("canonical product %s not found", code)
	return nil
}

func canonicalTypedProductByCode(t *testing.T, products []canonical.Product, code string) map[string]interface{} {
	t.Helper()
	for _, product := range products {
		if product.ProductCode == code {
			return product.Map()
		}
	}
	t.Fatalf("canonical product %s not found", code)
	return nil
}

func assertCanonicalFixtureValues(t *testing.T, source string, product map[string]interface{}) {
	t.Helper()
	if fmt.Sprint(product["foreign_price"]) != "2.75" || fmt.Sprint(product["weight_grams"]) != "1.84" || fmt.Sprint(product["total_stock"]) != "20" || fmt.Sprint(product["final_price"]) != "111999" {
		t.Fatalf("%s canonical values differ: %#v", source, product)
	}
	for _, raw := range []string{"Sharh1", "Sharh2", "FOROSH", "KHARYD", "ALLANBAR", "ANBAR"} {
		if _, exists := product[raw]; exists {
			t.Fatalf("%s leaked raw field %s", source, raw)
		}
	}
}

func assertTabularCanonicalFixture(t *testing.T, source string, rows [][]string) {
	t.Helper()
	if len(rows) < 2 {
		t.Fatalf("%s has no data rows", source)
	}
	columns := map[string]int{}
	for index, field := range rows[0] {
		columns[field] = index
	}
	for _, required := range []string{"product_code", "foreign_price", "weight_grams", "total_stock", "final_price", "record_hash"} {
		if _, exists := columns[required]; !exists {
			t.Fatalf("%s missing canonical column %s: %v", source, required, rows[0])
		}
	}
	for _, row := range rows[1:] {
		if len(row) <= columns["product_code"] || row[columns["product_code"]] != "102001011" {
			continue
		}
		for field, expected := range map[string]string{"foreign_price": "2.75", "weight_grams": "1.84", "total_stock": "20", "final_price": "111999"} {
			if len(row) <= columns[field] || row[columns[field]] != expected {
				t.Fatalf("%s %s = %q, want %q", source, field, row[columns[field]], expected)
			}
		}
		return
	}
	t.Fatalf("%s did not contain fixture Code", source)
}

// TestWebSocketUpdates tests WebSocket broadcasting of changes
func TestWebSocketUpdates(t *testing.T) {
	// Create a temporary JSON file
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := map[string]interface{}{
		"101": map[string]interface{}{
			"Code":  "101",
			"Name":  "Original",
			"ANBAR": []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates": "00.01.01",
		},
	}

	writeJSON := func(data map[string]interface{}) {
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal JSON: %v", err)
		}
		if err := os.WriteFile(jsonFile, jsonData, 0644); err != nil {
			t.Fatalf("Failed to write JSON file: %v", err)
		}
	}

	writeJSON(testData)

	// Create server with file watching
	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	// Start file watching with 0 debounce for tests
	if err := srv.StartWatching(0); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}

	// Start test HTTP server
	testServer := httptest.NewServer(srv.router)
	defer testServer.Close()

	// Connect WebSocket client
	wsURL := "ws" + testServer.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WebSocket: %v", err)
	}
	defer ws.Close()

	// Read initial message
	var initialMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))

	if err := ws.ReadJSON(&initialMsg); err != nil {
		t.Fatalf("Failed to read initial message: %v", err)
	}

	if initialMsg["type"] != "initial" {
		t.Errorf("Expected type=initial, got %v", initialMsg["type"])
	}

	if resources, ok := initialMsg["resources"].(map[string]interface{}); !ok {
		t.Fatalf("Expected resources object in initial message, got %T", initialMsg["resources"])
	} else if resources["version"] == "" {
		t.Error("Expected non-empty resources.version in initial message")
	}

	// Verify initial data has 1 record
	if records, ok := initialMsg["records"].(map[string]interface{}); ok {
		if len(records) != 1 {
			t.Errorf("Expected 1 initial record, got %d", len(records))
		}
	}

	// Give file watcher time to settle after initial file creation
	time.Sleep(200 * time.Millisecond)

	// Now modify the JSON file (add a new record)
	testData["102"] = map[string]interface{}{
		"Code":  "102",
		"Name":  "New Record",
		"ANBAR": []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"Dates": "00.01.01",
	}
	writeJSON(testData)

	// Wait for file watcher to detect change and broadcast
	time.Sleep(500 * time.Millisecond)

	// Read update message (may get multiple due to file watcher debounce, skip empty ones)
	var updateMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if err := ws.ReadJSON(&updateMsg); err != nil {
			t.Fatalf("Failed to read update message: %v", err)
		}
		// Skip empty updates
		if updateMsg["added"] != nil {
			break
		}
	}

	if updateMsg["type"] != "update" {
		t.Errorf("Expected type=update, got %v", updateMsg["type"])
	}

	// Check for added records
	if added, ok := updateMsg["added"].([]interface{}); ok {
		if len(added) != 1 {
			t.Errorf("Expected 1 added record, got %d", len(added))
		}
	} else {
		t.Error("Expected added field in update message")
	}

	// Delete a record
	delete(testData, "101")
	writeJSON(testData)

	// Wait for file watcher to detect change
	time.Sleep(500 * time.Millisecond)

	// Read delete message (may get multiple due to file watcher debounce, skip empty ones)
	var deleteMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if err := ws.ReadJSON(&deleteMsg); err != nil {
			t.Fatalf("Failed to read delete message: %v", err)
		}
		// Skip empty updates
		if deleteMsg["deleted"] != nil {
			break
		}
	}

	if deleteMsg["type"] != "update" {
		t.Errorf("Expected type=update, got %v", deleteMsg["type"])
	}

	// Check for deleted records
	if deleted, ok := deleteMsg["deleted"].([]interface{}); ok {
		if len(deleted) != 1 {
			t.Errorf("Expected 1 deleted record, got %d", len(deleted))
		}
		if deleted[0] != "101" {
			t.Errorf("Expected deleted code=101, got %v", deleted[0])
		}
	} else {
		t.Error("Expected deleted field in update message")
	}
}

// TestComputeChanges tests the change detection logic
func TestComputeChanges(t *testing.T) {
	srv := &Server{}

	// Test case 1: No previous records (all new)
	newRecords := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "102", "Name": "Record 2"},
	}

	changes := srv.computeChanges(newRecords)

	if changes["type"] != "update" {
		t.Errorf("Expected type=update, got %v", changes["type"])
	}

	if added, ok := changes["added"].([]map[string]interface{}); ok {
		if len(added) != 2 {
			t.Errorf("Expected 2 added records, got %d", len(added))
		}
	} else {
		t.Error("Expected added field")
	}

	// Test case 2: Add a record
	srv.lastRecords = newRecords
	newRecords2 := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "102", "Name": "Record 2"},
		{"Code": "103", "Name": "Record 3"},
	}

	changes = srv.computeChanges(newRecords2)

	if added, ok := changes["added"].([]map[string]interface{}); ok {
		if len(added) != 1 {
			t.Errorf("Expected 1 added record, got %d", len(added))
		}
		if added[0]["Code"] != "103" {
			t.Errorf("Expected added code=103, got %v", added[0]["Code"])
		}
	} else {
		t.Error("Expected added field")
	}

	// Test case 3: Delete a record
	srv.lastRecords = newRecords2
	newRecords3 := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "103", "Name": "Record 3"},
	}

	changes = srv.computeChanges(newRecords3)

	if deleted, ok := changes["deleted"].([]string); ok {
		if len(deleted) != 1 {
			t.Errorf("Expected 1 deleted record, got %d", len(deleted))
		}
		if deleted[0] != "102" {
			t.Errorf("Expected deleted code=102, got %v", deleted[0])
		}
	} else {
		t.Error("Expected deleted field")
	}
}

func TestClientSnapshotSeedDoesNotReplaceWatcherBaseline(t *testing.T) {
	srv := &Server{
		lastRecords:      []map[string]interface{}{{"Code": "watcher"}},
		lastRecordsReady: true,
	}
	srv.seedLastRecords([]map[string]interface{}{{"Code": "client"}})
	if len(srv.lastRecords) != 1 || srv.lastRecords[0]["Code"] != "watcher" {
		t.Fatalf("client snapshot replaced watcher baseline: %#v", srv.lastRecords)
	}

	empty := &Server{}
	empty.seedLastRecords([]map[string]interface{}{})
	empty.seedLastRecords([]map[string]interface{}{{"Code": "later-client"}})
	if !empty.lastRecordsReady || len(empty.lastRecords) != 0 {
		t.Fatalf("initialized empty baseline was replaced: %#v", empty.lastRecords)
	}
}

// TestNotificationAudioEndpoint tests the /static/notification.ogg endpoint
func TestNotificationAudioEndpoint(t *testing.T) {
	// Create a minimal server for testing the audio endpoint
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := map[string]interface{}{
		"101": map[string]interface{}{
			"Code": "101",
			"Name": "Test",
		},
	}

	data, err := json.MarshalIndent(testData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test JSON data: %v", err)
	}
	if err := os.WriteFile(jsonFile, data, 0644); err != nil {
		t.Fatalf("Failed to write test JSON file: %v", err)
	}

	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	t.Run("GET /static/notification.ogg - full file", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "audio/ogg" {
			t.Errorf("Expected Content-Type audio/ogg, got %s", ct)
		}

		if ar := w.Header().Get("Accept-Ranges"); ar != "bytes" {
			t.Errorf("Expected Accept-Ranges bytes, got %s", ar)
		}

		if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("Expected Cache-Control with max-age=31536000, got %s", cc)
		}

		if w.Body.Len() == 0 {
			t.Error("Expected non-empty audio file")
		}
	})

	t.Run("GET /static/notification.ogg - Range request (bytes=0-99)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		req.Header.Set("Range", "bytes=0-99")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusPartialContent {
			t.Errorf("Expected status 206, got %d", w.Code)
		}

		if w.Body.Len() != 100 {
			t.Errorf("Expected 100 bytes, got %d", w.Body.Len())
		}

		if cr := w.Header().Get("Content-Range"); cr == "" {
			t.Error("Expected Content-Range header")
		}
	})

	t.Run("GET /static/notification.ogg - Range request (bytes=100-)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		req.Header.Set("Range", "bytes=100-")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusPartialContent {
			t.Errorf("Expected status 206, got %d", w.Code)
		}

		if cr := w.Header().Get("Content-Range"); cr == "" {
			t.Error("Expected Content-Range header")
		}
	})

	t.Run("GET /static/notification.ogg - Range request (bytes=-500)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		req.Header.Set("Range", "bytes=-500")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusPartialContent {
			t.Errorf("Expected status 206, got %d", w.Code)
		}

		// Should return last 500 bytes or entire file if smaller
		if w.Body.Len() == 0 {
			t.Error("Expected non-empty response")
		}
	})

	t.Run("GET /static/notification.ogg - Invalid Range request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		req.Header.Set("Range", "bytes=invalid")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("Expected status 416, got %d", w.Code)
		}
	})

	t.Run("GET /static/notification.ogg - Out of bounds Range", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/notification.ogg", nil)
		req.Header.Set("Range", "bytes=999999999-")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("Expected status 416, got %d", w.Code)
		}
	})
}

func TestProcessEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")
	if err := os.WriteFile(jsonFile, []byte(`{"101":{"Code":"101","Name":"Test"}}`), 0644); err != nil {
		t.Fatalf("Failed to write test JSON file: %v", err)
	}

	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	t.Run("GET /api/processes/patris81", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/processes/patris81", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		if response["success"] != true {
			t.Errorf("Expected success=true, got %v", response["success"])
		}
		if _, ok := response["processes"].([]interface{}); !ok {
			t.Errorf("Expected processes array, got %T", response["processes"])
		}
	})

	t.Run("GET /api/processes/file", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/processes/file", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		if response["file"] != filepath.Base(jsonFile) {
			t.Errorf("Expected file basename %q, got %v", filepath.Base(jsonFile), response["file"])
		}
		if response["path"] == "" {
			t.Error("Expected full path in response")
		}
		if _, ok := response["processes"].([]interface{}); !ok {
			t.Errorf("Expected processes array, got %T", response["processes"])
		}
	})
}

func TestBroadcastProcessInfo(t *testing.T) {
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")
	if err := os.WriteFile(jsonFile, []byte(`{"101":{"Code":"101","Name":"Test"}}`), 0644); err != nil {
		t.Fatalf("Failed to write test JSON file: %v", err)
	}

	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	testServer := httptest.NewServer(srv.router)
	defer testServer.Close()

	wsURL := "ws" + testServer.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WebSocket: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("Failed to read process info message: %v", err)
		}
		if msg["type"] != "process_info" {
			continue
		}
		if _, ok := msg["status"].(map[string]interface{}); !ok {
			t.Fatalf("Expected status object in process_info message, got %T", msg["status"])
		}
		return
	}
}
