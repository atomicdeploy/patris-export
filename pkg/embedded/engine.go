package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/ipc"
	"github.com/atomicdeploy/patris-export/pkg/server"
	"github.com/atomicdeploy/patris-export/pkg/version"
)

type Options struct {
	ConfigFiles     []string     `json:"config_files,omitempty"`
	DatabasePath    string       `json:"database_path,omitempty"`
	CharmapPath     string       `json:"charmap_path,omitempty"`
	TempDir         string       `json:"temp_dir,omitempty"`
	DirectAccess    bool         `json:"direct_access,omitempty"`
	DirectAccessSet bool         `json:"direct_access_set,omitempty"`
	Watch           bool         `json:"watch,omitempty"`
	WatchSet        bool         `json:"watch_set,omitempty"`
	Debounce        string       `json:"debounce,omitempty"`
	Version         version.Info `json:"version,omitempty"`
}

type Engine struct {
	mu        sync.Mutex
	closed    bool
	cfg       appconfig.Config
	manager   *appconfig.Manager
	server    *server.Server
	http      *http.Server
	httpDone  chan struct{}
	ipc       *ipc.Server
	ipcCancel context.CancelFunc
	ipcDone   chan struct{}
}

var errEngineClosed = errors.New("embedded engine is closed")

func New(options Options) (*Engine, error) {
	manager, err := appconfig.LoadFiles(options.ConfigFiles)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	cfg := manager.Get()
	appconfig.ApplyEnv(&cfg)
	if options.DatabasePath != "" {
		cfg.Database.Path = options.DatabasePath
	}
	if options.CharmapPath != "" {
		cfg.Database.Charmap = options.CharmapPath
	}
	if options.TempDir != "" {
		cfg.Runtime.TempDir = options.TempDir
	}
	if options.DirectAccessSet {
		cfg.Database.DirectAccess = options.DirectAccess
	}
	if options.WatchSet {
		cfg.Server.Watch = options.Watch
	}
	if options.Debounce != "" {
		cfg.Server.Debounce = options.Debounce
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return nil, errors.New("database path is required")
	}

	filecopy.SetTempDir(appconfig.ResolveTempDir(cfg.Runtime.TempDir))
	filecopy.SetTempPolicy(cfg.Runtime.TempStrategy, appconfig.TempMemoryLimitBytes(cfg.Runtime.TempMemoryLimitMB))
	converter.SetRTLConversion(cfg.Database.RTLConversion)

	var charMap converter.CharMapping
	if cfg.Database.Charmap != "" {
		charMap, err = converter.LoadCharMapping(cfg.Database.Charmap)
		if err != nil {
			return nil, fmt.Errorf("load charmap: %w", err)
		}
		converter.SetDefaultMapping(charMap)
	}

	info := options.Version
	if info.Version == "" {
		info = version.Current()
	}
	srv, err := server.NewServerWithOptions(cfg.Database.Path, charMap, server.Options{
		Config:  manager,
		Version: info,
	}, !cfg.Database.DirectAccess)
	if err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	e := &Engine{
		cfg:     cfg,
		manager: manager,
		server:  srv,
	}
	if cfg.Server.Watch {
		if err := e.StartWatch(cfg.Server.Debounce); err != nil {
			_ = e.Close()
			return nil, err
		}
	}
	return e, nil
}

func (e *Engine) Server() *server.Server {
	return e.server
}

func (e *Engine) ConfigManager() *appconfig.Manager {
	return e.manager
}

func (e *Engine) StartWatch(debounce string) error {
	if strings.TrimSpace(debounce) == "" {
		debounce = "0s"
	}
	duration, err := time.ParseDuration(debounce)
	if err != nil {
		return fmt.Errorf("invalid debounce duration %q: %w", debounce, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEngineClosed
	}
	return e.server.StartWatching(duration)
}

func (e *Engine) StartHTTP(addr string) error {
	if strings.TrimSpace(addr) == "" {
		addr = e.cfg.Addr()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEngineClosed
	}
	if e.http != nil {
		return errors.New("HTTP server already started")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen for embedded HTTP on %s: %w", addr, err)
	}
	httpServer := &http.Server{Addr: listener.Addr().String(), Handler: e.server.Router()}
	done := make(chan struct{})
	e.http = httpServer
	e.httpDone = done
	go func() {
		defer close(done)
		log.Printf("Embedded HTTP listening on %s", listener.Addr())
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Embedded HTTP server stopped with error: %v", err)
		}
	}()
	return nil
}

func (e *Engine) StartIPC(path string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", errEngineClosed
	}
	if e.ipc != nil {
		return "", errors.New("IPC server already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ipcServer := ipc.NewServer(path, e)
	listener, err := ipcServer.Listen()
	if err != nil {
		cancel()
		return "", fmt.Errorf("listen for embedded IPC on %s: %w", ipcServer.Path(), err)
	}
	done := make(chan struct{})
	e.ipcCancel = cancel
	e.ipc = ipcServer
	e.ipcDone = done
	go func() {
		defer close(done)
		if err := ipcServer.ServeListener(ctx, listener); err != nil {
			log.Printf("IPC server stopped with error: %v", err)
		}
	}()
	return ipcServer.Path(), nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.ipcCancel != nil {
		e.ipcCancel()
		e.ipcCancel = nil
	}

	var firstErr error
	if e.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := e.http.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
		e.http = nil
	}
	if err := waitForStop(e.httpDone, "HTTP"); err != nil && firstErr == nil {
		firstErr = err
	}
	e.httpDone = nil
	if err := waitForStop(e.ipcDone, "IPC"); err != nil && firstErr == nil {
		firstErr = err
	}
	e.ipcDone = nil
	e.ipc = nil
	if e.server != nil {
		if err := e.server.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func waitForStop(done <-chan struct{}, name string) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("timed out waiting for embedded %s server to stop", name)
	}
}

func (e *Engine) CallJSON(ctx context.Context, raw string) (string, error) {
	var req ipc.Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return "", err
	}
	resp := e.Call(ctx, req)
	data, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *Engine) Call(ctx context.Context, req ipc.Request) ipc.Response {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ipc.Response{ID: req.ID, OK: false, Error: errEngineClosed.Error()}
	}
	return NewServerHandler(e.server).Call(ctx, req)
}

func (e *Engine) Subscribe(ctx context.Context, buffer int) (<-chan map[string]interface{}, func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		ch := make(chan map[string]interface{})
		close(ch)
		return ch, func() {}
	}
	return e.server.SubscribeEvents(buffer)
}

var supportedMethods = []string{
	"app.get",
	"records.list",
	"info.get",
	"status.get",
	"config.get",
	"config.set",
	"toast.show",
	"refresh",
}

// SupportedMethods returns the canonical request names shared by direct C ABI
// calls and local IPC clients. Aliases accepted by the dispatcher are omitted.
func SupportedMethods() []string {
	return append([]string(nil), supportedMethods...)
}

type ServerHandler struct {
	server *server.Server
}

func NewServerHandler(srv *server.Server) *ServerHandler {
	return &ServerHandler{server: srv}
}

func (h *ServerHandler) Call(ctx context.Context, req ipc.Request) ipc.Response {
	method := strings.ToLower(strings.TrimSpace(req.Method))
	result, err := callServer(ctx, h.server, method, req.Params)
	if err != nil {
		return ipc.Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	return ipc.Response{ID: req.ID, OK: true, Result: result}
}

func (h *ServerHandler) Subscribe(ctx context.Context, buffer int) (<-chan map[string]interface{}, func()) {
	return h.server.SubscribeEvents(buffer)
}

func callServer(ctx context.Context, srv *server.Server, method string, params json.RawMessage) (interface{}, error) {
	if srv == nil {
		return nil, errors.New("server is not initialized")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	switch method {
	case "app.get", "app":
		return srv.AppMetadata(), nil
	case "records.list", "records.get", "records":
		return srv.RecordsPayload()
	case "info.get", "info":
		return srv.Info()
	case "status.get", "status":
		return srv.Status(), nil
	case "config.get", "config":
		return srv.Config(), nil
	case "config.set":
		cfg, err := decodeConfigParams(params)
		if err != nil {
			return nil, err
		}
		return srv.ReplaceConfig(cfg)
	case "toast.show", "toast":
		var req server.ToastRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("decode toast params: %w", err)
		}
		req.Source = "ipc"
		if err := srv.ShowToast(req); err != nil {
			return map[string]interface{}{"native_error": err.Error()}, nil
		}
		return map[string]interface{}{"success": true}, nil
	case "refresh":
		srv.Refresh()
		return map[string]interface{}{"refreshed": true}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func decodeConfigParams(params json.RawMessage) (appconfig.Config, error) {
	var cfg appconfig.Config
	if len(params) == 0 || string(params) == "null" {
		return cfg, errors.New("config params are required")
	}
	if err := json.Unmarshal(params, &cfg); err == nil && cfg.SchemaVersion != 0 {
		return cfg, nil
	}
	var wrapped struct {
		Config appconfig.Config `json:"config"`
	}
	if err := json.Unmarshal(params, &wrapped); err != nil {
		return cfg, fmt.Errorf("decode config params: %w", err)
	}
	if wrapped.Config.SchemaVersion == 0 {
		return cfg, errors.New("config params must contain a config object")
	}
	return wrapped.Config, nil
}
