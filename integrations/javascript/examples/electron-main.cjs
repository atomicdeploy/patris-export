'use strict';

const {
  PrivilegedPatrisHost,
  registerElectronShutdownBarrier,
  registerElectronPatrisHost,
  wrapExistingElectronBridge
} = require('..');

// PatrisBridge is the existing Digitalogic Electron adapter. It already owns
// DLL -> executable -> REST discovery, its utility process, and lifecycle.
function installPatrisElectronHost({ ipcMain, PatrisBridge, settings, allowedOrigins, authorize }) {
  const bridge = new PatrisBridge(settings);
  const host = new PrivilegedPatrisHost({
    allowedOrigins,
    transports: [wrapExistingElectronBridge(bridge)]
  });
  const unregister = registerElectronPatrisHost({ ipcMain, host, authorize });
  return async () => {
    unregister();
    await host.close();
  };
}

function installPatrisElectronShutdown({ app, cleanup, onError }) {
  return registerElectronShutdownBarrier({ app, cleanup, onError });
}

module.exports = { installPatrisElectronHost, installPatrisElectronShutdown };
