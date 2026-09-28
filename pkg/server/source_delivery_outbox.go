package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const (
	sourceDeliveryOutboxSchema   = "patris.source-delivery-outbox/v1"
	sourceDeliveryOutboxMaxBytes = 36 << 20
	sourceDeliveryRetryMaximum   = 5 * time.Minute
)

var errSourceDeliveryOutbox = errors.New("source_delivery_outbox_unavailable")

type sourceDeliveryOutboxEntry struct {
	Event          updateout.Event `json:"event"`
	PreparedAt     time.Time       `json:"prepared_at"`
	QueuedAt       time.Time       `json:"queued_at"`
	Attempts       int             `json:"attempts"`
	ProbeAttempts  int             `json:"probe_attempts,omitempty"`
	State          string          `json:"state"`
	FailureCode    string          `json:"failure_code,omitempty"`
	OutcomeUnknown bool            `json:"outcome_unknown,omitempty"`
}

type sourceDeliveryOutboxState struct {
	Schema         string                     `json:"schema"`
	DestinationKey string                     `json:"destination_key,omitempty"`
	Active         *sourceDeliveryOutboxEntry `json:"active,omitempty"`
	Latest         *sourceDeliveryOutboxEntry `json:"latest,omitempty"`
}

type sourceDeliveryOutbox struct {
	mu        sync.Mutex
	server    *Server
	path      string
	state     sourceDeliveryOutboxState
	wake      chan struct{}
	now       func() time.Time
	retryBase time.Duration
	deliver   func(appconfig.Config, updateout.Event, time.Time, time.Time) (updateout.DeliveryResult, string, error)
	probe     func(context.Context, appconfig.Config, *canonical.Envelope) (sourceDeliveryReceiptProbe, error)
}

func newSourceDeliveryOutbox(server *Server, path string) (*sourceDeliveryOutbox, error) {
	path = strings.TrimSpace(path)
	if server == nil || path == "" {
		return nil, errSourceDeliveryOutbox
	}
	outbox := &sourceDeliveryOutbox{
		server:    server,
		path:      path,
		state:     sourceDeliveryOutboxState{Schema: sourceDeliveryOutboxSchema},
		wake:      make(chan struct{}, 1),
		now:       time.Now,
		retryBase: time.Second,
		deliver:   server.dispatchUpdateEventNow,
		probe:     probeSourceDeliveryReceipt,
	}
	if err := outbox.load(); err != nil {
		return nil, err
	}
	return outbox, nil
}

func canonicalSourceDeliveryEvent(event updateout.Event) bool {
	return event.SnapshotContract != nil && event.SnapshotContract.Schema == canonical.ContractName
}

func (outbox *sourceDeliveryOutbox) enqueue(cfg appconfig.Config, event updateout.Event, preparedAt, queuedAt time.Time) error {
	if updateout.Normalize(cfg.SendUpdates).Mode == "full" && event.SnapshotContract != nil {
		// Bind persistence and receipt reconciliation to the exact contract that
		// the configured transport will select.
		event.Contract = event.SnapshotContract
	}
	entry, err := sourceDeliveryOutboxEntryForEvent(event, preparedAt, queuedAt)
	if err != nil {
		return err
	}
	key := sourceDeliveryDestinationKey(cfg)
	if key == "" {
		return errSourceDeliveryOutbox
	}

	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if outbox.state.Active == nil || outbox.state.DestinationKey != key {
		outbox.state = sourceDeliveryOutboxState{
			Schema:         sourceDeliveryOutboxSchema,
			DestinationKey: key,
			Active:         entry,
		}
	} else if sourceDeliveryEntryID(outbox.state.Active) == sourceDeliveryEntryID(entry) {
		return nil
	} else {
		latest, latestErr := sourceDeliverySnapshotFallback(event, preparedAt, queuedAt)
		if latestErr != nil {
			return latestErr
		}
		outbox.state.Latest = latest
	}
	if err := outbox.persistLocked(); err != nil {
		// Retain the candidate in memory and wake the worker. The caller also
		// receives the error and therefore must not advance its watcher baseline.
		// If storage recovers without another source notification, the worker can
		// still durably persist and deliver this exact event.
		outbox.signal()
		return err
	}
	outbox.signal()
	return nil
}

func sourceDeliveryOutboxEntryForEvent(event updateout.Event, preparedAt, queuedAt time.Time) (*sourceDeliveryOutboxEntry, error) {
	if event.Type != "initial" && event.Type != "update" {
		return nil, errSourceDeliveryOutbox
	}
	if event.Contract == nil || event.SnapshotContract == nil {
		return nil, errSourceDeliveryOutbox
	}
	if err := validateSourceDeliveryContracts(event.Contract, event.SnapshotContract); err != nil {
		return nil, err
	}
	event.Raw = false
	event.Records = nil
	event.Changes = nil
	if event.Timestamp == "" {
		event.Timestamp = queuedAt.UTC().Format(time.RFC3339Nano)
	}
	return &sourceDeliveryOutboxEntry{
		Event:      event,
		PreparedAt: preparedAt.UTC(),
		QueuedAt:   queuedAt.UTC(),
		State:      "pending",
	}, nil
}

func sourceDeliverySnapshotFallback(event updateout.Event, preparedAt, queuedAt time.Time) (*sourceDeliveryOutboxEntry, error) {
	snapshot := event.SnapshotContract
	fallback := updateout.Event{
		Type:             "initial",
		Timestamp:        queuedAt.UTC().Format(time.RFC3339Nano),
		Source:           event.Source,
		KeyField:         event.KeyField,
		Contract:         snapshot,
		SnapshotContract: snapshot,
	}
	return sourceDeliveryOutboxEntryForEvent(fallback, preparedAt, queuedAt)
}

func validateSourceDeliveryContracts(selected, snapshot *canonical.Envelope) error {
	if selected == nil || snapshot == nil || selected.Schema != canonical.ContractName || snapshot.Schema != canonical.ContractName {
		return errSourceDeliveryOutbox
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) > canonical.MaxSnapshotBytes {
		return errSourceDeliveryOutbox
	}
	verified, _, err := canonical.VerifySnapshotJSON(encoded)
	if err != nil || !verified.Source.SameIdentity(snapshot.Source) || verified.EventID != snapshot.EventID {
		return errSourceDeliveryOutbox
	}
	if selected.EventID == "" || !selected.Source.SameIdentity(snapshot.Source) {
		return errSourceDeliveryOutbox
	}
	if selected.EventType != "snapshot" && selected.EventType != "update" {
		return errSourceDeliveryOutbox
	}
	return nil
}

func sourceDeliveryDestinationKey(cfg appconfig.Config) string {
	delivery := updateout.Normalize(cfg.SendUpdates)
	if !delivery.Enabled || delivery.URL == "" || delivery.ProductSyncSecretEnv == "" || len(delivery.Command) != 0 {
		return ""
	}
	material, err := json.Marshal([]any{
		delivery.URL,
		delivery.Method,
		delivery.Format,
		delivery.Mode,
		delivery.ProductSyncSecretEnv,
		delivery.Headers,
		cfg.Canonical,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:])
}

func sourceDeliveryEntryID(entry *sourceDeliveryOutboxEntry) string {
	if entry == nil || entry.Event.Contract == nil {
		return ""
	}
	return entry.Event.Contract.EventID
}

func (outbox *sourceDeliveryOutbox) start(ctx context.Context, group *sync.WaitGroup) {
	if outbox == nil || group == nil {
		return
	}
	group.Add(1)
	go func() {
		defer group.Done()
		outbox.run(ctx)
	}()
	outbox.mu.Lock()
	pending := outbox.state.Active != nil
	outbox.mu.Unlock()
	if pending {
		outbox.signal()
	}
}

func (outbox *sourceDeliveryOutbox) run(ctx context.Context) {
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-outbox.wake:
		case <-retry:
		}
		retry = nil
		for {
			pending, delay := outbox.processOnce()
			if !pending {
				break
			}
			if delay > 0 {
				timer := time.NewTimer(delay)
				retry = timer.C
				break
			}
		}
	}
}

func (outbox *sourceDeliveryOutbox) processOnce() (bool, time.Duration) {
	outbox.mu.Lock()
	if outbox.state.Active == nil {
		outbox.mu.Unlock()
		return false, 0
	}
	cfg := outbox.server.Config()
	if sourceDeliveryDestinationKey(cfg) != outbox.state.DestinationKey {
		outbox.state.Active.State = "configuration_changed"
		outbox.state.Active.FailureCode = "configuration_changed"
		_ = outbox.persistLocked()
		outbox.mu.Unlock()
		return true, sourceDeliveryRetryMaximum
	}
	entry := *outbox.state.Active
	if sourceDeliveryEntryRequiresProbe(entry) {
		entry.ProbeAttempts++
		entry.State = "probing_receipt"
		entry.FailureCode = ""
		*outbox.state.Active = entry
		if err := outbox.persistLocked(); err != nil {
			outbox.mu.Unlock()
			return true, outbox.retryDelay(entry.ProbeAttempts)
		}
		outbox.mu.Unlock()

		ctx := outbox.server.backgroundCtx
		if ctx == nil {
			ctx = context.Background()
		}
		probe, probeErr := outbox.probe(ctx, cfg, entry.Event.Contract)

		outbox.mu.Lock()
		if outbox.state.Active == nil || sourceDeliveryEntryID(outbox.state.Active) != sourceDeliveryEntryID(&entry) {
			pending := outbox.state.Active != nil
			outbox.mu.Unlock()
			return pending, 0
		}
		active := outbox.state.Active
		if probeErr != nil {
			active.State = "receipt_probe_inconclusive"
			active.OutcomeUnknown = true
			active.FailureCode = "receipt_probe_failed"
			_ = outbox.persistLocked()
			attempts := active.ProbeAttempts
			outbox.mu.Unlock()
			return true, outbox.retryDelay(attempts)
		}
		switch probe.Status {
		case sourceDeliveryReceiptApplied, sourceDeliveryReceiptSuperseded:
			pending, persistErr := outbox.completeActiveLocked(entry, probe.Source)
			outbox.mu.Unlock()
			if persistErr != nil {
				return true, outbox.retryDelay(entry.ProbeAttempts)
			}
			return pending, 0
		case sourceDeliveryReceiptPending:
			active.State = "receipt_pending"
			active.OutcomeUnknown = false
			active.FailureCode = ""
			_ = outbox.persistLocked()
			attempts := active.ProbeAttempts
			outbox.mu.Unlock()
			return true, outbox.retryDelay(attempts)
		case sourceDeliveryReceiptNotFound:
			// The authoritative receiver has proved that this exact event was not
			// accepted. One write attempt is now safe; another uncertain outcome
			// returns to receipt probing before any further write.
			active.State = "pending"
			active.OutcomeUnknown = false
			active.FailureCode = ""
			if err := outbox.persistLocked(); err != nil {
				outbox.mu.Unlock()
				return true, outbox.retryDelay(active.ProbeAttempts)
			}
			entry = *active
			outbox.mu.Unlock()
		default:
			active.State = "receipt_probe_inconclusive"
			active.OutcomeUnknown = true
			active.FailureCode = "receipt_probe_invalid"
			_ = outbox.persistLocked()
			attempts := active.ProbeAttempts
			outbox.mu.Unlock()
			return true, outbox.retryDelay(attempts)
		}
	} else {
		outbox.mu.Unlock()
	}

	outbox.mu.Lock()
	if outbox.state.Active == nil || sourceDeliveryEntryID(outbox.state.Active) != sourceDeliveryEntryID(&entry) {
		pending := outbox.state.Active != nil
		outbox.mu.Unlock()
		return pending, 0
	}
	entry.Attempts++
	entry.State = "attempting"
	entry.OutcomeUnknown = true
	entry.FailureCode = ""
	*outbox.state.Active = entry
	if err := outbox.persistLocked(); err != nil {
		outbox.mu.Unlock()
		return true, outbox.retryDelay(entry.Attempts)
	}
	outbox.mu.Unlock()

	// The outbox owns retry policy. Disable the transport's legacy in-call
	// retries so an ambiguous first attempt can never be repeated before the
	// authoritative receipt probe runs.
	deliveryConfig := cfg
	deliveryConfig.SendUpdates.RetryAttempts = 1
	result, terminalCode, dispatchErr := outbox.deliver(deliveryConfig, entry.Event, entry.PreparedAt, entry.QueuedAt)
	success := dispatchErr == nil && terminalCode == "receipt_received"
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if outbox.state.Active == nil || sourceDeliveryEntryID(outbox.state.Active) != sourceDeliveryEntryID(&entry) {
		return outbox.state.Active != nil, 0
	}
	if success {
		completedSource := entry.Event.Contract.Source
		if result.Delivery != nil {
			completedSource = result.Delivery.Source
		}
		pending, err := outbox.completeActiveLocked(entry, completedSource)
		if err != nil {
			return true, outbox.retryDelay(entry.Attempts)
		}
		return pending, 0
	}

	active := outbox.state.Active
	active.State = "pending"
	active.OutcomeUnknown = terminalCode != "delivery_failed"
	if result.OutcomeUnknown != nil && *result.OutcomeUnknown {
		active.OutcomeUnknown = true
	}
	if active.OutcomeUnknown {
		active.State = "outcome_unknown"
	}
	active.FailureCode = safeOutboxFailureCode(result.FailureCode, terminalCode)
	if persistErr := outbox.persistLocked(); persistErr != nil {
		log.Printf("Failed to persist source delivery recovery state")
	}
	return true, outbox.retryDelay(active.Attempts)
}

func sourceDeliveryEntryRequiresProbe(entry sourceDeliveryOutboxEntry) bool {
	if entry.OutcomeUnknown {
		return true
	}
	switch entry.State {
	case "attempting", "outcome_unknown", "probing_receipt", "receipt_pending", "receipt_probe_inconclusive":
		return true
	default:
		return false
	}
}

func (outbox *sourceDeliveryOutbox) completeActiveLocked(entry sourceDeliveryOutboxEntry, completedSource canonical.Source) (bool, error) {
	if outbox.state.Active == nil || sourceDeliveryEntryID(outbox.state.Active) != sourceDeliveryEntryID(&entry) {
		return outbox.state.Active != nil, nil
	}
	outbox.state.Active = outbox.state.Latest
	outbox.state.Latest = nil
	if outbox.state.Active != nil && outbox.state.Active.Event.SnapshotContract != nil && outbox.state.Active.Event.SnapshotContract.Source.SameIdentity(completedSource) {
		outbox.state.Active = nil
	}
	if outbox.state.Active == nil {
		outbox.state.DestinationKey = ""
	}
	if err := outbox.persistLocked(); err != nil {
		return true, err
	}
	return outbox.state.Active != nil, nil
}

func (outbox *sourceDeliveryOutbox) retryDelay(attempt int) time.Duration {
	base := outbox.retryBase
	if base <= 0 {
		base = time.Second
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 8 {
		shift = 8
	}
	delay := base * time.Duration(1<<shift)
	if delay > sourceDeliveryRetryMaximum {
		return sourceDeliveryRetryMaximum
	}
	return delay
}

func safeOutboxFailureCode(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 96 {
			continue
		}
		valid := true
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
				valid = false
				break
			}
		}
		if valid {
			return value
		}
	}
	return "delivery_failed"
}

func (outbox *sourceDeliveryOutbox) signal() {
	select {
	case outbox.wake <- struct{}{}:
	default:
	}
}

func (outbox *sourceDeliveryOutbox) status() map[string]interface{} {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	result := map[string]interface{}{
		"pending":   outbox.state.Active != nil,
		"coalesced": outbox.state.Latest != nil,
	}
	if active := outbox.state.Active; active != nil {
		result["state"] = active.State
		result["attempts"] = active.Attempts
		result["probe_attempts"] = active.ProbeAttempts
		result["failure_code"] = active.FailureCode
		result["outcome_unknown"] = active.OutcomeUnknown
		if active.Event.Contract != nil {
			result["event_id"] = active.Event.Contract.EventID
			result["source"] = active.Event.Contract.Source
		}
	}
	return result
}

func (outbox *sourceDeliveryOutbox) load() error {
	info, err := os.Lstat(outbox.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > sourceDeliveryOutboxMaxBytes {
		return errSourceDeliveryOutbox
	}
	file, err := os.Open(outbox.path)
	if err != nil {
		return errSourceDeliveryOutbox
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, sourceDeliveryOutboxMaxBytes+1))
	if err != nil || len(data) > sourceDeliveryOutboxMaxBytes {
		return errSourceDeliveryOutbox
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state sourceDeliveryOutboxState
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || validateSourceDeliveryOutboxState(state) != nil {
		return errSourceDeliveryOutbox
	}
	outbox.state = state
	return nil
}

func validateSourceDeliveryOutboxState(state sourceDeliveryOutboxState) error {
	if state.Schema != sourceDeliveryOutboxSchema {
		return errSourceDeliveryOutbox
	}
	if state.Active == nil {
		if state.Latest != nil || state.DestinationKey != "" {
			return errSourceDeliveryOutbox
		}
		return nil
	}
	if len(state.DestinationKey) != sha256.Size*2 {
		return errSourceDeliveryOutbox
	}
	if _, err := hex.DecodeString(state.DestinationKey); err != nil {
		return errSourceDeliveryOutbox
	}
	for _, entry := range []*sourceDeliveryOutboxEntry{state.Active, state.Latest} {
		if entry == nil {
			continue
		}
		if entry.Attempts < 0 || entry.ProbeAttempts < 0 || entry.PreparedAt.IsZero() || entry.QueuedAt.IsZero() || entry.QueuedAt.Before(entry.PreparedAt) {
			return errSourceDeliveryOutbox
		}
		switch entry.State {
		case "pending", "attempting", "outcome_unknown", "configuration_changed", "probing_receipt", "receipt_pending", "receipt_probe_inconclusive":
		default:
			return errSourceDeliveryOutbox
		}
		if _, err := sourceDeliveryOutboxEntryForEvent(entry.Event, entry.PreparedAt, entry.QueuedAt); err != nil {
			return errSourceDeliveryOutbox
		}
	}
	return nil
}

func (outbox *sourceDeliveryOutbox) persistLocked() error {
	outbox.state.Schema = sourceDeliveryOutboxSchema
	data, err := json.Marshal(outbox.state)
	if err != nil || len(data) > sourceDeliveryOutboxMaxBytes {
		return errSourceDeliveryOutbox
	}
	directory := filepath.Dir(outbox.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errSourceDeliveryOutbox
	}
	temporary, err := os.CreateTemp(directory, ".source-delivery-outbox-*.tmp")
	if err != nil {
		return errSourceDeliveryOutbox
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return errSourceDeliveryOutbox
	}
	if n, err := temporary.Write(data); err != nil || n != len(data) {
		_ = temporary.Close()
		return errSourceDeliveryOutbox
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errSourceDeliveryOutbox
	}
	if err := temporary.Close(); err != nil {
		return errSourceDeliveryOutbox
	}
	if err := replaceSourceDeliveryOutboxFile(temporaryPath, outbox.path); err != nil {
		return fmt.Errorf("%w: replace", errSourceDeliveryOutbox)
	}
	removeTemporary = false
	return nil
}
