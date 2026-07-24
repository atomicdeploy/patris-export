package datasource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInSourcesImplementContextDataSource(t *testing.T) {
	var _ ContextDataSource = (*JSONDataSource)(nil)
	var _ ContextDataSource = (*ParadoxDataSource)(nil)
}

func TestJSONDataSourceHonorsPreCancelledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "products.json")
	if err := os.WriteFile(path, []byte(`[{"Code":"001"}]`), 0o600); err != nil {
		t.Fatalf("write JSON source: %v", err)
	}
	source := &JSONDataSource{path: path}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.GetRawRecordsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled JSON source error=%v, want context.Canceled", err)
	}
}

func TestParadoxDataSourceHonorsPreCancelledContextBeforeNativeOpen(t *testing.T) {
	source := &ParadoxDataSource{path: filepath.Join(t.TempDir(), "missing.db")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.GetRawRecordsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled Paradox source error=%v, want context.Canceled", err)
	}
}
