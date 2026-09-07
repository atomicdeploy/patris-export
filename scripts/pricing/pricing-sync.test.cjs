'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { runBulk, baseURL } = require('./pricing-sync.cjs');

const token = 's'.repeat(43);
const receipt = () => ({ refreshed: true, delivered: true, source_revision: 'sha256:' + 'a'.repeat(64),
  delivery: { status: 'accepted', event_id: 'sha256:' + 'b'.repeat(64), attempts: 1,
    pending_products: 0, deferred_products: 0, deferred_missing: 0, deferred_ambiguous: 0 } });

async function fixture(t, refresh, options = {}) {
  const calls = [];
  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks).toString();
    calls.push(req.url);
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/api/status') {
      if (options.failAfter && calls.filter(path => path === '/api/status').length > 1) {
        res.writeHead(503); res.end('{}'); return;
      }
      res.end(JSON.stringify({ timestamp: new Date().toISOString(), patris81: { running: false }, file_access: {} }));
      return;
    }
    assert.equal(req.headers['x-patris-excel-client'], 'digitalogic-price-calculator/v1');
    assert.equal(req.headers['content-type'], 'application/json');
    assert.equal(req.method, 'POST');
    if (req.url === '/api/pricing-sync/session') {
      assert.equal(body, '{}');
      if (options.authFail) { res.writeHead(403); res.end(JSON.stringify({ secret: token })); return; }
      options.onSession?.();
      res.end(JSON.stringify({ csrf_token: token, expires_at: new Date(Date.now() + 600000).toISOString(), metadata: 'descriptive' }));
      return;
    }
    assert.equal(req.url, '/api/refresh');
    assert.equal(req.headers['x-patris-excel-csrf-token'], token);
    assert.deepEqual(JSON.parse(body), { delivery: 'wait' });
    refresh(req, res);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)); });
  return { baseUrl: `http://127.0.0.1:${server.address().port}`, calls };
}

test('actual HTTP session, authenticated refresh, terminal receipt and both readiness checks', async t => {
  const f = await fixture(t, (req, res) => res.end(JSON.stringify(receipt())));
  const logs = [];
  const result = await runBulk({ baseUrl: f.baseUrl, log: line => logs.push(line) });
  assert.deepEqual(f.calls, ['/api/status', '/api/pricing-sync/session', '/api/refresh', '/api/status']);
  assert.equal(result.exit_code, 0);
  assert.equal(result.receipt.event_id, receipt().delivery.event_id);
  assert.equal(result.readiness_before, true);
  assert.equal(result.readiness_after, true);
  assert.equal(result.n8n_notification_receipt, 'not_exposed_by_endpoint');
  assert.ok(result.elapsed_ms >= result.checkpoints_ms.receipt_ms);
  assert.equal(JSON.stringify({ result, logs }).includes(token), false);
});

test('pending receipt is unverified and never retried', async t => {
  const body = receipt(); body.delivery.pending_products = 1;
  const f = await fixture(t, (req, res) => res.end(JSON.stringify(body)));
  const result = await runBulk(f);
  assert.equal(result.delivered, false);
  assert.equal(result.outcome, 'unknown_delivery_outcome');
  assert.equal(result.error, 'terminal_receipt_unverified');
  assert.equal(f.calls.filter(path => path === '/api/refresh').length, 1);
});

test('overall timeout stays unknown, marks post-readiness unverified and never retries', async t => {
  const f = await fixture(t, () => {});
  const result = await runBulk({ ...f, timeoutMs: 30 });
  assert.equal(result.error, 'request_timeout');
  assert.equal(result.outcome, 'unknown_delivery_outcome');
  assert.equal(result.readiness_after, false);
  assert.equal(result.readiness_after_error, 'overall_deadline_exhausted');
  assert.equal(result.exit_code, 1);
  assert.equal(f.calls.filter(path => path === '/api/refresh').length, 1);
});

test('authentication failure never refreshes and does not reveal response secrets', async t => {
  const f = await fixture(t, () => assert.fail('must not refresh'), { authFail: true });
  const result = await runBulk(f);
  assert.equal(result.error, 'http_403');
  assert.equal(result.outcome, 'not_started');
  assert.equal(f.calls.includes('/api/refresh'), false);
  assert.equal(JSON.stringify(result).includes(token), false);
});

test('more than 60 seconds is critical even with a delivered receipt', async t => {
  let time = 0;
  const f = await fixture(t, (req, res) => { time = 60004; res.end(JSON.stringify(receipt())); });
  const result = await runBulk({ ...f, timeoutMs: 90000, now: () => time });
  assert.equal(result.elapsed_ms, 60004);
  assert.equal(result.delivered, true);
  assert.equal(result.performance, 'critical_over_60_seconds');
  assert.equal(result.exit_code, 2);
});

test('default 60-second budget includes session time and never starts a late mutation', async t => {
  let time = 0;
  const f = await fixture(t, () => assert.fail('budget is exhausted'), { onSession: () => { time = 60001; } });
  const result = await runBulk({ ...f, now: () => time });
  assert.equal(result.outcome, 'not_started');
  assert.equal(result.error, 'overall_deadline_exhausted');
  assert.equal(f.calls.includes('/api/refresh'), false);
  assert.equal(result.exit_code, 2);
});

test('longer override emits a critical event at the threshold before receipt arrives', async t => {
  let threshold;
  let cleared = false;
  let time = 0;
  const events = [];
  const f = await fixture(t, (req, res) => {
    time = 60000;
    threshold();
    assert.equal(events.length, 1);
    assert.equal(events[0].event, 'critical_threshold_reached');
    assert.equal(events[0].elapsed_ms, 60000);
    time = 61000;
    res.end(JSON.stringify(receipt()));
  });
  const result = await runBulk({ ...f, timeoutMs: 90000, now: () => time,
    onEvent: event => events.push(event),
    schedule: (callback, delay) => { assert.equal(delay, 60000); threshold = callback; return 1; },
    unschedule: id => { assert.equal(id, 1); cleared = true; },
  });
  assert.equal(result.delivered, true);
  assert.equal(result.exit_code, 2);
  assert.equal(cleared, true);
});

test('post-operation readiness failure prevents a clean success exit', async t => {
  const f = await fixture(t, (req, res) => res.end(JSON.stringify(receipt())), { failAfter: true });
  const result = await runBulk(f);
  assert.equal(result.delivered, true);
  assert.equal(result.readiness_after, false);
  assert.equal(result.exit_code, 1);
});

test('missing-product deferrals are explicit and not full-catalog success', async t => {
  const body = receipt(); body.delivery.deferred_products = 2; body.delivery.deferred_missing = 2;
  const f = await fixture(t, (req, res) => res.end(JSON.stringify(body)));
  const result = await runBulk(f);
  assert.equal(result.outcome, 'delivered_with_deferrals');
  assert.equal(result.exit_code, 3);
});

test('only loopback origins are accepted', () => {
  assert.equal(baseURL('http://127.0.0.1:18080'), 'http://127.0.0.1:18080');
  for (const url of ['https://example.com', 'http://127.0.0.1:18080/path', 'http://secret@localhost', 'http://localhost/?token=secret']) {
    assert.throws(() => baseURL(url), /base_url_must_be_loopback_origin/);
  }
});
