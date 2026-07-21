package naming

import (
	"reflect"
	"testing"
)

func TestWarningsDetectDeclaredConventionsWithoutSourceValues(t *testing.T) {
	row := map[string]interface{}{
		"Code":   "A100",
		"Name":   "  کالاWidget2 ",
		"Serial": "ABC123",
	}
	want := []string{
		"naming_leading_space:name",
		"naming_mixed_kind_without_space:name",
		"naming_multiple_spaces:name",
		"naming_trailing_space:name",
	}
	if got := Warnings(row); !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v", got, want)
	}
	for _, warning := range Warnings(row) {
		if warning == row["Name"] {
			t.Fatal("warning retained the source value")
		}
	}
}

func TestWarningsAcceptSeparatedKindsAndPersianJoiner(t *testing.T) {
	row := map[string]interface{}{
		"Name":   "کالای ABC ۱۲۳",
		"Sharh1": "نیمه‌هادی",
	}
	if got := Warnings(row); len(got) != 0 {
		t.Fatalf("valid text warnings = %#v", got)
	}
}

func TestWarningsUseCanonicalDescriptionFieldID(t *testing.T) {
	want := []string{"naming_leading_space:description"}
	if got := Warnings(map[string]interface{}{"Sharh2": " description"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("description warnings = %#v, want %#v", got, want)
	}
}

func TestAttachPreservesExistingWarningsAndSummarizesRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"Name": "Bad  spacing", "warnings": []string{"price_missing"}},
		{"name": "Valid name", "warnings": []interface{}{"existing"}},
	}
	if got := Attach(rows); got.Rows != 1 || got.Violations != 1 {
		t.Fatalf("attach summary = %+v", got)
	}
	want := []string{"naming_multiple_spaces:name", "price_missing"}
	if got := rows[0]["warnings"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged warnings = %#v, want %#v", got, want)
	}
	if got := Summarize(rows); got.Rows != 1 || got.Violations != 1 {
		t.Fatalf("summarized warnings = %+v", got)
	}
}

func TestWarningsIgnoreCodesSerialsAndNonStringValues(t *testing.T) {
	row := map[string]interface{}{
		"Code":        "ABC123",
		"Serial":      "ABC123",
		"Name":        42,
		"description": nil,
	}
	if got := Warnings(row); len(got) != 0 {
		t.Fatalf("non-naming fields produced warnings: %#v", got)
	}
}
