package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var errInvalidHandle = errors.New("invalid handle")

// engineInstance is the subset of the embedded engine used by the stable C
// boundary. Keeping the registry independent of the concrete engine also makes
// its close/call ordering testable without crossing cgo in unit tests.
type engineInstance interface {
	CallJSON(context.Context, string) (string, error)
	StartHTTP(string) error
	StartIPC(string) (string, error)
	Close() error
}

type handleEntry struct {
	mu     sync.Mutex
	engine engineInstance
	closed bool
}

type handleRegistry struct {
	next    atomic.Uint64
	mu      sync.RWMutex
	handles map[uint64]*handleEntry
}

func newHandleRegistry() *handleRegistry {
	return &handleRegistry{handles: make(map[uint64]*handleEntry)}
}

func (r *handleRegistry) add(engine engineInstance) uint64 {
	handle := r.next.Add(1)
	r.mu.Lock()
	r.handles[handle] = &handleEntry{engine: engine}
	r.mu.Unlock()
	return handle
}

// with serializes every operation for a handle. An entry may be looked up just
// before Close removes it, so the entry-local closed flag is checked again
// after acquiring its lock.
func (r *handleRegistry) with(handle uint64, fn func(engineInstance) error) error {
	r.mu.RLock()
	entry := r.handles[handle]
	r.mu.RUnlock()
	if entry == nil {
		return errInvalidHandle
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || entry.engine == nil {
		return errInvalidHandle
	}
	return fn(entry.engine)
}

// close removes the handle before waiting for an in-flight operation. New
// callers therefore fail immediately, while the engine is closed only after
// the operation that already owns the entry lock has completed.
func (r *handleRegistry) close(handle uint64) error {
	r.mu.Lock()
	entry := r.handles[handle]
	if entry != nil {
		delete(r.handles, handle)
	}
	r.mu.Unlock()
	if entry == nil {
		return errInvalidHandle
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || entry.engine == nil {
		return errInvalidHandle
	}
	entry.closed = true
	engine := entry.engine
	entry.engine = nil
	return engine.Close()
}
