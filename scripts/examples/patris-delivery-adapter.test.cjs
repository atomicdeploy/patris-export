'use strict';

const assert = require('node:assert/strict');
const { once } = require('node:events');
const fs = require('node:fs');
const path = require('node:path');
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
    event_type: 'update',
    event_id: 'evt-sparse-001',
    source: { id: 'patris-test' },
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
    'X-Patris-Event-ID': payload.event_id,
    'X-Patris-Event': payload.event_type,
    'X-Patris-Source': payload.source.id,
  }), payload);

  assert.throws(
    () => validateEnvelope(payload, { 'X-Patris-Event-ID': 'different' }),
    /does not match/,
  );
});

test('accepts the repository canonical golden envelope without an invented schema version', () => {
  const fixturePath = path.join(__dirname, '..', '..', 'testdata', 'patris-product-sync.synthetic.json');
  const payload = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
  assert.equal(Object.hasOwn(payload, 'schema_version'), false);
  assert.equal(validateEnvelope(payload, {
    'X-Patris-Contract': payload.schema,
    'X-Patris-Event-ID': payload.event_id,
    'X-Patris-Event': payload.event_type,
    'X-Patris-Source': payload.source.id,
  }), payload);
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
  assert.equal(request.headers['X-Patris-Event'], 'update');
  assert.equal(request.headers['X-Patris-Source'], 'patris-test');
  assert.equal(Object.hasOwn(request.headers, 'X-Patris-Contract-Version'), false);
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

test('gRPC JSON gateway receives lossless canonical JSON without a parallel product schema', () => {
  const request = buildOutboundRequest(config({
    transport: 'grpc-gateway',
    target: 'https://gateway.example.test/v1/patris:apply',
  }), envelope());
  assert.deepEqual(JSON.parse(JSON.parse(request.body).envelope_json), envelope());
});

test('all adapter modes preserve exact canonical numeric tokens', () => {
  const raw = '{"schema":"patris.product-sync","event_type":"update","event_id":"evt-exact","source":{"id":"patris-test"},"products":[{"product_code":"EXACT","final_price":100000000000000001,"foreign_price":0.1000000000000000006}]}';
  const payload = validateEnvelope(JSON.parse(raw));

  const rpc = buildOutboundRequest(config({ transport: 'json-rpc', target: 'https://api.example.test/rpc' }), payload, raw);
  assert.match(rpc.body, /"final_price":100000000000000001/);
  assert.match(rpc.body, /"foreign_price":0\.1000000000000000006/);

  const ajax = buildOutboundRequest(config({ transport: 'wordpress-ajax', target: 'https://api.example.test/ajax' }), payload, raw);
  assert.equal(new URLSearchParams(ajax.body).get('payload'), raw);

  const grpc = buildOutboundRequest(config({ transport: 'grpc-gateway', target: 'https://api.example.test/grpc' }), payload, raw);
  assert.equal(JSON.parse(grpc.body).envelope_json, raw);
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

  assert.throws(() => buildOutboundRequest(config({
    transport: 'json-rpc',
    target: 'https://api.example.test/rpc?token=not-allowed',
  }), envelope()), /query parameters/);
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
    return new Response(JSON.stringify({
      jsonrpc: '2.0',
      id: payload.event_id,
      result: {
        status: 'accepted', event_id: payload.event_id, retryable: false,
        pending_products: 0, deferred_products: 0,
      },
    }), {
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

  await assert.rejects(
    forwardEnvelope(config({
      transport: 'json-rpc',
      target: 'https://api.example.test/rpc',
    }), payload, async () => new Response(JSON.stringify({
      jsonrpc: '2.0',
      id: payload.event_id,
      result: { accepted: false, retryable: true },
    }), { status: 200 })),
    /delivery state/,
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
    'X-Patris-Event-ID': 'evt-sparse-001',
    'X-Patris-Event': 'update',
    'X-Patris-Source': 'patris-test',
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

test('WordPress retry-pending state is validated and propagated to Patris', async (t) => {
  const payload = envelope();
  const server = createAdapterServer(config({
    transport: 'wordpress-ajax',
    target: 'https://wordpress.example.test/wp-admin/admin-ajax.php',
    ingressSecret: 'local-ingress-test',
  }), {
    fetchImpl: async () => new Response(JSON.stringify({
      success: true,
      data: {
        status: 'retry_pending', event_id: payload.event_id, retryable: true,
        pending_products: 10, deferred_products: 3,
      },
    }), { status: 200 }),
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => server.close());

  const address = server.address();
  const response = await fetch(`http://127.0.0.1:${address.port}/ingest`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Patris-Contract': payload.schema,
      'X-Patris-Event-ID': payload.event_id,
      'X-Patris-Event': payload.event_type,
      'X-Patris-Source': payload.source.id,
      [SECRET_HEADER]: 'local-ingress-test',
    },
    body: JSON.stringify(payload),
  });
  assert.equal(response.status, 200);
  assert.deepEqual((await response.json()).data, {
    status: 'retry_pending', event_id: payload.event_id, retryable: true,
    pending_products: 10, deferred_products: 3,
  });
});
