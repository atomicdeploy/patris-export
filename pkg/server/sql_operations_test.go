package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
)

const (
	sqlTestOperatorToken = "operator-token-that-is-deliberately-longer-than-32-bytes"
	sqlTestEdgeToken     = "edge-token-must-never-authorize-sql-operations"
	sqlTestDSN           = "sql_api_user:super-secret-password@tcp(secret-db.internal:3306)/patris?tls=true"
	sqlTestCAPath        = "C:/protected/secret-database-ca.pem"
	sqlTestServerName    = "secret-db.internal"
)

type sqlTestCredentials struct {
	origin string
	scheme string
	host   string
	cookie *http.Cookie
	csrf   string
}

func newSQLOperationsTestServer(t *testing.T) *Server {
	t.Helper()
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "products.json")
	source := []byte(`{
  "001": {"Code": "001", "Name": "Alpha"},
  "002": {"Code": "002", "Name": "Beta"}
}`)
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatalf("write SQL operations source: %v", err)
	}
	manager, err := appconfig.Load(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("load SQL operations config: %v", err)
	}
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.Database.Raw = true
		cfg.Export.MySQLDSN = sqlTestDSN
		cfg.Export.SQLiteTable = "must_not_be_used_for_mysql_operations"
		cfg.Export.MySQLTable = "products"
		cfg.Export.MySQLTLSCAFile = sqlTestCAPath
		cfg.Export.MySQLTLSServerName = sqlTestServerName
		cfg.Export.MySQLConnectTimeout = "3s"
		cfg.Export.BatchSize = 2
		cfg.Export.Reconciliation = string(recordsink.SoftDeleteMissing)
		cfg.Edge.Token = sqlTestEdgeToken
	}); err != nil {
		t.Fatalf("configure SQL operations server: %v", err)
	}
	server, err := NewServerWithOptions(sourcePath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create SQL operations server: %v", err)
	}
	server.sqlOperations.operatorToken = sqlTestOperatorToken
	t.Cleanup(func() { server.Close() })
	return server
}

func createSQLTestSession(t *testing.T, server *Server, remote bool) sqlTestCredentials {
	t.Helper()
	credentials := sqlTestCredentials{
		origin: "http://127.0.0.1:18080",
		scheme: "http",
		host:   "127.0.0.1:18080",
	}
	remoteAddress := "127.0.0.1:41234"
	if remote {
		credentials.origin = "https://patris.example"
		credentials.scheme = "https"
		credentials.host = "patris.example"
		remoteAddress = "203.0.113.19:41234"
	}
	request := httptest.NewRequest(http.MethodPost, credentials.origin+"/api/sql-target/session", nil)
	request.RemoteAddr = remoteAddress
	request.Header.Set("Origin", credentials.origin)
	if remote {
		request.Header.Set(sqlOperatorTokenHeader, sqlTestOperatorToken)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create SQL operator session: status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode SQL operator session: %v", err)
	}
	if !payload.Authenticated || len(payload.CSRFToken) < 32 {
		t.Fatalf("invalid SQL operator session payload: %#v", payload)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("SQL operator session cookies=%d, want 1", len(cookies))
	}
	credentials.cookie = cookies[0]
	credentials.csrf = payload.CSRFToken
	return credentials
}

func newAuthenticatedSQLRequest(credentials sqlTestCredentials, method, path string, body []byte) *http.Request {
	request := httptest.NewRequest(method, credentials.scheme+"://"+credentials.host+path, bytes.NewReader(body))
	request.Header.Set("Origin", credentials.origin)
	request.Header.Set(sqlCSRFHeader, credentials.csrf)
	request.AddCookie(credentials.cookie)
	return request
}

func serveSQLRequest(server *Server, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

func responseContainsAny(response *httptest.ResponseRecorder, values ...string) (string, bool) {
	body := response.Body.String()
	for _, value := range values {
		if value != "" && strings.Contains(body, value) {
			return value, true
		}
	}
	return "", false
}

func TestSQLOperatorSessionRequiresExactOriginAndDedicatedAuthorization(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	tests := []struct {
		name       string
		target     string
		origin     []string
		remoteAddr string
		token      string
		wantStatus int
	}{
		{
			name:       "loopback bootstrap",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusOK,
		},
		{
			name:       "localhost bootstrap",
			target:     "http://localhost:18080/api/sql-target/session",
			origin:     []string{"http://localhost:18080"},
			remoteAddr: "[::1]:41000",
			wantStatus: http.StatusOK,
		},
		{
			name:       "public host is not loopback bootstrap",
			target:     "http://patris.example/api/sql-target/session",
			origin:     []string{"http://patris.example"},
			remoteAddr: "127.0.0.1:41000",
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote plaintext rejected",
			target:     "http://patris.example/api/sql-target/session",
			origin:     []string{"http://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote missing operator token",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "edge token cannot authorize SQL",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			token:      sqlTestEdgeToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote dedicated token over TLS",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "trusted loopback reverse proxy",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "127.0.0.1:41000",
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "untrusted peer cannot spoof forwarded TLS",
			target:     "http://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing origin",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "multiple origin headers",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080", "http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "origin host mismatch",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://localhost:18080"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "origin with path rejected",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080/path"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "opaque origin rejected",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"null"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusForbidden,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, nil)
			request.RemoteAddr = test.remoteAddr
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			if test.token != "" {
				request.Header.Set(sqlOperatorTokenHeader, test.token)
			}
			if test.name == "trusted loopback reverse proxy" {
				request.Header.Set("X-Forwarded-Proto", "https")
				request.Header.Set("X-Forwarded-Host", "patris.example")
			}
			if test.name == "untrusted peer cannot spoof forwarded TLS" {
				request.Header.Set("X-Forwarded-Proto", "https")
			}
			response := serveSQLRequest(server, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
			if value, found := responseContainsAny(response, sqlTestOperatorToken, sqlTestEdgeToken, sqlTestDSN, sqlTestCAPath, sqlTestServerName); found {
				t.Fatalf("response exposed protected value %q: %s", value, response.Body)
			}
		})
	}
}

func TestSQLOperatorSessionCookieCSRFOriginAndExpiry(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	server.sqlOperations.now = func() time.Time { return now }
	server.sqlOperations.sessionTTL = 2 * time.Minute
	credentials := createSQLTestSession(t, server, false)

	if !credentials.cookie.HttpOnly || credentials.cookie.SameSite != http.SameSiteStrictMode ||
		credentials.cookie.Path != "/api/sql-target" || credentials.cookie.Secure {
		t.Fatalf("unsafe loopback session cookie: %#v", credentials.cookie)
	}
	if !credentials.cookie.Expires.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("cookie expiry=%v, want %v", credentials.cookie.Expires, now.Add(2*time.Minute))
	}

	valid := newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil)
	if response := serveSQLRequest(server, valid); response.Code != http.StatusOK {
		t.Fatalf("valid session status=%d: %s", response.Code, response.Body)
	}
	browserRealisticGET := newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil)
	browserRealisticGET.Header.Del("Origin")
	browserRealisticGET.Header.Set("Sec-Fetch-Site", "same-origin")
	browserRealisticGET.Header.Set("Sec-Fetch-Mode", "cors")
	browserRealisticGET.Header.Set("Referer", credentials.origin+"/")
	if response := serveSQLRequest(server, browserRealisticGET); response.Code != http.StatusOK {
		t.Fatalf("browser-realistic GET without Origin status=%d: %s", response.Code, response.Body)
	}

	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing CSRF", func(request *http.Request) { request.Header.Del(sqlCSRFHeader) }},
		{"wrong CSRF", func(request *http.Request) { request.Header.Set(sqlCSRFHeader, "wrong") }},
		{"multiple CSRF", func(request *http.Request) { request.Header.Add(sqlCSRFHeader, "second") }},
		{"missing cookie", func(request *http.Request) { request.Header.Del("Cookie") }},
		{"missing origin", func(request *http.Request) { request.Header.Del("Origin") }},
		{"wrong origin", func(request *http.Request) { request.Header.Set("Origin", "http://localhost:18080") }},
		{"cross-site fetch metadata", func(request *http.Request) {
			request.Header.Del("Origin")
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			request.Header.Set("Referer", credentials.origin+"/")
		}},
		{"missing referer fallback", func(request *http.Request) {
			request.Header.Del("Origin")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
		}},
		{"mismatched referer fallback", func(request *http.Request) {
			request.Header.Del("Origin")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Referer", "http://localhost:18080/")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil)
			test.mutate(request)
			response := serveSQLRequest(server, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want 403: %s", response.Code, response.Body)
			}
		})
	}

	now = now.Add(2 * time.Minute)
	expired := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil))
	if expired.Code != http.StatusForbidden {
		t.Fatalf("expired session status=%d, want 403: %s", expired.Code, expired.Body)
	}
}

func TestSQLOperatorSessionRevokeRequiresOriginAndCSRF(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)

	wrongOrigin := newAuthenticatedSQLRequest(credentials, http.MethodDelete, "/api/sql-target/session", nil)
	wrongOrigin.Header.Set("Origin", "http://localhost:18080")
	if response := serveSQLRequest(server, wrongOrigin); response.Code != http.StatusForbidden {
		t.Fatalf("wrong-origin revoke status=%d, want 403: %s", response.Code, response.Body)
	}
	missingCSRF := newAuthenticatedSQLRequest(credentials, http.MethodDelete, "/api/sql-target/session", nil)
	missingCSRF.Header.Del(sqlCSRFHeader)
	if response := serveSQLRequest(server, missingCSRF); response.Code != http.StatusForbidden {
		t.Fatalf("missing-CSRF revoke status=%d, want 403: %s", response.Code, response.Body)
	}
	if response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil)); response.Code != http.StatusOK {
		t.Fatalf("failed revoke attempts invalidated session: status=%d body=%s", response.Code, response.Body)
	}

	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodDelete, "/api/sql-target/session", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("revoke status=%d: %s", response.Code, response.Body)
	}
	var payload struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if payload.Authenticated {
		t.Fatalf("revoke response remained authenticated: %s", response.Body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sqlSessionCookieName || cookies[0].Value != "" ||
		cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("revoke did not expire the protected session cookie: %#v", cookies)
	}
	if followup := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil)); followup.Code != http.StatusForbidden {
		t.Fatalf("revoked session status=%d, want 403: %s", followup.Code, followup.Body)
	}

	remoteCredentials := createSQLTestSession(t, server, true)
	if !remoteCredentials.cookie.Secure {
		t.Fatalf("remote TLS session cookie is not Secure: %#v", remoteCredentials.cookie)
	}
}

func TestSQLTargetStatusIsUsefulAndSecretFree(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status endpoint=%d: %s", response.Code, response.Body)
	}
	var status sqlTargetStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode target status: %v", err)
	}
	if !status.Configured || !status.TableConfigured || status.Driver != "mysql" || status.BatchSize != 2 ||
		status.Reconciliation != recordsink.SoftDeleteMissing || status.ConnectTimeoutMS != 3000 ||
		!status.VerifiedTLSConfigured || status.Busy || status.LastResultAvailable {
		t.Fatalf("unexpected SQL target status: %#v", status)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", cacheControl)
	}
	if value, found := responseContainsAny(response, sqlTestOperatorToken, sqlTestEdgeToken, sqlTestDSN, "super-secret-password", sqlTestCAPath, sqlTestServerName); found {
		t.Fatalf("status exposed protected value %q: %s", value, response.Body)
	}
}

func TestSQLTargetProbeAndLastResultRedactTargetAndDriverErrors(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	var captured recordsink.SQLOptions
	server.sqlOperations.probe = func(_ context.Context, options recordsink.SQLOptions) (recordsink.SQLProbeResult, error) {
		captured = options
		return recordsink.SQLProbeResult{
			Connected:     true,
			Driver:        "mysql",
			Vendor:        "MariaDB",
			ServerVersion: "11.4.2-secret-build-metadata",
			TLS:           recordsink.SQLTLSEncrypted,
			ElapsedMS:     19,
		}, nil
	}
	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/test", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("probe status=%d: %s", response.Code, response.Body)
	}
	if captured.DSN != sqlTestDSN || captured.MySQLTLS.CAFile != sqlTestCAPath ||
		captured.MySQLTLS.ServerName != sqlTestServerName || captured.ConnectTimeout != 3*time.Second {
		t.Fatalf("probe did not receive protected server configuration: %#v", captured)
	}
	if value, found := responseContainsAny(response, sqlTestDSN, "super-secret-password", sqlTestCAPath, sqlTestServerName, "secret-build-metadata"); found {
		t.Fatalf("probe exposed protected value %q: %s", value, response.Body)
	}
	var probePayload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(response.Body).Decode(&probePayload); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if !probePayload.Success || probePayload.Diagnostic.Probe == nil ||
		probePayload.Diagnostic.Probe.Vendor != "mariadb" ||
		probePayload.Diagnostic.Probe.TLS != recordsink.SQLTLSEncrypted {
		t.Fatalf("unexpected safe probe response: %#v", probePayload)
	}

	rawFailure := fmt.Sprintf("dial %s with CA %s failed: raw-driver-secret", sqlTestDSN, sqlTestCAPath)
	server.sqlOperations.probe = func(context.Context, recordsink.SQLOptions) (recordsink.SQLProbeResult, error) {
		return recordsink.SQLProbeResult{}, errors.New(rawFailure)
	}
	failed := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/test", nil))
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("failed probe status=%d, want 502: %s", failed.Code, failed.Body)
	}
	if value, found := responseContainsAny(failed, sqlTestDSN, "super-secret-password", sqlTestCAPath, sqlTestServerName, "raw-driver-secret"); found {
		t.Fatalf("failure exposed protected value %q: %s", value, failed.Body)
	}

	last := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/last-result", nil))
	if last.Code != http.StatusOK {
		t.Fatalf("last result status=%d: %s", last.Code, last.Body)
	}
	if value, found := responseContainsAny(last, sqlTestDSN, "super-secret-password", sqlTestCAPath, sqlTestServerName, "raw-driver-secret"); found {
		t.Fatalf("last result exposed protected value %q: %s", value, last.Body)
	}
	var lastPayload struct {
		Available  bool                   `json:"available"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(last.Body).Decode(&lastPayload); err != nil {
		t.Fatalf("decode last result: %v", err)
	}
	if !lastPayload.Available || lastPayload.Diagnostic.Status != "failed" ||
		lastPayload.Diagnostic.Failure == nil ||
		lastPayload.Diagnostic.Failure.Code != string(recordsink.SQLFailureUnknown) {
		t.Fatalf("unexpected last result: %#v", lastPayload)
	}
}

func TestSQLTargetPreviewAndConfirmedSyncUseCanonicalSharedSink(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	confirmationToken := recordsink.SoftDeleteConfirmationTokenPrefix + strings.Repeat("a", 64)
	var (
		mu       sync.Mutex
		calls    int
		options  []recordsink.SQLOptions
		rowCodes [][]string
	)
	server.sqlOperations.sync = func(_ context.Context, option recordsink.SQLOptions, rows []map[string]interface{}) (recordsink.SQLResult, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		options = append(options, option)
		codes := make([]string, 0, len(rows))
		for _, row := range rows {
			codes = append(codes, fmt.Sprint(row["Code"]))
		}
		sort.Strings(codes)
		rowCodes = append(rowCodes, codes)
		result := recordsink.SQLResult{
			Inserted:       len(rows),
			ElapsedMS:      -7,
			DryRun:         !option.DryRun,
			Reconciliation: recordsink.ReconciliationMode("unsafe-value"),
			ReconciliationEvidence: &recordsink.SQLReconciliationEvidence{
				SourceRows:           len(rows),
				TargetRows:           3,
				MissingRows:          1,
				WouldSoftDelete:      1,
				PartialSourceRisk:    true,
				ConfirmationRequired: true,
				ApplyAllowed:         true,
			},
		}
		if option.DryRun {
			result.ReconciliationEvidence.ConfirmationToken = confirmationToken
			return result, nil
		}
		if option.ReconciliationToken == "" {
			result.ReconciliationEvidence.ApplyAllowed = false
			result.ReconciliationEvidence.GuardCode = recordsink.ReconciliationGuardPreviewRequired
			return result, &recordsink.ReconciliationGuardError{Code: recordsink.ReconciliationGuardPreviewRequired}
		}
		return result, nil
	}

	preview := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", preview.Code, preview.Body)
	}
	var previewPayload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(preview.Body).Decode(&previewPayload); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !previewPayload.Success || previewPayload.Diagnostic.Operation != "preview" ||
		previewPayload.Diagnostic.Result == nil || !previewPayload.Diagnostic.Result.DryRun ||
		previewPayload.Diagnostic.Result.Reconciliation != recordsink.SoftDeleteMissing ||
		previewPayload.Diagnostic.Result.Inserted != 2 || previewPayload.Diagnostic.Result.ElapsedMS != 0 {
		t.Fatalf("unexpected preview diagnostic: %#v", previewPayload)
	}
	evidence := previewPayload.Diagnostic.Result.ReconciliationEvidence
	if evidence == nil || evidence.ConfirmationToken != confirmationToken ||
		!evidence.ConfirmationRequired || !evidence.ApplyAllowed ||
		evidence.SourceRows != 2 || evidence.TargetRows != 3 ||
		evidence.MissingRows != 1 || evidence.WouldSoftDelete != 1 ||
		!evidence.PartialSourceRisk {
		t.Fatalf("preview omitted safe reconciliation evidence: %#v", evidence)
	}

	rejectedRequests := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  int
	}{
		{"missing content type", "", []byte(`{"confirm":"manual_sync"}`), http.StatusUnsupportedMediaType},
		{"missing confirmation", "application/json", []byte(`{}`), http.StatusBadRequest},
		{"wrong confirmation", "application/json", []byte(`{"confirm":"yes"}`), http.StatusBadRequest},
		{"unknown field", "application/json", []byte(`{"confirm":"manual_sync","dsn":"forbidden"}`), http.StatusBadRequest},
		{"malformed reconciliation token", "application/json", []byte(`{"confirm":"manual_sync","reconciliation_token":"sha256:not-a-valid-plan"}`), http.StatusBadRequest},
		{"uppercase reconciliation token", "application/json", []byte(`{"confirm":"manual_sync","reconciliation_token":"sha256:` + strings.Repeat("A", 64) + `"}`), http.StatusBadRequest},
		{"padded reconciliation token", "application/json", []byte(`{"confirm":"manual_sync","reconciliation_token":" ` + confirmationToken + `"}`), http.StatusBadRequest},
		{"multiple objects", "application/json", []byte(`{"confirm":"manual_sync"} {}`), http.StatusBadRequest},
		{"oversized", "application/json", []byte(`{"confirm":"manual_sync","padding":"` + strings.Repeat("x", maximumSQLRequestBytes) + `"}`), http.StatusBadRequest},
	}
	for _, test := range rejectedRequests {
		t.Run(test.name, func(t *testing.T) {
			request := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", test.body)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := serveSQLRequest(server, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
		})
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("sync called %d times before confirmation, want preview only", calls)
	}
	mu.Unlock()

	missingTokenRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", []byte(`{"confirm":"manual_sync"}`))
	missingTokenRequest.Header.Set("Content-Type", "application/json")
	missingToken := serveSQLRequest(server, missingTokenRequest)
	if missingToken.Code != http.StatusConflict {
		t.Fatalf("soft-delete sync without preview token status=%d, want 409: %s", missingToken.Code, missingToken.Body)
	}
	var missingTokenPayload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(missingToken.Body).Decode(&missingTokenPayload); err != nil {
		t.Fatalf("decode missing-token guard: %v", err)
	}
	if missingTokenPayload.Success || missingTokenPayload.Diagnostic.Failure == nil ||
		missingTokenPayload.Diagnostic.Failure.Code != string(recordsink.SQLFailureReconciliation) ||
		missingTokenPayload.Diagnostic.Failure.Reason != recordsink.ReconciliationGuardPreviewRequired ||
		missingTokenPayload.Diagnostic.Result == nil ||
		missingTokenPayload.Diagnostic.Result.ReconciliationEvidence == nil ||
		missingTokenPayload.Diagnostic.Result.ReconciliationEvidence.ConfirmationToken != "" {
		t.Fatalf("missing-token guard was unsafe or incomplete: %#v", missingTokenPayload)
	}

	confirmedBody := []byte(fmt.Sprintf(`{"confirm":"manual_sync","reconciliation_token":%q}`, confirmationToken))
	confirmedRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", confirmedBody)
	confirmedRequest.Header.Set("Content-Type", "application/json; charset=utf-8")
	confirmed := serveSQLRequest(server, confirmedRequest)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmed sync status=%d: %s", confirmed.Code, confirmed.Body)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || len(options) != 3 || !options[0].DryRun || options[1].DryRun || options[2].DryRun {
		t.Fatalf("unexpected SQL sink calls/options: calls=%d options=%#v", calls, options)
	}
	for index, option := range options {
		if option.DSN != sqlTestDSN || option.Table != "products" || option.KeyField != "Code" ||
			option.Batch != 2 || option.Reconciliation != recordsink.SoftDeleteMissing ||
			option.MySQLTLS.CAFile != sqlTestCAPath || option.MySQLTLS.ServerName != sqlTestServerName {
			t.Fatalf("sink option[%d] did not use shared protected configuration: %#v", index, option)
		}
	}
	if options[0].ReconciliationToken != "" || options[1].ReconciliationToken != "" ||
		options[2].ReconciliationToken != confirmationToken {
		t.Fatalf("preview token was not passed only to the confirmed apply: %#v", options)
	}
	wantCodes := []string{"001", "002"}
	for index, codes := range rowCodes {
		if fmt.Sprint(codes) != fmt.Sprint(wantCodes) {
			t.Fatalf("sink call[%d] codes=%v, want %v", index, codes, wantCodes)
		}
	}
	if value, found := responseContainsAny(confirmed, sqlTestDSN, "super-secret-password", sqlTestCAPath, sqlTestServerName); found {
		t.Fatalf("sync exposed protected value %q: %s", value, confirmed.Body)
	}
}

func TestSQLTargetFailedSyncCannotReportPartialSuccessOrRawError(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	invalidEvidenceToken := "sha256:must-not-cross-the-http-boundary"
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		return recordsink.SQLResult{
			Inserted:  1,
			Updated:   1,
			Unchanged: -1,
			Deleted:   9,
			Failed:    -1,
			ElapsedMS: -1,
			ReconciliationEvidence: &recordsink.SQLReconciliationEvidence{
				SourceRows:           -3,
				ProtectedRows:        -2,
				TargetRows:           -1,
				ConfirmationRequired: true,
				ApplyAllowed:         true,
				ConfirmationToken:    invalidEvidenceToken,
				GuardCode:            recordsink.ReconciliationGuardCode("raw-internal-guard"),
			},
		}, errors.New("raw driver error at " + sqlTestDSN)
	}
	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("failed sync status=%d, want 502: %s", response.Code, response.Body)
	}
	if value, found := responseContainsAny(response, sqlTestDSN, "super-secret-password", "raw driver error", invalidEvidenceToken, "raw-internal-guard"); found {
		t.Fatalf("sync failure exposed protected value %q: %s", value, response.Body)
	}
	var payload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode failed sync: %v", err)
	}
	result := payload.Diagnostic.Result
	if payload.Success || result == nil || result.Inserted != 0 || result.Updated != 0 ||
		result.Unchanged != 0 || result.Deleted != 0 || result.Failed != 2 ||
		result.ElapsedMS != 0 || !result.DryRun || result.Reconciliation != recordsink.SoftDeleteMissing {
		t.Fatalf("failed sync exposed partial-looking result: %#v", payload)
	}
	evidence := result.ReconciliationEvidence
	if evidence == nil || evidence.SourceRows != 0 || evidence.ProtectedRows != 0 ||
		evidence.TargetRows != 0 || evidence.ApplyAllowed || evidence.ConfirmationToken != "" ||
		evidence.GuardCode != recordsink.ReconciliationGuardUnknown {
		t.Fatalf("failed sync exposed unsafe reconciliation evidence: %#v", evidence)
	}
}

func TestSQLTargetBlocksBrowserHardDeleteAndIrrelevantTokens(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	calls := 0
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		calls++
		return recordsink.SQLResult{}, nil
	}
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Export.Reconciliation = string(recordsink.DeleteMissing)
	}); err != nil {
		t.Fatalf("set hard-delete mode: %v", err)
	}
	hardDeleteRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", []byte(`{"confirm":"manual_sync"}`))
	hardDeleteRequest.Header.Set("Content-Type", "application/json")
	hardDelete := serveSQLRequest(server, hardDeleteRequest)
	if hardDelete.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hard-delete sync status=%d, want 422: %s", hardDelete.Code, hardDelete.Body)
	}
	if calls != 0 {
		t.Fatalf("browser hard-delete request reached the SQL sink %d times", calls)
	}

	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Export.Reconciliation = string(recordsink.UpsertOnly)
	}); err != nil {
		t.Fatalf("set upsert-only mode: %v", err)
	}
	token := recordsink.SoftDeleteConfirmationTokenPrefix + strings.Repeat("b", 64)
	unexpectedTokenBody := []byte(fmt.Sprintf(`{"confirm":"manual_sync","reconciliation_token":%q}`, token))
	unexpectedTokenRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", unexpectedTokenBody)
	unexpectedTokenRequest.Header.Set("Content-Type", "application/json")
	unexpectedToken := serveSQLRequest(server, unexpectedTokenRequest)
	if unexpectedToken.Code != http.StatusBadRequest {
		t.Fatalf("upsert with irrelevant token status=%d, want 400: %s", unexpectedToken.Code, unexpectedToken.Body)
	}
	if calls != 0 {
		t.Fatalf("irrelevant preview token reached the SQL sink %d times", calls)
	}
}

func TestSQLOperationsSerializeMutatingAndProbeRequests(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	syncEntered := make(chan struct{})
	releaseSync := make(chan struct{})
	probeEntered := make(chan struct{})
	server.sqlOperations.sync = func(ctx context.Context, _ recordsink.SQLOptions, rows []map[string]interface{}) (recordsink.SQLResult, error) {
		close(syncEntered)
		select {
		case <-releaseSync:
			return recordsink.SQLResult{Inserted: len(rows)}, nil
		case <-ctx.Done():
			return recordsink.SQLResult{}, ctx.Err()
		}
	}
	server.sqlOperations.probe = func(context.Context, recordsink.SQLOptions) (recordsink.SQLProbeResult, error) {
		close(probeEntered)
		return recordsink.SQLProbeResult{Connected: true, Driver: "mysql"}, nil
	}

	previewDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		previewDone <- serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	}()
	select {
	case <-syncEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not enter the SQL sink")
	}

	statusResponse := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil))
	var status sqlTargetStatus
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("busy status endpoint=%d: %s", statusResponse.Code, statusResponse.Body)
	}
	if err := json.NewDecoder(statusResponse.Body).Decode(&status); err != nil {
		t.Fatalf("decode busy status: %v", err)
	}
	if !status.Busy {
		t.Fatalf("status did not report active SQL operation: %#v", status)
	}

	probeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		probeDone <- serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/test", nil))
	}()
	select {
	case <-probeEntered:
		t.Fatal("probe entered while preview held the serialized operation permit")
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseSync)

	select {
	case response := <-previewDone:
		if response.Code != http.StatusOK {
			t.Fatalf("preview status=%d: %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not complete")
	}
	select {
	case <-probeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not enter after preview released the operation permit")
	}
	select {
	case response := <-probeDone:
		if response.Code != http.StatusOK {
			t.Fatalf("probe status=%d: %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not complete")
	}
}

func TestSQLTargetSourceFailureAndRouteMethodsAreSafe(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	server.dataSourceMu.Lock()
	source := server.dataSource
	server.dataSource = nil
	server.dataSourceMu.Unlock()
	t.Cleanup(func() {
		server.dataSourceMu.Lock()
		server.dataSource = source
		server.dataSourceMu.Unlock()
	})
	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("source failure status=%d, want 503: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "data source is not initialized") {
		t.Fatalf("source failure exposed internal error: %s", response.Body)
	}

	methodResponse := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodDelete, "/api/sql-target/status", nil))
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected method status=%d, want 405: %s", methodResponse.Code, methodResponse.Body)
	}
}
