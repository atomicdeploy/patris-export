package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEngine struct {
	closeCalls atomic.Int32
}

func (*fakeEngine) CallJSON(context.Context, string) (string, error) { return `{}`, nil }
func (*fakeEngine) StartHTTP(string) error                           { return nil }
func (*fakeEngine) StartIPC(string) (string, error)                  { return "test", nil }
func (e *fakeEngine) Close() error {
	e.closeCalls.Add(1)
	return nil
}

func TestHandleRegistrySerializesCalls(t *testing.T) {
	registry := newHandleRegistry()
	handle := registry.add(&fakeEngine{})
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- registry.with(handle, func(engineInstance) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- registry.with(handle, func(engineInstance) error {
			close(secondEntered)
			return nil
		})
	}()
	<-secondStarted
	select {
	case <-secondEntered:
		t.Fatal("second operation entered while the first operation still owned the handle")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first operation failed: %v", err)
	}
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second operation did not enter after the first operation completed")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second operation failed: %v", err)
	}
}

func TestHandleRegistryCloseWaitsForInFlightCall(t *testing.T) {
	registry := newHandleRegistry()
	engine := &fakeEngine{}
	handle := registry.add(engine)
	callEntered := make(chan struct{})
	releaseCall := make(chan struct{})
	callDone := make(chan error, 1)
	go func() {
		callDone <- registry.with(handle, func(engineInstance) error {
			close(callEntered)
			<-releaseCall
			return nil
		})
	}()
	<-callEntered

	closeDone := make(chan error, 1)
	go func() { closeDone <- registry.close(handle) }()
	waitForHandleRemoval(t, registry, handle)
	if got := engine.closeCalls.Load(); got != 0 {
		t.Fatalf("engine closed before its in-flight call completed: %d", got)
	}
	if err := registry.with(handle, func(engineInstance) error { return nil }); !errors.Is(err, errInvalidHandle) {
		t.Fatalf("new call after close began returned %v, want invalid handle", err)
	}

	close(releaseCall)
	if err := <-callDone; err != nil {
		t.Fatalf("in-flight call failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if got := engine.closeCalls.Load(); got != 1 {
		t.Fatalf("engine close calls = %d, want 1", got)
	}
	if err := registry.close(handle); !errors.Is(err, errInvalidHandle) {
		t.Fatalf("second close returned %v, want invalid handle", err)
	}
}

func waitForHandleRemoval(t *testing.T, registry *handleRegistry, handle uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mu.RLock()
		_, exists := registry.handles[handle]
		registry.mu.RUnlock()
		if !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("close did not remove the handle before waiting for its active call")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBeginABICallClearsStaleError(t *testing.T) {
	setLastError(errors.New("stale"))
	beginABICall()
	if got := lastErrorSnapshot(); got != "" {
		t.Fatalf("last error was not cleared: %q", got)
	}
}

func TestRecoverABIContainsPanic(t *testing.T) {
	setLastError(nil)
	reset := false
	func() {
		defer recoverABI("test operation", func() { reset = true })
		panic("boom")
	}()
	if !reset {
		t.Fatal("panic recovery did not reset the ABI result")
	}
	if got := lastErrorSnapshot(); !strings.Contains(got, "test operation panic: boom") {
		t.Fatalf("panic was not recorded in last error: %q", got)
	}
}

func TestCapabilitiesJSONDescribesStableBoundary(t *testing.T) {
	data, err := capabilitiesJSON()
	if err != nil {
		t.Fatalf("capabilitiesJSON returned error: %v", err)
	}
	var got abiCapabilities
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("capabilities are not valid JSON: %v", err)
	}
	if got.Name != "patris-export-c" || got.ABIVersion != patrisExportABIVersion {
		t.Fatalf("unexpected ABI identity: %+v", got)
	}
	if got.Strings.FreeFunction != "PatrisExportFreeString" || got.Threading.HandleCalls != "serialized" {
		t.Fatalf("missing ownership/threading contract: %+v", got)
	}
	if len(got.RPCMethods) == 0 || len(got.Transports) == 0 {
		t.Fatalf("capabilities omit methods or transports: %+v", got)
	}
	if got.Licensing.Mode == "" {
		t.Fatalf("capabilities omit licensing mode: %+v", got)
	}
}
