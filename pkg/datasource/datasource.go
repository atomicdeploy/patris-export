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

	// Convert and transform records to match JSON export format
	exp := converter.NewExporter(converter.Patris2Fa)
	transformed := exp.ConvertAndTransformRecords(records)

	// Convert map to array of records
	result := make([]map[string]interface{}, 0, len(transformed))
	for _, record := range transformed {
		if recordMap, ok := record.(map[string]interface{}); ok {
			result = append(result, recordMap)
		}
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

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// The JSON file should match the transformed format with Code as keys
	// Extract records from the map
	records := make([]map[string]interface{}, 0, len(result))
	for _, value := range result {
		if recordMap, ok := value.(map[string]interface{}); ok {
			records = append(records, recordMap)
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
