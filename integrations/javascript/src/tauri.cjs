'use strict';

const { PatrisHostError, assertEnvelope, errorEnvelope } = require('./contract.cjs');

function createTauriRendererClient(options = {}) {
  const invoke = options.invoke;
  const invokeCommand = options.invokeCommand || 'patris_invoke';
  const statusCommand = options.statusCommand || 'patris_status';
  if (typeof invoke !== 'function') throw new PatrisHostError('TAURI_INVOKE_INVALID', 'Tauri invoke() is required.');
  let nextRequestId = 0;
  async function safeInvoke(command, request) {
    try {
      return assertEnvelope(await invoke(command, { request }));
    } catch (error) {
      return errorEnvelope(error, { requestId: request.requestId });
    }
  }
  return Object.freeze({
    call(method, params = null) {
      const request = { requestId: ++nextRequestId, method, params };
      return safeInvoke(invokeCommand, request);
    },
    status() {
      const request = { requestId: ++nextRequestId };
      return safeInvoke(statusCommand, request);
    }
  });
}

function createTauriCommandHandlers(options = {}) {
  const host = options.host;
  const getSourceUrl = options.getSourceUrl;
  if (!host || typeof host.handle !== 'function' || typeof host.handleStatus !== 'function') {
    throw new PatrisHostError('HOST_INVALID', 'A privileged Patris host is required.');
  }
  if (typeof getSourceUrl !== 'function') {
    throw new PatrisHostError('TAURI_ORIGIN_INVALID', 'A privileged Tauri source-URL resolver is required.');
  }
  return Object.freeze({
    async patrisInvoke(event, request) {
      return host.handle(await getSourceUrl(event), request);
    },
    async patrisStatus(event, request = {}) {
      return host.handleStatus(await getSourceUrl(event), request.requestId);
    }
  });
}

module.exports = {
  createTauriCommandHandlers,
  createTauriRendererClient
};
