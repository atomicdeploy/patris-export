package recordsink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteSoftDeleteRequiresExactPreviewAndFreshNoOpRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "private-product-alpha", "final_price": int64(100)},
		{"product_code": "private-product-beta", "final_price": int64(200)},
		{"product_code": "private-product-gamma", "final_price": int64(300)},
	}
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code",
	}, initial); err != nil {
		t.Fatal(err)
	}

	snapshot := []map[string]interface{}{
		{"product_code": "private-product-beta", "final_price": int64(200)},
		{"product_code": "private-product-gamma", "final_price": int64(350)},
	}
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatalf("soft-delete preview: %v", err)
	}
	assertSQLResultCounts(t, preview, 0, 1, 1, 1, 0)
	assertSoftDeleteEvidence(t, preview.ReconciliationEvidence, 2, 3, 1, 1, 0, 0)
	if !preview.ReconciliationEvidence.ApplyAllowed || !preview.ReconciliationEvidence.PartialSourceRisk ||
		!strings.HasPrefix(preview.ReconciliationEvidence.ConfirmationToken, SoftDeleteConfirmationTokenPrefix) ||
		len(preview.ReconciliationEvidence.ConfirmationToken) != SoftDeleteConfirmationTokenLength {
		t.Fatalf("unsafe or incomplete preview evidence: %+v", preview.ReconciliationEvidence)
	}
	confirmation := preview.ReconciliationEvidence.ConfirmationToken
	if _, exists := snapshot[0][SoftDeleteColumn]; exists {
		t.Fatal("soft-delete preparation mutated the caller's source row")
	}
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("dry-run preview evolved the destination schema")
	}
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"private-product-alpha": {price: 100},
		"private-product-beta":  {price: 200},
		"private-product-gamma": {price: 300},
	}, false)

	blocked, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewRequired)
	if blocked.ReconciliationEvidence == nil || blocked.ReconciliationEvidence.GuardCode != ReconciliationGuardPreviewRequired ||
		blocked.ReconciliationEvidence.ConfirmationToken != "" {
		t.Fatalf("missing-preview evidence exposed an apply token or omitted the guard: %+v", blocked.ReconciliationEvidence)
	}
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("missing confirmation mutated the destination schema")
	}

	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: "sha256:not-the-previewed-plan",
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: " " + confirmation + " ",
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("mismatched confirmation mutated the destination schema")
	}

	applied, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: confirmation,
	}, snapshot)
	if err != nil {
		t.Fatalf("apply exact soft-delete preview: %v", err)
	}
	assertSQLResultCounts(t, applied, 0, 1, 1, 1, 0)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"private-product-alpha": {price: 100, deleted: true},
		"private-product-beta":  {price: 200},
		"private-product-gamma": {price: 350},
	}, true)

	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: confirmation,
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)

	retryPreview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatalf("preview already-applied soft-delete plan: %v", err)
	}
	assertSQLResultCounts(t, retryPreview, 0, 0, 2, 0, 0)
	assertSoftDeleteEvidence(t, retryPreview.ReconciliationEvidence, 2, 3, 1, 0, 1, 0)
	if retryPreview.ReconciliationEvidence.ConfirmationToken == confirmation {
		t.Fatal("successful apply did not invalidate the pre-apply target-state token")
	}
	retried, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: retryPreview.ReconciliationEvidence.ConfirmationToken,
	}, snapshot)
	if err != nil {
		t.Fatalf("apply freshly previewed no-op retry: %v", err)
	}
	assertSQLResultCounts(t, retried, 0, 0, 2, 0, 0)

	restore := []map[string]interface{}{
		{"product_code": "private-product-alpha", "final_price": int64(100)},
		{"product_code": "private-product-beta", "final_price": int64(200)},
		{"product_code": "private-product-gamma", "final_price": int64(350)},
	}
	restorePreview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, restore)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, restorePreview, 0, 1, 2, 0, 0)
	assertSoftDeleteEvidence(t, restorePreview.ReconciliationEvidence, 3, 3, 0, 0, 0, 1)
	if restorePreview.ReconciliationEvidence.ConfirmationToken == confirmation {
		t.Fatal("different source keyset reused a stale confirmation token")
	}
	restored, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: restorePreview.ReconciliationEvidence.ConfirmationToken,
	}, restore)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, restored, 0, 1, 2, 0, 0)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"private-product-alpha": {price: 100},
		"private-product-beta":  {price: 200},
		"private-product-gamma": {price: 350},
	}, true)

	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-product-alpha", "private-product-beta", "private-product-gamma", path} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("operator evidence exposed destination detail %q: %s", forbidden, encoded)
		}
	}
}

func TestSQLiteSoftDeleteTokenBindsSourceValuesAndTargetMarkerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete-exact-plan.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
	}
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code",
	}, initial); err != nil {
		t.Fatal(err)
	}

	snapshot := []map[string]interface{}{{"product_code": "B", "final_price": int64(200)}}
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	token := preview.ReconciliationEvidence.ConfirmationToken

	changedSource := []map[string]interface{}{{"product_code": "B", "final_price": int64(201)}}
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: token,
	}, changedSource)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("same-key source-value change mutated the destination schema")
	}

	if _, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: token,
	}, snapshot); err != nil {
		t.Fatal(err)
	}
	markerPreview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE products SET "patris_export_deleted" = 0 WHERE product_code = 'A'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: markerPreview.ReconciliationEvidence.ConfirmationToken,
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"A": {price: 100},
		"B": {price: 200},
	}, true)
}

func TestSQLiteSoftDeleteBlocksEmptyAndChangedPartialSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete-guards.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
		{"product_code": "C", "final_price": int64(300)},
	}
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{Table: "products", KeyField: "product_code"}, initial); err != nil {
		t.Fatal(err)
	}

	emptyPreview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, nil)
	if err != nil {
		t.Fatalf("empty-source preview should return evidence: %v", err)
	}
	assertSQLResultCounts(t, emptyPreview, 0, 0, 0, 3, 0)
	assertSoftDeleteEvidence(t, emptyPreview.ReconciliationEvidence, 0, 3, 3, 3, 0, 0)
	if emptyPreview.ReconciliationEvidence.ApplyAllowed || emptyPreview.ReconciliationEvidence.ConfirmationToken != "" ||
		emptyPreview.ReconciliationEvidence.GuardCode != ReconciliationGuardEmptySource {
		t.Fatalf("empty source was not fail-closed: %+v", emptyPreview.ReconciliationEvidence)
	}
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: "sha256:empty-must-never-apply",
	}, nil)
	assertReconciliationGuard(t, err, ReconciliationGuardEmptySource)
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("empty-source apply attempt evolved the schema")
	}

	partial := initial[1:]
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, partial)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := preview.ReconciliationEvidence.ConfirmationToken
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code",
	}, []map[string]interface{}{{"product_code": "D", "final_price": int64(400)}}); err != nil {
		t.Fatal(err)
	}
	blocked, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: oldToken,
	}, partial)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
	if blocked.ReconciliationEvidence == nil || blocked.ReconciliationEvidence.TargetRows != 4 || blocked.ReconciliationEvidence.MissingRows != 2 {
		t.Fatalf("changed-target guard evidence = %+v", blocked.ReconciliationEvidence)
	}
	if sqliteColumnExists(t, path, "products", SoftDeleteColumn) {
		t.Fatal("stale partial-source confirmation evolved the schema")
	}
	refreshed, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, partial)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ReconciliationEvidence.ConfirmationToken == oldToken || !refreshed.ReconciliationEvidence.PartialSourceRisk {
		t.Fatalf("changed destination did not require a new risky-plan confirmation: %+v", refreshed.ReconciliationEvidence)
	}
}

func TestSQLiteSoftDeletePreviewCanSafelyAuthorizeNewDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "new-soft-delete.sqlite")
	snapshot := []map[string]interface{}{{"product_code": "001", "final_price": int64(100)}}
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, preview, 1, 0, 0, 0, 0)
	assertSoftDeleteEvidence(t, preview.ReconciliationEvidence, 1, 0, 0, 0, 0, 0)
	if sqlitePathExists(path) {
		t.Fatal("new-destination preview created the SQLite file")
	}
	applied, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: preview.ReconciliationEvidence.ConfirmationToken,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, applied, 1, 0, 0, 0, 0)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{"001": {price: 100}}, true)
}

func TestSQLiteSoftDeletePreservesProtectedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete-protected.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
		{"product_code": "C", "final_price": int64(300)},
	}
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{Table: "products", KeyField: "product_code"}, initial); err != nil {
		t.Fatal(err)
	}
	snapshot := []map[string]interface{}{{"product_code": "B", "final_price": int64(200)}}
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ProtectedKeys: []string{"A"}, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, preview, 0, 0, 1, 1, 0)
	assertSoftDeleteEvidence(t, preview.ReconciliationEvidence, 1, 3, 1, 1, 0, 0)
	if preview.ReconciliationEvidence.ProtectedRows != 1 {
		t.Fatalf("protected evidence = %+v", preview.ReconciliationEvidence)
	}
	applied, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ProtectedKeys: []string{"A"}, ReconciliationToken: preview.ReconciliationEvidence.ConfirmationToken,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLResultCounts(t, applied, 0, 0, 1, 1, 0)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"A": {price: 100},
		"B": {price: 200},
		"C": {price: 300, deleted: true},
	}, true)
}

func TestMySQLSoftDeleteStagesKeysInBatchesAndNeverHardDeletes(t *testing.T) {
	state := &schemaDriverState{}
	db := sql.OpenDB(schemaConnector{state: state})
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	count, err := softDeleteRowsAbsentFromSnapshot(
		context.Background(), tx, "mysql", "products", "product_code", []string{"001", "002", "003"}, 2,
	)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("soft-delete affected count = %d, want fake-driver result 1", count)
	}
	joined := strings.Join(state.execs, "\n")
	if !strings.Contains(joined, "UPDATE `products` SET `patris_export_deleted` = ?") ||
		strings.Contains(joined, "DELETE FROM `products`") {
		t.Fatalf("MySQL soft-delete statement was unsafe:\n%s", joined)
	}
	staged := 0
	for _, execution := range state.prepared {
		if strings.HasPrefix(execution.query, "INSERT INTO `patris_snapshot_keys`") {
			staged += len(execution.args)
		}
	}
	if staged != 3 {
		t.Fatalf("staged snapshot keys = %d, want 3: %#v", staged, state.prepared)
	}
	if columnType("sqlite", SoftDeleteColumn, "product_code", nil) != "INTEGER" ||
		columnType("mysql", SoftDeleteColumn, "product_code", nil) != "TINYINT(1)" {
		t.Fatal("soft-delete metadata did not use portable boolean column types")
	}
}

func TestSQLiteSoftDeleteFailureRollsBackAndCanRetryExactPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete-retry.sqlite")
	initial := []map[string]interface{}{
		{"product_code": "A", "final_price": int64(100)},
		{"product_code": "B", "final_price": int64(200)},
	}
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{Table: "products", KeyField: "product_code"}, initial); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE products ADD COLUMN "patris_export_deleted" INTEGER NOT NULL DEFAULT 0`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_soft_delete BEFORE UPDATE OF "patris_export_deleted" ON products
		WHEN NEW.product_code = 'A' AND NEW."patris_export_deleted" = 1
		BEGIN SELECT RAISE(ABORT, 'injected soft-delete failure'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot := []map[string]interface{}{{"product_code": "B", "final_price": int64(250)}}
	preview, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	token := preview.ReconciliationEvidence.ConfirmationToken
	failed, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: token,
	}, snapshot)
	if err == nil {
		t.Fatal("injected soft-delete failure unexpectedly committed")
	}
	assertSQLResultCounts(t, failed, 0, 0, 0, 0, 1)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"A": {price: 100},
		"B": {price: 200},
	}, true)

	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_soft_delete`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	retried, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: token,
	}, snapshot)
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	assertSQLResultCounts(t, retried, 0, 1, 0, 1, 0)
	assertSQLiteSoftDeleteRows(t, path, map[string]softDeleteRow{
		"A": {price: 100, deleted: true},
		"B": {price: 250},
	}, true)
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing,
		ReconciliationToken: token,
	}, snapshot)
	assertReconciliationGuard(t, err, ReconciliationGuardPreviewMismatch)
}

func TestSoftDeleteRejectsReservedAndInvalidMetadataWithoutExposingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soft-delete-metadata.sqlite")
	if _, err := SyncSQLite(context.Background(), path, SQLOptions{Table: "products", KeyField: "product_code"}, []map[string]interface{}{{
		"product_code": "A", "final_price": int64(100),
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, []map[string]interface{}{{"product_code": "A", SoftDeleteColumn: true}})
	assertReconciliationGuard(t, err, ReconciliationGuardReservedField)
	for _, alias := range []string{"patris-export-deleted", " patris export deleted ", "_PATRIS_EXPORT_DELETED_"} {
		_, err = SyncSQLite(context.Background(), path, SQLOptions{
			Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
		}, []map[string]interface{}{{"product_code": "A", alias: true}})
		assertReconciliationGuard(t, err, ReconciliationGuardReservedField)
	}
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "patris-export-deleted", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, []map[string]interface{}{{"patris-export-deleted": "A"}})
	assertReconciliationGuard(t, err, ReconciliationGuardReservedField)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	const privateValue = "private-invalid-tombstone-value"
	if _, err := db.Exec(`ALTER TABLE products ADD COLUMN "patris_export_deleted" TEXT`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE products SET "patris_export_deleted" = ?`, privateValue); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = SyncSQLite(context.Background(), path, SQLOptions{
		Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
	}, []map[string]interface{}{{"product_code": "A", "final_price": int64(100)}})
	assertReconciliationGuard(t, err, ReconciliationGuardInvalidTombstone)
	if strings.Contains(err.Error(), privateValue) {
		t.Fatalf("invalid metadata error exposed its stored value: %v", err)
	}

	for name, definition := range map[string]string{
		"nullable":     `INTEGER DEFAULT 0`,
		"true_default": `INTEGER NOT NULL DEFAULT 1`,
	} {
		t.Run(name, func(t *testing.T) {
			invalidPath := filepath.Join(t.TempDir(), name+".sqlite")
			if _, err := SyncSQLite(context.Background(), invalidPath, SQLOptions{
				Table: "products", KeyField: "product_code",
			}, []map[string]interface{}{{"product_code": "A", "final_price": int64(100)}}); err != nil {
				t.Fatal(err)
			}
			invalidDB, err := sql.Open("sqlite3", invalidPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := invalidDB.Exec(`ALTER TABLE products ADD COLUMN "patris_export_deleted" ` + definition); err != nil {
				invalidDB.Close()
				t.Fatal(err)
			}
			if err := invalidDB.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = SyncSQLite(context.Background(), invalidPath, SQLOptions{
				Table: "products", KeyField: "product_code", Reconciliation: SoftDeleteMissing, DryRun: true,
			}, []map[string]interface{}{{"product_code": "A", "final_price": int64(100)}})
			assertReconciliationGuard(t, err, ReconciliationGuardInvalidTombstone)
		})
	}
}

type softDeleteRow struct {
	price   int64
	deleted bool
}

func assertSoftDeleteEvidence(t *testing.T, evidence *SQLReconciliationEvidence, source, target, missing, wouldDelete, alreadyDeleted, wouldRestore int) {
	t.Helper()
	if evidence == nil || evidence.SourceRows != source || evidence.TargetRows != target || evidence.MissingRows != missing ||
		evidence.WouldSoftDelete != wouldDelete || evidence.AlreadySoftDeleted != alreadyDeleted || evidence.WouldRestore != wouldRestore {
		t.Fatalf("soft-delete evidence = %+v, want source=%d target=%d missing=%d would_delete=%d already_deleted=%d restore=%d",
			evidence, source, target, missing, wouldDelete, alreadyDeleted, wouldRestore)
	}
}

func assertReconciliationGuard(t *testing.T, err error, expected ReconciliationGuardCode) {
	t.Helper()
	var guard *ReconciliationGuardError
	if !errors.As(err, &guard) || guard.Code != expected {
		t.Fatalf("reconciliation error = %#v, want guard %q", err, expected)
	}
}

func TestSQLSourceFingerprintPreservesDatabaseValueTypes(t *testing.T) {
	base := []map[string]interface{}{{"Code": "001", "value": int64(1)}}
	baseFingerprint := SQLSourceFingerprint(base, "Code", []string{"protected"})
	tests := []struct {
		name  string
		value interface{}
	}{
		{name: "float", value: float64(1)},
		{name: "text", value: "1"},
		{name: "bytes", value: []byte("1")},
		{name: "time text", value: time.Unix(1, 0).UTC().Format(time.RFC3339Nano)},
		{name: "timestamp", value: time.Unix(1, 0).UTC()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := []map[string]interface{}{{"Code": "001", "value": test.value}}
			if got := SQLSourceFingerprint(changed, "Code", []string{"protected"}); got == baseFingerprint {
				t.Fatalf("typed source change %T(%v) reused int64 fingerprint", test.value, test.value)
			}
		})
	}
	if got := SQLSourceFingerprint(base, "Code", []string{"other"}); got == baseFingerprint {
		t.Fatal("protected-key change reused source fingerprint")
	}
}

func sqliteColumnExists(t *testing.T, path, table, column string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func sqlitePathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func assertSQLiteSoftDeleteRows(t *testing.T, path string, expected map[string]softDeleteRow, markerExists bool) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := `SELECT product_code, final_price FROM products ORDER BY product_code`
	if markerExists {
		query = `SELECT product_code, final_price, "patris_export_deleted" FROM products ORDER BY product_code`
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var code string
		var price int64
		var deleted bool
		if markerExists {
			err = rows.Scan(&code, &price, &deleted)
		} else {
			err = rows.Scan(&code, &price)
		}
		if err != nil {
			t.Fatal(err)
		}
		want, exists := expected[code]
		if !exists || want.price != price || want.deleted != deleted {
			t.Fatalf("row %q = price:%d deleted:%t, expected=%+v exists=%t", code, price, deleted, want, exists)
		}
		seen[code] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(expected) {
		t.Fatalf("rows seen=%v, want=%v", seen, expected)
	}
}
