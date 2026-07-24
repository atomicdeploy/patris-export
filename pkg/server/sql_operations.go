package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
)

const (
	sqlOperatorTokenEnv      = "PATRIS_EXPORT_SQL_OPERATOR_TOKEN"
	sqlOperatorTokenHeader   = "X-Patris-SQL-Operator-Token"
	sqlCSRFHeader            = "X-Patris-CSRF-Token"
	sqlSessionCookieName     = "patris_sql_operator_session"
	sqlManualSyncConfirm     = "manual_sync"
	sqlOperatorSessionTTL    = 10 * time.Minute
	sqlOperationTimeout      = 2 * time.Minute
	maximumSQLOperatorTokens = 128
	maximumSQLRequestBytes   = 4096
)

type sqlProbeFunc func(context.Context, recordsink.SQLOptions) (recordsink.SQLProbeResult, error)
type sqlSyncFunc func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error)

type sqlOperatorSession struct {
	csrfHash  [sha256.Size]byte
	origin    string
	createdAt time.Time
	expiresAt time.Time
}

type sqlOperationsState struct {
	sessionsMu    sync.Mutex
	sessions      map[[sha256.Size]byte]sqlOperatorSession
	operatorToken string
	sessionTTL    time.Duration
	now           func() time.Time

	permit chan struct{}
	active atomic.Bool

	lastMu sync.RWMutex
	last   *sqlOperationDiagnostic

	probe sqlProbeFunc
	sync  sqlSyncFunc
}

type sqlTargetStatus struct {
	Configured            bool                          `json:"configured"`
	Driver                string                        `json:"driver"`
	TableConfigured       bool                          `json:"table_configured"`
	BatchSize             int                           `json:"batch_size"`
	Reconciliation        recordsink.ReconciliationMode `json:"reconciliation"`
	ConnectTimeoutMS      int64                         `json:"connect_timeout_ms"`
	VerifiedTLSConfigured bool                          `json:"verified_tls_configured"`
	Busy                  bool                          `json:"busy"`
	LastResultAvailable   bool                          `json:"last_result_available"`
}

type sqlSafeProbeResult struct {
	Connected bool                   `json:"connected"`
	Driver    string                 `json:"driver"`
	Vendor    string                 `json:"vendor,omitempty"`
	TLS       recordsink.SQLTLSState `json:"tls"`
	ElapsedMS int64                  `json:"elapsed_ms"`
}

type sqlSafeFailure struct {
	Code      string                             `json:"code"`
	Stage     string                             `json:"stage"`
	Retryable bool                               `json:"retryable"`
	Message   string                             `json:"message"`
	Reason    recordsink.ReconciliationGuardCode `json:"reason,omitempty"`
}

type sqlOperationDiagnostic struct {
	Operation   string                `json:"operation"`
	Status      string                `json:"status"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at"`
	RecordCount *int                  `json:"record_count,omitempty"`
	Probe       *sqlSafeProbeResult   `json:"probe,omitempty"`
	Result      *recordsink.SQLResult `json:"result,omitempty"`
	Failure     *sqlSafeFailure       `json:"failure,omitempty"`
}

type sqlAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type sqlAPIErrorResponse struct {
	Success bool        `json:"success"`
	Error   sqlAPIError `json:"error"`
}

func newSQLOperationsState() *sqlOperationsState {
	state := &sqlOperationsState{
		sessions:      make(map[[sha256.Size]byte]sqlOperatorSession),
		operatorToken: os.Getenv(sqlOperatorTokenEnv),
		sessionTTL:    sqlOperatorSessionTTL,
		now:           time.Now,
		permit:        make(chan struct{}, 1),
		probe:         recordsink.ProbeSQLTarget,
		sync:          recordsink.SyncSQLWithResult,
	}
	state.permit <- struct{}{}
	return state
}

func (s *Server) handlePostSQLTargetSession(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	origin, originHost, ok := strictSameOrigin(r)
	if !ok {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return
	}
	localBootstrap := remoteAddressIsLoopback(r.RemoteAddr) && hostnameIsLoopback(originHost)
	if !localBootstrap {
		if !strings.HasPrefix(origin, "https://") || !s.sqlOperations.operatorTokenAuthorized(r) {
			writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
			return
		}
	}

	sessionID, csrfToken, expiresAt, err := s.sqlOperations.createSession(origin)
	if err != nil {
		writeSQLAPIError(w, http.StatusInternalServerError, "session_unavailable", "The SQL operator session could not be created.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sqlSessionCookieName,
		Value:    sessionID,
		Path:     "/api/sql-target",
		Expires:  expiresAt,
		MaxAge:   int(s.sqlOperations.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(origin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, map[string]interface{}{
		"authenticated": true,
		"csrf_token":    csrfToken,
		"expires_at":    expiresAt,
	})
}

func (s *Server) handleDeleteSQLTargetSession(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	cookie, err := r.Cookie(sqlSessionCookieName)
	if err != nil || cookie.Value == "" {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return
	}
	origin, _, ok := strictSameOrigin(r)
	if !ok {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return
	}
	s.sqlOperations.revokeSession(cookie.Value)
	http.SetCookie(w, &http.Cookie{
		Name:     sqlSessionCookieName,
		Value:    "",
		Path:     "/api/sql-target",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(origin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, map[string]interface{}{"authenticated": false})
}

func (s *Server) handleGetSQLTargetStatus(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	cfg := s.Config()
	_, hasLast := s.sqlOperations.lastDiagnostic()
	reconciliation := recordsink.ReconciliationMode(cfg.Export.Reconciliation)
	if reconciliation == "" {
		reconciliation = recordsink.UpsertOnly
	}
	writeJSON(w, sqlTargetStatus{
		Configured:            strings.TrimSpace(cfg.Export.MySQLDSN) != "",
		Driver:                "mysql",
		TableConfigured:       strings.TrimSpace(firstNonEmpty(cfg.Convert.Table, cfg.Export.MySQLTable)) != "",
		BatchSize:             cfg.Export.BatchSize,
		Reconciliation:        reconciliation,
		ConnectTimeoutMS:      recordsink.ParseSQLConnectTimeout(cfg.Export.MySQLConnectTimeout).Milliseconds(),
		VerifiedTLSConfigured: strings.TrimSpace(cfg.Export.MySQLTLSCAFile) != "" || strings.TrimSpace(cfg.Export.MySQLTLSServerName) != "",
		Busy:                  s.sqlOperations.active.Load(),
		LastResultAvailable:   hasLast,
	})
}

func (s *Server) handlePostSQLTargetTest(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sqlOperationTimeout)
	defer cancel()
	if !s.sqlOperations.acquire(ctx) {
		writeSQLAPIError(w, http.StatusGatewayTimeout, "operation_timeout", "The SQL operation could not start before its deadline.")
		return
	}
	defer s.sqlOperations.release()

	startedAt := s.sqlOperations.currentTime()
	result, err := s.sqlOperations.probe(ctx, s.sqlTargetProbeOptions(s.Config()))
	diagnostic := sqlOperationDiagnostic{
		Operation:  "test",
		Status:     "succeeded",
		StartedAt:  startedAt,
		FinishedAt: s.sqlOperations.currentTime(),
		Probe:      safeSQLProbeResult(result),
	}
	if err != nil {
		diagnostic.Status = "failed"
		diagnostic.Failure = safeSQLFailure(recordsink.SQLStageProbe, err)
	}
	s.sqlOperations.storeLast(diagnostic)
	writeSQLOperationDiagnostic(w, diagnostic, err)
}

func (s *Server) handlePostSQLTargetPreview(w http.ResponseWriter, r *http.Request) {
	s.handleSQLTargetSync(w, r, true)
}

func (s *Server) handlePostSQLTargetSync(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	reconciliationToken, ok := decodeManualSyncConfirmation(w, r)
	if !ok {
		return
	}
	s.runSQLTargetSync(w, r, false, reconciliationToken)
}

func (s *Server) handleSQLTargetSync(w http.ResponseWriter, r *http.Request, dryRun bool) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	s.runSQLTargetSync(w, r, dryRun, "")
}

func (s *Server) runSQLTargetSync(w http.ResponseWriter, r *http.Request, dryRun bool, reconciliationToken string) {
	ctx, cancel := context.WithTimeout(r.Context(), sqlOperationTimeout)
	defer cancel()
	if !s.sqlOperations.acquire(ctx) {
		writeSQLAPIError(w, http.StatusGatewayTimeout, "operation_timeout", "The SQL operation could not start before its deadline.")
		return
	}
	defer s.sqlOperations.release()

	cfg := s.Config()
	reconciliation := recordsink.ReconciliationMode(cfg.Export.Reconciliation)
	if !dryRun {
		switch reconciliation {
		case recordsink.DeleteMissing:
			writeSQLAPIError(w, http.StatusUnprocessableEntity, "hard_delete_unavailable", "Browser-triggered hard deletion is not available.")
			return
		case recordsink.SoftDeleteMissing:
		default:
			if reconciliationToken != "" {
				writeSQLAPIError(w, http.StatusBadRequest, "unexpected_reconciliation_token", "This SQL reconciliation mode does not accept a preview token.")
				return
			}
		}
	}

	operation := "sync"
	if dryRun {
		operation = "preview"
	}
	startedAt := s.sqlOperations.currentTime()
	result, err := s.RecordResult()
	if err != nil {
		diagnostic := sqlOperationDiagnostic{
			Operation:  operation,
			Status:     "failed",
			StartedAt:  startedAt,
			FinishedAt: s.sqlOperations.currentTime(),
			Failure: &sqlSafeFailure{
				Code:      "source_unavailable",
				Stage:     "source",
				Retryable: true,
				Message:   "The source records could not be prepared.",
			},
		}
		s.sqlOperations.storeLast(diagnostic)
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "diagnostic": diagnostic})
		return
	}

	recordCount := len(result.Rows)
	options := s.sqlTargetSyncOptions(cfg, result, dryRun)
	if !dryRun {
		options.ReconciliationToken = reconciliationToken
	}
	syncResult, syncErr := s.sqlOperations.sync(ctx, options, result.Rows)
	syncResult = safeSQLResult(syncResult, options, recordCount, syncErr)
	diagnostic := sqlOperationDiagnostic{
		Operation:   operation,
		Status:      "succeeded",
		StartedAt:   startedAt,
		FinishedAt:  s.sqlOperations.currentTime(),
		RecordCount: &recordCount,
		Result:      &syncResult,
	}
	if syncErr != nil {
		diagnostic.Status = "failed"
		diagnostic.Failure = safeSQLFailure(recordsink.SQLStageSync, syncErr)
	}
	s.sqlOperations.storeLast(diagnostic)
	writeSQLOperationDiagnostic(w, diagnostic, syncErr)
}

func (s *Server) handleGetSQLTargetLastResult(w http.ResponseWriter, r *http.Request) {
	setSQLOperationHeaders(w)
	if !s.requireSQLOperatorSession(w, r) {
		return
	}
	diagnostic, ok := s.sqlOperations.lastDiagnostic()
	if !ok {
		writeJSON(w, map[string]interface{}{"available": false})
		return
	}
	writeJSON(w, map[string]interface{}{"available": true, "diagnostic": diagnostic})
}

func (s *Server) sqlTargetProbeOptions(cfg appconfig.Config) recordsink.SQLOptions {
	return recordsink.SQLOptions{
		Driver:         "mysql",
		DSN:            cfg.Export.MySQLDSN,
		ConnectTimeout: recordsink.ParseSQLConnectTimeout(cfg.Export.MySQLConnectTimeout),
		MySQLTLS: recordsink.MySQLTLSOptions{
			CAFile:     cfg.Export.MySQLTLSCAFile,
			ServerName: cfg.Export.MySQLTLSServerName,
		},
	}
}

func (s *Server) sqlTargetSyncOptions(cfg appconfig.Config, result recordpipe.Result, dryRun bool) recordsink.SQLOptions {
	options := s.sqlTargetProbeOptions(cfg)
	options.Table = firstNonEmpty(cfg.Convert.Table, cfg.Export.MySQLTable, recordpipe.SourceTableName(s.currentDBPath()))
	options.KeyField = result.KeyField
	options.Batch = cfg.Export.BatchSize
	options.Reconciliation = recordsink.ReconciliationMode(cfg.Export.Reconciliation)
	options.DryRun = dryRun
	if result.Contract != nil {
		options.ProtectedKeys = append([]string(nil), result.Contract.QuarantinedCodes...)
	}
	return options
}

func (s *Server) requireSQLOperatorSession(w http.ResponseWriter, r *http.Request) bool {
	origin, _, ok := protectedRequestOrigin(r)
	if !ok {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return false
	}
	cookie, err := r.Cookie(sqlSessionCookieName)
	if err != nil || cookie.Value == "" {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return false
	}
	csrfValues := r.Header.Values(sqlCSRFHeader)
	if len(csrfValues) != 1 || csrfValues[0] == "" || !s.sqlOperations.sessionAuthorized(cookie.Value, csrfValues[0], origin) {
		writeSQLAPIError(w, http.StatusForbidden, "forbidden", "SQL operator authorization failed.")
		return false
	}
	return true
}

func (state *sqlOperationsState) createSession(origin string) (string, string, time.Time, error) {
	sessionID, err := randomSQLToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrfToken, err := randomSQLToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := state.currentTime()
	expiresAt := now.Add(state.sessionTTL)
	sessionHash := sha256.Sum256([]byte(sessionID))

	state.sessionsMu.Lock()
	defer state.sessionsMu.Unlock()
	state.pruneSessionsLocked(now)
	if len(state.sessions) >= maximumSQLOperatorTokens {
		state.evictOldestSessionLocked()
	}
	state.sessions[sessionHash] = sqlOperatorSession{
		csrfHash:  sha256.Sum256([]byte(csrfToken)),
		origin:    origin,
		createdAt: now,
		expiresAt: expiresAt,
	}
	return sessionID, csrfToken, expiresAt, nil
}

func (state *sqlOperationsState) sessionAuthorized(sessionID, csrfToken, origin string) bool {
	sessionHash := sha256.Sum256([]byte(sessionID))
	csrfHash := sha256.Sum256([]byte(csrfToken))
	now := state.currentTime()
	state.sessionsMu.Lock()
	defer state.sessionsMu.Unlock()
	state.pruneSessionsLocked(now)
	session, ok := state.sessions[sessionHash]
	if !ok || session.origin != origin || !now.Before(session.expiresAt) {
		return false
	}
	return subtle.ConstantTimeCompare(session.csrfHash[:], csrfHash[:]) == 1
}

func (state *sqlOperationsState) revokeSession(sessionID string) {
	sessionHash := sha256.Sum256([]byte(sessionID))
	state.sessionsMu.Lock()
	delete(state.sessions, sessionHash)
	state.sessionsMu.Unlock()
}

func (state *sqlOperationsState) operatorTokenAuthorized(r *http.Request) bool {
	if len(state.operatorToken) < 32 || len(state.operatorToken) > 512 {
		return false
	}
	values := r.Header.Values(sqlOperatorTokenHeader)
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 512 {
		return false
	}
	want := sha256.Sum256([]byte(state.operatorToken))
	got := sha256.Sum256([]byte(values[0]))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (state *sqlOperationsState) pruneSessionsLocked(now time.Time) {
	for key, session := range state.sessions {
		if !now.Before(session.expiresAt) {
			delete(state.sessions, key)
		}
	}
}

func (state *sqlOperationsState) evictOldestSessionLocked() {
	var oldestKey [sha256.Size]byte
	var oldest time.Time
	found := false
	for key, session := range state.sessions {
		if !found || session.createdAt.Before(oldest) {
			oldestKey = key
			oldest = session.createdAt
			found = true
		}
	}
	if found {
		delete(state.sessions, oldestKey)
	}
}

func (state *sqlOperationsState) acquire(ctx context.Context) bool {
	select {
	case <-state.permit:
		state.active.Store(true)
		return true
	case <-ctx.Done():
		return false
	}
}

func (state *sqlOperationsState) release() {
	state.active.Store(false)
	state.permit <- struct{}{}
}

func (state *sqlOperationsState) currentTime() time.Time {
	return state.now().UTC()
}

func (state *sqlOperationsState) storeLast(diagnostic sqlOperationDiagnostic) {
	copy := cloneSQLOperationDiagnostic(diagnostic)
	state.lastMu.Lock()
	state.last = &copy
	state.lastMu.Unlock()
}

func (state *sqlOperationsState) lastDiagnostic() (sqlOperationDiagnostic, bool) {
	state.lastMu.RLock()
	defer state.lastMu.RUnlock()
	if state.last == nil {
		return sqlOperationDiagnostic{}, false
	}
	return cloneSQLOperationDiagnostic(*state.last), true
}

func cloneSQLOperationDiagnostic(value sqlOperationDiagnostic) sqlOperationDiagnostic {
	copy := value
	if value.RecordCount != nil {
		recordCount := *value.RecordCount
		copy.RecordCount = &recordCount
	}
	if value.Probe != nil {
		probe := *value.Probe
		copy.Probe = &probe
	}
	if value.Result != nil {
		result := *value.Result
		if value.Result.ReconciliationEvidence != nil {
			evidence := *value.Result.ReconciliationEvidence
			result.ReconciliationEvidence = &evidence
		}
		copy.Result = &result
	}
	if value.Failure != nil {
		failure := *value.Failure
		copy.Failure = &failure
	}
	return copy
}

func randomSQLToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func strictSameOrigin(r *http.Request) (string, string, bool) {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return "", "", false
	}
	rawOrigin := strings.TrimSpace(values[0])
	if rawOrigin == "" || strings.Contains(rawOrigin, ",") {
		return "", "", false
	}
	parsed, err := url.Parse(rawOrigin)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	origin, originHost, ok := canonicalHTTPOrigin(parsed.Scheme, parsed.Host)
	if !ok {
		return "", "", false
	}
	expected, _, ok := effectiveRequestOrigin(r)
	if !ok || origin != expected {
		return "", "", false
	}
	return origin, originHost, true
}

func protectedRequestOrigin(r *http.Request) (string, string, bool) {
	if len(r.Header.Values("Origin")) > 0 || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return strictSameOrigin(r)
	}

	fetchSite := r.Header.Values("Sec-Fetch-Site")
	referers := r.Header.Values("Referer")
	if len(fetchSite) != 1 || fetchSite[0] != "same-origin" || len(referers) != 1 {
		return "", "", false
	}
	referer, err := url.Parse(strings.TrimSpace(referers[0]))
	if err != nil || referer.User != nil || referer.Opaque != "" || referer.Host == "" {
		return "", "", false
	}
	refererOrigin, refererHost, ok := canonicalHTTPOrigin(referer.Scheme, referer.Host)
	if !ok {
		return "", "", false
	}
	expected, _, ok := effectiveRequestOrigin(r)
	if !ok || refererOrigin != expected {
		return "", "", false
	}
	return refererOrigin, refererHost, true
}

func effectiveRequestOrigin(r *http.Request) (string, string, bool) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	authority := r.Host

	// Patris serves HTTP directly, so a remote HTTPS deployment normally
	// terminates TLS at a local reverse proxy. Forwarded values are honored
	// only from a loopback peer and only as singular, canonical values.
	if r.TLS == nil && remoteAddressIsLoopback(r.RemoteAddr) {
		forwardedProto := r.Header.Values("X-Forwarded-Proto")
		forwardedHost := r.Header.Values("X-Forwarded-Host")
		if len(forwardedProto) > 1 || len(forwardedHost) > 1 {
			return "", "", false
		}
		if len(forwardedProto) == 1 {
			scheme = strings.ToLower(strings.TrimSpace(forwardedProto[0]))
			if scheme != "http" && scheme != "https" {
				return "", "", false
			}
			if len(forwardedHost) == 1 {
				authority = strings.TrimSpace(forwardedHost[0])
			}
		} else if len(forwardedHost) != 0 {
			return "", "", false
		}
	}
	return canonicalHTTPOrigin(scheme, authority)
}

func canonicalHTTPOrigin(scheme, authority string) (string, string, bool) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	authority = strings.TrimSpace(authority)
	if (scheme != "http" && scheme != "https") || authority == "" || strings.ContainsAny(authority, "/?#") {
		return "", "", false
	}
	parsed, err := url.Parse(scheme + "://" + authority)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || strings.Contains(hostname, "%") {
		return "", "", false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		hostname = ip.String()
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", "", false
		}
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			port = ""
		}
	}
	normalizedAuthority := hostname
	if strings.Contains(hostname, ":") {
		normalizedAuthority = "[" + hostname + "]"
	}
	if port != "" {
		normalizedAuthority = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + normalizedAuthority, hostname, true
}

func remoteAddressIsLoopback(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(remoteAddress), "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostnameIsLoopback(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func safeSQLProbeResult(result recordsink.SQLProbeResult) *sqlSafeProbeResult {
	vendor := strings.ToLower(strings.TrimSpace(result.Vendor))
	if vendor != "mysql" && vendor != "mariadb" {
		vendor = ""
	}
	tlsState := result.TLS
	switch tlsState {
	case recordsink.SQLTLSUnknown, recordsink.SQLTLSEncrypted, recordsink.SQLTLSPlaintext:
	default:
		tlsState = recordsink.SQLTLSUnknown
	}
	elapsedMS := result.ElapsedMS
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	return &sqlSafeProbeResult{
		Connected: result.Connected,
		Driver:    "mysql",
		Vendor:    vendor,
		TLS:       tlsState,
		ElapsedMS: elapsedMS,
	}
}

func safeSQLFailure(stage recordsink.SQLFailureStage, err error) *sqlSafeFailure {
	failure := recordsink.ClassifySQLError(stage, err)
	if failure == nil {
		return nil
	}
	return &sqlSafeFailure{
		Code:      string(failure.Code),
		Stage:     string(failure.Stage),
		Retryable: failure.Retryable,
		Message:   failure.Message,
		Reason:    safeReconciliationGuardCode(failure.Reason),
	}
}

func safeSQLResult(result recordsink.SQLResult, options recordsink.SQLOptions, recordCount int, err error) recordsink.SQLResult {
	result.Inserted = nonNegativeSQLCount(result.Inserted)
	result.Updated = nonNegativeSQLCount(result.Updated)
	result.Unchanged = nonNegativeSQLCount(result.Unchanged)
	result.Deleted = nonNegativeSQLCount(result.Deleted)
	result.Failed = nonNegativeSQLCount(result.Failed)
	result.ElapsedMS = max(result.ElapsedMS, 0)
	result.DryRun = options.DryRun
	switch options.Reconciliation {
	case recordsink.SoftDeleteMissing:
		result.Reconciliation = recordsink.SoftDeleteMissing
	case recordsink.DeleteMissing:
		result.Reconciliation = recordsink.DeleteMissing
	default:
		result.Reconciliation = recordsink.UpsertOnly
	}
	if result.Reconciliation == recordsink.SoftDeleteMissing {
		result.ReconciliationEvidence = safeSQLReconciliationEvidence(result.ReconciliationEvidence, result.DryRun)
	} else {
		result.ReconciliationEvidence = nil
	}
	if err != nil {
		// SyncSQLWithResult is transactional and already reports this shape.
		// Enforce it again at the HTTP boundary so a future sink implementation
		// cannot expose partial-looking successes for an unconfirmed transaction.
		result.Inserted = 0
		result.Updated = 0
		result.Unchanged = 0
		result.Deleted = 0
		result.Failed = nonNegativeSQLCount(recordCount)
	}
	return result
}

func safeSQLReconciliationEvidence(value *recordsink.SQLReconciliationEvidence, dryRun bool) *recordsink.SQLReconciliationEvidence {
	if value == nil {
		return nil
	}
	copy := *value
	copy.SourceRows = nonNegativeSQLCount(copy.SourceRows)
	copy.ProtectedRows = nonNegativeSQLCount(copy.ProtectedRows)
	copy.TargetRows = nonNegativeSQLCount(copy.TargetRows)
	copy.MissingRows = nonNegativeSQLCount(copy.MissingRows)
	copy.WouldSoftDelete = nonNegativeSQLCount(copy.WouldSoftDelete)
	copy.AlreadySoftDeleted = nonNegativeSQLCount(copy.AlreadySoftDeleted)
	copy.WouldRestore = nonNegativeSQLCount(copy.WouldRestore)
	copy.GuardCode = safeReconciliationGuardCode(copy.GuardCode)

	tokenValid := validSoftDeleteConfirmationToken(copy.ConfirmationToken)
	if copy.ConfirmationToken != "" && !tokenValid {
		copy.ApplyAllowed = false
		copy.GuardCode = recordsink.ReconciliationGuardUnknown
	}
	if copy.SourceRows == 0 {
		copy.ApplyAllowed = false
		if copy.GuardCode == "" {
			copy.GuardCode = recordsink.ReconciliationGuardEmptySource
		}
	}
	if !copy.ConfirmationRequired {
		copy.ApplyAllowed = false
	}
	if !dryRun || !copy.ApplyAllowed || copy.GuardCode != "" || !tokenValid {
		copy.ConfirmationToken = ""
	}
	if dryRun && copy.ApplyAllowed && copy.ConfirmationRequired && copy.ConfirmationToken == "" {
		copy.ApplyAllowed = false
		copy.GuardCode = recordsink.ReconciliationGuardUnknown
	}
	return &copy
}

func safeReconciliationGuardCode(value recordsink.ReconciliationGuardCode) recordsink.ReconciliationGuardCode {
	switch value {
	case "",
		recordsink.ReconciliationGuardEmptySource,
		recordsink.ReconciliationGuardPreviewRequired,
		recordsink.ReconciliationGuardPreviewMismatch,
		recordsink.ReconciliationGuardReservedField,
		recordsink.ReconciliationGuardInvalidTombstone,
		recordsink.ReconciliationGuardUnknown:
		return value
	default:
		return recordsink.ReconciliationGuardUnknown
	}
}

func validSoftDeleteConfirmationToken(value string) bool {
	if len(value) != recordsink.SoftDeleteConfirmationTokenLength ||
		!strings.HasPrefix(value, recordsink.SoftDeleteConfirmationTokenPrefix) {
		return false
	}
	for _, character := range value[len(recordsink.SoftDeleteConfirmationTokenPrefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func nonNegativeSQLCount(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func decodeManualSyncConfirmation(w http.ResponseWriter, r *http.Request) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeSQLAPIError(w, http.StatusUnsupportedMediaType, "json_required", "Manual sync requires an application/json request.")
		return "", false
	}
	body := http.MaxBytesReader(w, r.Body, maximumSQLRequestBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var request struct {
		Confirm             string `json:"confirm"`
		ReconciliationToken string `json:"reconciliation_token,omitempty"`
	}
	if err := decoder.Decode(&request); err != nil {
		writeSQLAPIError(w, http.StatusBadRequest, "invalid_request", "Manual sync requires an explicit confirmation.")
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeSQLAPIError(w, http.StatusBadRequest, "invalid_request", "Manual sync requires one JSON object.")
		return "", false
	}
	if request.Confirm != sqlManualSyncConfirm {
		writeSQLAPIError(w, http.StatusBadRequest, "confirmation_required", "Manual sync requires an explicit confirmation.")
		return "", false
	}
	if request.ReconciliationToken != "" && !validSoftDeleteConfirmationToken(request.ReconciliationToken) {
		writeSQLAPIError(w, http.StatusBadRequest, "invalid_reconciliation_token", "The reconciliation preview token is invalid.")
		return "", false
	}
	return request.ReconciliationToken, true
}

func writeSQLOperationDiagnostic(w http.ResponseWriter, diagnostic sqlOperationDiagnostic, err error) {
	status := http.StatusOK
	if err != nil {
		status = sqlOperationFailureStatus(diagnostic.Failure)
	}
	writeJSONStatus(w, status, map[string]interface{}{"success": err == nil, "diagnostic": diagnostic})
}

func sqlOperationFailureStatus(failure *sqlSafeFailure) int {
	if failure == nil {
		return http.StatusBadGateway
	}
	switch failure.Code {
	case string(recordsink.SQLFailureInvalidConfiguration), string(recordsink.SQLFailureConstraint):
		return http.StatusUnprocessableEntity
	case string(recordsink.SQLFailureReconciliation):
		return http.StatusConflict
	case string(recordsink.SQLFailureTimeout):
		return http.StatusGatewayTimeout
	case string(recordsink.SQLFailureCancelled):
		return http.StatusRequestTimeout
	case string(recordsink.SQLFailureTransient), string(recordsink.SQLFailureUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func writeSQLAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, sqlAPIErrorResponse{
		Success: false,
		Error: sqlAPIError{
			Code:    code,
			Message: message,
		},
	})
}

func setSQLOperationHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Origin")
}
