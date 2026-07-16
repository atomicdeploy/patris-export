package recordsink

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var errInjectedProductWrite = errors.New("injected product batch failure")

func TestSQLiteSyncInitialUpdateDryRunAndProtectedDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "products.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "001", "final_price": int64(100)},
		{"product_code": "002", "final_price": int64(200)},
		{"product_code": "003", "final_price": int64(300)},
	}
	result, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Batch: 2,
	}, initial)
	if err != nil {
		t.Fatalf("initial SQLite sync: %v", err)
	}
	assertSQLResultCounts(t, result, 3, 0, 0, 0, 0)
	if result.Reconciliation != UpsertOnly {
		t.Fatalf("unsafe default reconciliation = %q", result.Reconciliation)
	}

	update := []map[string]interface{}{
		{"product_code": "002", "final_price": int64(250)},
		{"product_code": "003", "final_price": int64(300)},
		{"product_code": "004", "final_price": int64(400)},
	}
	result, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Batch: 2,
	}, update)
	if err != nil {
		t.Fatalf("SQLite update sync: %v", err)
	}
	assertSQLResultCounts(t, result, 1, 1, 1, 0, 0)
	assertSQLiteProductState(t, path, 4, "002", 250)

	preview := []map[string]interface{}{
		{"product_code": "002", "final_price": int64(250)},
		{"product_code": "004", "final_price": int64(450)},
	}
	result, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table:          "products",
		KeyField:       "product_code",
		Batch:          1,
		Reconciliation: DeleteMissing,
		DryRun:         true,
		ProtectedKeys:  []string{"001"},
	}, preview)
	if err != nil {
		t.Fatalf("SQLite dry run: %v", err)
	}
	assertSQLResultCounts(t, result, 0, 1, 1, 1, 0)
	if !result.DryRun {
		t.Fatal("dry-run result was not marked as a preview")
	}
	assertSQLiteProductState(t, path, 4, "004", 400)

	result, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table:          "products",
		KeyField:       "product_code",
		Batch:          1,
		Reconciliation: DeleteMissing,
		ProtectedKeys:  []string{"001"},
	}, preview)
	if err != nil {
		t.Fatalf("SQLite delete_missing sync: %v", err)
	}
	assertSQLResultCounts(t, result, 0, 1, 1, 1, 0)
	assertSQLiteProductState(t, path, 3, "004", 450)
}

func TestSQLiteDryRunDoesNotCreateDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "preview.sqlite")
	result, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", DryRun: true,
	}, []map[string]interface{}{{"product_code": "001", "final_price": int64(100)}})
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, result, 1, 0, 0, 0, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry run created destination %q: %v", path, err)
	}
}

func TestSQLiteDryRunDoesNotEvolveSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview-existing.sqlite")
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code",
	}, []map[string]interface{}{{"product_code": "001", "name": "Existing"}}); err != nil {
		t.Fatal(err)
	}
	result, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", DryRun: true,
	}, []map[string]interface{}{{"product_code": "001", "name": "Existing", "location": "A-1"}})
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, result, 0, 1, 0, 0, 0)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('products') WHERE name = 'location'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("dry run added the location column")
	}
}

func TestMySQLPreparedWritesHonorBatchSize(t *testing.T) {
	state := &schemaDriverState{
		tableExists: true,
		columns: [][]driver.Value{
			{"product_code", "varchar(191)", "NO", "PRI", nil, ""},
			{"final_price", "bigint", "YES", "", nil, ""},
		},
	}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	rows := make([]map[string]interface{}, 5)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"product_code": "00" + string(rune('1'+index)),
			"final_price":  int64(100 + index),
		}
	}
	result, err := SyncSQLDB(context.Background(), db, SQLOptions{
		Driver: "mysql", Table: "products", KeyField: "product_code", Batch: 2,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, result, 5, 0, 0, 0, 0)
	productWrites := preparedProductWrites(state.prepared)
	if len(productWrites) != 3 {
		t.Fatalf("prepared product batches = %d, want 3: %#v", len(productWrites), productWrites)
	}
	for index, wantArgs := range []int{4, 4, 2} {
		if got := len(productWrites[index].args); got != wantArgs {
			t.Fatalf("batch %d args = %d, want %d", index, got, wantArgs)
		}
	}
}

func TestSQLBatchFailureRollsBackAndReportsFailedRows(t *testing.T) {
	state := &schemaDriverState{
		tableExists:       true,
		failProductExecAt: 2,
		columns: [][]driver.Value{
			{"product_code", "varchar(191)", "NO", "PRI", nil, ""},
			{"final_price", "bigint", "YES", "", nil, ""},
		},
	}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	rows := []map[string]interface{}{
		{"product_code": "001", "final_price": int64(100)},
		{"product_code": "002", "final_price": int64(200)},
		{"product_code": "003", "final_price": int64(300)},
	}
	result, err := SyncSQLDB(context.Background(), db, SQLOptions{
		Driver: "mysql", Table: "products", KeyField: "product_code", Batch: 2,
	}, rows)
	if !errors.Is(err, errInjectedProductWrite) {
		t.Fatalf("batch error = %v", err)
	}
	if result.Failed != len(rows) || state.commits != 0 || state.rollbacks != 1 {
		t.Fatalf("failure result=%+v commits=%d rollbacks=%d", result, state.commits, state.rollbacks)
	}
}

func TestMariaDBSyncInitialWriteAndUpdate(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PATRIS_EXPORT_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("set PATRIS_EXPORT_TEST_MYSQL_DSN to run the MariaDB/MySQL integration proof")
	}
	table := fmt.Sprintf("patris_recordsink_it_%d", time.Now().UTC().UnixNano())
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("open integration database:", err)
	}
	defer db.Close()
	defer func() {
		_, _ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent("mysql", table))
	}()

	options := SQLOptions{
		Driver: "mysql", DSN: dsn, Table: table, KeyField: "product_code", Batch: 2,
	}
	result, err := SyncSQLWithResult(context.Background(), options, []map[string]interface{}{
		{"product_code": "001", "foreign_price": 10.25, "final_price": int64(100)},
		{"product_code": "002", "foreign_price": 20.5, "final_price": int64(200)},
	})
	if err != nil {
		t.Fatalf("initial MariaDB sync: %v", err)
	}
	assertSQLResultCounts(t, result, 2, 0, 0, 0, 0)

	result, err = SyncSQLWithResult(context.Background(), options, []map[string]interface{}{
		{"product_code": "002", "foreign_price": 21.5, "final_price": int64(250)},
		{"product_code": "003", "foreign_price": 30.75, "final_price": int64(300)},
	})
	if err != nil {
		t.Fatalf("MariaDB update sync: %v", err)
	}
	assertSQLResultCounts(t, result, 1, 1, 0, 0, 0)
	var count int
	var price int64
	query := "SELECT COUNT(*), MAX(CASE WHEN product_code = '002' THEN final_price END) FROM " + quoteIdent("mysql", table)
	if err := db.QueryRow(query).Scan(&count, &price); err != nil {
		t.Fatal(err)
	}
	if count != 3 || price != 250 {
		t.Fatalf("MariaDB proof state: count=%d updated_price=%d", count, price)
	}
}

func assertSQLResultCounts(t *testing.T, result SQLResult, inserted, updated, unchanged, deleted, failed int) {
	t.Helper()
	if result.Inserted != inserted || result.Updated != updated || result.Unchanged != unchanged || result.Deleted != deleted || result.Failed != failed {
		t.Fatalf("SQL result = %+v, want inserted=%d updated=%d unchanged=%d deleted=%d failed=%d", result, inserted, updated, unchanged, deleted, failed)
	}
}

func assertSQLiteProductState(t *testing.T, path string, count int, code string, price int64) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var gotCount int
	var gotPrice int64
	if err := db.QueryRow(`SELECT COUNT(*), MAX(CASE WHEN product_code = ? THEN final_price END) FROM products`, code).Scan(&gotCount, &gotPrice); err != nil {
		t.Fatal(err)
	}
	if gotCount != count || gotPrice != price {
		t.Fatalf("SQLite state: count=%d price[%s]=%d, want count=%d price=%d", gotCount, code, gotPrice, count, price)
	}
}

func preparedProductWrites(executions []preparedExecution) []preparedExecution {
	result := []preparedExecution{}
	for _, execution := range executions {
		if strings.HasPrefix(execution.query, "INSERT INTO `products`") {
			result = append(result, execution)
		}
	}
	return result
}
