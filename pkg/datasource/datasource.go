package datasource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
)

// DataSource represents an abstract data source that can be either a Paradox DB or JSON file
type DataSource interface {
	// GetRecords returns all records from the data source
	GetRecords() ([]map[string]interface{}, error)
	// GetRawRecords returns records before Patris conversion/output shaping.
	GetRawRecords() ([]map[string]interface{}, error)
	// GetPath returns the file path of the data source
	GetPath() string
	// Close closes the data source
	Close() error
}

// ContextDataSource cooperatively stops source preparation when a caller's
// bounded operation is cancelled. DataSource remains backward compatible for
// external implementations; the built-in sources implement both interfaces.
type ContextDataSource interface {
	GetRawRecordsContext(context.Context) ([]map[string]interface{}, error)
}

// ParadoxDataSource represents a Paradox database file
type ParadoxDataSource struct {
	path        string
	converter   converter.CharMapping
	useTempFile bool
}

// JSONDataSource represents a transformed JSON file
type JSONDataSource struct {
	path string
}

// NewDataSource creates a new data source based on the file extension
func NewDataSource(path string, charMap converter.CharMapping, useTempFile ...bool) (DataSource, error) {
	ext := sourceExt(path)
	copyBeforeRead := true
	if len(useTempFile) > 0 {
		copyBeforeRead = useTempFile[0]
	}
	if filecopy.IsURL(path) {
		copyBeforeRead = true
	}

	switch ext {
	case ".json":
		return &JSONDataSource{path: path}, nil
	case ".db":
		return &ParadoxDataSource{path: path, converter: charMap, useTempFile: copyBeforeRead}, nil
	default:
		return nil, fmt.Errorf("unsupported file type: %s (expected .db or .json)", ext)
	}
}

func sourceExt(path string) string {
	if filecopy.IsURL(path) {
		if u, err := url.Parse(path); err == nil {
			return strings.ToLower(filepath.Ext(u.Path))
		}
	}
	return strings.ToLower(filepath.Ext(path))
}

// GetRecords implements DataSource for ParadoxDataSource
func (p *ParadoxDataSource) GetRecords() ([]map[string]interface{}, error) {
	records, err := p.GetRawRecords()
	if err != nil {
		return nil, err
	}

	// Convert and transform records to match JSON export format.
	exp := converter.NewExporter(converter.Patris2Fa)
	transformed := exp.ConvertAndTransformRecords(mapsToParadox(records))

	// Convert keyed output back to ordered records and preserve Code for
	// WebSocket diffing, CSV export, and downstream sync targets.
	result := make([]map[string]interface{}, 0, len(transformed))
	for code, record := range transformed {
		if recordMap, ok := record.(map[string]interface{}); ok {
			row := make(map[string]interface{}, len(recordMap)+1)
			row["Code"] = code
			for key, value := range recordMap {
				row[key] = value
			}
			result = append(result, row)
		}
	}

	return result, nil
}

// GetRawRecords implements DataSource for ParadoxDataSource without applying
// encoding conversion, ANBAR compaction, Code-keying, or custom mapping.
func (p *ParadoxDataSource) GetRawRecords() ([]map[string]interface{}, error) {
	return p.GetRawRecordsContext(context.Background())
}

// GetRawRecordsContext implements cooperative cancellation for Paradox source
// preparation. Individual native pxlib calls cannot be interrupted, but
// cancellation is checked around each record/field call.
func (p *ParadoxDataSource) GetRawRecordsContext(ctx context.Context) ([]map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pathToOpen := p.path
	cleanup := func() {}
	if filecopy.IsURL(p.path) {
		tempFileInfo, err := filecopy.DownloadToTempContext(ctx, p.path)
		if err != nil {
			return nil, fmt.Errorf("failed to download database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	} else if p.useTempFile {
		tempFileInfo, err := filecopy.CopyToTempContext(ctx, p.path)
		if err != nil {
			return nil, fmt.Errorf("failed to copy database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	}
	defer cleanup()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := paradox.Open(pathToOpen)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	records, err := db.GetRecordsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read records: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(records))
	for index, record := range records {
		if index%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		row := make(map[string]interface{}, len(record))
		for key, value := range record {
			row[key] = value
		}
		result = append(result, row)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetPath implements DataSource for ParadoxDataSource
func (p *ParadoxDataSource) GetPath() string {
	return p.path
}

// Close implements DataSource for ParadoxDataSource
func (p *ParadoxDataSource) Close() error {
	return nil
}

// GetRecords implements DataSource for JSONDataSource
func (j *JSONDataSource) GetRecords() ([]map[string]interface{}, error) {
	return j.GetRawRecords()
}

// GetRawRecords implements DataSource for JSONDataSource.
func (j *JSONDataSource) GetRawRecords() ([]map[string]interface{}, error) {
	return j.GetRawRecordsContext(context.Background())
}

// GetRawRecordsContext implements cooperative cancellation for JSON download,
// file reading, parsing boundaries, and row materialization.
func (j *JSONDataSource) GetRawRecordsContext(ctx context.Context) ([]map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pathToRead := j.path
	cleanup := func() {}
	if filecopy.IsURL(j.path) {
		tempFileInfo, err := filecopy.DownloadToTempContext(ctx, j.path)
		if err != nil {
			return nil, fmt.Errorf("failed to download JSON to temp: %w", err)
		}
		pathToRead = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	}
	defer cleanup()

	data, err := readFileContext(ctx, pathToRead)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file: %w", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return rows, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// The JSON file should match the transformed format with Code as keys
	// Extract records from the map
	records := make([]map[string]interface{}, 0, len(result))
	index := 0
	for code, value := range result {
		if index%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		index++
		if recordMap, ok := value.(map[string]interface{}); ok {
			row := make(map[string]interface{}, len(recordMap)+1)
			if _, hasCode := recordMap["Code"]; !hasCode {
				row["Code"] = code
			}
			for key, fieldValue := range recordMap {
				row[key] = fieldValue
			}
			records = append(records, row)
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buffer bytes.Buffer
	chunk := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, readErr := file.Read(chunk)
		if read > 0 {
			if _, err := buffer.Write(chunk[:read]); err != nil {
				return nil, err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return buffer.Bytes(), nil
			}
			return nil, readErr
		}
	}
}

// GetPath implements DataSource for JSONDataSource
func (j *JSONDataSource) GetPath() string {
	return j.path
}

// Close implements DataSource for JSONDataSource
func (j *JSONDataSource) Close() error {
	return nil
}

func mapsToParadox(rows []map[string]interface{}) []paradox.Record {
	records := make([]paradox.Record, 0, len(rows))
	for _, row := range rows {
		record := paradox.Record{}
		for key, value := range row {
			record[key] = value
		}
		records = append(records, record)
	}
	return records
}
