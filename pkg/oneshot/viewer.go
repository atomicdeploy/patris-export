package oneshot

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/version"
)

type Options struct {
	Path         string
	CharMap      converter.CharMapping
	UseTempFile  bool
	OutputPath   string
	Open         bool
	Title        string
	BuildVersion version.Info
}

type Result struct {
	HTMLPath string
	Opened   bool
	Records  int
	Fields   int
}

type snapshot struct {
	Title       string       `json:"title"`
	Source      string       `json:"source"`
	GeneratedAt string       `json:"generated_at"`
	Version     version.Info `json:"version"`
	RecordCount int          `json:"record_count"`
	Fields      []string     `json:"fields"`
	Records     template.JS  `json:"records"`
}

func Run(options Options) (Result, error) {
	if strings.TrimSpace(options.Path) == "" {
		return Result{}, fmt.Errorf("database path is required")
	}
	if strings.TrimSpace(options.Title) == "" {
		options.Title = "Patris Export Snapshot"
	}
	if options.BuildVersion.Version == "" {
		options.BuildVersion = version.Current()
	}

	ds, err := datasource.NewDataSource(options.Path, options.CharMap, options.UseTempFile)
	if err != nil {
		return Result{}, err
	}
	defer ds.Close()

	records, err := ds.GetRecords()
	if err != nil {
		return Result{}, err
	}
	fields := collectFields(records)
	htmlPath, err := resolveOutputPath(options.OutputPath, options.Path)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(htmlPath), 0755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}
	if err := WriteHTML(htmlPath, snapshot{
		Title:       options.Title,
		Source:      options.Path,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Version:     options.BuildVersion,
		RecordCount: len(records),
		Fields:      fields,
		Records:     mustMarshalRecords(records),
	}); err != nil {
		return Result{}, err
	}

	result := Result{HTMLPath: htmlPath, Records: len(records), Fields: len(fields)}
	if options.Open {
		if err := OpenHTML(htmlPath, options.Title); err != nil {
			return result, err
		}
		result.Opened = true
	}
	return result, nil
}

func WriteHTML(path string, data snapshot) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot HTML: %w", err)
	}
	defer file.Close()
	return snapshotTemplate.Execute(file, data)
}

func OpenHTML(path, title string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fileURL := fileURL(abs)
	switch runtime.GOOS {
	case "windows":
		if exe := firstCommand(
			"msedge.exe",
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			"chrome.exe",
		); exe != "" {
			return exec.Command(exe, "--new-window", "--app="+fileURL).Start()
		}
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", fileURL).Start()
	case "linux":
		for _, candidate := range []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if exe, err := exec.LookPath(candidate); err == nil {
				return exec.Command(exe, "--new-window", "--app="+fileURL).Start()
			}
		}
		if exe, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command(exe, fileURL).Start()
		}
	default:
		if exe, err := exec.LookPath("open"); err == nil {
			return exec.Command(exe, fileURL).Start()
		}
	}
	return fmt.Errorf("no native viewer launcher found for %s", runtime.GOOS)
}

func resolveOutputPath(outputPath, source string) (string, error) {
	if strings.TrimSpace(outputPath) != "" {
		if filepath.Ext(outputPath) == "" {
			return filepath.Join(outputPath, defaultSnapshotName(source)), nil
		}
		return filepath.Clean(outputPath), nil
	}
	dir, err := os.MkdirTemp("", "patris-view-*")
	if err != nil {
		return "", fmt.Errorf("create temp snapshot directory: %w", err)
	}
	return filepath.Join(dir, defaultSnapshotName(source)), nil
}

func defaultSnapshotName(source string) string {
	base := "patris-export"
	if filecopy.IsURL(source) {
		if parsed, err := url.Parse(source); err == nil {
			base = filepath.Base(parsed.Path)
			if base == "." || base == "/" || base == "" {
				base = parsed.Host
			}
		}
	} else if strings.TrimSpace(source) != "" {
		base = filepath.Base(source)
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		base = "patris-export"
	}
	return base + "-viewer.html"
}

func collectFields(records []map[string]interface{}) []string {
	seen := map[string]bool{}
	fields := []string{}
	preferred := []string{"Code", "Name", "Serial", "Vahed", "FOROSH", "KHARYD", "ALLANBAR", "ANBAR"}
	for _, field := range preferred {
		for _, record := range records {
			if _, ok := record[field]; ok && !seen[field] {
				seen[field] = true
				fields = append(fields, field)
			}
		}
	}
	other := []string{}
	for _, record := range records {
		for field := range record {
			if !seen[field] {
				seen[field] = true
				other = append(other, field)
			}
		}
	}
	sort.Strings(other)
	return append(fields, other...)
}

func mustMarshalRecords(records []map[string]interface{}) template.JS {
	data, err := json.Marshal(records)
	if err != nil {
		return template.JS("[]")
	}
	return template.JS(data)
}

func firstCommand(candidates ...string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			continue
		}
		if exe, err := exec.LookPath(candidate); err == nil {
			return exe
		}
	}
	return ""
}

func fileURL(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

var snapshotTemplate = template.Must(template.New("snapshot").Funcs(template.FuncMap{
	"json": func(value interface{}) template.JS {
		data, _ := json.Marshal(value)
		return template.JS(data)
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
  color-scheme: dark light;
  --bg: #0d1117;
  --panel: #111827;
  --panel-2: #172033;
  --text: #f8fafc;
  --muted: #9ca3af;
  --border: rgba(148,163,184,.22);
  --accent: #22d3ee;
  --accent-2: #8b5cf6;
  --good: #34d399;
  --warn: #fbbf24;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #f3f6fb;
    --panel: #ffffff;
    --panel-2: #eef4ff;
    --text: #111827;
    --muted: #5b6678;
    --border: rgba(60,72,90,.18);
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  min-height: 100vh;
  background:
    radial-gradient(circle at top left, rgba(34,211,238,.18), transparent 32rem),
    radial-gradient(circle at bottom right, rgba(139,92,246,.14), transparent 34rem),
    var(--bg);
  color: var(--text);
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
.app { width: min(1440px, calc(100% - 28px)); margin: 0 auto; padding: 22px 0 34px; }
.topbar {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-start;
  margin-bottom: 18px;
}
h1 { margin: 0; font-size: clamp(1.5rem, 2.6vw, 2.4rem); letter-spacing: 0; }
.subtitle { margin-top: 6px; color: var(--muted); font-size: .95rem; overflow-wrap: anywhere; }
.meta { display: flex; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.pill {
  display: inline-flex;
  padding: 7px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: rgba(17,24,39,.58);
  color: var(--text);
  font: 700 .82rem ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
}
.toolbar {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) 190px 190px;
  gap: 10px;
  margin-bottom: 14px;
}
input, select {
  width: 100%;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel);
  color: var(--text);
  padding: 11px 12px;
  outline: none;
  font: 500 .94rem ui-sans-serif, system-ui;
}
.panel {
  background: color-mix(in srgb, var(--panel) 92%, transparent);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 18px 60px rgba(0,0,0,.30);
}
.table-wrap { max-height: calc(100vh - 190px); overflow: auto; }
table { width: 100%; border-collapse: collapse; min-width: 980px; }
th, td {
  border-bottom: 1px solid var(--border);
  padding: 10px 12px;
  text-align: left;
  vertical-align: top;
}
th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--panel-2);
  color: var(--muted);
  font-size: .78rem;
  text-transform: uppercase;
  letter-spacing: .08em;
}
td {
  font: 500 .9rem ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
}
tr:hover td { background: rgba(34,211,238,.07); }
.code { color: var(--accent); font-weight: 800; }
.stock-zero { color: var(--warn); }
.stock-ok { color: var(--good); }
.empty { padding: 42px; color: var(--muted); text-align: center; }
.footer { margin-top: 12px; color: var(--muted); font-size: .82rem; }
@media (max-width: 760px) {
  .topbar { flex-direction: column; }
  .meta { justify-content: flex-start; }
  .toolbar { grid-template-columns: 1fr; }
  .table-wrap { max-height: none; }
}
</style>
</head>
<body>
<main class="app">
  <header class="topbar">
    <div>
      <h1>{{.Title}}</h1>
      <div class="subtitle">{{.Source}}</div>
    </div>
    <div class="meta">
      <span class="pill" id="recordCount">{{.RecordCount}} rows</span>
      <span class="pill">{{len .Fields}} columns</span>
      <span class="pill">{{.Version.Version}} {{.Version.Commit}}</span>
    </div>
  </header>
  <section class="toolbar">
    <input id="search" type="search" placeholder="Search rows...">
    <select id="fieldFilter"><option value="">All columns</option></select>
    <select id="stockFilter">
      <option value="">All stock states</option>
      <option value="in">ALLANBAR > 0</option>
      <option value="out">ALLANBAR = 0</option>
    </select>
  </section>
  <section class="panel">
    <div class="table-wrap">
      <table>
        <thead><tr id="head"></tr></thead>
        <tbody id="body"><tr><td class="empty">Loading snapshot...</td></tr></tbody>
      </table>
    </div>
  </section>
  <div class="footer">Generated {{.GeneratedAt}}. This is a local one-shot snapshot; it does not modify the Patris database.</div>
</main>
<script id="records" type="application/json">{{.Records}}</script>
<script id="fields" type="application/json">{{json .Fields}}</script>
<script>
const records = JSON.parse(document.getElementById('records').textContent);
const fields = JSON.parse(document.getElementById('fields').textContent);
const head = document.getElementById('head');
const body = document.getElementById('body');
const search = document.getElementById('search');
const fieldFilter = document.getElementById('fieldFilter');
const stockFilter = document.getElementById('stockFilter');
const recordCount = document.getElementById('recordCount');
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]));
}
function valueText(value) {
  if (Array.isArray(value)) return value.join(', ');
  if (value && typeof value === 'object') return JSON.stringify(value);
  return value ?? '';
}
function codeKind(code) {
  const text = String(code ?? '');
  if (text.length <= 3) return 'group';
  if (text.length <= 6) return 'subgroup';
  return 'item';
}
function renderHead() {
  head.innerHTML = fields.map(field => '<th>' + escapeHtml(field) + '</th>').join('');
  fieldFilter.innerHTML += fields.map(field => '<option value="' + escapeHtml(field) + '">' + escapeHtml(field) + '</option>').join('');
}
function rowMatches(record, query, field) {
  if (!query) return true;
  if (field) return valueText(record[field]).toLowerCase().includes(query);
  return fields.some(name => valueText(record[name]).toLowerCase().includes(query));
}
function stockMatches(record, mode) {
  if (!mode) return true;
  const value = Number(record.ALLANBAR ?? 0);
  return mode === 'in' ? value > 0 : value === 0;
}
function render() {
  const query = search.value.trim().toLowerCase();
  const field = fieldFilter.value;
  const stock = stockFilter.value;
  const rows = records
    .filter(record => rowMatches(record, query, field) && stockMatches(record, stock))
    .sort((a, b) => collator.compare(String(a.Code ?? ''), String(b.Code ?? '')));
  recordCount.textContent = rows.length + ' / ' + records.length + ' rows';
  if (!rows.length) {
    body.innerHTML = '<tr><td colspan="' + fields.length + '" class="empty">No rows match the current filters.</td></tr>';
    return;
  }
  body.innerHTML = rows.map(record => {
    const kind = codeKind(record.Code);
    return '<tr data-kind="' + escapeHtml(kind) + '">' + fields.map(field => {
      const value = valueText(record[field]);
      const cls = field === 'Code' ? 'code' : field === 'ALLANBAR' ? (Number(value) > 0 ? 'stock-ok' : 'stock-zero') : '';
      const badge = field === 'Code' ? ' <small>(' + escapeHtml(kind) + ')</small>' : '';
      return '<td class="' + escapeHtml(cls) + '">' + escapeHtml(value) + badge + '</td>';
    }).join('') + '</tr>';
  }).join('');
}
renderHead();
render();
search.addEventListener('input', render);
fieldFilter.addEventListener('change', render);
stockFilter.addEventListener('change', render);
</script>
</body>
</html>`))
