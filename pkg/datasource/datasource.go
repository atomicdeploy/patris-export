package datasource

import (
	"encoding/json"
	"fmt"
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
	pathToOpen := p.path
	cleanup := func() {}
	if filecopy.IsURL(p.path) {
		tempFileInfo, err := filecopy.DownloadToTemp(p.path)
		if err != nil {
			return nil, fmt.Errorf("failed to download database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	} else if p.useTempFile {
		tempFileInfo, err := filecopy.CopyToTemp(p.path)
		if err != nil {
			return nil, fmt.Errorf("failed to copy database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	}
	defer cleanup()

	db, err := paradox.Open(pathToOpen)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	records, err := db.GetRecords()
	if err != nil {
		return nil, fmt.Errorf("failed to read records: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		row := make(map[string]interface{}, len(record))
		for key, value := range record {
			row[key] = value
		}
		result = append(result, row)
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
	pathToRead := j.path
	cleanup := func() {}
	if filecopy.IsURL(j.path) {
		tempFileInfo, err := filecopy.DownloadToTemp(j.path)
		if err != nil {
			return nil, fmt.Errorf("failed to download JSON to temp: %w", err)
		}
		pathToRead = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	}
	defer cleanup()

	data, err := os.ReadFile(pathToRead)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file: %w", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err == nil {
		return rows, nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// The JSON file should match the transformed format with Code as keys
	// Extract records from the map
	records := make([]map[string]interface{}, 0, len(result))
	for code, value := range result {
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

	return records, nil
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
