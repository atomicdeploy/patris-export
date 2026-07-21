'use strict';

const {
  PatrisHostError,
  assertAllowedMethod,
  assertAllowedOrigin,
  compileOriginAllowlist,
  errorEnvelope,
  successEnvelope
} = require('./contract.cjs');

function transportName(transport) {
  return String(transport && (transport.mode || transport.name) || 'unknown');
}

function asTransportError(error, fallbackCode = 'RUNTIME_START_FAILED') {
  if (error instanceof PatrisHostError) return error;
  return new PatrisHostError(fallbackCode, error && error.message ? error.message : String(error), {
    cause: error,
    retryable: true
  });
}

class PrivilegedPatrisHost {
  constructor(options = {}) {
    this.allowedOrigins = compileOriginAllowlist(options.allowedOrigins);
    this.transports = Array.isArray(options.transports) ? options.transports.filter(Boolean) : [];
    this.activeTransport = null;
    this.lifecycle = 'idle';
    this.attempts = [];
    this.initializePromise = null;
    this.closePromise = null;
    this.inflight = new Set();
    this.nextRequestId = 0;
  }

  status() {
    return Object.freeze({
      lifecycle: this.lifecycle,
      ready: this.lifecycle === 'ready' && !!this.activeTransport,
      mode: this.activeTransport ? transportName(this.activeTransport) : 'unavailable',
      attempts: this.attempts.map((attempt) => ({ ...attempt }))
    });
  }

  async initialize() {
    if (this.lifecycle === 'closed') {
      throw new PatrisHostError('HOST_CLOSED', 'The Patris host has already been closed.');
    }
    if (this.lifecycle === 'closing') {
      throw new PatrisHostError('HOST_CLOSING', 'The Patris host is closing.');
    }
    if (this.activeTransport && this.lifecycle === 'ready') return this.status();
    if (this.initializePromise) return this.initializePromise;

    this.lifecycle = 'initializing';
    this.initializePromise = this.initializeTransports();
    try {
      return await this.initializePromise;
    } finally {
      this.initializePromise = null;
    }
  }

  async initializeTransports() {
    this.attempts = [];
    for (const transport of this.transports) {
      const mode = transportName(transport);
      try {
        const state = await transport.initialize();
        if (state && state.ready === false) {
          throw new PatrisHostError('RUNTIME_NOT_CONFIGURED', `${mode} runtime is installed but not configured.`);
        }
        if (this.lifecycle === 'closing' || this.lifecycle === 'closed') {
          await Promise.resolve(transport.close && transport.close()).catch(() => {});
          throw new PatrisHostError('HOST_CLOSING', 'The Patris host started closing during initialization.');
        }
        this.activeTransport = transport;
        this.lifecycle = 'ready';
        return this.status();
      } catch (error) {
        const typed = asTransportError(error);
        this.attempts.push(Object.freeze({ mode, code: typed.code, message: typed.message }));
        await Promise.resolve(transport.close && transport.close()).catch(() => {});
        if (this.lifecycle === 'closing' || this.lifecycle === 'closed') throw typed;
      }
    }
    this.lifecycle = 'failed';
    throw new PatrisHostError(
      'RUNTIME_UNAVAILABLE',
      this.attempts.length
        ? 'No configured Patris runtime could be started.'
        : 'No Patris DLL, executable, or REST runtime is configured.',
      { retryable: true }
    );
  }

  async handle(sourceUrl, request = {}) {
    const requestId = request.requestId === undefined || request.requestId === null
      ? ++this.nextRequestId
      : request.requestId;
    let origin = '';
    let method = '';
    let operation = null;
    let transport = null;
    try {
      origin = assertAllowedOrigin(sourceUrl, this.allowedOrigins);
      method = assertAllowedMethod(request.method);
      if (this.lifecycle === 'closing') {
        throw new PatrisHostError('HOST_CLOSING', 'The Patris host is closing.');
      }
      if (this.lifecycle === 'closed') {
        throw new PatrisHostError('HOST_CLOSED', 'The Patris host has already been closed.');
      }

      // Register the whole initialize-and-call operation before yielding. This
      // makes the request part of the close barrier even when the transport was
      // already ready and initialize() resolves on the next microtask.
      operation = (async () => {
        await this.initialize();
        transport = this.activeTransport;
        if (!transport) {
          throw new PatrisHostError('NOT_READY', 'The Patris runtime is not ready.', { retryable: true });
        }
        return transport.call(method, request.params ?? null);
      })();
      this.inflight.add(operation);
      const result = await operation;
      return successEnvelope(result, { requestId, method, mode: transportName(transport) });
    } catch (error) {
      return errorEnvelope(error, {
        requestId,
        ...(method ? { method } : {}),
        ...(transport || this.activeTransport ? { mode: transportName(transport || this.activeTransport) } : {}),
        ...(origin ? { origin } : {})
      });
    } finally {
      if (operation) this.inflight.delete(operation);
    }
  }

  async handleStatus(sourceUrl, requestId = null) {
    try {
      assertAllowedOrigin(sourceUrl, this.allowedOrigins);
      return successEnvelope(this.status(), { requestId });
    } catch (error) {
      return errorEnvelope(error, { requestId });
    }
  }

  async close() {
    if (this.closePromise) return this.closePromise;
    if (this.lifecycle === 'closed') return this.status();
    this.lifecycle = 'closing';
    this.closePromise = (async () => {
      if (this.initializePromise) await this.initializePromise.catch(() => {});
      await Promise.allSettled(Array.from(this.inflight));
      const active = this.activeTransport;
      this.activeTransport = null;
      try {
        if (active && typeof active.close === 'function') await active.close();
      } catch (error) {
        throw asTransportError(error, 'RUNTIME_CLOSE_FAILED');
      } finally {
        this.lifecycle = 'closed';
      }
      return this.status();
    })();
    return this.closePromise;
  }
}

function wrapExistingElectronBridge(bridge) {
  if (!bridge || typeof bridge.initialize !== 'function' || typeof bridge.call !== 'function') {
    throw new PatrisHostError('BRIDGE_INVALID', 'The existing Electron PatrisBridge instance is invalid.');
  }
  return {
    name: 'electron-bridge',
    async initialize() {
      const state = await bridge.initialize();
      return { ...state, ready: state && state.ready === true };
    },
    call(method, params) {
      return bridge.call(method, params);
    },
    close() {
      return typeof bridge.close === 'function' ? bridge.close() : undefined;
    }
  };
}

module.exports = {
  PrivilegedPatrisHost,
  wrapExistingElectronBridge
};
