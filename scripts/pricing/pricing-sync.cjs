#!/usr/bin/env node
'use strict';

const { performance } = require('node:perf_hooks');

const CLIENT = 'digitalogic-price-calculator/v1';
const HASH = /^sha256:[a-f0-9]{64}$/;
const LIMIT_MS = 60000;
const HELP = `Usage: node pricing-sync.cjs bulk [--base-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd bulk
       pricing-sync.cmd single PRODUCT_CODE [--site-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd session [--site-url URL] [--timeout-ms N] [--json]

Session: Node.js 22+; authenticate once, then enter one product code per line.
Enter quit or end input to close. Session readiness reports authentication time;
each product receipt measures the subsequent command. No automatic write retry.

Bulk refresh through the existing Go pricing session and /api/refresh delivery:wait.
Default URL: http://127.0.0.1:18080 (loopback only). Node.js 18+; no dependencies.
Default overall budget: 60000 ms, shared by readiness, session, refresh and readback.
An explicit longer --timeout-ms emits a CRITICAL event at 60 seconds while waiting.
Checks HTTP /api/status before and after; outputs a terminal JSON receipt.
No automatic refresh retry. A timeout may have applied changes; inspect the server
receipt before retrying. Single calls WordPress directly; it is not a Go refresh.
Single requires separately configured DIGITALOGIC_PRICING_WRITE_KEY and
DIGITALOGIC_PRICING_WRITE_SECRET environment variables (WooCommerce write key).
Default --site-url: https://digitalogic.ir; HTTPS origin only, no redirects.
Single reports client elapsed time against a strict <1000 ms target; an unchanged
receipt does not prove changed-price latency. It uses committed Patris inputs and
requires PHP authority. No fresh Patris row or independent notification receipt.
Alternatively on the WordPress host, run:
  wp digitalogic pricing recalculate --product-code=YOUR_PATRIS_CODE
It recalculates the committed Patris input; it does not fetch a fresh Patris row.
Authenticated WordPress clients can POST product_code to:
  /wp-json/digitalogic/v1/pricing/products/recalculate
The dedicated source-ingest secret is not a substitute for WordPress permissions.
This endpoint does not expose an independent n8n notification receipt.
Exit: 0 delivered within target, 1 failed/unknown, 2 target missed (bulk >60s,
single >=1000ms), 3 bulk delivered with missing-product deferrals.
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

async function requestJSON(base, path, { body, token, authorization, timeoutMs = 5000, timing } = {}) {
  const requestStarted = performance.now();
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const headers = { Accept: 'application/json', 'X-Patris-Excel-Client': CLIENT };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers['X-Patris-Excel-CSRF-Token'] = token;
    if (authorization) headers.Authorization = authorization;
    const response = await fetch(base + path, {
      method: body === undefined ? 'GET' : 'POST', headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'error', signal: controller.signal,
    });
    if (timing) timing.response_headers_ms = Math.round(performance.now() - requestStarted);
    // Never print a raw response, URL error, header or session token.
    const inspectBusy = path === '/api/refresh' && body !== undefined && response.status === 429;
    const inspectRefreshFailure = path === '/api/refresh' && body !== undefined && response.status === 502;
    if (!response.ok && !inspectBusy && !inspectRefreshFailure) {
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
    if (timing) timing.response_complete_ms = Math.round(performance.now() - requestStarted);
    let data;
    try { data = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
    catch { throw new Error(inspectBusy ? 'http_429' : inspectRefreshFailure ? 'http_502' : 'invalid_json'); }
    if (inspectBusy) {
      // This exact refresh rejection precedes source reads and delivery dispatch.
      throw new Error(data?.success === false && data.code === 'pricing_busy' ? 'pricing_busy' : 'http_429');
    }
    if (inspectRefreshFailure) {
      const allowed = ['delivery_failed', 'delivery_receipt_unresolved', 'owner_projection_unavailable'];
      const failure = new Error(data?.refreshed === true && data?.delivered === false && allowed.includes(data.code) ? data.code : 'http_502');
      if (failure.message !== 'http_502') {
        failure.dispatch_diagnostic = data.dispatch_diagnostic;
        failure.snapshot_timing = data.snapshot_timing;
      }
      throw failure;
    }
    return data;
  } catch (error) {
    if (controller.signal.aborted && error.message !== 'response_too_large') throw new Error('request_timeout');
    if (/^(http_[0-9]{3}|pricing_busy|delivery_failed|delivery_receipt_unresolved|owner_projection_unavailable|response_too_large|invalid_json)$/.test(error.message)) throw error;
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
    result.dispatch_diagnostic = body.dispatch_diagnostic;
    result.snapshot_timing = body.snapshot_timing;
    result.receipt = receiptFrom(body);
    result.delivered = true;
    result.outcome = result.receipt.deferred_missing ? 'delivered_with_deferrals' : 'delivered';
    checkpoints.receipt_ms = Math.round(now() - start);
  } catch (error) {
    result.error = error.message;
    result.dispatch_diagnostic = error.dispatch_diagnostic;
    result.snapshot_timing = error.snapshot_timing;
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

async function runSingle({ productCode, siteUrl = 'https://digitalogic.ir', timeoutMs = LIMIT_MS,
  env = process.env, now = () => performance.now(), session } = {}) {
  if (typeof productCode !== 'string' || !productCode || productCode.trim() !== productCode
    || /[\x00-\x1f\x7f]/.test(productCode)) throw new Error('invalid_product_code');
  let site;
  try { site = new URL(siteUrl); } catch { throw new Error('invalid_site_url'); }
  if (site.protocol !== 'https:' || site.username || site.password || site.search || site.hash
    || site.pathname !== '/') throw new Error('site_url_must_be_https_origin');
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 600000) throw new Error('invalid_timeout_ms');
  const key = env.DIGITALOGIC_PRICING_WRITE_KEY;
  const secret = env.DIGITALOGIC_PRICING_WRITE_SECRET;
  if (!/^ck_[a-f0-9]{40}$/.test(key || '') || !/^cs_[a-f0-9]{40}$/.test(secret || '')) {
    throw new Error('separate_woocommerce_write_credentials_required');
  }
  const start = now();
  const result = { scope: 'single', product_code: productCode, authority: 'php',
    input_scope: 'committed_patris_source', delivered: false, outcome: 'unknown_delivery_outcome',
    receipt_scope: 'wordpress_reconciliation', n8n_notification_receipt: 'not_exposed_by_endpoint' };
  try {
    result.timing = {};
    const body = session ? await session.request(productCode) : await requestJSON(site.origin, '/wp-json/digitalogic/v1/pricing/products/recalculate', {
      body: { product_code: productCode }, timeoutMs, timing: (result.timing = {}),
      authorization: 'Basic ' + Buffer.from(key + ':' + secret).toString('base64'),
    });
    const d = body?.data;
    const counts = ['source_count', 'changed_products', 'updated_products', 'already_current_products',
      'deferred_missing', 'deferred_ambiguous', 'pending_products', 'warning_count', 'elapsed_ms'];
    if (body?.success !== true || d?.schema !== 'digitalogic.pricing-reconcile-result'
      || d.status !== 'reconciled' || d.authority !== 'php' || d.input_scope !== 'committed_patris_source'
      || d.product_code !== productCode || !Array.isArray(d.scope_codes)
      || d.scope_codes.length !== 1 || d.scope_codes[0] !== productCode
      || !HASH.test(d.pricing_revision) || !counts.every(k => Number.isSafeInteger(d[k]) && d[k] >= 0)
      || d.source_count < 1 || !Array.isArray(d.sources) || d.sources.length !== d.source_count
      || d.pending_products || d.deferred_missing || d.deferred_ambiguous
      || !d.sources.every(s => s.target_products === 1 && HASH.test(s.event_id)
        && s.woocommerce && ['pending', 'deferred', 'failed', 'missing', 'ambiguous', 'identity_hazard',
          'materialization_mismatch_stopped'].every(k => s.woocommerce[k] === 0)
        && Number.isSafeInteger(s.woocommerce.updated) && s.woocommerce.updated >= 0
        && Number.isSafeInteger(s.woocommerce.already_applied) && s.woocommerce.already_applied >= 0
        && s.woocommerce.updated + s.woocommerce.already_applied === 1)) {
      throw new Error('terminal_receipt_unverified');
    }
    // Whitelist scalar diagnostics only; never echo arbitrary response fields.
    result.receipt = { status: d.status, pricing_revision: d.pricing_revision };
    for (const k of counts) result.receipt[k === 'elapsed_ms' ? 'server_elapsed_ms' : k] = d[k];
    result.delivered = true;
    result.outcome = d.updated_products > 0 ? 'reconciled_with_updates' : 'already_current';
  } catch (error) {
    result.error = error.message;
    result.next_action = 'Inspect the WordPress product and reconciliation state before any manual retry.';
  }
  const elapsed = now() - start;
  result.elapsed_ms = Math.round(elapsed);
  result.target_ms = 1000;
  if (result.delivered) {
    result.timing.outside_reported_coordinator_ms = Math.max(0, result.elapsed_ms - result.receipt.server_elapsed_ms);
    result.timing.scope = 'Client response timing; outside coordinator includes network and WordPress bootstrap, not network alone.';
  }
  result.performance = elapsed < 1000 ? 'under_1_second' : 'target_missed';
  result.changed_price_latency_proven = false;
  result.exit_code = !result.delivered ? 1 : elapsed >= 1000 ? 2 : 0;
  return result;
}

async function main(args) {
  if (!args.length || args.includes('--help') || args.includes('-h')) { process.stdout.write(HELP); return 0; }
  const mode = args.shift();
  if (!['bulk', 'single', 'session'].includes(mode)) throw new Error('invalid_arguments_use_help');
  const options = {};
  if (mode === 'single') options.productCode = args.shift();
  let json = false;
  while (args.length) {
    const key = args.shift();
    if (key === '--json') json = true;
    else if (key === '--base-url' && mode === 'bulk' && args.length) options.baseUrl = args.shift();
    else if (key === '--site-url' && mode !== 'bulk' && args.length) options.siteUrl = args.shift();
    else if (key === '--timeout-ms' && args.length) options.timeoutMs = Number(args.shift());
    else throw new Error('invalid_arguments_use_help');
  }
  options.log = json ? () => {} : message => process.stderr.write(message + '\n');
  options.onEvent = event => process.stderr.write(json ? JSON.stringify(event) + '\n' : event.message + '\n');
  if (mode === 'session') {
    const { openPricingSession } = require('./pricing-session.cjs');
    const key = process.env.DIGITALOGIC_PRICING_WRITE_KEY;
    const secret = process.env.DIGITALOGIC_PRICING_WRITE_SECRET;
    if (!/^ck_[a-f0-9]{40}$/.test(key || '') || !/^cs_[a-f0-9]{40}$/.test(secret || '')) throw Error('separate_woocommerce_write_credentials_required');
    if (options.timeoutMs !== undefined && (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs < 1 || options.timeoutMs > 600000)) throw Error('invalid_timeout_ms');
    const started = performance.now();
    const session = await openPricingSession({ ...options, siteUrl: options.siteUrl || 'https://digitalogic.ir', authorization: 'Basic ' + Buffer.from(key + ':' + secret).toString('base64') });
    process.stdout.write(JSON.stringify({ event: 'session_ready', authentication_and_connection_ms: Math.round(performance.now() - started) }) + '\n');
    const lines = require('node:readline').createInterface({ input: process.stdin, crlfDelay: Infinity });
    let exitCode = 0;
    try {
      for await (const line of lines) {
        if (line === 'quit') break;
        if (!line) continue;
        const result = await runSingle({ ...options, productCode: line, session });
        process.stdout.write(JSON.stringify(result) + '\n');
        exitCode = Math.max(exitCode, result.exit_code);
        if (!result.delivered) break;
      }
    } finally { lines.close(); session.close(); }
    return exitCode;
  }
  const result = await (mode === 'single' ? runSingle(options) : runBulk(options));
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
module.exports = { runBulk, runSingle, receiptFrom, baseURL, main };
