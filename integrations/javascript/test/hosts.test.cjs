'use strict';

const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

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
  registerElectronShutdownBarrier,
  registerElectronPatrisHost,
  wrapExistingElectronBridge
} = require('..');
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
          ? { '1001': { name: 'Module', final_price: '150000' } }
          : { method }
      }),
      transport('rest', { initialize: async () => { remoteInitializations += 1; return { ready: true }; } })
    ]
  });
  const result = await host.handle(`${TRUSTED_ORIGIN}/panel/`, { method: 'records.list', requestId: 'records-1' });
  assert.equal(result.ok, true);
  assert.equal(result.meta.mode, 'executable');
  assert.equal(result.result['1001'].final_price, '150000');
  assert.equal(Object.hasOwn(result.result['1001'], 'Code'), false);
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
  calls[0].resolve({ '1001': { name: 'Module' } });
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
      call: async () => ({ 'RACE-SAFE': { name: 'Module' } }),
      close: async () => { closed = true; }
    })]
  });
  await host.initialize();

  const request = host.handle(TRUSTED_ORIGIN, { method: 'records.list', requestId: 'admitted' });
  const closing = host.close();
  const result = await request;

  assert.equal(result.ok, true);
  assert.equal(result.result['RACE-SAFE'].name, 'Module');
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
    async call(method) { events.push(method); return { '42': { name: 'Module' } }; },
    async close() { events.push('close'); }
  };
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [wrapExistingElectronBridge(existing)]
  });
  const result = await host.handle(TRUSTED_ORIGIN, { method: 'records.list' });
  assert.equal(result.result['42'].name, 'Module');
  await host.close();
  assert.deepEqual(events, ['initialize', 'records.list', 'close']);
});

test('REST transport verifies identity and does not invent an auth token', async () => {
  const requests = [];
  const fetchImpl = async (url, options) => {
    requests.push({ url, options });
    const body = url.endsWith('/api/app')
      ? { name: 'Patris Export', version: { version: '1.2.0' } }
      : { '1001': { stock: { main: 3 } } };
    return { ok: true, status: 200, async json() { return body; } };
  };
  const rest = new RestTransport({ baseUrl: 'https://patris.example/base/', fetchImpl });
  await rest.initialize();
  const records = await rest.call('records.list');
  assert.equal(records['1001'].stock.main, 3);
  assert.equal(Object.hasOwn(records['1001'], 'Code'), false);
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

test('sandboxed Electron preload is a standalone file with typed IPC failures', async () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'examples', 'electron-preload.cjs'), 'utf8');
  assert.doesNotMatch(source, /require\(\s*['"](?:\.{1,2}\/|@atomicdeploy)/);

  let exposed;
  let fail = false;
  const invocations = [];
  const electron = {
    contextBridge: {
      exposeInMainWorld(name, value) {
        assert.equal(name, 'patrisExport');
        exposed = value;
      }
    },
    ipcRenderer: {
      async invoke(channel, request) {
        invocations.push({ channel, request });
        if (fail) throw new Error('private IPC failure details');
        return { ok: true, result: { ready: true }, meta: { requestId: request.requestId } };
      }
    }
  };
  vm.runInNewContext(source, {
    require(specifier) {
      assert.equal(specifier, 'electron');
      return electron;
    }
  }, { filename: 'electron-preload.cjs' });

  assert.equal((await exposed.status()).result.ready, true);
  assert.equal(invocations[0].channel, 'patris:status');
  fail = true;
  const failed = await exposed.call('records.list');
  assert.equal(failed.ok, false);
  assert.equal(failed.error.code, 'ELECTRON_IPC_FAILED');
  assert.equal(failed.error.message.includes('private IPC failure details'), false);
});

class FakeIpcMain {
  constructor() { this.handlers = new Map(); }
  handle(channel, handler) { this.handlers.set(channel, handler); }
  removeHandler(channel) { this.handlers.delete(channel); }
}

test('Electron derives origin from senderFrame and exposes only typed IPC calls', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll', { call: async () => ({ 'E-1': { name: 'Module' } }) })]
  });
  const ipcMain = new FakeIpcMain();
  const authorize = async (event, context) => event.wordpressSession?.canManageProducts === true
    && new URL(context.sourceUrl).pathname.startsWith('/panel/');
  const unregister = registerElectronPatrisHost({ ipcMain, host, authorize });
  const event = {
    senderFrame: { url: `${TRUSTED_ORIGIN}/panel/` },
    wordpressSession: { canManageProducts: true }
  };
  const renderer = createElectronRendererClient({
    invoke: (channel, request) => ipcMain.handlers.get(channel)(event, request)
  });
  const result = await renderer.call('records.list', { sourceUrl: 'https://attacker.invalid/' });
  assert.equal(result.ok, true);
  assert.equal(result.result['E-1'].name, 'Module');
  unregister();
  assert.equal(ipcMain.handlers.size, 0);
  await host.close();
});

test('Electron same-origin pages fail closed without privileged session authorization', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  const ipcMain = new FakeIpcMain();
  registerElectronPatrisHost({
    ipcMain,
    host,
    authorize: async (event, context) => event.wordpressSession?.canManageProducts === true
      && new URL(context.sourceUrl).pathname.startsWith('/panel/')
  });
  const event = {
    senderFrame: { url: `${TRUSTED_ORIGIN}/wp-login.php` },
    wordpressSession: { canManageProducts: false }
  };
  const request = {
    requestId: 'unauthorized-1',
    method: 'config.set',
    params: { rendererClaimsAuthorized: true }
  };
  const result = await ipcMain.handlers.get('patris:invoke')(event, request);
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'ELECTRON_NOT_AUTHORIZED');
  assert.equal(result.meta.requestId, 'unauthorized-1');
  assert.equal(host.status().lifecycle, 'idle');
  await host.close();
});

test('Electron host registration requires a privileged authorizer', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  assert.throws(
    () => registerElectronPatrisHost({ ipcMain: new FakeIpcMain(), host }),
    (error) => error.code === 'ELECTRON_AUTHORIZER_REQUIRED'
  );
  await host.close();
});

test('privileged authorizer exceptions fail closed without exposing details', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  const ipcMain = new FakeIpcMain();
  registerElectronPatrisHost({
    ipcMain,
    host,
    authorize: async () => { throw new Error('private session failure details'); }
  });
  const result = await ipcMain.handlers.get('patris:status')({
    senderFrame: { url: `${TRUSTED_ORIGIN}/panel/` }
  }, { requestId: 'auth-error' });
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'ELECTRON_NOT_AUTHORIZED');
  assert.equal(result.error.message.includes('private session failure details'), false);
  assert.equal(host.status().lifecycle, 'idle');
  await host.close();
});

test('Electron fails closed when the initiating sender frame is unavailable', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  const ipcMain = new FakeIpcMain();
  registerElectronPatrisHost({ ipcMain, host, authorize: async () => true });
  const event = { sender: { getURL: () => `${TRUSTED_ORIGIN}/panel/` } };

  const result = await ipcMain.handlers.get('patris:invoke')(event, { method: 'records.list' });

  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'ORIGIN_NOT_ALLOWED');
  await host.close();
});

test('Electron before-quit barrier awaits cleanup exactly once before re-entering quit', async () => {
  const app = new EventEmitter();
  const cleanupGate = deferred();
  let cleanupCalls = 0;
  let quitCalls = 0;
  let prevented = 0;
  app.quit = () => { quitCalls += 1; };
  registerElectronShutdownBarrier({
    app,
    cleanup: async () => { cleanupCalls += 1; await cleanupGate.promise; }
  });

  const event = { preventDefault: () => { prevented += 1; } };
  app.emit('before-quit', event);
  app.emit('before-quit', event);
  assert.equal(prevented, 2);
  assert.equal(cleanupCalls, 0);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(cleanupCalls, 1);
  assert.equal(quitCalls, 0);

  cleanupGate.resolve();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(cleanupCalls, 1);
  assert.equal(quitCalls, 1);
  assert.equal(app.listenerCount('before-quit'), 0);
});

test('Electron shutdown barrier still releases quit when cleanup and error reporting fail', async () => {
  const app = new EventEmitter();
  let quitCalls = 0;
  let errorCalls = 0;
  app.quit = () => { quitCalls += 1; };
  registerElectronShutdownBarrier({
    app,
    cleanup: async () => { throw new Error('private cleanup details'); },
    onError: () => { errorCalls += 1; throw new Error('reporter failed'); }
  });

  app.emit('before-quit', { preventDefault() {} });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(errorCalls, 1);
  assert.equal(quitCalls, 1);
  assert.equal(app.listenerCount('before-quit'), 0);
});

test('Tauri command contract derives origin in the privileged command handler', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: ['tauri://localhost'],
    transports: [transport('dll', { call: async () => ({ 'T-1': { name: 'Module' } }) })]
  });
  const handlers = createTauriCommandHandlers({
    host,
    getSourceUrl: async (event) => event.privilegedWindowUrl,
    authorize: async (event) => event.wordpressSession?.canReadPatris === true
  });
  const client = createTauriRendererClient({
    invoke: (command, { request }) => command === 'patris_status'
      ? handlers.patrisStatus({ privilegedWindowUrl: 'tauri://localhost/', wordpressSession: { canReadPatris: true } }, request)
      : handlers.patrisInvoke({ privilegedWindowUrl: 'tauri://localhost/', wordpressSession: { canReadPatris: true } }, request)
  });
  const result = await client.call('records.list');
  assert.equal(result.ok, true);
  assert.equal(result.result['T-1'].name, 'Module');
  await host.close();
});

test('Tauri same-origin commands fail closed without privileged authorization', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: ['https://digitalogic.ir'],
    transports: [transport('dll')]
  });
  const handlers = createTauriCommandHandlers({
    host,
    getSourceUrl: async (event) => event.privilegedWindowUrl,
    authorize: async (event, context) => event.wordpressSession?.canManageProducts === true
      && new URL(context.sourceUrl).pathname.startsWith('/panel/')
  });
  const result = await handlers.patrisInvoke({
    privilegedWindowUrl: 'https://digitalogic.ir/wp-login.php',
    wordpressSession: { canManageProducts: false }
  }, { requestId: 'tauri-denied', method: 'config.set', params: { authorized: true } });
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'TAURI_NOT_AUTHORIZED');
  assert.equal(host.status().lifecycle, 'idle');
  await host.close();
});

test('Tauri and WebView2 privileged handlers require authorizers', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: [TRUSTED_ORIGIN],
    transports: [transport('dll')]
  });
  assert.throws(
    () => createTauriCommandHandlers({ host, getSourceUrl: async () => TRUSTED_ORIGIN }),
    (error) => error.code === 'TAURI_AUTHORIZER_REQUIRED'
  );
  assert.throws(
    () => createWebView2MessageHandler({
      host,
      getSourceUrl: async () => TRUSTED_ORIGIN,
      postMessage: async () => {}
    }),
    (error) => error.code === 'WEBVIEW2_AUTHORIZER_REQUIRED'
  );
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
      call: async (method) => method === 'records.list' ? { 'W-1': { name: 'Module' } } : { status: 'ok' }
    })]
  });
  const webview = new FakeWebView();
  const handleMessage = createWebView2MessageHandler({
    host,
    getSourceUrl: async () => 'https://patris.local/index.html',
    authorize: async (event) => event.trustedSource === 'native',
    postMessage: async (payload) => setImmediate(() => webview.receive(payload))
  });
  webview.outbound = (payload) => setImmediate(() => handleMessage({ data: payload, trustedSource: 'native' }));
  const client = createWebView2RendererClient({ webview, timeoutMs: 1000 });
  const [records, status] = await Promise.all([
    client.call('records.list'),
    client.call('status.get')
  ]);
  assert.equal(records.result['W-1'].name, 'Module');
  assert.equal(status.result.status, 'ok');
  client.dispose();
  assert.equal(webview.listeners.size, 0);
  await host.close();
});

test('WebView2 same-origin messages return a typed denial without privileged authorization', async () => {
  const host = new PrivilegedPatrisHost({
    allowedOrigins: ['https://digitalogic.ir'],
    transports: [transport('dll')]
  });
  let response;
  const handleMessage = createWebView2MessageHandler({
    host,
    getSourceUrl: async (event) => event.sourceUrl,
    authorize: async (event, context) => event.wordpressSession?.canManageProducts === true
      && new URL(context.sourceUrl).pathname.startsWith('/panel/'),
    postMessage: async (payload) => { response = payload; }
  });
  const handled = await handleMessage({
    sourceUrl: 'https://digitalogic.ir/public/',
    wordpressSession: { canManageProducts: false },
    data: {
      channel: 'patris:request', action: 'invoke',
      request: { requestId: 'webview-denied', method: 'config.set', params: { authorized: true } }
    }
  });
  assert.equal(handled, true);
  assert.equal(response.envelope.ok, false);
  assert.equal(response.envelope.error.code, 'WEBVIEW2_NOT_AUTHORIZED');
  assert.equal(host.status().lifecycle, 'idle');
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

test('WebView2 synchronous native send failure is typed and clears pending work', async () => {
  const webview = new FakeWebView();
  webview.outbound = () => { throw new Error('native bridge unavailable'); };
  const client = createWebView2RendererClient({ webview, timeoutMs: 1000 });
  const result = await client.call('records.list');
  assert.equal(result.ok, false);
  assert.equal(result.error.code, 'WEBVIEW2_SEND_FAILED');
  assert.equal(result.error.retryable, true);
  assert.equal(result.error.message.includes('native bridge unavailable'), false);
  client.dispose();
});
