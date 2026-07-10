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
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/atomicdeploy/patris-export/pkg/embedded"
	"github.com/atomicdeploy/patris-export/pkg/version"
)

var (
	nextHandle uint64
	handlesMu  sync.RWMutex
	handles    = map[uint64]*embedded.Engine{}
	lastErrMu  sync.Mutex
	lastErr    string
)

func main() {}

//export PatrisExportVersionJSON
func PatrisExportVersionJSON() *C.char {
	data, err := json.Marshal(version.Current())
	if err != nil {
		return C.CString(`{"error":"failed to encode version"}`)
	}
	return C.CString(string(data))
}

//export PatrisExportCreate
func PatrisExportCreate(optionsJSON *C.char) C.uint64_t {
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
	handle := atomic.AddUint64(&nextHandle, 1)
	handlesMu.Lock()
	handles[handle] = engine
	handlesMu.Unlock()
	return C.uint64_t(handle)
}

//export PatrisExportClose
func PatrisExportClose(handle C.uint64_t) C.int {
	engine := takeHandle(uint64(handle))
	if engine == nil {
		setLastError(fmt.Errorf("invalid handle"))
		return 0
	}
	if err := engine.Close(); err != nil {
		setLastError(err)
		return 0
	}
	return 1
}

//export PatrisExportCall
func PatrisExportCall(handle C.uint64_t, requestJSON *C.char) *C.char {
	engine := getHandle(uint64(handle))
	if engine == nil {
		setLastError(fmt.Errorf("invalid handle"))
		return nil
	}
	if requestJSON == nil {
		setLastError(fmt.Errorf("request JSON is required"))
		return nil
	}
	response, err := engine.CallJSON(context.Background(), C.GoString(requestJSON))
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(response)
}

//export PatrisExportStartHTTP
func PatrisExportStartHTTP(handle C.uint64_t, addr *C.char) C.int {
	engine := getHandle(uint64(handle))
	if engine == nil {
		setLastError(fmt.Errorf("invalid handle"))
		return 0
	}
	value := ""
	if addr != nil {
		value = C.GoString(addr)
	}
	if err := engine.StartHTTP(value); err != nil {
		setLastError(err)
		return 0
	}
	return 1
}

//export PatrisExportStartIPC
func PatrisExportStartIPC(handle C.uint64_t, path *C.char) *C.char {
	engine := getHandle(uint64(handle))
	if engine == nil {
		setLastError(fmt.Errorf("invalid handle"))
		return nil
	}
	value := ""
	if path != nil {
		value = C.GoString(path)
	}
	actualPath, err := engine.StartIPC(value)
	if err != nil {
		setLastError(err)
		return nil
	}
	return C.CString(actualPath)
}

//export PatrisExportLastError
func PatrisExportLastError() *C.char {
	lastErrMu.Lock()
	defer lastErrMu.Unlock()
	return C.CString(lastErr)
}

//export PatrisExportFreeString
func PatrisExportFreeString(value *C.char) {
	if value != nil {
		C.free(unsafe.Pointer(value))
	}
}

func getHandle(handle uint64) *embedded.Engine {
	handlesMu.RLock()
	defer handlesMu.RUnlock()
	return handles[handle]
}

func takeHandle(handle uint64) *embedded.Engine {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	engine := handles[handle]
	delete(handles, handle)
	return engine
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
