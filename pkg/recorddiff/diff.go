package recorddiff

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// RecordChange describes one modified record while preserving the legacy
// WebSocket fields and carrying the complete new record for downstream sinks.
type RecordChange struct {
	Code          string                 `json:"code"`
	ChangeType    string                 `json:"change_type"`
	OldValues     map[string]interface{} `json:"old_values,omitempty"`
	NewValues     map[string]interface{} `json:"new_values,omitempty"`
	ChangedFields []string               `json:"changed_fields,omitempty"`
	Record        map[string]interface{} `json:"record,omitempty"`
}

// ChangeSet is the shared incremental payload used by the WebSocket server,
// CLI watch webhooks, and command sinks.
type ChangeSet struct {
	Type       string                   `json:"type"`
	Timestamp  string                   `json:"timestamp"`
	Added      []map[string]interface{} `json:"added,omitempty"`
	Deleted    []string                 `json:"deleted,omitempty"`
	Modified   []RecordChange           `json:"modified,omitempty"`
	TotalCount int                      `json:"total_count"`
	KeyField   string                   `json:"key_field"`
	Raw        bool                     `json:"raw"`
}

// Between compares two transformed snapshots using keyField. It never mutates
// either input and keeps record ordering stable while sorting changed fields.
func Between(previous, current []map[string]interface{}, keyField string, at time.Time) ChangeSet {
	keyField = strings.TrimSpace(keyField)
	if keyField == "" {
		keyField = "Code"
	}
	if at.IsZero() {
		at = time.Now()
	}

	result := ChangeSet{
		Type:       "update",
		Timestamp:  at.Format(time.RFC3339),
		TotalCount: len(current),
		KeyField:   keyField,
	}

	if len(previous) == 0 {
		result.Added = copyRows(current)
		return result
	}

	oldByKey, oldOrder := indexRows(previous, keyField)
	newByKey, newOrder := indexRows(current, keyField)

	for _, key := range newOrder {
		newRecord := newByKey[key]
		oldRecord, exists := oldByKey[key]
		if !exists {
			result.Added = append(result.Added, copyRecord(newRecord))
			continue
		}

		changedFields, oldValues, newValues := changedValues(oldRecord, newRecord, keyField)
		if len(changedFields) == 0 {
			continue
		}
		result.Modified = append(result.Modified, RecordChange{
			Code:          key,
			ChangeType:    "modified",
			OldValues:     oldValues,
			NewValues:     newValues,
			ChangedFields: changedFields,
			Record:        copyRecord(newRecord),
		})
	}

	for _, key := range oldOrder {
		if _, exists := newByKey[key]; !exists {
			result.Deleted = append(result.Deleted, key)
		}
	}

	return result
}

func (changes ChangeSet) Empty() bool {
	return len(changes.Added) == 0 && len(changes.Modified) == 0 && len(changes.Deleted) == 0
}

func (changes ChangeSet) Counts() (added, modified, deleted int) {
	return len(changes.Added), len(changes.Modified), len(changes.Deleted)
}

// Map preserves the map-shaped WebSocket payload used by existing clients.
func (changes ChangeSet) Map() map[string]interface{} {
	result := map[string]interface{}{
		"type":        changes.Type,
		"timestamp":   changes.Timestamp,
		"total_count": changes.TotalCount,
		"key_field":   changes.KeyField,
		"raw":         changes.Raw,
	}
	if len(changes.Added) > 0 {
		result["added"] = changes.Added
	}
	if len(changes.Deleted) > 0 {
		result["deleted"] = changes.Deleted
	}
	if len(changes.Modified) > 0 {
		result["modified"] = changes.Modified
	}
	return result
}

func indexRows(rows []map[string]interface{}, keyField string) (map[string]map[string]interface{}, []string) {
	indexed := make(map[string]map[string]interface{}, len(rows))
	order := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		value, exists := row[keyField]
		if !exists {
			continue
		}
		key := fmt.Sprintf("%v", value)
		indexed[key] = row
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			order = append(order, key)
		}
	}
	return indexed, order
}

func changedValues(oldRecord, newRecord map[string]interface{}, keyField string) ([]string, map[string]interface{}, map[string]interface{}) {
	fields := make(map[string]struct{}, len(oldRecord)+len(newRecord))
	for field := range oldRecord {
		if field != keyField {
			fields[field] = struct{}{}
		}
	}
	for field := range newRecord {
		if field != keyField {
			fields[field] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(fields))
	for field := range fields {
		ordered = append(ordered, field)
	}
	sort.Strings(ordered)

	changed := make([]string, 0, len(ordered))
	oldValues := make(map[string]interface{})
	newValues := make(map[string]interface{})
	for _, field := range ordered {
		oldValue, hadOld := oldRecord[field]
		newValue, hasNew := newRecord[field]
		if hadOld == hasNew && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		changed = append(changed, field)
		if hadOld {
			oldValues[field] = oldValue
		} else {
			oldValues[field] = nil
		}
		if hasNew {
			newValues[field] = newValue
		} else {
			newValues[field] = nil
		}
	}
	return changed, oldValues, newValues
}

func copyRows(rows []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		result = append(result, copyRecord(row))
	}
	return result
}

func copyRecord(record map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(record))
	for key, value := range record {
		result[key] = value
	}
	return result
}
