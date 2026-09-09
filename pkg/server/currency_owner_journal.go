package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const currencyJournalSchema = "patris.currency-owner-intent/v1"
const currencyJournalLimit = 64 << 10

var currencyJournalID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errCurrencyJournal = errors.New("currency_journal_unavailable")

// An immutable local admission, saved before any remote submission. It contains
// only the original caller intent and its owner origin, never credentials.
type currencyIntentRecord struct {
	Schema      string                       `json:"schema"`
	JobID       string                       `json:"job_id"`
	Sequence    uint64                       `json:"sequence"`
	CreatedAt   time.Time                    `json:"created_at"`
	OwnerOrigin string                       `json:"owner_origin"`
	Request     excelPricingWritebackRequest `json:"request"`
}
type currencyTerminalRecord struct {
	Schema     string    `json:"schema"`
	JobID      string    `json:"job_id"`
	Status     string    `json:"status"`
	FinishedAt time.Time `json:"finished_at"`
}
type currencyJournalEntry struct {
	record   currencyIntentRecord
	terminal *currencyTerminalRecord
}

// Journal methods are called during construction or with the queue lock held.
func (queue *excelPricingWritebackQueue) currencyJournalOrigin() (string, error) {
	if queue.server == nil || queue.server.config == nil {
		return "", nil
	}
	u, e := url.Parse(queue.server.Config().SendUpdates.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errCurrencyJournal
	}
	return "https://" + u.Host, nil
}

func (queue *excelPricingWritebackQueue) readCurrencyJournal() ([]currencyJournalEntry, error) {
	if queue.currencyJournalDir == "" {
		return nil, nil
	}
	info, e := os.Lstat(queue.currencyJournalDir)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errCurrencyJournal
	}
	files, e := os.ReadDir(queue.currencyJournalDir)
	if e != nil || len(files) > 2*excelPricingWritebackMaxJobs {
		return nil, errCurrencyJournal
	}
	origin, e := queue.currencyJournalOrigin()
	if e != nil {
		return nil, e
	}
	entries := map[string]*currencyJournalEntry{}
	markers := map[string]*currencyTerminalRecord{}
	sequences := map[uint64]bool{}
	requests := map[string]bool{}
	for _, file := range files {
		name := file.Name()
		suffix := ".json"
		terminal := strings.HasSuffix(name, ".terminal.json")
		if terminal {
			suffix = ".terminal.json"
		}
		id := strings.TrimSuffix(name, suffix)
		if !strings.HasSuffix(name, suffix) || !currencyJournalID.MatchString(id) || file.Type()&os.ModeSymlink != 0 || file.IsDir() {
			return nil, errCurrencyJournal
		}
		path := filepath.Join(queue.currencyJournalDir, name)
		if terminal {
			var m currencyTerminalRecord
			if readCurrencyJournalJSON(path, &m) != nil || m.Schema != currencyJournalSchema || m.JobID != id || (m.Status != "confirmed" && m.Status != "superseded") || m.FinishedAt.IsZero() {
				return nil, errCurrencyJournal
			}
			markers[id] = &m
		} else {
			var r currencyIntentRecord
			if readCurrencyJournalJSON(path, &r) != nil || r.JobID != id || r.OwnerOrigin != origin || validateCurrencyRecord(r) != nil || sequences[r.Sequence] || requests[r.Request.RequestID] {
				return nil, errCurrencyJournal
			}
			sequences[r.Sequence] = true
			requests[r.Request.RequestID] = true
			entries[id] = &currencyJournalEntry{record: r}
		}
	}
	for id, m := range markers {
		entry := entries[id]
		if entry == nil || m.FinishedAt.Before(entry.record.CreatedAt) {
			return nil, errCurrencyJournal
		}
		entry.terminal = m
	}
	if len(entries) > excelPricingWritebackMaxJobs {
		return nil, errCurrencyJournal
	}
	result := make([]currencyJournalEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].record.Sequence < result[j].record.Sequence })
	return result, nil
}

func validateCurrencyRecord(r currencyIntentRecord) error {
	request := r.Request
	if r.Schema != currencyJournalSchema || !currencyJournalID.MatchString(r.JobID) || r.Sequence == 0 || r.CreatedAt.IsZero() || (request.Schema != excelPricingWritebackRequestSchema && request.Schema != excelPricingWritebackBatchRequestSchema) || !excelPricingIdempotencyPattern.MatchString(request.RequestID) || !isSHA256Revision(request.ExpectedStateRevision) || validateExcelPricingSettings(request.Settings) != nil {
		return errCurrencyJournal
	}
	keys, _, _, e := normalizeExcelPricingWritebackIntent(request)
	if e != nil {
		return errCurrencyJournal
	}
	for _, key := range keys {
		switch key {
		case "yuan_price", "dollar_price", "cny_effective_date", "usd_effective_date":
		default:
			return errCurrencyJournal
		}
	}
	return nil
}

func readCurrencyJournalJSON(path string, out any) error {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > currencyJournalLimit {
		return errCurrencyJournal
	}
	f, e := os.Open(path)
	if e != nil {
		return errCurrencyJournal
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, currencyJournalLimit+1))
	if e != nil || len(data) > currencyJournalLimit {
		return errCurrencyJournal
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errCurrencyJournal
	}
	return nil
}

func (queue *excelPricingWritebackQueue) loadCurrencyJournal() error {
	entries, e := queue.readCurrencyJournal()
	if e != nil {
		return e
	}
	// No partial restore when even one journal is malformed.
	for _, entry := range entries {
		r := entry.record
		request := r.Request
		keys, previous, batch, _ := normalizeExcelPricingWritebackIntent(request)
		job := &excelPricingWritebackJob{Schema: excelPricingWritebackJobSchema, JobID: r.JobID, RequestID: request.RequestID, SettingKey: request.SettingKey, Status: "observation_required", Code: "currency_owner_restart_observation_required", Blocking: true, UpdatedAt: queue.now().UTC().Format(time.RFC3339), settings: request.Settings, expectedStateRevision: request.ExpectedStateRevision, previousConfirmedValue: strings.TrimSpace(request.PreviousConfirmedValue), previousConfirmedValues: previous, sequence: r.Sequence, createdAt: r.CreatedAt, ownerObserveOnly: true}
		if batch {
			job.SettingKey = "settings_batch"
			job.SettingKeys = keys
			job.DesiredValues = map[string]string{}
			for _, key := range keys {
				job.DesiredValues[key], _ = excelPricingSettingValue(request.Settings, key)
			}
		} else {
			job.DesiredValue, _ = excelPricingSettingValue(request.Settings, request.SettingKey)
		}
		if entry.terminal != nil && entry.terminal.Status == "superseded" {
			job.Status = "superseded"
			job.Code = "superseded"
			job.Blocking = false
		}
		queue.jobs[r.JobID] = job
		if r.Sequence > queue.sequence {
			queue.sequence = r.Sequence
		}
		for _, key := range keys {
			queue.latestByKey[key] = r.JobID
		}
	}
	for _, entry := range entries {
		job := queue.jobs[entry.record.JobID]
		if !queue.isLatestLocked(job) {
			job.Status = "superseded"
			job.Code = "superseded"
			job.Blocking = false
		}
	}
	return nil
}

func (queue *excelPricingWritebackQueue) saveCurrencyIntent(job *excelPricingWritebackJob, request excelPricingWritebackRequest) error {
	if queue.currencyJournalDir == "" {
		return nil
	}
	if queue.currencyJournalError != nil || job == nil || !currencyOnlyWriteback(job) {
		return errCurrencyJournal
	}
	origin, e := queue.currencyJournalOrigin()
	if e != nil {
		return e
	}
	record := currencyIntentRecord{Schema: currencyJournalSchema, JobID: job.JobID, Sequence: job.sequence, CreatedAt: job.createdAt, OwnerOrigin: origin, Request: request}
	if validateCurrencyRecord(record) != nil || job.RequestID != request.RequestID || job.expectedStateRevision != request.ExpectedStateRevision || job.settings != request.Settings {
		return errCurrencyJournal
	}
	if e = queue.pruneCurrencyJournal(queue.now().UTC()); e != nil {
		return e
	}
	entries, e := queue.readCurrencyJournal()
	if e != nil {
		return e
	}
	if len(entries) >= excelPricingWritebackMaxJobs {
		return errors.New("currency_journal_capacity")
	}
	for _, entry := range entries {
		if entry.record.Sequence == record.Sequence || entry.record.Request.RequestID == request.RequestID {
			return errCurrencyJournal
		}
	}
	if e = os.MkdirAll(queue.currencyJournalDir, 0700); e != nil {
		return errCurrencyJournal
	}
	return writeCurrencyJournalJSON(filepath.Join(queue.currencyJournalDir, job.JobID+".json"), record)
}

func writeCurrencyJournalJSON(path string, value any) error {
	data, e := json.Marshal(value)
	if e != nil || len(data) > currencyJournalLimit {
		return errCurrencyJournal
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errCurrencyJournal
	}
	// An incomplete file is deliberately retained: later admission fails closed.
	n, e := f.Write(data)
	if e == nil && n != len(data) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return errCurrencyJournal
	}
	return nil
}

func (queue *excelPricingWritebackQueue) markCurrencyJournalTerminal(job *excelPricingWritebackJob) error {
	if queue.currencyJournalDir == "" || job == nil || !currencyOnlyWriteback(job) {
		return nil
	}
	if !currencyJournalID.MatchString(job.JobID) || (job.Status != "confirmed" && job.Status != "superseded") {
		return errCurrencyJournal
	}
	entries, e := queue.readCurrencyJournal()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.record.JobID == job.JobID {
			if entry.terminal != nil {
				return nil
			}
			return writeCurrencyJournalJSON(filepath.Join(queue.currencyJournalDir, job.JobID+".terminal.json"), currencyTerminalRecord{Schema: currencyJournalSchema, JobID: job.JobID, Status: job.Status, FinishedAt: queue.now().UTC()})
		}
	}
	return errCurrencyJournal
}

// Only validated terminal records expire. Unknown owner outcomes have no age
// limit. Removing the marker first makes an interrupted cleanup conservative.
func (queue *excelPricingWritebackQueue) pruneCurrencyJournal(now time.Time) error {
	entries, e := queue.readCurrencyJournal()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.terminal != nil && now.Sub(entry.terminal.FinishedAt) > excelPricingWritebackJobTTL {
			id := entry.record.JobID
			// A restored confirmed record is deliberately re-observed. Keep its
			// evidence while that local readback is unresolved.
			if job := queue.jobs[id]; job != nil && job.Status != "confirmed" && job.Status != "superseded" {
				continue
			}
			if os.Remove(filepath.Join(queue.currencyJournalDir, id+".terminal.json")) != nil || os.Remove(filepath.Join(queue.currencyJournalDir, id+".json")) != nil {
				return errCurrencyJournal
			}
		}
	}
	return nil
}
