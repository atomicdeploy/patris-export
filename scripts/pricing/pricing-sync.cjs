#!/usr/bin/env node
'use strict';

const { performance } = require('node:perf_hooks');

const CLIENT = 'digitalogic-price-calculator/v1';
const HASH = /^sha256:[a-f0-9]{64}$/;
const LIMIT_MS = 60000;
const HELP = `Usage: node pricing-sync.cjs bulk [--base-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd bulk

Bulk refresh through the existing Go pricing session and /api/refresh delivery:wait.
Default URL: http://127.0.0.1:18080 (loopback only). Node.js 18+; no dependencies.
Default overall budget: 60000 ms, shared by readiness, session, refresh and readback.
An explicit longer --timeout-ms emits a CRITICAL event at 60 seconds while waiting.
Checks HTTP /api/status before and after; outputs a terminal JSON receipt.
No automatic refresh retry. A timeout may have applied changes; inspect the server
receipt before retrying. Only bulk is supported; the server has no single-product scope.
This endpoint does not expose an independent n8n notification receipt.
Exit: 0 delivered, 1 failed/unknown, 2 over 60s, 3 delivered with missing-product deferrals.
`;

function baseURL(value) {
  let url;
  try { url = new URL(value); } catch { throw new Error('invalid_base_url'); }
  if (!['http:', 'https:'].includes(url.protocol)
    || !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)
    || url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('base_url_must_be_loopback_origin');
  }
  return url.origin;
}

async function requestJSON(base, path, { body, token, timeoutMs = 5000 } = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const headers = { Accept: 'application/json', 'X-Patris-Excel-Client': CLIENT };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers['X-Patris-Excel-CSRF-Token'] = token;
    const response = await fetch(base + path, {
      method: body === undefined ? 'GET' : 'POST', headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'error', signal: controller.signal,
    });
    // Never print a raw response, URL error, header or session token.
    const inspectBusy = path === '/api/refresh' && body !== undefined && response.status === 429;
    if (!response.ok && !inspectBusy) {
      await response.body?.cancel();
      throw new Error('http_' + response.status);
    }
    let length = 0;
    const chunks = [];
    for await (const chunk of response.body) {
      length += chunk.length;
      if (length > 65536) { controller.abort(); throw new Error('response_too_large'); }
      chunks.push(chunk);
    }
    let data;
    try { data = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
    catch { throw new Error(inspectBusy ? 'http_429' : 'invalid_json'); }
    if (inspectBusy) {
      // This exact refresh rejection precedes source reads and delivery dispatch.
      throw new Error(data?.success === false && data.code === 'pricing_busy' ? 'pricing_busy' : 'http_429');
    }
    return data;
  } catch (error) {
    if (controller.signal.aborted && error.message !== 'response_too_large') throw new Error('request_timeout');
    if (/^(http_[0-9]{3}|pricing_busy|response_too_large|invalid_json)$/.test(error.message)) throw error;
    throw new Error('transport_failed');
  } finally { clearTimeout(timer); }
}

function receiptFrom(body) {
  const d = body?.delivery;
  if (body?.refreshed !== true || body.delivered !== true
    || !HASH.test(body.source_revision) || !d || !HASH.test(d.event_id)
    || !['accepted', 'already_current', 'replayed', 'recovered'].includes(d.status)
    || !['attempts', 'pending_products', 'deferred_products', 'deferred_missing', 'deferred_ambiguous']
      .every(key => Number.isSafeInteger(d[key]) && d[key] >= 0)
    || d.attempts < 1 || d.pending_products !== 0 || d.deferred_ambiguous !== 0
    || d.deferred_products !== d.deferred_missing) throw new Error('terminal_receipt_unverified');
  return {
    source_revision: body.source_revision, event_id: d.event_id, status: d.status,
    attempts: d.attempts, pending_products: d.pending_products,
    deferred_products: d.deferred_products, deferred_missing: d.deferred_missing,
    deferred_ambiguous: d.deferred_ambiguous,
  };
}

async function runBulk({ baseUrl = 'http://127.0.0.1:18080', timeoutMs = LIMIT_MS,
  log = () => {}, onEvent = () => {}, now = () => performance.now(),
  schedule = setTimeout, unschedule = clearTimeout } = {}) {
  const base = baseURL(baseUrl);
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 600000) throw new Error('invalid_timeout_ms');
  const start = now();
  const remaining = (cap = timeoutMs) => {
    const budget = timeoutMs - (now() - start);
    if (budget <= 0) throw new Error('overall_deadline_exhausted');
    return Math.max(1, Math.min(cap, Math.ceil(budget)));
  };
  const criticalTimer = schedule(() => onEvent({
    event: 'critical_threshold_reached', elapsed_ms: Math.round(now() - start),
    message: 'CRITICAL: 60 seconds reached; the operation has not completed. No retry was sent.',
  }), LIMIT_MS);
  const checkpoints = {};
  const result = { scope: 'bulk', delivered: false, outcome: 'not_started',
    readiness_before: false, readiness_after: false,
    receipt_scope: 'go_receiver_ack', n8n_notification_receipt: 'not_exposed_by_endpoint' };
  let refreshSent = false;
  const ready = async () => {
    const data = await requestJSON(base, '/api/status', { timeoutMs: remaining(5000) });
    if (!data || typeof data !== 'object' || Array.isArray(data)
      || typeof data.timestamp !== 'string' || !data.patris81 || !data.file_access) {
      throw new Error('http_readiness_unverified');
    }
  };
  try {
    log('Checking Go HTTP readiness...');
    await ready();
    result.readiness_before = true;
    checkpoints.ready_before_ms = Math.round(now() - start);
    log('Opening local pricing session...');
    const session = await requestJSON(base, '/api/pricing-sync/session', { body: {}, timeoutMs: remaining(5000) });
    if (!/^[A-Za-z0-9_-]{43}$/.test(session?.csrf_token)
      || !Number.isFinite(Date.parse(session.expires_at)) || Date.parse(session.expires_at) <= Date.now()) {
      throw new Error('session_unverified');
    }
    checkpoints.session_ms = Math.round(now() - start);
    log('Refreshing bulk prices; waiting for terminal delivery receipt...');
    const refreshBudget = remaining();
    refreshSent = true;
    const body = await requestJSON(base, '/api/refresh', { body: { delivery: 'wait' }, token: session.csrf_token, timeoutMs: refreshBudget });
    result.receipt = receiptFrom(body);
    result.delivered = true;
    result.outcome = result.receipt.deferred_missing ? 'delivered_with_deferrals' : 'delivered';
    checkpoints.receipt_ms = Math.round(now() - start);
  } catch (error) {
    result.error = error.message;
    const rejectedBusy = error.message === 'pricing_busy';
    result.outcome = refreshSent && !rejectedBusy ? 'unknown_delivery_outcome' : 'not_started';
    if (rejectedBusy) result.next_action = 'Wait for the active pricing operation and inspect its delivery receipt before an explicit retry.';
    else if (refreshSent) result.next_action = 'Inspect the existing server delivery receipt before any manual retry.';
  } finally {
    log('Checking Go HTTP readiness after operation...');
    try { await ready(); result.readiness_after = true; }
    catch (error) { result.readiness_after = false; result.readiness_after_error = error.message; }
    unschedule(criticalTimer);
  }
  const elapsed = now() - start;
  result.elapsed_ms = Math.round(elapsed);
  result.checkpoints_ms = checkpoints;
  result.performance = elapsed > LIMIT_MS ? 'critical_over_60_seconds' : 'within_60_seconds';
  result.exit_code = elapsed > LIMIT_MS ? 2
    : !result.delivered || !result.readiness_after ? 1
      : result.receipt.deferred_missing ? 3 : 0;
  return result;
}

async function main(args) {
  if (!args.length || args.includes('--help') || args.includes('-h')) { process.stdout.write(HELP); return 0; }
  if (args.shift() !== 'bulk') throw new Error('only_bulk_is_supported');
  const options = {};
  let json = false;
  while (args.length) {
    const key = args.shift();
    if (key === '--json') json = true;
    else if (key === '--base-url' && args.length) options.baseUrl = args.shift();
    else if (key === '--timeout-ms' && args.length) options.timeoutMs = Number(args.shift());
    else throw new Error('invalid_arguments_use_help');
  }
  options.log = json ? () => {} : message => process.stderr.write(message + '\n');
  options.onEvent = event => process.stderr.write(json ? JSON.stringify(event) + '\n' : event.message + '\n');
  const result = await runBulk(options);
  process.stdout.write(JSON.stringify(result, null, 2) + '\n');
  return result.exit_code;
}

if (require.main === module) {
  main(process.argv.slice(2)).then(code => { process.exitCode = code; }).catch(error => {
    // Only our fixed validation codes reach this boundary; never print error objects.
    const code = /^[a-z_]+$/.test(error.message) ? error.message : 'command_failed';
    process.stderr.write(code + '\n'); process.exitCode = 1;
  });
}
module.exports = { runBulk, receiptFrom, baseURL, main };
