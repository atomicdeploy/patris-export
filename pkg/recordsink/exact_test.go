package recordsink

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExactDecimalTokenSurvivesCSVXLSXAndSQLiteBoundaries(t *testing.T) {
	exact := "0.1000000000000000006"
	rows := []map[string]interface{}{{"product_code": "A", "foreign_price": json.Number(exact)}}

	csv, err := CSVBytes(rows, "product_code")
	if err != nil || !strings.Contains(string(csv), exact) {
		t.Fatalf("CSV lost exact decimal token: err=%v csv=%q", err, csv)
	}

	xlsxPath := filepath.Join(t.TempDir(), "exact.xlsx")
	if err := WriteXLSX(xlsxPath, rows, "product_code"); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	cellValue, err := book.GetCellValue("Records", "B2")
	if err != nil || cellValue != exact {
		t.Fatalf("XLSX lost exact decimal token: value=%q err=%v", cellValue, err)
	}

	sqlitePath := filepath.Join(t.TempDir(), "exact.sqlite")
	if err := WriteSQLite(sqlitePath, "products", "product_code", rows); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dataType, stored string
	if err := db.QueryRow(`SELECT typeof(foreign_price), foreign_price FROM products WHERE product_code = 'A'`).Scan(&dataType, &stored); err != nil {
		t.Fatal(err)
	}
	if dataType != "text" || stored != exact {
		t.Fatalf("SQLite lost exact decimal token: typeof=%q value=%q", dataType, stored)
	}
}

func TestExactDecimalTokenSurvivesMySQLBindBoundary(t *testing.T) {
	exact := "0.1000000000000000006"
	state := &schemaDriverState{}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	rows := []map[string]interface{}{{"product_code": "A", "foreign_price": json.Number(exact)}}
	if err := SyncSnapshotSQL(context.Background(), db, "mysql", "products", "product_code", rows); err != nil {
		t.Fatal(err)
	}
	if got := columnType("mysql", "foreign_price", "product_code", rows); got != "DECIMAL(65,30)" {
		t.Fatalf("exact MySQL decimal type = %q", got)
	}
	found := false
	for _, execution := range state.prepared {
		if strings.Contains(execution.query, "INSERT INTO `products`") && len(execution.args) == 2 && execution.args[1] == exact {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MySQL bind lost exact decimal token: %#v", state.prepared)
	}
}
