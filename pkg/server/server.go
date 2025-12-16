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
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/atomicdeploy/patris-export/web"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Server represents the HTTP/WebSocket server
type Server struct {
	router      *mux.Router
	dbPath      string
	charMap     converter.CharMapping
	watcher     *watcher.FileWatcher
	wsClients   map[*websocket.Conn]bool
	wsClientsMu sync.RWMutex
	upgrader    websocket.Upgrader
}

// NewServer creates a new server instance
func NewServer(dbPath string, charMap converter.CharMapping) (*Server, error) {
	s := &Server{
		router:    mux.NewRouter(),
		dbPath:    dbPath,
		charMap:   charMap,
		wsClients: make(map[*websocket.Conn]bool),
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
	s.router.HandleFunc("/", s.handleIndex).Methods("GET")
	s.router.HandleFunc("/api/records", s.handleGetRecords).Methods("GET")
	s.router.HandleFunc("/api/info", s.handleGetInfo).Methods("GET")
	s.router.HandleFunc("/ws", s.handleWebSocket)
}

// handleIndex serves the embedded SPA
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.IndexHTML)
}

// handleGetRecords returns all database records as JSON
func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	db, err := paradox.Open(s.dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open database: %v", err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	records, err := db.GetRecords()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert records
	convertedRecords := make([]paradox.Record, len(records))
	for i, record := range records {
		convertedRecord := make(paradox.Record)
		for key, value := range record {
			if strVal, ok := value.(string); ok {
				convertedRecord[key] = converter.Patris2Fa(strVal)
			} else {
				convertedRecord[key] = value
			}
		}
		convertedRecords[i] = convertedRecord
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(convertedRecords),
		"records": convertedRecords,
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

	s.wsClientsMu.Lock()
	s.wsClients[conn] = true
	s.wsClientsMu.Unlock()

	log.Printf("🔌 New WebSocket connection (total: %d)", len(s.wsClients))

	// Send initial data
	s.sendRecordsToClient(conn)

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
func (s *Server) sendRecordsToClient(conn *websocket.Conn) {
	db, err := paradox.Open(s.dbPath)
	if err != nil {
		log.Printf("Failed to open database: %v", err)
		return
	}
	defer db.Close()

	records, err := db.GetRecords()
	if err != nil {
		log.Printf("Failed to read records: %v", err)
		return
	}

	// Convert records
	convertedRecords := make([]paradox.Record, len(records))
	for i, record := range records {
		convertedRecord := make(paradox.Record)
		for key, value := range record {
			if strVal, ok := value.(string); ok {
				convertedRecord[key] = converter.Patris2Fa(strVal)
			} else {
				convertedRecord[key] = value
			}
		}
		convertedRecords[i] = convertedRecord
	}

	message := map[string]interface{}{
		"type":      "update",
		"timestamp": time.Now().Format(time.RFC3339),
		"count":     len(convertedRecords),
		"records":   convertedRecords,
	}

	if err := conn.WriteJSON(message); err != nil {
		log.Printf("Failed to send to WebSocket: %v", err)
	}
}

// broadcastUpdate broadcasts database changes to all connected WebSocket clients
func (s *Server) broadcastUpdate() {
	s.wsClientsMu.RLock()
	defer s.wsClientsMu.RUnlock()

	if len(s.wsClients) == 0 {
		return
	}

	log.Printf("📡 Broadcasting update to %d clients", len(s.wsClients))

	for conn := range s.wsClients {
		go s.sendRecordsToClient(conn)
	}
}

// StartWatching starts watching the database file for changes
func (s *Server) StartWatching() error {
	fw, err := watcher.NewFileWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	s.watcher = fw

	if err := fw.Watch(s.dbPath, func(path string) {
		log.Printf("🔄 Database file changed, broadcasting to clients")
		s.broadcastUpdate()
	}); err != nil {
		return fmt.Errorf("failed to watch file: %w", err)
	}

	fw.Start()
	log.Printf("👀 Watching database file: %s", filepath.Base(s.dbPath))

	return nil
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	log.Printf("🚀 Starting server on %s", addr)
	log.Printf("📊 Serving database: %s", filepath.Base(s.dbPath))
	
	if _, err := os.Stat(s.dbPath); os.IsNotExist(err) {
		return fmt.Errorf("database file does not exist: %s", s.dbPath)
	}

	return http.ListenAndServe(addr, s.router)
}

// Close cleans up server resources
func (s *Server) Close() error {
	if s.watcher != nil {
		return s.watcher.Close()
	}
	return nil
}
