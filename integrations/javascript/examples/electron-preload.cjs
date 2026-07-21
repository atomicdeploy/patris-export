'use strict';

const { contextBridge, ipcRenderer } = require('electron');

// This example is intentionally self-contained. Electron sandboxed preloads
// may load Electron's built-in module but cannot require arbitrary application
// files. Keep the native host, session state, and capability checks in main.
let nextRequestId = 0;

function typedFailure(code, message, requestId) {
  return Object.freeze({
    ok: false,
    error: Object.freeze({ code, message, retryable: false }),
    meta: Object.freeze({ requestId })
  });
}

async function safeInvoke(channel, request) {
  try {
    const response = await ipcRenderer.invoke(channel, request);
    if (!response || typeof response !== 'object' || typeof response.ok !== 'boolean') {
      return typedFailure('INVALID_RESPONSE', 'The privileged Patris host returned an invalid response envelope.', request.requestId);
    }
    if (response.ok && !Object.prototype.hasOwnProperty.call(response, 'result')) {
      return typedFailure('INVALID_RESPONSE', 'The privileged Patris host returned an invalid response envelope.', request.requestId);
    }
    if (!response.ok && (!response.error || typeof response.error.code !== 'string' || typeof response.error.message !== 'string')) {
      return typedFailure('INVALID_RESPONSE', 'The privileged Patris host returned an invalid response envelope.', request.requestId);
    }
    return response;
  } catch {
    return typedFailure('ELECTRON_IPC_FAILED', 'The privileged Patris host could not complete the IPC request.', request.requestId);
  }
}

const client = Object.freeze({
  call(method, params = null) {
    return safeInvoke('patris:invoke', { requestId: ++nextRequestId, method, params });
  },
  status() {
    return safeInvoke('patris:status', { requestId: ++nextRequestId });
  }
});

// The renderer receives two typed functions. It never receives require(), a
// filesystem path, a native handle, the Node-API addon, or the Patris DLL.
contextBridge.exposeInMainWorld('patrisExport', {
  status: () => client.status(),
  call: (method, params) => client.call(method, params)
});
