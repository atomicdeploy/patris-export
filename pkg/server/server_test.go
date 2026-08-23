package server

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
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
		if len(response) != 1 {
			t.Fatalf("default refresh response changed: %#v", response)
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
		srv.dataSourceMu.Lock()
		originalDataSource := srv.dataSource
		srv.dataSource = nil
		srv.dataSourceMu.Unlock()
		defer func() {
			srv.dataSourceMu.Lock()
			srv.dataSource = originalDataSource
			srv.dataSourceMu.Unlock()
		}()

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
		body := w.Body.String()
		for _, marker := range []string{`data-page-i18n="homeHeroTitle"`, `id="languageSelect"`, `data-page-icon="moon"`, `font-family:'Vazirmatn'`} {
			if !strings.Contains(body, marker) {
				t.Errorf("welcome page is missing localized embedded marker %q", marker)
			}
		}
		for _, emoji := range []string{"🌙", "☀️", "🚀", "🌗", "⚡", "🔍", "📱", "🔄"} {
			if strings.Contains(body, emoji) {
				t.Errorf("welcome page still contains raw emoji glyph %q", emoji)
			}
		}
	})

	t.Run("GET /debug/charmap", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/debug/charmap", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
		body := w.Body.String()
		for _, marker := range []string{`data-page-i18n="charmapPageTitle"`, `data-page-i18n-placeholder="filterCharmap"`, `id="languageSelect"`, `font-family:'Vazirmatn'`} {
			if !strings.Contains(body, marker) {
				t.Errorf("character-map page is missing localized embedded marker %q", marker)
			}
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
		for _, marker := range []string{"Launch Visualizer", `data-page-i18n="homeHeroTitle"`, `id="languageSelect"`} {
			if !strings.Contains(body, marker) {
				t.Fatalf("Expected welcome partial marker %q", marker)
			}
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
		for _, marker := range []string{"Character Map Viewer", `data-page-i18n="charmapPageTitle"`, `id="languageSelect"`} {
			if !strings.Contains(body, marker) {
				t.Fatalf("Expected charmap partial marker %q", marker)
			}
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

func TestOfficeRecordDownloadsEnforceDistinctTrustedContracts(t *testing.T) {
	tmpDir := t.TempDir()
	datasetPath := filepath.Join(tmpDir, "records.json")
	if err := os.WriteFile(datasetPath, []byte(`{"1":{"Code":"1","Name":"Fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	xlsxPath := createServerXLSXPackage(t)
	xlsmPath := createServerMacroPackage(t, ".xlsm", false)
	xltmPath := createServerMacroPackage(t, ".xltm", false)
	xltmBytes, err := os.ReadFile(xltmPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Default()
	cfg.Export.XLSXTemplate = xlsxPath
	cfg.Export.XLSXTarget = "table:ExportProducts"
	cfg.Export.XLSMTemplate = xlsmPath
	cfg.Export.XLSMTarget = "table:ExportProducts"
	cfg.Export.XLTMTemplate = xltmPath
	cfg.Export.XLTMTarget = "table:ExportProducts"
	configPath := filepath.Join(tmpDir, "config.json")
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerWithOptions(datasetPath, nil, Options{Config: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	xlsxRequest := httptest.NewRequest(http.MethodGet, "/api/records.xlsx?download=1", nil)
	xlsxResponse := httptest.NewRecorder()
	srv.router.ServeHTTP(xlsxResponse, xlsxRequest)
	if xlsxResponse.Code != http.StatusOK {
		t.Fatalf("XLSX status = %d: %s", xlsxResponse.Code, xlsxResponse.Body.String())
	}
	if got := xlsxResponse.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("XLSX content type = %q", got)
	}
	if xlsxResponse.Header().Get("X-Patris-Office-Record-Count") != "1" ||
		xlsxResponse.Header().Get("X-Patris-Office-Source-SHA256") == "" ||
		xlsxResponse.Header().Get("X-Patris-Office-Source-SHA256") == xlsxResponse.Header().Get("X-Patris-Office-Output-SHA256") {
		t.Fatalf("XLSX provenance headers = %+v", xlsxResponse.Header())
	}
	xlsxBook, err := excelize.OpenReader(bytes.NewReader(xlsxResponse.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer xlsxBook.Close()
	if code, _ := xlsxBook.GetCellValue("Records", "A2", excelize.Options{RawCellValue: true}); code != "1" {
		t.Fatalf("populated XLSX code = %q, want 1", code)
	}

	xlsmRequest := httptest.NewRequest(http.MethodGet, "/api/records.xlsm?download=1", nil)
	xlsmResponse := httptest.NewRecorder()
	srv.router.ServeHTTP(xlsmResponse, xlsmRequest)
	if xlsmResponse.Code != http.StatusOK {
		t.Fatalf("XLSM status = %d: %s", xlsmResponse.Code, xlsmResponse.Body.String())
	}
	if got := xlsmResponse.Header().Get("Content-Type"); got != "application/vnd.ms-excel.sheet.macroEnabled.12" {
		t.Fatalf("XLSM content type = %q", got)
	}
	if xlsmResponse.Header().Get("X-Patris-Office-Record-Count") != "1" ||
		xlsmResponse.Header().Get("X-Patris-Office-Source-SHA256") == "" ||
		xlsmResponse.Header().Get("X-Patris-Office-Source-SHA256") == xlsmResponse.Header().Get("X-Patris-Office-Output-SHA256") {
		t.Fatalf("XLSM provenance headers = %+v", xlsmResponse.Header())
	}
	xlsmBook, err := excelize.OpenReader(bytes.NewReader(xlsmResponse.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer xlsmBook.Close()
	if code, _ := xlsmBook.GetCellValue("Records", "A2", excelize.Options{RawCellValue: true}); code != "1" {
		t.Fatalf("populated XLSM code = %q, want 1", code)
	}

	xltmRequest := httptest.NewRequest(http.MethodGet, "/api/records.xltm?download=1", nil)
	xltmResponse := httptest.NewRecorder()
	srv.router.ServeHTTP(xltmResponse, xltmRequest)
	if xltmResponse.Code != http.StatusOK {
		t.Fatalf("XLTM status = %d: %s", xltmResponse.Code, xltmResponse.Body.String())
	}
	if got := xltmResponse.Header().Get("Content-Type"); got != "application/vnd.ms-excel.template.macroEnabled.12" {
		t.Fatalf("XLTM content type = %q", got)
	}
	if xltmResponse.Header().Get("X-Patris-Template-Data-Empty") != "true" ||
		xltmResponse.Header().Get("X-Patris-Office-Record-Count") != "0" ||
		!bytes.Equal(xltmResponse.Body.Bytes(), xltmBytes) {
		t.Fatal("XLTM was not served as the verified byte-identical blank template")
	}

	for _, requestPath := range []string{
		"/api/records.xlsx?path=C%3A%5Cclient%5Csupplied.xlsx",
		"/api/records.xlsm?template_path=C%3A%5Cclient%5Csupplied.xlsm",
		"/api/records.xltm?source_url=https%3A%2F%2Fexample.invalid%2Ftemplate.xltm",
	} {
		response := httptest.NewRecorder()
		srv.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("client Office source %q status = %d, want 400", requestPath, response.Code)
		}
	}
	bakeResponse := httptest.NewRecorder()
	srv.router.ServeHTTP(bakeResponse, httptest.NewRequest(http.MethodGet, "/api/records.xltm?include_data=true", nil))
	if bakeResponse.Code != http.StatusConflict || bakeResponse.Header().Get("X-Patris-Template-Data-Empty") != "required" {
		t.Fatalf("XLTM bake attempt status=%d marker=%q", bakeResponse.Code, bakeResponse.Header().Get("X-Patris-Template-Data-Empty"))
	}
}

func createServerXLSXPackage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted.xlsx")
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", "Records"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "A1", "Code"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "B1", "Name"); err != nil {
		t.Fatal(err)
	}
	if err := book.AddTable("Records", &excelize.Table{Range: "A1:B2", Name: "ExportProducts", StyleName: "TableStyleMedium2"}); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestXLTMDownloadRejectsConfiguredTemplateWithInitialData(t *testing.T) {
	tmpDir := t.TempDir()
	datasetPath := filepath.Join(tmpDir, "records.json")
	if err := os.WriteFile(datasetPath, []byte(`{"1":{"Code":"1","Name":"Fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Default()
	cfg.Export.XLTMTemplate = createServerMacroPackage(t, ".xltm", true)
	cfg.Export.XLTMTarget = "table:ExportProducts"
	configPath := filepath.Join(tmpDir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerWithOptions(datasetPath, nil, Options{Config: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	response := httptest.NewRecorder()
	srv.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/records.xltm", nil))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "must remain data-empty") {
		t.Fatalf("populated XLTM status=%d body=%q, want fail-closed 422", response.Code, response.Body.String())
	}
}

func serverTestVBAProject(t *testing.T) []byte {
	t.Helper()
	candidates, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*.xltm"))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("locate tracked macro fixture: candidates=%v error=%v", candidates, err)
	}
	archive, err := zip.OpenReader(candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name != "xl/vbaProject.bin" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil || len(value) == 0 {
			t.Fatalf("read vbaProject.bin: bytes=%d error=%v", len(value), readErr)
		}
		return value
	}
	t.Fatal("tracked macro fixture has no vbaProject.bin")
	return nil
}

func createServerMacroPackage(t *testing.T, extension string, populated bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted"+extension)
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", "Records"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "A1", "Code"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "B1", "Name"); err != nil {
		t.Fatal(err)
	}
	if populated {
		_ = book.SetCellStr("Records", "A2", "INITIAL")
	}
	if err := book.AddTable("Records", &excelize.Table{Range: "A1:B2", Name: "ExportProducts", StyleName: "TableStyleMedium2"}); err != nil {
		t.Fatal(err)
	}
	if err := book.AddVBAProject(serverTestVBAProject(t)); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMacroEnabledRecordDownloadRequiresConfiguredMatchingTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	datasetPath := filepath.Join(tmpDir, "records.json")
	if err := os.WriteFile(datasetPath, []byte(`{"1":{"Code":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(datasetPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for _, format := range []string{"xlsm", "xltm"} {
		request := httptest.NewRequest(http.MethodGet, "/api/records."+format, nil)
		recorder := httptest.NewRecorder()
		srv.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", format, recorder.Code)
		}
	}
}

func TestBrowserConfigRedactsOfficeTemplatePaths(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Export.XLSXTemplate = `C:\private\records.xlsx`
	cfg.Export.XLSXTarget = `table:ExportProducts`
	cfg.Export.XLSMTemplate = `C:\private\records.xlsm`
	cfg.Export.XLSMTarget = `table:ExportProducts`
	cfg.Export.XLTMTemplate = `C:\private\records.xltm`
	cfg.Export.XLTMTarget = `name:ExportProducts`
	got := browserConfig(cfg)
	if got.Export.XLSXTemplate != "" || got.Export.XLSXTarget != "" || got.Export.XLSMTemplate != "" || got.Export.XLSMTarget != "" || got.Export.XLTMTemplate != "" || got.Export.XLTMTarget != "" {
		t.Fatalf("browser config exposed Office template paths: %+v", got.Export)
	}
}

func TestBrowserConfigRedactsAndPreservesProtectedMySQLConnectionConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "patris-export.json")
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("load config manager: %v", err)
	}
	const protectedDSN = "protected-dsn-test-value"
	const protectedCAPath = "C:/protected/mysql/private-ca-path.pem"
	const protectedServerName = "private-db.internal.example"
	const protectedXLSXPath = "C:/protected/templates/records.xlsx"
	const protectedXLSXTarget = "table:ExportProducts"
	const protectedXLSMPath = "C:/protected/templates/records.xlsm"
	const protectedXLSMTarget = "table:ExportProducts"
	const protectedXLTMPath = "C:/protected/templates/records.xltm"
	const protectedXLTMTarget = "name:ExportProducts"
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.Export.MySQLDSN = protectedDSN
		cfg.Export.MySQLTLSCAFile = protectedCAPath
		cfg.Export.MySQLTLSServerName = protectedServerName
		cfg.Export.XLSXTemplate = protectedXLSXPath
		cfg.Export.XLSXTarget = protectedXLSXTarget
		cfg.Export.XLSMTemplate = protectedXLSMPath
		cfg.Export.XLSMTarget = protectedXLSMTarget
		cfg.Export.XLTMTemplate = protectedXLTMPath
		cfg.Export.XLTMTarget = protectedXLTMTarget
	}); err != nil {
		t.Fatalf("store protected MySQL config: %v", err)
	}

	jsonPath := filepath.Join(tmpDir, "records.json")
	if err := os.WriteFile(jsonPath, []byte(`{"001":{"Code":"001","Name":"Test"}}`), 0600); err != nil {
		t.Fatalf("write test records: %v", err)
	}
	srv, err := NewServerWithOptions(jsonPath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer srv.Close()

	assertRedacted := func(t *testing.T, value interface{}) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal browser payload: %v", err)
		}
		body := string(data)
		for _, forbidden := range []string{
			protectedDSN,
			protectedCAPath,
			protectedServerName,
			protectedXLSXPath,
			protectedXLSMPath,
			protectedXLTMPath,
			`"mysql_dsn"`,
			`"mysql_tls_ca_file"`,
			`"mysql_tls_server_name"`,
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("browser payload exposed protected SQL configuration %q: %s", forbidden, body)
			}
		}
	}

	get := httptest.NewRecorder()
	srv.router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d: %s", get.Code, get.Body.String())
	}
	assertRedacted(t, json.RawMessage(get.Body.Bytes()))

	initial := srv.initialSnapshotMessage(recordpipe.Result{Rows: []map[string]interface{}{}, KeyField: "Code"}, jsonPath, "")
	assertRedacted(t, initial)

	events, unsubscribe := srv.SubscribeEvents(1)
	defer unsubscribe()
	srv.broadcastConfig(manager.Get())
	select {
	case event := <-events:
		assertRedacted(t, event)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for config broadcast")
	}

	clientConfig := browserConfig(manager.Get())
	clientConfig.UI.Theme = "dark"
	clientConfig.Export.MySQLDSN = "browser-supplied-dsn-must-be-ignored"
	clientConfig.Export.MySQLTLSCAFile = "browser-supplied-ca-must-be-ignored.pem"
	clientConfig.Export.MySQLTLSServerName = "browser-supplied-name.invalid"
	clientConfig.Export.XLSXTemplate = "C:/browser/replacement.xlsx"
	clientConfig.Export.XLSXTarget = "table:BrowserReplacement"
	clientConfig.Export.XLSMTemplate = "C:/browser/replacement.xlsm"
	clientConfig.Export.XLSMTarget = "table:BrowserReplacement"
	clientConfig.Export.XLTMTemplate = "C:/browser/replacement.xltm"
	clientConfig.Export.XLTMTarget = "name:BrowserReplacement"
	body, err := json.Marshal(clientConfig)
	if err != nil {
		t.Fatalf("marshal browser update: %v", err)
	}
	put := httptest.NewRecorder()
	srv.router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/config status = %d: %s", put.Code, put.Body.String())
	}
	assertRedacted(t, json.RawMessage(put.Body.Bytes()))
	if got := manager.Get(); got.Export.MySQLDSN != protectedDSN || got.Export.MySQLTLSCAFile != protectedCAPath ||
		got.Export.MySQLTLSServerName != protectedServerName ||
		got.Export.XLSXTemplate != protectedXLSXPath || got.Export.XLSXTarget != protectedXLSXTarget ||
		got.Export.XLSMTemplate != protectedXLSMPath || got.Export.XLSMTarget != protectedXLSMTarget ||
		got.Export.XLTMTemplate != protectedXLTMPath || got.Export.XLTMTarget != protectedXLTMTarget ||
		got.UI.Theme != "dark" {
		t.Fatalf("browser update did not preserve protected server config and apply UI setting: DSN=%t CA=%t name=%t xlsx=%t xlsx_target=%t xlsm=%t xlsm_target=%t xltm=%t xltm_target=%t theme=%q",
			got.Export.MySQLDSN == protectedDSN,
			got.Export.MySQLTLSCAFile == protectedCAPath,
			got.Export.MySQLTLSServerName == protectedServerName,
			got.Export.XLSXTemplate == protectedXLSXPath,
			got.Export.XLSXTarget == protectedXLSXTarget,
			got.Export.XLSMTemplate == protectedXLSMPath,
			got.Export.XLSMTarget == protectedXLSMTarget,
			got.Export.XLTMTemplate == protectedXLTMPath,
			got.Export.XLTMTarget == protectedXLTMTarget,
			got.UI.Theme,
		)
	}
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
	if len(keyedProducts) != 292 {
		t.Fatalf("canonical /api/records returned %d top-level entries, want 292 leaf product rows", len(keyedProducts))
	}
	for _, metadata := range []string{"schema", "event_id", "products"} {
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
	if contentType := contractRecorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/vnd.patris.product-sync+json") {
		t.Fatalf("unexpected contract content type %q", contentType)
	}
	var envelope canonical.Envelope
	if err := json.NewDecoder(contractRecorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode dedicated canonical REST envelope: %v", err)
	}
	contractRESTProduct := canonicalTypedProductByCode(t, envelope.Products, "102001011")
	assertJSONEquivalent(t, "records and dedicated contract", restProduct, contractRESTProduct)
	if envelope.Schema != canonical.ContractName || len(envelope.Categories) != 54 {
		t.Fatalf("canonical contract schema/categories = %s/%d, want %s/54", envelope.Schema, len(envelope.Categories), canonical.ContractName)
	}

	categoriesRequest := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	categoriesRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(categoriesRecorder, categoriesRequest)
	if categoriesRecorder.Code != http.StatusOK {
		t.Fatalf("categories status = %d: %s", categoriesRecorder.Code, categoriesRecorder.Body.String())
	}
	var keyedCategories map[string]map[string]interface{}
	if err := json.NewDecoder(categoriesRecorder.Body).Decode(&keyedCategories); err != nil {
		t.Fatalf("decode canonical categories: %v", err)
	}
	if len(keyedCategories) != 54 {
		t.Fatalf("canonical /api/categories returned %d entries, want 54", len(keyedCategories))
	}
	if _, leakedAsProduct := keyedProducts["101"]; leakedAsProduct {
		t.Fatalf("root category Code 101 leaked into /api/records")
	}
	if category, exists := keyedCategories["101"]; !exists || category["category_code"] != nil || category["depth"] != float64(1) {
		// Keyed payloads carry the Code in the object key, not redundantly in
		// the row. Depth proves the structural projection survived.
		t.Fatalf("root category Code 101 missing or malformed: %#v", category)
	}

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
	assertHumanXLSXCanonicalFixture(t, xlsxRows)
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
		"schema":          envelope.Schema,
		"formula_id":      envelope.FormulaID,
		"source_dataset":  envelope.Source.Dataset,
		"source_revision": envelope.Source.Revision,
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

	formulaRequest := httptest.NewRequest(http.MethodGet, "/api/records.xlsx?language=fa&mode=formula&zebra=0", nil)
	formulaRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(formulaRecorder, formulaRequest)
	if formulaRecorder.Code != http.StatusOK {
		t.Fatalf("formula XLSX status = %d: %s", formulaRecorder.Code, formulaRecorder.Body.String())
	}
	formulaBook, err := excelize.OpenReader(bytes.NewReader(formulaRecorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("open formula XLSX: %v", err)
	}
	defer formulaBook.Close()
	formulaRows, err := formulaBook.GetRows("Records", excelize.Options{RawCellValue: true})
	if err != nil || len(formulaRows) < 2 {
		t.Fatalf("formula XLSX rows = %d, err=%v", len(formulaRows), err)
	}
	formulaColumns := map[string]int{}
	for index, header := range formulaRows[0] {
		formulaColumns[header] = index + 1
	}
	for _, required := range []string{
		"کد کالا", "قیمت ارزی", "وزن (گرم)", "هزینه حمل/کیلوگرم", "ارز هزینه حمل",
		"حاشیه سود (%)", "نرخ یوان (تومان)", "مبلغ منبع قیمت", "ارز منبع قیمت",
		"نوع منبع قیمت", "تعداد رقم گردکردن قیمت", "روش گردکردن قیمت", "قیمت نهایی (تومان)",
	} {
		if formulaColumns[required] == 0 {
			t.Fatalf("formula XLSX missing Persian header %q: %v", required, formulaRows[0])
		}
	}
	formulaRow := 0
	for index, row := range formulaRows[1:] {
		codeColumn := formulaColumns["کد کالا"] - 1
		if len(row) > codeColumn && row[codeColumn] == "102001011" {
			formulaRow = index + 2
			break
		}
	}
	if formulaRow == 0 {
		t.Fatal("formula XLSX fixture Code was not found")
	}
	formulaCell, _ := excelize.CoordinatesToCellName(formulaColumns["قیمت نهایی (تومان)"], formulaRow)
	formula, err := formulaBook.GetCellFormula("Records", formulaCell)
	if err != nil || !strings.Contains(formula, "ROUND((") || !strings.Contains(formula, `="CNY"`) ||
		!strings.Contains(formula, `="IRR"`) || !strings.Contains(formula, `"foreign_price"`) ||
		!strings.Contains(formula, `"partner_price"`) || !strings.Contains(formula, `"sale_price_direct"`) ||
		!strings.Contains(formula, `MOD(`) || !strings.Contains(formula, `/10`) {
		t.Fatalf("formula XLSX final price formula = %q, err=%v", formula, err)
	}
	formulaView, err := formulaBook.GetSheetView("Records", 0)
	if err != nil || formulaView.RightToLeft == nil || !*formulaView.RightToLeft {
		t.Fatalf("formula Persian XLSX view = %+v, err=%v", formulaView.RightToLeft, err)
	}
	formulaMetadataRows, err := formulaBook.GetRows("Metadata")
	if err != nil {
		t.Fatal(err)
	}
	formulaMetadata := map[string]string{}
	for _, row := range formulaMetadataRows[1:] {
		if len(row) >= 2 {
			formulaMetadata[row[0]] = row[1]
		}
	}
	if formulaMetadata["xlsx_language"] != "fa" || formulaMetadata["xlsx_mode"] != "formula" || formulaMetadata["zebra_rows"] != "false" {
		t.Fatalf("formula XLSX query options = %+v", formulaMetadata)
	}

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

func TestCanonicalProductSyncRemainsAvailableWithRawViewerMode(t *testing.T) {
	fixtureConfig := filepath.Join("..", "..", "testdata", "canonical-static-config.json")
	configData, err := os.ReadFile(fixtureConfig)
	if err != nil {
		t.Fatalf("read canonical fixture config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "raw-canonical-config.json")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatalf("copy canonical fixture config: %v", err)
	}
	manager, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("load copied canonical fixture config: %v", err)
	}
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.Database.Raw = true
		cfg.Convert.Raw = true
	}); err != nil {
		t.Fatalf("enable raw viewer mode: %v", err)
	}

	dbPath := filepath.Join("..", "..", "testdata", "kala.db")
	srv, err := NewServerWithOptions(dbPath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create raw canonical server: %v", err)
	}
	defer srv.Close()

	recordsRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recordsRecorder, httptest.NewRequest(http.MethodGet, "/api/records", nil))
	if recordsRecorder.Code != http.StatusOK {
		t.Fatalf("raw records status = %d: %s", recordsRecorder.Code, recordsRecorder.Body.String())
	}
	var rawRecords map[string]map[string]interface{}
	if err := json.NewDecoder(recordsRecorder.Body).Decode(&rawRecords); err != nil {
		t.Fatalf("decode raw viewer rows: %v", err)
	}
	rawProduct, ok := rawRecords["102001011"]
	if !ok {
		t.Fatalf("raw viewer did not preserve Code-keyed KALA row")
	}
	if _, exists := rawProduct["Sharh1"]; !exists {
		t.Fatalf("raw viewer row no longer exposes diagnostic source fields: %#v", rawProduct)
	}
	if _, exists := rawProduct["product_code"]; exists {
		t.Fatalf("raw viewer row was unexpectedly canonicalized: %#v", rawProduct)
	}

	contractRecorder := httptest.NewRecorder()
	srv.router.ServeHTTP(contractRecorder, httptest.NewRequest(http.MethodGet, "/api/product-sync", nil))
	if contractRecorder.Code != http.StatusOK {
		t.Fatalf("product-sync status = %d: %s", contractRecorder.Code, contractRecorder.Body.String())
	}
	if contentType := contractRecorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/vnd.patris.product-sync+json") {
		t.Fatalf("unexpected product-sync content type %q", contentType)
	}
	var envelope canonical.Envelope
	if err := json.NewDecoder(contractRecorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode canonical product-sync envelope: %v", err)
	}
	if envelope.Schema != canonical.ContractName || envelope.Source.Dataset != "kala.db" {
		t.Fatalf("product-sync identity = %q/%q, want %q/kala.db", envelope.Schema, envelope.Source.Dataset, canonical.ContractName)
	}
	product := canonicalTypedProductByCode(t, envelope.Products, "102001011")
	assertCanonicalFixtureValues(t, "raw-mode product-sync", product)
}

func TestCanonicalRequestTimeoutBoundsDigitalogicPricing(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Canonical.Pricing.Mode = "digitalogic"
	cfg.Canonical.Pricing.Digitalogic.Timeout = "25ms"
	if got := canonicalRequestTimeout(cfg); got != time.Second {
		t.Fatalf("short Digitalogic timeout bound = %s, want 1s", got)
	}

	cfg.Canonical.Pricing.Digitalogic.Timeout = "15s"
	if got := canonicalRequestTimeout(cfg); got != 30*time.Second {
		t.Fatalf("Digitalogic timeout with transform grace = %s, want 30s", got)
	}

	cfg.Canonical.Pricing.Digitalogic.Timeout = "10m"
	if got := canonicalRequestTimeout(cfg); got != 2*time.Minute {
		t.Fatalf("large Digitalogic timeout cap = %s, want 2m", got)
	}
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

func assertHumanXLSXCanonicalFixture(t *testing.T, rows [][]string) {
	t.Helper()
	if len(rows) < 2 {
		t.Fatal("XLSX has no data rows")
	}
	columns := map[string]int{}
	for index, field := range rows[0] {
		columns[field] = index
	}
	for _, required := range []string{"Product Code", "Foreign Price", "Weight (g)", "Total Stock", "Final Price (IRT)", "Record Hash"} {
		if _, exists := columns[required]; !exists {
			t.Fatalf("XLSX missing human-readable column %s: %v", required, rows[0])
		}
	}
	for _, row := range rows[1:] {
		if len(row) <= columns["Product Code"] || row[columns["Product Code"]] != "102001011" {
			continue
		}
		for field, expected := range map[string]string{"Foreign Price": "2.75", "Weight (g)": "1.84", "Total Stock": "20", "Final Price (IRT)": "111999"} {
			if len(row) <= columns[field] || row[columns[field]] != expected {
				t.Fatalf("XLSX %s = %q, want %q", field, row[columns[field]], expected)
			}
		}
		return
	}
	t.Fatal("XLSX did not contain fixture Code")
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
		lastRecords:          []map[string]interface{}{{"Code": "watcher"}},
		lastRecordsReady:     true,
		lastContractRevision: "sha256:watcher",
	}
	srv.seedLastSnapshot([]map[string]interface{}{{"Code": "client"}}, "sha256:client")
	if len(srv.lastRecords) != 1 || srv.lastRecords[0]["Code"] != "watcher" {
		t.Fatalf("client snapshot replaced watcher baseline: %#v", srv.lastRecords)
	}
	if srv.lastContractRevision != "sha256:watcher" {
		t.Fatalf("client snapshot replaced watcher contract revision: %q", srv.lastContractRevision)
	}

	empty := &Server{}
	empty.seedLastRecords([]map[string]interface{}{})
	empty.seedLastRecords([]map[string]interface{}{{"Code": "later-client"}})
	if !empty.lastRecordsReady || len(empty.lastRecords) != 0 {
		t.Fatalf("initialized empty baseline was replaced: %#v", empty.lastRecords)
	}
}

func TestCategoryOnlyRevisionChangeTriggersUpdateDispatch(t *testing.T) {
	rows := []map[string]interface{}{{"product_code": "101001001", "name": "LM2576"}}
	srv := &Server{}
	srv.seedLastSnapshot(rows, "sha256:before")

	changes, contractChanged := srv.updateRecordBaseline(recordpipe.Result{
		Rows:     rows,
		KeyField: "product_code",
		Contract: &canonical.Envelope{Source: canonical.Source{Revision: "sha256:after"}},
	})
	added, modified, deleted := changes.Counts()
	if added+modified+deleted != 0 {
		t.Fatalf("category-only revision produced product changes: %+v", changes)
	}
	if !contractChanged {
		t.Fatal("category-only revision change was not marked for outbound dispatch")
	}

	_, contractChanged = srv.updateRecordBaseline(recordpipe.Result{
		Rows:     rows,
		KeyField: "product_code",
		Contract: &canonical.Envelope{Source: canonical.Source{Revision: "sha256:after"}},
	})
	if contractChanged {
		t.Fatal("unchanged contract revision requested a duplicate outbound dispatch")
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
