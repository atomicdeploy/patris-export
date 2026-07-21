'use strict';

const {
  PatrisHostError,
  assertEnvelope,
  errorEnvelope
} = require('./contract.cjs');
const {
  isPrivilegedCallAuthorized,
  requirePrivilegedAuthorizer
} = require('./authorization.cjs');

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
  const authorize = options.authorize;
  if (!host || typeof host.handle !== 'function' || typeof host.handleStatus !== 'function') {
    throw new PatrisHostError('HOST_INVALID', 'A privileged Patris host is required.');
  }
  if (typeof getSourceUrl !== 'function') {
    throw new PatrisHostError('TAURI_ORIGIN_INVALID', 'A privileged Tauri source-URL resolver is required.');
  }
  requirePrivilegedAuthorizer(authorize, 'TAURI_AUTHORIZER_REQUIRED', 'A privileged Tauri session/capability authorizer is required.');
  async function authorized(event, request, action) {
    let sourceUrl = '';
    try { sourceUrl = String(await getSourceUrl(event) || ''); } catch { sourceUrl = ''; }
    const requestId = request && request.requestId;
    const method = action === 'invoke' && request ? String(request.method || '') : null;
    if (!await isPrivilegedCallAuthorized(authorize, event, { sourceUrl, action, method })) {
      return errorEnvelope(new PatrisHostError(
        'TAURI_NOT_AUTHORIZED',
        'The current host session is not authorized to use Patris Export.'
      ), { requestId });
    }
    return action === 'status'
      ? host.handleStatus(sourceUrl, requestId)
      : host.handle(sourceUrl, request);
  }
  return Object.freeze({
    async patrisInvoke(event, request) {
      return authorized(event, request || {}, 'invoke');
    },
    async patrisStatus(event, request = {}) {
      return authorized(event, request, 'status');
    }
  });
}

module.exports = {
  createTauriCommandHandlers,
  createTauriRendererClient
};
