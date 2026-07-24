export const SQL_TARGET_ENDPOINTS = Object.freeze({
    session: '/api/sql-target/session',
    status: '/api/sql-target/status',
    test: '/api/sql-target/test',
    preview: '/api/sql-target/preview',
    sync: '/api/sql-target/sync',
    lastResult: '/api/sql-target/last-result'
});

export const SQL_TARGET_OPERATOR_HEADER = 'X-Patris-SQL-Operator-Token';
export const SQL_TARGET_CSRF_HEADER = 'X-Patris-CSRF-Token';
export const SQL_TARGET_SYNC_CONFIRMATION = 'manual_sync';

const ALLOWED_RECONCILIATION = new Set([
    'upsert_only',
    'soft_delete_missing',
    'delete_missing'
]);
const ALLOWED_OPERATIONS = new Set(['test', 'preview', 'sync']);
const ALLOWED_OPERATION_STATUS = new Set(['succeeded', 'failed']);
const ALLOWED_VENDOR = new Set(['mysql', 'mariadb']);
const ALLOWED_TLS = new Set(['unknown', 'encrypted', 'plaintext']);
const ALLOWED_DRIVER = new Set(['mysql']);
const ALLOWED_RECONCILIATION_GUARDS = new Set([
    '',
    'empty_source',
    'preview_required',
    'preview_mismatch',
    'reserved_field_collision',
    'invalid_tombstone_state',
    'safety_guard'
]);
const RECONCILIATION_TOKEN_PATTERN = /^sha256:[a-f0-9]{64}$/;
const PREVIEW_GRANT_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const MAX_SAFE_TEXT_LENGTH = 96;

export class SQLTargetAPIError extends Error {
    constructor(code = 'request_failed', status = 0, diagnostic = null) {
        super(code);
        this.name = 'SQLTargetAPIError';
        this.code = safeIdentifier(code, 'request_failed');
        this.status = nonNegativeInteger(status);
        this.diagnostic = diagnostic ? normalizeSQLDiagnostic(diagnostic) : null;
    }
}

export function createSQLTargetAPI(options = {}) {
    const fetchImpl = options.fetchImpl || globalThis.fetch;
    const now = typeof options.now === 'function' ? options.now : () => Date.now();
    if (typeof fetchImpl !== 'function') {
        throw new TypeError('fetchImpl is required');
    }

    let csrfToken = '';
    let expiresAt = 0;
    let statusFingerprint = '';
    let previewAuthorization = null;

    const clearLocalSession = () => {
        csrfToken = '';
        expiresAt = 0;
        statusFingerprint = '';
        previewAuthorization = null;
    };

    const request = async (endpoint, requestOptions = {}) => {
        const headers = {
            Accept: 'application/json',
            ...(requestOptions.headers || {})
        };
        let response;
        let payload;
        try {
            response = await fetchImpl(endpoint, {
                method: requestOptions.method || 'GET',
                credentials: 'same-origin',
                cache: 'no-store',
                headers,
                ...(requestOptions.body === undefined ? {} : { body: requestOptions.body })
            });
            payload = await readAPIResponse(response);
        } catch (error) {
            previewAuthorization = null;
            throw error;
        }
        if (!response.ok) {
            previewAuthorization = null;
            const code = safeIdentifier(payload?.error?.code, 'request_failed');
            if (response.status === 403) {
                clearLocalSession();
            }
            throw new SQLTargetAPIError(code, response.status, payload?.diagnostic);
        }
        return payload;
    };

    const protectedRequest = async (endpoint, requestOptions = {}) => {
        if (!csrfToken || !expiresAt || now() >= expiresAt) {
            clearLocalSession();
            throw new SQLTargetAPIError('session_required', 403);
        }
        return request(endpoint, {
            ...requestOptions,
            headers: {
                ...(requestOptions.headers || {}),
                [SQL_TARGET_CSRF_HEADER]: csrfToken
            }
        });
    };

    return Object.freeze({
        async unlock(operatorCredential = '') {
            previewAuthorization = null;
            const credential = String(operatorCredential || '');
            if (credential.length > 512) {
                throw new SQLTargetAPIError('invalid_credential', 400);
            }
            const headers = {};
            if (credential) {
                headers[SQL_TARGET_OPERATOR_HEADER] = credential;
            }
            const payload = await request(SQL_TARGET_ENDPOINTS.session, {
                method: 'POST',
                headers
            });
            const nextCSRFToken = safeOpaqueToken(payload?.csrf_token);
            const nextExpiresAt = Date.parse(String(payload?.expires_at || ''));
            if (payload?.authenticated !== true || !nextCSRFToken || !Number.isFinite(nextExpiresAt) || nextExpiresAt <= now()) {
                throw new SQLTargetAPIError('invalid_response', 502);
            }
            csrfToken = nextCSRFToken;
            expiresAt = nextExpiresAt;
            statusFingerprint = '';
            previewAuthorization = null;
            return Object.freeze({
                authenticated: true,
                expiresAt: new Date(expiresAt).toISOString()
            });
        },

        isUnlocked() {
            return !!csrfToken && !!expiresAt && now() < expiresAt;
        },

        sessionExpiresAt() {
            return expiresAt ? new Date(expiresAt).toISOString() : '';
        },

        invalidate() {
            clearLocalSession();
        },

        async lock() {
            if (!csrfToken || !expiresAt || now() >= expiresAt) {
                clearLocalSession();
                return Object.freeze({ authenticated: false });
            }
            try {
                const payload = await protectedRequest(SQL_TARGET_ENDPOINTS.session, {
                    method: 'DELETE'
                });
                if (payload?.authenticated !== false) {
                    throw new SQLTargetAPIError('invalid_response', 502);
                }
                return Object.freeze({ authenticated: false });
            } finally {
                clearLocalSession();
            }
        },

        async status() {
            previewAuthorization = null;
            const status = normalizeSQLTargetStatus(
                await protectedRequest(SQL_TARGET_ENDPOINTS.status)
            );
            const nextFingerprint = sqlTargetStatusFingerprint(status);
            statusFingerprint = nextFingerprint;
            return status;
        },

        async lastResult() {
            previewAuthorization = null;
            const payload = await protectedRequest(SQL_TARGET_ENDPOINTS.lastResult);
            if (payload?.available !== true) {
                return Object.freeze({ available: false, diagnostic: null });
            }
            return Object.freeze({
                available: true,
                diagnostic: normalizeSQLDiagnostic(payload?.diagnostic)
            });
        },

        previewState() {
            if (previewAuthorization && now() >= previewAuthorization.grantExpiresAt) {
                previewAuthorization = null;
            }
            return Object.freeze({
                ready: !!previewAuthorization,
                reconciliation: previewAuthorization?.reconciliation || '',
                tokenRequired: previewAuthorization?.reconciliation === 'soft_delete_missing',
                tokenAvailable: !!previewAuthorization?.token
            });
        },

        clearPreview() {
            previewAuthorization = null;
        },

        async run(operation, runOptions = {}) {
            if (!ALLOWED_OPERATIONS.has(operation)) {
                throw new SQLTargetAPIError('unsupported_operation', 400);
            }
            if (operation !== 'sync') {
                previewAuthorization = null;
            }
            const requestOptions = { method: 'POST' };
            if (operation === 'sync') {
                const authorization = previewAuthorization;
                // Consume locally before validation or network I/O. Every
                // apply attempt and outcome requires a fresh direct preview.
                previewAuthorization = null;
                try {
                    const reconciliation = allowedString(
                        runOptions.reconciliation,
                        ALLOWED_RECONCILIATION,
                        'upsert_only'
                    );
                    if (reconciliation === 'delete_missing') {
                        throw new SQLTargetAPIError('unsupported_reconciliation', 400);
                    }
                    if (!authorization
                        || now() >= authorization.grantExpiresAt
                        || authorization.statusFingerprint !== statusFingerprint
                        || authorization.reconciliation !== reconciliation) {
                        throw new SQLTargetAPIError('preview_required', 409);
                    }
                    if (reconciliation === 'soft_delete_missing' && !authorization.token) {
                        throw new SQLTargetAPIError('preview_confirmation_required', 409);
                    }
                    const requestBody = {
                        confirm: SQL_TARGET_SYNC_CONFIRMATION,
                        preview_grant: authorization.grant
                    };
                    if (authorization.token) {
                        requestBody.reconciliation_token = authorization.token;
                    }
                    requestOptions.headers = { 'Content-Type': 'application/json' };
                    requestOptions.body = JSON.stringify(requestBody);
                    const payload = await protectedRequest(SQL_TARGET_ENDPOINTS.sync, requestOptions);
                    return normalizeSQLDiagnostic(payload?.diagnostic);
                } finally {
                    previewAuthorization = null;
                }
            }
            const payload = await protectedRequest(SQL_TARGET_ENDPOINTS[operation], requestOptions);
            const diagnostic = normalizeSQLDiagnostic(payload?.diagnostic);
            if (operation === 'preview') {
                previewAuthorization = previewAuthorizationFromDiagnostic(
                    payload?.diagnostic,
                    diagnostic,
                    statusFingerprint,
                    expiresAt,
                    now()
                );
            }
            return diagnostic;
        }
    });
}

export function normalizeSQLTargetStatus(value) {
    const status = value && typeof value === 'object' ? value : {};
    return Object.freeze({
        configured: status.configured === true,
        driver: allowedString(status.driver, ALLOWED_DRIVER, 'mysql'),
        tableConfigured: (status.table_configured ?? status.tableConfigured) === true,
        batchSize: nonNegativeInteger(status.batch_size ?? status.batchSize),
        reconciliation: allowedString(status.reconciliation, ALLOWED_RECONCILIATION, 'upsert_only'),
        connectTimeoutMS: nonNegativeInteger(status.connect_timeout_ms ?? status.connectTimeoutMS),
        verifiedTLSConfigured: (status.verified_tls_configured ?? status.verifiedTLSConfigured) === true,
        busy: status.busy === true,
        lastResultAvailable: (status.last_result_available ?? status.lastResultAvailable) === true
    });
}

export function normalizeSQLDiagnostic(value) {
    const diagnostic = value && typeof value === 'object' ? value : {};
    const operation = allowedString(diagnostic.operation, ALLOWED_OPERATIONS, 'test');
    const status = allowedString(diagnostic.status, ALLOWED_OPERATION_STATUS, 'failed');
    const normalized = {
        operation,
        status,
        startedAt: safeTimestamp(diagnostic.started_at ?? diagnostic.startedAt),
        finishedAt: safeTimestamp(diagnostic.finished_at ?? diagnostic.finishedAt),
        recordCount: (diagnostic.record_count ?? diagnostic.recordCount) === undefined || (diagnostic.record_count ?? diagnostic.recordCount) === null
            ? null
            : nonNegativeInteger(diagnostic.record_count ?? diagnostic.recordCount),
        probe: normalizeSQLProbe(diagnostic.probe),
        result: normalizeSQLResult(diagnostic.result),
        failure: normalizeSQLFailure(diagnostic.failure)
    };
    return Object.freeze(normalized);
}

export function normalizeSQLProbe(value) {
    if (!value || typeof value !== 'object') {
        return null;
    }
    return Object.freeze({
        connected: value.connected === true,
        driver: allowedString(value.driver, ALLOWED_DRIVER, 'mysql'),
        vendor: allowedString(value.vendor, ALLOWED_VENDOR, ''),
        tls: allowedString(value.tls, ALLOWED_TLS, 'unknown'),
        elapsedMS: nonNegativeInteger(value.elapsed_ms ?? value.elapsedMS)
    });
}

export function normalizeSQLResult(value) {
    if (!value || typeof value !== 'object') {
        return null;
    }
    return Object.freeze({
        inserted: nonNegativeInteger(value.inserted),
        updated: nonNegativeInteger(value.updated),
        unchanged: nonNegativeInteger(value.unchanged),
        deleted: nonNegativeInteger(value.deleted),
        failed: nonNegativeInteger(value.failed),
        elapsedMS: nonNegativeInteger(value.elapsed_ms ?? value.elapsedMS),
        dryRun: (value.dry_run ?? value.dryRun) === true,
        reconciliation: allowedString(value.reconciliation, ALLOWED_RECONCILIATION, 'upsert_only'),
        reconciliationEvidence: normalizeSQLReconciliationEvidence(value.reconciliation_evidence ?? value.reconciliationEvidence)
    });
}

export function normalizeSQLReconciliationEvidence(value) {
    if (!value || typeof value !== 'object') {
        return null;
    }
    const confirmationToken = String(value.confirmation_token ?? value.confirmationToken ?? '');
    return Object.freeze({
        sourceRows: nonNegativeInteger(value.source_rows ?? value.sourceRows),
        protectedRows: nonNegativeInteger(value.protected_rows ?? value.protectedRows),
        targetRows: nonNegativeInteger(value.target_rows ?? value.targetRows),
        missingRows: nonNegativeInteger(value.missing_rows ?? value.missingRows),
        wouldSoftDelete: nonNegativeInteger(value.would_soft_delete ?? value.wouldSoftDelete),
        alreadySoftDeleted: nonNegativeInteger(value.already_soft_deleted ?? value.alreadySoftDeleted),
        wouldRestore: nonNegativeInteger(value.would_restore ?? value.wouldRestore),
        partialSourceRisk: (value.partial_source_risk ?? value.partialSourceRisk) === true,
        confirmationRequired: (value.confirmation_required ?? value.confirmationRequired) === true,
        applyAllowed: (value.apply_allowed ?? value.applyAllowed) === true,
        confirmationAvailable: RECONCILIATION_TOKEN_PATTERN.test(confirmationToken),
        guardCode: allowedString(value.guard_code ?? value.guardCode, ALLOWED_RECONCILIATION_GUARDS, 'safety_guard')
    });
}

export function normalizeSQLFailure(value) {
    if (!value || typeof value !== 'object') {
        return null;
    }
    // The server already emits a secret-safe message. The browser deliberately
    // keeps only stable identifiers, so a future backend regression cannot
    // surface a connection string, certificate path, host, or driver text.
    return Object.freeze({
        code: safeIdentifier(value.code, 'operation_failed'),
        stage: safeIdentifier(value.stage, 'operation'),
        reason: normalizeFailureReason(value.reason),
        retryable: value.retryable === true
    });
}

export function sqlTargetStatusModel(status, diagnostic = null) {
    const safeStatus = status || normalizeSQLTargetStatus({});
    const safeDiagnostic = diagnostic ? normalizeSQLDiagnostic(diagnostic) : null;
    const probe = safeDiagnostic?.probe || null;
    return Object.freeze({
        configuredTone: safeStatus.configured ? 'success' : 'warning',
        connectionTone: probe?.connected ? 'success' : (safeDiagnostic?.operation === 'test' && safeDiagnostic.status === 'failed' ? 'danger' : 'neutral'),
        operationTone: safeDiagnostic?.status === 'succeeded' ? 'success' : (safeDiagnostic?.status === 'failed' ? 'danger' : 'neutral'),
        connected: probe?.connected === true,
        vendor: probe?.vendor || '',
        tls: probe?.tls || 'unknown',
        busy: safeStatus.busy === true
    });
}

export function createSQLTargetController(options = {}) {
    const documentRef = options.documentRef || globalThis.document;
    const translate = typeof options.translate === 'function' ? options.translate : key => key;
    const getLanguage = typeof options.getLanguage === 'function' ? options.getLanguage : () => 'en';
    const api = options.api || createSQLTargetAPI({ fetchImpl: options.fetchImpl });
    const byId = id => documentRef?.getElementById?.(id) || null;
    const state = {
        status: null,
        diagnostic: null,
        unlocked: false,
        busy: false,
        expiresAt: '',
        noticeKey: 'sqlTargetUnlockHelp',
        noticeTone: 'neutral'
    };

    const elements = {
        panel: byId('sqlTargetPanel'),
        title: byId('sqlTargetTitle'),
        openButton: byId('sqlTargetBtn'),
        closeButton: byId('closeSQLTarget'),
        unlockForm: byId('sqlTargetUnlockForm'),
        credential: byId('sqlTargetOperatorCredential'),
        unlockButton: byId('sqlTargetUnlockButton'),
        lockButton: byId('sqlTargetLock'),
        locked: byId('sqlTargetLocked'),
        unlocked: byId('sqlTargetUnlocked'),
        session: byId('sqlTargetSessionStatus'),
        notice: byId('sqlTargetNotice'),
        indicator: byId('sqlTargetIndicator'),
        refresh: byId('sqlTargetRefresh'),
        test: byId('sqlTargetTest'),
        preview: byId('sqlTargetPreview'),
        sync: byId('sqlTargetSync'),
        syncConfirm: byId('sqlTargetSyncConfirm'),
        previewAuthorization: byId('sqlTargetPreviewAuthorization'),
        configured: byId('sqlTargetConfiguredValue'),
        driver: byId('sqlTargetDriverValue'),
        table: byId('sqlTargetTableValue'),
        reconciliation: byId('sqlTargetReconciliationValue'),
        batchSize: byId('sqlTargetBatchSizeValue'),
        timeout: byId('sqlTargetTimeoutValue'),
        tlsConfigured: byId('sqlTargetTLSConfiguredValue'),
        targetBusy: byId('sqlTargetBusyValue'),
        connected: byId('sqlTargetConnectedValue'),
        vendor: byId('sqlTargetVendorValue'),
        tls: byId('sqlTargetTLSValue'),
        probeElapsed: byId('sqlTargetProbeElapsedValue'),
        lastEmpty: byId('sqlTargetLastEmpty'),
        lastContent: byId('sqlTargetLastContent'),
        lastOperation: byId('sqlTargetLastOperation'),
        lastStatus: byId('sqlTargetLastStatus'),
        lastRecords: byId('sqlTargetLastRecords'),
        lastElapsed: byId('sqlTargetLastElapsed'),
        inserted: byId('sqlTargetInserted'),
        updated: byId('sqlTargetUpdated'),
        unchanged: byId('sqlTargetUnchanged'),
        deleted: byId('sqlTargetDeleted'),
        failed: byId('sqlTargetFailed'),
        evidence: byId('sqlTargetReconciliationEvidence'),
        evidenceGuard: byId('sqlTargetEvidenceGuard'),
        evidenceSource: byId('sqlTargetEvidenceSource'),
        evidenceProtected: byId('sqlTargetEvidenceProtected'),
        evidenceTarget: byId('sqlTargetEvidenceTarget'),
        evidenceMissing: byId('sqlTargetEvidenceMissing'),
        evidenceWouldDelete: byId('sqlTargetEvidenceWouldDelete'),
        evidenceAlreadyDeleted: byId('sqlTargetEvidenceAlreadyDeleted'),
        evidenceWouldRestore: byId('sqlTargetEvidenceWouldRestore'),
        evidencePartialRisk: byId('sqlTargetEvidencePartialRisk'),
        evidenceApplyAllowed: byId('sqlTargetEvidenceApplyAllowed'),
        evidenceConfirmation: byId('sqlTargetEvidenceConfirmation'),
        failure: byId('sqlTargetFailure'),
        failureCode: byId('sqlTargetFailureCode'),
        failureStage: byId('sqlTargetFailureStage'),
        failureReason: byId('sqlTargetFailureReason'),
        failureRetryable: byId('sqlTargetFailureRetryable')
    };

    const text = (element, value) => {
        if (element) element.textContent = value;
    };
    const localized = key => translate(key);
    const yesNo = value => localized(value ? 'yes' : 'no');
    const unknown = () => localized('unknown');
    const dash = () => '—';

    const render = () => {
        const language = getLanguage() === 'fa' ? 'fa' : 'en';
        if (elements.panel) {
            elements.panel.dir = language === 'fa' ? 'rtl' : 'ltr';
            elements.panel.lang = language;
        }
        state.unlocked = api.isUnlocked();
        if (elements.locked) elements.locked.hidden = state.unlocked;
        if (elements.unlocked) elements.unlocked.hidden = !state.unlocked;
        if (elements.unlockButton) elements.unlockButton.disabled = state.busy;
        if (elements.credential) elements.credential.disabled = state.busy;
        if (elements.lockButton) {
            elements.lockButton.hidden = !state.unlocked;
            elements.lockButton.disabled = state.busy;
        }

        text(elements.session, state.unlocked
            ? localized('sqlTargetSessionUnlocked')
            : localized('sqlTargetSessionLocked'));
        if (elements.session) {
            elements.session.className = `sql-target-session-chip ${state.unlocked ? 'success' : 'neutral'}`;
        }
        if (elements.indicator) {
            const targetReady = state.status?.configured && state.status?.tableConfigured;
            elements.indicator.className = `sql-target-indicator ${state.unlocked ? (targetReady ? 'success' : 'warning') : 'neutral'}`;
            elements.indicator.title = state.unlocked
                ? localized(targetReady ? 'sqlTargetConfigured' : 'sqlTargetNotConfigured')
                : localized('sqlTargetSessionLocked');
        }

        text(elements.notice, localized(state.noticeKey));
        if (elements.notice) {
            elements.notice.dataset.tone = state.noticeTone;
        }

        const status = state.status || normalizeSQLTargetStatus({});
        const model = sqlTargetStatusModel(status, state.diagnostic);
        text(elements.configured, yesNo(status.configured));
        text(elements.driver, status.driver.toUpperCase());
        text(elements.table, yesNo(status.tableConfigured));
        text(elements.reconciliation, localized(`sqlTargetReconciliation_${status.reconciliation}`));
        text(elements.batchSize, formatInteger(status.batchSize, language));
        text(elements.timeout, formatElapsed(status.connectTimeoutMS, language));
        text(elements.tlsConfigured, yesNo(status.verifiedTLSConfigured));
        text(elements.targetBusy, localized((state.busy || model.busy) ? 'busy' : 'ready'));
        text(elements.connected, state.diagnostic?.probe ? yesNo(model.connected) : unknown());
        text(elements.vendor, model.vendor ? model.vendor.toUpperCase() : dash());
        text(elements.tls, localized(`sqlTargetTLS_${model.tls}`));
        text(elements.probeElapsed, state.diagnostic?.probe
            ? formatElapsed(state.diagnostic.probe.elapsedMS, language)
            : dash());

        renderDiagnostic(language);

        const configured = status.configured;
        const tableConfigured = status.tableConfigured;
        const preview = typeof api.previewState === 'function'
            ? api.previewState()
            : { ready: false, reconciliation: '', tokenRequired: false, tokenAvailable: false };
        if (!preview.ready && elements.syncConfirm) {
            elements.syncConfirm.checked = false;
        }
        const hardDeleteBlocked = status.reconciliation === 'delete_missing';
        if (elements.previewAuthorization) {
            const previewKey = hardDeleteBlocked
                ? 'sqlTargetPreviewUnsupported'
                : preview.ready
                    ? 'sqlTargetPreviewReady'
                    : 'sqlTargetPreviewRequired';
            text(elements.previewAuthorization, localized(previewKey));
            elements.previewAuthorization.className = `sql-target-operation-chip ${hardDeleteBlocked ? 'danger' : preview.ready ? 'success' : 'neutral'}`;
        }
        const disabled = !state.unlocked || state.busy || status.busy;
        if (elements.refresh) elements.refresh.disabled = disabled;
        if (elements.test) elements.test.disabled = disabled || !configured;
        if (elements.preview) elements.preview.disabled = disabled || !configured || !tableConfigured;
        if (elements.sync) {
            elements.sync.disabled = disabled
                || !configured
                || !tableConfigured
                || hardDeleteBlocked
                || !preview.ready
                || !elements.syncConfirm?.checked;
        }
        if (elements.syncConfirm) {
            elements.syncConfirm.disabled = disabled
                || !configured
                || !tableConfigured
                || hardDeleteBlocked
                || !preview.ready;
        }
        if (elements.unlocked) elements.unlocked.setAttribute('aria-busy', String(state.busy || status.busy));
    };

    const renderDiagnostic = language => {
        const diagnostic = state.diagnostic;
        if (elements.lastEmpty) elements.lastEmpty.hidden = !!diagnostic;
        if (elements.lastContent) elements.lastContent.hidden = !diagnostic;
        if (!diagnostic) return;

        const result = diagnostic.result;
        text(elements.lastOperation, localized(`sqlTargetOperation_${diagnostic.operation}`));
        text(elements.lastStatus, localized(`sqlTargetOperationStatus_${diagnostic.status}`));
        if (elements.lastStatus) {
            elements.lastStatus.className = `sql-target-operation-chip ${diagnostic.status === 'succeeded' ? 'success' : 'danger'}`;
        }
        text(elements.lastRecords, diagnostic.recordCount === null ? dash() : formatInteger(diagnostic.recordCount, language));
        const elapsed = result?.elapsedMS ?? diagnostic.probe?.elapsedMS;
        text(elements.lastElapsed, elapsed === undefined || elapsed === null ? dash() : formatElapsed(elapsed, language));
        text(elements.inserted, formatInteger(result?.inserted || 0, language));
        text(elements.updated, formatInteger(result?.updated || 0, language));
        text(elements.unchanged, formatInteger(result?.unchanged || 0, language));
        text(elements.deleted, formatInteger(result?.deleted || 0, language));
        text(elements.failed, formatInteger(result?.failed || 0, language));

        const evidence = result?.reconciliationEvidence || null;
        if (elements.evidence) elements.evidence.hidden = !evidence;
        if (evidence) {
            text(elements.evidenceSource, formatInteger(evidence.sourceRows, language));
            text(elements.evidenceProtected, formatInteger(evidence.protectedRows, language));
            text(elements.evidenceTarget, formatInteger(evidence.targetRows, language));
            text(elements.evidenceMissing, formatInteger(evidence.missingRows, language));
            text(elements.evidenceWouldDelete, formatInteger(evidence.wouldSoftDelete, language));
            text(elements.evidenceAlreadyDeleted, formatInteger(evidence.alreadySoftDeleted, language));
            text(elements.evidenceWouldRestore, formatInteger(evidence.wouldRestore, language));
            text(elements.evidencePartialRisk, yesNo(evidence.partialSourceRisk));
            text(elements.evidenceApplyAllowed, yesNo(evidence.applyAllowed));
            text(elements.evidenceConfirmation, yesNo(evidence.confirmationAvailable));
            const guardKey = evidence.guardCode
                ? `sqlTargetGuard_${evidence.guardCode}`
                : 'sqlTargetGuard_clear';
            text(elements.evidenceGuard, localized(guardKey));
            if (elements.evidenceGuard) {
                elements.evidenceGuard.className = `sql-target-operation-chip ${evidence.applyAllowed ? 'success' : 'danger'}`;
            }
        }

        const failure = diagnostic.failure;
        if (elements.failure) elements.failure.hidden = !failure;
        if (failure) {
            text(elements.failureCode, failure.code);
            text(elements.failureStage, failure.stage);
            text(elements.failureReason, failure.reason
                ? localized(`sqlTargetGuard_${failure.reason}`)
                : dash());
            text(elements.failureRetryable, yesNo(failure.retryable));
        }
    };

    const clearPreviewUI = () => {
        api.clearPreview?.();
        if (elements.syncConfirm) {
            elements.syncConfirm.checked = false;
        }
    };

    const handleError = error => {
        clearPreviewUI();
        if (error instanceof SQLTargetAPIError && (error.status === 403 || error.code === 'session_required')) {
            api.invalidate?.();
            state.unlocked = false;
            state.noticeKey = error.code === 'session_required'
                ? 'sqlTargetSessionExpired'
                : 'sqlTargetUnlockRejected';
        } else {
            state.noticeKey = error instanceof SQLTargetAPIError
                ? `sqlTargetError_${error.code}`
                : 'sqlTargetRequestFailed';
            if (localized(state.noticeKey) === state.noticeKey) {
                state.noticeKey = 'sqlTargetRequestFailed';
            }
        }
        state.noticeTone = 'danger';
    };

    const refresh = async () => {
        clearPreviewUI();
        if (!api.isUnlocked()) {
            state.noticeKey = 'sqlTargetUnlockHelp';
            state.noticeTone = 'neutral';
            render();
            return;
        }
        state.busy = true;
        state.noticeKey = 'sqlTargetRefreshing';
        state.noticeTone = 'neutral';
        render();
        try {
            const [status, last] = await Promise.all([api.status(), api.lastResult()]);
            state.status = status;
            state.diagnostic = last.available ? last.diagnostic : null;
            state.noticeKey = !status.configured
                ? 'sqlTargetNotConfiguredNotice'
                : status.tableConfigured
                    ? 'sqlTargetReadyNotice'
                    : 'sqlTargetTableRequiredNotice';
            state.noticeTone = status.configured && status.tableConfigured ? 'success' : 'warning';
        } catch (error) {
            handleError(error);
        } finally {
            state.busy = false;
            render();
        }
    };

    const unlock = async event => {
        event?.preventDefault?.();
        state.busy = true;
        state.noticeKey = 'sqlTargetUnlocking';
        state.noticeTone = 'neutral';
        render();
        const credential = elements.credential?.value || '';
        try {
            const session = await api.unlock(credential);
            state.expiresAt = session.expiresAt;
            state.unlocked = true;
            state.noticeKey = 'sqlTargetSessionReady';
            state.noticeTone = 'success';
        } catch (error) {
            handleError(error);
        } finally {
            if (elements.credential) elements.credential.value = '';
            state.busy = false;
            render();
        }
        if (api.isUnlocked()) {
            await refresh();
        }
    };

    const run = async operation => {
        if (operation === 'sync' && !elements.syncConfirm?.checked) {
            state.noticeKey = 'sqlTargetSyncConfirmationRequired';
            state.noticeTone = 'warning';
            render();
            return;
        }
        if (operation !== 'sync') {
            clearPreviewUI();
        } else if (elements.syncConfirm) {
            // The API consumes its in-memory grant before validation or I/O;
            // clear the separate destructive confirmation at the same time.
            elements.syncConfirm.checked = false;
        }
        state.busy = true;
        state.noticeKey = `sqlTargetRunning_${operation}`;
        state.noticeTone = 'neutral';
        render();
        try {
            state.diagnostic = await api.run(operation, {
                reconciliation: state.status?.reconciliation || 'upsert_only'
            });
            state.noticeKey = state.diagnostic.status === 'succeeded'
                ? `sqlTargetCompleted_${operation}`
                : 'sqlTargetOperationFailed';
            state.noticeTone = state.diagnostic.status === 'succeeded' ? 'success' : 'danger';
            if (operation === 'preview') {
                const preview = api.previewState?.();
                if (!preview?.ready) {
                    state.noticeKey = state.status.reconciliation === 'delete_missing'
                        ? 'sqlTargetDeleteMissingBlocked'
                        : 'sqlTargetPreviewNotAuthorized';
                    state.noticeTone = 'warning';
                }
            } else {
                state.status = await api.status();
            }
        } catch (error) {
            if (error instanceof SQLTargetAPIError && error.diagnostic) {
                state.diagnostic = error.diagnostic;
            }
            handleError(error);
        } finally {
            state.busy = false;
            render();
        }
    };

    const lock = async () => {
        clearPreviewUI();
        state.busy = true;
        state.noticeKey = 'sqlTargetLocking';
        state.noticeTone = 'neutral';
        render();
        try {
            await api.lock();
            state.status = null;
            state.diagnostic = null;
            state.unlocked = false;
            state.noticeKey = 'sqlTargetLockedNotice';
            state.noticeTone = 'success';
        } catch {
            api.invalidate?.();
            state.status = null;
            state.diagnostic = null;
            state.unlocked = false;
            state.noticeKey = 'sqlTargetLockFailed';
            state.noticeTone = 'danger';
        } finally {
            if (elements.syncConfirm) elements.syncConfirm.checked = false;
            state.busy = false;
            render();
        }
    };

    elements.unlockForm?.addEventListener('submit', unlock);
    elements.lockButton?.addEventListener('click', lock);
    elements.refresh?.addEventListener('click', refresh);
    elements.test?.addEventListener('click', () => run('test'));
    elements.preview?.addEventListener('click', () => run('preview'));
    elements.sync?.addEventListener('click', () => run('sync'));
    elements.syncConfirm?.addEventListener('change', render);

    render();

    return Object.freeze({
        async open() {
            render();
            if (api.isUnlocked()) {
                await refresh();
            } else {
                queueMicrotask(() => elements.unlockButton?.focus?.({ preventScroll: true }));
            }
        },
        refresh,
        run,
        lock,
        render,
        setLanguage() {
            render();
        },
        snapshot() {
            return Object.freeze({
                unlocked: api.isUnlocked(),
                busy: state.busy,
                status: state.status,
                diagnostic: state.diagnostic,
                expiresAt: state.expiresAt,
                noticeKey: state.noticeKey
            });
        }
    });
}

export function formatElapsed(value, language = 'en') {
    const milliseconds = nonNegativeInteger(value);
    const locale = language === 'fa' ? 'fa-IR' : 'en-US';
    if (milliseconds < 1000) {
        return `${new Intl.NumberFormat(locale).format(milliseconds)} ms`;
    }
    return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(milliseconds / 1000)} s`;
}

export function formatInteger(value, language = 'en') {
    return new Intl.NumberFormat(language === 'fa' ? 'fa-IR' : 'en-US')
        .format(nonNegativeInteger(value));
}

async function readAPIResponse(response) {
    try {
        return await response.json();
    } catch {
        if (!response.ok) {
            return { error: { code: 'request_failed' } };
        }
        throw new SQLTargetAPIError('invalid_response', response.status);
    }
}

function nonNegativeInteger(value) {
    const number = Number(value);
    if (!Number.isFinite(number) || number <= 0) {
        return 0;
    }
    return Math.min(Number.MAX_SAFE_INTEGER, Math.floor(number));
}

function allowedString(value, allowed, fallback) {
    const normalized = String(value || '').trim().toLowerCase();
    return allowed.has(normalized) ? normalized : fallback;
}

function safeIdentifier(value, fallback) {
    const normalized = String(value || '').trim().toLowerCase();
    if (!normalized || normalized.length > MAX_SAFE_TEXT_LENGTH || !/^[a-z0-9_-]+$/.test(normalized)) {
        return fallback;
    }
    return normalized;
}

function normalizeFailureReason(value) {
    const normalized = String(value || '').trim().toLowerCase();
    if (!normalized) {
        return '';
    }
    return ALLOWED_RECONCILIATION_GUARDS.has(normalized)
        ? normalized
        : 'safety_guard';
}

function safeOpaqueToken(value) {
    const token = String(value || '');
    if (token.length < 16 || token.length > 512 || !/^[A-Za-z0-9_-]+$/.test(token)) {
        return '';
    }
    return token;
}

function safeTimestamp(value) {
    const parsed = Date.parse(String(value || ''));
    return Number.isFinite(parsed) ? new Date(parsed).toISOString() : '';
}

function sqlTargetStatusFingerprint(status) {
    return JSON.stringify({
        configured: status.configured,
        driver: status.driver,
        tableConfigured: status.tableConfigured,
        batchSize: status.batchSize,
        reconciliation: status.reconciliation,
        connectTimeoutMS: status.connectTimeoutMS,
        verifiedTLSConfigured: status.verifiedTLSConfigured
    });
}

function previewAuthorizationFromDiagnostic(
    rawDiagnostic,
    diagnostic,
    statusFingerprint,
    sessionExpiresAt,
    currentTime
) {
    const result = diagnostic?.result;
    if (diagnostic?.status !== 'succeeded' || !result?.dryRun) {
        return null;
    }
    if (result.reconciliation === 'delete_missing') {
        return null;
    }
    const grant = String(rawDiagnostic?.preview_grant || '');
    const rawGrantExpiresAt = Date.parse(String(rawDiagnostic?.preview_grant_expires_at || ''));
    const grantExpiresAt = Math.min(rawGrantExpiresAt, sessionExpiresAt);
    if (!PREVIEW_GRANT_PATTERN.test(grant)
        || !Number.isFinite(rawGrantExpiresAt)
        || !Number.isFinite(grantExpiresAt)
        || grantExpiresAt <= currentTime) {
        return null;
    }
    if (result.reconciliation === 'soft_delete_missing') {
        const evidence = result.reconciliationEvidence;
        const token = String(rawDiagnostic?.result?.reconciliation_evidence?.confirmation_token || '');
        if (!evidence?.applyAllowed || !evidence.confirmationRequired || !RECONCILIATION_TOKEN_PATTERN.test(token)) {
            return null;
        }
        return Object.freeze({
            reconciliation: result.reconciliation,
            token,
            grant,
            grantExpiresAt,
            statusFingerprint
        });
    }
    return Object.freeze({
        reconciliation: 'upsert_only',
        token: '',
        grant,
        grantExpiresAt,
        statusFingerprint
    });
}
