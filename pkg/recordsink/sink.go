package recordsink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

type SQLOptions struct {
	Driver         string
	DSN            string
	Table          string
	KeyField       string
	Batch          int
	Reconciliation ReconciliationMode
	DryRun         bool
	ProtectedKeys  []string
}

// ReconciliationMode controls what happens to destination rows that are not
// present in the supplied records. UpsertOnly is intentionally the zero-value
// behavior so a missing or newly introduced setting cannot delete user data.
type ReconciliationMode string

const (
	UpsertOnly    ReconciliationMode = "upsert_only"
	DeleteMissing ReconciliationMode = "delete_missing"
)

// SQLResult is safe to return to callers and UI layers: it contains operation
// diagnostics only and never includes the destination DSN.
type SQLResult struct {
	Inserted       int                `json:"inserted"`
	Updated        int                `json:"updated"`
	Unchanged      int                `json:"unchanged"`
	Deleted        int                `json:"deleted"`
	Failed         int                `json:"failed"`
	ElapsedMS      int64              `json:"elapsed_ms"`
	DryRun         bool               `json:"dry_run"`
	Reconciliation ReconciliationMode `json:"reconciliation"`
}

// SnapshotOptions configures the compatibility WriteSQLite/SyncSnapshotSQL
// wrappers. WriteSQLite remains upsert_only unless Reconciliation is explicitly
// DeleteMissing. ProtectedKeys are retained during that opt-in reconciliation.
type SnapshotOptions struct {
	ProtectedKeys  []string
	Batch          int
	DryRun         bool
	Reconciliation ReconciliationMode
}

func WriteJSON(writer io.Writer, payload interface{}) error {
	enc := json.NewEncoder(writer)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func WriteCSV(writer io.Writer, rows []map[string]interface{}, keyField string) error {
	cw := csv.NewWriter(writer)
	fields := recordmap.Fields(rows, keyField)
	if len(fields) == 0 {
		return nil
	}
	if err := cw.Write(fields); err != nil {
		return err
	}
	for _, row := range rows {
		values := make([]string, len(fields))
		for i, field := range fields {
			values[i] = cell(row[field])
		}
		if err := cw.Write(values); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func CSVBytes(rows []map[string]interface{}, keyField string) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteCSV(&buf, rows, keyField); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func WriteSQLite(path, table, keyField string, rows []map[string]interface{}, options ...SnapshotOptions) error {
	snapshot := mergeSnapshotOptions(options)
	_, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table:          table,
		KeyField:       keyField,
		Batch:          snapshot.Batch,
		DryRun:         snapshot.DryRun,
		Reconciliation: snapshot.Reconciliation,
		ProtectedKeys:  snapshot.ProtectedKeys,
	}, rows)
	return err
}

// SyncSQLite runs the shared SQL sink against a SQLite file and returns
// operation counts. It is used by both one-shot and watch conversions.
func SyncSQLite(ctx context.Context, path string, options SQLOptions, rows []map[string]interface{}) (SQLResult, error) {
	if path == "" {
		return SQLResult{}, fmt.Errorf("sqlite output path is required")
	}
	dsn := path
	if options.DryRun {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			dsn = ":memory:"
		} else if err != nil {
			return SQLResult{}, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
			return SQLResult{}, err
		}
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return SQLResult{}, err
	}
	defer db.Close()
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	options.Driver = "sqlite"
	return SyncSQLDB(ctx, db, options, rows)
}

func SyncSQL(ctx context.Context, options SQLOptions, rows []map[string]interface{}) error {
	_, err := SyncSQLWithResult(ctx, options, rows)
	return err
}

// SyncSQLWithResult opens a configured SQL destination and reports the shared
// sink result. The returned value deliberately has no connection fields.
func SyncSQLWithResult(ctx context.Context, options SQLOptions, rows []map[string]interface{}) (SQLResult, error) {
	driver := strings.ToLower(strings.TrimSpace(options.Driver))
	if driver == "" {
		driver = "mysql"
	}
	if options.DSN == "" {
		return SQLResult{}, fmt.Errorf("%s DSN is required", driver)
	}
	db, err := sql.Open(driver, options.DSN)
	if err != nil {
		return SQLResult{}, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return SQLResult{}, fmt.Errorf("connect to %s destination: %w", driver, err)
	}
	options.Driver = driver
	return SyncSQLDB(ctx, db, options, rows)
}

// UpsertSQL writes a partial set of rows without deleting records that are not
// present in the supplied slice. Use SyncSnapshotSQL only when rows represent a
// complete authoritative snapshot.
func UpsertSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}) error {
	_, err := SyncSQLDB(ctx, db, SQLOptions{
		Driver: driver, Table: table, KeyField: keyField, Reconciliation: UpsertOnly,
	}, rows)
	return err
}

// SyncSnapshotSQL writes a complete authoritative snapshot. Row upserts and
// removal of keys absent from the snapshot share one transaction. An empty
// snapshot clears an existing table but does not create a table by itself.
func SyncSnapshotSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}, options ...SnapshotOptions) error {
	snapshot := mergeSnapshotOptions(options)
	_, err := SyncSQLDB(ctx, db, SQLOptions{
		Driver:         driver,
		Table:          table,
		KeyField:       keyField,
		Batch:          snapshot.Batch,
		DryRun:         snapshot.DryRun,
		Reconciliation: DeleteMissing,
		ProtectedKeys:  snapshot.ProtectedKeys,
	}, rows)
	return err
}

// SyncSQLDB executes the common SQLite/MySQL sink against an already-open
// database. Data writes and delete_missing reconciliation share one
// transaction; schema evolution remains additive and is performed before it.
func SyncSQLDB(ctx context.Context, db *sql.DB, options SQLOptions, rows []map[string]interface{}) (result SQLResult, err error) {
	started := time.Now()
	result.DryRun = options.DryRun
	result.Reconciliation, err = normalizeReconciliation(options.Reconciliation)
	defer func() {
		result.ElapsedMS = time.Since(started).Milliseconds()
		if err != nil {
			// Inserted/updated/unchanged/deleted are committed-result counts, not
			// a plan. Never report successes for an operation that rolled back or
			// whose commit outcome was not confirmed.
			result.Inserted = 0
			result.Updated = 0
			result.Unchanged = 0
			result.Deleted = 0
			result.Failed = len(rows)
		}
	}()
	if err != nil {
		return result, err
	}

	driver := strings.ToLower(strings.TrimSpace(options.Driver))
	if driver == "" {
		driver = "mysql"
	}
	table := sanitizeIdentifier(options.Table)
	if table == "" {
		table = "patris_export"
	}
	batch := effectiveBatchSize(driver, options.Batch, 1)
	tableExists, err := sqlTableExists(ctx, db, driver, table)
	if err != nil {
		return result, err
	}

	fields := recordmap.Fields(rows, options.KeyField)
	keyField := strings.TrimSpace(options.KeyField)
	if keyField == "" {
		if len(fields) > 0 {
			keyField = fields[0]
		} else if len(options.ProtectedKeys) > 0 || result.Reconciliation == DeleteMissing {
			return result, fmt.Errorf("SQL key field is required for reconciliation")
		}
	}
	keys, err := snapshotKeys(rows, keyField, nil)
	if err != nil {
		return result, err
	}
	if result.Reconciliation == UpsertOnly && len(rows) == 0 {
		return result, nil
	}
	if !tableExists && len(rows) == 0 {
		return result, nil
	}

	var beforeColumns map[string]bool
	if tableExists {
		if err := requireTransactionalTable(ctx, db, driver, table); err != nil {
			return result, err
		}
		beforeColumns, err = existingColumns(ctx, db, driver, table)
		if err != nil {
			return result, err
		}
		if keyField != "" && !beforeColumns[strings.ToLower(keyField)] {
			return result, fmt.Errorf("existing table %q has no key column %q", table, keyField)
		}
	}

	if !options.DryRun && len(rows) > 0 {
		if err := createTable(ctx, db, driver, table, fields, keyField, rows); err != nil {
			return result, err
		}
		// Re-check after CREATE IF NOT EXISTS so a concurrently created or
		// pre-existing non-transactional table cannot bypass the earlier probe.
		if err := requireTransactionalTable(ctx, db, driver, table); err != nil {
			return result, err
		}
		if err := ensureColumns(ctx, db, driver, table, fields, keyField, rows); err != nil {
			return result, err
		}
		tableExists = true
		beforeColumns = make(map[string]bool, len(fields))
		for _, field := range fields {
			beforeColumns[strings.ToLower(field)] = true
		}
	}
	if !tableExists {
		result.Inserted = len(rows)
		return result, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	toWrite, counts, err := classifyRows(ctx, tx, driver, table, keyField, fields, rows, beforeColumns, batch)
	if err != nil {
		return result, err
	}
	result.Inserted = counts.Inserted
	result.Updated = counts.Updated
	result.Unchanged = counts.Unchanged

	keepKeys := keys
	if result.Reconciliation == DeleteMissing {
		keepKeys, err = appendProtectedKeys(keys, options.ProtectedKeys)
		if err != nil {
			return result, err
		}
		if options.DryRun {
			result.Deleted, err = countRowsAbsentFromSnapshot(ctx, tx, driver, table, keyField, keepKeys)
			if err != nil {
				return result, err
			}
		}
	}
	if options.DryRun {
		return result, nil
	}

	if len(toWrite) > 0 {
		if err := upsertRows(ctx, tx, driver, table, keyField, fields, toWrite, options.Batch); err != nil {
			return result, err
		}
	}
	if result.Reconciliation == DeleteMissing {
		result.Deleted, err = deleteRowsAbsentFromSnapshot(ctx, tx, driver, table, keyField, keepKeys, options.Batch)
		if err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	committed = true
	return result, nil
}

func sqlTableExists(ctx context.Context, db *sql.DB, driver, table string) (bool, error) {
	query := "SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ? LIMIT 1"
	if isSQLite(driver) {
		query = "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1"
	}
	var exists int
	err := db.QueryRowContext(ctx, query, table).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

func requireTransactionalTable(ctx context.Context, db *sql.DB, driver, table string) error {
	if isSQLite(driver) {
		return nil
	}
	query := "SELECT ENGINE FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ? LIMIT 1"
	var engine sql.NullString
	if err := db.QueryRowContext(ctx, query, table).Scan(&engine); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("SQL table %q disappeared before synchronization", table)
		}
		return fmt.Errorf("inspect %s storage engine: %w", table, err)
	}
	if !engine.Valid || !strings.EqualFold(strings.TrimSpace(engine.String), "InnoDB") {
		name := strings.TrimSpace(engine.String)
		if name == "" {
			name = "unknown"
		}
		return fmt.Errorf("existing table %q uses non-transactional or unsupported engine %q; InnoDB is required", table, name)
	}
	return nil
}

func normalizeReconciliation(value ReconciliationMode) (ReconciliationMode, error) {
	switch ReconciliationMode(strings.ToLower(strings.TrimSpace(string(value)))) {
	case "", UpsertOnly:
		return UpsertOnly, nil
	case DeleteMissing:
		return DeleteMissing, nil
	default:
		return UpsertOnly, fmt.Errorf("unsupported SQL reconciliation mode %q", value)
	}
}

func effectiveBatchSize(driver string, requested, fieldCount int) int {
	if requested <= 0 {
		requested = 500
	}
	if fieldCount <= 0 {
		fieldCount = 1
	}
	parameterLimit := 65535
	if isSQLite(driver) {
		// Stay compatible with SQLite builds that retain the traditional 999
		// host-parameter limit rather than assuming a newer compile-time value.
		parameterLimit = 999
	}
	if maxRows := parameterLimit / fieldCount; requested > maxRows {
		requested = maxRows
	}
	if requested < 1 {
		return 1
	}
	return requested
}

func appendProtectedKeys(keys, protected []string) ([]string, error) {
	seen := make(map[string]struct{}, len(keys)+len(protected))
	result := append([]string(nil), keys...)
	for _, key := range result {
		seen[key] = struct{}{}
	}
	for index, value := range protected {
		key := strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("protected snapshot key %d is empty", index)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result, nil
}

type sqlCounts struct {
	Inserted  int
	Updated   int
	Unchanged int
}

func classifyRows(
	ctx context.Context,
	tx *sql.Tx,
	driver, table, keyField string,
	fields []string,
	rows []map[string]interface{},
	existingColumns map[string]bool,
	batch int,
) ([]map[string]interface{}, sqlCounts, error) {
	counts := sqlCounts{}
	if len(rows) == 0 {
		return nil, counts, nil
	}
	queryFields := make([]string, 0, len(fields))
	for _, field := range fields {
		if existingColumns[strings.ToLower(field)] {
			queryFields = append(queryFields, field)
		}
	}
	if len(queryFields) == 0 || !existingColumns[strings.ToLower(keyField)] {
		counts.Inserted = len(rows)
		return append([]map[string]interface{}(nil), rows...), counts, nil
	}

	batch = effectiveBatchSize(driver, batch, 1)
	toWrite := make([]map[string]interface{}, 0, len(rows))
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		quotedFields := make([]string, len(queryFields))
		for i, field := range queryFields {
			quotedFields[i] = quoteIdent(driver, field)
		}
		query := fmt.Sprintf(
			"SELECT %s FROM %s WHERE %s IN (%s)",
			strings.Join(quotedFields, ", "),
			quoteIdent(driver, table),
			quoteIdent(driver, keyField),
			placeholders,
		)
		args := make([]interface{}, len(chunk))
		for i, row := range chunk {
			args[i] = sqlValue(row[keyField])
		}
		found, err := queryExistingRows(ctx, tx, query, queryFields, keyField, args)
		if err != nil {
			return nil, counts, err
		}
		for _, row := range chunk {
			key := databaseKey(row[keyField])
			existing, exists := found[key]
			if !exists {
				counts.Inserted++
				toWrite = append(toWrite, row)
				continue
			}
			if rowMatchesExisting(row, existing, fields, existingColumns) {
				counts.Unchanged++
				continue
			}
			counts.Updated++
			toWrite = append(toWrite, row)
		}
	}
	return toWrite, counts, nil
}

func queryExistingRows(ctx context.Context, tx *sql.Tx, query string, fields []string, keyField string, args []interface{}) (map[string]map[string]interface{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]map[string]interface{})
	for rows.Next() {
		values := make([]interface{}, len(fields))
		destinations := make([]interface{}, len(fields))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		record := make(map[string]interface{}, len(fields))
		for i, field := range fields {
			record[field] = values[i]
		}
		result[databaseKey(record[keyField])] = record
	}
	return result, rows.Err()
}

func rowMatchesExisting(row, existing map[string]interface{}, fields []string, existingColumns map[string]bool) bool {
	for _, field := range fields {
		if !existingColumns[strings.ToLower(field)] {
			if row[field] != nil {
				return false
			}
			continue
		}
		if !sqlValuesEquivalent(field, row[field], existing[field]) {
			return false
		}
	}
	return true
}

func sqlValuesEquivalent(field string, expected, actual interface{}) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	kind := mergeValueKind(canonicalFieldKind(field), classifyValue(expected))
	if kind == valueKindBoolean {
		return booleanText(expected) == booleanText(actual)
	}
	if kind == valueKindInteger || kind == valueKindReal || kind == valueKindDecimal {
		expectedNumber, expectedOK := rationalValue(expected)
		actualNumber, actualOK := rationalValue(actual)
		if expectedOK && actualOK {
			return expectedNumber.Cmp(actualNumber) == 0
		}
	}
	return databaseText(expected) == databaseText(actual)
}

func rationalValue(value interface{}) (*big.Rat, bool) {
	text := databaseText(value)
	if text == "" {
		return nil, false
	}
	result := new(big.Rat)
	if _, ok := result.SetString(text); ok {
		return result, true
	}
	// big.Rat does not accept every exponent spelling used by float drivers.
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, false
	}
	result.SetFloat64(parsed)
	return result, true
}

func booleanText(value interface{}) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "1"
		}
		return "0"
	}
	text := strings.ToLower(strings.TrimSpace(databaseText(value)))
	if text == "true" {
		return "1"
	}
	if text == "false" {
		return "0"
	}
	return text
}

func databaseText(value interface{}) string {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return cell(value)
	}
}

func databaseKey(value interface{}) string {
	return databaseText(value)
}

func upsertRows(ctx context.Context, tx *sql.Tx, driver, table, keyField string, fields []string, rows []map[string]interface{}, requestedBatch int) error {
	batch := effectiveBatchSize(driver, requestedBatch, len(fields))
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]
		stmt, err := tx.PrepareContext(ctx, upsertStatement(driver, table, fields, keyField, len(chunk)))
		if err != nil {
			return err
		}
		values := make([]interface{}, 0, len(fields)*len(chunk))
		for _, row := range chunk {
			for _, field := range fields {
				values = append(values, sqlValue(row[field]))
			}
		}
		_, execErr := stmt.ExecContext(ctx, values...)
		closeErr := stmt.Close()
		if execErr != nil {
			return execErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func countRowsAbsentFromSnapshot(ctx context.Context, tx *sql.Tx, driver, table, keyField string, keys []string) (int, error) {
	keep := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keep[key] = struct{}{}
	}
	query := fmt.Sprintf("SELECT %s FROM %s", quoteIdent(driver, keyField), quoteIdent(driver, table))
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var value interface{}
		if err := rows.Scan(&value); err != nil {
			return 0, err
		}
		if _, exists := keep[databaseKey(value)]; !exists {
			count++
		}
	}
	return count, rows.Err()
}

func mergeSnapshotOptions(values []SnapshotOptions) SnapshotOptions {
	merged := SnapshotOptions{}
	for _, value := range values {
		merged.ProtectedKeys = append(merged.ProtectedKeys, value.ProtectedKeys...)
		if value.Batch != 0 {
			merged.Batch = value.Batch
		}
		if value.DryRun {
			merged.DryRun = true
		}
		if value.Reconciliation != "" {
			merged.Reconciliation = value.Reconciliation
		}
	}
	return merged
}

func snapshotKeys(rows []map[string]interface{}, keyField string, protected []string) ([]string, error) {
	seen := make(map[string]struct{}, len(rows)+len(protected))
	keys := make([]string, 0, len(rows)+len(protected))
	for index, row := range rows {
		value, exists := row[keyField]
		if !exists || value == nil || strings.TrimSpace(cell(value)) == "" {
			return nil, fmt.Errorf("snapshot row %d is missing key field %q", index, keyField)
		}
		key := cell(value)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("snapshot contains duplicate %s %q", keyField, key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for index, value := range protected {
		key := strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("protected snapshot key %d is empty", index)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys, nil
}

func deleteRowsAbsentFromSnapshot(ctx context.Context, tx *sql.Tx, driver, table, keyField string, keys []string, requestedBatch int) (int, error) {
	if len(keys) == 0 {
		result, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", quoteIdent(driver, table)))
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		return int(count), err
	}
	tempTable := "_patris_snapshot_keys"
	drop := fmt.Sprintf("DROP TEMPORARY TABLE IF EXISTS %s", quoteIdent(driver, tempTable))
	if isSQLite(driver) {
		drop = fmt.Sprintf("DROP TABLE IF EXISTS temp.%s", quoteIdent(driver, tempTable))
	}
	if _, err := tx.ExecContext(ctx, drop); err != nil {
		return 0, err
	}
	createPrefix := "CREATE TEMP TABLE"
	if !isSQLite(driver) {
		createPrefix = "CREATE TEMPORARY TABLE"
	}
	create := fmt.Sprintf(
		"%s %s (%s %s PRIMARY KEY)",
		createPrefix,
		quoteIdent(driver, tempTable),
		quoteIdent(driver, "snapshot_key"),
		columnType(driver, keyField, keyField, nil),
	)
	if _, err := tx.ExecContext(ctx, create); err != nil {
		return 0, err
	}
	defer func() { _, _ = tx.ExecContext(context.Background(), drop) }()

	batch := effectiveBatchSize(driver, requestedBatch, 1)
	for start := 0; start < len(keys); start += batch {
		end := start + batch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("(?),", len(chunk)), ",")
		insert := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES %s",
			quoteIdent(driver, tempTable),
			quoteIdent(driver, "snapshot_key"),
			placeholders,
		)
		stmt, err := tx.PrepareContext(ctx, insert)
		if err != nil {
			return 0, err
		}
		values := make([]interface{}, len(chunk))
		for i, key := range chunk {
			values[i] = key
		}
		_, execErr := stmt.ExecContext(ctx, values...)
		closeErr := stmt.Close()
		if execErr != nil {
			return 0, execErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}

	target := quoteIdent(driver, table)
	targetKey := quoteIdent(driver, keyField)
	temp := quoteIdent(driver, tempTable)
	tempKey := quoteIdent(driver, "snapshot_key")
	remove := fmt.Sprintf(
		"DELETE FROM %s WHERE NOT EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s)",
		target, temp, temp, tempKey, target, targetKey,
	)
	result, err := tx.ExecContext(ctx, remove)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func createTable(ctx context.Context, db *sql.DB, driver, table string, fields []string, keyField string, rows []map[string]interface{}) error {
	cols := make([]string, 0, len(fields))
	for _, field := range fields {
		def := fmt.Sprintf("%s %s", quoteIdent(driver, field), columnType(driver, field, keyField, rows))
		if field == keyField {
			def += " PRIMARY KEY"
		}
		cols = append(cols, def)
	}
	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", quoteIdent(driver, table), strings.Join(cols, ", "))
	if !isSQLite(driver) {
		query += " ENGINE=InnoDB"
	}
	_, err := db.ExecContext(ctx, query)
	return err
}

func ensureColumns(ctx context.Context, db *sql.DB, driver, table string, fields []string, keyField string, rows []map[string]interface{}) error {
	existing, err := existingColumns(ctx, db, driver, table)
	if err != nil {
		return err
	}
	for _, field := range fields {
		if existing[strings.ToLower(field)] {
			continue
		}
		definition := fmt.Sprintf("%s %s", quoteIdent(driver, field), columnType(driver, field, keyField, rows))
		query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quoteIdent(driver, table), definition)
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("add %s.%s column: %w", table, field, err)
		}
		existing[strings.ToLower(field)] = true
	}
	return nil
}

func existingColumns(ctx context.Context, db *sql.DB, driver, table string) (map[string]bool, error) {
	result := map[string]bool{}
	if isSQLite(driver) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(driver, table)))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue interface{}
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				return nil, err
			}
			result[strings.ToLower(name)] = true
		}
		return result, rows.Err()
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf("SHOW COLUMNS FROM %s", quoteIdent(driver, table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field, dataType, nullable, key string
		var defaultValue, extra interface{}
		if err := rows.Scan(&field, &dataType, &nullable, &key, &defaultValue, &extra); err != nil {
			return nil, err
		}
		result[strings.ToLower(field)] = true
	}
	return result, rows.Err()
}

func columnType(driver, field, keyField string, rows []map[string]interface{}) string {
	if field == keyField {
		if isSQLite(driver) {
			return "TEXT"
		}
		return "VARCHAR(191)"
	}
	kind := canonicalFieldKind(field)
	for _, row := range rows {
		value, exists := row[field]
		if !exists || value == nil {
			continue
		}
		kind = mergeValueKind(kind, classifyValue(value))
		if kind == valueKindText {
			break
		}
	}
	if isSQLite(driver) {
		switch kind {
		case valueKindInteger, valueKindBoolean:
			return "INTEGER"
		case valueKindDecimal:
			return "DECIMAL_TEXT"
		case valueKindReal:
			return "REAL"
		default:
			return "TEXT"
		}
	}
	switch kind {
	case valueKindInteger:
		return "BIGINT"
	case valueKindBoolean:
		return "TINYINT(1)"
	case valueKindReal:
		return "DECIMAL(30,10)"
	case valueKindDecimal:
		return "DECIMAL(65,30)"
	default:
		return "LONGTEXT"
	}
}

func canonicalFieldKind(field string) valueKind {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "final_price":
		return valueKindInteger
	case "sale_price_source", "purchase_price_source", "total_stock", "minimum_stock", "foreign_price", "weight_grams", "shipping_price_per_kg_cny", "markup_percent", "irt_per_cny":
		return valueKindReal
	default:
		return valueKindUnknown
	}
}

type valueKind uint8

const (
	valueKindUnknown valueKind = iota
	valueKindBoolean
	valueKindInteger
	valueKindReal
	valueKindDecimal
	valueKindText
)

func classifyValue(value interface{}) valueKind {
	switch value.(type) {
	case bool:
		return valueKindBoolean
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return valueKindInteger
	case float32, float64:
		return valueKindReal
	case json.Number:
		return valueKindDecimal
	default:
		return valueKindText
	}
}

func mergeValueKind(current, next valueKind) valueKind {
	if current == valueKindUnknown {
		return next
	}
	if current == next {
		return current
	}
	if (current == valueKindInteger && next == valueKindReal) || (current == valueKindReal && next == valueKindInteger) {
		return valueKindReal
	}
	if next == valueKindDecimal || current == valueKindDecimal {
		if current == valueKindText || next == valueKindText {
			return valueKindText
		}
		return valueKindDecimal
	}
	return valueKindText
}

func isSQLite(driver string) bool {
	return driver == "sqlite3" || driver == "sqlite"
}

func upsertStatement(driver, table string, fields []string, keyField string, rowCount int) string {
	quoted := make([]string, len(fields))
	rowPlaceholders := make([]string, len(fields))
	for i, field := range fields {
		quoted[i] = quoteIdent(driver, field)
		rowPlaceholders[i] = "?"
	}
	if rowCount < 1 {
		rowCount = 1
	}
	valueGroups := make([]string, rowCount)
	for i := range valueGroups {
		valueGroups[i] = "(" + strings.Join(rowPlaceholders, ", ") + ")"
	}
	updates := []string{}
	for _, field := range fields {
		if field == keyField {
			continue
		}
		q := quoteIdent(driver, field)
		if isSQLite(driver) {
			updates = append(updates, fmt.Sprintf("%s=excluded.%s", q, q))
		} else {
			updates = append(updates, fmt.Sprintf("%s=VALUES(%s)", q, q))
		}
	}
	if isSQLite(driver) {
		conflictAction := "DO NOTHING"
		if len(updates) > 0 {
			conflictAction = "DO UPDATE SET " + strings.Join(updates, ", ")
		}
		return fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES %s ON CONFLICT (%s) %s",
			quoteIdent(driver, table),
			strings.Join(quoted, ", "),
			strings.Join(valueGroups, ", "),
			quoteIdent(driver, keyField),
			conflictAction,
		)
	}
	if len(updates) == 0 {
		updates = append(updates, fmt.Sprintf("%s=VALUES(%s)", quoteIdent(driver, keyField), quoteIdent(driver, keyField)))
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s ON DUPLICATE KEY UPDATE %s",
		quoteIdent(driver, table),
		strings.Join(quoted, ", "),
		strings.Join(valueGroups, ", "),
		strings.Join(updates, ", "),
	)
}

func cell(value interface{}) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && (reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Struct) {
		data, err := json.Marshal(value)
		if err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", value)
}

func sqlValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	switch value.(type) {
	case string, []byte, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool, time.Time:
		return value
	case json.Number:
		return value.(json.Number).String()
	default:
		return cell(value)
	}
}

func quoteIdent(driver, ident string) string {
	ident = sanitizeIdentifier(ident)
	if ident == "" {
		ident = "field"
	}
	quote := "`"
	if isSQLite(driver) {
		quote = `"`
	}
	return quote + strings.ReplaceAll(ident, quote, quote+quote) + quote
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "t_" + out
	}
	return out
}

func SortRows(rows []map[string]interface{}, keyField string) {
	sort.SliceStable(rows, func(i, j int) bool {
		return fmt.Sprintf("%v", rows[i][keyField]) < fmt.Sprintf("%v", rows[j][keyField])
	})
}
