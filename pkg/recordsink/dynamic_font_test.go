package recordsink

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestReadDynamicWorkbookFontPolicyUsesNamedCells(t *testing.T) {
	path := filepath.Join(t.TempDir(), "font-policy.xlsx")
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", "Settings"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Settings", "B1", "Yekan Bakh"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Settings", "B2", "Segoe UI"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetDefinedName(&excelize.DefinedName{Name: "PersianFont", RefersTo: "Settings!$B$1"}); err != nil {
		t.Fatal(err)
	}
	if err := book.SetDefinedName(&excelize.DefinedName{Name: "LatinFont", RefersTo: "Settings!$B$2"}); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	policy, err := ReadDynamicWorkbookFontPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if policy.PersianFont != "Yekan Bakh" || policy.LatinFont != "Segoe UI" {
		t.Fatalf("named font policy = %+v", policy)
	}
}

func TestAuditDrawingXMLRequiresExplicitPersianFontSlots(t *testing.T) {
	valid := `<xdr:wsDr xmlns:xdr="urn:xdr" xmlns:a="urn:a"><xdr:sp><xdr:txBody><a:p><a:r><a:rPr lang="fa-IR"><a:latin typeface="Yekan Bakh"/><a:ea typeface="Yekan Bakh"/><a:cs typeface="Yekan Bakh"/></a:rPr><a:t>همگام سازی</a:t></a:r></a:p></xdr:txBody></xdr:sp></xdr:wsDr>`
	runs, slots, err := auditDrawingXML(strings.NewReader(valid), "xl/drawings/drawing1.xml", "Yekan Bakh")
	if err != nil || runs != 1 || slots != 3 {
		t.Fatalf("valid DrawingML audit runs=%d slots=%d error=%v", runs, slots, err)
	}

	for _, test := range []struct {
		name string
		xml  string
		want string
	}{
		{name: "missing complex slot", xml: strings.Replace(valid, `<a:cs typeface="Yekan Bakh"/>`, "", 1), want: `cs=""`},
		{name: "forbidden family", xml: strings.Replace(valid, `<a:ea typeface="Yekan Bakh"/>`, `<a:ea typeface="Arial"/>`, 1), want: `ea="Arial"`},
		{name: "missing language", xml: strings.Replace(valid, ` lang="fa-IR"`, ``, 1), want: `language ""`},
		{name: "wrong language", xml: strings.Replace(valid, `lang="fa-IR"`, `lang="en-US"`, 1), want: `language "en-US"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := auditDrawingXML(strings.NewReader(test.xml), "xl/drawings/drawing1.xml", "Yekan Bakh")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("audit error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestForbiddenWorkbookFontsAreNarrowAndCaseInsensitive(t *testing.T) {
	for _, value := range []string{"Aptos", "calibri", " ARIAL "} {
		if !isForbiddenWorkbookFont(value) {
			t.Fatalf("forbidden font %q was accepted", value)
		}
	}
	for _, value := range []string{"Yekan Bakh", "Segoe UI", "Arial Unicode MS"} {
		if isForbiddenWorkbookFont(value) {
			t.Fatalf("allowed font %q was rejected", value)
		}
	}
}
