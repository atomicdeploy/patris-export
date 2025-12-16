package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/atomicdeploy/patris-export/web"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Server represents the HTTP/WebSocket server
type Server struct {
	router          *mux.Router
	dbPath          string
	charMap         converter.CharMapping
	dataSource      datasource.DataSource
	watcher         *watcher.FileWatcher
	wsClients       map[*websocket.Conn]*sync.Mutex
	wsClientsMu     sync.RWMutex
	upgrader        websocket.Upgrader
	lastRecords     []map[string]interface{}
	lastRecordsMu   sync.RWMutex
}

// ChangeSet represents incremental changes to the database
type ChangeSet struct {
	Type       string                   `json:"type"`
	Timestamp  string                   `json:"timestamp"`
	Added      []map[string]interface{} `json:"added,omitempty"`
	Deleted    []string                 `json:"deleted,omitempty"`
	TotalCount int                      `json:"total_count"`
}

// NewServer creates a new server instance
func NewServer(dbPath string, charMap converter.CharMapping) (*Server, error) {
	// Create data source (supports both .db and .json files)
	ds, err := datasource.NewDataSource(dbPath, charMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}

	s := &Server{
		router:     mux.NewRouter(),
		dbPath:     dbPath,
		charMap:    charMap,
		dataSource: ds,
		wsClients:  make(map[*websocket.Conn]*sync.Mutex),
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

	// Set up routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	s.router.HandleFunc("/", s.handleWelcome).Methods("GET")
	s.router.HandleFunc("/viewer", s.handleViewer).Methods("GET")
	s.router.HandleFunc("/api/records", s.handleGetRecords).Methods("GET")
	s.router.HandleFunc("/api/info", s.handleGetInfo).Methods("GET")
	s.router.HandleFunc("/ws", s.handleWebSocket)
}

// handleWelcome serves the welcome page
func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.WelcomeHTML)
}

// handleViewer serves the SPA visualizer
func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.ViewerHTML)
}

// handleGetRecords returns all database records as JSON
func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	records, err := s.dataSource.GetRecords()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(records),
		"records": records,
	})
}

// handleGetInfo returns database schema information
func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	db, err := paradox.Open(s.dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open database: %v", err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	fields, err := db.GetFields()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get fields: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"file":        filepath.Base(s.dbPath),
		"num_records": db.GetNumRecords(),
		"num_fields":  db.GetNumFields(),
		"fields":      fields,
	})
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

	log.Printf("🔌 New WebSocket connection (total: %d)", len(s.wsClients))

	// Send initial data
	s.sendRecordsToClient(conn, connMu)

	// Handle disconnection
	go func() {
		defer func() {
			s.wsClientsMu.Lock()
			delete(s.wsClients, conn)
			s.wsClientsMu.Unlock()
			conn.Close()
			log.Printf("🔌 WebSocket disconnected (remaining: %d)", len(s.wsClients))
		}()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
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
}

// broadcastUpdate broadcasts database changes to all connected WebSocket clients
func (s *Server) broadcastUpdate() {
	s.wsClientsMu.RLock()
	clientCount := len(s.wsClients)
	s.wsClientsMu.RUnlock()

	if clientCount == 0 {
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
	if a, ok := changes["added"].([]map[string]interface{}); ok {
		added = len(a)
	}
	if d, ok := changes["deleted"].([]string); ok {
		deleted = len(d)
	}
	log.Printf("📊 Changes detected: %d added, %d deleted", added, deleted)

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

	// Find added records
	for code, record := range newMap {
		if _, exists := oldMap[code]; !exists {
			added = append(added, record)
			log.Printf("➕ Added record: Code=%s", code)
		}
	}

	// Find deleted records
	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			deleted = append(deleted, code)
			log.Printf("➖ Deleted record: Code=%s", code)
		}
	}

	if len(added) > 0 {
		changes["added"] = added
	}
	if len(deleted) > 0 {
		changes["deleted"] = deleted
	}

	return changes
}

// StartWatching starts watching the database file for changes with the specified debounce duration
func (s *Server) StartWatching(debounceDuration time.Duration) error {
	fw, err := watcher.NewFileWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	s.watcher = fw

	if err := fw.Watch(s.dbPath, func(path string) {
		log.Printf("🔄 File changed: %s", filepath.Base(path))
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
	if s.watcher != nil {
		return s.watcher.Close()
	}
	if s.dataSource != nil {
		return s.dataSource.Close()
	}
	return nil
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	log.Printf("🚀 Starting server on %s", addr)
	log.Printf("📊 Serving file: %s", filepath.Base(s.dbPath))

	if _, err := os.Stat(s.dbPath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", s.dbPath)
	}

	return http.ListenAndServe(addr, s.router)
}
