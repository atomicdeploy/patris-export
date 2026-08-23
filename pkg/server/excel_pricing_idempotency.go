package server

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const excelPricingMutationRetention = 15 * time.Minute

type excelPricingMutationEntry struct {
	fingerprint string
	status      int
	body        []byte
	completed   bool
	updatedAt   time.Time
}

type excelPricingPreviewBinding struct {
	fingerprint string
	updatedAt   time.Time
}

type excelPricingMutationLedger struct {
	mu       sync.Mutex
	now      func() time.Time
	entries  map[string]*excelPricingMutationEntry
	previews map[string]excelPricingPreviewBinding
}

type excelPricingMutationBeginResult int

const (
	excelPricingMutationBeginNew excelPricingMutationBeginResult = iota
	excelPricingMutationBeginReplay
	excelPricingMutationBeginRunning
	excelPricingMutationBeginConflict
)

func newExcelPricingMutationLedger(now func() time.Time) *excelPricingMutationLedger {
	if now == nil {
		now = time.Now
	}
	return &excelPricingMutationLedger{
		now:      now,
		entries:  make(map[string]*excelPricingMutationEntry),
		previews: make(map[string]excelPricingPreviewBinding),
	}
}

func (ledger *excelPricingMutationLedger) begin(
	operation, idempotencyKey, fingerprint string,
) (excelPricingMutationBeginResult, int, []byte) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	now := ledger.now().UTC()
	ledger.pruneLocked(now)
	key := excelPricingMutationLedgerKey(operation, idempotencyKey)
	if entry := ledger.entries[key]; entry != nil {
		if entry.fingerprint != fingerprint {
			return excelPricingMutationBeginConflict, 0, nil
		}
		if entry.completed {
			return excelPricingMutationBeginReplay, entry.status, append([]byte(nil), entry.body...)
		}
		return excelPricingMutationBeginRunning, 0, nil
	}
	ledger.entries[key] = &excelPricingMutationEntry{
		fingerprint: fingerprint,
		updatedAt:   now,
	}
	return excelPricingMutationBeginNew, 0, nil
}

func (ledger *excelPricingMutationLedger) abort(operation, idempotencyKey, fingerprint string) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	key := excelPricingMutationLedgerKey(operation, idempotencyKey)
	entry := ledger.entries[key]
	if entry != nil && !entry.completed && entry.fingerprint == fingerprint {
		delete(ledger.entries, key)
	}
}

func (ledger *excelPricingMutationLedger) complete(
	operation, idempotencyKey, fingerprint string,
	status int,
	body []byte,
) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	key := excelPricingMutationLedgerKey(operation, idempotencyKey)
	entry := ledger.entries[key]
	if entry == nil || entry.fingerprint != fingerprint || entry.completed {
		return
	}
	entry.status = status
	entry.body = append([]byte(nil), body...)
	entry.completed = true
	entry.updatedAt = ledger.now().UTC()
}

func (ledger *excelPricingMutationLedger) bindPreview(digest, fingerprint string) bool {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	now := ledger.now().UTC()
	ledger.pruneLocked(now)
	if binding, exists := ledger.previews[digest]; exists && binding.fingerprint != fingerprint {
		return false
	}
	ledger.previews[digest] = excelPricingPreviewBinding{
		fingerprint: fingerprint,
		updatedAt:   now,
	}
	return true
}

func (ledger *excelPricingMutationLedger) previewMatch(digest, fingerprint string) (bool, bool) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	now := ledger.now().UTC()
	ledger.pruneLocked(now)
	binding, exists := ledger.previews[digest]
	return exists, exists && binding.fingerprint == fingerprint
}

func (ledger *excelPricingMutationLedger) pruneLocked(now time.Time) {
	for key, entry := range ledger.entries {
		if entry == nil || now.Sub(entry.updatedAt) > excelPricingMutationRetention {
			delete(ledger.entries, key)
		}
	}
	for digest, binding := range ledger.previews {
		if now.Sub(binding.updatedAt) > excelPricingMutationRetention {
			delete(ledger.previews, digest)
		}
	}
}

func excelPricingMutationLedgerKey(operation, idempotencyKey string) string {
	return operation + "|" + idempotencyKey
}

func excelPricingMutationFingerprint(request excelPricingLocalRequest) string {
	body, _ := json.Marshal(request)
	return excelPricingSnapshotDigest(body)
}

func excelPricingPreviewBindingFingerprint(request excelPricingLocalRequest) string {
	body, _ := json.Marshal(struct {
		ExpectedStateRevision string                `json:"expected_state_revision"`
		Settings              *excelPricingSettings `json:"settings"`
		ProductChanges        json.RawMessage       `json:"product_changes"`
	}{request.ExpectedStateRevision, request.Settings, request.ProductChanges})
	return excelPricingSnapshotDigest(body)
}

func excelPricingPreviewDigest(body []byte) (string, bool, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return "", false, errors.New("preview response is invalid")
	}
	raw, exists := root["preview_digest"]
	if !exists {
		if rawData, hasData := root["data"]; hasData {
			var data map[string]json.RawMessage
			if json.Unmarshal(rawData, &data) != nil || data == nil {
				return "", false, errors.New("preview response data is invalid")
			}
			raw, exists = data["preview_digest"]
		}
	}
	if !exists {
		return "", false, nil
	}
	var digest string
	if json.Unmarshal(raw, &digest) != nil || !isSHA256Revision(digest) {
		return "", true, errors.New("preview digest is invalid")
	}
	return digest, true, nil
}
