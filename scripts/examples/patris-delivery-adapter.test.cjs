'use strict';

const assert = require('node:assert/strict');
const { once } = require('node:events');
const test = require('node:test');

const {
  SECRET_HEADER,
  buildOutboundRequest,
  configFromOptions,
  createAdapterServer,
  forwardEnvelope,
  validateEnvelope,
} = require('./patris-delivery-adapter.cjs');

function envelope() {
  return {
    schema: 'patris.product-sync',
    schema_version: '1.1',
    event_type: 'update',
    event_id: 'evt-sparse-001',
    products: [
      {
        product_code: '113007045',
        name: null,
        warehouse_stock: { central: 2 },
      },
      {
        product_code: 'EXPLICIT-NULL-PAIR',
        shipping_price_per_kg: null,
        shipping_price_per_kg_currency: null,
      },
    ],
  };
}

function config(overrides = {}) {
  return {
    transport: 'mock',
    listen: { host: '127.0.0.1', port: 0 },
    target: '',
    method: 'patris.productSync',
    action: 'patris_product_sync',
    timeoutMs: 1000,
    failFirst: 0,
    maxBodyBytes: 1024 * 1024,
    ingressSecret: '',
    targetSecret: '',
    ...overrides,
  };
}

test('validates canonical body identity against Patris headers', () => {
  const payload = envelope();
  assert.equal(SECRET_HEADER, 'X-Patris-Product-Sync-Secret');
  assert.equal(validateEnvelope(payload, {
    'X-Patris-Contract': payload.schema,
    'X-Patris-Contract-Version': payload.schema_version,
    'X-Patris-Event-ID': payload.event_id,
  }), payload);

  assert.throws(
    () => validateEnvelope(payload, { 'X-Patris-Event-ID': 'different' }),
    /does not match/,
  );
});

test('JSON-RPC wrapper preserves explicit null and does not invent unseen keys', () => {
  const request = buildOutboundRequest(config({
    transport: 'json-rpc',
    target: 'https://api.example.test/rpc',
    targetSecret: 'test-only',
  }), envelope());
  const body = JSON.parse(request.body);

  assert.equal(body.jsonrpc, '2.0');
  assert.equal(body.id, 'evt-sparse-001');
  assert.equal(body.params.products[0].name, null);
  assert.equal(Object.hasOwn(body.params.products[0], 'name'), true);
  assert.equal(Object.hasOwn(body.params.products[0], 'shipping_method_id'), false);
  assert.equal(Object.hasOwn(body.params.products[0], 'shipping_price_per_kg'), false);
  assert.equal(Object.hasOwn(body.params.products[0], 'shipping_price_per_kg_currency'), false);
  assert.equal(Object.hasOwn(body.params.products[0], 'weight_grams'), false);
  assert.equal(body.params.products[1].shipping_price_per_kg, null);
  assert.equal(body.params.products[1].shipping_price_per_kg_currency, null);
  assert.equal(request.headers['Idempotency-Key'], 'evt-sparse-001');
  assert.equal(request.headers.Authorization, 'Bearer test-only');
});

test('WordPress AJAX wrapper preserves the original sparse envelope', () => {
  const request = buildOutboundRequest(config({
    transport: 'wordpress-ajax',
    target: 'https://wordpress.example.test/wp-admin/admin-ajax.php',
    targetSecret: 'test-only',
  }), envelope());
  const form = new URLSearchParams(request.body);
  const payload = JSON.parse(form.get('payload'));

  assert.equal(form.get('action'), 'patris_product_sync');
  assert.equal(form.get('event_id'), 'evt-sparse-001');
  assert.equal(payload.products[0].name, null);
  assert.equal(Object.hasOwn(payload.products[0], 'shipping_method_id'), false);
  assert.equal(request.headers[SECRET_HEADER], 'test-only');
  assert.equal(Object.hasOwn(request.headers, 'Authorization'), false);
});

test('gRPC JSON gateway receives the direct envelope without a parallel schema', () => {
  const request = buildOutboundRequest(config({
    transport: 'grpc-gateway',
    target: 'https://gateway.example.test/v1/patris:apply',
  }), envelope());
  assert.deepEqual(JSON.parse(request.body), envelope());
});

test('secret-bearing remote adapter targets require HTTPS', () => {
  assert.throws(() => buildOutboundRequest(config({
    transport: 'json-rpc',
    target: 'http://api.example.test/rpc',
    targetSecret: 'must-not-cross-plaintext-http',
  }), envelope()), /requires HTTPS/);

  assert.doesNotThrow(() => buildOutboundRequest(config({
    transport: 'json-rpc',
    target: 'http://127.0.0.1:18082/rpc',
    targetSecret: 'loopback-test',
  }), envelope()));
});

test('CLI configuration resolves secret names without storing them in options', (t) => {
  const variable = 'PATRIS_ADAPTER_TEST_INGRESS_SECRET';
  const previous = process.env[variable];
  process.env[variable] = 'local-test-value';
  t.after(() => {
    if (previous === undefined) delete process.env[variable];
    else process.env[variable] = previous;
  });

  const parsed = configFromOptions({
    transport: 'json-rpc',
    target: 'https://api.example.test/rpc',
    ingress_secret_env: variable,
    listen: 'localhost:18081',
  });
  assert.equal(parsed.ingressSecret, 'local-test-value');
  assert.equal(parsed.action, 'patris_product_sync');
  assert.equal(Object.hasOwn(parsed, 'ingress_secret_env'), false);

  assert.throws(() => configFromOptions({
    transport: 'json-rpc',
    target: 'https://api.example.test/rpc',
  }), /requires --ingress-secret-env/);
});

test('JSON-RPC response identity and method errors are enforced', async () => {
  const payload = envelope();
  let captured;
  const fetchOK = async (url, options) => {
    captured = { url, options };
    return new Response(JSON.stringify({ jsonrpc: '2.0', id: payload.event_id, result: { accepted: true } }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };

  await forwardEnvelope(config({
    transport: 'json-rpc',
    target: 'https://api.example.test/rpc',
  }), payload, fetchOK);
  assert.equal(captured.url, 'https://api.example.test/rpc');
  assert.equal(JSON.parse(captured.options.body).params.event_id, payload.event_id);

  await assert.rejects(
    forwardEnvelope(config({
      transport: 'json-rpc',
      target: 'https://api.example.test/rpc',
    }), payload, async () => new Response(JSON.stringify({
      jsonrpc: '2.0',
      id: payload.event_id,
      error: { code: -32001, message: 'busy', data: { retryable: true } },
    }), { status: 200 })),
    (error) => error.retryable === true && /rejected/.test(error.message),
  );
});

test('mock receiver exposes a retryable failure and then accepts identical event identity', async (t) => {
  const server = createAdapterServer(config({
    failFirst: 1,
    ingressSecret: 'local-ingress-test',
  }));
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => server.close());

  const address = server.address();
  const url = `http://127.0.0.1:${address.port}/ingest`;
  const body = JSON.stringify(envelope());
  const headers = {
    'Content-Type': 'application/json',
    'X-Patris-Contract': 'patris.product-sync',
    'X-Patris-Contract-Version': '1.1',
    'X-Patris-Event-ID': 'evt-sparse-001',
    [SECRET_HEADER]: 'local-ingress-test',
  };

  const first = await fetch(url, { method: 'POST', headers, body });
  assert.equal(first.status, 503);
  assert.equal((await first.json()).details.retryable, true);

  const second = await fetch(url, { method: 'POST', headers, body });
  assert.equal(second.status, 200);
  const accepted = await second.json();
  assert.equal(accepted.data.event_id, 'evt-sparse-001');
  assert.equal(accepted.data.retryable, false);
  assert.equal(accepted.data.pending_products, 0);
  assert.equal(accepted.data.deferred_products, 0);
});
