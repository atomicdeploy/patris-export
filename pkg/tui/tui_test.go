package tui

import (
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/version"
)

func TestAnalyzeRecordsClassifiesCodeHierarchyAndStock(t *testing.T) {
	records := []map[string]interface{}{
		{"Code": "100", "ALLANBAR": 0},
		{"Code": "100200", "ALLANBAR": "12.5"},
		{"Code": "100200300", "ALLANBAR": 7},
		{"Code": "100200300400", "ALLANBAR": -1},
	}

	groups, subgroups, items, positiveStock, zeroStock := analyzeRecords(records)

	if groups != 1 || subgroups != 1 || items != 2 {
		t.Fatalf("unexpected hierarchy counts: groups=%d subgroups=%d items=%d", groups, subgroups, items)
	}
	if positiveStock != 2 || zeroStock != 2 {
		t.Fatalf("unexpected stock counts: positive=%d zero=%d", positiveStock, zeroStock)
	}
}

func TestFieldsFromRecordsReturnsSortedUnion(t *testing.T) {
	fields := fieldsFromRecords([]map[string]interface{}{
		{"Name": "main", "Code": "100"},
		{"ALLANBAR": 4, "Name": "child"},
	})

	got := strings.Join(fields, ",")
	want := "ALLANBAR,Code,Name"
	if got != want {
		t.Fatalf("fieldsFromRecords() = %q, want %q", got, want)
	}
}

func TestWebURLNormalizesWildcardAndIPv6Hosts(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 18080
	if got, want := webURL(cfg, "/viewer"), "http://127.0.0.1:18080/viewer"; got != want {
		t.Fatalf("webURL wildcard = %q, want %q", got, want)
	}

	cfg.Server.Host = "::1"
	if got, want := webURL(cfg, "/ws"), "http://[::1]:18080/ws"; got != want {
		t.Fatalf("webURL IPv6 = %q, want %q", got, want)
	}
}

func TestPreviewRendersOperationalDashboard(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Server.Port = 18080
	cfg.Database.Path = ""
	out := Preview(cfg, "test-config.json", version.Info{
		Version:   "test",
		Commit:    "abc123",
		BuildDate: "2026-07-11T00:00:00Z",
		GoVersion: "go-test",
		Platform:  "windows/amd64",
	}, 120, 32)

	for _, want := range []string{
		"Patris Export",
		"Terminal operations dashboard",
		"Dashboard",
		"Source",
		"Live Data",
		"API Shortcuts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output missing %q:\n%s", want, out)
		}
	}
}
