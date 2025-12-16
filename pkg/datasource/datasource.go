package datasource

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/converter"
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
	path      string
	converter converter.CharMapping
}

// JSONDataSource represents a transformed JSON file
type JSONDataSource struct {
	path string
}

// NewDataSource creates a new data source based on the file extension
func NewDataSource(path string, charMap converter.CharMapping) (DataSource, error) {
	ext := strings.ToLower(filepath.Ext(path))
	
	switch ext {
	case ".json":
		return &JSONDataSource{path: path}, nil
	case ".db":
		return &ParadoxDataSource{path: path, converter: charMap}, nil
	default:
		return nil, fmt.Errorf("unsupported file type: %s (expected .db or .json)", ext)
	}
}

// GetRecords implements DataSource for ParadoxDataSource
func (p *ParadoxDataSource) GetRecords() ([]map[string]interface{}, error) {
	db, err := paradox.Open(p.path)
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
	data, err := os.ReadFile(j.path)
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
