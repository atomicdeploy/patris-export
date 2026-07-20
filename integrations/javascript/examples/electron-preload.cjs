'use strict';

const { contextBridge, ipcRenderer } = require('electron');
const { createElectronRendererClient } = require('../src/electron.cjs');

const client = createElectronRendererClient({
  invoke: (channel, request) => ipcRenderer.invoke(channel, request)
});

// The renderer receives two typed functions. It never receives require(), a
// filesystem path, a native handle, the Node-API addon, or the Patris DLL.
contextBridge.exposeInMainWorld('patrisExport', {
  status: () => client.status(),
  call: (method, params) => client.call(method, params)
});
