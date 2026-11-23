package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/ipc"
)

func TestCallJSONRejectsUnknownMethod(t *testing.T) {
	e := &Engine{}
	raw, err := e.CallJSON(context.Background(), `{"id":1,"method":"missing"}`)
	if err != nil {
		t.Fatalf("CallJSON returned error: %v", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("expected failed response, got %#v", resp)
	}
}

func TestStartHTTPRejectsOccupiedAddressSynchronously(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	defer listener.Close()

	engine := newLifecycleTestEngine(t)
	defer engine.Close()
	if err := engine.StartHTTP(listener.Addr().String()); err == nil {
		t.Fatal("StartHTTP succeeded even though the address was already occupied")
	}
}

func TestStartHTTPCanCloseImmediately(t *testing.T) {
	engine := newLifecycleTestEngine(t)
	if err := engine.StartHTTP("127.0.0.1:0"); err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second Close was not idempotent: %v", err)
	}
	if err := engine.StartHTTP("127.0.0.1:0"); !errors.Is(err, errEngineClosed) {
		t.Fatalf("StartHTTP after close returned %v, want %v", err, errEngineClosed)
	}
}

func TestStartIPCCanCloseImmediately(t *testing.T) {
	engine := newLifecycleTestEngine(t)
	path := filepath.Join(t.TempDir(), "patris-export.sock")
	if runtime.GOOS == "windows" {
		path = fmt.Sprintf("patris-export-test-%d", time.Now().UnixNano())
	}
	actual, err := engine.StartIPC(path)
	if err != nil {
		t.Fatalf("StartIPC: %v", err)
	}
	if actual == "" {
		t.Fatal("StartIPC returned an empty endpoint")
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := engine.StartIPC(path); !errors.Is(err, errEngineClosed) {
		t.Fatalf("StartIPC after close returned %v, want %v", err, errEngineClosed)
	}
}

func TestCallAfterCloseReturnsStructuredError(t *testing.T) {
	engine := newLifecycleTestEngine(t)
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	response := engine.Call(context.Background(), ipcRequest("status.get"))
	if response.OK || response.Error != errEngineClosed.Error() {
		t.Fatalf("unexpected response after close: %+v", response)
	}
}

func TestNewKeepsServerConfigAlignedWithEffectiveOptions(t *testing.T) {
	engine := newLifecycleTestEngine(t)
	defer engine.Close()
	if engine.Server().Config().Server.Watch {
		t.Fatal("embedded server config ignored the validated Watch=false option")
	}
}

func TestNewDoesNotPersistEnvironmentSecrets(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_SEND_UPDATES", "false")
	t.Setenv("PATRIS_EXPORT_SEND_URL", "")
	t.Setenv("PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV", "")
	t.Setenv("PATRIS_EXPORT_SEND_COMMAND", "")
	t.Setenv("PATRIS_EXPORT_MYSQL_DSN", "mysql://embedded-test-secret@invalid/db")
	t.Setenv("PATRIS_EXPORT_EDGE_TOKEN", "embedded-test-edge-secret")
	databasePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "kala.db"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	original := []byte("{}\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{ConfigFiles: []string{configPath}, DatabasePath: databasePath, Watch: false, WatchSet: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("embedded startup persisted process-local configuration: %s", after)
	}
	reloadedBase := []byte("{\"ui\":{\"language\":\"fa\"},\"server\":{\"watch\":true}}\n")
	if err := os.WriteFile(configPath, reloadedBase, 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.ConfigManager().Reload(); err != nil {
		t.Fatal(err)
	}
	effective := engine.Server().Config()
	if effective.Database.Path != databasePath || effective.Server.Watch || effective.UI.Language != "fa" ||
		effective.Export.MySQLDSN != "mysql://embedded-test-secret@invalid/db" || effective.Edge.Token != "embedded-test-edge-secret" {
		t.Fatalf("runtime overlay was lost after reload: %+v", effective)
	}
	effective.UI.Language = "en"
	if _, err := engine.Server().ReplaceConfig(effective); err != nil {
		t.Fatalf("embedded config.set equivalent was rejected: %v", err)
	}
	after, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(after, []byte("embedded-test-secret")) {
		t.Fatalf("reload persisted a process-local secret: %s", after)
	}
	if err := engine.ConfigManager().Reload(); err != nil {
		t.Fatal(err)
	}
	reloaded := engine.Server().Config()
	if reloaded.UI.Language != "en" || reloaded.Database.Path != databasePath || reloaded.Server.Watch ||
		reloaded.Export.MySQLDSN != "mysql://embedded-test-secret@invalid/db" {
		t.Fatalf("embedded config update or runtime overlay was lost: %+v", reloaded)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsCanonicalDeliveryWithoutWatch(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_SEND_UPDATES", "true")
	t.Setenv("PATRIS_EXPORT_SEND_URL", "https://receiver.invalid/product-sync")
	t.Setenv("PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV", "PATRIS_EMBEDDED_TEST_SECRET")
	databasePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "kala.db"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	encoded, err := json.Marshal(map[string]interface{}{
		"database": map[string]interface{}{"path": databasePath},
		"server":   map[string]interface{}{"watch": false},
		"canonical": map[string]interface{}{
			"enabled": true,
		},
		"send_updates": map[string]interface{}{
			"enabled":                 true,
			"url":                     "https://receiver.invalid/product-sync",
			"product_sync_secret_env": "PATRIS_EMBEDDED_TEST_SECRET",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if engine, err := New(Options{ConfigFiles: []string{configPath}}); err == nil {
		_ = engine.Close()
		t.Fatal("embedded engine accepted canonical delivery with watching disabled")
	}
}

func newLifecycleTestEngine(t *testing.T) *Engine {
	t.Helper()
	t.Setenv("PATRIS_EXPORT_SEND_UPDATES", "false")
	t.Setenv("PATRIS_EXPORT_SEND_URL", "")
	t.Setenv("PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV", "")
	t.Setenv("PATRIS_EXPORT_SEND_COMMAND", "")
	databasePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "kala.db"))
	if err != nil {
		t.Fatalf("resolve test database: %v", err)
	}
	engine, err := New(Options{
		ConfigFiles:  []string{filepath.Join(t.TempDir(), "config.json")},
		DatabasePath: databasePath,
		Watch:        false,
		WatchSet:     true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func ipcRequest(method string) ipc.Request {
	return ipc.Request{ID: 1, Method: method}
}
