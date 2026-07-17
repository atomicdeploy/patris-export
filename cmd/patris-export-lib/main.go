package main

/*
#include <stdlib.h>
#include <stdint.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"unsafe"

	"github.com/atomicdeploy/patris-export/pkg/embedded"
	"github.com/atomicdeploy/patris-export/pkg/licensing"
	"github.com/atomicdeploy/patris-export/pkg/version"
)

var (
	engineHandles = newHandleRegistry()
	lastErrMu     sync.Mutex
	lastErr       string
)

const patrisExportABIVersion uint32 = 1

type abiStringCapabilities struct {
	Encoding     string `json:"encoding"`
	Ownership    string `json:"ownership"`
	FreeFunction string `json:"free_function"`
}

type abiThreadingCapabilities struct {
	HandleCalls    string `json:"handle_calls"`
	Close          string `json:"close"`
	LastError      string `json:"last_error"`
	EngineSettings string `json:"engine_settings"`
}

type abiLicensingCapabilities struct {
	Mode     string `json:"mode"`
	Required bool   `json:"required"`
}

type abiCapabilities struct {
	Name       string                   `json:"name"`
	ABIVersion uint32                   `json:"abi_version"`
	Product    version.Info             `json:"product"`
	RPCMethods []string                 `json:"rpc_methods"`
	Transports []string                 `json:"transports"`
	Strings    abiStringCapabilities    `json:"strings"`
	Threading  abiThreadingCapabilities `json:"threading"`
	Licensing  abiLicensingCapabilities `json:"licensing"`
}

func main() {}

//export PatrisExportVersionJSON
func PatrisExportVersionJSON() (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportVersionJSON", func() { result = nil })
	data, err := json.Marshal(version.Current())
	if err != nil {
		setLastError(fmt.Errorf("encode version: %w", err))
		return C.CString(`{"error":"failed to encode version"}`)
	}
	return C.CString(string(data))
}

//export PatrisExportABIVersion
func PatrisExportABIVersion() (result C.uint32_t) {
	beginABICall()
	defer recoverABI("PatrisExportABIVersion", func() { result = 0 })
	return C.uint32_t(patrisExportABIVersion)
}

//export PatrisExportCapabilitiesJSON
func PatrisExportCapabilitiesJSON() (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportCapabilitiesJSON", func() { result = nil })
	data, err := capabilitiesJSON()
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(string(data))
}

//export PatrisExportCreate
func PatrisExportCreate(optionsJSON *C.char) (result C.uint64_t) {
	beginABICall()
	defer recoverABI("PatrisExportCreate", func() { result = 0 })
	if err := licensing.Enforce(context.Background()); err != nil {
		setLastError(err)
		return 0
	}
	var options embedded.Options
	if optionsJSON != nil {
		raw := C.GoString(optionsJSON)
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &options); err != nil {
				setLastError(fmt.Errorf("decode options: %w", err))
				return 0
			}
		}
	}
	engine, err := embedded.New(options)
	if err != nil {
		setLastError(err)
		return 0
	}
	handle := engineHandles.add(engine)
	return C.uint64_t(handle)
}

//export PatrisExportLicenseStatusJSON
func PatrisExportLicenseStatusJSON() (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportLicenseStatusJSON", func() { result = nil })
	data, err := json.Marshal(licensing.CurrentStatus(context.Background()))
	if err != nil {
		setLastError(fmt.Errorf("encode license status: %w", err))
		return nil
	}
	return C.CString(string(data))
}

//export PatrisExportLicenseChallenge
func PatrisExportLicenseChallenge() (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportLicenseChallenge", func() { result = nil })
	challenge, err := licensing.Challenge(context.Background())
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(challenge)
}

//export PatrisExportLicenseInstall
func PatrisExportLicenseInstall(key *C.char) (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportLicenseInstall", func() { result = nil })
	if key == nil {
		setLastError(fmt.Errorf("license key is required"))
		return nil
	}
	status, err := licensing.Install(context.Background(), C.GoString(key))
	if err != nil {
		setLastError(err)
		return nil
	}
	data, err := json.Marshal(status)
	if err != nil {
		setLastError(fmt.Errorf("encode license status: %w", err))
		return nil
	}
	return C.CString(string(data))
}

//export PatrisExportLicenseRemove
func PatrisExportLicenseRemove() (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportLicenseRemove", func() { result = nil })
	status, err := licensing.Remove(context.Background())
	if err != nil {
		setLastError(err)
		return nil
	}
	data, err := json.Marshal(status)
	if err != nil {
		setLastError(fmt.Errorf("encode license status: %w", err))
		return nil
	}
	return C.CString(string(data))
}

//export PatrisExportClose
func PatrisExportClose(handle C.uint64_t) (result C.int) {
	beginABICall()
	defer recoverABI("PatrisExportClose", func() { result = 0 })
	if err := engineHandles.close(uint64(handle)); err != nil {
		setLastError(err)
		return 0
	}
	return 1
}

//export PatrisExportCall
func PatrisExportCall(handle C.uint64_t, requestJSON *C.char) (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportCall", func() { result = nil })
	if requestJSON == nil {
		setLastError(fmt.Errorf("request JSON is required"))
		return nil
	}
	request := C.GoString(requestJSON)
	var response string
	err := engineHandles.with(uint64(handle), func(engine engineInstance) error {
		var err error
		response, err = engine.CallJSON(context.Background(), request)
		return err
	})
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(response)
}

//export PatrisExportStartHTTP
func PatrisExportStartHTTP(handle C.uint64_t, addr *C.char) (result C.int) {
	beginABICall()
	defer recoverABI("PatrisExportStartHTTP", func() { result = 0 })
	value := ""
	if addr != nil {
		value = C.GoString(addr)
	}
	if err := engineHandles.with(uint64(handle), func(engine engineInstance) error {
		return engine.StartHTTP(value)
	}); err != nil {
		setLastError(err)
		return 0
	}
	return 1
}

//export PatrisExportStartIPC
func PatrisExportStartIPC(handle C.uint64_t, path *C.char) (result *C.char) {
	beginABICall()
	defer recoverABI("PatrisExportStartIPC", func() { result = nil })
	value := ""
	if path != nil {
		value = C.GoString(path)
	}
	var actualPath string
	err := engineHandles.with(uint64(handle), func(engine engineInstance) error {
		var err error
		actualPath, err = engine.StartIPC(value)
		return err
	})
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(actualPath)
}

//export PatrisExportLastError
func PatrisExportLastError() (result *C.char) {
	defer recoverABI("PatrisExportLastError", func() { result = nil })
	return C.CString(lastErrorSnapshot())
}

//export PatrisExportFreeString
func PatrisExportFreeString(value *C.char) {
	defer recoverABI("PatrisExportFreeString", nil)
	if value != nil {
		C.free(unsafe.Pointer(value))
	}
}

func capabilitiesJSON() ([]byte, error) {
	return json.Marshal(abiCapabilities{
		Name:       "patris-export-c",
		ABIVersion: patrisExportABIVersion,
		Product:    version.Current(),
		RPCMethods: embedded.SupportedMethods(),
		Transports: []string{"direct", "http", "ipc"},
		Strings: abiStringCapabilities{
			Encoding:     "utf-8",
			Ownership:    "caller",
			FreeFunction: "PatrisExportFreeString",
		},
		Threading: abiThreadingCapabilities{
			HandleCalls:    "serialized",
			Close:          "waits-for-in-flight-handle-call",
			LastError:      "process-global-snapshot",
			EngineSettings: "process-global; use one active engine per process",
		},
		Licensing: abiLicensingCapabilities{
			Mode:     licensing.BuildMode(),
			Required: licensing.Required(),
		},
	})
}

func beginABICall() {
	setLastError(nil)
}

func recoverABI(operation string, resetResult func()) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if resetResult != nil {
		resetResult()
	}
	err := fmt.Errorf("%s panic: %v", operation, recovered)
	setLastError(err)
	log.Printf("Patris Export C ABI recovered a panic: %v\n%s", err, debug.Stack())
}

func lastErrorSnapshot() string {
	lastErrMu.Lock()
	defer lastErrMu.Unlock()
	return lastErr
}

func setLastError(err error) {
	lastErrMu.Lock()
	defer lastErrMu.Unlock()
	if err == nil {
		lastErr = ""
		return
	}
	lastErr = err.Error()
}
