package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

func newLifecycleTestEngine(t *testing.T) *Engine {
	t.Helper()
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
