package recordsink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

const (
	defaultSQLConnectTimeout  = 10 * time.Second
	minimumSQLConnectTimeout  = 100 * time.Millisecond
	maximumSQLConnectTimeout  = 2 * time.Minute
	maximumCustomCABytes      = 4 * 1024 * 1024
	maximumServerVersionRunes = 128
)

// MySQLTLSOptions contains protected connection material. These values may be
// loaded from server-side configuration or environment variables, but must not
// be copied into browser configuration or outward diagnostics.
type MySQLTLSOptions struct {
	CAFile     string `json:"-"`
	ServerName string `json:"-"`
}

// SQLTLSState reports only whether the established connection is encrypted.
// It never includes certificate names, paths, ciphers, or target addresses.
type SQLTLSState string

const (
	SQLTLSUnknown   SQLTLSState = "unknown"
	SQLTLSEncrypted SQLTLSState = "encrypted"
	SQLTLSPlaintext SQLTLSState = "plaintext"
)

// SQLProbeResult is a bounded, non-secret result suitable for a future
// authenticated status API. The probe performs no schema or data mutation.
type SQLProbeResult struct {
	Connected     bool        `json:"connected"`
	Driver        string      `json:"driver"`
	Vendor        string      `json:"vendor,omitempty"`
	ServerVersion string      `json:"server_version,omitempty"`
	TLS           SQLTLSState `json:"tls"`
	ElapsedMS     int64       `json:"elapsed_ms"`
}

// ParseSQLConnectTimeout applies the same finite bounds used by config
// normalization. Invalid or empty values use the safe default.
func ParseSQLConnectTimeout(value string) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		duration = defaultSQLConnectTimeout
	}
	if duration < minimumSQLConnectTimeout {
		return minimumSQLConnectTimeout
	}
	if duration > maximumSQLConnectTimeout {
		return maximumSQLConnectTimeout
	}
	return duration
}

// ProbeSQLTarget opens the configured target, applies a finite connection
// deadline, and performs only ping and read-only server metadata queries.
func ProbeSQLTarget(ctx context.Context, options SQLOptions) (SQLProbeResult, error) {
	started := time.Now()
	driverName := normalizedSQLDriver(options.Driver)
	result := SQLProbeResult{Driver: driverName, TLS: SQLTLSUnknown}

	connectCtx, cancel := sqlConnectContext(ctx, options.ConnectTimeout)
	defer cancel()
	db, err := openSQLTarget(connectCtx, options)
	if err != nil {
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result, err
	}
	defer db.Close()

	result, err = ProbeSQLDB(connectCtx, db, driverName)
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result, err
}

// ProbeSQLDB performs the non-mutating portion of a connection probe against
// an already-open database. It is exported so server/IPC layers can inject a
// managed connection without creating another probe implementation.
func ProbeSQLDB(ctx context.Context, db *sql.DB, driverName string) (result SQLProbeResult, err error) {
	started := time.Now()
	result.Driver = normalizedSQLDriver(driverName)
	result.TLS = SQLTLSUnknown
	defer func() { result.ElapsedMS = time.Since(started).Milliseconds() }()

	if db == nil {
		return result, ClassifySQLError(SQLStageConfiguration, errors.New("nil SQL database"))
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return result, ClassifySQLError(SQLStageConnect, err)
	}
	defer connection.Close()
	if err := connection.PingContext(ctx); err != nil {
		return result, ClassifySQLError(SQLStageConnect, err)
	}
	result.Connected = true
	if result.Driver != "mysql" {
		result.Vendor = result.Driver
		return result, nil
	}

	var version string
	if err := connection.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return result, ClassifySQLError(SQLStageProbe, err)
	}
	result.ServerVersion = safeServerVersion(version)
	result.Vendor = mysqlVendor(version)

	var statusName string
	var cipher sql.NullString
	if err := connection.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&statusName, &cipher); err == nil {
		if cipher.Valid && strings.TrimSpace(cipher.String) != "" {
			result.TLS = SQLTLSEncrypted
		} else {
			result.TLS = SQLTLSPlaintext
		}
	} else if ctx.Err() != nil {
		return result, ClassifySQLError(SQLStageProbe, ctx.Err())
	}
	return result, nil
}

func openSQLTarget(ctx context.Context, options SQLOptions) (*sql.DB, error) {
	driverName := normalizedSQLDriver(options.Driver)
	if strings.TrimSpace(options.DSN) == "" {
		return nil, ClassifySQLError(SQLStageConfiguration, errors.New("SQL DSN is required"))
	}
	if driverName != "mysql" {
		db, err := sql.Open(driverName, options.DSN)
		if err != nil {
			return nil, ClassifySQLError(SQLStageConfiguration, err)
		}
		return db, nil
	}

	config, err := mysqlConnectorConfig(ctx, options)
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, ClassifySQLError(SQLStageConfiguration, err)
	}
	return sql.OpenDB(connector), nil
}

func mysqlConnectorConfig(ctx context.Context, options SQLOptions) (*mysql.Config, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, ClassifySQLError(SQLStageConnect, err)
		}
	}
	config, err := mysql.ParseDSN(options.DSN)
	if err != nil {
		return nil, ClassifySQLError(SQLStageConfiguration, err)
	}
	if config.Timeout <= 0 {
		config.Timeout = normalizedSQLConnectTimeout(options.ConnectTimeout)
	}

	tlsOptions := options.MySQLTLS
	tlsOptions.CAFile = strings.TrimSpace(tlsOptions.CAFile)
	tlsOptions.ServerName = strings.TrimSpace(tlsOptions.ServerName)
	if tlsOptions.CAFile != "" || tlsOptions.ServerName != "" {
		tlsConfig, err := verifiedMySQLTLSConfig(tlsOptions)
		if err != nil {
			return nil, ClassifySQLError(SQLStageTLS, err)
		}
		// A protected CA/server-name setting always selects verified TLS and
		// disables any plaintext fallback or insecure mode present in the DSN.
		config.TLSConfig = ""
		config.TLS = tlsConfig
		config.AllowFallbackToPlaintext = false
	}

	if remaining, bounded := contextDeadlineRemaining(ctx); bounded {
		if remaining <= 0 {
			return nil, ClassifySQLError(SQLStageConnect, context.DeadlineExceeded)
		}
		config.Timeout = capSQLDriverTimeout(config.Timeout, remaining)
		config.ReadTimeout = capSQLDriverTimeout(config.ReadTimeout, remaining)
		config.WriteTimeout = capSQLDriverTimeout(config.WriteTimeout, remaining)
	}
	return config, nil
}

func contextDeadlineRemaining(ctx context.Context) (time.Duration, bool) {
	if ctx == nil {
		return 0, false
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return 0, false
	}
	remaining := time.Until(deadline)
	return remaining, true
}

func capSQLDriverTimeout(configured, remaining time.Duration) time.Duration {
	if configured <= 0 || configured > remaining {
		return remaining
	}
	return configured
}

func verifiedMySQLTLSConfig(options MySQLTLSOptions) (*tls.Config, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if options.CAFile != "" {
		pemBytes, err := readBoundedCustomCA(options.CAFile)
		if err != nil {
			return nil, err
		}
		if !rootCAs.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("custom CA file does not contain a valid PEM certificate")
		}
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
		ServerName: strings.TrimSpace(options.ServerName),
	}, nil
}

func readBoundedCustomCA(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open custom CA: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumCustomCABytes+1))
	if err != nil {
		return nil, fmt.Errorf("read custom CA: %w", err)
	}
	if len(data) > maximumCustomCABytes {
		return nil, errors.New("custom CA exceeds the size limit")
	}
	return data, nil
}

func sqlConnectContext(ctx context.Context, requested time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, normalizedSQLConnectTimeout(requested))
}

func normalizedSQLConnectTimeout(requested time.Duration) time.Duration {
	if requested <= 0 {
		requested = defaultSQLConnectTimeout
	}
	if requested < minimumSQLConnectTimeout {
		return minimumSQLConnectTimeout
	}
	if requested > maximumSQLConnectTimeout {
		return maximumSQLConnectTimeout
	}
	return requested
}

func normalizedSQLDriver(driverName string) string {
	driverName = strings.ToLower(strings.TrimSpace(driverName))
	if driverName == "" {
		return "mysql"
	}
	return driverName
}

func mysqlVendor(version string) string {
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return "mariadb"
	}
	return "mysql"
}

func safeServerVersion(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '\r' && r != '\n' {
			return r
		}
		return -1
	}, strings.TrimSpace(value))
	if utf8.RuneCountInString(value) <= maximumServerVersionRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximumServerVersionRunes])
}
