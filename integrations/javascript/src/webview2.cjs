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

function createWebView2RendererClient(options = {}) {
  const webview = options.webview;
  const timeoutMs = Number(options.timeoutMs || 30000);
  if (!webview || typeof webview.postMessage !== 'function' || typeof webview.addEventListener !== 'function') {
    throw new PatrisHostError('WEBVIEW2_INVALID', 'window.chrome.webview is required.');
  }
  let nextRequestId = 0;
  let disposed = false;
  const pending = new Map();

  function onMessage(event) {
    const payload = event && event.data;
    if (!payload || payload.channel !== 'patris:result') return;
    const item = pending.get(payload.requestId);
    if (!item) return;
    pending.delete(payload.requestId);
    clearTimeout(item.timer);
    try { item.resolve(assertEnvelope(payload.envelope)); }
    catch (error) { item.resolve(errorEnvelope(error, { requestId: payload.requestId })); }
  }

  webview.addEventListener('message', onMessage);

  function send(action, request) {
    if (disposed) return Promise.resolve(errorEnvelope(new PatrisHostError('CLIENT_DISPOSED', 'The WebView2 Patris client is disposed.'), { requestId: request.requestId }));
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        pending.delete(request.requestId);
        resolve(errorEnvelope(new PatrisHostError('TIMEOUT', `WebView2 Patris ${action} timed out.`, { retryable: true }), {
          requestId: request.requestId
        }));
      }, timeoutMs);
      pending.set(request.requestId, { resolve, timer });
      try {
        webview.postMessage({ channel: 'patris:request', action, request });
      } catch (error) {
        pending.delete(request.requestId);
        clearTimeout(timer);
        resolve(errorEnvelope(new PatrisHostError(
          'WEBVIEW2_SEND_FAILED',
          'The WebView2 native bridge could not accept the Patris request.',
          { retryable: true, cause: error }
        ), { requestId: request.requestId }));
      }
    });
  }

  return Object.freeze({
    call(method, params = null) {
      return send('invoke', { requestId: ++nextRequestId, method, params });
    },
    status() {
      return send('status', { requestId: ++nextRequestId });
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      if (typeof webview.removeEventListener === 'function') webview.removeEventListener('message', onMessage);
      for (const [requestId, item] of pending) {
        clearTimeout(item.timer);
        item.resolve(errorEnvelope(new PatrisHostError('CLIENT_DISPOSED', 'The WebView2 Patris client was disposed.'), { requestId }));
      }
      pending.clear();
    }
  });
}

function createWebView2MessageHandler(options = {}) {
  const host = options.host;
  const getSourceUrl = options.getSourceUrl;
  const postMessage = options.postMessage;
  const authorize = options.authorize;
  if (!host || typeof host.handle !== 'function' || typeof host.handleStatus !== 'function') {
    throw new PatrisHostError('HOST_INVALID', 'A privileged Patris host is required.');
  }
  if (typeof getSourceUrl !== 'function' || typeof postMessage !== 'function') {
    throw new PatrisHostError('WEBVIEW2_HOST_INVALID', 'Privileged WebView2 source-URL and postMessage adapters are required.');
  }
  requirePrivilegedAuthorizer(authorize, 'WEBVIEW2_AUTHORIZER_REQUIRED', 'A privileged WebView2 session/capability authorizer is required.');
  return async function handleWebMessage(event) {
    const payload = event && event.data;
    if (!payload || payload.channel !== 'patris:request' || !payload.request) return false;
    const requestId = payload.request.requestId;
    const action = payload.action === 'status' ? 'status' : payload.action === 'invoke' ? 'invoke' : 'invalid';
    const method = action === 'invoke' ? String(payload.request.method || '') : null;
    let sourceUrl = '';
    try { sourceUrl = String(await getSourceUrl(event) || ''); } catch { sourceUrl = ''; }
    let envelope;
    if (action === 'invalid') {
      envelope = errorEnvelope(new PatrisHostError('WEBVIEW2_REQUEST_INVALID', 'The WebView2 Patris request action is invalid.'), { requestId });
    } else if (!await isPrivilegedCallAuthorized(authorize, event, { sourceUrl, action, method })) {
      envelope = errorEnvelope(new PatrisHostError(
        'WEBVIEW2_NOT_AUTHORIZED',
        'The current host session is not authorized to use Patris Export.'
      ), { requestId });
    } else {
      envelope = action === 'status'
        ? await host.handleStatus(sourceUrl, requestId)
        : await host.handle(sourceUrl, payload.request);
    }
    await postMessage({
      channel: 'patris:result',
      requestId,
      envelope
    }, event);
    return true;
  };
}

module.exports = {
  createWebView2MessageHandler,
  createWebView2RendererClient
};
