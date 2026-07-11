package updateout

import (
	"strings"
	"testing"
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
