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
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

type SQLOptions struct {
	Driver   string
	DSN      string
	Table    string
	KeyField string
	Batch    int
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

func WriteSQLite(path, table, keyField string, rows []map[string]interface{}) error {
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
	return UpsertSQL(ctx, db, "sqlite", table, keyField, rows)
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
	return UpsertSQL(ctx, db, driver, options.Table, options.KeyField, rows)
}

func UpsertSQL(ctx context.Context, db *sql.DB, driver, table, keyField string, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	table = sanitizeIdentifier(table)
	if table == "" {
		table = "patris_export"
	}
	fields := recordmap.Fields(rows, keyField)
	if len(fields) == 0 {
		return nil
	}
	if keyField == "" {
		keyField = fields[0]
	}
	if err := createTable(ctx, db, driver, table, fields, keyField); err != nil {
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
	return tx.Commit()
}

func createTable(ctx context.Context, db *sql.DB, driver, table string, fields []string, keyField string) error {
	cols := make([]string, 0, len(fields))
	for _, field := range fields {
		def := fmt.Sprintf("%s TEXT", quoteIdent(driver, field))
		if field == keyField {
			def += " PRIMARY KEY"
		}
		cols = append(cols, def)
	}
	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", quoteIdent(driver, table), strings.Join(cols, ", "))
	_, err := db.ExecContext(ctx, query)
	return err
}

func upsertStatement(driver, table string, fields []string, keyField string) string {
	quoted := make([]string, len(fields))
	placeholders := make([]string, len(fields))
	for i, field := range fields {
		quoted[i] = quoteIdent(driver, field)
		placeholders[i] = "?"
	}
	if driver == "sqlite3" || driver == "sqlite" {
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
	case []interface{}, []string, []int, []float64, map[string]interface{}:
		data, err := json.Marshal(v)
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
	if driver == "sqlite3" || driver == "sqlite" {
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
