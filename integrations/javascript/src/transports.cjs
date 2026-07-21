'use strict';

const fs = require('node:fs');
const net = require('node:net');
const path = require('node:path');
const { fork, spawn } = require('node:child_process');

const { HTTP_METHODS, PatrisHostError, assertAllowedMethod } = require('./contract.cjs');

function requiredFile(filePath, label) {
  const value = String(filePath || '').trim();
  if (!value || !fs.existsSync(value)) {
    throw new PatrisHostError('RUNTIME_MISSING', `${label} was not found.`, { retryable: true });
  }
  return path.resolve(value);
}

function normalizedBaseUrl(value) {
  let parsed;
  try {
    parsed = new URL(String(value || ''));
  } catch (error) {
    throw new PatrisHostError('REST_URL_INVALID', 'The Patris REST base URL must be an absolute HTTP(S) URL.', { cause: error });
  }
  if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) {
    throw new PatrisHostError('REST_URL_INVALID', 'The Patris REST base URL must be HTTP(S) and must not contain credentials.');
  }
  parsed.hash = '';
  parsed.search = '';
  parsed.pathname = parsed.pathname.replace(/\/+$/, '');
  return parsed.toString().replace(/\/$/, '');
}

function timeoutSignal(timeoutMs) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  if (typeof timer.unref === 'function') timer.unref();
  return { signal: controller.signal, cancel: () => clearTimeout(timer) };
}

class RestTransport {
  constructor(options = {}) {
    this.name = options.name || 'rest';
    this.baseUrl = normalizedBaseUrl(options.baseUrl);
    this.fetchImpl = options.fetchImpl || globalThis.fetch;
    this.timeoutMs = Number(options.timeoutMs || 30000);
    this.ready = false;
    if (typeof this.fetchImpl !== 'function') {
      throw new PatrisHostError('FETCH_UNAVAILABLE', 'This privileged host does not provide fetch().');
    }
  }

  async initialize() {
    const app = await this.call('app.get', null, Math.min(this.timeoutMs, 5000));
    if (!app || app.name !== 'Patris Export') {
      throw new PatrisHostError('REST_IDENTITY_MISMATCH', 'The configured endpoint did not identify itself as Patris Export.');
    }
    this.ready = true;
    return { ready: true, mode: this.name, app };
  }

  async call(method, params = null, timeoutMs = this.timeoutMs) {
    const allowed = assertAllowedMethod(method);
    const route = HTTP_METHODS[allowed];
    const timeout = timeoutSignal(timeoutMs);
    try {
      const response = await this.fetchImpl(`${this.baseUrl}${route[1]}`, {
        method: route[0],
        headers: route[0] === 'GET' ? { Accept: 'application/json' } : {
          Accept: 'application/json',
          'Content-Type': 'application/json'
        },
        body: route[0] === 'GET' ? undefined : JSON.stringify(params || {}),
        signal: timeout.signal,
        redirect: 'error'
      });
      if (!response || typeof response.ok !== 'boolean') {
        throw new PatrisHostError('INVALID_RESPONSE', 'The Patris REST transport returned an invalid response.');
      }
      if (!response.ok) {
        throw new PatrisHostError('HTTP_ERROR', `Patris REST returned HTTP ${response.status}.`, {
          retryable: response.status >= 500
        });
      }
      try {
        return await response.json();
      } catch (error) {
        throw new PatrisHostError('INVALID_RESPONSE', 'The Patris REST response was not valid JSON.', { cause: error });
      }
    } catch (error) {
      if (error instanceof PatrisHostError) throw error;
      if (error && error.name === 'AbortError') {
        throw new PatrisHostError('TIMEOUT', `Patris REST ${allowed} timed out.`, { cause: error, retryable: true });
      }
      throw new PatrisHostError('REST_UNAVAILABLE', error && error.message ? error.message : 'Patris REST is unavailable.', {
        cause: error,
        retryable: true
      });
    } finally {
      timeout.cancel();
    }
  }

  async close() {
    this.ready = false;
  }
}

function reserveLoopbackAddress() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      server.close((error) => error ? reject(error) : resolve(`127.0.0.1:${address.port}`));
    });
  });
}

function sleep(milliseconds) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, milliseconds);
    if (typeof timer.unref === 'function') timer.unref();
  });
}

async function stopChild(child, timeoutMs = 5000) {
  // child.killed only means a signal was sent; the process may still be alive.
  // Wait for an actual exit so close() does not report completion early.
  if (!child) return;
  const hasExited = child.exitCode !== null && child.exitCode !== undefined;
  const hasSignalExit = child.signalCode !== null && child.signalCode !== undefined;
  if (hasExited || hasSignalExit) return;
  await new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(forceTimer);
      child.removeListener('exit', finish);
      resolve();
    };
    child.once('exit', finish);
    const forceTimer = setTimeout(() => {
      try { child.kill('SIGKILL'); } catch (_) {}
      finish();
    }, timeoutMs);
    try { child.kill(); } catch (_) { finish(); }
  });
}

async function startExecutableSidecar(options) {
  const executablePath = requiredFile(options.executablePath, 'Patris executable');
  const databasePath = requiredFile(options.databasePath, 'Patris database');
  const address = await reserveLoopbackAddress();
  const child = (options.spawnImpl || spawn)(executablePath, [
    'serve',
    databasePath,
    '--addr', address,
    `--watch=${options.watch === false ? 'false' : 'true'}`,
    '--ipc=false'
  ], {
    cwd: path.dirname(executablePath),
    windowsHide: true,
    stdio: 'ignore'
  });
  let startupError = null;
  child.once('error', (error) => { startupError = error; });
  child.once('exit', (code, signal) => {
    startupError = new Error(`Patris executable exited with code ${code ?? 'none'}${signal ? ` (${signal})` : ''}.`);
  });
  return {
    baseUrl: `http://${address}`,
    startupError: () => startupError,
    close: () => stopChild(child, Number(options.stopTimeoutMs || 5000))
  };
}

class ExecutableTransport {
  constructor(options = {}) {
    this.name = 'executable';
    this.options = { ...options };
    this.startSidecar = options.startSidecar || startExecutableSidecar;
    this.sidecar = null;
    this.rest = null;
  }

  async initialize() {
    this.sidecar = await this.startSidecar(this.options);
    this.rest = new RestTransport({
      name: 'executable',
      baseUrl: this.sidecar.baseUrl,
      fetchImpl: this.options.fetchImpl,
      timeoutMs: this.options.timeoutMs || 30000
    });
    const attempts = Number(this.options.startupAttempts || 40);
    const delayMs = Number(this.options.startupDelayMs || 250);
    let lastError = null;
    for (let attempt = 0; attempt < attempts; attempt += 1) {
      const startupError = this.sidecar.startupError && this.sidecar.startupError();
      if (startupError) throw new PatrisHostError('EXECUTABLE_EXITED', startupError.message, { cause: startupError, retryable: true });
      try {
        const state = await this.rest.initialize();
        return { ...state, mode: this.name };
      } catch (error) {
        lastError = error;
        if (attempt + 1 < attempts) await sleep(delayMs);
      }
    }
    throw new PatrisHostError(
      'EXECUTABLE_START_TIMEOUT',
      `Patris executable started but its loopback API did not become ready${lastError ? `: ${lastError.message}` : '.'}`,
      { cause: lastError, retryable: true }
    );
  }

  call(method, params) {
    if (!this.rest) throw new PatrisHostError('NOT_READY', 'The Patris executable transport is not ready.');
    return this.rest.call(method, params);
  }

  async close() {
    if (this.rest) await this.rest.close();
    this.rest = null;
    if (this.sidecar && typeof this.sidecar.close === 'function') await this.sidecar.close();
    this.sidecar = null;
  }
}

class NativeWorkerTransport {
  constructor(options = {}) {
    this.name = 'dll';
    this.options = { ...options };
    this.worker = null;
    this.pending = new Map();
    this.nextId = 0;
    this.requestTimeoutMs = Number(options.timeoutMs || 30000);
    this.workerPath = path.resolve(options.workerPath || path.join(__dirname, 'native-worker.cjs'));
  }

  async initialize() {
    const addonPath = requiredFile(this.options.addonPath, 'Patris Node-API bridge');
    const dllPath = requiredFile(this.options.dllPath, 'Patris DLL');
    const forkImpl = this.options.forkImpl || fork;
    this.worker = forkImpl(this.workerPath, [], {
      env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
      windowsHide: true,
      stdio: ['ignore', 'ignore', 'ignore', 'ipc']
    });
    this.worker.on('message', (message) => this.onMessage(message));
    this.worker.once('error', (error) => this.onWorkerFailure(error));
    this.worker.once('exit', (code, signal) => {
      this.onWorkerFailure(new Error(`Patris native worker exited with code ${code ?? 'none'}${signal ? ` (${signal})` : ''}.`));
    });
    const result = await this.request('init', {
      addonPath,
      dllPath,
      options: this.options.engineOptions || {}
    });
    if (!result.ready) {
      throw new PatrisHostError('RUNTIME_NOT_CONFIGURED', 'Patris DLL loaded, but no database or config file was supplied.');
    }
    return { ...result, mode: this.name };
  }

  onMessage(message) {
    const pending = message && this.pending.get(message.id);
    if (!pending) return;
    this.pending.delete(message.id);
    clearTimeout(pending.timer);
    if (message.ok) pending.resolve(message.result);
    else pending.reject(new PatrisHostError(message.error && message.error.code || 'NATIVE_ERROR', message.error && message.error.message || 'Patris native call failed.'));
  }

  onWorkerFailure(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(new PatrisHostError('NATIVE_WORKER_EXITED', error.message, { cause: error, retryable: true }));
    }
    this.pending.clear();
    this.worker = null;
  }

  request(action, payload) {
    if (!this.worker || !this.worker.connected) {
      return Promise.reject(new PatrisHostError('NOT_READY', 'The Patris native worker is not running.'));
    }
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new PatrisHostError('TIMEOUT', `Patris native ${action} timed out.`, { retryable: true }));
      }, this.requestTimeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      this.worker.send({ id, action, payload });
    });
  }

  call(method, params) {
    return this.request('call', { method: assertAllowedMethod(method), params: params ?? null });
  }

  async close() {
    const worker = this.worker;
    if (!worker) return;
    try { await this.request('close', {}); } catch (_) {}
    this.worker = null;
    await stopChild(worker, Number(this.options.stopTimeoutMs || 5000));
  }
}

module.exports = {
  ExecutableTransport,
  NativeWorkerTransport,
  RestTransport,
  normalizedBaseUrl,
  reserveLoopbackAddress,
  startExecutableSidecar,
  stopChild
};
