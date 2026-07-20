'use strict';

const {
  PrivilegedPatrisHost,
  registerElectronPatrisHost,
  wrapExistingElectronBridge
} = require('../src/index.cjs');

// PatrisBridge is the existing Digitalogic Electron adapter. It already owns
// DLL -> executable -> REST discovery, its utility process, and lifecycle.
function installPatrisElectronHost({ ipcMain, PatrisBridge, settings, allowedOrigins }) {
  const bridge = new PatrisBridge(settings);
  const host = new PrivilegedPatrisHost({
    allowedOrigins,
    transports: [wrapExistingElectronBridge(bridge)]
  });
  const unregister = registerElectronPatrisHost({ ipcMain, host });
  return async () => {
    unregister();
    await host.close();
  };
}

module.exports = { installPatrisElectronHost };
