package appconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const SchemaVersion = 1

type Config struct {
	SchemaVersion int                    `json:"schema_version"`
	Server        ServerConfig           `json:"server"`
	Database      DatabaseConfig         `json:"database"`
	UI            UIConfig               `json:"ui"`
	ColumnLabels  map[string]string      `json:"column_labels"`
	Extra         map[string]interface{} `json:"extra,omitempty"`
}

type ServerConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Watch    bool   `json:"watch"`
	Debounce string `json:"debounce"`
}

type DatabaseConfig struct {
	Path         string `json:"path"`
	Charmap      string `json:"charmap"`
	DirectAccess bool   `json:"direct_access"`
}

type UIConfig struct {
	Theme                   string `json:"theme"`
	AutoScrollToChanged     bool   `json:"auto_scroll_to_changed"`
	HighlightChanges        bool   `json:"highlight_changes"`
	EnablePagination        bool   `json:"enable_pagination"`
	PageSize                int    `json:"page_size"`
	PlayNotificationSound   bool   `json:"play_notification_sound"`
	NotificationSoundSource string `json:"notification_sound_source"`
}

type Manager struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Server: ServerConfig{
			Host:     "127.0.0.1",
			Port:     8080,
			Watch:    true,
			Debounce: "0s",
		},
		Database: DatabaseConfig{
			DirectAccess: false,
		},
		UI: UIConfig{
			Theme:                   "system",
			HighlightChanges:        true,
			PageSize:                100,
			NotificationSoundSource: "external",
		},
		ColumnLabels: map[string]string{
			"Code":  "Code",
			"Name":  "Name",
			"ANBAR": "Warehouse",
		},
	}
}

func DefaultPath() string {
	for _, key := range []string{"PATRIS_CONFIG", "PATRIS_CONFIG_FILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	if runtime.GOOS == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, "Patris Export", "config.json")
		}
	}
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "patris-export", "config.json")
	}
	return "patris-export.json"
}

func Load(path string) (*Manager, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	m := &Manager{path: path, cfg: Default()}
	if err := m.Reload(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := m.Save(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	fn(&m.cfg)
	normalize(&m.cfg)
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(m.path, append(data, '\n'), 0644)
}

func (m *Manager) Save() error {
	return m.Update(func(*Config) {})
}

func (m *Manager) Replace(cfg Config) error {
	return m.Update(func(current *Config) {
		*current = cfg
	})
}

func (m *Manager) Reload() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("load config %s: %w", m.path, err)
	}
	normalize(&cfg)
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

func (m *Manager) Watch(onChange func(Config)) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil, err
	}
	go func() {
		var timer *time.Timer
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) != filepath.Clean(m.path) {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(120*time.Millisecond, func() {
					if err := m.Reload(); err == nil && onChange != nil {
						onChange(m.Get())
					}
				})
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return w, nil
}

func (c Config) Addr() string {
	host := strings.TrimSpace(c.Server.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	port := c.Server.Port
	if port <= 0 {
		port = 8080
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func ApplyEnv(cfg *Config) {
	if value := os.Getenv("PATRIS_HOST"); strings.TrimSpace(value) != "" {
		cfg.Server.Host = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_PORT"); strings.TrimSpace(value) != "" {
		if port, err := strconv.Atoi(value); err == nil {
			cfg.Server.Port = port
		}
	}
	if value := os.Getenv("PATRIS_ADDR"); strings.TrimSpace(value) != "" {
		setAddr(cfg, value)
	}
	if value := os.Getenv("PATRIS_DB_PATH"); strings.TrimSpace(value) != "" {
		cfg.Database.Path = value
	}
	if value := os.Getenv("PATRIS_CHARMAP"); strings.TrimSpace(value) != "" {
		cfg.Database.Charmap = value
	}
	if value := os.Getenv("PATRIS_DEBOUNCE"); strings.TrimSpace(value) != "" {
		cfg.Server.Debounce = value
	}
	if value := os.Getenv("PATRIS_WATCH"); strings.TrimSpace(value) != "" {
		cfg.Server.Watch = parseBool(value, cfg.Server.Watch)
	}
	if value := os.Getenv("PATRIS_DIRECT_ACCESS"); strings.TrimSpace(value) != "" {
		cfg.Database.DirectAccess = parseBool(value, cfg.Database.DirectAccess)
	}
	normalize(cfg)
}

func ApplyAddr(cfg *Config, addr string) {
	setAddr(cfg, addr)
	normalize(cfg)
}

func normalize(cfg *Config) {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Debounce == "" {
		cfg.Server.Debounce = "0s"
	}
	if cfg.UI.PageSize <= 0 {
		cfg.UI.PageSize = 100
	}
	if cfg.UI.Theme == "" {
		cfg.UI.Theme = "system"
	}
	if cfg.UI.NotificationSoundSource == "" {
		cfg.UI.NotificationSoundSource = "external"
	}
	if cfg.ColumnLabels == nil {
		cfg.ColumnLabels = map[string]string{}
	}
}

func setAddr(cfg *Config, addr string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	host, portText, ok := strings.Cut(addr, ":")
	if !ok {
		return
	}
	if strings.HasPrefix(addr, ":") {
		host = "0.0.0.0"
		portText = strings.TrimPrefix(addr, ":")
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		host = "0.0.0.0"
	}
	if port, err := strconv.Atoi(portText); err == nil {
		cfg.Server.Host = host
		cfg.Server.Port = port
	}
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
