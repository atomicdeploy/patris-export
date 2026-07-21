package recordsink

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type matrixProduct struct {
	Code           string
	Name           string
	FinalPrice     int64
	WarehouseStock int64
}

func TestSQLTargetMatrixParitySchemaEvolutionAndRollback(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PATRIS_EXPORT_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("set PATRIS_EXPORT_TEST_MYSQL_DSN to run the disposable MySQL/MariaDB compatibility proof")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	mysqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("open disposable SQL target:", err)
	}
	defer mysqlDB.Close()
	if err := mysqlDB.PingContext(ctx); err != nil {
		t.Fatal("ping disposable SQL target:", err)
	}
	var version string
	if err := mysqlDB.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal("read disposable SQL target version:", err)
	}
	t.Logf("verifying SQL target %s", version)

	table := fmt.Sprintf("patris_matrix_%d", time.Now().UTC().UnixNano())
	quotedTable := quoteIdent("mysql", table)
	defer func() {
		_, _ = mysqlDB.Exec("DROP TABLE IF EXISTS " + quotedTable)
	}()

	sqlitePath := filepath.Join(t.TempDir(), "matrix.sqlite")
	options := SQLOptions{
		Driver:         "mysql",
		DSN:            dsn,
		Table:          table,
		KeyField:       "product_code",
		Batch:          1,
		ConnectTimeout: 5 * time.Second,
	}
	initial := []map[string]interface{}{
		{"product_code": "0001", "name": "Alpha", "final_price": int64(100)},
		{"product_code": "0002", "name": "Beta", "final_price": int64(200)},
	}

	mysqlResult, err := SyncSQLWithResult(ctx, options, initial)
	if err != nil {
		t.Fatal("initial MySQL/MariaDB sync:", err)
	}
	assertSQLResultCounts(t, mysqlResult, 2, 0, 0, 0, 0)
	sqliteResult, err := SyncSQLite(ctx, sqlitePath, SQLOptions{
		Table: table, KeyField: "product_code", Batch: 1,
	}, initial)
	if err != nil {
		t.Fatal("initial SQLite parity sync:", err)
	}
	assertSQLResultCounts(t, sqliteResult, 2, 0, 0, 0, 0)

	if _, err := mysqlDB.ExecContext(ctx, "ALTER TABLE "+quotedTable+" ADD COLUMN `operator_notes` VARCHAR(255) NULL, ADD COLUMN `rollback_guard` VARCHAR(64) NULL CHECK (`rollback_guard` <> 'reject')"); err != nil {
		t.Fatal("add MySQL/MariaDB user-owned columns:", err)
	}
	if _, err := mysqlDB.ExecContext(ctx, "UPDATE "+quotedTable+" SET `operator_notes` = 'preserve me', `rollback_guard` = CONCAT('guard-', `product_code`)"); err != nil {
		t.Fatal("seed MySQL/MariaDB user-owned columns:", err)
	}

	sqliteDB, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatal("open SQLite parity target:", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.ExecContext(ctx, "ALTER TABLE "+quoteIdent("sqlite", table)+" ADD COLUMN `operator_notes` TEXT NULL"); err != nil {
		t.Fatal("add SQLite user-owned notes column:", err)
	}
	if _, err := sqliteDB.ExecContext(ctx, "ALTER TABLE "+quoteIdent("sqlite", table)+" ADD COLUMN `rollback_guard` TEXT NULL CHECK (`rollback_guard` <> 'reject')"); err != nil {
		t.Fatal("add SQLite rollback column:", err)
	}
	if _, err := sqliteDB.ExecContext(ctx, "UPDATE "+quoteIdent("sqlite", table)+" SET `operator_notes` = 'preserve me', `rollback_guard` = 'guard-' || `product_code`"); err != nil {
		t.Fatal("seed SQLite user-owned columns:", err)
	}

	evolved := []map[string]interface{}{
		{"product_code": "0001", "name": "Alpha", "final_price": int64(150), "warehouse_stock": int64(7)},
		{"product_code": "0002", "name": "Beta", "final_price": int64(200), "warehouse_stock": int64(3)},
	}
	mysqlResult, err = SyncSQLWithResult(ctx, options, evolved)
	if err != nil {
		t.Fatal("evolved MySQL/MariaDB sync:", err)
	}
	assertSQLResultCounts(t, mysqlResult, 0, 2, 0, 0, 0)
	sqliteResult, err = SyncSQLite(ctx, sqlitePath, SQLOptions{
		Table: table, KeyField: "product_code", Batch: 1,
	}, evolved)
	if err != nil {
		t.Fatal("evolved SQLite parity sync:", err)
	}
	assertSQLResultCounts(t, sqliteResult, 0, 2, 0, 0, 0)

	mysqlProducts := readMatrixProducts(t, ctx, mysqlDB, "mysql", table)
	sqliteProducts := readMatrixProducts(t, ctx, sqliteDB, "sqlite", table)
	if !reflect.DeepEqual(mysqlProducts, sqliteProducts) {
		t.Fatalf("canonical SQL parity mismatch: mysql=%+v sqlite=%+v", mysqlProducts, sqliteProducts)
	}
	if len(mysqlProducts) != 2 || mysqlProducts[0].Code != "0001" {
		t.Fatalf("string keys with leading zeros were not preserved: %+v", mysqlProducts)
	}
	assertMatrixUserColumn(t, ctx, mysqlDB, "mysql", table, "0001")
	assertMatrixUserColumn(t, ctx, sqliteDB, "sqlite", table, "0001")

	failing := []map[string]interface{}{
		{"product_code": "0001", "name": "Alpha", "final_price": int64(999), "warehouse_stock": int64(7), "rollback_guard": "accepted"},
		{"product_code": "0003", "name": "Gamma", "final_price": int64(300), "warehouse_stock": int64(1), "rollback_guard": "reject"},
	}
	mysqlResult, err = SyncSQLWithResult(ctx, options, failing)
	assertMatrixRollback(t, "MySQL/MariaDB", mysqlResult, err, len(failing))
	sqliteResult, err = SyncSQLite(ctx, sqlitePath, SQLOptions{
		Table: table, KeyField: "product_code", Batch: 1,
	}, failing)
	assertMatrixRollback(t, "SQLite", sqliteResult, err, len(failing))

	if got := readMatrixProducts(t, ctx, mysqlDB, "mysql", table); !reflect.DeepEqual(got, mysqlProducts) {
		t.Fatalf("MySQL/MariaDB batch failure did not roll back: got=%+v want=%+v", got, mysqlProducts)
	}
	if got := readMatrixProducts(t, ctx, sqliteDB, "sqlite", table); !reflect.DeepEqual(got, sqliteProducts) {
		t.Fatalf("SQLite batch failure did not roll back: got=%+v want=%+v", got, sqliteProducts)
	}
}

func readMatrixProducts(t *testing.T, ctx context.Context, db *sql.DB, driver, table string) []matrixProduct {
	t.Helper()
	query := "SELECT `product_code`, `name`, `final_price`, `warehouse_stock` FROM " + quoteIdent(driver, table) + " ORDER BY `product_code`"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatal("read matrix products:", err)
	}
	defer rows.Close()

	products := make([]matrixProduct, 0)
	for rows.Next() {
		var product matrixProduct
		if err := rows.Scan(&product.Code, &product.Name, &product.FinalPrice, &product.WarehouseStock); err != nil {
			t.Fatal("scan matrix product:", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		t.Fatal("iterate matrix products:", err)
	}
	return products
}

func assertMatrixUserColumn(t *testing.T, ctx context.Context, db *sql.DB, driver, table, code string) {
	t.Helper()
	query := "SELECT `operator_notes` FROM " + quoteIdent(driver, table) + " WHERE `product_code` = ?"
	var notes string
	if err := db.QueryRowContext(ctx, query, code).Scan(&notes); err != nil {
		t.Fatal("read preserved user-owned column:", err)
	}
	if notes != "preserve me" {
		t.Fatalf("user-owned column changed during additive evolution: %q", notes)
	}
}

func assertMatrixRollback(t *testing.T, target string, result SQLResult, err error, requested int) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s rejected batch unexpectedly succeeded", target)
	}
	if result.Inserted != 0 || result.Updated != 0 || result.Unchanged != 0 || result.Deleted != 0 || result.Failed != requested {
		t.Fatalf("%s rollback result = %+v, want %d failed and no committed successes", target, result, requested)
	}
}
