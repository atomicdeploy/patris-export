'use strict';

const contract = require('./contract.cjs');
const { PrivilegedPatrisHost, wrapExistingElectronBridge } = require('./privileged-host.cjs');
const { ExecutableTransport, NativeWorkerTransport, RestTransport } = require('./transports.cjs');
const electron = require('./electron.cjs');
const tauri = require('./tauri.cjs');
const webview2 = require('./webview2.cjs');

function createConfiguredHost(options = {}) {
  const transports = [];
  if (options.dll) transports.push(new NativeWorkerTransport(options.dll));
  if (options.executable) transports.push(new ExecutableTransport(options.executable));
  if (options.rest && options.rest.baseUrl) transports.push(new RestTransport(options.rest));
  return new PrivilegedPatrisHost({
    allowedOrigins: options.allowedOrigins,
    transports
  });
}

module.exports = {
  ...contract,
  ...electron,
  ...tauri,
  ...webview2,
  ExecutableTransport,
  NativeWorkerTransport,
  PrivilegedPatrisHost,
  RestTransport,
  createConfiguredHost,
  wrapExistingElectronBridge
};
