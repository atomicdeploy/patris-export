package recordsink

import (
	"context"
	"crypto/x509"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestClassifySQLErrorUsesStableRetryCategories(t *testing.T) {
	tests := []struct {
		name      string
		stage     SQLFailureStage
		err       error
		code      SQLFailureCode
		retryable bool
	}{
		{name: "cancelled", stage: SQLStageProbe, err: context.Canceled, code: SQLFailureCancelled},
		{name: "deadline", stage: SQLStageConnect, err: context.DeadlineExceeded, code: SQLFailureTimeout, retryable: true},
		{name: "bad connection", stage: SQLStageSync, err: driver.ErrBadConn, code: SQLFailureUnavailable, retryable: true},
		{name: "invalid connection", stage: SQLStageConnect, err: mysql.ErrInvalidConn, code: SQLFailureUnavailable, retryable: true},
		{name: "authentication", stage: SQLStageConnect, err: &mysql.MySQLError{Number: 1045, Message: "secret account rejected"}, code: SQLFailureAuthentication},
		{name: "permission", stage: SQLStageSchema, err: &mysql.MySQLError{Number: 1142, Message: "private table denied"}, code: SQLFailurePermission},
		{name: "constraint", stage: SQLStageSync, err: &mysql.MySQLError{Number: 1062, Message: "duplicate private value"}, code: SQLFailureConstraint},
		{name: "deadlock", stage: SQLStageSync, err: &mysql.MySQLError{Number: 1213, Message: "transaction details"}, code: SQLFailureTransient, retryable: true},
		{name: "too many connections", stage: SQLStageConnect, err: &mysql.MySQLError{Number: 1040, Message: "server details"}, code: SQLFailureUnavailable, retryable: true},
		{name: "network timeout", stage: SQLStageConnect, err: &net.DNSError{IsTimeout: true}, code: SQLFailureTimeout, retryable: true},
		{name: "network unavailable", stage: SQLStageConnect, err: &net.DNSError{IsTemporary: true}, code: SQLFailureUnavailable, retryable: true},
		{name: "certificate hostname", stage: SQLStageConnect, err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "private.internal"}, code: SQLFailureTLSVerification},
		{name: "configuration", stage: SQLStageConfiguration, err: errors.New("raw config details"), code: SQLFailureInvalidConfiguration},
		{name: "schema", stage: SQLStageSchema, err: errors.New("raw schema details"), code: SQLFailureSchema},
		{name: "unknown", stage: SQLStageSync, err: errors.New("raw unknown details"), code: SQLFailureUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := ClassifySQLError(test.stage, fmt.Errorf("operation wrapper: %w", test.err))
			if failure.Code != test.code || failure.Stage != test.stage || failure.Retryable != test.retryable {
				t.Fatalf("classification = %+v, want code=%q stage=%q retryable=%t", failure, test.code, test.stage, test.retryable)
			}
			if !errors.Is(failure, test.err) {
				t.Fatalf("classified failure did not retain errors.Is relationship to %T", test.err)
			}
		})
	}
}

func TestSQLFailureSerializationAndTextNeverExposeCause(t *testing.T) {
	const secret = "mysql-secret-user:password@private-db.internal/private_schema"
	cause := fmt.Errorf("connect using %s: %w", secret, &mysql.MySQLError{Number: 1045, Message: secret})
	failure := ClassifySQLError(SQLStageConnect, cause)
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	outward := failure.Error() + "\n" + string(encoded)
	for _, forbidden := range []string{secret, "private-db", "private_schema", "password"} {
		if strings.Contains(outward, forbidden) {
			t.Fatalf("outward SQL diagnostic exposed %q: %s", forbidden, outward)
		}
	}
	if !strings.Contains(outward, string(SQLFailureAuthentication)) || !strings.Contains(outward, `"retryable":false`) {
		t.Fatalf("outward SQL diagnostic omitted stable fields: %s", outward)
	}
	if !errors.Is(failure, cause) {
		t.Fatal("classified failure did not retain its internal cause")
	}
}

func TestClassifySQLErrorPreservesAnExistingSafeFailure(t *testing.T) {
	original := ClassifySQLError(SQLStageTLS, errors.New("private CA path"))
	classified := ClassifySQLError(SQLStageSync, fmt.Errorf("outer: %w", original))
	if classified != original {
		t.Fatalf("existing safe failure was reclassified: got=%p want=%p", classified, original)
	}
}
