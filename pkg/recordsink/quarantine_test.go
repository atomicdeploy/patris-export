package recordsink

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteSnapshotPreservesProtectedQuarantineKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "name": "last-known-good"},
		{"product_code": "B", "name": "remove-me"},
	}
	if err := WriteSQLite(path, "products", "product_code", initial); err != nil {
		t.Fatal(err)
	}
	if err := WriteSQLite(path, "products", "product_code", nil, SnapshotOptions{ProtectedKeys: []string{"A"}}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	var name string
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(name), '') FROM products`).Scan(&count, &name); err != nil {
		t.Fatal(err)
	}
	if count != 1 || name != "last-known-good" {
		t.Fatalf("quarantine reconciliation changed protected row: count=%d name=%q", count, name)
	}
	if err := WriteSQLite(path, "products", "product_code", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("ordinary empty snapshot did not clear table: count=%d err=%v", count, err)
	}
}
