package recordsink

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

// SoftDeleteColumn is reserved for recordsink-managed tombstone state. Source
// records may not define or map a field with this name.
const SoftDeleteColumn = "patris_export_deleted"

const (
	// SoftDeleteConfirmationTokenPrefix identifies the only accepted digest
	// format for an exact soft-delete preview.
	SoftDeleteConfirmationTokenPrefix = "sha256:"
	// SoftDeleteConfirmationTokenLength is the exact byte length of the prefix
	// plus a lowercase SHA-256 hexadecimal digest.
	SoftDeleteConfirmationTokenLength = len(SoftDeleteConfirmationTokenPrefix) + sha256.Size*2
)

// ReconciliationGuardCode is a stable, non-secret reason why a soft-delete
// plan cannot be applied.
type ReconciliationGuardCode string

const (
	ReconciliationGuardEmptySource      ReconciliationGuardCode = "empty_source"
	ReconciliationGuardPreviewRequired  ReconciliationGuardCode = "preview_required"
	ReconciliationGuardPreviewMismatch  ReconciliationGuardCode = "preview_mismatch"
	ReconciliationGuardReservedField    ReconciliationGuardCode = "reserved_field_collision"
	ReconciliationGuardInvalidTombstone ReconciliationGuardCode = "invalid_tombstone_state"
	ReconciliationGuardUnknown          ReconciliationGuardCode = "safety_guard"
)

// SQLReconciliationEvidence is safe for operator output. It contains counts
// and an aggregate confirmation digest, never destination keys or connection
// material.
type SQLReconciliationEvidence struct {
	SourceRows           int                     `json:"source_rows"`
	ProtectedRows        int                     `json:"protected_rows"`
	TargetRows           int                     `json:"target_rows"`
	MissingRows          int                     `json:"missing_rows"`
	WouldSoftDelete      int                     `json:"would_soft_delete"`
	AlreadySoftDeleted   int                     `json:"already_soft_deleted"`
	WouldRestore         int                     `json:"would_restore"`
	PartialSourceRisk    bool                    `json:"partial_source_risk"`
	ConfirmationRequired bool                    `json:"confirmation_required"`
	ApplyAllowed         bool                    `json:"apply_allowed"`
	ConfirmationToken    string                  `json:"confirmation_token,omitempty"`
	GuardCode            ReconciliationGuardCode `json:"guard_code,omitempty"`
}

// ReconciliationGuardError deliberately excludes plan contents and supplied
// confirmation text from Error output.
type ReconciliationGuardError struct {
	Code ReconciliationGuardCode
}

func (err *ReconciliationGuardError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("SQL reconciliation blocked by safety guard (code=%s)", normalizedReconciliationGuardCode(err.Code))
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func prepareSoftDeleteRows(rows []map[string]interface{}, keyField string) ([]map[string]interface{}, error) {
	return prepareSoftDeleteRowsContext(context.Background(), rows, keyField)
}

func prepareSoftDeleteRowsContext(ctx context.Context, rows []map[string]interface{}, keyField string) ([]map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isSoftDeleteIdentifier(keyField) {
		return nil, &ReconciliationGuardError{Code: ReconciliationGuardReservedField}
	}
	prepared := make([]map[string]interface{}, len(rows))
	for index, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		copyRow := make(map[string]interface{}, len(row)+1)
		for field, value := range row {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if isSoftDeleteIdentifier(field) {
				return nil, &ReconciliationGuardError{Code: ReconciliationGuardReservedField}
			}
			copyRow[field] = value
		}
		copyRow[SoftDeleteColumn] = false
		prepared[index] = copyRow
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func buildSoftDeleteEvidence(
	ctx context.Context,
	queryer sqlQueryer,
	driver, table, keyField string,
	tableExists, markerExists bool,
	sourceRows []map[string]interface{},
	sourceKeys, keepKeys []string,
) (SQLReconciliationEvidence, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SQLReconciliationEvidence{}, "", err
	}
	evidence := SQLReconciliationEvidence{
		SourceRows:           len(sourceKeys),
		ProtectedRows:        len(keepKeys) - len(sourceKeys),
		ConfirmationRequired: true,
		ApplyAllowed:         len(sourceKeys) > 0,
	}
	if !tableExists {
		token, err := softDeleteConfirmationTokenContext(ctx, table, keyField, sourceRows, sourceKeys, keepKeys, nil)
		if err != nil {
			return evidence, "", err
		}
		return evidence, token, nil
	}
	if markerExists {
		if err := validateSoftDeleteColumn(ctx, queryer, driver, table); err != nil {
			return evidence, "", err
		}
	}

	sourceSet, err := stringSetContext(ctx, sourceKeys)
	if err != nil {
		return evidence, "", err
	}
	keepSet, err := stringSetContext(ctx, keepKeys)
	if err != nil {
		return evidence, "", err
	}
	fields := quoteIdent(driver, keyField)
	if markerExists {
		fields += ", " + quoteIdent(driver, SoftDeleteColumn)
	}
	query := fmt.Sprintf("SELECT %s FROM %s", fields, quoteIdent(driver, table))
	rows, err := queryer.QueryContext(ctx, query)
	if err != nil {
		return evidence, "", err
	}
	defer rows.Close()

	targetStates := make([]softDeleteTargetState, 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return evidence, "", err
		}
		var keyValue interface{}
		var markerValue interface{}
		if markerExists {
			err = rows.Scan(&keyValue, &markerValue)
		} else {
			err = rows.Scan(&keyValue)
		}
		if err != nil {
			return evidence, "", err
		}
		key := databaseKey(keyValue)
		if err := ctx.Err(); err != nil {
			return evidence, "", err
		}
		if strings.TrimSpace(key) == "" {
			return evidence, "", errors.New("soft-delete target contains an empty key")
		}
		deleted, err := softDeleteMarkerState(markerValue, markerExists)
		if err != nil {
			return evidence, "", err
		}
		targetStates = append(targetStates, softDeleteTargetState{Key: key, Deleted: deleted})
		if _, present := sourceSet[key]; present && deleted {
			evidence.WouldRestore++
		}
		if _, retained := keepSet[key]; retained {
			continue
		}
		evidence.MissingRows++
		if deleted {
			evidence.AlreadySoftDeleted++
		} else {
			evidence.WouldSoftDelete++
		}
	}
	if err := rows.Err(); err != nil {
		return evidence, "", err
	}
	if err := ctx.Err(); err != nil {
		return evidence, "", err
	}
	evidence.TargetRows = len(targetStates)
	evidence.PartialSourceRisk = evidence.MissingRows > 0
	token, err := softDeleteConfirmationTokenContext(ctx, table, keyField, sourceRows, sourceKeys, keepKeys, targetStates)
	if err != nil {
		return evidence, "", err
	}
	return evidence, token, nil
}

func authorizeSoftDelete(evidence *SQLReconciliationEvidence, expectedToken, suppliedToken string, dryRun bool) error {
	evidence.ConfirmationToken = ""
	evidence.GuardCode = ""
	if evidence.SourceRows == 0 {
		evidence.ApplyAllowed = false
		evidence.GuardCode = ReconciliationGuardEmptySource
		if dryRun {
			return nil
		}
		return &ReconciliationGuardError{Code: evidence.GuardCode}
	}
	evidence.ApplyAllowed = true
	if dryRun {
		evidence.ConfirmationToken = expectedToken
		return nil
	}
	if strings.TrimSpace(suppliedToken) == "" {
		evidence.ApplyAllowed = false
		evidence.GuardCode = ReconciliationGuardPreviewRequired
		return &ReconciliationGuardError{Code: evidence.GuardCode}
	}
	if suppliedToken != expectedToken {
		evidence.ApplyAllowed = false
		evidence.GuardCode = ReconciliationGuardPreviewMismatch
		return &ReconciliationGuardError{Code: evidence.GuardCode}
	}
	return nil
}

func softDeleteMarkerState(value interface{}, columnExists bool) (bool, error) {
	if !columnExists || value == nil {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(databaseText(value))) {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	default:
		return false, &ReconciliationGuardError{Code: ReconciliationGuardInvalidTombstone}
	}
}

func validateSoftDeleteColumn(ctx context.Context, queryer sqlQueryer, driver, table string) error {
	if isSQLite(driver) {
		rows, err := queryer.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(driver, table)))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue interface{}
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			if !strings.EqualFold(name, SoftDeleteColumn) {
				continue
			}
			normalized := strings.ToUpper(strings.TrimSpace(dataType))
			typeCompatible := strings.Contains(normalized, "INT") || strings.Contains(normalized, "BOOL")
			if typeCompatible && notNull == 1 && primaryKey == 0 && softDeleteDefaultIsFalse(defaultValue) {
				return nil
			}
			return &ReconciliationGuardError{Code: ReconciliationGuardInvalidTombstone}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return &ReconciliationGuardError{Code: ReconciliationGuardInvalidTombstone}
	}

	rows, err := queryer.QueryContext(ctx, fmt.Sprintf("SHOW COLUMNS FROM %s", quoteIdent(driver, table)))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var field, dataType, nullable, key string
		var defaultValue, extra interface{}
		if err := rows.Scan(&field, &dataType, &nullable, &key, &defaultValue, &extra); err != nil {
			return err
		}
		if !strings.EqualFold(field, SoftDeleteColumn) {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(dataType))
		typeCompatible := strings.HasPrefix(normalized, "tinyint") || normalized == "bool" || normalized == "boolean"
		keyCompatible := !strings.EqualFold(key, "PRI") && !strings.EqualFold(key, "UNI")
		if typeCompatible && strings.EqualFold(nullable, "NO") && keyCompatible && softDeleteDefaultIsFalse(defaultValue) {
			return nil
		}
		return &ReconciliationGuardError{Code: ReconciliationGuardInvalidTombstone}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return &ReconciliationGuardError{Code: ReconciliationGuardInvalidTombstone}
}

func isSoftDeleteIdentifier(value string) bool {
	return strings.EqualFold(NormalizeSQLIdentifier(value), SoftDeleteColumn)
}

func softDeleteDefaultIsFalse(value interface{}) bool {
	if value == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(databaseText(value)))
	for len(normalized) >= 2 && normalized[0] == '(' && normalized[len(normalized)-1] == ')' {
		normalized = strings.TrimSpace(normalized[1 : len(normalized)-1])
	}
	if len(normalized) >= 2 {
		first, last := normalized[0], normalized[len(normalized)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			normalized = strings.TrimSpace(normalized[1 : len(normalized)-1])
		}
	}
	return normalized == "0" || normalized == "false"
}

func normalizedReconciliationGuardCode(value ReconciliationGuardCode) ReconciliationGuardCode {
	switch value {
	case ReconciliationGuardEmptySource,
		ReconciliationGuardPreviewRequired,
		ReconciliationGuardPreviewMismatch,
		ReconciliationGuardReservedField,
		ReconciliationGuardInvalidTombstone:
		return value
	default:
		return ReconciliationGuardUnknown
	}
}

type softDeleteTargetState struct {
	Key     string
	Deleted bool
}

func softDeleteConfirmationToken(
	table, keyField string,
	sourceRows []map[string]interface{},
	sourceKeys, keepKeys []string,
	targetStates []softDeleteTargetState,
) string {
	token, _ := softDeleteConfirmationTokenContext(
		context.Background(),
		table,
		keyField,
		sourceRows,
		sourceKeys,
		keepKeys,
		targetStates,
	)
	return token
}

func softDeleteConfirmationTokenContext(
	ctx context.Context,
	table, keyField string,
	sourceRows []map[string]interface{},
	sourceKeys, keepKeys []string,
	targetStates []softDeleteTargetState,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest := sha256.New()
	for _, value := range []string{
		"patris-soft-delete-v2",
		strings.ToLower(strings.TrimSpace(table)),
		strings.ToLower(strings.TrimSpace(keyField)),
	} {
		if err := writeDigestStringContext(ctx, digest, value); err != nil {
			return "", err
		}
	}
	if err := writeSourceRowsDigestContext(ctx, digest, sourceRows, keyField); err != nil {
		return "", err
	}
	if err := writeDigestListContext(ctx, digest, sourceKeys); err != nil {
		return "", err
	}
	if err := writeDigestListContext(ctx, digest, keepKeys); err != nil {
		return "", err
	}
	if err := writeTargetStatesDigestContext(ctx, digest, targetStates); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return SoftDeleteConfirmationTokenPrefix + hex.EncodeToString(digest.Sum(nil)), nil
}

// SQLSourceFingerprint returns a deterministic, typed digest of the exact
// source rows and protected-key inputs supplied to a SQL sync. It deliberately
// distinguishes values that JSON alone collapses, such as int64 from float64,
// []byte from string, and time.Time from its formatted text.
func SQLSourceFingerprint(rows []map[string]interface{}, keyField string, protectedKeys []string) [sha256.Size]byte {
	fingerprint, _ := SQLSourceFingerprintContext(context.Background(), rows, keyField, protectedKeys)
	return fingerprint
}

// SQLSourceFingerprintContext is SQLSourceFingerprint with cooperative
// cancellation throughout field collection, stable ordering, typed value
// hashing, and protected-key hashing.
func SQLSourceFingerprintContext(
	ctx context.Context,
	rows []map[string]interface{},
	keyField string,
	protectedKeys []string,
) ([sha256.Size]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return [sha256.Size]byte{}, err
	}
	digest := sha256.New()
	if err := writeDigestStringContext(ctx, digest, "patris-sql-source-v1"); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := writeDigestStringContext(ctx, digest, strings.TrimSpace(keyField)); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := writeSourceRowsDigestContext(ctx, digest, rows, keyField); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := writeDigestListContext(ctx, digest, protectedKeys); err != nil {
		return [sha256.Size]byte{}, err
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, nil
}

func writeSourceRowsDigest(digest hash.Hash, rows []map[string]interface{}, keyField string) {
	_ = writeSourceRowsDigestContext(context.Background(), digest, rows, keyField)
}

func writeSourceRowsDigestContext(ctx context.Context, digest hash.Hash, rows []map[string]interface{}, keyField string) error {
	fields, err := sqlFieldsContext(ctx, rows, keyField)
	if err != nil {
		return err
	}
	writeDigestCount(digest, len(fields))
	for _, field := range fields {
		if err := writeDigestStringContext(ctx, digest, field); err != nil {
			return err
		}
	}

	ordered, err := stableDigestRowsContext(ctx, rows, keyField)
	if err != nil {
		return err
	}
	writeDigestCount(digest, len(ordered))
	for _, row := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, field := range fields {
			if err := ctx.Err(); err != nil {
				return err
			}
			value, exists := row[field]
			if !exists {
				value = nil
			}
			if err := writeSoftDeleteDigestValueContext(ctx, digest, value); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func softDeleteDigestValue(value interface{}) (string, string) {
	if value == nil {
		return "nil", ""
	}
	if timestamp, ok := value.(time.Time); ok {
		return "time.Time", timestamp.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%T", value), databaseText(value)
}

func writeTargetStatesDigest(digest hash.Hash, states []softDeleteTargetState) {
	_ = writeTargetStatesDigestContext(context.Background(), digest, states)
}

func writeTargetStatesDigestContext(ctx context.Context, digest hash.Hash, states []softDeleteTargetState) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ordered := append([]softDeleteTargetState(nil), states...)
	if err := stableSortTargetStatesContext(ctx, ordered); err != nil {
		return err
	}
	writeDigestCount(digest, len(ordered))
	for _, state := range ordered {
		if err := writeDigestStringContext(ctx, digest, state.Key); err != nil {
			return err
		}
		if state.Deleted {
			if err := writeDigestStringContext(ctx, digest, "1"); err != nil {
				return err
			}
		} else {
			if err := writeDigestStringContext(ctx, digest, "0"); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func writeDigestList(digest hash.Hash, values []string) {
	_ = writeDigestListContext(context.Background(), digest, values)
}

func writeDigestListContext(ctx context.Context, digest hash.Hash, values []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ordered := append([]string(nil), values...)
	if err := stableSortStringsContext(ctx, ordered); err != nil {
		return err
	}
	writeDigestCount(digest, len(ordered))
	for _, value := range ordered {
		if err := writeDigestStringContext(ctx, digest, value); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func writeDigestCount(digest hash.Hash, count int) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(count))
	_, _ = digest.Write(length[:])
}

func writeDigestPart(digest hash.Hash, value string) {
	_ = writeDigestStringContext(context.Background(), digest, value)
}

const sqlDigestChunkSize = 64 * 1024

func writeDigestStringContext(ctx context.Context, digest hash.Hash, value string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	for start := 0; start < len(value); start += sqlDigestChunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + sqlDigestChunkSize
		if end > len(value) {
			end = len(value)
		}
		_, _ = digest.Write([]byte(value[start:end]))
	}
	return ctx.Err()
}

func writeDigestBytesContext(ctx context.Context, digest hash.Hash, value []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	for start := 0; start < len(value); start += sqlDigestChunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + sqlDigestChunkSize
		if end > len(value) {
			end = len(value)
		}
		_, _ = digest.Write(value[start:end])
	}
	return ctx.Err()
}

func sqlFieldsContext(ctx context.Context, rows []map[string]interface{}, keyField string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	fields := []string{}
	if keyField != "" {
		seen[keyField] = true
		fields = append(fields, keyField)
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for field := range row {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !seen[field] {
				seen[field] = true
				fields = append(fields, field)
			}
		}
	}
	if len(fields) <= 1 {
		return fields, ctx.Err()
	}
	sortStart := 0
	if keyField != "" && fields[0] == keyField {
		sortStart = 1
	}
	if err := stableSortStringsContext(ctx, fields[sortStart:]); err != nil {
		return nil, err
	}
	return fields, ctx.Err()
}

type sqlDigestRow struct {
	row map[string]interface{}
	key string
}

func stableDigestRowsContext(ctx context.Context, rows []map[string]interface{}, keyField string) ([]map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ordered := make([]sqlDigestRow, len(rows))
	for index, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := databaseKey(row[keyField])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ordered[index] = sqlDigestRow{row: row, key: key}
	}
	if err := stableSortDigestRowsContext(ctx, ordered); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, len(ordered))
	for index, entry := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[index] = entry.row
	}
	return result, ctx.Err()
}

func stableSortStringsContext(ctx context.Context, values []string) error {
	return stableSortContext(ctx, values, func(left, right string) bool {
		return left < right
	})
}

func stableSortDigestRowsContext(ctx context.Context, values []sqlDigestRow) error {
	return stableSortContext(ctx, values, func(left, right sqlDigestRow) bool {
		return left.key < right.key
	})
}

func stableSortTargetStatesContext(ctx context.Context, values []softDeleteTargetState) error {
	return stableSortContext(ctx, values, func(left, right softDeleteTargetState) bool {
		return left.Key < right.Key
	})
}

func stableSortContext[T any](ctx context.Context, values []T, less func(T, T) bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) <= 1 {
		return nil
	}
	scratch := make([]T, len(values))
	var sortRange func(int, int) error
	sortRange = func(low, high int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if high-low <= 1 {
			return nil
		}
		middle := low + (high-low)/2
		if err := sortRange(low, middle); err != nil {
			return err
		}
		if err := sortRange(middle, high); err != nil {
			return err
		}
		left, right := low, middle
		for output := low; output < high; output++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch {
			case left >= middle:
				scratch[output] = values[right]
				right++
			case right >= high:
				scratch[output] = values[left]
				left++
			case !less(values[right], values[left]):
				scratch[output] = values[left]
				left++
			default:
				scratch[output] = values[right]
				right++
			}
		}
		for index := low; index < high; index++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = scratch[index]
		}
		return nil
	}
	if err := sortRange(0, len(values)); err != nil {
		return err
	}
	return ctx.Err()
}

func writeSoftDeleteDigestValueContext(ctx context.Context, digest hash.Hash, value interface{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	typeName := "nil"
	if value != nil {
		if _, ok := value.(time.Time); ok {
			typeName = "time.Time"
		} else {
			typeName = fmt.Sprintf("%T", value)
		}
	}
	if err := writeDigestStringContext(ctx, digest, typeName); err != nil {
		return err
	}
	switch typed := value.(type) {
	case nil:
		return writeDigestStringContext(ctx, digest, "")
	case time.Time:
		return writeDigestStringContext(ctx, digest, typed.Format(time.RFC3339Nano))
	case []byte:
		return writeDigestBytesContext(ctx, digest, typed)
	case string:
		return writeDigestStringContext(ctx, digest, typed)
	default:
		text := databaseText(value)
		if err := ctx.Err(); err != nil {
			return err
		}
		return writeDigestStringContext(ctx, digest, text)
	}
}

func stringSet(values []string) map[string]struct{} {
	result, _ := stringSetContext(context.Background(), values)
	return result
}

func stringSetContext(ctx context.Context, values []string) (map[string]struct{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[value] = struct{}{}
	}
	return result, ctx.Err()
}

func softDeleteRowsAbsentFromSnapshot(ctx context.Context, tx *sql.Tx, driver, table, keyField string, keys []string, requestedBatch int) (int, error) {
	if len(keys) == 0 {
		return 0, &ReconciliationGuardError{Code: ReconciliationGuardEmptySource}
	}
	tempTable, cleanup, err := stageSnapshotKeys(ctx, tx, driver, keyField, keys, requestedBatch)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	target := quoteIdent(driver, table)
	targetKey := quoteIdent(driver, keyField)
	marker := quoteIdent(driver, SoftDeleteColumn)
	temp := quoteIdent(driver, tempTable)
	tempKey := quoteIdent(driver, "snapshot_key")
	update := fmt.Sprintf(
		"UPDATE %s SET %s = ? WHERE (%s IS NULL OR %s = ?) AND NOT EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s)",
		target, marker, marker, marker, temp, temp, tempKey, target, targetKey,
	)
	result, err := tx.ExecContext(ctx, update, true, false)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func stageSnapshotKeys(ctx context.Context, tx *sql.Tx, driver, keyField string, keys []string, requestedBatch int) (string, func(), error) {
	tempTable := "_patris_snapshot_keys"
	drop := fmt.Sprintf("DROP TEMPORARY TABLE IF EXISTS %s", quoteIdent(driver, tempTable))
	if isSQLite(driver) {
		drop = fmt.Sprintf("DROP TABLE IF EXISTS temp.%s", quoteIdent(driver, tempTable))
	}
	cleanup := func() { _, _ = tx.ExecContext(context.Background(), drop) }
	if _, err := tx.ExecContext(ctx, drop); err != nil {
		return "", func() {}, err
	}
	createPrefix := "CREATE TEMP TABLE"
	if !isSQLite(driver) {
		createPrefix = "CREATE TEMPORARY TABLE"
	}
	create := fmt.Sprintf(
		"%s %s (%s %s PRIMARY KEY)",
		createPrefix,
		quoteIdent(driver, tempTable),
		quoteIdent(driver, "snapshot_key"),
		columnType(driver, keyField, keyField, nil),
	)
	if _, err := tx.ExecContext(ctx, create); err != nil {
		cleanup()
		return "", func() {}, err
	}

	batch := effectiveBatchSize(driver, requestedBatch, 1)
	for start := 0; start < len(keys); start += batch {
		end := start + batch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("(?),", len(chunk)), ",")
		insert := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES %s",
			quoteIdent(driver, tempTable),
			quoteIdent(driver, "snapshot_key"),
			placeholders,
		)
		stmt, err := tx.PrepareContext(ctx, insert)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		values := make([]interface{}, len(chunk))
		for index, key := range chunk {
			values[index] = key
		}
		_, execErr := stmt.ExecContext(ctx, values...)
		closeErr := stmt.Close()
		if execErr != nil {
			cleanup()
			return "", func() {}, execErr
		}
		if closeErr != nil {
			cleanup()
			return "", func() {}, closeErr
		}
	}
	return tempTable, cleanup, nil
}
