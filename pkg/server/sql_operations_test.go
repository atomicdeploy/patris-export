package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
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

type legacySQLTestSource struct {
	rawCalls int
}

type sqlFingerprintCancelValue struct {
	cancel context.CancelFunc
}

func (value sqlFingerprintCancelValue) String() string {
	value.cancel()
	return "private-fingerprint-value"
}

func (source *legacySQLTestSource) GetRecords() ([]map[string]interface{}, error) {
	return source.GetRawRecords()
}

func (source *legacySQLTestSource) GetRawRecords() ([]map[string]interface{}, error) {
	source.rawCalls++
	return []map[string]interface{}{{"Code": "legacy"}}, nil
}

func (source *legacySQLTestSource) GetPath() string {
	return "legacy.json"
}

func (source *legacySQLTestSource) Close() error {
	return nil
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
	if server.sqlOperations.operatorToken != "" {
		request.Header.Set(sqlOperatorTokenHeader, server.sqlOperations.operatorToken)
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
		configured string
		token      string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "configured loopback requires token",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			configured: sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "configured loopback accepts exact token",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unconfigured direct loopback bootstrap",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unconfigured direct localhost bootstrap",
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
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote plaintext rejected",
			target:     "http://patris.example/api/sql-target/session",
			origin:     []string{"http://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote missing operator token",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			configured: sqlTestOperatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "edge token cannot authorize SQL",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestEdgeToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote dedicated token over TLS",
			target:     "https://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "trusted loopback reverse proxy",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "127.0.0.1:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			headers: map[string]string{
				"X-Forwarded-Proto": "https",
				"X-Forwarded-Host":  "patris.example",
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "proxy marker disables tokenless rewritten loopback",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.19"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "standard Forwarded marker disables tokenless rewritten loopback",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			headers:    map[string]string{"Forwarded": "for=203.0.113.19;proto=http"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nonstandard X-Forwarded marker disables tokenless rewritten loopback",
			target:     "http://127.0.0.1:18080/api/sql-target/session",
			origin:     []string{"http://127.0.0.1:18080"},
			remoteAddr: "127.0.0.1:41000",
			headers:    map[string]string{"X-Forwarded-Prefix": "/patris"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "untrusted peer cannot spoof forwarded TLS",
			target:     "http://patris.example/api/sql-target/session",
			origin:     []string{"https://patris.example"},
			remoteAddr: "203.0.113.19:41000",
			configured: sqlTestOperatorToken,
			token:      sqlTestOperatorToken,
			headers:    map[string]string{"X-Forwarded-Proto": "https"},
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
			server.sqlOperations.operatorToken = test.configured
			request := httptest.NewRequest(http.MethodPost, test.target, nil)
			request.RemoteAddr = test.remoteAddr
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			if test.token != "" {
				request.Header.Set(sqlOperatorTokenHeader, test.token)
			}
			for name, value := range test.headers {
				request.Header.Set(name, value)
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

func TestSQLPreviewGrantCannotOutliveOrReappearAfterSessionRevoke(t *testing.T) {
	state := newSQLOperationsState()
	now := time.Now().UTC().Truncate(time.Second)
	state.now = func() time.Time { return now }
	sessionID, _, expiresAt, err := state.createSession("http://127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	sessionHash := sha256.Sum256([]byte(sessionID))
	grant := sqlIssuedPreviewGrant{
		grantHash:   sha256.Sum256([]byte(strings.Repeat("g", 43))),
		sessionHash: sessionHash,
		expiresAt:   expiresAt,
	}
	if !state.issuePreviewGrant(grant) {
		t.Fatal("active session could not issue preview grant")
	}
	state.revokeSession(sessionID)
	if got := state.takePreviewGrant(); got != nil {
		t.Fatal("revoked session retained preview grant")
	}
	if state.issuePreviewGrant(grant) {
		t.Fatal("revoked session reissued preview grant")
	}
}

func TestSQLTargetRequiresExplicitMySQLTableAndSafeMode(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Convert.Table = ""
		cfg.Export.MySQLTable = ""
		cfg.Export.SQLiteTable = "sqlite_only_must_not_be_selected"
	}); err != nil {
		t.Fatalf("configure table fallback: %v", err)
	}
	credentials := createSQLTestSession(t, server, false)
	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("fallback status endpoint=%d: %s", response.Code, response.Body)
	}
	var status sqlTargetStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode fallback status: %v", err)
	}
	if status.TableConfigured {
		t.Fatalf("status treated an implicit or SQLite-only table as configured: %#v", status)
	}
	sourceCalls := 0
	sinkCalls := 0
	server.sqlOperations.records = func(context.Context) (recordpipe.Result, error) {
		sourceCalls++
		return recordpipe.Result{}, nil
	}
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		sinkCalls++
		return recordsink.SQLResult{}, nil
	}
	preview := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if preview.Code != http.StatusUnprocessableEntity || !strings.Contains(preview.Body.String(), "target_table_required") {
		t.Fatalf("implicit-table preview status=%d, want safe 422: %s", preview.Code, preview.Body)
	}
	syncRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", []byte(`{"confirm":"manual_sync"}`))
	syncRequest.Header.Set("Content-Type", "application/json")
	syncResponse := serveSQLRequest(server, syncRequest)
	if syncResponse.Code != http.StatusUnprocessableEntity || !strings.Contains(syncResponse.Body.String(), "target_table_required") {
		t.Fatalf("implicit-table sync status=%d, want safe 422: %s", syncResponse.Code, syncResponse.Body)
	}
	if sourceCalls != 0 || sinkCalls != 0 {
		t.Fatalf("implicit target reached source=%d or sink=%d", sourceCalls, sinkCalls)
	}

	for _, malformedTable := range []string{
		"!!!",
		"sales.products",
		"1products",
		"_products",
		"products_",
		strings.Repeat("a", 65),
	} {
		if err := server.config.Update(func(cfg *appconfig.Config) {
			cfg.Convert.Table = malformedTable
			cfg.Export.MySQLTable = ""
		}); err != nil {
			t.Fatalf("configure malformed table %q: %v", malformedTable, err)
		}
		response = serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/status", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("malformed-table %q status endpoint=%d: %s", malformedTable, response.Code, response.Body)
		}
		if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
			t.Fatalf("decode malformed-table %q status: %v", malformedTable, err)
		}
		if status.TableConfigured {
			t.Fatalf("status treated malformed table %q as configured: %#v", malformedTable, status)
		}
		preview = serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
		if preview.Code != http.StatusUnprocessableEntity || !strings.Contains(preview.Body.String(), "target_table_invalid") {
			t.Fatalf("malformed-table %q preview status=%d, want safe 422: %s", malformedTable, preview.Code, preview.Body)
		}
		syncRequest = newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", []byte(`{"confirm":"manual_sync"}`))
		syncRequest.Header.Set("Content-Type", "application/json")
		syncResponse = serveSQLRequest(server, syncRequest)
		if syncResponse.Code != http.StatusUnprocessableEntity || !strings.Contains(syncResponse.Body.String(), "target_table_invalid") {
			t.Fatalf("malformed-table %q sync status=%d, want safe 422: %s", malformedTable, syncResponse.Code, syncResponse.Body)
		}
		if sourceCalls != 0 || sinkCalls != 0 {
			t.Fatalf("malformed target %q reached source=%d or sink=%d", malformedTable, sourceCalls, sinkCalls)
		}
	}
	if mode := safeSQLReconciliationMode(recordsink.ReconciliationMode("future_or_corrupt_mode")); mode != recordsink.UpsertOnly {
		t.Fatalf("unknown reconciliation mode=%q, want safe %q", mode, recordsink.UpsertOnly)
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
	previewGrant := previewPayload.Diagnostic.PreviewGrant
	if !validSQLPreviewGrant(previewGrant) ||
		previewPayload.Diagnostic.PreviewGrantExpiresAt == nil ||
		!previewPayload.Diagnostic.PreviewGrantExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("preview omitted its short-lived one-time grant: %#v", previewPayload.Diagnostic)
	}
	evidence := previewPayload.Diagnostic.Result.ReconciliationEvidence
	if evidence == nil || evidence.ConfirmationToken != confirmationToken ||
		!evidence.ConfirmationRequired || !evidence.ApplyAllowed ||
		evidence.SourceRows != 2 || evidence.TargetRows != 3 ||
		evidence.MissingRows != 1 || evidence.WouldSoftDelete != 1 ||
		!evidence.PartialSourceRisk {
		t.Fatalf("preview omitted safe reconciliation evidence: %#v", evidence)
	}
	lastPreview := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodGet, "/api/sql-target/last-result", nil))
	if lastPreview.Code != http.StatusOK {
		t.Fatalf("cached preview status=%d: %s", lastPreview.Code, lastPreview.Body)
	}
	if strings.Contains(lastPreview.Body.String(), confirmationToken) ||
		strings.Contains(lastPreview.Body.String(), previewGrant) ||
		strings.Contains(lastPreview.Body.String(), `"preview_grant"`) {
		t.Fatalf("cached preview replayed a direct-response authorization token: %s", lastPreview.Body)
	}
	var cachedPreviewPayload struct {
		Available  bool                   `json:"available"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(lastPreview.Body).Decode(&cachedPreviewPayload); err != nil {
		t.Fatalf("decode cached preview: %v", err)
	}
	cachedEvidence := cachedPreviewPayload.Diagnostic.Result.ReconciliationEvidence
	if !cachedPreviewPayload.Available || cachedEvidence == nil ||
		cachedPreviewPayload.Diagnostic.PreviewGrant != "" ||
		cachedPreviewPayload.Diagnostic.PreviewGrantExpiresAt != nil ||
		cachedEvidence.ConfirmationToken != "" || cachedEvidence.SourceRows != evidence.SourceRows ||
		cachedEvidence.TargetRows != evidence.TargetRows || cachedEvidence.MissingRows != evidence.MissingRows ||
		cachedEvidence.WouldSoftDelete != evidence.WouldSoftDelete ||
		cachedEvidence.ApplyAllowed ||
		cachedEvidence.GuardCode != recordsink.ReconciliationGuardPreviewRequired {
		t.Fatalf("cached preview lost safe evidence or retained its token: %#v", cachedPreviewPayload)
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
		{"malformed preview grant", "application/json", []byte(`{"confirm":"manual_sync","preview_grant":"not-base64url"}`), http.StatusBadRequest},
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

	refreshedPreview := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if refreshedPreview.Code != http.StatusOK {
		t.Fatalf("refreshed preview status=%d: %s", refreshedPreview.Code, refreshedPreview.Body)
	}
	var refreshedPayload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(refreshedPreview.Body).Decode(&refreshedPayload); err != nil {
		t.Fatalf("decode refreshed preview: %v", err)
	}
	refreshedGrant := refreshedPayload.Diagnostic.PreviewGrant
	if !refreshedPayload.Success || !validSQLPreviewGrant(refreshedGrant) {
		t.Fatalf("refreshed preview omitted grant: %#v", refreshedPayload)
	}
	grantWithoutPlanBody := []byte(fmt.Sprintf(
		`{"confirm":"manual_sync","preview_grant":%q}`,
		refreshedGrant,
	))
	grantWithoutPlanRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", grantWithoutPlanBody)
	grantWithoutPlanRequest.Header.Set("Content-Type", "application/json")
	grantWithoutPlan := serveSQLRequest(server, grantWithoutPlanRequest)
	if grantWithoutPlan.Code != http.StatusConflict ||
		!strings.Contains(grantWithoutPlan.Body.String(), string(recordsink.ReconciliationGuardPreviewRequired)) {
		t.Fatalf("soft-delete grant without exact-plan token status=%d, want 409: %s", grantWithoutPlan.Code, grantWithoutPlan.Body)
	}

	finalPreview := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if finalPreview.Code != http.StatusOK {
		t.Fatalf("final preview status=%d: %s", finalPreview.Code, finalPreview.Body)
	}
	var finalPreviewPayload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(finalPreview.Body).Decode(&finalPreviewPayload); err != nil {
		t.Fatalf("decode final preview: %v", err)
	}
	finalGrant := finalPreviewPayload.Diagnostic.PreviewGrant
	if !finalPreviewPayload.Success || !validSQLPreviewGrant(finalGrant) {
		t.Fatalf("final preview omitted grant: %#v", finalPreviewPayload)
	}

	confirmedBody := []byte(fmt.Sprintf(
		`{"confirm":"manual_sync","preview_grant":%q,"reconciliation_token":%q}`,
		finalGrant,
		confirmationToken,
	))
	confirmedRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", confirmedBody)
	confirmedRequest.Header.Set("Content-Type", "application/json; charset=utf-8")
	confirmed := serveSQLRequest(server, confirmedRequest)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmed sync status=%d: %s", confirmed.Code, confirmed.Body)
	}
	replayedRequest := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", confirmedBody)
	replayedRequest.Header.Set("Content-Type", "application/json")
	replayed := serveSQLRequest(server, replayedRequest)
	if replayed.Code != http.StatusConflict ||
		!strings.Contains(replayed.Body.String(), string(recordsink.ReconciliationGuardPreviewRequired)) {
		t.Fatalf("replayed grant status=%d, want generic preview-required 409: %s", replayed.Code, replayed.Body)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 4 || len(options) != 4 ||
		!options[0].DryRun || !options[1].DryRun || !options[2].DryRun || options[3].DryRun {
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
		options[2].ReconciliationToken != "" || options[3].ReconciliationToken != confirmationToken {
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

func TestSQLTargetUpsertGrantIsOneTimeSessionConfigSourceAndExpiryBound(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	server.sqlOperations.now = func() time.Time { return now }
	firstSession := createSQLTestSession(t, server, false)
	secondSession := createSQLTestSession(t, server, false)
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Export.Reconciliation = string(recordsink.UpsertOnly)
	}); err != nil {
		t.Fatalf("set upsert-only mode: %v", err)
	}

	var sourceValue interface{} = "alpha"
	server.sqlOperations.records = func(context.Context) (recordpipe.Result, error) {
		return recordpipe.Result{
			Rows:     []map[string]interface{}{{"Code": "001", "Name": sourceValue}},
			KeyField: "Code",
			Raw:      true,
		}, nil
	}
	sinkCalls := 0
	server.sqlOperations.sync = func(_ context.Context, options recordsink.SQLOptions, rows []map[string]interface{}) (recordsink.SQLResult, error) {
		sinkCalls++
		return recordsink.SQLResult{Inserted: len(rows), DryRun: options.DryRun}, nil
	}

	preview := func(t *testing.T) string {
		t.Helper()
		response := serveSQLRequest(server, newAuthenticatedSQLRequest(firstSession, http.MethodPost, "/api/sql-target/preview", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("upsert preview status=%d: %s", response.Code, response.Body)
		}
		var payload struct {
			Success    bool                   `json:"success"`
			Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upsert preview: %v", err)
		}
		if !payload.Success || !validSQLPreviewGrant(payload.Diagnostic.PreviewGrant) ||
			payload.Diagnostic.PreviewGrantExpiresAt == nil {
			t.Fatalf("upsert preview omitted grant: %#v", payload)
		}
		return payload.Diagnostic.PreviewGrant
	}
	apply := func(credentials sqlTestCredentials, grant string) *httptest.ResponseRecorder {
		body := []byte(fmt.Sprintf(`{"confirm":"manual_sync","preview_grant":%q}`, grant))
		request := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/sync", body)
		request.Header.Set("Content-Type", "application/json")
		return serveSQLRequest(server, request)
	}
	assertPreviewRequired := func(label string, response *httptest.ResponseRecorder) {
		if response.Code != http.StatusConflict ||
			!strings.Contains(response.Body.String(), string(recordsink.ReconciliationGuardPreviewRequired)) {
			t.Fatalf("%s status=%d, want generic preview-required 409: %s", label, response.Code, response.Body)
		}
		if value, found := responseContainsAny(response, sqlTestDSN, "super-secret-password", sqlTestCAPath); found {
			t.Fatalf("%s exposed protected value %q: %s", label, value, response.Body)
		}
	}

	firstGrant := preview(t)
	assertPreviewRequired("missing grant", apply(firstSession, ""))

	configGrant := preview(t)
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Export.MySQLDSN = "other_user:other-secret@tcp(second-db.internal:3306)/patris"
	}); err != nil {
		t.Fatalf("switch target DSN: %v", err)
	}
	assertPreviewRequired("target switch", apply(firstSession, configGrant))
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.Export.MySQLDSN = sqlTestDSN
	}); err != nil {
		t.Fatalf("restore target DSN: %v", err)
	}

	sourceGrant := preview(t)
	sourceValue = "changed-after-preview"
	assertPreviewRequired("source drift", apply(firstSession, sourceGrant))

	sourceValue = int64(1)
	typeGrant := preview(t)
	sourceValue = float64(1)
	assertPreviewRequired("source type drift", apply(firstSession, typeGrant))

	sessionGrant := preview(t)
	assertPreviewRequired("session switch", apply(secondSession, sessionGrant))

	expiredGrant := preview(t)
	now = now.Add(sqlPreviewGrantTTL + time.Second)
	assertPreviewRequired("expired grant", apply(firstSession, expiredGrant))

	validGrant := preview(t)
	applied := apply(firstSession, validGrant)
	if applied.Code != http.StatusOK {
		t.Fatalf("fresh bound upsert grant status=%d: %s", applied.Code, applied.Body)
	}
	assertPreviewRequired("replayed grant", apply(firstSession, validGrant))

	// Seven previews and one authorized apply reach the sink. Every rejected apply
	// is blocked before the sink.
	if sinkCalls != 8 {
		t.Fatalf("sink calls=%d, want 8 preview/apply calls", sinkCalls)
	}
	if firstGrant == configGrant {
		t.Fatal("independent previews reused an opaque grant")
	}
}

func TestSQLTargetSlowSyncBodyDoesNotHoldOperationPermit(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	probeEntered := make(chan struct{})
	server.sqlOperations.probe = func(context.Context, recordsink.SQLOptions) (recordsink.SQLProbeResult, error) {
		close(probeEntered)
		return recordsink.SQLProbeResult{Connected: true, Driver: "mysql"}, nil
	}

	reader, writer := io.Pipe()
	request := httptest.NewRequest(http.MethodPost, credentials.origin+"/api/sql-target/sync", reader)
	request.Header.Set("Origin", credentials.origin)
	request.Header.Set(sqlCSRFHeader, credentials.csrf)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(credentials.cookie)
	syncDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { syncDone <- serveSQLRequest(server, request) }()

	select {
	case response := <-syncDone:
		t.Fatalf("partial sync body unexpectedly completed: status=%d body=%s", response.Code, response.Body)
	case <-time.After(100 * time.Millisecond):
	}
	probeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		probeDone <- serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/test", nil))
	}()
	select {
	case <-probeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("slow sync body held the global SQL operation permit")
	}
	select {
	case response := <-probeDone:
		if response.Code != http.StatusOK {
			t.Fatalf("probe failed beside slow sync body: status=%d body=%s", response.Code, response.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not complete beside slow sync body")
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close partial request body: %v", err)
	}
	select {
	case response := <-syncDone:
		if response.Code != http.StatusBadRequest {
			t.Fatalf("partial sync body status=%d, want 400: %s", response.Code, response.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("partial sync body did not finish after EOF")
	}
}

func TestSQLSourcePreparationCancellationDoesNotWedgeOperations(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	sourceEntered := make(chan struct{})
	sourceExited := make(chan struct{})
	probeEntered := make(chan struct{})
	sinkCalls := 0
	server.sqlOperations.records = func(ctx context.Context) (recordpipe.Result, error) {
		close(sourceEntered)
		<-ctx.Done()
		close(sourceExited)
		return recordpipe.Result{}, ctx.Err()
	}
	server.sqlOperations.probe = func(context.Context, recordsink.SQLOptions) (recordsink.SQLProbeResult, error) {
		close(probeEntered)
		return recordsink.SQLProbeResult{Connected: true, Driver: "mysql"}, nil
	}
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		sinkCalls++
		return recordsink.SQLResult{}, nil
	}

	request := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil)
	requestContext, cancel := context.WithTimeout(request.Context(), 250*time.Millisecond)
	defer cancel()
	request = request.WithContext(requestContext)
	previewDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { previewDone <- serveSQLRequest(server, request) }()
	select {
	case <-sourceEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("source preparation did not start")
	}

	probeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		probeDone <- serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/test", nil))
	}()
	select {
	case <-probeEntered:
		t.Fatal("probe overlapped source preparation while the serialized permit was held")
	case <-time.After(100 * time.Millisecond):
	}

	var preview *httptest.ResponseRecorder
	select {
	case preview = <-previewDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled source preparation did not return promptly")
	}
	select {
	case <-sourceExited:
	default:
		t.Fatal("handler returned before the context-aware source exited")
	}
	select {
	case <-probeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not enter after the cancelled source released the operation permit")
	}
	select {
	case probe := <-probeDone:
		if probe.Code != http.StatusOK {
			t.Fatalf("probe failed after source cancellation: status=%d body=%s", probe.Code, probe.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not complete after source cancellation")
	}
	if preview.Code != http.StatusGatewayTimeout {
		t.Fatalf("cancelled source status=%d, want 504: %s", preview.Code, preview.Body)
	}
	var failed struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(preview.Body).Decode(&failed); err != nil {
		t.Fatalf("decode cancelled source response: %v", err)
	}
	if failed.Success || failed.Diagnostic.Failure == nil ||
		failed.Diagnostic.Failure.Code != string(recordsink.SQLFailureTimeout) ||
		failed.Diagnostic.Failure.Stage != "source" || sinkCalls != 0 {
		t.Fatalf("cancelled source reached sink or returned unsafe diagnostic: %#v sinkCalls=%d", failed, sinkCalls)
	}

	server.sqlOperations.records = func(ctx context.Context) (recordpipe.Result, error) {
		return server.RecordResultContext(ctx)
	}
	second := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if second.Code != http.StatusOK || sinkCalls != 1 {
		t.Fatalf("operation permit was not reusable: status=%d sinkCalls=%d body=%s", second.Code, sinkCalls, second.Body)
	}

	cancelled, cancelImmediately := context.WithCancel(context.Background())
	cancelImmediately()
	if _, err := server.RecordResultContext(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled RecordResultContext error=%v, want context.Canceled", err)
	}
}

func TestSQLSourceFingerprintCancellationDoesNotReachSink(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	request := newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil)
	requestContext, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(requestContext)

	server.sqlOperations.records = func(context.Context) (recordpipe.Result, error) {
		return recordpipe.Result{
			Rows: []map[string]interface{}{{
				"Code":  "A",
				"value": sqlFingerprintCancelValue{cancel: cancel},
			}},
			KeyField: "Code",
		}, nil
	}
	sinkCalls := 0
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		sinkCalls++
		return recordsink.SQLResult{}, nil
	}

	response := serveSQLRequest(server, request)
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("fingerprint cancellation status=%d, want 408: %s", response.Code, response.Body)
	}
	var payload struct {
		Success    bool                   `json:"success"`
		Diagnostic sqlOperationDiagnostic `json:"diagnostic"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode fingerprint cancellation: %v", err)
	}
	failure := payload.Diagnostic.Failure
	if payload.Success || payload.Diagnostic.Status != "failed" ||
		failure == nil ||
		failure.Code != string(recordsink.SQLFailureCancelled) ||
		failure.Stage != "source" ||
		failure.Retryable ||
		sinkCalls != 0 {
		t.Fatalf("unsafe fingerprint cancellation diagnostic=%#v sinkCalls=%d", payload, sinkCalls)
	}
	if strings.Contains(response.Body.String(), "private-fingerprint-value") {
		t.Fatalf("fingerprint cancellation exposed source value: %s", response.Body)
	}
}

func TestSQLSourcePreparationRejectsLegacyUnboundedDataSource(t *testing.T) {
	server := newSQLOperationsTestServer(t)
	credentials := createSQLTestSession(t, server, false)
	legacy := &legacySQLTestSource{}
	server.dataSourceMu.Lock()
	original := server.dataSource
	server.dataSource = legacy
	server.dataSourceMu.Unlock()
	t.Cleanup(func() {
		server.dataSourceMu.Lock()
		server.dataSource = original
		server.dataSourceMu.Unlock()
	})
	sinkCalls := 0
	server.sqlOperations.sync = func(context.Context, recordsink.SQLOptions, []map[string]interface{}) (recordsink.SQLResult, error) {
		sinkCalls++
		return recordsink.SQLResult{}, nil
	}

	response := serveSQLRequest(server, newAuthenticatedSQLRequest(credentials, http.MethodPost, "/api/sql-target/preview", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy source status=%d, want 503: %s", response.Code, response.Body)
	}
	if legacy.rawCalls != 0 || sinkCalls != 0 {
		t.Fatalf("bounded SQL request used legacy source calls=%d sink=%d", legacy.rawCalls, sinkCalls)
	}
	if strings.Contains(response.Body.String(), "bounded record reads") {
		t.Fatalf("legacy source response exposed internal detail: %s", response.Body)
	}

	result, err := server.RecordResultContext(context.Background())
	if err != nil || len(result.Rows) != 1 || legacy.rawCalls != 1 {
		t.Fatalf("legacy Background compatibility failed: rows=%d calls=%d err=%v", len(result.Rows), legacy.rawCalls, err)
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
