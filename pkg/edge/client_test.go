package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadURL(t *testing.T) {
	tests := map[string]string{
		"http://127.0.0.1:18080":                 "http://127.0.0.1:18080/api/edge/upload",
		"http://127.0.0.1:18080/":                "http://127.0.0.1:18080/api/edge/upload",
		"http://127.0.0.1:18080/api/edge/upload": "http://127.0.0.1:18080/api/edge/upload",
	}
	for input, want := range tests {
		got, err := UploadURL(input)
		if err != nil {
			t.Fatalf("UploadURL(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("UploadURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientUploadFile(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "kala.json")
	if err := os.WriteFile(source, []byte(`{"101":{"Code":"101","Name":"Edge"}}`), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/edge/upload" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.FormValue("source_id"); got != "edge-a" {
			t.Fatalf("source_id = %q, want edge-a", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("missing file part: %v", err)
		}
		file.Close()
		_ = json.NewEncoder(w).Encode(UploadResult{Success: true, Records: 1, Size: 36, Hash: "abcd1234"})
	}))
	defer server.Close()

	client := Client{TargetURL: server.URL, Token: "secret", SourceID: "edge-a", MaxBytes: 1024 * 1024}
	result, err := client.UploadFile(context.Background(), source)
	if err != nil {
		t.Fatalf("UploadFile returned error: %v", err)
	}
	if result.Records != 1 {
		t.Fatalf("records = %d, want 1", result.Records)
	}
}
