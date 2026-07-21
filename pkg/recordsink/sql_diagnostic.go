package recordsink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"

	"github.com/go-sql-driver/mysql"
)

// SQLFailureStage identifies the operation boundary that failed without
// exposing target details, SQL text, or driver messages.
type SQLFailureStage string

const (
	SQLStageConfiguration SQLFailureStage = "configuration"
	SQLStageTLS           SQLFailureStage = "tls"
	SQLStageConnect       SQLFailureStage = "connect"
	SQLStageProbe         SQLFailureStage = "probe"
	SQLStageSchema        SQLFailureStage = "schema"
	SQLStageSync          SQLFailureStage = "sync"
)

// SQLFailureCode is a stable, secret-safe category suitable for UI and API
// diagnostics. It deliberately does not include server-supplied error text.
type SQLFailureCode string

const (
	SQLFailureInvalidConfiguration SQLFailureCode = "invalid_configuration"
	SQLFailureTLSVerification      SQLFailureCode = "tls_verification_failed"
	SQLFailureAuthentication       SQLFailureCode = "authentication_failed"
	SQLFailurePermission           SQLFailureCode = "permission_denied"
	SQLFailureConstraint           SQLFailureCode = "constraint_rejected"
	SQLFailureTimeout              SQLFailureCode = "timeout"
	SQLFailureCancelled            SQLFailureCode = "cancelled"
	SQLFailureTransient            SQLFailureCode = "transient_database_error"
	SQLFailureUnavailable          SQLFailureCode = "database_unavailable"
	SQLFailureSchema               SQLFailureCode = "schema_incompatible"
	SQLFailureUnknown              SQLFailureCode = "unknown_database_error"
)

// SQLFailure is safe to serialize or log. The underlying cause remains
// available to errors.Is/errors.As for in-process control flow, but is never
// part of JSON or Error output.
type SQLFailure struct {
	Code      SQLFailureCode  `json:"code"`
	Stage     SQLFailureStage `json:"stage"`
	Retryable bool            `json:"retryable"`
	Message   string          `json:"message"`

	cause error
}

func (failure *SQLFailure) Error() string {
	if failure == nil {
		return ""
	}
	return fmt.Sprintf("SQL %s failed: %s (code=%s, retryable=%t)", failure.Stage, failure.Message, failure.Code, failure.Retryable)
}

func (failure *SQLFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// ClassifySQLError converts an internal driver or context failure into a
// stable outward diagnostic. It never copies err.Error() into the result.
func ClassifySQLError(stage SQLFailureStage, err error) *SQLFailure {
	if err == nil {
		return nil
	}
	var existing *SQLFailure
	if errors.As(err, &existing) {
		return existing
	}

	stage = normalizedSQLFailureStage(stage)
	code := classifySQLFailureCode(stage, err)
	return &SQLFailure{
		Code:      code,
		Stage:     stage,
		Retryable: sqlFailureRetryable(code),
		Message:   sqlFailureMessage(code),
		cause:     err,
	}
}

func normalizedSQLFailureStage(stage SQLFailureStage) SQLFailureStage {
	switch stage {
	case SQLStageConfiguration, SQLStageTLS, SQLStageConnect, SQLStageProbe, SQLStageSchema, SQLStageSync:
		return stage
	default:
		return SQLStageSync
	}
}

func classifySQLFailureCode(stage SQLFailureStage, err error) SQLFailureCode {
	switch {
	case errors.Is(err, context.Canceled):
		return SQLFailureCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return SQLFailureTimeout
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, mysql.ErrInvalidConn):
		return SQLFailureUnavailable
	}

	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) {
		return classifyMySQLErrorNumber(mysqlError.Number)
	}

	var hostnameError x509.HostnameError
	var authorityError x509.UnknownAuthorityError
	var certificateError x509.CertificateInvalidError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &hostnameError) || errors.As(err, &authorityError) || errors.As(err, &certificateError) || errors.As(err, &recordHeaderError) {
		return SQLFailureTLSVerification
	}

	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return SQLFailureTimeout
		}
		return SQLFailureUnavailable
	}

	switch stage {
	case SQLStageConfiguration:
		return SQLFailureInvalidConfiguration
	case SQLStageTLS:
		return SQLFailureTLSVerification
	case SQLStageSchema:
		return SQLFailureSchema
	default:
		return SQLFailureUnknown
	}
}

func classifyMySQLErrorNumber(number uint16) SQLFailureCode {
	switch number {
	case 1045, 1698:
		return SQLFailureAuthentication
	case 1044, 1142, 1143, 1227:
		return SQLFailurePermission
	case 1049:
		return SQLFailureInvalidConfiguration
	case 1048, 1062, 1364, 1406, 1451, 1452:
		return SQLFailureConstraint
	case 1053, 1158, 1159, 1160, 1161, 1205, 1213, 2006, 2013:
		return SQLFailureTransient
	case 1040:
		return SQLFailureUnavailable
	default:
		return SQLFailureUnknown
	}
}

func sqlFailureRetryable(code SQLFailureCode) bool {
	switch code {
	case SQLFailureTimeout, SQLFailureTransient, SQLFailureUnavailable:
		return true
	default:
		return false
	}
}

func sqlFailureMessage(code SQLFailureCode) string {
	switch code {
	case SQLFailureInvalidConfiguration:
		return "The SQL target configuration is invalid."
	case SQLFailureTLSVerification:
		return "The SQL target TLS connection could not be verified."
	case SQLFailureAuthentication:
		return "The SQL target rejected authentication."
	case SQLFailurePermission:
		return "The SQL target denied the required operation."
	case SQLFailureConstraint:
		return "The SQL target rejected data under an existing constraint."
	case SQLFailureTimeout:
		return "The SQL target operation timed out."
	case SQLFailureCancelled:
		return "The SQL target operation was cancelled."
	case SQLFailureTransient:
		return "The SQL target reported a transient database failure."
	case SQLFailureUnavailable:
		return "The SQL target is temporarily unavailable."
	case SQLFailureSchema:
		return "The SQL target schema is incompatible."
	default:
		return "The SQL target operation failed."
	}
}
