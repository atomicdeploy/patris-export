package appconfig

import (
	"path/filepath"
	"testing"
)

func TestRemoteDeliveryExampleConfigsLoad(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		url       string
		secretEnv string
	}{
		{
			name:      "native REST",
			file:      "send-updates-rest.json",
			url:       "https://receiver.example/wp-json/receiver/patris/product-sync",
			secretEnv: "PATRIS_PRODUCT_SYNC_SECRET",
		},
		{
			name:      "loopback adapter",
			file:      "send-updates-adapter.json",
			url:       "http://127.0.0.1:18081/ingest",
			secretEnv: "PATRIS_ADAPTER_INGRESS_SECRET",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "docs", "examples", test.file)
			manager, err := LoadFiles([]string{path})
			if err != nil {
				t.Fatalf("LoadFiles(%q): %v", path, err)
			}
			cfg := manager.Get()
			if !cfg.Canonical.Enabled || !cfg.SendUpdates.Enabled || !cfg.SendUpdates.RequireContract || cfg.SendUpdates.AllowRaw {
				t.Fatalf("unsafe or incomplete example config: %+v", cfg.SendUpdates)
			}
			if cfg.SendUpdates.URL != test.url || cfg.SendUpdates.ProductSyncSecretEnv != test.secretEnv {
				t.Fatalf("example destination = %q via %q, want %q via %q", cfg.SendUpdates.URL, cfg.SendUpdates.ProductSyncSecretEnv, test.url, test.secretEnv)
			}
			if cfg.SendUpdates.RetryAttempts != 3 || cfg.SendUpdates.RetryBackoff != "2s" || cfg.SendUpdates.Timeout != "10s" {
				t.Fatalf("example retry policy was not preserved: %+v", cfg.SendUpdates)
			}
		})
	}
}
