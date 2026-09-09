#!/usr/bin/env node
'use strict';

const { performance } = require('node:perf_hooks');

const CLIENT = 'digitalogic-price-calculator/v1';
const HASH = /^sha256:[a-f0-9]{64}$/;
const LIMIT_MS = 60000;
const DEFAULT_BULK_TIMEOUT_MS = 180000;
const HELP = `Usage: node pricing-sync.cjs bulk [--base-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd bulk
       pricing-sync.cmd single PRODUCT_CODE [--site-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd session [--site-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd fresh PRODUCT_CODE [--base-url URL] [--timeout-ms N] [--json]
       pricing-sync.cmd currency --request-id UNIQUE_ID [--cny N] [--usd N] [--cny-date YYYY-MM-DD] [--usd-date YYYY-MM-DD] [--json]
       pricing-sync.cmd currency-status --request-id SAME_ID [--json]

Currency commands use the separate WooCommerce credentials below and the owner REST API.
Only supplied fields are sent; omitted dates use owner policy. Default wait: 180000ms.
Use currency-status after an uncertain result; it never resubmits the mutation.
Currency supports --site-url and --timeout-ms. Exit 0 requires confirmed job and owner readback.
Currency progress goes to stderr (JSON events with --json); --quiet-progress disables it.

Session: Node.js 22+; authenticate once, then enter one product code per line.
Enter quit or end input to close. Session readiness reports authentication time;
each product receipt measures the subsequent command. No automatic write retry.

Bulk refresh through the existing Go pricing session and /api/refresh delivery:wait.
Default URL: http://127.0.0.1:18080 (loopback only). Node.js 18+; no dependencies.
Default bulk/fresh observation timeout: 180000 ms; single: 60000 ms.
The timeout covers readiness, session, refresh and readback; --timeout-ms overrides it.
Bulk's 60-second guideline is informational and depends on workload and write mode.
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
Exit: 0 bulk delivered (regardless of guideline) or single within target,
1 failed/unknown, 2 single target missed (>=1000ms),
3 bulk delivered with missing-product deferrals. Bulk timing never overrides delivery.
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

async function requestJSON(base, path, { body, token, authorization, timeoutMs = 5000, timing, identityHeaders } = {}) {
  const requestStarted = performance.now();
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const headers = { Accept: 'application/json', 'X-Patris-Excel-Client': CLIENT };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers['X-Patris-Excel-CSRF-Token'] = token;
    if (authorization) headers.Authorization = authorization;
    if (identityHeaders) {
      for (const name of ['If-Match', 'Idempotency-Key']) {
        if (identityHeaders[name]) headers[name] = identityHeaders[name];
      }
    }
    const response = await fetch(base + path, {
      method: body === undefined ? 'GET' : 'POST', headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'error', signal: controller.signal,
    });
    if (timing) timing.response_headers_ms = Math.round(performance.now() - requestStarted);
    // Never print a raw response, URL error, header or session token.
    const inspectBusy = path === '/api/refresh' && body !== undefined && response.status === 429;
    const inspectRefreshFailure = path === '/api/refresh' && body !== undefined && [502, 503].includes(response.status);
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
    catch { throw new Error(inspectBusy ? 'http_429' : inspectRefreshFailure ? 'http_' + response.status : 'invalid_json'); }
    if (inspectBusy) {
      // This exact refresh rejection precedes source reads and delivery dispatch.
      throw new Error(data?.success === false && data.code === 'pricing_busy' ? 'pricing_busy' : 'http_429');
    }
    if (inspectRefreshFailure) {
      const predispatch = ['pricing_authority_unavailable', 'delivery_unavailable', 'canonical_unavailable'];
      if (response.status === 503 && data?.refreshed === false && data?.delivered === false
          && predispatch.includes(data.code) && !data.dispatch_diagnostic && !data.delivery) {
        const failure = new Error(data.code);
        failure.predispatch = true;
        throw failure;
      }
      const allowed = ['delivery_failed', 'delivery_receipt_unresolved', 'owner_projection_unavailable'];
      const failure = new Error(response.status === 502 && data?.refreshed === true && data?.delivered === false && allowed.includes(data.code) ? data.code : 'http_' + response.status);
      if (allowed.includes(failure.message)) {
        failure.dispatch_diagnostic = data.dispatch_diagnostic;
        failure.snapshot_timing = data.snapshot_timing;
      }
      throw failure;
    }
    return data;
  } catch (error) {
    if (controller.signal.aborted && error.message !== 'response_too_large') throw new Error('request_timeout');
    if (/^(http_[0-9]{3}|pricing_busy|pricing_authority_unavailable|delivery_unavailable|canonical_unavailable|delivery_failed|delivery_receipt_unresolved|owner_projection_unavailable|response_too_large|invalid_json)$/.test(error.message)) throw error;
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

async function runBulk({ baseUrl = 'http://127.0.0.1:18080', timeoutMs = DEFAULT_BULK_TIMEOUT_MS, productCode,
  log = () => {}, onEvent = () => {}, now = () => performance.now(),
  schedule = setTimeout, unschedule = clearTimeout } = {}) {
  const base = baseURL(baseUrl);
  if (productCode !== undefined && (typeof productCode !== 'string' || !/^[0-9]{1,128}$/.test(productCode))) throw Error('invalid_product_code');
  const scoped = productCode !== undefined;
  const targetMs = scoped ? 1000 : LIMIT_MS;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 600000) throw new Error('invalid_timeout_ms');
  const start = now();
  const remaining = (cap = timeoutMs) => {
    const budget = timeoutMs - (now() - start);
    if (budget <= 0) throw new Error('overall_deadline_exhausted');
    return Math.max(1, Math.min(cap, Math.ceil(budget)));
  };
  const timingTimer = schedule(() => onEvent({
    event: scoped ? 'single_target_reached' : 'performance_guideline_exceeded',
    severity: 'info', elapsed_ms: Math.round(now() - start),
    message: scoped ? 'Single-product 1-second target reached; still waiting without retry.' : 'The 60-second workload guideline was reached; waiting for verified delivery without retry.',
  }), targetMs);
  const checkpoints = {};
  const result = { scope: scoped ? 'single' : 'bulk', delivered: false, outcome: 'not_started',
    readiness_before: false, readiness_after: false,
    receipt_scope: 'go_receiver_ack', n8n_notification_receipt: 'not_exposed_by_endpoint' };
  let refreshSent = false;
  const ready = async () => {
    const data = await requestJSON(base, '/api/status', { timeoutMs: remaining(5000) });
    if (!data || typeof data !== 'object' || Array.isArray(data)
      || typeof data.timestamp !== 'string' || !data.patris81 || !data.file_access) {
      throw new Error('http_readiness_unverified');
    }
    const operation = data.pricing_operation;
    if (result.receipt && operation?.dispatch_diagnostic?.delivery?.event_id === result.receipt.event_id) {
      const stages = {};
      for (const key of ['canonical_input', 'source_read', 'canonical_build', 'owner_inputs', 'dispatch', 'source_prepare', 'permit_wait']) {
        const value = operation.stage_ms?.[key];
        if (Number.isSafeInteger(value) && value >= 0) stages[key] = value;
      }
      result.server_stage_ms = stages;
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
    log(scoped ? 'Reading fresh Patris input and delivering the selected product...' : 'Refreshing bulk prices; waiting for terminal delivery receipt...');
    const refreshBudget = remaining();
    refreshSent = true;
    const payload = scoped ? { delivery: 'wait', product_code: productCode } : { delivery: 'wait' };
    const body = await requestJSON(base, '/api/refresh', { body: payload, token: session.csrf_token, timeoutMs: refreshBudget });
    if (scoped) {
      if (body.scope !== 'single' || body.product_code !== productCode || body.input_scope !== 'fresh_patris_source' || !['php', 'go'].includes(body.authority)) throw Error('terminal_receipt_unverified');
      Object.assign(result, { product_code: productCode, input_scope: body.input_scope, authority: body.authority });
    }
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
    const knownRejection = error.dispatch_diagnostic?.outcome_unknown === false;
    result.outcome = error.predispatch || rejectedBusy || !refreshSent ? 'not_started'
      : knownRejection ? 'delivery_failed' : 'unknown_delivery_outcome';
    if (rejectedBusy) result.next_action = 'Wait for the active pricing operation and inspect its delivery receipt before an explicit retry.';
    else if (error.predispatch) result.next_action = 'Resolve the owner or source preparation failure before an explicit retry; delivery was not dispatched.';
    else if (knownRejection) result.next_action = 'Resolve the reported command rejection before an explicit retry.';
    else if (refreshSent) result.next_action = 'Inspect the existing server delivery receipt before any manual retry.';
  } finally {
    log('Checking Go HTTP readiness after operation...');
    try { await ready(); result.readiness_after = true; }
    catch (error) { result.readiness_after = false; result.readiness_after_error = error.message; }
    unschedule(timingTimer);
  }
  const elapsed = now() - start;
  result.elapsed_ms = Math.round(elapsed);
  result.checkpoints_ms = checkpoints;
  const missed = scoped ? elapsed >= targetMs : elapsed > targetMs;
  result[scoped ? 'target_ms' : 'guideline_ms'] = targetMs;
  result.observation_timeout_ms = timeoutMs;
  result.performance = !result.delivered ? 'not_delivered'
    : scoped ? (missed ? 'target_missed' : 'under_1_second') : (missed ? 'over_guideline' : 'within_guideline');
  result.exit_code = !result.delivered || !result.readiness_after ? 1
    : scoped && missed ? 2
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
  result.performance = !result.delivered ? 'not_delivered' : elapsed < 1000 ? 'under_1_second' : 'target_missed';
  result.changed_price_latency_proven = false;
  result.exit_code = !result.delivered ? 1 : elapsed >= 1000 ? 2 : 0;
  return result;
}

async function main(args) {
  if (!args.length || args.includes('--help') || args.includes('-h')) { process.stdout.write(HELP); return 0; }
  const mode = args.shift();
  if (!['bulk', 'single', 'session', 'fresh', 'currency', 'currency-status'].includes(mode)) throw new Error('invalid_arguments_use_help');
  const options = {};
  const currency = mode === 'currency' || mode === 'currency-status';
  if (currency) { options.values = {}; options.observeOnly = mode === 'currency-status'; }
  if (mode === 'single' || mode === 'fresh') options.productCode = args.shift();
  if (mode === 'fresh' && !options.productCode) throw Error('invalid_product_code');
  let json = false;
  while (args.length) {
    const key = args.shift();
    if (key === '--json') json = true;
    else if (key === '--quiet-progress' && currency) options.quietProgress = true;
    else if (key === '--base-url' && ['bulk', 'fresh'].includes(mode) && args.length) options.baseUrl = args.shift();
    else if (key === '--site-url' && (currency || ['single', 'session'].includes(mode)) && args.length) options.siteUrl = args.shift();
    else if (key === '--request-id' && currency && args.length) options.requestId = args.shift();
    else if (currency && ['--cny','--usd','--cny-date','--usd-date'].includes(key) && args.length) {
      const field = { '--cny':'yuan_price', '--usd':'dollar_price', '--cny-date':'cny_effective_date', '--usd-date':'usd_effective_date' }[key];
      if (Object.hasOwn(options.values, field)) throw Error('duplicate_currency_field');
      options.values[field] = args.shift();
    }
    else if (key === '--timeout-ms' && args.length) options.timeoutMs = Number(args.shift());
    else throw new Error('invalid_arguments_use_help');
  }
  options.log = json ? () => {} : message => process.stderr.write(message + '\n');
  options.onEvent = event => process.stderr.write(json ? JSON.stringify(event) + '\n' : event.message + '\n');
  if (currency) {
    if (options.quietProgress) options.onEvent = () => {};
    const result = await require('./currency-owner.cjs').runCurrency({ ...options, requestJSON });
    process.stdout.write(JSON.stringify(result, null, 2) + '\n');
    return result.exit_code;
  }
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
