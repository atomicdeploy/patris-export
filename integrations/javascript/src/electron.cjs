'use strict';

const { PatrisHostError, assertEnvelope, errorEnvelope } = require('./contract.cjs');

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
  const invokeChannel = options.invokeChannel || 'patris:invoke';
  const statusChannel = options.statusChannel || 'patris:status';
  if (!ipcMain || typeof ipcMain.handle !== 'function' || typeof ipcMain.removeHandler !== 'function') {
    throw new PatrisHostError('ELECTRON_IPC_INVALID', 'Electron ipcMain is required.');
  }
  if (!host || typeof host.handle !== 'function' || typeof host.handleStatus !== 'function') {
    throw new PatrisHostError('HOST_INVALID', 'A privileged Patris host is required.');
  }
  ipcMain.removeHandler(invokeChannel);
  ipcMain.removeHandler(statusChannel);
  ipcMain.handle(invokeChannel, (event, request) => host.handle(sourceUrlFromElectronEvent(event), request));
  ipcMain.handle(statusChannel, (event, request = {}) => host.handleStatus(sourceUrlFromElectronEvent(event), request.requestId));
  return () => {
    ipcMain.removeHandler(invokeChannel);
    ipcMain.removeHandler(statusChannel);
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
  registerElectronPatrisHost,
  sourceUrlFromElectronEvent
};
