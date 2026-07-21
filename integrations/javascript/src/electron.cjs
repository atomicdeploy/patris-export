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

function sourceUrlFromElectronEvent(event) {
  // sender.getURL() identifies the top-level WebContents, not necessarily the
  // frame that invoked IPC. Fail closed when Electron cannot identify the
  // initiating frame so an untrusted subframe cannot inherit its parent's
  // trusted origin.
  return String(event && event.senderFrame && event.senderFrame.url || '');
}

function registerElectronPatrisHost(options = {}) {
  const ipcMain = options.ipcMain;
  const host = options.host;
  const authorize = options.authorize;
  const invokeChannel = options.invokeChannel || 'patris:invoke';
  const statusChannel = options.statusChannel || 'patris:status';
  if (!ipcMain || typeof ipcMain.handle !== 'function' || typeof ipcMain.removeHandler !== 'function') {
    throw new PatrisHostError('ELECTRON_IPC_INVALID', 'Electron ipcMain is required.');
  }
  if (!host || typeof host.handle !== 'function' || typeof host.handleStatus !== 'function') {
    throw new PatrisHostError('HOST_INVALID', 'A privileged Patris host is required.');
  }
  requirePrivilegedAuthorizer(authorize, 'ELECTRON_AUTHORIZER_REQUIRED', 'A privileged Electron session/capability authorizer is required.');
  async function authorized(event, request, action) {
    const sourceUrl = sourceUrlFromElectronEvent(event);
    const requestId = request && request.requestId;
    const method = action === 'invoke' && request ? String(request.method || '') : null;
    const allowed = await isPrivilegedCallAuthorized(authorize, event, { sourceUrl, action, method });
    if (allowed !== true) {
      return errorEnvelope(new PatrisHostError(
        'ELECTRON_NOT_AUTHORIZED',
        'The current host session is not authorized to use Patris Export.'
      ), { requestId });
    }
    return action === 'status'
      ? host.handleStatus(sourceUrl, requestId)
      : host.handle(sourceUrl, request);
  }
  ipcMain.removeHandler(invokeChannel);
  ipcMain.removeHandler(statusChannel);
  ipcMain.handle(invokeChannel, (event, request = {}) => authorized(event, request, 'invoke'));
  ipcMain.handle(statusChannel, (event, request = {}) => authorized(event, request, 'status'));
  return () => {
    ipcMain.removeHandler(invokeChannel);
    ipcMain.removeHandler(statusChannel);
  };
}

function registerElectronShutdownBarrier(options = {}) {
  const app = options.app;
  const cleanup = options.cleanup;
  const onError = options.onError;
  if (!app || typeof app.on !== 'function' || typeof app.removeListener !== 'function' || typeof app.quit !== 'function') {
    throw new PatrisHostError('ELECTRON_APP_INVALID', 'Electron app lifecycle methods are required.');
  }
  if (typeof cleanup !== 'function') {
    throw new PatrisHostError('ELECTRON_CLEANUP_REQUIRED', 'An awaited Electron Patris cleanup callback is required.');
  }
  if (onError !== undefined && typeof onError !== 'function') {
    throw new PatrisHostError('ELECTRON_CLEANUP_INVALID', 'Electron cleanup error handling must be a function.');
  }

  let started = false;
  let released = false;
  const beforeQuit = (event) => {
    if (released) return;
    if (event && typeof event.preventDefault === 'function') event.preventDefault();
    if (started) return;
    started = true;
    void Promise.resolve()
      .then(() => cleanup())
      .catch((error) => {
        if (!onError) return;
        try { onError(error); } catch { /* Error reporting must not block quit. */ }
      })
      .finally(() => {
        released = true;
        app.removeListener('before-quit', beforeQuit);
        try { app.quit(); }
        catch (error) {
          if (onError) {
            try { onError(error); } catch { /* Error reporting must remain best-effort. */ }
          }
        }
      });
  };
  app.on('before-quit', beforeQuit);
  return () => {
    if (released) return;
    released = true;
    app.removeListener('before-quit', beforeQuit);
  };
}

function createElectronRendererClient(options = {}) {
  const invoke = options.invoke;
  const invokeChannel = options.invokeChannel || 'patris:invoke';
  const statusChannel = options.statusChannel || 'patris:status';
  if (typeof invoke !== 'function') throw new PatrisHostError('ELECTRON_IPC_INVALID', 'Electron ipcRenderer.invoke is required.');
  let nextRequestId = 0;
  async function safeInvoke(channel, request) {
    try {
      return assertEnvelope(await invoke(channel, request));
    } catch (error) {
      return errorEnvelope(error, { requestId: request.requestId });
    }
  }
  return Object.freeze({
    call(method, params = null) {
      const request = { requestId: ++nextRequestId, method, params };
      return safeInvoke(invokeChannel, request);
    },
    status() {
      const request = { requestId: ++nextRequestId };
      return safeInvoke(statusChannel, request);
    }
  });
}

module.exports = {
  createElectronRendererClient,
  registerElectronShutdownBarrier,
  registerElectronPatrisHost,
  sourceUrlFromElectronEvent
};
