'use strict';

const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const {
  ALLOWED_METHODS,
  ExecutableTransport,
  NativeWorkerTransport,
  PrivilegedPatrisHost,
  RestTransport,
  compileOriginAllowlist,
  createElectronRendererClient,
  createTauriCommandHandlers,
  createTauriRendererClient,
  createWebView2MessageHandler,
  createWebView2RendererClient,
  registerElectronPatrisHost,
  wrapExistingElectronBridge
} = require('../src/index.cjs');
const { stopChild } = require('../src/transports.cjs');

const TRUSTED_ORIGIN = 'https://digitalogic.ir';

function transport(name, handlers = {}) {
  return {
    name,
    initialize: handlers.initialize || (async () => ({ ready: true })),
    call: handlers.call || (async (method) => ({ method })),
    close: handlers.close || (async () => {})
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

test('origin policy is exact and does not accept wildcard or opaque origins', () => {
  const allowlist = compileOriginAllowlist([
    'https://digitalogic.ir/panel/',
    'tauri://localhost/',
    'https://patris.local/'
  ]);
  assert.deepEqual([...allowlist], ['https://digitalogic.ir', 'tauri://localhost', 'https://patris.local']);
  assert.throws(() => compileOriginAllowlist(['https://*.example.com']), /Wildcard/);
  assert.throws(() => compileOriginAllowlist([]), /trusted renderer origin/);
});

test('the canonical method allowlist cannot be extended at runtime', () => {
  assert.equal(Object.isFrozen(ALLOWED_METHODS), true);
  assert.throws(() => ALLOWED_METHODS.push('process.exec'), TypeError);
  assert.equal(ALLOWED_METHODS.includes('process.exec'), false);
});

test('initialization falls back DLL to executable without duplicating a request', async () => {
  let remoteInitializations = 0;
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [
      transport('dll', { initialize: async () => { throw new Error('DLL unavailable'); } }),
      transport('executable', {
        call: async (method) => method === 'records.list'
          ? [{ code: '1001', name: 'Module', final_price: '150000' }]
          : { method }
      }),
      transport('rest', { initialize: async () => { remoteInitializations += 1; return { ready: true }; } })
    ]
  });
  const result = await host.handle(`${TRUSTED_ORIGIN}/panel/`, { method: 'records.list', requestId: 'records-1' });
  assert.equal(result.ok, true);
  assert.equal(result.meta.mode, 'executable');
  assert.equal(result.result[0].final_price, '150000');
  assert.equal(remoteInitializations, 0);
  assert.deepEqual(host.status().attempts.map((item) => item.mode), ['dll']);
  await host.close();
});

test('origin and method failures use the same typed result contract', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  const origin = await host.handle('https://attacker.invalid/', { method: 'records.list' });
  assert.deepEqual({ ok: origin.ok, code: origin.error.code }, { ok: false, code: 'ORIGIN_NOT_ALLOWED' });
  const method = await host.handle(`${TRUSTED_ORIGIN}/`, { method: 'process.exec' });
  assert.deepEqual({ ok: method.ok, code: method.error.code }, { ok: false, code: 'METHOD_NOT_ALLOWED' });
  await host.close();
});

test('close waits for concurrent calls and rejects new work while closing', async () => {
  const calls = [deferred(), deferred()];
  let callIndex = 0;
  let closed = false;
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', {
      call: async () => calls[callIndex++].promise,
      close: async () => { closed = true; }
    })]
  });
  await host.initialize();
  const first = host.handle(TRUSTED_ORIGIN, { method: 'records.list', requestId: 1 });
  const second = host.handle(TRUSTED_ORIGIN, { method: 'status.get', requestId: 2 });
  await new Promise((resolve) => setImmediate(resolve));
  const closing = host.close();
  const rejected = await host.handle(TRUSTED_ORIGIN, { method: 'app.get', requestId: 3 });
  assert.equal(rejected.error.code, 'HOST_CLOSING');
  assert.equal(closed, false);
  calls[1].resolve({ status: 'ok' });
  calls[0].resolve([{ code: '1001' }]);
  assert.equal((await first).ok, true);
  assert.equal((await second).ok, true);
  await closing;
  assert.equal(closed, true);
  assert.equal(host.status().lifecycle, 'closed');
});

test('close waits for a ready-host call admitted on the initialize microtask boundary', async () => {
  let closed = false;
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', {
      call: async () => [{ code: 'RACE-SAFE' }],
      close: async () => { closed = true; }
    })]
  });
  await host.initialize();

  const request = host.handle(TRUSTED_ORIGIN, { method: 'records.list', requestId: 'admitted' });
  const closing = host.close();
  const result = await request;

  assert.equal(result.ok, true);
  assert.equal(result.result[0].code, 'RACE-SAFE');
  await closing;
  assert.equal(closed, true);
  assert.equal(host.status().lifecycle, 'closed');
});

test('close during initialization does not activate or leak a late runtime', async () => {
  const startup = deferred();
  let closeCount = 0;
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', {
      initialize: async () => startup.promise,
      close: async () => { closeCount += 1; }
    })]
  });
  const initializing = host.initialize();
  const closing = host.close();
  startup.resolve({ ready: true });
  await assert.rejects(() => initializing, (error) => error.code === 'HOST_CLOSING');
  await closing;
  assert.equal(host.status().lifecycle, 'closed');
  assert.equal(host.status().ready, false);
  assert.ok(closeCount >= 1);
});

test('a transport close failure is typed without leaving the host stuck closing', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', {
      close: async () => { throw new Error('close failed'); }
    })]
  });
  await host.initialize();

  await assert.rejects(() => host.close(), (error) => error.code === 'RUNTIME_CLOSE_FAILED');
  assert.equal(host.status().lifecycle, 'closed');
  assert.equal(host.status().ready, false);
});

test('missing runtime is reported without leaking implementation details', async () => {
  const host = new PrivilegedPatrisHost({ allowedOrigins: [TRUSTED_ORIGIN], transports: [] });
  const result = await host.handle(TRUSTED_ORIGIN, { method: 'records.list' });
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'RUNTIME_UNAVAILABLE');
  assert.equal(result.error.retryable, true);
  await host.close();
});

test('the existing Electron PatrisBridge interface remains directly reusable', async () => {
  const events = [];
  const existing = {
    async initialize() { events.push('initialize'); return { ready: true, mode: 'dll' }; },
    async call(method) { events.push(method); return [{ code: '42' }]; },
    async close() { events.push('close'); }
  };
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [wrapExistingElectronBridge(existing)]
  });
  const result = await host.handle(TRUSTED_ORIGIN, { method: 'records.list' });
  assert.equal(result.result[0].code, '42');
  await host.close();
  assert.deepEqual(events, ['initialize', 'records.list', 'close']);
});

test('REST transport verifies identity and does not invent an auth token', async () => {
  const requests = [];
  const fetchImpl = async (url, options) => {
    requests.push({ url, options });
    const body = url.endsWith('/api/app')
      ? { name: 'Patris Export', version: { version: '1.2.0' } }
      : [{ code: '1001', stock: { main: 3 } }];
    return { ok: true, status: 200, async json() { return body; } };
  };
  const rest = new RestTransport({ baseUrl: 'https://patris.example/base/', fetchImpl });
  await rest.initialize();
  const records = await rest.call('records.list');
  assert.equal(records[0].stock.main, 3);
  assert.equal(requests[0].url, 'https://patris.example/base/api/app');
  assert.equal(Object.hasOwn(requests[0].options.headers, 'Authorization'), false);
});

test('executable transport delegates to the verified loopback REST contract', async () => {
  let closed = false;
  const fetchImpl = async (url) => ({
    ok: true,
    status: 200,
    async json() {
      return url.endsWith('/api/app') ? { name: 'Patris Export' } : { refreshed: true };
    }
  });
  const executable = new ExecutableTransport({
    startSidecar: async () => ({ baseUrl: 'http://127.0.0.1:39111', startupError: () => null, close: async () => { closed = true; } }),
    fetchImpl,
    startupAttempts: 1
  });
  assert.equal((await executable.initialize()).ready, true);
  assert.deepEqual(await executable.call('refresh'), { refreshed: true });
  await executable.close();
  assert.equal(closed, true);
});

test('child shutdown waits for process exit after a kill signal was already sent', async () => {
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  child.killed = true;
  let exited = false;
  child.kill = () => {
    setImmediate(() => {
      exited = true;
      child.exitCode = 0;
      child.emit('exit', 0, null);
    });
    return true;
  };

  await stopChild(child, 1000);
  assert.equal(exited, true);
});

test('native worker refuses missing addon and DLL before starting a child', async () => {
  const missing = path.join(os.tmpdir(), `missing-patris-${process.pid}`);
  const native = new NativeWorkerTransport({ addonPath: `${missing}.node`, dllPath: `${missing}.dll` });
  await assert.rejects(() => native.initialize(), (error) => error.code === 'RUNTIME_MISSING');
});

class FakeIpcMain {
  constructor() { this.handlers = new Map(); }
  handle(channel, handler) { this.handlers.set(channel, handler); }
  removeHandler(channel) { this.handlers.delete(channel); }
}

test('Electron derives origin from senderFrame and exposes only typed IPC calls', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', { call: async () => [{ code: 'E-1' }] })]
  });
  const ipcMain = new FakeIpcMain();
  const unregister = registerElectronPatrisHost({ ipcMain, host });
  const event = { senderFrame: { url: `${TRUSTED_ORIGIN}/panel/` } };
  const renderer = createElectronRendererClient({
    invoke: (channel, request) => ipcMain.handlers.get(channel)(event, request)
  });
  const result = await renderer.call('records.list', { sourceUrl: 'https://attacker.invalid/' });
  assert.equal(result.ok, true);
  assert.equal(result.result[0].code, 'E-1');
  unregister();
  assert.equal(ipcMain.handlers.size, 0);
  await host.close();
});

test('Electron fails closed when the initiating sender frame is unavailable', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  const ipcMain = new FakeIpcMain();
  registerElectronPatrisHost({ ipcMain, host });
  const event = { sender: { getURL: () => `${TRUSTED_ORIGIN}/panel/` } };

  const result = await ipcMain.handlers.get('patris:invoke')(event, { method: 'records.list' });

  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'ORIGIN_NOT_ALLOWED');
  await host.close();
});

test('Tauri command contract derives origin in the privileged command handler', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: ['tauri://localhost'],
    transports: [transport('dll', { call: async () => [{ code: 'T-1' }] })]
  });
  const handlers = createTauriCommandHandlers({
    host,
    getSourceUrl: async (event) => event.privilegedWindowUrl
  });
  const client = createTauriRendererClient({
    invoke: (command, { request }) => command === 'patris_status'
      ? handlers.patrisStatus({ privilegedWindowUrl: 'tauri://localhost/' }, request)
      : handlers.patrisInvoke({ privilegedWindowUrl: 'tauri://localhost/' }, request)
  });
  const result = await client.call('records.list');
  assert.equal(result.ok, true);
  assert.equal(result.result[0].code, 'T-1');
  await host.close();
});

class FakeWebView {
  constructor() { this.listeners = new Set(); this.outbound = null; }
  addEventListener(name, listener) { if (name === 'message') this.listeners.add(listener); }
  removeEventListener(name, listener) { if (name === 'message') this.listeners.delete(listener); }
  postMessage(payload) { this.outbound(payload); }
  receive(payload) { for (const listener of this.listeners) listener({ data: payload }); }
}

test('WebView2 maps concurrent responses by request id and validates host origin', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: ['https://patris.local'],
    transports: [transport('dll', {
      call: async (method) => method === 'records.list' ? [{ code: 'W-1' }] : { status: 'ok' }
    })]
  });
  const webview = new FakeWebView();
  const handleMessage = createWebView2MessageHandler({
    host,
    getSourceUrl: async () => 'https://patris.local/index.html',
    postMessage: async (payload) => setImmediate(() => webview.receive(payload))
  });
  webview.outbound = (payload) => setImmediate(() => handleMessage({ data: payload, trustedSource: 'native' }));
  const client = createWebView2RendererClient({ webview, timeoutMs: 1000 });
  const [records, status] = await Promise.all([
    client.call('records.list'),
    client.call('status.get')
  ]);
  assert.equal(records.result[0].code, 'W-1');
  assert.equal(status.result.status, 'ok');
  client.dispose();
  assert.equal(webview.listeners.size, 0);
  await host.close();
});

test('WebView2 missing host response becomes a typed timeout', async () => {
  const webview = new FakeWebView();
  webview.outbound = () => {};
  const client = createWebView2RendererClient({ webview, timeoutMs: 10 });
  const result = await client.call('records.list');
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'TIMEOUT');
  client.dispose();
});
