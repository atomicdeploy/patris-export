package recordsink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

type SQLOptions struct {
	Driver        string
	DSN           string
	Table         string
	KeyField      string
	Batch         int
	ProtectedKeys []string
}

// SnapshotOptions controls authoritative snapshot reconciliation. ProtectedKeys
// are retained when absent from rows, which lets callers quarantine uncertain
// records without turning them into destructive deletions.
type SnapshotOptions struct {
	ProtectedKeys []string
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

func WriteXLSX(path string, rows []map[string]interface{}, keyField string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	f := excelize.NewFile()
	sheet := "Records"
	defaultSheet := f.GetSheetName(0)
	f.SetSheetName(defaultSheet, sheet)
	fields := recordmap.Fields(rows, keyField)
	for i, field := range fields {
		cellName, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cellName, field)
	}
	for rowIndex, row := range rows {
		for colIndex, field := range fields {
			cellName, _ := excelize.CoordinatesToCellName(colIndex+1, rowIndex+2)
			_ = f.SetCellValue(sheet, cellName, row[field])
		}
	}
	if len(fields) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(len(fields))
		_ = f.SetColWidth(sheet, "A", lastCol, 18)
		_ = f.AutoFilter(sheet, fmt.Sprintf("A1:%s1", lastCol), nil)
	}
	style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if len(fields) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(len(fields))
		_ = f.SetCellStyle(sheet, "A1", fmt.Sprintf("%s1", lastCol), style)
	}
	return f.SaveAs(path)
}

func WriteSQLite(path, table, keyField string, rows []map[string]interface{}, options ...SnapshotOptions) error {
	if path == "" {
		return fmt.Errorf("sqlite output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return SyncSnapshotSQL(ctx, db, "sqlite", table, keyField, rows, options...)
}

func SyncSQL(ctx context.Context, options SQLOptions, rows []map[string]interface{}) error {
	driver := strings.ToLower(strings.TrimSpace(options.Driver))
	if driver == "" {
		driver = "mysql"
	}
	if options.DSN == "" {
		return fmt.Errorf("%s DSN is required", driver)
	}
	db, err := sql.Open(driver, options.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if options.Batch <= 0 {
		options.Batch = 500
	}
	return SyncSnapshotSQL(ctx, db, driver, options.Table, options.KeyField, rows, SnapshotOptions{ProtectedKeys: options.ProtectedKeys})
}

// UpsertSQL writes a partial set of rows without deleting records that are not
// present in the supplied slice. Use SyncSnapshotSQL only when rows represent a
// complete authoritative snapshot.
func UpsertSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}) error {
	return writeSQL(ctx, db, driver, table, keyField, rows, false, SnapshotOptions{})
}

// SyncSnapshotSQL writes a complete authoritative snapshot. Row upserts and
// removal of keys absent from the snapshot share one transaction. An empty
// snapshot clears an existing table but does not create a table by itself.
func SyncSnapshotSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}, options ...SnapshotOptions) error {
	return writeSQL(ctx, db, driver, table, keyField, rows, true, mergeSnapshotOptions(options))
}

func writeSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}, reconcile bool, snapshot SnapshotOptions) error {
	table = sanitizeIdentifier(table)
	if table == "" {
		table = "patris_export"
	}
	if len(rows) == 0 {
		if reconcile {
			if len(snapshot.ProtectedKeys) == 0 {
				return clearSnapshotTable(ctx, db, driver, table)
			}
			return reconcileProtectedOnlySnapshot(ctx, db, driver, table, keyField, snapshot.ProtectedKeys)
		}
		return nil
	}
	fields := recordmap.Fields(rows, keyField)
	if len(fields) == 0 {
		return nil
	}
	if keyField == "" {
		keyField = fields[0]
	}
	if reconcile {
		if _, err := snapshotKeys(rows, keyField, snapshot.ProtectedKeys); err != nil {
			return err
		}
	}
	if err := createTable(ctx, db, driver, table, fields, keyField, rows); err != nil {
		return err
	}
	if err := ensureColumns(ctx, db, driver, table, fields, keyField, rows); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmtText := upsertStatement(driver, table, fields, keyField)
	stmt, err := tx.PrepareContext(ctx, stmtText)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		values := make([]interface{}, len(fields))
		for i, field := range fields {
			values[i] = sqlValue(row[field])
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if reconcile {
		keys, err := snapshotKeys(rows, keyField, snapshot.ProtectedKeys)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := deleteRowsAbsentFromSnapshot(ctx, tx, driver, table, keyField, keys); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func clearSnapshotTable(ctx context.Context, db *sql.DB, driver, table string) error {
	exists, err := sqlTableExists(ctx, db, driver, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", quoteIdent(driver, table))); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func reconcileProtectedOnlySnapshot(ctx context.Context, db *sql.DB, driver, table, keyField string, protected []string) error {
	if strings.TrimSpace(keyField) == "" {
		return fmt.Errorf("snapshot key field is required when protected keys are present")
	}
	keys, err := snapshotKeys(nil, keyField, protected)
	if err != nil {
		return err
	}
	exists, err := sqlTableExists(ctx, db, driver, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := deleteRowsAbsentFromSnapshot(ctx, tx, driver, table, keyField, keys); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
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

func mergeSnapshotOptions(values []SnapshotOptions) SnapshotOptions {
	merged := SnapshotOptions{}
	for _, value := range values {
		merged.ProtectedKeys = append(merged.ProtectedKeys, value.ProtectedKeys...)
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

func deleteRowsAbsentFromSnapshot(ctx context.Context, tx *sql.Tx, driver, table, keyField string, keys []string) error {
	tempTable := "_patris_snapshot_keys"
	drop := fmt.Sprintf("DROP TEMPORARY TABLE IF EXISTS %s", quoteIdent(driver, tempTable))
	if isSQLite(driver) {
		drop = fmt.Sprintf("DROP TABLE IF EXISTS temp.%s", quoteIdent(driver, tempTable))
	}
	if _, err := tx.ExecContext(ctx, drop); err != nil {
		return err
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
		return err
	}
	defer func() { _, _ = tx.ExecContext(context.Background(), drop) }()

	insert := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (?)",
		quoteIdent(driver, tempTable),
		quoteIdent(driver, "snapshot_key"),
	)
	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := stmt.ExecContext(ctx, key); err != nil {
			_ = stmt.Close()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		return err
	}

	target := quoteIdent(driver, table)
	targetKey := quoteIdent(driver, keyField)
	temp := quoteIdent(driver, tempTable)
	tempKey := quoteIdent(driver, "snapshot_key")
	remove := fmt.Sprintf(
		"DELETE FROM %s WHERE NOT EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s)",
		target, temp, temp, tempKey, target, targetKey,
	)
	if _, err := tx.ExecContext(ctx, remove); err != nil {
		return err
	}
	return nil
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
	case "sale_price_source", "purchase_price_source", "total_stock", "minimum_stock", "foreign_price", "weight_grams", "freight_cny_per_kg", "markup_percent", "irt_per_cny":
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

func upsertStatement(driver, table string, fields []string, keyField string) string {
	quoted := make([]string, len(fields))
	placeholders := make([]string, len(fields))
	for i, field := range fields {
		quoted[i] = quoteIdent(driver, field)
		placeholders[i] = "?"
	}
	if isSQLite(driver) {
		return fmt.Sprintf(
			"INSERT OR REPLACE INTO %s (%s) VALUES (%s)",
			quoteIdent(driver, table),
			strings.Join(quoted, ", "),
			strings.Join(placeholders, ", "),
		)
	}
	updates := []string{}
	for _, field := range fields {
		if field == keyField {
			continue
		}
		q := quoteIdent(driver, field)
		updates = append(updates, fmt.Sprintf("%s=VALUES(%s)", q, q))
	}
	if len(updates) == 0 {
		updates = append(updates, fmt.Sprintf("%s=VALUES(%s)", quoteIdent(driver, keyField), quoteIdent(driver, keyField)))
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON DUPLICATE KEY UPDATE %s",
		quoteIdent(driver, table),
		strings.Join(quoted, ", "),
		strings.Join(placeholders, ", "),
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
