package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/notifier"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/recentsales"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/atomicdeploy/patris-export/pkg/updater"
	"github.com/atomicdeploy/patris-export/pkg/version"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/atomicdeploy/patris-export/web"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Server represents the HTTP/WebSocket server
type Server struct {
	router               *mux.Router
	dbPath               string
	charMap              converter.CharMapping
	dataSource           datasource.DataSource
	dataSourceMu         sync.RWMutex
	watcher              *watcher.FileWatcher
	wsClients            map[*websocket.Conn]*sync.Mutex
	wsClientsMu          sync.RWMutex
	upgrader             websocket.Upgrader
	lastRecords          []map[string]interface{}
	lastRecordsMu        sync.RWMutex
	lastRecordsReady     bool
	lastContractRevision string
	lastModTime          time.Time
	lastModTimeMu        sync.RWMutex
	useTempFile          bool
	config               *appconfig.Manager
	configWatcher        *appconfig.ConfigWatcher
	version              version.Info
	processMu            sync.Mutex
	processStatusCache   map[string]interface{}
	processStatusAt      time.Time
	lastSourceHash       string
	lastSourceHashMu     sync.Mutex
	eventSubscribers     map[chan map[string]interface{}]struct{}
	eventSubscribersMu   sync.RWMutex
	catalogProvider      pricingcatalog.Provider
	catalogProviderKey   string
	catalogProviderMu    sync.Mutex
	canonicalProjection  *canonicalProjectionCache
	sqlOperations        *sqlOperationsState
	excelPricing         *excelPricingState
	excelPricingRemote   *excelPricingRemoteEventsBridge
	excelPricingWrites   *excelPricingWritebackQueue
	backgroundCtx        context.Context
	backgroundCancel     context.CancelFunc
	backgroundWG         sync.WaitGroup
	serviceWG            sync.WaitGroup
}

type Options struct {
	Config  *appconfig.Manager
	Version version.Info
}

// Compatibility aliases keep the existing server API while sharing one diff
// implementation with CLI watch-mode webhooks.
type RecordChange = recorddiff.RecordChange
type ChangeSet = recorddiff.ChangeSet

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

type edgeUploadResponse struct {
	Success  bool   `json:"success"`
	File     string `json:"file,omitempty"`
	Path     string `json:"path,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Records  int    `json:"records,omitempty"`
	Message  string `json:"message,omitempty"`
}

type sourceFileManifest struct {
	Name         string    `json:"name"`
	Filename     string    `json:"filename"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	LastModified time.Time `json:"last_modified"`
	DownloadURL  string    `json:"download_url"`
	GeneratedAt  time.Time `json:"generated_at"`
}

const (
	refreshWaitMaxRequestBytes = 1024
	refreshWaitTimeout         = 8 * time.Minute
)

type refreshRequest struct {
	Delivery string `json:"delivery"`
}

type refreshDeliveryResponse struct {
	Status            string `json:"status"`
	EventID           string `json:"event_id"`
	Attempts          int    `json:"attempts"`
	PendingProducts   int    `json:"pending_products"`
	DeferredProducts  int    `json:"deferred_products"`
	DeferredMissing   int    `json:"deferred_missing"`
	DeferredAmbiguous int    `json:"deferred_ambiguous"`
}

type refreshWaitResponse struct {
	Refreshed      bool                     `json:"refreshed"`
	Delivered      bool                     `json:"delivered"`
	SourceRevision string                   `json:"source_revision,omitempty"`
	Delivery       *refreshDeliveryResponse `json:"delivery,omitempty"`
	Code           string                   `json:"code,omitempty"`
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

	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	s := &Server{
		router:              mux.NewRouter(),
		dbPath:              dbPath,
		charMap:             charMap,
		dataSource:          ds,
		wsClients:           make(map[*websocket.Conn]*sync.Mutex),
		eventSubscribers:    make(map[chan map[string]interface{}]struct{}),
		canonicalProjection: newCanonicalProjectionCache(),
		sqlOperations:       newSQLOperationsState(),
		excelPricing:        newExcelPricingState(),
		backgroundCtx:       backgroundCtx,
		backgroundCancel:    backgroundCancel,
		useTempFile:         copyBeforeRead,
		config:              options.Config,
		version:             options.Version,
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
	s.excelPricing.canonical = s.canonicalRecordResultContext
	s.excelPricingRemote = newExcelPricingRemoteEventsBridge(s)
	s.excelPricing.snapshotRevisionCurrent = s.excelPricingRemote.revisionCurrent
	s.excelPricingWrites = newExcelPricingWritebackQueue(s)

	// Set up routes
	s.setupRoutes()

	if s.config != nil {
		converter.SetRTLConversion(s.config.Get().Database.RTLConversion)
		w, err := s.config.Watch(func(cfg appconfig.Config) {
			log.Printf("⚙️ Config reloaded: %s", s.config.Path())
			s.invalidateCanonicalProjection(false)
			converter.SetRTLConversion(cfg.Database.RTLConversion)
			s.excelPricingRemote.configChanged(cfg)
			s.broadcastConfig(cfg)
		})
		if err != nil {
			backgroundCancel()
			_ = ds.Close()
			return nil, fmt.Errorf("failed to watch config: %w", err)
		}
		s.configWatcher = w
		s.excelPricingRemote.start(s.backgroundCtx, &s.backgroundWG)
		// The writeback queue is a process-lifetime worker. Keep it separate
		// from backgroundWG, which is also used to await bounded startup work.
		s.excelPricingWrites.start(s.backgroundCtx, &s.serviceWG)
	}

	return s, nil
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	s.router.HandleFunc("/", s.handleWelcome).Methods("GET")
	s.router.HandleFunc("/viewer", s.handleViewer).Methods("GET")
	s.router.HandleFunc("/debug/charmap", s.handleCharmapViewer).Methods("GET")
	s.router.HandleFunc("/partials/welcome", s.handleWelcomePartial).Methods("GET")
	s.router.HandleFunc("/partials/charmap", s.handleCharmapPartial).Methods("GET")
	s.router.HandleFunc("/api/records", s.handleGetRecords).Methods("GET")
	s.router.HandleFunc("/api/records.{format:json|csv|xlsx|xlsm|xltm}", s.handleGetRecords).Methods("GET")
	s.router.HandleFunc("/api/categories", s.handleGetCategories).Methods("GET")
	s.router.HandleFunc("/api/product-sync", s.handleGetProductSyncContract).Methods("GET")
	s.router.HandleFunc("/api/recent-sales", s.handleGetRecentSales).Methods("GET")
	// Pricing sync is a general-purpose integration surface. It is deliberately
	// not named after any one client (Excel, spreadsheet, or otherwise).
	s.router.HandleFunc("/api/pricing-sync/session", s.handlePostExcelPricingSession).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/state", s.handlePostExcelPricingState).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/preview", s.handlePostExcelPricingPreview).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/apply", s.handlePostExcelPricingApply).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/writebacks", s.handlePostExcelPricingWriteback).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/writebacks/{job_id}", s.handleGetExcelPricingWriteback).Methods("GET")
	s.router.HandleFunc("/api/pricing-sync/writebacks/{job_id}/ack", s.handlePostExcelPricingWritebackACK).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/confirmations", s.handlePostExcelPricingConfirmation).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/snapshots", s.handlePostExcelPricingSnapshot).Methods("POST")
	s.router.HandleFunc("/api/pricing-sync/events", s.handleGetExcelPricingEvents).Methods("GET")
	s.router.HandleFunc("/api/pricing-sync/snapshots/{job_id}", s.handleGetExcelPricingSnapshot).Methods("GET")
	s.router.HandleFunc("/api/pricing-sync/snapshots/{job_id}/payload", s.handleGetExcelPricingSnapshotPayload).Methods("GET")
	s.router.HandleFunc("/api/pricing-sync/snapshots/{job_id}", s.handleDeleteExcelPricingSnapshot).Methods("DELETE")
	s.router.HandleFunc("/api/info", s.handleGetInfo).Methods("GET")
	s.router.HandleFunc("/api/app", s.handleGetApp).Methods("GET")
	s.router.HandleFunc("/api/charmap", s.handleGetCharmap).Methods("GET")
	s.router.HandleFunc("/api/charmap/preview", s.handlePostCharmapPreview).Methods("POST")
	s.router.HandleFunc("/api/config", s.handleGetConfig).Methods("GET")
	s.router.HandleFunc("/api/config", s.handlePutConfig).Methods("PUT")
	s.router.HandleFunc("/api/status", s.handleGetStatus).Methods("GET")
	s.router.HandleFunc("/api/refresh", s.handlePostRefresh).Methods("POST")
	s.router.HandleFunc("/api/toast", s.handlePostToast).Methods("POST")
	s.router.HandleFunc("/api/edge/upload", s.handlePostEdgeUpload).Methods("POST")
	s.router.HandleFunc("/api/source/manifest", s.handleGetSourceManifest).Methods("GET")
	s.router.HandleFunc("/api/source/file", s.handleGetSourceFile).Methods("GET", "HEAD")
	s.router.HandleFunc("/api/update/manifest", s.handleGetUpdateManifest).Methods("GET")
	s.router.HandleFunc("/api/update/executable", s.handleGetExecutable).Methods("GET", "HEAD")
	s.router.HandleFunc("/api/processes/patris81", s.handleGetPatris81Processes).Methods("GET")
	s.router.HandleFunc("/api/processes/file", s.handleGetFileProcesses).Methods("GET")
	s.router.HandleFunc("/api/sql-target/session", s.handlePostSQLTargetSession).Methods("POST")
	s.router.HandleFunc("/api/sql-target/session", s.handleDeleteSQLTargetSession).Methods("DELETE")
	s.router.HandleFunc("/api/sql-target/status", s.handleGetSQLTargetStatus).Methods("GET")
	s.router.HandleFunc("/api/sql-target/test", s.handlePostSQLTargetTest).Methods("POST")
	s.router.HandleFunc("/api/sql-target/preview", s.handlePostSQLTargetPreview).Methods("POST")
	s.router.HandleFunc("/api/sql-target/sync", s.handlePostSQLTargetSync).Methods("POST")
	s.router.HandleFunc("/api/sql-target/last-result", s.handleGetSQLTargetLastResult).Methods("GET")
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
	result, err := s.RecordResult()
	if err != nil {
		return nil, fmt.Errorf("failed to read records: %w", err)
	}
	return recordsPayload(result), nil
}

func (s *Server) RecordsPayload() (interface{}, error) {
	result, err := s.RecordResult()
	if err != nil {
		return nil, fmt.Errorf("failed to read records: %w", err)
	}
	return recordsPayload(result), nil
}

func recordsPayload(result recordpipe.Result) map[string]interface{} {
	return recordmap.Keyed(result.Rows, result.KeyField, true)
}

func categoriesPayload(result recordpipe.Result) map[string]interface{} {
	if result.Contract == nil {
		return map[string]interface{}{}
	}
	return recordmap.Keyed(canonical.CategoriesToRows(result.Contract.Categories), "category_code", true)
}

func (s *Server) RecordResult() (recordpipe.Result, error) {
	return s.RecordResultContext(context.Background())
}

// RecordResultContext prepares the shared source projection with cooperative
// cancellation. Context-aware data sources stop file copies, downloads, and
// JSON reads promptly; native pxlib record extraction checks cancellation
// immediately before and after its non-interruptible native call.
func (s *Server) RecordResultContext(ctx context.Context) (recordpipe.Result, error) {
	return s.recordResultContext(ctx, s.recordOptions())
}

// canonicalRecordResultContext prepares the configured canonical projection
// independently from the viewer/export raw-mode preference. Raw mode controls
// observational source rows; it must not disable the dedicated integration
// contract for a dataset with an enabled canonical profile.
func (s *Server) canonicalRecordResultContext(ctx context.Context) (recordpipe.Result, error) {
	build := func(buildContext context.Context) (recordpipe.Result, error) {
		options := s.recordOptions()
		options.Raw = false
		return s.recordResultContext(buildContext, options)
	}
	if s.canonicalProjection == nil {
		return build(ctx)
	}
	return s.canonicalProjection.get(
		ctx,
		func() time.Duration { return canonicalProjectionMaxAge(s.Config()) },
		build,
	)
}

func (s *Server) recordResultContext(ctx context.Context, options recordpipe.Options) (recordpipe.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.dataSourceMu.RLock()
	ds := s.dataSource
	dbPath := s.dbPath
	s.dataSourceMu.RUnlock()
	if ds == nil {
		return recordpipe.Result{}, fmt.Errorf("data source is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return recordpipe.Result{}, err
	}
	var (
		records []map[string]interface{}
		err     error
	)
	if contextSource, ok := ds.(datasource.ContextDataSource); ok {
		records, err = contextSource.GetRawRecordsContext(ctx)
	} else {
		if ctx.Done() != nil {
			return recordpipe.Result{}, fmt.Errorf("data source does not support bounded record reads")
		}
		records, err = ds.GetRawRecords()
	}
	if err != nil {
		return recordpipe.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return recordpipe.Result{}, err
	}
	result := recordpipe.BuildContext(ctx, records, dbPath, options)
	if err := ctx.Err(); err != nil {
		return recordpipe.Result{}, err
	}
	return result, nil
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
		"file":        sourceBaseName(s.currentDBPath()),
		"path":        s.currentDBPath(),
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

// browserConfig returns the configuration shape that may be sent to the Web
// UI. Database credentials remain server-side and can only be supplied through
// protected config files, environment variables, or command-line options.
func browserConfig(cfg appconfig.Config) appconfig.Config {
	cfg.Export.MySQLDSN = ""
	cfg.Export.MySQLTLSCAFile = ""
	cfg.Export.MySQLTLSServerName = ""
	cfg.Export.XLSXTemplate = ""
	cfg.Export.XLSXTarget = ""
	cfg.Export.XLSMTemplate = ""
	cfg.Export.XLSMTarget = ""
	cfg.Export.XLTMTemplate = ""
	cfg.Export.XLTMTarget = ""
	cfg.RecentSales = recentsales.Config{}
	return cfg
}

func (s *Server) currentDBPath() string {
	s.dataSourceMu.RLock()
	defer s.dataSourceMu.RUnlock()
	return s.dbPath
}

func (s *Server) recordsRaw() ([]map[string]interface{}, error) {
	result, err := s.RecordResult()
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

func (s *Server) recordOptions() recordpipe.Options {
	cfg := s.Config()
	return recordpipe.Options{
		Raw:             cfg.Database.Raw,
		Mapping:         cfg.Transform,
		Canonical:       cfg.Canonical,
		CatalogProvider: s.pricingCatalogProvider(cfg),
		GeneratedAt:     time.Now(),
	}
}

func (s *Server) pricingCatalogProvider(cfg appconfig.Config) pricingcatalog.Provider {
	material, _ := json.Marshal(cfg.Canonical.Pricing)
	key := string(material)
	s.catalogProviderMu.Lock()
	defer s.catalogProviderMu.Unlock()
	if s.catalogProvider == nil || s.catalogProviderKey != key {
		s.catalogProvider = pricingcatalog.NewProvider(cfg.Canonical.Pricing)
		s.catalogProviderKey = key
	}
	return s.catalogProvider
}

func (s *Server) replaceDataSource(path string, records []map[string]interface{}) error {
	ds, err := datasource.NewDataSource(path, s.charMap, false)
	if err != nil {
		return err
	}
	if records == nil {
		records, err = ds.GetRecords()
		if err != nil {
			ds.Close()
			return err
		}
	}
	sourceEpoch := s.excelPricingRemote.fenceSourceChange()

	s.dataSourceMu.Lock()
	old := s.dataSource
	s.dataSource = ds
	s.dbPath = path
	s.useTempFile = false
	s.dataSourceMu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	s.lastSourceHashMu.Lock()
	s.lastSourceHash = ""
	s.lastSourceHashMu.Unlock()
	s.notifyExcelPricingSourceChanged("")
	s.excelPricingRemote.commitSourceChange(sourceEpoch)
	return nil
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
	s.invalidateCanonicalProjection(false)
	s.excelPricingRemote.configChanged(cfg)
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
	s.invalidateCanonicalProjection(false)
	s.broadcastInitialSnapshot("source_changed")
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
	s.dataSourceMu.RLock()
	dbPath := s.dbPath
	useTempFile := s.useTempFile
	s.dataSourceMu.RUnlock()

	pathToOpen := dbPath
	cleanup := func() {}
	if filecopy.IsURL(dbPath) {
		tempFileInfo, err := filecopy.DownloadToTemp(dbPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download database to temp: %w", err)
		}
		pathToOpen = tempFileInfo.TempPath
		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}
	} else if useTempFile && strings.EqualFold(filepath.Ext(dbPath), ".db") {
		tempFileInfo, err := filecopy.CopyToTemp(dbPath)
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

func (s *Server) handleWelcomePartial(w http.ResponseWriter, r *http.Request) {
	writeHTMLPartial(w, web.WelcomeHTML)
}

func (s *Server) handleCharmapPartial(w http.ResponseWriter, r *http.Request) {
	writeHTMLPartial(w, web.CharmapHTML)
}

func writeHTMLPartial(w http.ResponseWriter, page []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	html := string(page)
	lower := strings.ToLower(html)
	var parts []string

	searchFrom := 0
	for {
		start := strings.Index(lower[searchFrom:], "<style")
		if start < 0 {
			break
		}
		start += searchFrom
		end := strings.Index(lower[start:], "</style>")
		if end < 0 {
			break
		}
		end += start + len("</style>")
		parts = append(parts, html[start:end])
		searchFrom = end
	}

	bodyStart := strings.Index(lower, "<body")
	if bodyStart >= 0 {
		bodyOpenEnd := strings.Index(lower[bodyStart:], ">")
		bodyEnd := strings.LastIndex(lower, "</body>")
		if bodyOpenEnd >= 0 && bodyEnd > bodyStart {
			bodyOpenEnd += bodyStart + 1
			parts = append(parts, html[bodyOpenEnd:bodyEnd])
		}
	}
	if len(parts) == 0 {
		parts = append(parts, html)
	}
	_, _ = w.Write([]byte(strings.Join(parts, "\n")))
}

// handleGetRecords returns records as JSON/CSV/generated XLSX, populates only
// the configured trusted XLSM target, or returns only a verified blank XLTM.
func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	format := requestedRecordsFormat(r)
	if format == "" {
		http.Error(w, "unsupported records format; use json, csv, xlsx, xlsm, or xltm", http.StatusBadRequest)
		return
	}
	if format == "xltm" {
		s.writeRecordsXLTM(w, r)
		return
	}
	result, err := s.RecordResult()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}

	if format == "csv" {
		s.writeRecordsCSV(w, r, result.Rows, result.KeyField)
		return
	}
	if format == "xlsx" {
		s.writeRecordsXLSX(w, r, result)
		return
	}
	if format == "xlsm" {
		s.writeRecordsXLSM(w, r, result)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(recordsPayload(result)); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode JSON: %v", err), http.StatusInternalServerError)
	}
}

// handleGetCategories exposes structural catalog rows independently from the
// product collection. Generic/noncanonical datasets return 404 rather than an
// empty shape that could be mistaken for a canonical catalog.
func (s *Server) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	timeout := canonicalRequestTimeout(s.Config())
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, err := s.canonicalRecordResultContext(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			http.Error(w, fmt.Sprintf("Canonical categories timed out after %s", timeout), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}
	if result.Contract == nil {
		http.Error(w, "canonical categories are not available for this dataset", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(categoriesPayload(result)); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode categories: %v", err), http.StatusInternalServerError)
	}
}

// handleGetProductSyncContract exposes the living integration envelope
// without changing the long-standing row collection returned by /api/records.
func (s *Server) handleGetProductSyncContract(w http.ResponseWriter, r *http.Request) {
	timeout := canonicalRequestTimeout(s.Config())
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, err := s.canonicalRecordResultContext(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			http.Error(w, fmt.Sprintf("Canonical product-sync timed out after %s", timeout), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to read records: %v", err), http.StatusInternalServerError)
		return
	}
	if result.Contract == nil {
		http.Error(w, "canonical product-sync contract is not available for this dataset", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.patris.product-sync+json")
	if err := json.NewEncoder(w).Encode(result.Contract); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode product-sync contract: %v", err), http.StatusInternalServerError)
	}
}

func canonicalRequestTimeout(cfg appconfig.Config) time.Duration {
	timeout := 30 * time.Second
	if strings.EqualFold(strings.TrimSpace(cfg.Canonical.Pricing.Mode), "digitalogic") {
		if configured, err := time.ParseDuration(strings.TrimSpace(cfg.Canonical.Pricing.Digitalogic.Timeout)); err == nil && configured > 0 {
			// The remote deadline covers catalog and assignment requests. Keep a
			// separate bounded window for transforming, hashing, and encoding a
			// large canonical workbook contract after those requests complete.
			grace := configured
			if grace > 30*time.Second {
				grace = 30 * time.Second
			}
			timeout = configured + grace
		}
	}
	if timeout < time.Second {
		return time.Second
	}
	if timeout > 2*time.Minute {
		return 2 * time.Minute
	}
	return timeout
}

// handleGetRecentSales exposes only a privacy-safe product-level aggregate.
// It authenticates before reading the separately configured source and never
// serializes or returns source rows.
func (s *Server) handleGetRecentSales(w http.ResponseWriter, r *http.Request) {
	cfg := recentsales.DefaultConfig()
	if s.config != nil {
		cfg = s.config.Get().RecentSales
	}
	cfg = recentsales.NormalizeConfig(cfg)
	if !cfg.Enabled {
		writeRecentSalesError(w, http.StatusNotFound, "not_available", "Recent-sales aggregates are not available.")
		return
	}
	token := strings.TrimSpace(os.Getenv(cfg.TokenEnv))
	if len(token) < 16 {
		writeRecentSalesError(w, http.StatusServiceUnavailable, "not_configured", "Recent-sales authentication is not configured.")
		return
	}
	if !bearerTokenAuthorized(r, token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="patris-export-recent-sales"`)
		writeRecentSalesError(w, http.StatusUnauthorized, "unauthorized", "A valid bearer token is required.")
		return
	}
	query, err := recentsales.ParseQuery(r.URL.Query(), cfg)
	if err != nil {
		writeRecentSalesError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	envelope, err := recentsales.Load(r.Context(), cfg, query, s.currentDBPath())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeRecentSalesError(w, http.StatusRequestTimeout, "request_cancelled", "The recent-sales request was cancelled.")
			return
		}
		writeRecentSalesError(w, http.StatusServiceUnavailable, "source_unavailable", "The recent-sales aggregate source is unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", recentsales.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := json.NewEncoder(w).Encode(envelope); err != nil {
		http.Error(w, "Failed to encode recent-sales aggregate.", http.StatusInternalServerError)
	}
}

func writeRecentSalesError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSONStatus(w, status, map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func requestedRecordsFormat(r *http.Request) string {
	if routeFormat, ok := mux.Vars(r)["format"]; ok {
		return strings.ToLower(strings.TrimSpace(routeFormat))
	}
	queryFormat := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if queryFormat != "" {
		switch queryFormat {
		case "json", "csv", "xlsx", "xlsm", "xltm":
			return queryFormat
		default:
			return ""
		}
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/csv") {
		return "csv"
	}
	if strings.Contains(accept, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
		return "xlsx"
	}
	return "json"
}

func rejectsClientOfficeSource(r *http.Request) bool {
	for _, key := range []string{"template", "template_path", "path", "source", "source_url", "url"} {
		if strings.TrimSpace(r.URL.Query().Get(key)) != "" {
			return true
		}
	}
	return false
}

func requestWantsBakedTemplateData(r *http.Request) bool {
	for _, key := range []string{"populate", "bake_data", "include_data"} {
		switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func (s *Server) trustedOfficeContract(format string) (recordsink.TrustedOfficeContract, bool) {
	if s.config == nil {
		return recordsink.TrustedOfficeContract{}, false
	}
	cfg := s.config.Get()
	contract := recordsink.TrustedOfficeContract{}
	switch format {
	case "xlsx":
		contract.TemplatePath = strings.TrimSpace(cfg.Export.XLSXTemplate)
		contract.Target = strings.TrimSpace(cfg.Export.XLSXTarget)
	case "xlsm":
		contract.TemplatePath = strings.TrimSpace(cfg.Export.XLSMTemplate)
		contract.Target = strings.TrimSpace(cfg.Export.XLSMTarget)
	case "xltm":
		contract.TemplatePath = strings.TrimSpace(cfg.Export.XLTMTemplate)
		contract.Target = strings.TrimSpace(cfg.Export.XLTMTarget)
	}
	return contract, contract.TemplatePath != "" && contract.Target != ""
}

func officeDownloadName(dbPath, extension string) string {
	name := strings.TrimSuffix(sourceBaseName(dbPath), filepath.Ext(sourceBaseName(dbPath)))
	if strings.TrimSpace(name) == "" {
		name = "patris-export"
	}
	return name + extension
}

func freshOfficeOutputPath(extension string) (string, error) {
	temporary, err := os.CreateTemp("", "patris-records-*"+extension)
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func serveVerifiedOfficeArtifact(w http.ResponseWriter, r *http.Request, path, name, contentType string, report recordsink.OfficeArtifactReport) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Patris-Office-Source-SHA256", report.SourceSHA256)
	w.Header().Set("X-Patris-Office-Output-SHA256", report.OutputSHA256)
	w.Header().Set("X-Patris-Office-Record-Count", strconv.Itoa(report.RecordCount))
	if report.DataEmpty {
		w.Header().Set("X-Patris-Template-Data-Empty", "true")
	}
	http.ServeContent(w, r, name, stat.ModTime(), file)
	return nil
}

func (s *Server) writeRecordsXLSM(w http.ResponseWriter, r *http.Request, result recordpipe.Result) {
	if rejectsClientOfficeSource(r) {
		http.Error(w, "client-supplied Office source paths and URLs are not accepted", http.StatusBadRequest)
		return
	}
	contract, configured := s.trustedOfficeContract("xlsm")
	if !configured {
		if contract.TemplatePath != "" || contract.Target != "" {
			http.Error(w, "XLSM trusted template and explicit target must be configured together", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "XLSM trusted template and explicit target are not configured", http.StatusNotFound)
		return
	}
	path, err := freshOfficeOutputPath(".xlsm")
	if err != nil {
		http.Error(w, "Failed to create temporary XLSM output", http.StatusInternalServerError)
		return
	}
	defer os.Remove(path)
	report, err := recordsink.PopulateTrustedXLSM(path, result.Rows, result.KeyField, s.recordsXLSXOptions(r, result), contract)
	if err != nil {
		http.Error(w, "Configured XLSM package failed population or verification: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	name := officeDownloadName(s.currentDBPath(), ".xlsm")
	if err := serveVerifiedOfficeArtifact(w, r, path, name, "application/vnd.ms-excel.sheet.macroEnabled.12", report); err != nil {
		http.Error(w, "Failed to serve verified XLSM output", http.StatusInternalServerError)
	}
}

func (s *Server) writeRecordsXLTM(w http.ResponseWriter, r *http.Request) {
	if rejectsClientOfficeSource(r) {
		http.Error(w, "client-supplied Office source paths and URLs are not accepted", http.StatusBadRequest)
		return
	}
	if requestWantsBakedTemplateData(r) {
		w.Header().Set("X-Patris-Template-Data-Empty", "required")
		http.Error(w, "XLTM data population is forbidden; configured templates must remain blank", http.StatusConflict)
		return
	}
	contract, configured := s.trustedOfficeContract("xltm")
	if !configured {
		if contract.TemplatePath != "" || contract.Target != "" {
			http.Error(w, "XLTM trusted template and explicit target must be configured together", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "XLTM trusted template and explicit target are not configured", http.StatusNotFound)
		return
	}
	path, err := freshOfficeOutputPath(".xltm")
	if err != nil {
		http.Error(w, "Failed to create temporary XLTM output", http.StatusInternalServerError)
		return
	}
	defer os.Remove(path)
	report, err := recordsink.CopyVerifiedBlankXLTM(path, contract)
	if err != nil {
		http.Error(w, "Configured XLTM package failed blank-template verification: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	name := officeDownloadName(s.currentDBPath(), ".xltm")
	if err := serveVerifiedOfficeArtifact(w, r, path, name, "application/vnd.ms-excel.template.macroEnabled.12", report); err != nil {
		http.Error(w, "Failed to serve verified XLTM output", http.StatusInternalServerError)
	}
}

func (s *Server) writeRecordsCSV(w http.ResponseWriter, r *http.Request, records []map[string]interface{}, keyField string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	if wantsDownload(r) {
		name := strings.TrimSuffix(sourceBaseName(s.currentDBPath()), filepath.Ext(sourceBaseName(s.currentDBPath())))
		if strings.TrimSpace(name) == "" {
			name = "patris-export"
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".csv"))
	}

	if err := recordsink.WriteCSV(w, records, keyField); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode CSV: %v", err), http.StatusInternalServerError)
	}
}

func (s *Server) writeRecordsXLSX(w http.ResponseWriter, r *http.Request, result recordpipe.Result) {
	if rejectsClientOfficeSource(r) {
		http.Error(w, "client-supplied Office source paths and URLs are not accepted", http.StatusBadRequest)
		return
	}
	contract, configured := s.trustedOfficeContract("xlsx")
	if configured {
		path, err := freshOfficeOutputPath(".xlsx")
		if err != nil {
			http.Error(w, "Failed to create temporary XLSX output", http.StatusInternalServerError)
			return
		}
		defer os.Remove(path)
		report, err := recordsink.PopulateTrustedXLSX(path, result.Rows, result.KeyField, s.recordsXLSXOptions(r, result), contract)
		if err != nil {
			http.Error(w, "Configured XLSX package failed population or verification: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		name := officeDownloadName(s.currentDBPath(), ".xlsx")
		if err := serveVerifiedOfficeArtifact(w, r, path, name, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", report); err != nil {
			http.Error(w, "Failed to serve verified XLSX output", http.StatusInternalServerError)
		}
		return
	}
	if contract.TemplatePath != "" || contract.Target != "" {
		http.Error(w, "XLSX trusted template and explicit target must be configured together", http.StatusUnprocessableEntity)
		return
	}
	temp, err := os.CreateTemp("", "patris-records-*.xlsx")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create XLSX: %v", err), http.StatusInternalServerError)
		return
	}
	tempPath := temp.Name()
	_ = temp.Close()
	defer os.Remove(tempPath)
	options := s.recordsXLSXOptions(r, result)
	if err := recordsink.WriteXLSX(tempPath, result.Rows, result.KeyField, options); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode XLSX: %v", err), http.StatusInternalServerError)
		return
	}
	file, err := os.Open(tempPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open XLSX: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to stat XLSX: %v", err), http.StatusInternalServerError)
		return
	}
	name := officeDownloadName(s.currentDBPath(), ".xlsx")
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, stat.ModTime(), file)
}

func (s *Server) recordsXLSXOptions(r *http.Request, result recordpipe.Result) recordsink.XLSXOptions {
	dataset := sourceBaseName(s.currentDBPath())
	rtl := false
	preferences := recordsink.XLSXPreferences{Language: "en", Mode: "precalculated", ZebraRows: true}
	if s.config != nil {
		cfg := s.config.Get()
		preferences.Language = recordsink.ResolveXLSXLanguage(cfg.Export.XLSXLanguage, cfg.UI.Language)
		preferences.Mode = cfg.Export.XLSXMode
		preferences.ZebraRows = cfg.Export.XLSXZebraRows
		preferences.ColumnLabels = cfg.ColumnLabels
		rtl = cfg.UI.RTLTextDirection || preferences.Language == "fa"
	}
	if value := strings.TrimSpace(r.URL.Query().Get("language")); value != "" {
		preferences.Language = recordsink.ResolveXLSXLanguage(value, preferences.Language)
	}
	if value := strings.TrimSpace(r.URL.Query().Get("mode")); value != "" {
		preferences.Mode = value
	}
	if value, exists := r.URL.Query()["zebra"]; exists && len(value) > 0 {
		switch strings.ToLower(strings.TrimSpace(value[len(value)-1])) {
		case "1", "true", "yes", "on":
			preferences.ZebraRows = true
		case "0", "false", "no", "off":
			preferences.ZebraRows = false
		}
	}
	rtl = rtl || preferences.Language == "fa"
	if value, exists := r.URL.Query()["rtl"]; exists && len(value) > 0 {
		switch strings.ToLower(strings.TrimSpace(value[len(value)-1])) {
		case "1", "true", "yes", "rtl":
			rtl = true
		case "0", "false", "no", "ltr":
			rtl = false
		}
	}
	options := result.XLSXOptions(dataset, rtl, preferences)
	return options
}

func wantsDownload(r *http.Request) bool {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("download")))
	return value == "1" || value == "true" || value == "yes"
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
	if value := strings.TrimSpace(os.Getenv("PATRIS_EXPORT_DEBUG")); value != "" {
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
		writeJSON(w, browserConfig(appconfig.Default()))
		return
	}
	writeJSON(w, browserConfig(s.config.Get()))
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
	// The browser is not a connection-management surface. Preserve protected
	// server-side connection material even if an old cache or crafted request
	// includes replacement values.
	protectedConfig := s.config.Get()
	protectedExport := protectedConfig.Export
	cfg.Export.MySQLDSN = protectedExport.MySQLDSN
	cfg.Export.MySQLTLSCAFile = protectedExport.MySQLTLSCAFile
	cfg.Export.MySQLTLSServerName = protectedExport.MySQLTLSServerName
	cfg.Export.XLSXTemplate = protectedExport.XLSXTemplate
	cfg.Export.XLSXTarget = protectedExport.XLSXTarget
	cfg.Export.XLSMTemplate = protectedExport.XLSMTemplate
	cfg.Export.XLSMTarget = protectedExport.XLSMTarget
	cfg.Export.XLTMTemplate = protectedExport.XLTMTemplate
	cfg.Export.XLTMTarget = protectedExport.XLTMTarget
	cfg.RecentSales = protectedConfig.RecentSales
	cfg, err := s.ReplaceConfig(cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"success": true,
		"config":  browserConfig(cfg),
	})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Status())
}

func (s *Server) handlePostRefresh(w http.ResponseWriter, r *http.Request) {
	if refreshWaitOptedIn(r) {
		setExcelPricingResponseHeaders(w)
		if !excelPricingLocalRequestAllowed(r) ||
			!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
			!s.excelPricing.authorizedSession(r) {
			writeRefreshWaitError(w, http.StatusForbidden, false, "", "local_session_required")
			return
		}
		if !singleJSONContentType(r) {
			writeRefreshWaitError(w, http.StatusUnsupportedMediaType, false, "", "json_required")
			return
		}
		delivery, err := refreshDeliveryMode(w, r)
		if err != nil {
			writeRefreshWaitError(w, http.StatusBadRequest, false, "", "invalid_request")
			return
		}
		if delivery == "wait" {
			s.handlePostRefreshWait(w, r)
			return
		}
	}
	s.Refresh()
	writeJSON(w, map[string]interface{}{"refreshed": true})
}

func refreshWaitOptedIn(r *http.Request) bool {
	return r.Body != nil &&
		r.Body != http.NoBody &&
		r.ContentLength != 0 &&
		len(r.Header.Values(excelPricingClientHeader)) > 0
}

func refreshDeliveryMode(w http.ResponseWriter, r *http.Request) (string, error) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return "", nil
	}
	var request refreshRequest
	if err := decodeBoundedJSON(w, r, refreshWaitMaxRequestBytes, &request); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	delivery := strings.ToLower(strings.TrimSpace(request.Delivery))
	switch delivery {
	case "", "wait":
		return delivery, nil
	default:
		return "", errors.New("unsupported refresh delivery mode")
	}
}

func (s *Server) handlePostRefreshWait(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !excelPricingLocalRequestAllowed(r) ||
		!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
		!s.excelPricing.authorizedSession(r) {
		writeRefreshWaitError(w, http.StatusForbidden, false, "", "local_session_required")
		return
	}

	cfg := s.Config()
	deliveryConfig := updateout.Normalize(cfg.SendUpdates)
	if !deliveryConfig.Enabled ||
		deliveryConfig.Format != "json" ||
		deliveryConfig.Method != http.MethodPost ||
		strings.TrimSpace(deliveryConfig.URL) == "" ||
		strings.TrimSpace(deliveryConfig.ProductSyncSecretEnv) == "" {
		writeRefreshWaitError(w, http.StatusServiceUnavailable, false, "", "delivery_unavailable")
		return
	}
	if _, err := updateout.ResolveProductSyncSecret(deliveryConfig); err != nil {
		writeRefreshWaitError(w, http.StatusServiceUnavailable, false, "", "delivery_unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshWaitTimeout)
	defer cancel()
	select {
	case s.excelPricing.permit <- struct{}{}:
		defer func() { <-s.excelPricing.permit }()
	default:
		writeExcelPricingBusy(w, "pricing_busy")
		return
	}

	contract, err := s.excelPricingCanonical(ctx, cfg)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "canonical_unavailable"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusRequestTimeout
			code = "request_cancelled"
		}
		writeRefreshWaitError(w, status, false, "", code)
		return
	}

	// The wait extension synchronously delivers this freshly projected canonical
	// envelope. Calling Refresh here would also enqueue the legacy asynchronous
	// "initial" delivery and could send the same source revision twice.
	event := updateout.Event{
		Type:             "update",
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Source:           s.currentDBPath(),
		Raw:              false,
		Contract:         contract,
		SnapshotContract: contract,
	}
	dispatch := s.excelPricing.dispatch
	if dispatch == nil {
		dispatch = updateout.DispatchWithResult
	}
	result, err := dispatch(ctx, deliveryConfig, event)
	if err != nil || !excelPricingDeliveryComplete(result, contract.EventID) {
		writeRefreshWaitError(w, http.StatusBadGateway, true, contract.Source.Revision, "delivery_failed")
		return
	}

	writeJSON(w, refreshWaitResponse{
		Refreshed:      true,
		Delivered:      true,
		SourceRevision: contract.Source.Revision,
		Delivery: &refreshDeliveryResponse{
			Status:            result.Status,
			EventID:           result.EventID,
			Attempts:          result.Attempts,
			PendingProducts:   result.PendingProducts,
			DeferredProducts:  result.DeferredProducts,
			DeferredMissing:   result.DeferredMissing,
			DeferredAmbiguous: result.DeferredAmbiguous,
		},
	})
}

func writeRefreshWaitError(w http.ResponseWriter, status int, refreshed bool, sourceRevision, code string) {
	writeJSONStatus(w, status, refreshWaitResponse{
		Refreshed:      refreshed,
		Delivered:      false,
		SourceRevision: sourceRevision,
		Code:           code,
	})
}

func (s *Server) handleGetSourceManifest(w http.ResponseWriter, r *http.Request) {
	manifest, _, err := s.sourceFileManifest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to inspect source file: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, manifest)
}

func (s *Server) handleGetSourceFile(w http.ResponseWriter, r *http.Request) {
	manifest, sourcePath, err := s.sourceFileManifest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to inspect source file: %v", err), http.StatusInternalServerError)
		return
	}

	file, err := os.Open(sourcePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open source file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": manifest.Filename}))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("ETag", fmt.Sprintf("\"sha256:%s\"", manifest.SHA256))
	w.Header().Set("X-Checksum-SHA256", manifest.SHA256)
	w.Header().Set("X-Source-File", manifest.Filename)
	w.Header().Set("X-Source-Size", fmt.Sprintf("%d", manifest.Size))
	w.Header().Set("X-Source-Modified", manifest.LastModified.UTC().Format(time.RFC3339))
	http.ServeContent(w, r, manifest.Filename, manifest.LastModified, file)
}

func (s *Server) handleGetUpdateManifest(w http.ResponseWriter, r *http.Request) {
	manifest, err := s.executableManifest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to inspect executable: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, manifest)
}

func (s *Server) handleGetExecutable(w http.ResponseWriter, r *http.Request) {
	manifest, exePath, err := s.executableManifestForPath(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to inspect executable: %v", err), http.StatusInternalServerError)
		return
	}

	file, err := os.Open(exePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open executable: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": manifest.Filename}))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("ETag", fmt.Sprintf("\"sha256:%s\"", manifest.SHA256))
	w.Header().Set("X-Checksum-SHA256", manifest.SHA256)
	w.Header().Set("X-Executable-Size", fmt.Sprintf("%d", manifest.Size))
	w.Header().Set("X-Executable-Modified", manifest.LastModified.UTC().Format(time.RFC3339))
	http.ServeContent(w, r, manifest.Filename, manifest.LastModified, file)
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

func (s *Server) handlePostEdgeUpload(w http.ResponseWriter, r *http.Request) {
	if !s.edgeUploadAuthorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="patris-export-edge"`)
		writeJSONStatus(w, http.StatusUnauthorized, edgeUploadResponse{Success: false, Message: "edge upload token is required or invalid"})
		return
	}

	cfg := s.Config()
	maxBytes := cfg.Edge.MaxUploadMB * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024*1024)
	if err := r.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to parse upload: %v", err)})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: "multipart field 'file' is required"})
		return
	}
	defer file.Close()

	originalName := firstNonEmpty(r.FormValue("file_name"), header.Filename, "edge-upload.db")
	ext := strings.ToLower(filepath.Ext(originalName))
	if ext != ".db" && ext != ".json" {
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: "uploaded file must be .db or .json"})
		return
	}

	uploadDir := appconfig.ResolveTempDir(cfg.Edge.UploadDir)
	if strings.TrimSpace(cfg.Edge.UploadDir) == "" || strings.EqualFold(cfg.Edge.UploadDir, "edge-uploads") {
		uploadDir = filepath.Join(filecopy.TempRootForSize(header.Size), "edge-uploads")
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to create upload directory: %v", err)})
		return
	}

	sourceID := firstNonEmpty(r.FormValue("source_id"), r.Header.Get("X-Patris-Source-ID"), "edge")
	filename := fmt.Sprintf("%s-%s-%d%s", sanitizeFilename(sourceID), sanitizeFilename(strings.TrimSuffix(filepath.Base(originalName), ext)), time.Now().UTC().UnixNano(), ext)
	destPath := filepath.Join(uploadDir, filename)
	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to create upload file: %v", err)})
		return
	}

	hash := crc32.NewIEEE()
	written, copyErr := io.CopyBuffer(io.MultiWriter(dest, hash), file, make([]byte, filecopy.ChunkSize))
	closeErr := dest.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusInternalServerError, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to save upload: %v", copyErr)})
		return
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusInternalServerError, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to close upload file: %v", closeErr)})
		return
	}
	if written > maxBytes {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusRequestEntityTooLarge, edgeUploadResponse{Success: false, Message: "uploaded file exceeds configured edge.max_upload_mb"})
		return
	}
	if expectedSize := firstNonEmpty(r.FormValue("size"), r.Header.Get("X-Patris-File-Size")); expectedSize != "" {
		if parsed, err := strconv.ParseInt(expectedSize, 10, 64); err == nil && parsed != written {
			_ = os.Remove(destPath)
			writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: fmt.Sprintf("upload size mismatch: expected %d got %d", parsed, written)})
			return
		}
	}

	gotHash := fmt.Sprintf("%08x", hash.Sum32())
	expectedHash := firstNonEmpty(r.FormValue("crc32"), r.Header.Get("X-Patris-File-CRC32"))
	if expectedHash != "" && !strings.EqualFold(expectedHash, gotHash) {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: fmt.Sprintf("upload checksum mismatch: expected %s got %s", expectedHash, gotHash)})
		return
	}
	if modTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.FormValue("mod_time"))); err == nil {
		_ = os.Chtimes(destPath, time.Now(), modTime)
	}

	ds, err := datasource.NewDataSource(destPath, s.charMap, false)
	if err != nil {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to open uploaded source: %v", err)})
		return
	}
	records, err := ds.GetRecords()
	if err != nil {
		_ = ds.Close()
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusBadRequest, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to read uploaded source: %v", err)})
		return
	}
	_ = ds.Close()
	if err := s.replaceDataSource(destPath, records); err != nil {
		_ = os.Remove(destPath)
		writeJSONStatus(w, http.StatusInternalServerError, edgeUploadResponse{Success: false, Message: fmt.Sprintf("failed to activate uploaded source: %v", err)})
		return
	}

	log.Printf("📥 Edge upload accepted from %s: %s (%d bytes, %d records, crc32=%s)", sourceID, filepath.Base(destPath), written, len(records), gotHash)
	s.notifyConfigured("file_updated", "Patris edge upload received", fmt.Sprintf("%s uploaded %s (%d records)", sourceID, filepath.Base(originalName), len(records)))
	s.broadcastInitialSnapshot("source_changed")

	writeJSON(w, edgeUploadResponse{
		Success:  true,
		File:     filepath.Base(originalName),
		Path:     destPath,
		SourceID: sourceID,
		Hash:     gotHash,
		Size:     written,
		Records:  len(records),
		Message:  "edge upload accepted",
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
	dbPath := s.currentDBPath()
	if filecopy.IsURL(dbPath) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"file":      sourceBaseName(dbPath),
			"path":      dbPath,
			"remote":    true,
			"count":     0,
			"in_use":    false,
			"processes": []processmon.ProcessInfo{},
		})
		return
	}

	fileInfo, err := processmon.FindProcessesWithFile(dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find processes with file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"file":      sourceBaseName(dbPath),
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
	result, err := s.RecordResult()
	if err != nil {
		log.Printf("Failed to read records: %v", err)
		return
	}
	records := result.Rows
	dbPath := s.currentDBPath()

	message := s.initialSnapshotMessage(result, dbPath, "")

	connMu.Lock()
	err = conn.WriteJSON(message)
	connMu.Unlock()

	if err != nil {
		log.Printf("Failed to send to WebSocket: %v", err)
		return
	}

	// Client connections are observational and must not replace a watcher-owned
	// change baseline.
	revision := ""
	if result.Contract != nil {
		revision = result.Contract.Source.Revision
	}
	s.seedLastSnapshot(records, revision)

	log.Printf("📤 Sent initial %d records to client", len(records))
	go s.broadcastProcessInfo()
}

func (s *Server) initialSnapshotMessage(result recordpipe.Result, dbPath, reason string) map[string]interface{} {
	message := map[string]interface{}{
		"type":        "initial",
		"timestamp":   time.Now().Format(time.RFC3339),
		"added":       result.Rows,
		"total_count": len(result.Rows),
		"file_name":   sourceBaseName(dbPath),
		"file_path":   dbPath,
		"version":     s.version,
		"resources":   web.Resources(),
		"raw":         result.Raw,
		"key_field":   result.KeyField,
	}
	if reason != "" {
		message["reason"] = reason
		message["source_changed"] = true
	}
	if s.config != nil {
		message["config"] = browserConfig(s.config.Get())
	}
	if result.Contract != nil {
		message["contract"] = result.SyncEnvelope(nil)
	}
	return message
}

func (s *Server) broadcastInitialSnapshot(reason string) {
	result, err := s.RecordResult()
	if err != nil {
		log.Printf("Failed to read records for initial snapshot: %v", err)
		return
	}
	records := result.Rows
	dbPath := s.currentDBPath()
	message := s.initialSnapshotMessage(result, dbPath, reason)

	s.lastRecordsMu.Lock()
	s.lastRecords = records
	s.lastRecordsReady = true
	if result.Contract != nil {
		s.lastContractRevision = result.Contract.Source.Revision
	} else {
		s.lastContractRevision = ""
	}
	s.lastRecordsMu.Unlock()

	s.wsClientsMu.RLock()
	for conn, connMu := range s.wsClients {
		go func(c *websocket.Conn, mu *sync.Mutex) {
			mu.Lock()
			err := c.WriteJSON(message)
			mu.Unlock()
			if err != nil {
				log.Printf("Failed to send source snapshot to WebSocket: %v", err)
			}
		}(conn, connMu)
	}
	s.wsClientsMu.RUnlock()

	log.Printf("📤 Broadcast initial snapshot (%s): %d records from %s", reason, len(records), sourceBaseName(dbPath))
	s.dispatchUpdateEvent(updateout.Event{
		Type:             "initial",
		Timestamp:        fmt.Sprintf("%v", message["timestamp"]),
		Source:           dbPath,
		Raw:              result.Raw,
		Records:          records,
		KeyField:         result.KeyField,
		Contract:         result.SyncEnvelope(nil),
		SnapshotContract: result.SyncEnvelope(nil),
	})
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
	result, err := s.RecordResult()
	if err != nil {
		log.Printf("Failed to read records: %v", err)
		return
	}
	records := result.Rows

	// Compute changes
	changeSet, contractChanged := s.updateRecordBaseline(result)
	changeSet.Raw = result.Raw
	changes := changeSet.Map()
	if contract := result.SyncEnvelope(&changeSet); contract != nil {
		changes["contract"] = contract
	}

	// Log what we're sending
	added, modified, deleted := changeSet.Counts()
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
	if added+modified+deleted > 0 || contractChanged {
		if contractChanged && added+modified+deleted == 0 {
			log.Printf("Catalog structure changed without product-row changes")
		}
		s.notifyConfigured("row_updated", "Patris rows changed", s.rowChangeMessage(added, modified, deleted, changes))
		s.dispatchUpdateEvent(updateout.Event{
			Type:             "update",
			Timestamp:        changeSet.Timestamp,
			Source:           s.currentDBPath(),
			Raw:              result.Raw,
			Records:          records,
			Changes:          &changeSet,
			KeyField:         result.KeyField,
			Contract:         result.SyncEnvelope(&changeSet),
			SnapshotContract: result.SyncEnvelope(nil),
		})
	}
	go s.broadcastProcessInfo()
}

func (s *Server) dispatchInitialUpdate(ctx context.Context) {
	cfg := s.Config().SendUpdates
	if !cfg.Enabled || !cfg.Initial {
		return
	}
	result, err := s.RecordResultContext(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		log.Printf("Failed to prepare initial send update payload: %v", err)
		return
	}
	s.dispatchUpdateEvent(updateout.Event{
		Type:             "initial",
		Timestamp:        time.Now().Format(time.RFC3339),
		Source:           s.currentDBPath(),
		Raw:              result.Raw,
		Records:          result.Rows,
		KeyField:         result.KeyField,
		Contract:         result.SyncEnvelope(nil),
		SnapshotContract: result.SyncEnvelope(nil),
	})
}

func (s *Server) dispatchInitialUpdateAsync() {
	cfg := s.Config()
	if !cfg.SendUpdates.Enabled || !cfg.SendUpdates.Initial {
		return
	}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		ctx, cancel := context.WithTimeout(s.backgroundCtx, canonicalRequestTimeout(cfg))
		defer cancel()
		s.dispatchInitialUpdate(ctx)
	}()
}

func (s *Server) dispatchUpdateEvent(event updateout.Event) {
	cfg := s.Config().SendUpdates
	if !cfg.Enabled {
		return
	}
	if event.Type == "initial" && !cfg.Initial {
		return
	}
	go func() {
		result, err := updateout.DispatchWithResult(context.Background(), cfg, event)
		if err != nil {
			log.Printf("Failed to send update event: %v", err)
			return
		}
		if result.Status != "" {
			log.Printf("Sent update event: %s", result.DiagnosticSummary())
		}
	}()
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

func (s *Server) executableManifest(r *http.Request) (updater.ExecutableManifest, error) {
	manifest, _, err := s.executableManifestForPath(r)
	return manifest, err
}

func (s *Server) executableManifestForPath(r *http.Request) (updater.ExecutableManifest, string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return updater.ExecutableManifest{}, "", err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	info, err := os.Stat(exePath)
	if err != nil {
		return updater.ExecutableManifest{}, "", err
	}
	hash, err := updater.FileSHA256(exePath)
	if err != nil {
		return updater.ExecutableManifest{}, "", err
	}
	v := s.version
	if v.Version == "" {
		v = version.Current()
	}
	manifest := updater.ExecutableManifest{
		Name:     "Patris Export",
		Filename: filepath.Base(exePath),
		Version: updater.VersionShape{
			Version:   v.Version,
			BuildDate: v.BuildDate,
			Commit:    v.Commit,
			GoVersion: v.GoVersion,
			Platform:  v.Platform,
		},
		Platform:     updater.CurrentPlatform(),
		Size:         info.Size(),
		SHA256:       hash,
		LastModified: info.ModTime().UTC(),
		DownloadURL:  absoluteURL(r, "/api/update/executable"),
		GeneratedAt:  time.Now().UTC(),
	}
	return manifest, exePath, nil
}

func (s *Server) sourceFileManifest(r *http.Request) (sourceFileManifest, string, error) {
	sourcePath := s.currentDBPath()
	if filecopy.IsURL(sourcePath) {
		return sourceFileManifest{}, "", fmt.Errorf("current source is remote URL-backed and cannot be served as a local static file")
	}
	if resolved, err := filepath.EvalSymlinks(sourcePath); err == nil {
		sourcePath = resolved
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return sourceFileManifest{}, "", err
	}
	if info.IsDir() {
		return sourceFileManifest{}, "", fmt.Errorf("current source is a directory: %s", sourcePath)
	}
	hash, err := updater.FileSHA256(sourcePath)
	if err != nil {
		return sourceFileManifest{}, "", err
	}
	manifest := sourceFileManifest{
		Name:         "Patris Export Source Database",
		Filename:     filepath.Base(sourcePath),
		Path:         sourcePath,
		Size:         info.Size(),
		SHA256:       hash,
		LastModified: info.ModTime().UTC(),
		DownloadURL:  absoluteURL(r, "/api/source/file"),
		GeneratedAt:  time.Now().UTC(),
	}
	return manifest, sourcePath, nil
}

func absoluteURL(r *http.Request, path string) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: path}).String()
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
	dbPath := s.currentDBPath()
	if filecopy.IsURL(dbPath) {
		return "", ""
	}
	hash, err := filecopy.CalculateHash(dbPath)
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
	sourceEpoch := s.excelPricingRemote.fenceSourceChange()
	oldHash, newHash := s.updateSourceHash()
	message := fmt.Sprintf("%s changed", sourceBaseName(path))
	if oldHash != "" || newHash != "" {
		message = fmt.Sprintf("%s changed (%s -> %s)", sourceBaseName(path), shortHash(oldHash), shortHash(newHash))
	}
	s.notifyExcelPricingSourceChanged(newHash)
	s.excelPricingRemote.commitSourceChange(sourceEpoch)
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

func writeJSONStatus(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) edgeUploadAuthorized(r *http.Request) bool {
	token := ""
	if s.config != nil {
		token = s.config.Get().Edge.Token
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("PATRIS_EXPORT_EDGE_TOKEN"))
	}
	if token == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Patris-Edge-Token"))
	if got == "" {
		return bearerTokenAuthorized(r, token)
	}
	return secureTokenEqual(got, token)
}

func bearerTokenAuthorized(r *http.Request, token string) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	parts := strings.Fields(strings.TrimSpace(values[0]))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return secureTokenEqual(parts[1], token)
}

func secureTokenEqual(got, want string) bool {
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "edge"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	cleaned := strings.Trim(b.String(), ".-")
	if cleaned == "" {
		return "edge"
	}
	if len(cleaned) > 80 {
		cleaned = cleaned[:80]
	}
	return cleaned
}

func (s *Server) processStatus() map[string]interface{} {
	patris81Processes, patrisErr := findPatrisProcessesWithTimeout(1500 * time.Millisecond)
	var fileInfo *processmon.FileAccessInfo
	var fileErr error
	dbPath := s.currentDBPath()
	if !filecopy.IsURL(dbPath) {
		fileInfo, fileErr = findFileProcessesWithTimeout(dbPath, 1500*time.Millisecond)
	}

	status := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"patris81": map[string]interface{}{
			"running":   len(patris81Processes) > 0,
			"count":     len(patris81Processes),
			"processes": patris81Processes,
		},
		"file_access": map[string]interface{}{
			"file":      sourceBaseName(dbPath),
			"path":      dbPath,
			"remote":    filecopy.IsURL(dbPath),
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	processes, err := processmon.FindProcessByNameContext(ctx, "patris81.exe")
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("timed out inspecting patris81.exe processes: %w", ctx.Err())
	}
	return processes, err
}

func findFileProcessesWithTimeout(path string, timeout time.Duration) (*processmon.FileAccessInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	info, err := processmon.FindProcessesWithFileContext(ctx, path)
	if err != nil && ctx.Err() != nil {
		return &processmon.FileAccessInfo{
			FilePath:  path,
			Processes: []processmon.ProcessInfo{},
		}, fmt.Errorf("timed out inspecting file access: %w", ctx.Err())
	}
	return info, err
}

func (s *Server) broadcastConfig(cfg appconfig.Config) {
	message := map[string]interface{}{
		"type":      "config_update",
		"timestamp": time.Now().Format(time.RFC3339),
		"config":    browserConfig(cfg),
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
	return s.computeChangesByKey(newRecords, "Code")
}

func (s *Server) computeChangesByKey(newRecords []map[string]interface{}, keyField string) map[string]interface{} {
	return s.computeChangeSetByKey(newRecords, keyField).Map()
}

func (s *Server) computeChangeSetByKey(newRecords []map[string]interface{}, keyField string) recorddiff.ChangeSet {
	changes := recorddiff.Between(s.lastRecords, newRecords, keyField, time.Now())
	if !s.lastRecordsReady {
		log.Printf("🆕 First load: all %d records are new", len(newRecords))
		return changes
	}
	s.logDetailedChanges(changes.Added, changes.Deleted, changes.Modified)
	return changes
}

func (s *Server) seedLastRecords(records []map[string]interface{}) {
	s.seedLastSnapshot(records, "")
}

func (s *Server) seedLastSnapshot(records []map[string]interface{}, contractRevision string) {
	s.lastRecordsMu.Lock()
	defer s.lastRecordsMu.Unlock()
	if s.lastRecordsReady {
		return
	}
	s.lastRecords = recordmap.CopyRows(records)
	s.lastRecordsReady = true
	s.lastContractRevision = contractRevision
}

func (s *Server) updateRecordBaseline(result recordpipe.Result) (recorddiff.ChangeSet, bool) {
	s.lastRecordsMu.Lock()
	defer s.lastRecordsMu.Unlock()

	currentRevision := ""
	if result.Contract != nil {
		currentRevision = result.Contract.Source.Revision
	}
	contractChanged := s.lastRecordsReady && currentRevision != s.lastContractRevision
	changeSet := result.FilterChanges(s.computeChangeSetByKey(result.Rows, result.KeyField))
	s.lastRecords = recordmap.CopyRows(result.Rows)
	s.lastRecordsReady = true
	s.lastContractRevision = currentRevision
	return changeSet, contractChanged
}

// logDetailedChanges logs detailed information about what changed
func (s *Server) logDetailedChanges(added []map[string]interface{}, deleted []string, modified []RecordChange) {
	// Get file timestamps
	s.lastModTimeMu.Lock()
	lastModTime := s.lastModTime
	s.lastModTimeMu.Unlock()

	dbPath := s.currentDBPath()
	fileInfo, err := os.Stat(dbPath)
	var currentModTime time.Time
	if err == nil {
		currentModTime = fileInfo.ModTime()
		s.lastModTimeMu.Lock()
		s.lastModTime = currentModTime
		s.lastModTimeMu.Unlock()
	}

	// Log file timestamps
	log.Println(strings.Repeat("━", 80))
	log.Printf("📁 File: %s", filepath.Base(dbPath))
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
	dbPath := s.currentDBPath()

	if filecopy.IsURL(dbPath) {
		pollInterval := debounceDuration
		if pollInterval <= 0 {
			pollInterval = 5 * time.Minute
		}
		if err := fw.Poll(dbPath, func(path string) {
			log.Printf("🔄 Remote source changed: %s", path)
			s.notifyFileUpdated(path)
			s.broadcastUpdate()
		}, pollInterval); err != nil {
			return fmt.Errorf("failed to poll URL: %w", err)
		}
		log.Printf("👀 Polling remote source: %s (interval: %v)", dbPath, pollInterval)
		s.dispatchInitialUpdateAsync()
		return nil
	}

	if err := fw.Watch(dbPath, func(path string) {
		log.Printf("🔄 File changed: %s", filepath.Base(path))
		s.notifyFileUpdated(path)
		s.broadcastUpdate()
	}, debounceDuration); err != nil {
		return fmt.Errorf("failed to watch file: %w", err)
	}

	fw.Start()
	ext := filepath.Ext(dbPath)
	fileType := "database"
	if ext == ".json" {
		fileType = "JSON"
	}
	log.Printf("👀 Watching %s file: %s", fileType, filepath.Base(dbPath))

	s.dispatchInitialUpdateAsync()
	return nil
}

// Close cleans up server resources
func (s *Server) Close() error {
	var firstErr error
	if s.backgroundCancel != nil {
		s.backgroundCancel()
	}
	s.backgroundWG.Wait()
	s.serviceWG.Wait()
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
	s.dataSourceMu.Lock()
	ds := s.dataSource
	s.dataSource = nil
	s.dataSourceMu.Unlock()
	if ds != nil {
		if err := ds.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	log.Printf("🚀 Starting server on %s", addr)
	dbPath := s.currentDBPath()
	log.Printf("📊 Serving file: %s", filepath.Base(dbPath))

	if !filecopy.IsURL(dbPath) {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", dbPath)
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
