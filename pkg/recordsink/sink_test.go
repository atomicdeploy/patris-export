package recordsink

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"strings"
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

func TestWriteSQLiteReconcilesFullSnapshotAndClearsExistingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
	}
	if err := WriteSQLite(path, "products", "product_code", initial); err != nil {
		t.Fatalf("initial snapshot failed: %v", err)
	}
	if err := WriteSQLite(path, "products", "product_code", []map[string]interface{}{
		{"product_code": "B", "final_price": int64(250)},
	}, SnapshotOptions{Reconciliation: DeleteMissing}); err != nil {
		t.Fatalf("replacement snapshot failed: %v", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	var price int64
	if err := db.QueryRow(`SELECT COUNT(*), MAX(final_price) FROM products`).Scan(&count, &price); err != nil {
		t.Fatal(err)
	}
	if count != 1 || price != 250 {
		t.Fatalf("snapshot reconciliation left stale data: count=%d price=%d", count, price)
	}

	if err := WriteSQLite(path, "products", "product_code", nil, SnapshotOptions{Reconciliation: DeleteMissing}); err != nil {
		t.Fatalf("empty snapshot failed: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty snapshot retained %d rows", count)
	}
}

func TestWriteSQLiteSnapshotRetainsProtectedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected-snapshot.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
		{"product_code": "C", "final_price": int64(300)},
	}
	if err := WriteSQLite(path, "products", "product_code", initial); err != nil {
		t.Fatalf("initial snapshot failed: %v", err)
	}
	if err := WriteSQLite(path, "products", "product_code", []map[string]interface{}{
		{"product_code": "B", "final_price": int64(250)},
	}, SnapshotOptions{Reconciliation: DeleteMissing, ProtectedKeys: []string{"A"}}); err != nil {
		t.Fatalf("protected replacement snapshot failed: %v", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertSQLiteCodes(t, db, []string{"A", "B"})

	if err := WriteSQLite(path, "products", "product_code", nil, SnapshotOptions{Reconciliation: DeleteMissing, ProtectedKeys: []string{"A"}}); err != nil {
		t.Fatalf("all-quarantined snapshot failed: %v", err)
	}
	assertSQLiteCodes(t, db, []string{"A"})
}

func assertSQLiteCodes(t *testing.T, db *sql.DB, expected []string) {
	t.Helper()
	rows, err := db.Query(`SELECT product_code FROM products ORDER BY product_code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, code)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("codes = %v, want %v", actual, expected)
	}
}

func TestUpsertSQLKeepsRowsAbsentFromPartialUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := UpsertSQL(ctx, db, "sqlite", "products", "product_code", []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
	}); err != nil {
		t.Fatalf("initial upsert failed: %v", err)
	}
	if err := UpsertSQL(ctx, db, "sqlite", "products", "product_code", []map[string]interface{}{
		{"product_code": "B", "final_price": int64(250)},
	}); err != nil {
		t.Fatalf("partial upsert failed: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("partial upsert destructively removed rows; count=%d", count)
	}
	if err := UpsertSQL(ctx, db, "sqlite", "products", "product_code", nil); err != nil {
		t.Fatalf("empty partial upsert failed: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("empty partial upsert destructively removed rows; count=%d", count)
	}
}

func TestWriteSQLiteUsesCanonicalTypesAndEvolvesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canonical.sqlite")
	first := []map[string]interface{}{{
		"product_code":  "00123",
		"foreign_price": 24.5,
		"weight_grams":  240.0,
		"final_price":   int64(2009410),
		"warnings":      []string{},
	}}
	if err := WriteSQLite(path, "products", "product_code", first); err != nil {
		t.Fatalf("first canonical write failed: %v", err)
	}
	second := []map[string]interface{}{{
		"product_code":  "00123",
		"foreign_price": 25.0,
		"weight_grams":  240.0,
		"final_price":   int64(2047110),
		"warnings":      []string{"pricing_catalog_stale"},
		"location":      "A-12",
	}}
	if err := WriteSQLite(path, "products", "product_code", second); err != nil {
		t.Fatalf("schema-evolution write failed: %v", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA table_info("products")`)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		types[name] = strings.ToUpper(dataType)
	}
	_ = rows.Close()
	for field, expected := range map[string]string{
		"product_code":  "TEXT",
		"foreign_price": "REAL",
		"weight_grams":  "REAL",
		"final_price":   "INTEGER",
		"warnings":      "TEXT",
		"location":      "TEXT",
	} {
		if types[field] != expected {
			t.Fatalf("%s type = %q, want %q (all=%v)", field, types[field], expected, types)
		}
	}
	var code, location, warnings string
	var final int64
	if err := db.QueryRow(`SELECT product_code, location, warnings, final_price FROM products`).Scan(&code, &location, &warnings, &final); err != nil {
		t.Fatal(err)
	}
	if code != "00123" || location != "A-12" || warnings != `["pricing_catalog_stale"]` || final != 2047110 {
		t.Fatalf("unexpected evolved row: code=%q location=%q warnings=%q final=%d", code, location, warnings, final)
	}
}

func TestMySQLCanonicalColumnTypes(t *testing.T) {
	rows := []map[string]interface{}{{"product_code": "001", "foreign_price": nil, "final_price": nil}}
	checks := map[string]string{
		"product_code":  "VARCHAR(191)",
		"foreign_price": "DECIMAL(30,10)",
		"final_price":   "BIGINT",
		"warnings":      "LONGTEXT",
	}
	for field, expected := range checks {
		if got := columnType("mysql", field, "product_code", rows); got != expected {
			t.Fatalf("mysql type for %s = %q, want %q", field, got, expected)
		}
	}
}

func TestMySQLCreatesTransactionalTable(t *testing.T) {
	state := &schemaDriverState{}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	if err := createTable(context.Background(), db, "mysql", "products", []string{"product_code", "final_price"}, "product_code", []map[string]interface{}{{
		"product_code": "001", "final_price": int64(100),
	}}); err != nil {
		t.Fatal(err)
	}
	if len(state.execs) != 1 || !strings.HasSuffix(state.execs[0], " ENGINE=InnoDB") {
		t.Fatalf("MySQL create is not explicitly transactional: %#v", state.execs)
	}
}

func TestMySQLSchemaEvolutionAddsTypedCanonicalColumns(t *testing.T) {
	state := &schemaDriverState{}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	rows := []map[string]interface{}{{
		"product_code":  "001",
		"record_hash":   "sha256:one",
		"foreign_price": 24.5,
		"final_price":   int64(2009410),
	}}
	fields := []string{"product_code", "record_hash", "foreign_price", "final_price"}
	if err := ensureColumns(context.Background(), db, "mysql", "products", fields, "product_code", rows); err != nil {
		t.Fatalf("mysql schema evolution failed: %v", err)
	}
	joined := strings.Join(state.execs, "\n")
	for _, expected := range []string{
		"ALTER TABLE `products` ADD COLUMN `foreign_price` DECIMAL(30,10)",
		"ALTER TABLE `products` ADD COLUMN `final_price` BIGINT",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing MySQL evolution statement %q in:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "product_code") || strings.Contains(joined, "record_hash") {
		t.Fatalf("existing MySQL columns were added again:\n%s", joined)
	}
}

func TestMySQLSnapshotPreservesTypedNullValuesAndDeletesAbsentRows(t *testing.T) {
	state := &schemaDriverState{columns: [][]driver.Value{
		{"product_code", "varchar(191)", "NO", "PRI", nil, ""},
		{"foreign_price", "decimal(30,10)", "YES", "", nil, ""},
		{"final_price", "bigint", "YES", "", nil, ""},
	}}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	rows := []map[string]interface{}{
		{"product_code": "001", "foreign_price": 24.5, "final_price": nil},
		{"product_code": "002", "foreign_price": nil, "final_price": int64(2009410)},
	}
	if err := SyncSnapshotSQL(context.Background(), db, "mysql", "products", "product_code", rows); err != nil {
		t.Fatalf("mysql snapshot sync failed: %v", err)
	}
	if state.commits != 1 || state.rollbacks != 0 {
		t.Fatalf("transaction outcome: commits=%d rollbacks=%d", state.commits, state.rollbacks)
	}

	var productWrites []preparedExecution
	for _, execution := range state.prepared {
		if strings.HasPrefix(execution.query, "INSERT INTO `products`") {
			productWrites = append(productWrites, execution)
		}
	}
	if len(productWrites) != 1 {
		t.Fatalf("product batch writes = %d, want 1 (all=%#v)", len(productWrites), state.prepared)
	}
	if got := productWrites[0].args; len(got) != 6 || got[0] != "001" || got[1] != nil || got[2] != float64(24.5) || got[3] != "002" || got[4] != int64(2009410) || got[5] != nil {
		t.Fatalf("typed MySQL batch = %#v", got)
	}
	joined := strings.Join(state.execs, "\n")
	if !strings.Contains(joined, "DELETE FROM `products` WHERE NOT EXISTS") {
		t.Fatalf("snapshot reconciliation delete was not executed:\n%s", joined)
	}
}

func TestMySQLEmptySnapshotClearsExistingTable(t *testing.T) {
	state := &schemaDriverState{tableExists: true}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	if err := SyncSnapshotSQL(context.Background(), db, "mysql", "products", "product_code", nil); err != nil {
		t.Fatalf("empty mysql snapshot failed: %v", err)
	}
	if state.commits != 1 || state.rollbacks != 0 {
		t.Fatalf("transaction outcome: commits=%d rollbacks=%d", state.commits, state.rollbacks)
	}
	if !containsExact(state.execs, "DELETE FROM `products`") {
		t.Fatalf("empty snapshot did not clear table: %#v", state.execs)
	}
}

func TestMySQLEmptySnapshotRetainsProtectedKeys(t *testing.T) {
	state := &schemaDriverState{tableExists: true}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	if err := SyncSnapshotSQL(
		context.Background(),
		db,
		"mysql",
		"products",
		"product_code",
		nil,
		SnapshotOptions{ProtectedKeys: []string{"QUARANTINED"}},
	); err != nil {
		t.Fatalf("protected empty mysql snapshot failed: %v", err)
	}
	if state.commits != 1 || state.rollbacks != 0 {
		t.Fatalf("transaction outcome: commits=%d rollbacks=%d", state.commits, state.rollbacks)
	}
	if containsExact(state.execs, "DELETE FROM `products`") {
		t.Fatalf("protected empty snapshot performed destructive clear: %#v", state.execs)
	}
	if !strings.Contains(strings.Join(state.execs, "\n"), "DELETE FROM `products` WHERE NOT EXISTS") {
		t.Fatalf("protected empty snapshot did not reconcile through protected keys: %#v", state.execs)
	}
	found := false
	for _, execution := range state.prepared {
		if strings.Contains(execution.query, "snapshot_keys") && len(execution.args) == 1 && execution.args[0] == "QUARANTINED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("protected key was not staged: %#v", state.prepared)
	}
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type schemaDriverState struct {
	execs             []string
	prepared          []preparedExecution
	columns           [][]driver.Value
	tableExists       bool
	commits           int
	rollbacks         int
	productExecs      int
	failProductExecAt int
}

type preparedExecution struct {
	query string
	args  []driver.Value
}

type schemaConnector struct {
	state *schemaDriverState
}

func (connector schemaConnector) Connect(context.Context) (driver.Conn, error) {
	return &schemaConn{state: connector.state}, nil
}

func (connector schemaConnector) Driver() driver.Driver { return schemaDriver{} }

type schemaDriver struct{}

func (schemaDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type schemaConn struct {
	state *schemaDriverState
}

func (conn *schemaConn) Prepare(query string) (driver.Stmt, error) {
	return &schemaStmt{state: conn.state, query: query}, nil
}
func (conn *schemaConn) Close() error              { return nil }
func (conn *schemaConn) Begin() (driver.Tx, error) { return &schemaTx{state: conn.state}, nil }

func (conn *schemaConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "information_schema.tables") {
		values := [][]driver.Value{}
		if conn.state.tableExists {
			values = append(values, []driver.Value{int64(1)})
		}
		return &schemaRows{columns: []string{"exists"}, values: values}, nil
	}
	if !strings.HasPrefix(query, "SHOW COLUMNS FROM") {
		if strings.HasPrefix(query, "SELECT ") && strings.Contains(query, " FROM `products` WHERE ") {
			columnsPart := strings.TrimPrefix(strings.SplitN(query, " FROM ", 2)[0], "SELECT ")
			columns := strings.Split(strings.ReplaceAll(columnsPart, "`", ""), ", ")
			return &schemaRows{columns: columns}, nil
		}
		return nil, errors.New("unexpected query: " + query)
	}
	columns := conn.state.columns
	if columns == nil {
		columns = [][]driver.Value{
			{"product_code", "varchar(191)", "NO", "PRI", nil, ""},
			{"record_hash", "longtext", "YES", "", nil, ""},
		}
	}
	return &schemaRows{columns: []string{"Field", "Type", "Null", "Key", "Default", "Extra"}, values: columns}, nil
}

func (conn *schemaConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	conn.state.execs = append(conn.state.execs, query)
	return driver.RowsAffected(1), nil
}

type schemaRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *schemaRows) Columns() []string {
	return rows.columns
}

func (rows *schemaRows) Close() error { return nil }

func (rows *schemaRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.index])
	rows.index++
	return nil
}

type schemaStmt struct {
	state *schemaDriverState
	query string
}

func (stmt *schemaStmt) Close() error  { return nil }
func (stmt *schemaStmt) NumInput() int { return -1 }
func (stmt *schemaStmt) Exec(args []driver.Value) (driver.Result, error) {
	stmt.state.prepared = append(stmt.state.prepared, preparedExecution{query: stmt.query, args: append([]driver.Value(nil), args...)})
	if strings.HasPrefix(stmt.query, "INSERT INTO `products`") {
		stmt.state.productExecs++
		if stmt.state.failProductExecAt > 0 && stmt.state.productExecs == stmt.state.failProductExecAt {
			return nil, errInjectedProductWrite
		}
	}
	return driver.RowsAffected(1), nil
}
func (stmt *schemaStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("query not implemented")
}

type schemaTx struct {
	state *schemaDriverState
}

func (tx *schemaTx) Commit() error {
	tx.state.commits++
	return nil
}

func (tx *schemaTx) Rollback() error {
	tx.state.rollbacks++
	return nil
}
