package recorddiff

import (
	"reflect"
	"testing"
	"time"
)

func TestBetweenUsesCustomKeyAndCapturesCompleteChanges(t *testing.T) {
	at := time.Date(2026, 7, 16, 6, 30, 0, 0, time.FixedZone("IRST", 3*60*60+30*60))
	previous := []map[string]interface{}{
		{"sku": "100", "title": "Bolt", "price": 10.0, "stock": 2},
		{"sku": "200", "title": "Nut", "price": 5.0},
	}
	current := []map[string]interface{}{
		{"sku": "100", "title": "Bolt", "price": 12.0},
		{"sku": "300", "title": "Washer", "price": 3.0},
	}

	changes := Between(previous, current, "sku", at)
	if changes.Timestamp != "2026-07-16T06:30:00+03:30" || changes.KeyField != "sku" || changes.TotalCount != 2 {
		t.Fatalf("unexpected metadata: %#v", changes)
	}
	if len(changes.Added) != 1 || changes.Added[0]["sku"] != "300" {
		t.Fatalf("unexpected added rows: %#v", changes.Added)
	}
	if !reflect.DeepEqual(changes.Deleted, []string{"200"}) {
		t.Fatalf("unexpected deleted keys: %#v", changes.Deleted)
	}
	if len(changes.Modified) != 1 {
		t.Fatalf("unexpected modified rows: %#v", changes.Modified)
	}
	modified := changes.Modified[0]
	if modified.Code != "100" || modified.Record["title"] != "Bolt" || modified.Record["price"] != 12.0 {
		t.Fatalf("modified record does not preserve the complete new row: %#v", modified)
	}
	if !reflect.DeepEqual(modified.ChangedFields, []string{"price", "stock"}) {
		t.Fatalf("changed fields should be sorted and include removed fields: %#v", modified.ChangedFields)
	}
	if modified.OldValues["price"] != 10.0 || modified.NewValues["price"] != 12.0 || modified.NewValues["stock"] != nil {
		t.Fatalf("unexpected field values: old=%#v new=%#v", modified.OldValues, modified.NewValues)
	}
}

func TestBetweenInitialSnapshotCopiesRowsWithoutMutatingInputs(t *testing.T) {
	current := []map[string]interface{}{{"Code": "100", "Name": "Original"}}
	changes := Between(nil, current, "", time.Time{})
	if len(changes.Added) != 1 || changes.KeyField != "Code" {
		t.Fatalf("unexpected initial changes: %#v", changes)
	}
	changes.Added[0]["Name"] = "Changed"
	if current[0]["Name"] != "Original" {
		t.Fatal("Between mutated the caller's current snapshot")
	}
	if changes.Empty() {
		t.Fatal("initial snapshot must not be empty")
	}
}

func TestBetweenIsStableAndDoesNotMutateInputs(t *testing.T) {
	previous := []map[string]interface{}{
		{"Code": "2", "z": 1, "a": 1},
		{"Code": "1", "value": "deleted"},
	}
	current := []map[string]interface{}{
		{"Code": "2", "z": 2, "a": 2},
		{"Code": "3", "value": "added"},
	}
	previousCopy := []map[string]interface{}{
		{"Code": "2", "z": 1, "a": 1},
		{"Code": "1", "value": "deleted"},
	}
	currentCopy := []map[string]interface{}{
		{"Code": "2", "z": 2, "a": 2},
		{"Code": "3", "value": "added"},
	}

	changes := Between(previous, current, "Code", time.Unix(1, 0))
	if !reflect.DeepEqual(changes.Modified[0].ChangedFields, []string{"a", "z"}) {
		t.Fatalf("changed fields are not deterministic: %#v", changes.Modified[0].ChangedFields)
	}
	if !reflect.DeepEqual(previous, previousCopy) || !reflect.DeepEqual(current, currentCopy) {
		t.Fatal("Between mutated an input snapshot")
	}
	added, modified, deleted := changes.Counts()
	if added != 1 || modified != 1 || deleted != 1 {
		t.Fatalf("unexpected counts: %d %d %d", added, modified, deleted)
	}
}

func TestBetweenIgnoresSortFieldsAndOmitsThemFromPayloads(t *testing.T) {
	at := time.Unix(1, 0)
	previous := []map[string]interface{}{{"Code": "100", "Name": "Old", "Sort": "a", "SortCode": "1"}}
	current := []map[string]interface{}{{"Code": "100", "Name": "New", "Sort": "b", "SortCode": "2"}}

	changes := Between(previous, current, "Code", at)
	if len(changes.Modified) != 1 {
		t.Fatalf("modified changes = %#v, want one non-Sort change", changes.Modified)
	}
	modified := changes.Modified[0]
	if !reflect.DeepEqual(modified.ChangedFields, []string{"Name"}) {
		t.Fatalf("changed fields = %#v, want only Name", modified.ChangedFields)
	}
	for _, field := range []string{"Sort", "SortCode"} {
		if _, exists := modified.Record[field]; exists {
			t.Fatalf("ignored field %s leaked into modified record: %#v", field, modified.Record)
		}
		if _, exists := modified.OldValues[field]; exists {
			t.Fatalf("ignored field %s leaked into old values: %#v", field, modified.OldValues)
		}
		if _, exists := modified.NewValues[field]; exists {
			t.Fatalf("ignored field %s leaked into new values: %#v", field, modified.NewValues)
		}
	}

	onlySortChanged := Between(previous, []map[string]interface{}{{"Code": "100", "Name": "Old", "Sort": "changed"}}, "Code", at)
	if !onlySortChanged.Empty() {
		t.Fatalf("Sort-only mutation produced a change: %#v", onlySortChanged)
	}

	initial := Between(nil, current, "Code", at)
	if len(initial.Added) != 1 {
		t.Fatalf("initial added rows = %#v", initial.Added)
	}
	for _, field := range []string{"Sort", "SortCode"} {
		if _, exists := initial.Added[0][field]; exists {
			t.Fatalf("ignored field %s leaked into initial payload: %#v", field, initial.Added[0])
		}
	}
}
