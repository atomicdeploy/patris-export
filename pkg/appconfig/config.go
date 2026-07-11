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

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const SchemaVersion = 1

var configBaseNames = []string{
	"patris-export.json",
	"patris-export.yaml",
	"patris-export.yml",
	"patris-export.toml",
}

type Config struct {
	SchemaVersion int                    `json:"schema_version" yaml:"schema_version" toml:"schema_version"`
	Server        ServerConfig           `json:"server" yaml:"server" toml:"server"`
	Database      DatabaseConfig         `json:"database" yaml:"database" toml:"database"`
	Runtime       RuntimeConfig          `json:"runtime" yaml:"runtime" toml:"runtime"`
	Convert       ConvertConfig          `json:"convert" yaml:"convert" toml:"convert"`
	Transform     recordmap.Config       `json:"transform" yaml:"transform" toml:"transform"`
	Export        ExportConfig           `json:"export" yaml:"export" toml:"export"`
	SendUpdates   updateout.Config       `json:"send_updates" yaml:"send_updates" toml:"send_updates"`
	Edge          EdgeConfig             `json:"edge" yaml:"edge" toml:"edge"`
	Notifications NotificationsConfig    `json:"notifications" yaml:"notifications" toml:"notifications"`
	UI            UIConfig               `json:"ui" yaml:"ui" toml:"ui"`
	ColumnLabels  map[string]string      `json:"column_labels" yaml:"column_labels" toml:"column_labels"`
	Extra         map[string]interface{} `json:"extra,omitempty" yaml:"extra,omitempty" toml:"extra,omitempty"`
}

type ServerConfig struct {
	Host     string    `json:"host" yaml:"host" toml:"host"`
	Port     int       `json:"port" yaml:"port" toml:"port"`
	Watch    bool      `json:"watch" yaml:"watch" toml:"watch"`
	Debounce string    `json:"debounce" yaml:"debounce" toml:"debounce"`
	HTTP     bool      `json:"http" yaml:"http" toml:"http"`
	IPC      IPCConfig `json:"ipc" yaml:"ipc" toml:"ipc"`
}

type IPCConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Path    string `json:"path" yaml:"path" toml:"path"`
}

type DatabaseConfig struct {
	Path          string `json:"path" yaml:"path" toml:"path"`
	Charmap       string `json:"charmap" yaml:"charmap" toml:"charmap"`
	DirectAccess  bool   `json:"direct_access" yaml:"direct_access" toml:"direct_access"`
	RTLConversion bool   `json:"rtl_conversion" yaml:"rtl_conversion" toml:"rtl_conversion"`
	Raw           bool   `json:"raw" yaml:"raw" toml:"raw"`
}

type RuntimeConfig struct {
	TempDir           string `json:"temp_dir" yaml:"temp_dir" toml:"temp_dir"`
	TempStrategy      string `json:"temp_strategy" yaml:"temp_strategy" toml:"temp_strategy"`
	TempMemoryLimitMB int64  `json:"temp_memory_limit_mb" yaml:"temp_memory_limit_mb" toml:"temp_memory_limit_mb"`
	Debug             bool   `json:"debug" yaml:"debug" toml:"debug"`
}

type ConvertConfig struct {
	Output   string `json:"output" yaml:"output" toml:"output"`
	Format   string `json:"format" yaml:"format" toml:"format"`
	Watch    bool   `json:"watch" yaml:"watch" toml:"watch"`
	Debounce string `json:"debounce" yaml:"debounce" toml:"debounce"`
	Raw      bool   `json:"raw" yaml:"raw" toml:"raw"`
	Table    string `json:"table,omitempty" yaml:"table,omitempty" toml:"table,omitempty"`
}

type ExportConfig struct {
	SQLitePath  string `json:"sqlite_path,omitempty" yaml:"sqlite_path,omitempty" toml:"sqlite_path,omitempty"`
	SQLiteTable string `json:"sqlite_table,omitempty" yaml:"sqlite_table,omitempty" toml:"sqlite_table,omitempty"`
	MySQLDSN    string `json:"mysql_dsn,omitempty" yaml:"mysql_dsn,omitempty" toml:"mysql_dsn,omitempty"`
	MySQLTable  string `json:"mysql_table,omitempty" yaml:"mysql_table,omitempty" toml:"mysql_table,omitempty"`
	BatchSize   int    `json:"batch_size,omitempty" yaml:"batch_size,omitempty" toml:"batch_size,omitempty"`
}

type EdgeConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	TargetURL   string `json:"target_url" yaml:"target_url" toml:"target_url"`
	Token       string `json:"token,omitempty" yaml:"token,omitempty" toml:"token,omitempty"`
	SourceID    string `json:"source_id" yaml:"source_id" toml:"source_id"`
	Debounce    string `json:"debounce" yaml:"debounce" toml:"debounce"`
	MaxUploadMB int64  `json:"max_upload_mb" yaml:"max_upload_mb" toml:"max_upload_mb"`
	UploadDir   string `json:"upload_dir" yaml:"upload_dir" toml:"upload_dir"`
}

type NotificationsConfig struct {
	Enabled            bool `json:"enabled" yaml:"enabled" toml:"enabled"`
	Native             bool `json:"native" yaml:"native" toml:"native"`
	InApp              bool `json:"in_app" yaml:"in_app" toml:"in_app"`
	ClientConnected    bool `json:"client_connected" yaml:"client_connected" toml:"client_connected"`
	ClientDisconnected bool `json:"client_disconnected" yaml:"client_disconnected" toml:"client_disconnected"`
	FileUpdated        bool `json:"file_updated" yaml:"file_updated" toml:"file_updated"`
	RowUpdated         bool `json:"row_updated" yaml:"row_updated" toml:"row_updated"`
	IncludeRowValues   bool `json:"include_row_values" yaml:"include_row_values" toml:"include_row_values"`
	MaxRows            int  `json:"max_rows" yaml:"max_rows" toml:"max_rows"`
}

type UIConfig struct {
	Theme                   string `json:"theme" yaml:"theme" toml:"theme"`
	AutoScrollToChanged     bool   `json:"auto_scroll_to_changed" yaml:"auto_scroll_to_changed" toml:"auto_scroll_to_changed"`
	HighlightChanges        bool   `json:"highlight_changes" yaml:"highlight_changes" toml:"highlight_changes"`
	RTLTextDirection        bool   `json:"rtl_text_direction" yaml:"rtl_text_direction" toml:"rtl_text_direction"`
	EnablePagination        bool   `json:"enable_pagination" yaml:"enable_pagination" toml:"enable_pagination"`
	PageSize                int    `json:"page_size" yaml:"page_size" toml:"page_size"`
	PlayNotificationSound   bool   `json:"play_notification_sound" yaml:"play_notification_sound" toml:"play_notification_sound"`
	NotificationSoundSource string `json:"notification_sound_source" yaml:"notification_sound_source" toml:"notification_sound_source"`
}

type Manager struct {
	path  string
	paths []string
	mu    sync.RWMutex
	cfg   Config
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Server: ServerConfig{
			Host:     "127.0.0.1",
			Port:     8080,
			Watch:    true,
			Debounce: "0s",
			HTTP:     true,
		},
		Database: DatabaseConfig{
			DirectAccess: false,
		},
		Runtime: RuntimeConfig{
			TempDir:           "system",
			TempStrategy:      "auto",
			TempMemoryLimitMB: 100,
		},
		Convert: ConvertConfig{
			Output:   ".",
			Format:   "json",
			Debounce: "1s",
		},
		SendUpdates: updateout.Config{
			Method:  "POST",
			Format:  "json",
			Mode:    "changes",
			Initial: true,
			Timeout: "10s",
		},
		Edge: EdgeConfig{
			Debounce:    "1s",
			MaxUploadMB: 512,
			UploadDir:   "edge-uploads",
		},
		Notifications: NotificationsConfig{
			Native:  true,
			InApp:   true,
			MaxRows: 3,
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

func ResolvePaths(paths []string) []string {
	explicit := cleanPathList(paths)
	if len(explicit) > 0 {
		return explicit
	}

	if value := strings.TrimSpace(os.Getenv("PATRIS_CONFIG_FILES")); value != "" {
		return cleanPathList(strings.Split(value, string(os.PathListSeparator)))
	}
	if value := strings.TrimSpace(os.Getenv("PATRIS_CONFIG_PATHS")); value != "" {
		return cleanPathList(strings.Split(value, string(os.PathListSeparator)))
	}
	for _, key := range []string{"PATRIS_CONFIG", "PATRIS_CONFIG_FILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return cleanPathList([]string{value})
		}
	}

	discovered := []string{}
	addCandidateDir := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		for _, name := range configBaseNames {
			discovered = append(discovered, filepath.Join(dir, name))
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		addCandidateDir(cwd)
		addCandidateDir(filepath.Join(cwd, "config"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		addCandidateDir(filepath.Join(exeDir, "config"))
	}
	discovered = append(discovered, DefaultPath())
	return cleanPathList(discovered)
}

func ResolveTempDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "system") || strings.EqualFold(value, "default") {
		return ""
	}
	if filepath.IsAbs(value) {
		return value
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), value)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, value)
	}
	return value
}

func TempMemoryLimitBytes(limitMB int64) int64 {
	if limitMB <= 0 {
		limitMB = 100
	}
	return limitMB * 1024 * 1024
}

func cleanPathList(paths []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, path := range paths {
		for _, part := range splitPathEntry(path) {
			cleaned := filepath.Clean(os.ExpandEnv(strings.TrimSpace(part)))
			if cleaned == "." || cleaned == "" || seen[cleaned] {
				continue
			}
			seen[cleaned] = true
			out = append(out, cleaned)
		}
	}
	return out
}

func splitPathEntry(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.Contains(path, "\n") {
		return strings.Fields(path)
	}
	if strings.Contains(path, string(os.PathListSeparator)) {
		return strings.Split(path, string(os.PathListSeparator))
	}
	return []string{path}
}

func Load(path string) (*Manager, error) {
	return LoadFiles([]string{path})
}

func LoadFiles(paths []string) (*Manager, error) {
	resolved := ResolvePaths(paths)
	if len(resolved) == 0 {
		resolved = []string{DefaultPath()}
	}
	writePath := resolved[len(resolved)-1]
	m := &Manager{path: writePath, paths: resolved, cfg: Default()}

	cfg := Default()
	loaded := false
	lastLoadedPath := ""
	for _, p := range resolved {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := loadInto(p, &cfg); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		loaded = true
		lastLoadedPath = p
	}
	normalize(&cfg)
	if lastLoadedPath != "" {
		writePath = lastLoadedPath
		m.path = writePath
	}
	m.cfg = cfg
	if !loaded {
		if err := m.Save(); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(writePath); errors.Is(err, os.ErrNotExist) {
		if err := m.Save(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) Paths() []string {
	out := make([]string, len(m.paths))
	copy(out, m.paths)
	return out
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
	data, err := encodeConfig(m.path, m.cfg)
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
	cfg := Default()
	loaded := false
	for _, p := range m.paths {
		if err := loadInto(p, &cfg); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		loaded = true
	}
	if !loaded {
		if err := loadInto(m.path, &cfg); err != nil {
			return err
		}
	}
	normalize(&cfg)
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

func loadInto(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decodeConfig(path, data, cfg); err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	normalize(cfg)
	return nil
}

func decodeConfig(path string, data []byte, cfg *Config) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, cfg)
	case ".toml":
		return toml.Unmarshal(data, cfg)
	default:
		return json.Unmarshal(data, cfg)
	}
}

func encodeConfig(path string, cfg Config) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		data, err = yaml.Marshal(cfg)
	case ".toml":
		data, err = toml.Marshal(cfg)
	default:
		data, err = json.MarshalIndent(cfg, "", "  ")
	}
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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
	if value := os.Getenv("PATRIS_HTTP"); strings.TrimSpace(value) != "" {
		cfg.Server.HTTP = parseBool(value, cfg.Server.HTTP)
	}
	if value := os.Getenv("PATRIS_IPC"); strings.TrimSpace(value) != "" {
		cfg.Server.IPC.Enabled = parseBool(value, cfg.Server.IPC.Enabled)
	}
	if value := os.Getenv("PATRIS_IPC_PATH"); strings.TrimSpace(value) != "" {
		cfg.Server.IPC.Path = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_DIRECT_ACCESS"); strings.TrimSpace(value) != "" {
		cfg.Database.DirectAccess = parseBool(value, cfg.Database.DirectAccess)
	}
	if value := os.Getenv("PATRIS_RTL"); strings.TrimSpace(value) != "" {
		cfg.Database.RTLConversion = parseBool(value, cfg.Database.RTLConversion)
	}
	if value := os.Getenv("PATRIS_RAW"); strings.TrimSpace(value) != "" {
		cfg.Database.Raw = parseBool(value, cfg.Database.Raw)
		cfg.Convert.Raw = cfg.Database.Raw
	}
	if value := os.Getenv("PATRIS_MAPPING_FILE"); strings.TrimSpace(value) != "" {
		cfg.Transform.MappingFile = strings.TrimSpace(value)
		cfg.Transform.Enabled = true
	}
	for _, key := range []string{"PATRIS_TEMP_DIR", "PATRIS_TMPDIR"} {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			cfg.Runtime.TempDir = strings.TrimSpace(value)
			break
		}
	}
	if value := os.Getenv("PATRIS_TEMP_STRATEGY"); strings.TrimSpace(value) != "" {
		cfg.Runtime.TempStrategy = strings.TrimSpace(value)
	}
	for _, key := range []string{"PATRIS_TEMP_MEMORY_LIMIT_MB", "PATRIS_TMPFS_LIMIT_MB"} {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			if limit, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				cfg.Runtime.TempMemoryLimitMB = limit
			}
			break
		}
	}
	if value := os.Getenv("PATRIS_DEBUG"); strings.TrimSpace(value) != "" {
		cfg.Runtime.Debug = parseBool(value, cfg.Runtime.Debug)
	}
	if value := os.Getenv("PATRIS_OUTPUT"); strings.TrimSpace(value) != "" {
		cfg.Convert.Output = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_FORMAT"); strings.TrimSpace(value) != "" {
		cfg.Convert.Format = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EXPORT_TABLE"); strings.TrimSpace(value) != "" {
		cfg.Convert.Table = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SQLITE_PATH"); strings.TrimSpace(value) != "" {
		cfg.Export.SQLitePath = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SQLITE_TABLE"); strings.TrimSpace(value) != "" {
		cfg.Export.SQLiteTable = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_MYSQL_DSN"); strings.TrimSpace(value) != "" {
		cfg.Export.MySQLDSN = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_MYSQL_TABLE"); strings.TrimSpace(value) != "" {
		cfg.Export.MySQLTable = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EXPORT_BATCH_SIZE"); strings.TrimSpace(value) != "" {
		if batch, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.Export.BatchSize = batch
		}
	}
	if value := os.Getenv("PATRIS_CONVERT_WATCH"); strings.TrimSpace(value) != "" {
		cfg.Convert.Watch = parseBool(value, cfg.Convert.Watch)
	}
	if value := os.Getenv("PATRIS_CONVERT_DEBOUNCE"); strings.TrimSpace(value) != "" {
		cfg.Convert.Debounce = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SEND_UPDATES"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Enabled = parseBool(value, cfg.SendUpdates.Enabled)
	}
	if value := os.Getenv("PATRIS_SEND_URL"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.URL = strings.TrimSpace(value)
		cfg.SendUpdates.Enabled = true
	}
	if value := os.Getenv("PATRIS_SEND_METHOD"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Method = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SEND_FORMAT"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Format = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SEND_MODE"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Mode = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SEND_INITIAL"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Initial = parseBool(value, cfg.SendUpdates.Initial)
	}
	if value := os.Getenv("PATRIS_SEND_TIMEOUT"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Timeout = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_SEND_COMMAND"); strings.TrimSpace(value) != "" {
		cfg.SendUpdates.Command = strings.Fields(value)
		cfg.SendUpdates.Enabled = true
	}
	if value := os.Getenv("PATRIS_EDGE_ENABLED"); strings.TrimSpace(value) != "" {
		cfg.Edge.Enabled = parseBool(value, cfg.Edge.Enabled)
	}
	if value := os.Getenv("PATRIS_EDGE_TARGET_URL"); strings.TrimSpace(value) != "" {
		cfg.Edge.TargetURL = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EDGE_TOKEN"); strings.TrimSpace(value) != "" {
		cfg.Edge.Token = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EDGE_SOURCE_ID"); strings.TrimSpace(value) != "" {
		cfg.Edge.SourceID = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EDGE_DEBOUNCE"); strings.TrimSpace(value) != "" {
		cfg.Edge.Debounce = strings.TrimSpace(value)
	}
	if value := os.Getenv("PATRIS_EDGE_MAX_UPLOAD_MB"); strings.TrimSpace(value) != "" {
		if maxMB, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			cfg.Edge.MaxUploadMB = maxMB
		}
	}
	if value := os.Getenv("PATRIS_EDGE_UPLOAD_DIR"); strings.TrimSpace(value) != "" {
		cfg.Edge.UploadDir = strings.TrimSpace(value)
	}
	applyBoolEnv := func(key string, dst *bool) {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			*dst = parseBool(value, *dst)
		}
	}
	applyBoolEnv("PATRIS_NOTIFICATIONS", &cfg.Notifications.Enabled)
	applyBoolEnv("PATRIS_NOTIFY_NATIVE", &cfg.Notifications.Native)
	applyBoolEnv("PATRIS_NOTIFY_IN_APP", &cfg.Notifications.InApp)
	applyBoolEnv("PATRIS_NOTIFY_CLIENT_CONNECTED", &cfg.Notifications.ClientConnected)
	applyBoolEnv("PATRIS_NOTIFY_CLIENT_DISCONNECTED", &cfg.Notifications.ClientDisconnected)
	applyBoolEnv("PATRIS_NOTIFY_FILE_UPDATED", &cfg.Notifications.FileUpdated)
	applyBoolEnv("PATRIS_NOTIFY_ROW_UPDATED", &cfg.Notifications.RowUpdated)
	applyBoolEnv("PATRIS_NOTIFY_INCLUDE_ROW_VALUES", &cfg.Notifications.IncludeRowValues)
	if value := os.Getenv("PATRIS_NOTIFY_MAX_ROWS"); strings.TrimSpace(value) != "" {
		if maxRows, err := strconv.Atoi(value); err == nil {
			cfg.Notifications.MaxRows = maxRows
		}
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
	if !cfg.Server.HTTP && !cfg.Server.IPC.Enabled {
		cfg.Server.HTTP = true
	}
	if cfg.Runtime.TempDir == "" {
		cfg.Runtime.TempDir = "system"
	}
	cfg.Runtime.TempStrategy = strings.ToLower(strings.TrimSpace(cfg.Runtime.TempStrategy))
	switch cfg.Runtime.TempStrategy {
	case "", "auto":
		cfg.Runtime.TempStrategy = "auto"
	case "system", "disk", "tmp":
		cfg.Runtime.TempStrategy = "system"
	case "memory", "shm", "tmpfs", "ram":
		cfg.Runtime.TempStrategy = "memory"
	default:
		cfg.Runtime.TempStrategy = "auto"
	}
	if cfg.Runtime.TempMemoryLimitMB <= 0 {
		cfg.Runtime.TempMemoryLimitMB = 100
	}
	if cfg.Convert.Output == "" {
		cfg.Convert.Output = "."
	}
	if cfg.Convert.Format == "" {
		cfg.Convert.Format = "json"
	}
	cfg.Convert.Format = strings.ToLower(strings.TrimSpace(cfg.Convert.Format))
	switch cfg.Convert.Format {
	case "json", "csv", "xlsx", "excel", "sqlite", "mysql":
		if cfg.Convert.Format == "excel" {
			cfg.Convert.Format = "xlsx"
		}
	default:
		cfg.Convert.Format = "json"
	}
	if cfg.Convert.Debounce == "" {
		cfg.Convert.Debounce = "1s"
	}
	if cfg.Export.BatchSize <= 0 {
		cfg.Export.BatchSize = 500
	}
	cfg.SendUpdates = updateout.Normalize(cfg.SendUpdates)
	cfg.Edge.TargetURL = strings.TrimRight(strings.TrimSpace(cfg.Edge.TargetURL), "/")
	cfg.Edge.Token = strings.TrimSpace(cfg.Edge.Token)
	cfg.Edge.SourceID = strings.TrimSpace(cfg.Edge.SourceID)
	if cfg.Edge.Debounce == "" {
		cfg.Edge.Debounce = "1s"
	}
	if cfg.Edge.MaxUploadMB <= 0 {
		cfg.Edge.MaxUploadMB = 512
	}
	if cfg.Edge.UploadDir == "" {
		cfg.Edge.UploadDir = "edge-uploads"
	}
	if !cfg.Notifications.Native && !cfg.Notifications.InApp {
		cfg.Notifications.Native = true
		cfg.Notifications.InApp = true
	}
	if cfg.Notifications.MaxRows <= 0 {
		cfg.Notifications.MaxRows = 3
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
