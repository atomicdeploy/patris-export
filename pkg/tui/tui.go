package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Padding(0, 1)
	tabStyle   = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("245"))
	activeTab  = tabStyle.Copy().Bold(true).Foreground(lipgloss.Color("86")).Underline(true)
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2)
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

type model struct {
	cfg     appconfig.Config
	path    string
	version version.Info
	tab     int
	width   int
	height  int
}

func Run(cfg appconfig.Config, configPath string, build version.Info) error {
	_, err := tea.NewProgram(model{cfg: cfg, path: configPath, version: build}, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "right", "l":
			m.tab = (m.tab + 1) % 4
		case "shift+tab", "left", "h":
			m.tab = (m.tab + 3) % 4
		case "w":
			return m, launchWebSocat(m.cfg)
		}
	}
	return m, nil
}

func (m model) View() string {
	tabs := []string{"About", "Config", "Processes", "WebSocat"}
	rendered := make([]string, len(tabs))
	for i, t := range tabs {
		if i == m.tab {
			rendered[i] = activeTab.Render(t)
		} else {
			rendered[i] = tabStyle.Render(t)
		}
	}

	var body string
	switch m.tab {
	case 0:
		body = m.about()
	case 1:
		body = m.config()
	case 2:
		body = m.processes()
	default:
		body = m.websocat()
	}

	footer := mutedStyle.Render("Tab/←/→ navigate  w launch websocat  q quit")
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Patris Export"),
		strings.Join(rendered, " "),
		boxStyle.Width(max(60, m.width-6)).Render(body),
		footer,
	)
}

func (m model) about() string {
	return fmt.Sprintf("%s\n\nVersion: %s\nCommit: %s\nBuild: %s\nGo: %s\nPlatform: %s",
		"Modern Paradox/BDE export service for Patris81.",
		m.version.Version,
		m.version.Commit,
		m.version.BuildDate,
		m.version.GoVersion,
		m.version.Platform,
	)
}

func (m model) config() string {
	return fmt.Sprintf("Config: %s\nDatabase: %s\nBind: %s\nWatch: %v\nDebounce: %s\nTheme: %s\nPage size: %d",
		m.path,
		empty(m.cfg.Database.Path, "(not set)"),
		m.cfg.Addr(),
		m.cfg.Server.Watch,
		m.cfg.Server.Debounce,
		m.cfg.UI.Theme,
		m.cfg.UI.PageSize,
	)
}

func (m model) processes() string {
	procs, err := processmon.FindProcessByName("patris81.exe")
	if err != nil {
		return warnStyle.Render("Could not inspect Patris81.exe: " + err.Error())
	}
	status := okStyle.Render("not running")
	if len(procs) > 0 {
		status = warnStyle.Render(fmt.Sprintf("running (%d)", len(procs)))
	}
	lines := []string{"Patris81.exe: " + status}
	for _, p := range procs {
		lines = append(lines, fmt.Sprintf("PID %d  %s", p.PID, p.Exe))
	}
	if m.cfg.Database.Path != "" {
		fileInfo, err := processmon.FindProcessesWithFile(m.cfg.Database.Path)
		if err == nil {
			lines = append(lines, "", fmt.Sprintf("Database lock: %v (%d process(es))", len(fileInfo.Processes) > 0, len(fileInfo.Processes)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) websocat() string {
	url := "ws://" + m.cfg.Addr() + "/ws"
	return fmt.Sprintf("Inspect live rows and events from a terminal:\n\n  scripts/windows/Watch-WebSocat.ps1 -Url %s -Once\n  scripts/watch-websocket.sh %s --once\n\nPress w to launch websocat in a new terminal when available.", url, url)
}

func launchWebSocat(cfg appconfig.Config) tea.Cmd {
	return func() tea.Msg {
		url := "ws://" + cfg.Addr() + "/ws"
		if runtime.GOOS == "windows" {
			_ = exec.Command("cmd", "/c", "start", "Patris WebSocat", "powershell", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", "scripts\\windows\\Watch-WebSocat.ps1", "-Url", url).Start()
		} else {
			_ = exec.Command("sh", "-c", fmt.Sprintf("x-terminal-emulator -e './scripts/watch-websocket.sh %s' >/dev/null 2>&1 &", url)).Start()
		}
		return nil
	}
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
