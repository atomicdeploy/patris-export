import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const moduleSource = await readFile(new URL('../src/sql-target.js', import.meta.url), 'utf8');
const viewerSource = await readFile(new URL('../src/viewer.html', import.meta.url), 'utf8');
const appSource = await readFile(new URL('../src/app.js', import.meta.url), 'utf8');
const styleSource = await readFile(new URL('../src/styles.scss', import.meta.url), 'utf8');
const tableUXSource = await readFile(new URL('../src/table-ux.js', import.meta.url), 'utf8');
const sqlTarget = await import(`data:text/javascript;base64,${Buffer.from(moduleSource).toString('base64')}`);
const tableUX = await import(`data:text/javascript;base64,${Buffer.from(tableUXSource).toString('base64')}`);

const {
    SQL_TARGET_CSRF_HEADER,
    SQL_TARGET_ENDPOINTS,
    SQL_TARGET_OPERATOR_HEADER,
    SQL_TARGET_SYNC_CONFIRMATION,
    SQLTargetAPIError,
    createSQLTargetAPI,
    createSQLTargetController,
    formatElapsed,
    normalizeSQLDiagnostic,
    normalizeSQLTargetStatus,
    sqlTargetStatusModel
} = sqlTarget;

function jsonResponse(status, payload) {
    return {
        ok: status >= 200 && status < 300,
        status,
        async json() {
            return payload;
        }
    };
}

function futureSession() {
    return {
        authenticated: true,
        csrf_token: 'csrf_token_that_is_long_enough_1234',
        expires_at: new Date(Date.now() + 600_000).toISOString()
    };
}

function previewGrant(fill = 'A') {
    return String(fill).repeat(43).slice(0, 43);
}

function previewGrantExpiry(offsetMS = 60_000) {
    return new Date(Date.now() + offsetMS).toISOString();
}

test('SQL target endpoints are bounded to the dedicated protected namespace', () => {
    assert.deepEqual(SQL_TARGET_ENDPOINTS, {
        session: '/api/sql-target/session',
        status: '/api/sql-target/status',
        test: '/api/sql-target/test',
        preview: '/api/sql-target/preview',
        sync: '/api/sql-target/sync',
        lastResult: '/api/sql-target/last-result'
    });
    assert.equal(SQL_TARGET_SYNC_CONFIRMATION, 'manual_sync');
    assert.doesNotMatch(moduleSource, /localStorage|sessionStorage|indexedDB/);
});

test('session bootstrap sends a remote credential once without forging browser security headers', async () => {
    const calls = [];
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            calls.push({ url, options });
            return jsonResponse(200, futureSession());
        }
    });

    await api.unlock('remote-operator-secret');
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, SQL_TARGET_ENDPOINTS.session);
    assert.equal(calls[0].options.method, 'POST');
    assert.equal(calls[0].options.credentials, 'same-origin');
    assert.equal(calls[0].options.cache, 'no-store');
    assert.equal(calls[0].options.headers[SQL_TARGET_OPERATOR_HEADER], 'remote-operator-secret');
    assert.equal(calls[0].options.headers.Origin, undefined);
    assert.equal(calls[0].options.headers.Referer, undefined);
    assert.equal(calls[0].options.headers['Sec-Fetch-Site'], undefined);
    assert.equal(calls[0].options.body, undefined);
});

test('local bootstrap omits the operator header entirely', async () => {
    let request;
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            request = { url, options };
            return jsonResponse(200, futureSession());
        }
    });
    await api.unlock('');
    assert.equal(request.options.headers[SQL_TARGET_OPERATOR_HEADER], undefined);
});

test('protected reads use the in-memory CSRF token and ordinary same-origin fetch metadata', async () => {
    const calls = [];
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            calls.push({ url, options });
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            if (url === SQL_TARGET_ENDPOINTS.status) {
                return jsonResponse(200, {
                    configured: true,
                    driver: 'mysql',
                    table_configured: true,
                    batch_size: 50,
                    reconciliation: 'soft_delete_missing',
                    connect_timeout_ms: 5000,
                    verified_tls_configured: true,
                    busy: false,
                    last_result_available: false
                });
            }
            return jsonResponse(200, { available: false });
        }
    });

    await api.unlock();
    const status = await api.status();
    const last = await api.lastResult();
    assert.equal(status.reconciliation, 'soft_delete_missing');
    assert.equal(last.available, false);
    for (const call of calls.slice(1)) {
        assert.equal(call.options.method, 'GET');
        assert.equal(call.options.headers[SQL_TARGET_CSRF_HEADER], futureSession().csrf_token);
        assert.equal(call.options.headers.Origin, undefined);
        assert.equal(call.options.headers.Referer, undefined);
        assert.equal(call.options.headers['Sec-Fetch-Site'], undefined);
    }
});

test('stored preview diagnostics never authorize sync or expose a returned token', async () => {
    const token = `sha256:${'b2'.repeat(32)}`;
    const grant = previewGrant('B');
    const api = createSQLTargetAPI({
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) {
                return jsonResponse(200, futureSession());
            }
            if (url === SQL_TARGET_ENDPOINTS.lastResult) {
                return jsonResponse(200, {
                    available: true,
                    diagnostic: {
                        operation: 'preview',
                        status: 'succeeded',
                        preview_grant: grant,
                        preview_grant_expires_at: previewGrantExpiry(),
                        result: {
                            dry_run: true,
                            reconciliation: 'soft_delete_missing',
                            reconciliation_evidence: {
                                source_rows: 4,
                                protected_rows: 4,
                                target_rows: 5,
                                missing_rows: 1,
                                confirmation_required: true,
                                apply_allowed: true,
                                confirmation_token: token
                            }
                        }
                    }
                });
            }
            return jsonResponse(200, {});
        }
    });

    await api.unlock();
    const last = await api.lastResult();
    assert.equal(last.available, true);
    assert.equal(last.diagnostic.result.reconciliationEvidence.confirmationAvailable, true);
    assert.doesNotMatch(JSON.stringify(last), /sha256:/);
    assert.doesNotMatch(JSON.stringify(last), new RegExp(grant));
    assert.deepEqual(api.previewState(), {
        ready: false,
        reconciliation: '',
        tokenRequired: false,
        tokenAvailable: false
    });
    await assert.rejects(
        api.run('sync', { reconciliation: 'soft_delete_missing' }),
        error => error.code === 'preview_required'
    );
});

test('test, preview, and sync use exact methods and sync confirmation body', async () => {
    const calls = [];
    const grant = previewGrant('C');
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            calls.push({ url, options });
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            const operation = url.endsWith('/test') ? 'test' : url.endsWith('/preview') ? 'preview' : 'sync';
            return jsonResponse(200, {
                success: true,
                diagnostic: {
                    operation,
                    status: 'succeeded',
                    ...(operation === 'preview' ? {
                        preview_grant: grant,
                        preview_grant_expires_at: previewGrantExpiry()
                    } : {}),
                    result: {
                        inserted: 1,
                        updated: 2,
                        unchanged: 3,
                        deleted: 0,
                        failed: 0,
                        elapsed_ms: 45,
                        dry_run: url.endsWith('/preview'),
                        reconciliation: 'upsert_only'
                    }
                }
            });
        }
    });

    await api.unlock();
    await api.run('test');
    await api.run('preview');
    await api.run('sync');

    const [testCall, previewCall, syncCall] = calls.slice(1);
    assert.equal(testCall.url, SQL_TARGET_ENDPOINTS.test);
    assert.equal(testCall.options.method, 'POST');
    assert.equal(testCall.options.body, undefined);
    assert.equal(previewCall.url, SQL_TARGET_ENDPOINTS.preview);
    assert.equal(previewCall.options.body, undefined);
    assert.equal(syncCall.url, SQL_TARGET_ENDPOINTS.sync);
    assert.equal(syncCall.options.headers['Content-Type'], 'application/json');
    assert.deepEqual(JSON.parse(syncCall.options.body), {
        confirm: SQL_TARGET_SYNC_CONFIRMATION,
        preview_grant: grant
    });
});

test('soft-delete apply carries only the exact lowercase preview digest and consumes it once', async () => {
    const calls = [];
    const token = `sha256:${'a1'.repeat(32)}`;
    const grant = previewGrant('D');
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            calls.push({ url, options });
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            if (url === SQL_TARGET_ENDPOINTS.preview) {
                return jsonResponse(200, {
                    success: true,
                    diagnostic: {
                        operation: 'preview',
                        status: 'succeeded',
                        preview_grant: grant,
                        preview_grant_expires_at: previewGrantExpiry(),
                        record_count: 2,
                        result: {
                            inserted: 0,
                            updated: 0,
                            unchanged: 2,
                            deleted: 1,
                            failed: 0,
                            elapsed_ms: 20,
                            dry_run: true,
                            reconciliation: 'soft_delete_missing',
                            reconciliation_evidence: {
                                source_rows: 2,
                                protected_rows: 1,
                                target_rows: 3,
                                missing_rows: 1,
                                would_soft_delete: 1,
                                already_soft_deleted: 0,
                                would_restore: 0,
                                partial_source_risk: true,
                                confirmation_required: true,
                                apply_allowed: true,
                                confirmation_token: token
                            }
                        }
                    }
                });
            }
            return jsonResponse(200, {
                success: true,
                diagnostic: {
                    operation: 'sync',
                    status: 'succeeded',
                    result: {
                        inserted: 0,
                        updated: 0,
                        unchanged: 2,
                        deleted: 1,
                        failed: 0,
                        elapsed_ms: 25,
                        dry_run: false,
                        reconciliation: 'soft_delete_missing'
                    }
                }
            });
        }
    });
    await api.unlock();
    const preview = await api.run('preview');
    assert.equal(preview.result.reconciliationEvidence.confirmationAvailable, true);
    assert.doesNotMatch(JSON.stringify(preview), /sha256:/);
    assert.doesNotMatch(JSON.stringify(preview), new RegExp(grant));
    assert.deepEqual(api.previewState(), {
        ready: true,
        reconciliation: 'soft_delete_missing',
        tokenRequired: true,
        tokenAvailable: true
    });

    await api.run('sync', { reconciliation: 'soft_delete_missing' });
    const syncBody = JSON.parse(calls.at(-1).options.body);
    assert.deepEqual(syncBody, {
        confirm: SQL_TARGET_SYNC_CONFIRMATION,
        preview_grant: grant,
        reconciliation_token: token
    });
    assert.equal(api.previewState().ready, false);
    await assert.rejects(
        api.run('sync', { reconciliation: 'soft_delete_missing' }),
        error => error.code === 'preview_required'
    );
});

test('any protected target configuration drift invalidates preview authorization', async () => {
    const token = `sha256:${'c3'.repeat(32)}`;
    const grant = previewGrant('E');
    let batchSize = 50;
    const api = createSQLTargetAPI({
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) {
                return jsonResponse(200, futureSession());
            }
            if (url === SQL_TARGET_ENDPOINTS.status) {
                return jsonResponse(200, {
                    configured: true,
                    driver: 'mysql',
                    table_configured: true,
                    batch_size: batchSize,
                    reconciliation: 'soft_delete_missing',
                    connect_timeout_ms: 5000,
                    verified_tls_configured: true
                });
            }
            return jsonResponse(200, {
                diagnostic: {
                    operation: 'preview',
                    status: 'succeeded',
                    preview_grant: grant,
                    preview_grant_expires_at: previewGrantExpiry(),
                    result: {
                        dry_run: true,
                        reconciliation: 'soft_delete_missing',
                        reconciliation_evidence: {
                            source_rows: 2,
                            protected_rows: 2,
                            target_rows: 3,
                            missing_rows: 1,
                            confirmation_required: true,
                            apply_allowed: true,
                            confirmation_token: token
                        }
                    }
                }
            });
        }
    });

    await api.unlock();
    await api.status();
    await api.run('preview');
    assert.equal(api.previewState().ready, true);
    batchSize = 25;
    await api.status();
    assert.equal(api.previewState().ready, false);
    await assert.rejects(
        api.run('sync', { reconciliation: 'soft_delete_missing' }),
        error => error.code === 'preview_required'
    );
});

test('uppercase or malformed reconciliation digests never authorize soft-delete apply', async () => {
    const api = createSQLTargetAPI({
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            return jsonResponse(200, {
                success: true,
                diagnostic: {
                    operation: 'preview',
                    status: 'succeeded',
                    preview_grant: previewGrant('F'),
                    preview_grant_expires_at: previewGrantExpiry(),
                    result: {
                        dry_run: true,
                        reconciliation: 'soft_delete_missing',
                        reconciliation_evidence: {
                            source_rows: 1,
                            apply_allowed: true,
                            confirmation_required: true,
                            confirmation_token: `sha256:${'AF'.repeat(32)}`
                        }
                    }
                }
            });
        }
    });
    await api.unlock();
    const preview = await api.run('preview');
    assert.equal(preview.result.reconciliationEvidence.confirmationAvailable, false);
    assert.equal(api.previewState().ready, false);
});

test('malformed or expired preview grants never authorize an apply', async () => {
    let currentTime = Date.now();
    let grant = 'not-a-valid-grant';
    let grantExpiresAt = currentTime + 1_000;
    const api = createSQLTargetAPI({
        now: () => currentTime,
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) {
                return jsonResponse(200, {
                    authenticated: true,
                    csrf_token: 'csrf_token_that_is_long_enough_1234',
                    expires_at: new Date(currentTime + 600_000).toISOString()
                });
            }
            return jsonResponse(200, {
                diagnostic: {
                    operation: 'preview',
                    status: 'succeeded',
                    preview_grant: grant,
                    preview_grant_expires_at: new Date(grantExpiresAt).toISOString(),
                    result: {
                        dry_run: true,
                        reconciliation: 'upsert_only'
                    }
                }
            });
        }
    });

    await api.unlock();
    await api.run('preview');
    assert.equal(api.previewState().ready, false);

    grant = previewGrant('G');
    await api.run('preview');
    assert.equal(api.previewState().ready, true);
    currentTime = grantExpiresAt + 1;
    assert.equal(api.previewState().ready, false);
    await assert.rejects(
        api.run('sync', { reconciliation: 'upsert_only' }),
        error => error.code === 'preview_required'
    );
});

test('a failed apply consumes the one-time grant and blocks client replay', async () => {
    const grant = previewGrant('H');
    let syncCalls = 0;
    let api;
    api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            if (url === SQL_TARGET_ENDPOINTS.preview) {
                return jsonResponse(200, {
                    diagnostic: {
                        operation: 'preview',
                        status: 'succeeded',
                        preview_grant: grant,
                        preview_grant_expires_at: previewGrantExpiry(),
                        result: {
                            dry_run: true,
                            reconciliation: 'upsert_only'
                        }
                    }
                });
            }
            syncCalls += 1;
            assert.equal(api.previewState().ready, false);
            assert.equal(JSON.parse(options.body).preview_grant, grant);
            return jsonResponse(409, {
                success: false,
                diagnostic: {
                    operation: 'sync',
                    status: 'failed',
                    failure: {
                        code: 'reconciliation_blocked',
                        stage: 'reconciliation',
                        reason: 'preview_required',
                        retryable: false
                    }
                }
            });
        }
    });

    await api.unlock();
    await api.run('preview');
    await assert.rejects(
        api.run('sync', { reconciliation: 'upsert_only' }),
        error => error.diagnostic?.failure?.reason === 'preview_required'
    );
    assert.equal(api.previewState().ready, false);
    await assert.rejects(
        api.run('sync', { reconciliation: 'upsert_only' }),
        error => error.code === 'preview_required'
    );
    assert.equal(syncCalls, 1);
});

test('a failed re-preview clears the prior grant before network completion', async () => {
    const grant = previewGrant('I');
    let previewCalls = 0;
    const api = createSQLTargetAPI({
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            previewCalls += 1;
            if (previewCalls === 1) {
                return jsonResponse(200, {
                    diagnostic: {
                        operation: 'preview',
                        status: 'succeeded',
                        preview_grant: grant,
                        preview_grant_expires_at: previewGrantExpiry(),
                        result: {
                            dry_run: true,
                            reconciliation: 'upsert_only'
                        }
                    }
                });
            }
            return jsonResponse(503, {
                success: false,
                error: { code: 'operation_timeout' }
            });
        }
    });

    await api.unlock();
    await api.run('preview');
    assert.equal(api.previewState().ready, true);
    await assert.rejects(
        api.run('preview'),
        error => error.code === 'operation_timeout'
    );
    assert.equal(api.previewState().ready, false);
});

test('lock revokes the server session with DELETE, CSRF, and no browser-forged headers', async () => {
    const calls = [];
    const api = createSQLTargetAPI({
        fetchImpl: async (url, options) => {
            calls.push({ url, options });
            if (options.method === 'DELETE') return jsonResponse(200, { authenticated: false });
            return jsonResponse(200, futureSession());
        }
    });
    await api.unlock();
    await api.lock();
    const revoke = calls.at(-1);
    assert.equal(revoke.url, SQL_TARGET_ENDPOINTS.session);
    assert.equal(revoke.options.method, 'DELETE');
    assert.equal(revoke.options.headers[SQL_TARGET_CSRF_HEADER], futureSession().csrf_token);
    assert.equal(revoke.options.headers.Origin, undefined);
    assert.equal(api.isUnlocked(), false);
});

test('invalid or forbidden sessions fail closed and clear client authorization state', async () => {
    const invalid = createSQLTargetAPI({
        fetchImpl: async () => jsonResponse(200, {
            authenticated: true,
            csrf_token: 'short',
            expires_at: new Date(Date.now() + 60_000).toISOString()
        })
    });
    await assert.rejects(invalid.unlock(), error => {
        assert.ok(error instanceof SQLTargetAPIError);
        assert.equal(error.code, 'invalid_response');
        return true;
    });
    assert.equal(invalid.isUnlocked(), false);

    let protectedCount = 0;
    const forbidden = createSQLTargetAPI({
        fetchImpl: async url => {
            if (url === SQL_TARGET_ENDPOINTS.session) return jsonResponse(200, futureSession());
            protectedCount += 1;
            return jsonResponse(403, { success: false, error: { code: 'forbidden', message: 'generic' } });
        }
    });
    await forbidden.unlock();
    await assert.rejects(forbidden.status(), error => error.code === 'forbidden');
    assert.equal(protectedCount, 1);
    assert.equal(forbidden.isUnlocked(), false);
});

test('status normalization allowlists fields and clamps malformed numeric values', () => {
    const normalized = normalizeSQLTargetStatus({
        configured: 1,
        driver: 'postgres',
        table_configured: true,
        batch_size: -40,
        reconciliation: 'drop_everything',
        connect_timeout_ms: Number.POSITIVE_INFINITY,
        verified_tls_configured: true,
        busy: 'yes',
        last_result_available: true,
        dsn: 'mysql://should-not-survive',
        server_name: 'private.example'
    });
    assert.deepEqual(normalized, {
        configured: false,
        driver: 'mysql',
        tableConfigured: true,
        batchSize: 0,
        reconciliation: 'upsert_only',
        connectTimeoutMS: 0,
        verifiedTLSConfigured: true,
        busy: false,
        lastResultAvailable: true
    });
    assert.doesNotMatch(JSON.stringify(normalized), /private|dsn|server_name/i);
});

test('diagnostic normalization drops raw messages and all unrecognized connection material', () => {
    const diagnostic = normalizeSQLDiagnostic({
        operation: 'sync',
        status: 'failed',
        started_at: '2026-07-21T00:00:00Z',
        finished_at: '2026-07-21T00:00:01Z',
        record_count: 967,
        probe: {
            connected: false,
            driver: 'mysql',
            vendor: 'mariadb',
            tls: 'encrypted',
            elapsed_ms: 120,
            host: 'private.example'
        },
        result: {
            inserted: -1,
            updated: 2.9,
            unchanged: 3,
            deleted: 4,
            failed: 5,
            elapsed_ms: 999,
            dry_run: false,
            reconciliation: 'soft_delete_missing',
            raw_error: 'secret'
        },
        failure: {
            code: 'connection_failed',
            stage: 'probe',
            reason: 'preview_mismatch',
            retryable: true,
            message: 'mysql://user:password@private.example/catalog',
            certificate_path: 'C:\\secret\\root.pem'
        },
        dsn: 'mysql://secret'
    });

    assert.equal(diagnostic.probe.vendor, 'mariadb');
    assert.equal(diagnostic.result.inserted, 0);
    assert.equal(diagnostic.result.updated, 2);
    assert.deepEqual(diagnostic.failure, {
        code: 'connection_failed',
        stage: 'probe',
        reason: 'preview_mismatch',
        retryable: true
    });
    assert.doesNotMatch(JSON.stringify(diagnostic), /password|private|certificate|dsn|raw_error/i);

    const unknownReason = normalizeSQLDiagnostic({
        failure: { reason: 'mysql://user:password@private.example/catalog' }
    });
    assert.equal(unknownReason.failure.reason, 'safety_guard');
    assert.doesNotMatch(JSON.stringify(unknownReason), /password|private|mysql:\/\//i);
    assert.equal(normalizeSQLDiagnostic({ failure: {} }).failure.reason, '');
});

test('status model and elapsed formatting remain deterministic in EN and FA', () => {
    const status = normalizeSQLTargetStatus({
        configured: true,
        table_configured: true,
        busy: false
    });
    const model = sqlTargetStatusModel(status, {
        operation: 'test',
        status: 'succeeded',
        probe: { connected: true, vendor: 'mysql', tls: 'encrypted', elapsed_ms: 150 }
    });
    assert.equal(model.configuredTone, 'success');
    assert.equal(model.connectionTone, 'success');
    assert.equal(model.vendor, 'mysql');
    assert.equal(formatElapsed(999, 'en'), '999 ms');
    assert.equal(formatElapsed(1500, 'en'), '1.5 s');
    assert.match(formatElapsed(1500, 'fa'), /۱٫۵ s/);
});

class FakeElement {
    constructor(id) {
        this.id = id;
        this.listeners = new Map();
        this.attributes = new Map();
        this.dataset = {};
        this.hidden = false;
        this.disabled = false;
        this.checked = false;
        this.value = '';
        this.textContent = '';
        this.className = '';
        this.dir = '';
        this.lang = '';
        this.focused = false;
    }

    addEventListener(type, listener) {
        const listeners = this.listeners.get(type) || [];
        listeners.push(listener);
        this.listeners.set(type, listeners);
    }

    setAttribute(name, value) {
        this.attributes.set(name, String(value));
    }

    focus() {
        this.focused = true;
    }

    async emit(type) {
        const event = { preventDefault() {} };
        for (const listener of this.listeners.get(type) || []) {
            await listener(event);
        }
    }
}

function fakeDocument() {
    const elements = new Map();
    return {
        elements,
        getElementById(id) {
            if (!elements.has(id)) elements.set(id, new FakeElement(id));
            return elements.get(id);
        }
    };
}

test('controller clears credentials, unlocks, refreshes, and enforces explicit sync confirmation', async () => {
    const documentRef = fakeDocument();
    let unlocked = false;
    let previewReady = false;
    const operations = [];
    const api = {
        async unlock(credential) {
            operations.push(['unlock', credential]);
            unlocked = true;
            return { authenticated: true, expiresAt: new Date(Date.now() + 600_000).toISOString() };
        },
        isUnlocked: () => unlocked,
        lock() {
            unlocked = false;
        },
        invalidate() {
            unlocked = false;
            previewReady = false;
        },
        clearPreview() {
            previewReady = false;
        },
        previewState() {
            return {
                ready: previewReady,
                reconciliation: previewReady ? 'soft_delete_missing' : '',
                tokenRequired: previewReady,
                tokenAvailable: previewReady
            };
        },
        async status() {
            operations.push(['status']);
            return normalizeSQLTargetStatus({
                configured: true,
                driver: 'mysql',
                table_configured: true,
                batch_size: 50,
                reconciliation: 'soft_delete_missing',
                connect_timeout_ms: 5000,
                verified_tls_configured: true,
                busy: false,
                last_result_available: false
            });
        },
        async lastResult() {
            operations.push(['last']);
            return { available: false, diagnostic: null };
        },
        async run(operation) {
            operations.push(['run', operation]);
            if (operation === 'preview') previewReady = true;
            if (operation === 'sync') previewReady = false;
            return normalizeSQLDiagnostic({
                operation,
                status: 'succeeded',
                record_count: 2,
                result: {
                    inserted: 1,
                    updated: 1,
                    unchanged: 0,
                    deleted: 0,
                    failed: 0,
                    elapsed_ms: 20,
                    dry_run: operation === 'preview',
                    reconciliation: 'soft_delete_missing'
                }
            });
        }
    };
    const controller = createSQLTargetController({
        documentRef,
        api,
        translate: key => key,
        getLanguage: () => 'fa'
    });
    const credential = documentRef.getElementById('sqlTargetOperatorCredential');
    credential.value = 'transient-secret';
    await documentRef.getElementById('sqlTargetUnlockForm').emit('submit');

    assert.deepEqual(operations.slice(0, 3), [
        ['unlock', 'transient-secret'],
        ['status'],
        ['last']
    ]);
    assert.equal(credential.value, '');
    assert.equal(documentRef.getElementById('sqlTargetLocked').hidden, true);
    assert.equal(documentRef.getElementById('sqlTargetUnlocked').hidden, false);
    assert.equal(documentRef.getElementById('sqlTargetPanel').dir, 'rtl');

    const sync = documentRef.getElementById('sqlTargetSync');
    assert.equal(sync.disabled, true);
    const confirmation = documentRef.getElementById('sqlTargetSyncConfirm');
    confirmation.checked = true;
    await confirmation.emit('change');
    assert.equal(sync.disabled, true);

    await documentRef.getElementById('sqlTargetPreview').emit('click');
    assert.deepEqual(operations.at(-1), ['run', 'preview']);
    assert.equal(confirmation.checked, false);
    assert.equal(sync.disabled, true);

    confirmation.checked = true;
    await confirmation.emit('change');
    assert.equal(sync.disabled, false);
    await sync.emit('click');
    assert.deepEqual(operations.at(-2), ['run', 'sync']);
    assert.deepEqual(operations.at(-1), ['status']);
    assert.equal(confirmation.checked, false);
});

test('controller clears stale preview readiness and confirmation before retries and refresh errors', async () => {
    const documentRef = fakeDocument();
    let previewReady = false;
    let previewFailure = false;
    let statusFailure = false;
    let previewWasClearedBeforeRetry = false;
    let previewWasClearedBeforeRefresh = false;
    const status = normalizeSQLTargetStatus({
        configured: true,
        driver: 'mysql',
        table_configured: true,
        batch_size: 50,
        reconciliation: 'upsert_only',
        connect_timeout_ms: 5000,
        verified_tls_configured: true
    });
    const api = {
        isUnlocked: () => true,
        clearPreview() {
            previewReady = false;
        },
        previewState() {
            return {
                ready: previewReady,
                reconciliation: previewReady ? 'upsert_only' : '',
                tokenRequired: false,
                tokenAvailable: false
            };
        },
        async status() {
            if (statusFailure) {
                previewWasClearedBeforeRefresh = !previewReady
                    && !documentRef.getElementById('sqlTargetSyncConfirm').checked;
                throw new Error('status unavailable');
            }
            return status;
        },
        async lastResult() {
            return { available: false, diagnostic: null };
        },
        async run(operation) {
            if (operation === 'preview' && previewFailure) {
                previewWasClearedBeforeRetry = !previewReady
                    && !documentRef.getElementById('sqlTargetSyncConfirm').checked;
                throw new SQLTargetAPIError('operation_timeout', 504);
            }
            if (operation === 'preview') previewReady = true;
            return normalizeSQLDiagnostic({
                operation,
                status: 'succeeded',
                result: {
                    dry_run: operation === 'preview',
                    reconciliation: 'upsert_only'
                }
            });
        }
    };
    const controller = createSQLTargetController({
        documentRef,
        api,
        translate: key => key
    });
    await controller.refresh();

    const preview = documentRef.getElementById('sqlTargetPreview');
    const confirmation = documentRef.getElementById('sqlTargetSyncConfirm');
    const sync = documentRef.getElementById('sqlTargetSync');
    await preview.emit('click');
    confirmation.checked = true;
    await confirmation.emit('change');
    assert.equal(previewReady, true);
    assert.equal(sync.disabled, false);

    previewFailure = true;
    await preview.emit('click');
    assert.equal(previewReady, false);
    assert.equal(confirmation.checked, false);
    assert.equal(sync.disabled, true);
    assert.equal(previewWasClearedBeforeRetry, true);

    previewFailure = false;
    await preview.emit('click');
    confirmation.checked = true;
    await confirmation.emit('change');
    assert.equal(sync.disabled, false);

    statusFailure = true;
    await controller.refresh();
    assert.equal(previewReady, false);
    assert.equal(confirmation.checked, false);
    assert.equal(sync.disabled, true);
    assert.equal(previewWasClearedBeforeRefresh, true);
    assert.equal(controller.snapshot().noticeKey, 'sqlTargetRequestFailed');
});

test('viewer contract provides an accessible, redacted, responsive operations dialog', () => {
    for (const id of [
        'sqlTargetBtn',
        'sqlTargetPanel',
        'sqlTargetTitle',
        'sqlTargetUnlockForm',
        'sqlTargetOperatorCredential',
        'sqlTargetUnlockButton',
        'sqlTargetLock',
        'sqlTargetRefresh',
        'sqlTargetTest',
        'sqlTargetPreview',
        'sqlTargetSyncConfirm',
        'sqlTargetSync',
        'sqlTargetLastContent',
        'sqlTargetReconciliationEvidence',
        'sqlTargetFailure',
        'sqlTargetFailureReason'
    ]) {
        assert.match(viewerSource, new RegExp(`id="${id}"`), `missing #${id}`);
    }
    assert.match(viewerSource, /id="sqlTargetPanel"[^>]+role="dialog"[^>]+aria-modal="true"/);
    assert.match(viewerSource, /id="sqlTargetNotice"[^>]+role="status"[^>]+aria-live="polite"/);
    assert.match(viewerSource, /id="sqlTargetOperatorCredential"[^>]+type="password"[^>]+autocomplete="off"/);
    assert.match(viewerSource, /id="sqlTargetSyncConfirm"[^>]+type="checkbox"/);
    assert.doesNotMatch(viewerSource, /mysql:\/\/|password@|private\.example|raw error/i);
});

test('app routing and styles cover modal lifecycle, RTL, dark mode, and narrow screens', () => {
    assert.match(appSource, /import \{ createSQLTargetController \} from '\.\/sql-target\.js'/);
    assert.match(appSource, /'sql-target': 'sqlTargetPanel'/);
    assert.match(appSource, /sqlTargetController\?\.open\(\)/);
    assert.match(appSource, /sqlTargetController\?\.setLanguage\(\)/);
    assert.match(styleSource, /\.sql-target-modal/);
    assert.match(styleSource, /\.dark-mode &/);
    assert.match(styleSource, /\[dir='rtl'\]/);
    assert.match(styleSource, /@media \(max-width: 460px\)/);
});

test('every SQL target UI key has both English and Persian translations', () => {
    const required = [
        'openSQLTarget',
        'sqlTargetTitle',
        'sqlTargetDescription',
        'sqlTargetUnlockTitle',
        'sqlTargetSessionLocked',
        'sqlTargetSessionUnlocked',
        'sqlTargetOverview',
        'sqlTargetTest',
        'sqlTargetPreview',
        'sqlTargetSync',
        'sqlTargetSyncConfirm',
        'sqlTargetLock',
        'sqlTargetLastResult',
        'sqlTargetFailureTitle',
        'sqlTargetFailureReason',
        'sqlTargetTableRequiredNotice',
        'sqlTargetError_invalid_preview_grant',
        'sqlTargetReconciliation_soft_delete_missing',
        'sqlTargetTLS_encrypted',
        'sqlTargetOperationStatus_failed'
    ];
    for (const key of required) {
        assert.notEqual(tableUX.tableText('en', key), key, `missing English ${key}`);
        assert.notEqual(tableUX.tableText('fa', key), key, `missing Persian ${key}`);
    }
    const coverage = tableUX.tableTranslationCoverage();
    assert.deepEqual(coverage.missingEnglish, []);
    assert.deepEqual(coverage.missingPersian, []);
});
