package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/notifier"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/version"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/atomicdeploy/patris-export/web"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Server represents the HTTP/WebSocket server
type Server struct {
	router             *mux.Router
	dbPath             string
	charMap            converter.CharMapping
	dataSource         datasource.DataSource
	watcher            *watcher.FileWatcher
	wsClients          map[*websocket.Conn]*sync.Mutex
	wsClientsMu        sync.RWMutex
	upgrader           websocket.Upgrader
	lastRecords        []map[string]interface{}
	lastRecordsMu      sync.RWMutex
	lastModTime        time.Time
	lastModTimeMu      sync.RWMutex
	useTempFile        bool
	config             *appconfig.Manager
	configWatcher      *fsnotify.Watcher
	version            version.Info
	processMu          sync.Mutex
	processStatusCache map[string]interface{}
	processStatusAt    time.Time
	lastSourceHash     string
	lastSourceHashMu   sync.Mutex
	eventSubscribers   map[chan map[string]interface{}]struct{}
	eventSubscribersMu sync.RWMutex
}

type Options struct {
	Config  *appconfig.Manager
	Version version.Info
}

// RecordChange represents a change to a specific record
type RecordChange struct {
	Code          string                 `json:"code"`
	ChangeType    string                 `json:"change_type"` // "added", "deleted", "modified"
	OldValues     map[string]interface{} `json:"old_values,omitempty"`
	NewValues     map[string]interface{} `json:"new_values,omitempty"`
	ChangedFields []string               `json:"changed_fields,omitempty"`
}

// ChangeSet represents incremental changes to the database
type ChangeSet struct {
	Type       string                   `json:"type"`
	Timestamp  string                   `json:"timestamp"`
	Added      []map[string]interface{} `json:"added,omitempty"`
	Deleted    []string                 `json:"deleted,omitempty"`
	Modified   []RecordChange           `json:"modified,omitempty"`
	TotalCount int                      `json:"total_count"`
}

// ToastRequest represents a desktop/browser notification request.
type ToastRequest struct {
	Type      string `json:"type,omitempty"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Icon      string `json:"icon,omitempty"`
	Source    string `json:"source,omitempty"`
	Native    *bool  `json:"native,omitempty"`
	Broadcast *bool  `json:"broadcast,omitempty"`
}

type charmapPreviewRequest struct {
	Content string `json:"content"`
	Path    string `json:"path"`
}

// NewServer creates a new server instance
func NewServer(dbPath string, charMap converter.CharMapping, useTempFile ...bool) (*Server, error) {
	return NewServerWithOptions(dbPath, charMap, Options{}, useTempFile...)
}

func NewServerWithOptions(dbPath string, charMap converter.CharMapping, options Options, useTempFile ...bool) (*Server, error) {
	copyBeforeRead := true
	if len(useTempFile) > 0 {
		copyBeforeRead = useTempFile[0]
	}
	if filecopy.IsURL(dbPath) {
		copyBeforeRead = true
	}

	// Create data source (supports both .db and .json files)
	ds, err := datasource.NewDataSource(dbPath, charMap, copyBeforeRead)
	if err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}

	s := &Server{
		router:           mux.NewRouter(),
		dbPath:           dbPath,
		charMap:          charMap,
		dataSource:       ds,
		wsClients:        make(map[*websocket.Conn]*sync.Mutex),
		eventSubscribers: make(map[chan map[string]interface{}]struct{}),
		useTempFile:      copyBeforeRead,
		config:           options.Config,
		version:          options.Version,
		upgrader: websocket.Upgrader{
			// Security: Configure origin checking for production use
			// Default allows localhost only
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				// Allow empty origin (direct connections, testing)
				if origin == "" {
					return true
				}
				// Allow localhost for development
				if origin == "http://localhost:8080" || origin == "http://127.0.0.1:8080" {
					return true
				}
				// For production: Add your domain(s) here and remove the default true below
				// Example: return origin == "https://yourdomain.com"
				// Currently allowing all origins for initial deployment - CHANGE THIS IN PRODUCTION!
				log.Printf("⚠️  WebSocket connection from origin: %s (origin check bypassed - configure for production!)", origin)
				return true
			},
		},
	}
	if s.version.Version == "" {
		s.version = version.Current()
	}

	// Set up routes
	s.setupRoutes()

	if s.config != nil {
		converter.SetRTLConversion(s.config.Get().Database.RTLConversion)
		w, err := s.config.Watch(func(cfg appconfig.Config) {
			log.Printf("⚙️ Config reloaded: %s", s.config.Path())
			converter.SetRTLConversion(cfg.Database.RTLConversion)
			s.broadcastConfig(cfg)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to watch config: %w", err)
		}
		s.configWatcher = w
	}

	return s, nil
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	s.router.HandleFunc("/", s.handleWelcome).Methods("GET")
	s.router.HandleFunc("/viewer", s.handleViewer).Methods("GET")
	s.router.HandleFunc("/debug/charmap", s.handleCharmapViewer).Methods("GET")
	s.router.HandleFunc("/api/records", s.handleGetRecords).Methods("GET")
	s.router.HandleFunc("/api/info", s.handleGetInfo).Methods("GET")
	s.router.HandleFunc("/api/app", s.handleGetApp).Methods("GET")
	s.router.HandleFunc("/api/charmap", s.handleGetCharmap).Methods("GET")
	s.router.HandleFunc("/api/charmap/preview", s.handlePostCharmapPreview).Methods("POST")
	s.router.HandleFunc("/api/config", s.handleGetConfig).Methods("GET")
	s.router.HandleFunc("/api/config", s.handlePutConfig).Methods("PUT")
	s.router.HandleFunc("/api/status", s.handleGetStatus).Methods("GET")
	s.router.HandleFunc("/api/toast", s.handlePostToast).Methods("POST")
	s.router.HandleFunc("/api/processes/patris81", s.handleGetPatris81Processes).Methods("GET")
	s.router.HandleFunc("/api/processes/file", s.handleGetFileProcesses).Methods("GET")
	s.router.HandleFunc("/static/notification.ogg", s.handleNotificationAudio).Methods("GET", "HEAD")
	s.router.HandleFunc("/static/patris-api-icon.png", s.handleAppIcon).Methods("GET", "HEAD")
	s.router.HandleFunc("/favicon.ico", s.handleFavicon).Methods("GET", "HEAD")
	s.router.HandleFunc("/ws", s.handleWebSocket)
}

// Router returns the HTTP router used by the standalone server. Embedded hosts
// can mount it in their own HTTP stack instead of opening a patris-export port.
func (s *Server) Router() http.Handler {
	return s.router
}

// Records returns the current records using the same transformed shape served
// by GET /api/records.
func (s *Server) Records() (map[string]interface{}, error) {
	records, err := s.dataSource.GetRecords()
	if err != nil {
		return nil, fmt.Errorf("failed to read records: %w", err)
	}
	exporter := converter.NewExporter(nil)
	return exporter.TransformRecordsMap(records), nil
}

// Info returns database schema and metadata using the same shape served by
// GET /api/info.
func (s *Server) Info() (map[string]interface{}, error) {
	db, cleanup, err := s.openDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer cleanup()

	fields, err := db.GetFields()
	if err != nil {
		return nil, fmt.Errorf("failed to get fields: %w", err)
	}

	return map[string]interface{}{
		"success":     true,
		"file":        sourceBaseName(s.dbPath),
		"path":        s.dbPath,
		"version":     s.version,
		"num_records": db.GetNumRecords(),
		"num_fields":  db.GetNumFields(),
		"fields":      fields,
	}, nil
}

// AppMetadata returns version/resource/config metadata for embedded and IPC
// clients.
func (s *Server) AppMetadata() map[string]interface{} {
	return s.appMetadata()
}

// Config returns the active configuration snapshot.
func (s *Server) Config() appconfig.Config {
	if s.config == nil {
		return appconfig.Default()
	}
	return s.config.Get()
}

// ReplaceConfig persists and broadcasts a new configuration snapshot.
func (s *Server) ReplaceConfig(cfg appconfig.Config) (appconfig.Config, error) {
	if s.config == nil {
		return appconfig.Default(), fmt.Errorf("configuration storage is not enabled")
	}
	if err := s.config.Replace(cfg); err != nil {
		return appconfig.Config{}, err
	}
	cfg = s.config.Get()
	s.broadcastConfig(cfg)
	return cfg, nil
}

// Status returns process and database lock status using the same shape served
// by GET /api/status.
func (s *Server) Status() map[string]interface{} {
	return s.processStatus()
}

// ShowToast displays a native notification and/or broadcasts it to connected
// browser, IPC, and embedded subscribers.
func (s *Server) ShowToast(req ToastRequest) error {
	return s.processToastRequest(req)
}

// Refresh forces a data refresh broadcast to browser, IPC, and embedded
// subscribers.
func (s *Server) Refresh() {
	s.broadcastUpdate()
}

// SubscribeEvents subscribes to the same event stream used by the WebSocket
// broadcaster. The returned unsubscribe function must be called by the host.
func (s *Server) SubscribeEvents(buffer int) (<-chan map[string]interface{}, func()) {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan map[string]interface{}, buffer)
	s.eventSubscribersMu.Lock()
	s.eventSubscribers[ch] = struct{}{}
	s.eventSubscribersMu.Unlock()
	unsubscribe := func() {
		s.eventSubscribersMu.Lock()
		if _, ok := s.eventSubscribers[ch]; ok {
			delete(s.eventSubscribers, ch)
			close(ch)
		}
		s.eventSubscribersMu.Unlock()
	}
	return ch, unsubscribe
}

func (s *Server) openDatabase() (*paradox.Database, func(), error) {
	pathToOpen := s.dbPath
	cleanup := func() {}
	if filecopy.IsURL(s.dbPath) {
		tempFileInfo, err := filecopy.DownloadToTemp(s.dbPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	} else if s.useTempFile && strings.EqualFold(filepath.Ext(s.dbPath), ".db") {
		tempFileInfo, err := filecopy.CopyToTemp(s.dbPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to copy database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	}

	db, err := paradox.Open(pathToOpen)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	return db, func() {
		db.Close()
		cleanup()
	}, nil
}

// handleWelcome serves the welcome page
func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(web.WelcomeHTML)
}

// handleViewer serves the SPA visualizer
func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(web.ViewerHTML)
}

func (s *Server) handleCharmapViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(web.CharmapHTML)
}

// handleGetRecords returns all database records as JSON
func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	transformed, err := s.Records()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(transformed); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode JSON: %v", err), http.StatusInternalServerError)
	}
}

// handleGetInfo returns database schema information
func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.Info()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.appMetadata())
}

func (s *Server) handleGetCharmap(w http.ResponseWriter, r *http.Request) {
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	if source == "" {
		source = "active"
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path != "" {
		if !s.debugEnabled() {
			http.Error(w, "custom charmap path preview requires debug mode", http.StatusForbidden)
			return
		}
		s.writeCharmapFile(w, path, "path")
		return
	}
	if source == "default" {
		writeJSON(w, s.charmapPayload("default", "", converter.DefaultCharMapping(), nil))
		return
	}
	if s.config != nil {
		cfg := s.config.Get()
		if strings.TrimSpace(cfg.Database.Charmap) != "" {
			s.writeCharmapFile(w, cfg.Database.Charmap, "active")
			return
		}
	}
	writeJSON(w, s.charmapPayload("embedded", "", converter.DefaultCharMapping(), nil))
}

func (s *Server) handlePostCharmapPreview(w http.ResponseWriter, r *http.Request) {
	if !s.debugEnabled() {
		http.Error(w, "custom charmap preview requires debug mode", http.StatusForbidden)
		return
	}
	var req charmapPreviewRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode charmap preview request: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) != "" {
		mapping, issues, err := converter.ParseCharMappingReport(strings.NewReader(req.Content))
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse charmap content: %v", err), http.StatusBadRequest)
			return
		}
		writeJSON(w, s.charmapPayload("preview", "", mapping, issues))
		return
	}
	if strings.TrimSpace(req.Path) != "" {
		s.writeCharmapFile(w, req.Path, "preview")
		return
	}
	http.Error(w, "content or path is required", http.StatusBadRequest)
}

func (s *Server) writeCharmapFile(w http.ResponseWriter, path, source string) {
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open charmap file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read charmap file: %v", err), http.StatusBadRequest)
		return
	}
	if len(data) > 2*1024*1024 {
		http.Error(w, "charmap file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	mapping, issues, err := converter.ParseCharMappingReport(bytes.NewReader(data))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse charmap file: %v", err), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.charmapPayload(source, path, mapping, issues))
}

func (s *Server) charmapPayload(source, path string, mapping converter.CharMapping, issues []converter.CharMappingIssue) map[string]interface{} {
	return map[string]interface{}{
		"success":       true,
		"debug_enabled": s.debugEnabled(),
		"source":        source,
		"path":          path,
		"count":         len(mapping),
		"entries":       converter.CharMappingEntries(mapping),
		"issues":        issues,
	}
}

func (s *Server) debugEnabled() bool {
	if value := strings.TrimSpace(os.Getenv("PATRIS_DEBUG")); value != "" {
		return strings.EqualFold(value, "1") || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes") || strings.EqualFold(value, "on")
	}
	return s.config != nil && s.config.Get().Runtime.Debug
}

func (s *Server) appMetadata() map[string]interface{} {
	payload := map[string]interface{}{
		"name":        "Patris Export",
		"version":     s.version,
		"resources":   web.Resources(),
		"config_path": "",
	}
	if s.config != nil {
		payload["config_path"] = s.config.Path()
		payload["config_paths"] = s.config.Paths()
	}
	return payload
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.config == nil {
		writeJSON(w, appconfig.Default())
		return
	}
	writeJSON(w, s.config.Get())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if s.config == nil {
		http.Error(w, "configuration storage is not enabled", http.StatusServiceUnavailable)
		return
	}
	var cfg appconfig.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode config: %v", err), http.StatusBadRequest)
		return
	}
	cfg, err := s.ReplaceConfig(cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"success": true,
		"config":  cfg,
	})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Status())
}

// handlePostToast displays a native notification and broadcasts it to web clients.
func (s *Server) handlePostToast(w http.ResponseWriter, r *http.Request) {
	var req ToastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode toast request: %v", err), http.StatusBadRequest)
		return
	}

	req.Source = "api"
	nativeErr := s.ShowToast(req)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      nativeErr == nil,
		"native_error": errorString(nativeErr),
	})
}

// handleGetPatris81Processes returns information about running patris81.exe processes.
func (s *Server) handleGetPatris81Processes(w http.ResponseWriter, r *http.Request) {
	processes, err := processmon.FindProcessByName("patris81.exe")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find processes: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"count":     len(processes),
		"processes": processes,
	})
}

// handleGetFileProcesses returns information about processes accessing the database file.
func (s *Server) handleGetFileProcesses(w http.ResponseWriter, r *http.Request) {
	if filecopy.IsURL(s.dbPath) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"file":      sourceBaseName(s.dbPath),
			"path":      s.dbPath,
			"remote":    true,
			"count":     0,
			"in_use":    false,
			"processes": []processmon.ProcessInfo{},
		})
		return
	}

	fileInfo, err := processmon.FindProcessesWithFile(s.dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find processes with file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"file":      sourceBaseName(s.dbPath),
		"path":      fileInfo.FilePath,
		"count":     len(fileInfo.Processes),
		"in_use":    len(fileInfo.Processes) > 0,
		"processes": fileInfo.Processes,
	})
}

// handleNotificationAudio serves the notification audio file with HEAD/range support.
func (s *Server) handleNotificationAudio(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/ogg")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "notification.ogg", time.Time{}, bytes.NewReader(web.NotificationAudio))
}

func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "patris-api-icon.png", time.Time{}, bytes.NewReader(web.AppIconPNG))
}

// handleFavicon serves the application icon.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "favicon.ico", time.Time{}, bytes.NewReader(web.FaviconICO))
}

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade to WebSocket: %v", err)
		return
	}

	connMu := &sync.Mutex{}
	s.wsClientsMu.Lock()
	s.wsClients[conn] = connMu
	s.wsClientsMu.Unlock()
	clientAddr := r.RemoteAddr

	log.Printf("🔌 New WebSocket connection (total: %d)", len(s.wsClients))

	s.notifyConfigured("client_connected", "Patris client connected", fmt.Sprintf("%s connected to the live viewer", clientAddr))

	// Send initial data
	s.sendRecordsToClient(conn, connMu)

	// Handle disconnection
	go func() {
		defer func() {
			s.wsClientsMu.Lock()
			delete(s.wsClients, conn)
			s.wsClientsMu.Unlock()
			conn.Close()
			s.notifyConfigured("client_disconnected", "Patris client disconnected", fmt.Sprintf("%s disconnected from the live viewer", clientAddr))
			log.Printf("🔌 WebSocket disconnected (remaining: %d)", len(s.wsClients))
		}()

		for {
			var msg ToastRequest
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			if msg.Type == "toast" {
				msg.Source = "websocket"
				if err := s.processToastRequest(msg); err != nil {
					log.Printf("Failed to show WebSocket toast: %v", err)
				}
				continue
			}
			if msg.Type == "refresh" {
				log.Printf("🔄 Refresh requested from WebSocket client")
				s.broadcastUpdate()
			}
		}
	}()
}

// sendRecordsToClient sends current database records to a WebSocket client
func (s *Server) sendRecordsToClient(conn *websocket.Conn, connMu *sync.Mutex) {
	records, err := s.dataSource.GetRecords()
	if err != nil {
		log.Printf("Failed to read records: %v", err)
		return
	}

	// Send as initial load (all records are "added")
	message := map[string]interface{}{
		"type":        "initial",
		"timestamp":   time.Now().Format(time.RFC3339),
		"added":       records,
		"total_count": len(records),
		"file_name":   sourceBaseName(s.dbPath),
		"file_path":   s.dbPath,
		"version":     s.version,
		"resources":   web.Resources(),
	}
	if s.config != nil {
		message["config"] = s.config.Get()
	}

	connMu.Lock()
	err = conn.WriteJSON(message)
	connMu.Unlock()

	if err != nil {
		log.Printf("Failed to send to WebSocket: %v", err)
		return
	}

	// Store current records for future change detection
	s.lastRecordsMu.Lock()
	s.lastRecords = records
	s.lastRecordsMu.Unlock()

	log.Printf("📤 Sent initial %d records to client", len(records))
	go s.broadcastProcessInfo()
}

// broadcastUpdate broadcasts database changes to all connected WebSocket clients
func (s *Server) broadcastUpdate() {
	s.wsClientsMu.RLock()
	clientCount := len(s.wsClients)
	s.wsClientsMu.RUnlock()

	if clientCount < 0 {
		log.Printf("⚠️  No clients connected, skipping broadcast")
		return
	}

	log.Printf("📡 Broadcasting update to %d clients", clientCount)

	// Get current records
	records, err := s.dataSource.GetRecords()
	if err != nil {
		log.Printf("Failed to read records: %v", err)
		return
	}

	// Compute changes
	s.lastRecordsMu.Lock()
	changes := s.computeChanges(records)
	s.lastRecords = records
	s.lastRecordsMu.Unlock()

	// Log what we're sending
	added := 0
	deleted := 0
	modified := 0
	if a, ok := changes["added"].([]map[string]interface{}); ok {
		added = len(a)
	}
	if d, ok := changes["deleted"].([]string); ok {
		deleted = len(d)
	}
	if m, ok := changes["modified"].([]RecordChange); ok {
		modified = len(m)
	}
	log.Printf("📊 Broadcasting: %d added, %d modified, %d deleted", added, modified, deleted)

	// Broadcast to all clients
	s.wsClientsMu.RLock()
	for conn, connMu := range s.wsClients {
		go func(c *websocket.Conn, mu *sync.Mutex) {
			mu.Lock()
			err := c.WriteJSON(changes)
			mu.Unlock()
			if err != nil {
				log.Printf("Failed to send to WebSocket: %v", err)
			}
		}(conn, connMu)
	}
	s.wsClientsMu.RUnlock()

	log.Printf("✅ Broadcast complete")
	if added+modified+deleted > 0 {
		s.notifyConfigured("row_updated", "Patris rows changed", s.rowChangeMessage(added, modified, deleted, changes))
	}
	go s.broadcastProcessInfo()
}

func (s *Server) processToastRequest(req ToastRequest) error {
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "Patris Export"
	}
	if strings.TrimSpace(req.Message) == "" {
		req.Message = "Notification"
	}

	if req.Broadcast == nil || *req.Broadcast {
		s.broadcastToast(req, "")
	}

	if req.Native == nil || *req.Native {
		err := notifier.Show(notifier.Toast{Title: req.Title, Message: req.Message}, web.AppIconPNG)
		if err != nil && (req.Broadcast == nil || *req.Broadcast) {
			s.broadcastToast(req, errorString(err))
		}
		return err
	}
	return nil
}

func (s *Server) notifyConfigured(event, title, message string) {
	if s.config == nil {
		return
	}
	cfg := s.config.Get().Notifications
	if !cfg.Enabled || !notificationEventEnabled(cfg, event) {
		return
	}

	req := ToastRequest{
		Type:    "toast",
		Title:   title,
		Message: message,
		Source:  event,
		Native:  &cfg.Native,
	}
	if cfg.InApp {
		req.Broadcast = boolPtr(true)
	} else {
		req.Broadcast = boolPtr(false)
	}
	if err := s.processToastRequest(req); err != nil {
		log.Printf("Configured notification failed: %v", err)
	}
}

func notificationEventEnabled(cfg appconfig.NotificationsConfig, event string) bool {
	switch event {
	case "client_connected":
		return cfg.ClientConnected
	case "client_disconnected":
		return cfg.ClientDisconnected
	case "file_updated":
		return cfg.FileUpdated
	case "row_updated":
		return cfg.RowUpdated
	default:
		return false
	}
}

func (s *Server) rowChangeMessage(added, modified, deleted int, changes map[string]interface{}) string {
	summary := fmt.Sprintf("%d added, %d modified, %d deleted", added, modified, deleted)
	if s.config == nil {
		return summary
	}
	cfg := s.config.Get().Notifications
	if !cfg.IncludeRowValues {
		return summary
	}
	maxRows := cfg.MaxRows
	if maxRows <= 0 {
		maxRows = 3
	}
	details := []string{}
	if rows, ok := changes["modified"].([]RecordChange); ok {
		for i, row := range rows {
			if i >= maxRows {
				details = append(details, fmt.Sprintf("and %d more modified", len(rows)-i))
				break
			}
			fieldDetails := []string{}
			for _, field := range row.ChangedFields {
				fieldDetails = append(fieldDetails, fmt.Sprintf("%s: %v -> %v", field, row.OldValues[field], row.NewValues[field]))
				if len(fieldDetails) >= 3 {
					break
				}
			}
			if len(row.ChangedFields) > len(fieldDetails) {
				fieldDetails = append(fieldDetails, fmt.Sprintf("%d more field(s)", len(row.ChangedFields)-len(fieldDetails)))
			}
			if len(fieldDetails) == 0 {
				fieldDetails = append(fieldDetails, "fields changed")
			}
			details = append(details, fmt.Sprintf("Code %s: %s", row.Code, strings.Join(fieldDetails, ", ")))
		}
	}
	if rows, ok := changes["added"].([]map[string]interface{}); ok && len(details) < maxRows {
		for i, row := range rows {
			if len(details) >= maxRows {
				break
			}
			details = append(details, fmt.Sprintf("Added Code %v", row["Code"]))
			if i == len(rows)-1 {
				break
			}
		}
	}
	if rows, ok := changes["deleted"].([]string); ok && len(details) < maxRows {
		for _, code := range rows {
			if len(details) >= maxRows {
				break
			}
			details = append(details, fmt.Sprintf("Deleted Code %s", code))
		}
	}
	if len(details) == 0 {
		return summary
	}
	return summary + " | " + strings.Join(details, "; ")
}

func boolPtr(value bool) *bool {
	return &value
}

func (s *Server) updateSourceHash() (oldHash, newHash string) {
	if filecopy.IsURL(s.dbPath) {
		return "", ""
	}
	hash, err := filecopy.CalculateHash(s.dbPath)
	if err != nil {
		log.Printf("Failed to calculate source hash: %v", err)
		return "", ""
	}
	s.lastSourceHashMu.Lock()
	defer s.lastSourceHashMu.Unlock()
	oldHash = s.lastSourceHash
	s.lastSourceHash = hash
	return oldHash, hash
}

func (s *Server) notifyFileUpdated(path string) {
	oldHash, newHash := s.updateSourceHash()
	message := fmt.Sprintf("%s changed", sourceBaseName(path))
	if oldHash != "" || newHash != "" {
		message = fmt.Sprintf("%s changed (%s -> %s)", sourceBaseName(path), shortHash(oldHash), shortHash(newHash))
	}
	s.notifyConfigured("file_updated", "Patris source file updated", message)
}

func shortHash(hash string) string {
	if hash == "" {
		return "unknown"
	}
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}

func (s *Server) broadcastToast(req ToastRequest, nativeError string) {
	message := map[string]interface{}{
		"type":      "toast",
		"timestamp": time.Now().Format(time.RFC3339),
		"title":     req.Title,
		"message":   req.Message,
		"source":    req.Source,
	}
	if req.Icon != "" {
		message["icon"] = req.Icon
	}
	if nativeError != "" {
		message["native_error"] = nativeError
	}

	s.wsClientsMu.RLock()
	defer s.wsClientsMu.RUnlock()
	for conn, connMu := range s.wsClients {
		go func(c *websocket.Conn, mu *sync.Mutex) {
			mu.Lock()
			err := c.WriteJSON(message)
			mu.Unlock()
			if err != nil {
				log.Printf("Failed to send toast to WebSocket: %v", err)
			}
		}(conn, connMu)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode JSON: %v", err), http.StatusInternalServerError)
	}
}

func (s *Server) processStatus() map[string]interface{} {
	patris81Processes, patrisErr := findPatrisProcessesWithTimeout(1500 * time.Millisecond)
	var fileInfo *processmon.FileAccessInfo
	var fileErr error
	if !filecopy.IsURL(s.dbPath) {
		fileInfo, fileErr = findFileProcessesWithTimeout(s.dbPath, 1500*time.Millisecond)
	}

	status := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"patris81": map[string]interface{}{
			"running":   len(patris81Processes) > 0,
			"count":     len(patris81Processes),
			"processes": patris81Processes,
		},
		"file_access": map[string]interface{}{
			"file":      sourceBaseName(s.dbPath),
			"path":      s.dbPath,
			"remote":    filecopy.IsURL(s.dbPath),
			"in_use":    fileInfo != nil && len(fileInfo.Processes) > 0,
			"count":     0,
			"processes": []processmon.ProcessInfo{},
		},
	}
	if patrisErr != nil {
		status["patris81"].(map[string]interface{})["error"] = patrisErr.Error()
	}
	if fileInfo != nil {
		status["file_access"].(map[string]interface{})["count"] = len(fileInfo.Processes)
		status["file_access"].(map[string]interface{})["processes"] = fileInfo.Processes
	}
	if fileErr != nil {
		status["file_access"].(map[string]interface{})["error"] = fileErr.Error()
	}
	return status
}

func findPatrisProcessesWithTimeout(timeout time.Duration) ([]processmon.ProcessInfo, error) {
	type result struct {
		processes []processmon.ProcessInfo
		err       error
	}
	ch := make(chan result, 1)
	go func() {
		processes, err := processmon.FindProcessByName("patris81.exe")
		ch <- result{processes: processes, err: err}
	}()
	select {
	case res := <-ch:
		return res.processes, res.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timed out inspecting patris81.exe processes")
	}
}

func findFileProcessesWithTimeout(path string, timeout time.Duration) (*processmon.FileAccessInfo, error) {
	type result struct {
		info *processmon.FileAccessInfo
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		info, err := processmon.FindProcessesWithFile(path)
		ch <- result{info: info, err: err}
	}()
	select {
	case res := <-ch:
		return res.info, res.err
	case <-time.After(timeout):
		return &processmon.FileAccessInfo{
			FilePath:  path,
			Processes: []processmon.ProcessInfo{},
		}, fmt.Errorf("timed out inspecting file access")
	}
}

func (s *Server) broadcastConfig(cfg appconfig.Config) {
	message := map[string]interface{}{
		"type":      "config_update",
		"timestamp": time.Now().Format(time.RFC3339),
		"config":    cfg,
	}
	s.broadcastMessage(message)
}

func (s *Server) broadcastMessage(message map[string]interface{}) {
	s.eventSubscribersMu.RLock()
	for ch := range s.eventSubscribers {
		select {
		case ch <- cloneMap(message):
		default:
		}
	}
	s.eventSubscribersMu.RUnlock()

	s.wsClientsMu.RLock()
	defer s.wsClientsMu.RUnlock()
	for conn, connMu := range s.wsClients {
		go func(c *websocket.Conn, mu *sync.Mutex) {
			mu.Lock()
			err := c.WriteJSON(message)
			mu.Unlock()
			if err != nil {
				log.Printf("Failed to send WebSocket message: %v", err)
			}
		}(conn, connMu)
	}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (s *Server) broadcastProcessInfo() {
	message := map[string]interface{}{
		"type":      "process_info",
		"timestamp": time.Now().Format(time.RFC3339),
		"status":    s.cachedProcessStatus(2 * time.Second),
	}
	s.broadcastMessage(message)
}

func (s *Server) cachedProcessStatus(ttl time.Duration) map[string]interface{} {
	s.processMu.Lock()
	defer s.processMu.Unlock()

	if s.processStatusCache != nil && time.Since(s.processStatusAt) < ttl {
		return s.processStatusCache
	}

	status := s.processStatus()
	s.processStatusCache = status
	s.processStatusAt = time.Now()
	return status
}

// computeChanges computes the difference between old and new records
func (s *Server) computeChanges(newRecords []map[string]interface{}) map[string]interface{} {
	changes := map[string]interface{}{
		"type":        "update",
		"timestamp":   time.Now().Format(time.RFC3339),
		"total_count": len(newRecords),
	}

	// If no previous records, all are new
	if len(s.lastRecords) == 0 {
		changes["added"] = newRecords
		log.Printf("🆕 First load: all %d records are new", len(newRecords))
		return changes
	}

	// Create maps by Code for efficient lookup
	oldMap := make(map[string]map[string]interface{})
	for _, record := range s.lastRecords {
		if code, ok := record["Code"]; ok {
			codeStr := fmt.Sprintf("%v", code)
			oldMap[codeStr] = record
		}
	}

	newMap := make(map[string]map[string]interface{})
	for _, record := range newRecords {
		if code, ok := record["Code"]; ok {
			codeStr := fmt.Sprintf("%v", code)
			newMap[codeStr] = record
		}
	}

	added := []map[string]interface{}{}
	deleted := []string{}
	modified := []RecordChange{}

	// Find added records
	for code, record := range newMap {
		if _, exists := oldMap[code]; !exists {
			added = append(added, record)
		}
	}

	// Find deleted records
	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			deleted = append(deleted, code)
		}
	}

	// Find modified records (records that exist in both but have different values)
	for code, newRecord := range newMap {
		if oldRecord, exists := oldMap[code]; exists {
			changedFields := []string{}
			oldValues := make(map[string]interface{})
			newValues := make(map[string]interface{})

			// Compare each field
			for key, newVal := range newRecord {
				if key == "Code" {
					continue // Skip the key field
				}
				oldVal, hasOldVal := oldRecord[key]

				// Check if values differ
				if !hasOldVal || !reflect.DeepEqual(oldVal, newVal) {
					changedFields = append(changedFields, key)
					if hasOldVal {
						oldValues[key] = oldVal
					} else {
						oldValues[key] = nil
					}
					newValues[key] = newVal
				}
			}

			// Check for fields that existed in old but not in new
			for key, oldVal := range oldRecord {
				if key == "Code" {
					continue
				}
				if _, exists := newRecord[key]; !exists {
					changedFields = append(changedFields, key)
					oldValues[key] = oldVal
					newValues[key] = nil
				}
			}

			if len(changedFields) > 0 {
				modified = append(modified, RecordChange{
					Code:          code,
					ChangeType:    "modified",
					OldValues:     oldValues,
					NewValues:     newValues,
					ChangedFields: changedFields,
				})
			}
		}
	}

	// Log detailed change information
	s.logDetailedChanges(added, deleted, modified)

	if len(added) > 0 {
		changes["added"] = added
	}
	if len(deleted) > 0 {
		changes["deleted"] = deleted
	}
	if len(modified) > 0 {
		changes["modified"] = modified
	}

	return changes
}

// logDetailedChanges logs detailed information about what changed
func (s *Server) logDetailedChanges(added []map[string]interface{}, deleted []string, modified []RecordChange) {
	// Get file timestamps
	s.lastModTimeMu.Lock()
	lastModTime := s.lastModTime
	s.lastModTimeMu.Unlock()

	fileInfo, err := os.Stat(s.dbPath)
	var currentModTime time.Time
	if err == nil {
		currentModTime = fileInfo.ModTime()
		s.lastModTimeMu.Lock()
		s.lastModTime = currentModTime
		s.lastModTimeMu.Unlock()
	}

	// Log file timestamps
	log.Println(strings.Repeat("━", 80))
	log.Printf("📁 File: %s", filepath.Base(s.dbPath))
	if !lastModTime.IsZero() {
		timeDiff := currentModTime.Sub(lastModTime)
		log.Printf("⏰ Last modified: %s (%s)", lastModTime.Format("2006-01-02 15:04:05"), formatDuration(timeDiff))
	}
	if !currentModTime.IsZero() {
		log.Printf("⏱️ Current time:  %s", currentModTime.Format("2006-01-02 15:04:05"))
	}
	log.Println(strings.Repeat("━", 80))

	totalChanges := len(added) + len(deleted) + len(modified)

	if totalChanges == 0 {
		log.Println("ℹ️  No changes detected")
		return
	}

	log.Printf("📊 Total changes: %d record(s) (%d added, %d modified, %d deleted)",
		totalChanges, len(added), len(modified), len(deleted))
	log.Println("")

	// If more than 10 records changed, show summary only
	if totalChanges > 10 {
		log.Printf("⚡ Large change detected: %d record(s) modified", totalChanges)
		log.Printf("   • Added: %d", len(added))
		log.Printf("   • Modified: %d", len(modified))
		log.Printf("   • Deleted: %d", len(deleted))
		log.Println(strings.Repeat("━", 80))
		return
	}

	// Show detailed changes for each type
	recordsShown := 0
	const maxDetailRecords = 5

	// Log added records
	for i, record := range added {
		if recordsShown >= maxDetailRecords {
			remaining := len(added) - i + len(modified) + len(deleted)
			log.Printf("   ... & %d more record(s)", remaining)
			break
		}
		code := fmt.Sprintf("%v", record["Code"])
		log.Printf("➕ Added: Code=%s", code)
		recordsShown++
	}

	// Log modified records
	for i, change := range modified {
		if recordsShown >= maxDetailRecords {
			remaining := len(modified) - i + len(deleted)
			log.Printf("   ... & %d more record(s)", remaining)
			break
		}

		if len(change.ChangedFields) == 1 && change.ChangedFields[0] != "ANBAR" {
			// Single field change (non-ANBAR) - show inline
			field := change.ChangedFields[0]
			oldVal := change.OldValues[field]
			newVal := change.NewValues[field]
			log.Printf("✏️  Modified: Code=%s, Field=%s, Old=%v, New=%v",
				change.Code, field, oldVal, newVal)
		} else {
			// Multiple field changes or ANBAR change - show as table
			// Check if ANBAR field changed
			hasANBAR := false
			for _, field := range change.ChangedFields {
				if field == "ANBAR" {
					hasANBAR = true
					break
				}
			}

			if hasANBAR {
				// Special handling for ANBAR array changes
				log.Printf("✏️  Modified: Code=%s (%d field(s) changed)", change.Code, len(change.ChangedFields))

				// Show ANBAR changes in detail
				oldANBAR, oldIsArray := change.OldValues["ANBAR"]
				newANBAR, newIsArray := change.NewValues["ANBAR"]

				if oldIsArray && newIsArray {
					// Compare arrays element by element
					oldArr, oldOk := convertToIntSlice(oldANBAR)
					newArr, newOk := convertToIntSlice(newANBAR)

					if oldOk && newOk {
						// Find which ANBAR indices changed
						changedIndices := []int{}
						maxLen := len(oldArr)
						if len(newArr) > maxLen {
							maxLen = len(newArr)
						}

						for idx := 0; idx < maxLen; idx++ {
							oldVal := 0
							newVal := 0
							if idx < len(oldArr) {
								oldVal = oldArr[idx]
							}
							if idx < len(newArr) {
								newVal = newArr[idx]
							}
							if oldVal != newVal {
								changedIndices = append(changedIndices, idx)
							}
						}

						if len(changedIndices) > 0 {
							log.Println("   ┌──────────────┬──────────────┬──────────────┐")
							log.Println("   │ ANBAR Field  │ Old Value    │ New Value    │")
							log.Println("   ├──────────────┼──────────────┼──────────────┤")
							for _, idx := range changedIndices {
								oldVal := 0
								newVal := 0
								if idx < len(oldArr) {
									oldVal = oldArr[idx]
								}
								if idx < len(newArr) {
									newVal = newArr[idx]
								}
								log.Printf("   │ ANBAR%-7d │ %-12d │ %-12d │", idx+1, oldVal, newVal)
							}
							log.Println("   └──────────────┴──────────────┴──────────────┘")
						}
					}
				}

				// Show other non-ANBAR fields if any
				nonANBARFields := []string{}
				for _, field := range change.ChangedFields {
					if field != "ANBAR" {
						nonANBARFields = append(nonANBARFields, field)
					}
				}

				if len(nonANBARFields) > 0 {
					log.Println("   ┌─────────────────┬────────────────────┬────────────────────┐")
					log.Println("   │ Field           │ Old Value          │ New Value          │")
					log.Println("   ├─────────────────┼────────────────────┼────────────────────┤")
					for _, field := range nonANBARFields {
						oldVal := fmt.Sprintf("%v", change.OldValues[field])
						newVal := fmt.Sprintf("%v", change.NewValues[field])
						if len(oldVal) > 18 {
							oldVal = oldVal[:15] + "..."
						}
						if len(newVal) > 18 {
							newVal = newVal[:15] + "..."
						}
						log.Printf("   │ %-15s │ %-18s │ %-18s │", field, oldVal, newVal)
					}
					log.Println("   └─────────────────┴────────────────────┴────────────────────┘")
				}
			} else {
				// Non-ANBAR multiple field changes - show as table
				log.Printf("✏️  Modified: Code=%s (%d field(s) changed)", change.Code, len(change.ChangedFields))
				log.Println("   ┌─────────────────┬────────────────────┬────────────────────┐")
				log.Println("   │ Field           │ Old Value          │ New Value          │")
				log.Println("   ├─────────────────┼────────────────────┼────────────────────┤")
				for _, field := range change.ChangedFields {
					oldVal := fmt.Sprintf("%v", change.OldValues[field])
					newVal := fmt.Sprintf("%v", change.NewValues[field])
					if len(oldVal) > 18 {
						oldVal = oldVal[:15] + "..."
					}
					if len(newVal) > 18 {
						newVal = newVal[:15] + "..."
					}
					log.Printf("   │ %-15s │ %-18s │ %-18s │", field, oldVal, newVal)
				}
				log.Println("   └─────────────────┴────────────────────┴────────────────────┘")
			}
		}
		recordsShown++
	}

	// Log deleted records
	for i, code := range deleted {
		if recordsShown >= maxDetailRecords {
			remaining := len(deleted) - i
			log.Printf("   ... & %d more record(s)", remaining)
			break
		}
		log.Printf("➖ Deleted: Code=%s", code)
		recordsShown++
	}

	log.Println(strings.Repeat("━", 80))
}

// StartWatching starts watching the database file for changes with the specified debounce duration
func (s *Server) StartWatching(debounceDuration time.Duration) error {
	fw, err := watcher.NewFileWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	s.watcher = fw
	s.updateSourceHash()

	if filecopy.IsURL(s.dbPath) {
		pollInterval := debounceDuration
		if pollInterval <= 0 {
			pollInterval = 5 * time.Minute
		}
		if err := fw.Poll(s.dbPath, func(path string) {
			log.Printf("🔄 Remote source changed: %s", path)
			s.notifyFileUpdated(path)
			s.broadcastUpdate()
		}, pollInterval); err != nil {
			return fmt.Errorf("failed to poll URL: %w", err)
		}
		log.Printf("👀 Polling remote source: %s (interval: %v)", s.dbPath, pollInterval)
		return nil
	}

	if err := fw.Watch(s.dbPath, func(path string) {
		log.Printf("🔄 File changed: %s", filepath.Base(path))
		s.notifyFileUpdated(path)
		s.broadcastUpdate()
	}, debounceDuration); err != nil {
		return fmt.Errorf("failed to watch file: %w", err)
	}

	fw.Start()
	ext := filepath.Ext(s.dbPath)
	fileType := "database"
	if ext == ".json" {
		fileType = "JSON"
	}
	log.Printf("👀 Watching %s file: %s", fileType, filepath.Base(s.dbPath))

	return nil
}

// Close cleans up server resources
func (s *Server) Close() error {
	var firstErr error
	if s.configWatcher != nil {
		if err := s.configWatcher.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.watcher != nil {
		if err := s.watcher.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.dataSource != nil {
		if err := s.dataSource.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	log.Printf("🚀 Starting server on %s", addr)
	log.Printf("📊 Serving file: %s", filepath.Base(s.dbPath))

	if !filecopy.IsURL(s.dbPath) {
		if _, err := os.Stat(s.dbPath); os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", s.dbPath)
		}
	}

	return http.ListenAndServe(addr, s.router)
}

func sourceBaseName(path string) string {
	if filecopy.IsURL(path) {
		if u, err := url.Parse(path); err == nil {
			if base := filepath.Base(u.Path); base != "." && base != "/" && base != "" {
				return base
			}
			return u.Host
		}
	}
	return filepath.Base(path)
}

// convertToIntSlice converts an interface{} to a slice of integers
// This handles ANBAR arrays which can be []interface{} or []float64 or []int
func convertToIntSlice(val interface{}) ([]int, bool) {
	switch v := val.(type) {
	case []interface{}:
		result := make([]int, len(v))
		for i, item := range v {
			switch num := item.(type) {
			case int:
				result[i] = num
			case float64:
				result[i] = int(num)
			case float32:
				result[i] = int(num)
			case int64:
				result[i] = int(num)
			case int32:
				result[i] = int(num)
			default:
				return nil, false
			}
		}
		return result, true
	case []int:
		return v, true
	case []float64:
		result := make([]int, len(v))
		for i, num := range v {
			result[i] = int(num)
		}
		return result, true
	default:
		return nil, false
	}
}

// formatDuration formats a duration into a human-readable string like "2 seconds ago", "5 minutes ago", etc.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	seconds := int(d.Seconds())
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24

	if days > 0 {
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
	if hours > 0 {
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	if minutes > 0 {
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	}
	if seconds > 0 {
		if seconds == 1 {
			return "1 second ago"
		}
		return fmt.Sprintf("%d seconds ago", seconds)
	}
	return "just now"
}
