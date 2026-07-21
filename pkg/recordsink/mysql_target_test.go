package recordsink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMySQLConnectorConfigUsesVerifiedCustomCAAndBoundedTimeout(t *testing.T) {
	caPath, certificate := writeTestCA(t)
	options := SQLOptions{
		DSN:            "test_user:test_password@tcp(db.internal.example:3306)/shop?tls=skip-verify",
		ConnectTimeout: 25 * time.Millisecond,
		MySQLTLS: MySQLTLSOptions{
			CAFile:     caPath,
			ServerName: "mysql.service.internal",
		},
	}
	config, err := mysqlConnectorConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	if config.Timeout != minimumSQLConnectTimeout {
		t.Fatalf("connector timeout = %s, want bounded minimum %s", config.Timeout, minimumSQLConnectTimeout)
	}
	if config.TLS == nil || config.TLS.MinVersion != tls.VersionTLS12 || config.TLS.InsecureSkipVerify {
		t.Fatalf("connector did not require verified TLS 1.2+: %+v", config.TLS)
	}
	if config.TLS.ServerName != "mysql.service.internal" || config.TLSConfig != "" || config.AllowFallbackToPlaintext {
		t.Fatalf("connector retained an insecure/fallback TLS mode: TLSConfig=%q fallback=%t server=%q", config.TLSConfig, config.AllowFallbackToPlaintext, config.TLS.ServerName)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: config.TLS.RootCAs}); err != nil {
		t.Fatalf("custom CA was not added to the trust pool: %v", err)
	}
}

func TestMySQLConnectorConfigRespectsExplicitDSNTimeoutWithoutCustomTLS(t *testing.T) {
	config, err := mysqlConnectorConfig(SQLOptions{
		DSN:            "user:password@tcp(localhost:3306)/shop?timeout=3s&tls=true",
		ConnectTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Timeout != 3*time.Second {
		t.Fatalf("explicit DSN timeout = %s, want 3s", config.Timeout)
	}
	if config.TLSConfig != "true" || config.TLS == nil || config.TLS.InsecureSkipVerify {
		t.Fatalf("built-in verified DSN TLS mode was unexpectedly rewritten: TLSConfig=%q TLS=%+v", config.TLSConfig, config.TLS)
	}
}

func TestCustomCAFailuresReturnOnlySafeTypedDiagnostics(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private-customer-ca-name.pem")
	if err := os.WriteFile(privatePath, []byte("not a certificate; private contents"), 0600); err != nil {
		t.Fatal(err)
	}
	const dsn = "private_user:private_password@tcp(private-db.internal:3306)/private_schema"
	db, err := openSQLTarget(SQLOptions{DSN: dsn, MySQLTLS: MySQLTLSOptions{CAFile: privatePath}})
	if db != nil {
		db.Close()
		t.Fatal("invalid custom CA unexpectedly opened a target")
	}
	var failure *SQLFailure
	if !errors.As(err, &failure) || failure.Code != SQLFailureTLSVerification || failure.Stage != SQLStageTLS || failure.Retryable {
		t.Fatalf("invalid custom CA failure = %#v", err)
	}
	encoded, marshalErr := json.Marshal(failure)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	outward := failure.Error() + string(encoded)
	for _, forbidden := range []string{privatePath, dsn, "private_password", "private-db", "private_schema", "private contents"} {
		if strings.Contains(outward, forbidden) {
			t.Fatalf("custom CA diagnostic exposed %q: %s", forbidden, outward)
		}
	}
}

func TestSQLOptionsJSONCannotExposeProtectedConnectionMaterial(t *testing.T) {
	const dsn = "private_user:private_password@tcp(private-db.internal:3306)/private_schema"
	const caPath = "C:/protected/private-ca.pem"
	const serverName = "private-db.internal"
	encoded, err := json.Marshal(SQLOptions{
		Driver: "mysql",
		DSN:    dsn,
		MySQLTLS: MySQLTLSOptions{
			CAFile:     caPath,
			ServerName: serverName,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{dsn, caPath, serverName, "DSN", "MySQLTLS", "CAFile", "ServerName"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("serialized SQL options exposed %q: %s", forbidden, encoded)
		}
	}
}

func TestCustomCAReadIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized-ca.pem")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maximumCustomCABytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedCustomCA(path); err == nil {
		t.Fatal("oversized custom CA was accepted")
	}
}

func TestProbeSQLDBUsesOnlyReadOnlyQueriesAndReturnsSanitizedState(t *testing.T) {
	state := &probeDriverState{
		version: strings.Repeat("10.11.6-MariaDB ", 20) + "\nprivate-control-text",
		cipher:  "TLS_AES_256_GCM_SHA384",
	}
	db := sql.OpenDB(probeConnector{state: state})
	defer db.Close()

	result, err := ProbeSQLDB(context.Background(), db, "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Connected || result.Driver != "mysql" || result.Vendor != "mariadb" || result.TLS != SQLTLSEncrypted {
		t.Fatalf("probe result = %+v", result)
	}
	if strings.ContainsAny(result.ServerVersion, "\r\n") || utf8.RuneCountInString(result.ServerVersion) > maximumServerVersionRunes {
		t.Fatalf("server version was not sanitized and bounded: %q", result.ServerVersion)
	}
	if state.pings != 1 || state.execs != 0 {
		t.Fatalf("probe activity: pings=%d execs=%d", state.pings, state.execs)
	}
	wantQueries := []string{"SELECT VERSION()", "SHOW SESSION STATUS LIKE 'Ssl_cipher'"}
	if len(state.queries) != len(wantQueries) {
		t.Fatalf("probe queries = %#v", state.queries)
	}
	for index, query := range wantQueries {
		if state.queries[index] != query {
			t.Fatalf("probe query %d = %q, want %q", index, state.queries[index], query)
		}
	}
}

func TestProbeSQLDBTreatsTLSStatusAsBestEffortAndHonorsCancellation(t *testing.T) {
	t.Run("TLS status unavailable", func(t *testing.T) {
		state := &probeDriverState{version: "8.4.0", statusErr: errors.New("status unavailable")}
		db := sql.OpenDB(probeConnector{state: state})
		defer db.Close()
		result, err := ProbeSQLDB(context.Background(), db, "mysql")
		if err != nil || !result.Connected || result.Vendor != "mysql" || result.TLS != SQLTLSUnknown {
			t.Fatalf("best-effort TLS status result=%+v err=%v", result, err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		state := &probeDriverState{pingWait: true}
		db := sql.OpenDB(probeConnector{state: state})
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		result, err := ProbeSQLDB(ctx, db, "mysql")
		var failure *SQLFailure
		if result.Connected || !errors.As(err, &failure) || failure.Code != SQLFailureTimeout || !failure.Retryable {
			t.Fatalf("cancelled probe result=%+v err=%#v", result, err)
		}
	})

	t.Run("deadline during metadata", func(t *testing.T) {
		state := &probeDriverState{version: "8.4.0", statusWait: true}
		db := sql.OpenDB(probeConnector{state: state})
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		result, err := ProbeSQLDB(ctx, db, "mysql")
		var failure *SQLFailure
		if !result.Connected || !errors.As(err, &failure) || failure.Code != SQLFailureTimeout || !failure.Retryable {
			t.Fatalf("metadata timeout result=%+v err=%#v", result, err)
		}
	})
}

func TestProbeSQLTargetRejectsMissingDSNWithoutTargetDetails(t *testing.T) {
	result, err := ProbeSQLTarget(context.Background(), SQLOptions{Driver: "mysql"})
	var failure *SQLFailure
	if result.Connected || result.Driver != "mysql" || !errors.As(err, &failure) || failure.Code != SQLFailureInvalidConfiguration {
		t.Fatalf("missing target probe result=%+v err=%#v", result, err)
	}
}

func TestParseSQLConnectTimeoutUsesFiniteBounds(t *testing.T) {
	tests := map[string]time.Duration{
		"":        defaultSQLConnectTimeout,
		"invalid": defaultSQLConnectTimeout,
		"1ms":     minimumSQLConnectTimeout,
		"5s":      5 * time.Second,
		"24h":     maximumSQLConnectTimeout,
	}
	for input, expected := range tests {
		if got := ParseSQLConnectTimeout(input); got != expected {
			t.Fatalf("ParseSQLConnectTimeout(%q) = %s, want %s", input, got, expected)
		}
	}
}

func writeTestCA(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Patris test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, block, 0600); err != nil {
		t.Fatal(err)
	}
	return path, certificate
}

type probeDriverState struct {
	version    string
	cipher     string
	statusErr  error
	pingWait   bool
	statusWait bool
	pings      int
	queries    []string
	execs      int
}

type probeConnector struct {
	state *probeDriverState
}

func (connector probeConnector) Connect(context.Context) (driver.Conn, error) {
	return &probeConn{state: connector.state}, nil
}

func (connector probeConnector) Driver() driver.Driver { return probeDriver{} }

type probeDriver struct{}

func (probeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type probeConn struct {
	state *probeDriverState
}

func (conn *probeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("probe must not prepare statements")
}
func (conn *probeConn) Close() error { return nil }
func (conn *probeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("probe must not begin transactions")
}

func (conn *probeConn) Ping(ctx context.Context) error {
	conn.state.pings++
	if conn.state.pingWait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (conn *probeConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	conn.state.queries = append(conn.state.queries, query)
	switch query {
	case "SELECT VERSION()":
		return &probeRows{columns: []string{"VERSION()"}, values: [][]driver.Value{{conn.state.version}}}, nil
	case "SHOW SESSION STATUS LIKE 'Ssl_cipher'":
		if conn.state.statusWait {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if conn.state.statusErr != nil {
			return nil, conn.state.statusErr
		}
		return &probeRows{columns: []string{"Variable_name", "Value"}, values: [][]driver.Value{{"Ssl_cipher", conn.state.cipher}}}, nil
	default:
		return nil, errors.New("unexpected probe query")
	}
}

func (conn *probeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	conn.state.execs++
	return nil, errors.New("probe must not execute statements")
}

type probeRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *probeRows) Columns() []string { return rows.columns }
func (rows *probeRows) Close() error      { return nil }
func (rows *probeRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.index])
	rows.index++
	return nil
}
