package recordsink

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestWriteSQLiteCreatesUpsertableTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.sqlite")
	rows := []map[string]interface{}{
		{"sku": "100", "title": "Bolt", "price": 25},
		{"sku": "200", "title": "Nut", "price": 10},
	}
	if err := WriteSQLite(path, "products", "sku", rows); err != nil {
		t.Fatalf("WriteSQLite failed: %v", err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatalf("query sqlite: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
}
