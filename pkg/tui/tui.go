package tui

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

const refreshInterval = 3 * time.Second

var (
	appTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)
	subtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	tabStyle      = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("245"))
	activeTab     = tabStyle.Copy().Bold(true).Foreground(lipgloss.Color("86")).Underline(true)
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2)
	cardStyle     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	badStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	accentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	keyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("238")).Padding(0, 1)
)

type tabID int

const (
	tabDashboard tabID = iota
	tabData
	tabConfig
	tabCharmap
	tabProcesses
	tabTools
	tabAbout
)

type tabDef struct {
	ID    tabID
	Label string
}

var tabs = []tabDef{
	{tabDashboard, "Dashboard"},
	{tabData, "Data"},
	{tabConfig, "Config"},
	{tabCharmap, "Charmap"},
	{tabProcesses, "Processes"},
	{tabTools, "Tools"},
	{tabAbout, "About"},
}

type model struct {
	cfg     appconfig.Config
	path    string
	version version.Info
	tab     int
	width   int
	height  int
	scroll  int
	state   dashboardState
	message string
}

type dashboardState struct {
	SourcePath        string
	SourceName        string
	SourceExists      bool
	SourceRemote      bool
	SourceSize        int64
	SourceModified    time.Time
	RecordCount       int
	FieldCount        int
	Fields            []string
	GroupRows         int
	SubgroupRows      int
	ItemRows          int
	PositiveStock     int
	ZeroStock         int
	PatrisProcesses   []processmon.ProcessInfo
	FileAccess        *processmon.FileAccessInfo
	FileAccessErr     error
	LoadErr           error
	LastRefresh       time.Time
	CharmapCount      int
	CharmapIssues     int
	CharmapSource     string
	WebURL            string
	ViewerURL         string
	WebSocketURL      string
	SourceFileURL     string
	SourceManifestURL string
}

type refreshMsg dashboardState

type tickMsg time.Time

func Run(cfg appconfig.Config, configPath string, build version.Info) error {
	_, err := tea.NewProgram(newModel(cfg, configPath, build), tea.WithAltScreen()).Run()
	return err
}

func newModel(cfg appconfig.Config, configPath string, build version.Info) model {
	return model{
		cfg:     cfg,
		path:    configPath,
		version: build,
		state:   collectState(cfg),
	}
}

func Preview(cfg appconfig.Config, configPath string, build version.Info, width, height int) string {
	m := newModel(cfg, configPath, build)
	m.width = width
	m.height = height
	return m.View()
}

func (m model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m.cfg), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case refreshMsg:
		m.state = dashboardState(msg)
		m.message = "Refreshed " + m.state.LastRefresh.Format("15:04:05")
	case tickMsg:
		return m, tea.Batch(refreshCmd(m.cfg), tickCmd())
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "right", "l":
			m.tab = (m.tab + 1) % len(tabs)
			m.scroll = 0
		case "shift+tab", "left", "h":
			m.tab = (m.tab + len(tabs) - 1) % len(tabs)
			m.scroll = 0
		case "1", "2", "3", "4", "5", "6", "7":
			index, _ := strconv.Atoi(msg.String())
			if index >= 1 && index <= len(tabs) {
				m.tab = index - 1
				m.scroll = 0
			}
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll -= max(4, m.contentHeight()/2)
			if m.scroll < 0 {
				m.scroll = 0
			}
		case "pgdown":
			m.scroll += max(4, m.contentHeight()/2)
		case "home":
			m.scroll = 0
		case "r":
			m.message = "Refreshing..."
			return m, refreshCmd(m.cfg)
		case "w":
			m.message = "Launching WebSocat watcher..."
			return m, launchWebSocat(m.cfg)
		case "o":
			m.message = "Opening web viewer..."
			return m, openURL(m.state.ViewerURL)
		}
	}
	return m, nil
}

func (m model) View() string {
	width := m.safeWidth()
	renderedTabs := make([]string, len(tabs))
	for i, t := range tabs {
		label := fmt.Sprintf("%d %s", i+1, t.Label)
		if i == m.tab {
			renderedTabs[i] = activeTab.Render(label)
		} else {
			renderedTabs[i] = tabStyle.Render(label)
		}
	}

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		appTitleStyle.Render("Patris Export"),
		" ",
		subtitleStyle.Render("Terminal operations dashboard"),
	)
	status := m.statusLine()
	body := m.activeBody()
	footer := mutedStyle.Render("tab/←/→ navigate  1-7 jump  ↑/↓ scroll  r refresh  o viewer  w WebSocat  q quit")
	if m.message != "" {
		footer += "\n" + mutedStyle.Render(m.message)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		status,
		strings.Join(renderedTabs, " "),
		panelStyle.Width(width).Render(body),
		footer,
	)
}

func (m model) safeWidth() int {
	if m.width <= 0 {
		return 110
	}
	return max(68, m.width-6)
}

func (m model) contentHeight() int {
	if m.height <= 0 {
		return 20
	}
	return max(8, m.height-10)
}

func (m model) activeBody() string {
	switch tabs[m.tab].ID {
	case tabDashboard:
		return m.dashboard()
	case tabData:
		return m.data()
	case tabConfig:
		return m.config()
	case tabCharmap:
		return m.charmap()
	case tabProcesses:
		return m.processes()
	case tabTools:
		return m.tools()
	default:
		return m.about()
	}
}

func (m model) statusLine() string {
	source := mutedStyle.Render("source unknown")
	if m.state.SourceName != "" {
		source = accentStyle.Render(m.state.SourceName)
	}
	records := fmt.Sprintf("%d rows", m.state.RecordCount)
	if m.state.LoadErr != nil {
		records = badStyle.Render("read error")
	} else {
		records = okStyle.Render(records)
	}
	patris := okStyle.Render("Patris81 idle")
	if len(m.state.PatrisProcesses) > 0 {
		patris = warnStyle.Render(fmt.Sprintf("Patris81 running: %d", len(m.state.PatrisProcesses)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		cardStyle.Render("File "+source),
		" ",
		cardStyle.Render("Data "+records),
		" ",
		cardStyle.Render(patris),
		" ",
		cardStyle.Render("Updated "+m.state.LastRefresh.Format("15:04:05")),
	)
}

func (m model) dashboard() string {
	state := m.state
	left := []string{
		accentStyle.Render("Source"),
		kv("Path", empty(state.SourcePath, "(not set)")),
		kv("Exists", boolText(state.SourceExists)),
		kv("Remote", boolText(state.SourceRemote)),
		kv("Size", humanBytes(state.SourceSize)),
		kv("Modified", timeText(state.SourceModified)),
	}
	if state.LoadErr != nil {
		left = append(left, badStyle.Render("Read error: "+state.LoadErr.Error()))
	}

	right := []string{
		accentStyle.Render("Live Data"),
		kv("Records", fmt.Sprintf("%d", state.RecordCount)),
		kv("Fields", fmt.Sprintf("%d", state.FieldCount)),
		kv("Groups", fmt.Sprintf("%d", state.GroupRows)),
		kv("Subgroups", fmt.Sprintf("%d", state.SubgroupRows)),
		kv("Items", fmt.Sprintf("%d", state.ItemRows)),
		kv("Positive stock", fmt.Sprintf("%d", state.PositiveStock)),
		kv("Zero stock", fmt.Sprintf("%d", state.ZeroStock)),
	}

	api := []string{
		accentStyle.Render("API Shortcuts"),
		kv("Web UI", state.WebURL),
		kv("Viewer", state.ViewerURL),
		kv("WebSocket", state.WebSocketURL),
		kv("Source manifest", state.SourceManifestURL),
		kv("Source file", state.SourceFileURL),
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top,
			cardStyle.Width(52).Render(strings.Join(left, "\n")),
			" ",
			cardStyle.Width(38).Render(strings.Join(right, "\n")),
		),
		"",
		cardStyle.Width(min(m.safeWidth()-6, 94)).Render(strings.Join(api, "\n")),
	)
}

func (m model) data() string {
	if m.state.LoadErr != nil {
		return badStyle.Render("Could not read records: " + m.state.LoadErr.Error())
	}
	fields := m.state.Fields
	if len(fields) == 0 {
		return mutedStyle.Render("No fields detected yet.")
	}
	limit := clampWindow(len(fields), m.scroll, max(6, m.contentHeight()-8))
	rows := make([][]string, 0, limit.Count)
	for _, field := range fields[limit.Start:limit.End] {
		rows = append(rows, []string{field, classifyField(field), fieldHint(field)})
	}
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("Field", "Type", "Use").
		Rows(rows...)
	return fmt.Sprintf("Detected %d fields from %d transformed records.\n%s\n\n%s",
		m.state.FieldCount,
		m.state.RecordCount,
		m.scrollHint(limit.Start, limit.End, len(fields)),
		t.String(),
	)
}

func (m model) config() string {
	rows := [][]string{
		{"Config file", empty(m.path, "(not set)")},
		{"Database path", empty(m.cfg.Database.Path, "(not set)")},
		{"Charmap", empty(m.cfg.Database.Charmap, "embedded default")},
		{"Bind address", m.cfg.Addr()},
		{"HTTP", boolText(m.cfg.Server.HTTP)},
		{"IPC", boolText(m.cfg.Server.IPC.Enabled)},
		{"Watch", boolText(m.cfg.Server.Watch)},
		{"Debounce", m.cfg.Server.Debounce},
		{"Direct access", boolText(m.cfg.Database.DirectAccess)},
		{"RTL conversion", boolText(m.cfg.Database.RTLConversion)},
		{"Temp policy", m.cfg.Runtime.TempStrategy + " / " + m.cfg.Runtime.TempDir},
		{"Theme", m.cfg.UI.Theme},
		{"Page size", fmt.Sprintf("%d", m.cfg.UI.PageSize)},
	}
	return renderKVTable("Configuration", rows)
}

func (m model) charmap() string {
	mapping := converter.DefaultCharMapping()
	source := "embedded Patris81 default"
	var issues []converter.CharMappingIssue
	if strings.TrimSpace(m.cfg.Database.Charmap) != "" {
		file, err := os.Open(m.cfg.Database.Charmap)
		if err != nil {
			return warnStyle.Render("Could not open charmap: " + err.Error())
		}
		defer file.Close()
		parsed, parseIssues, err := converter.ParseCharMappingReport(file)
		if err != nil {
			return warnStyle.Render("Could not parse charmap: " + err.Error())
		}
		mapping = parsed
		issues = parseIssues
		source = m.cfg.Database.Charmap
	}

	entries := converter.CharMappingEntries(mapping)
	limit := clampWindow(len(entries), m.scroll, max(8, m.contentHeight()-8))
	rows := make([][]string, 0, limit.Count)
	for _, entry := range entries[limit.Start:limit.End] {
		rows = append(rows, []string{
			"0x" + entry.Hex,
			fmt.Sprintf("%d", entry.Decimal),
			entry.Character,
			strings.Join(entry.Codepoints, " "),
		})
	}
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("Hex", "Dec", "Char", "Codepoint").
		Rows(rows...)

	summary := fmt.Sprintf("Source: %s\nEntries: %d", source, len(entries))
	if len(issues) > 0 {
		summary += fmt.Sprintf("\nIgnored lines: %d", len(issues))
	}
	return summary + "\n" + m.scrollHint(limit.Start, limit.End, len(entries)) + "\n\n" + t.String()
}

func (m model) processes() string {
	lines := []string{accentStyle.Render("Process Monitor")}
	if len(m.state.PatrisProcesses) == 0 {
		lines = append(lines, "Patris81.exe: "+okStyle.Render("not running"))
	} else {
		lines = append(lines, "Patris81.exe: "+warnStyle.Render(fmt.Sprintf("running (%d)", len(m.state.PatrisProcesses))))
		for _, p := range m.state.PatrisProcesses {
			lines = append(lines, fmt.Sprintf("  PID %-7d %s", p.PID, empty(p.Exe, p.Name)))
		}
	}
	lines = append(lines, "")
	if m.state.FileAccessErr != nil {
		lines = append(lines, warnStyle.Render("File lock inspection failed: "+m.state.FileAccessErr.Error()))
	} else if m.state.FileAccess == nil {
		lines = append(lines, "Database lock: "+mutedStyle.Render("not inspected"))
	} else if len(m.state.FileAccess.Processes) == 0 {
		lines = append(lines, "Database lock: "+okStyle.Render("no external holders detected"))
	} else {
		lines = append(lines, "Database lock: "+warnStyle.Render(fmt.Sprintf("%d process(es)", len(m.state.FileAccess.Processes))))
		for _, p := range m.state.FileAccess.Processes {
			lines = append(lines, fmt.Sprintf("  PID %-7d %-22s %s", p.PID, p.Name, p.Exe))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) tools() string {
	rows := [][]string{
		{"Open viewer", keyStyle.Render("o"), m.state.ViewerURL},
		{"Launch WebSocat watcher", keyStyle.Render("w"), m.state.WebSocketURL},
		{"Refresh dashboard", keyStyle.Render("r"), "Reload source/process/charmap state"},
		{"Raw source manifest", "", m.state.SourceManifestURL},
		{"Raw source file", "", m.state.SourceFileURL},
		{"Executable manifest", "", m.state.WebURL + "/api/update/manifest"},
		{"Executable file", "", m.state.WebURL + "/api/update/executable"},
	}
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("Action", "Key", "Target").
		Rows(rows...)
	return t.String() + "\n\n" + mutedStyle.Render("Tip: the WebSocat helper is ideal for inspecting live update payloads from a terminal.")
}

func (m model) about() string {
	lines := []string{
		appTitleStyle.Render("Patris Export"),
		"Modern Paradox/BDE export service for Patris81.",
		"",
		kv("Version", m.version.Version),
		kv("Commit", m.version.Commit),
		kv("Build date", m.version.BuildDate),
		kv("Go", m.version.GoVersion),
		kv("Platform", m.version.Platform),
		"",
		accentStyle.Render("TUI capabilities"),
		"- Live refresh with Bubble Tea ticks",
		"- Styled dashboard, tables, and status cards with Lip Gloss",
		"- Source, config, charmap, process, file-lock, API, and WebSocat views",
		"- Keyboard-first navigation for service operators",
	}
	return strings.Join(lines, "\n")
}

func collectState(cfg appconfig.Config) dashboardState {
	dbPath := strings.TrimSpace(cfg.Database.Path)
	state := dashboardState{
		SourcePath:        dbPath,
		SourceName:        sourceName(dbPath),
		SourceRemote:      filecopy.IsURL(dbPath),
		LastRefresh:       time.Now(),
		WebURL:            webURL(cfg, ""),
		ViewerURL:         webURL(cfg, "/viewer"),
		WebSocketURL:      wsURL(cfg),
		SourceManifestURL: webURL(cfg, "/api/source/manifest"),
		SourceFileURL:     webURL(cfg, "/api/source/file"),
		CharmapSource:     "embedded default",
	}

	if dbPath != "" && !state.SourceRemote {
		if info, err := os.Stat(dbPath); err == nil {
			state.SourceExists = true
			state.SourceSize = info.Size()
			state.SourceModified = info.ModTime()
		}
	}

	if dbPath != "" {
		ds, err := datasource.NewDataSource(dbPath, nil, !cfg.Database.DirectAccess)
		if err != nil {
			state.LoadErr = err
		} else {
			records, err := ds.GetRecords()
			_ = ds.Close()
			if err != nil {
				state.LoadErr = err
			} else {
				state.RecordCount = len(records)
				state.Fields = fieldsFromRecords(records)
				state.FieldCount = len(state.Fields)
				state.GroupRows, state.SubgroupRows, state.ItemRows, state.PositiveStock, state.ZeroStock = analyzeRecords(records)
			}
		}
	}

	if procs, err := processmon.FindProcessByName("patris81.exe"); err == nil {
		state.PatrisProcesses = procs
	}
	if dbPath != "" && !state.SourceRemote {
		info, err := processmon.FindProcessesWithFile(dbPath)
		state.FileAccess = info
		state.FileAccessErr = err
	}

	mapping := converter.DefaultCharMapping()
	state.CharmapCount = len(mapping)
	if strings.TrimSpace(cfg.Database.Charmap) != "" {
		state.CharmapSource = cfg.Database.Charmap
		if file, err := os.Open(cfg.Database.Charmap); err == nil {
			if parsed, issues, err := converter.ParseCharMappingReport(file); err == nil {
				state.CharmapCount = len(parsed)
				state.CharmapIssues = len(issues)
			}
			_ = file.Close()
		}
	}

	return state
}

func refreshCmd(cfg appconfig.Config) tea.Cmd {
	return func() tea.Msg {
		return refreshMsg(collectState(cfg))
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func launchWebSocat(cfg appconfig.Config) tea.Cmd {
	return func() tea.Msg {
		url := wsURL(cfg)
		if runtime.GOOS == "windows" {
			_ = exec.Command("cmd", "/c", "start", "Patris WebSocat", "powershell", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", "scripts\\windows\\Watch-WebSocat.ps1", "-Url", url).Start()
		} else {
			_ = exec.Command("sh", "-c", fmt.Sprintf("x-terminal-emulator -e './scripts/watch-websocket.sh %s' >/dev/null 2>&1 &", url)).Start()
		}
		return nil
	}
}

func openURL(target string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(target) == "" {
			return nil
		}
		switch runtime.GOOS {
		case "windows":
			_ = exec.Command("cmd", "/c", "start", "", target).Start()
		case "darwin":
			_ = exec.Command("open", target).Start()
		default:
			_ = exec.Command("xdg-open", target).Start()
		}
		return nil
	}
}

type windowRange struct {
	Start int
	End   int
	Count int
}

func clampWindow(total, start, count int) windowRange {
	if count < 1 {
		count = 1
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = max(0, total-count)
	}
	end := min(total, start+count)
	if end < start {
		end = start
	}
	return windowRange{Start: start, End: end, Count: end - start}
}

func (m model) scrollHint(start, end, total int) string {
	if total == 0 {
		return mutedStyle.Render("No rows")
	}
	return mutedStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start+1, end, total))
}

func renderKVTable(title string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("Setting", "Value").
		Rows(rows...)
	return accentStyle.Render(title) + "\n\n" + t.String()
}

func fieldsFromRecords(records []map[string]interface{}) []string {
	seen := map[string]bool{}
	for _, record := range records {
		for key := range record {
			seen[key] = true
		}
	}
	fields := make([]string, 0, len(seen))
	for key := range seen {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return fields
}

func analyzeRecords(records []map[string]interface{}) (groups, subgroups, items, positiveStock, zeroStock int) {
	for _, record := range records {
		code := fmt.Sprintf("%v", record["Code"])
		depth := codeDepth(code)
		switch depth {
		case 1:
			groups++
		case 2:
			subgroups++
		default:
			items++
		}
		stock := numericValue(record["ALLANBAR"])
		if stock > 0 {
			positiveStock++
		} else {
			zeroStock++
		}
	}
	return
}

func codeDepth(code string) int {
	digits := strings.Builder{}
	for _, r := range code {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	length := digits.Len()
	if length == 0 {
		return 0
	}
	depth := (length + 2) / 3
	if depth > 3 {
		return 3
	}
	return depth
}

func numericValue(value interface{}) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case float32:
		return float64(v)
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n
	default:
		return 0
	}
}

func classifyField(field string) string {
	name := strings.ToUpper(field)
	switch {
	case name == "CODE":
		return "hierarchy"
	case strings.HasPrefix(name, "ANBAR") || name == "ALLANBAR":
		return "stock"
	case strings.Contains(name, "DATE"):
		return "date"
	case strings.Contains(name, "FOROSH") || strings.Contains(name, "KHARYD"):
		return "price"
	default:
		return "text/number"
	}
}

func fieldHint(field string) string {
	name := strings.ToUpper(field)
	switch {
	case name == "CODE":
		return "group/subgroup/item key"
	case strings.HasPrefix(name, "ANBAR"):
		return "warehouse quantity"
	case name == "ALLANBAR":
		return "total stock"
	case name == "NAME":
		return "display name"
	default:
		return "source column"
	}
}

func webURL(cfg appconfig.Config, path string) string {
	host := cfg.Server.Host
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	port := cfg.Server.Port
	if port <= 0 {
		port = 8080
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: path}).String()
}

func wsURL(cfg appconfig.Config) string {
	u, _ := url.Parse(webURL(cfg, "/ws"))
	u.Scheme = "ws"
	return u.String()
}

func sourceName(path string) string {
	if path == "" {
		return ""
	}
	if filecopy.IsURL(path) {
		if u, err := url.Parse(path); err == nil {
			return filepath.Base(u.Path)
		}
	}
	return filepath.Base(path)
}

func kv(key, value string) string {
	return mutedStyle.Render(key+": ") + value
}

func boolText(value bool) string {
	if value {
		return okStyle.Render("yes")
	}
	return mutedStyle.Render("no")
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return mutedStyle.Render("unknown")
	}
	return value.Format("2006-01-02 15:04:05")
}

func humanBytes(bytes int64) string {
	if bytes <= 0 {
		return mutedStyle.Render("unknown")
	}
	units := []string{"B", "KiB", "MiB", "GiB"}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", bytes, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func empty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
