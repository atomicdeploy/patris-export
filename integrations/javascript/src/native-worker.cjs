'use strict';

const path = require('node:path');

const { ALLOWED_METHODS, assertAllowedMethod } = require('./contract.cjs');

let addon = null;
let handle = null;
let version = null;
let capabilities = null;
let chain = Promise.resolve();

function typedError(error, code = 'NATIVE_ERROR') {
  return {
    code: String(error && error.code || code),
    message: String(error && error.message || error || 'Patris native worker failed.'),
    retryable: false
  };
}

function parseJSON(raw, label) {
  try {
    return JSON.parse(raw);
  } catch (error) {
    const wrapped = new Error(`${label} returned invalid JSON: ${error.message}`);
    wrapped.code = 'INVALID_RESPONSE';
    throw wrapped;
  }
}

function initialize(payload = {}) {
  const addonPath = path.resolve(String(payload.addonPath || ''));
  addon = require(addonPath);
  const dllPath = path.resolve(String(payload.dllPath || ''));
  version = parseJSON(addon.patrisVersion(dllPath), 'PatrisExportVersionJSON');
  const abiVersion = Number(addon.patrisABI(dllPath));
  if (abiVersion !== 1) {
    const error = new Error(`Unsupported Patris Export ABI version ${abiVersion}; this bridge supports ABI 1.`);
    error.code = 'ABI_UNSUPPORTED';
    throw error;
  }
  capabilities = parseJSON(addon.patrisCapabilities(dllPath), 'PatrisExportCapabilitiesJSON');
  const methods = Array.isArray(capabilities.rpc_methods) ? capabilities.rpc_methods : [];
  for (const method of ALLOWED_METHODS) {
    if (!methods.includes(method)) {
      const error = new Error(`Patris DLL capabilities are missing required method ${method}.`);
      error.code = 'ABI_CAPABILITY_MISMATCH';
      throw error;
    }
  }
  const options = payload.options || {};
  const configured = !!options.database_path || (Array.isArray(options.config_files) && options.config_files.length > 0);
  if (configured) handle = addon.patrisCreate(dllPath, JSON.stringify(options));
  return { ready: !!handle, configured, version, abiVersion, capabilities };
}

function call(payload = {}, id) {
  if (!handle) {
    const error = new Error('Patris DLL is loaded but no engine is configured.');
    error.code = 'RUNTIME_NOT_CONFIGURED';
    throw error;
  }
  const method = assertAllowedMethod(payload.method);
  const response = parseJSON(addon.patrisCall(handle, JSON.stringify({
    id,
    method,
    params: payload.params ?? null
  })), 'PatrisExportCall');
  if (!response || response.ok !== true) {
    const error = new Error(response && response.error || `Patris ${method} failed.`);
    error.code = 'NATIVE_CALL_FAILED';
    throw error;
  }
  return response.result ?? null;
}

function close() {
  if (handle) addon.patrisClose(handle);
  handle = null;
  return { closed: true };
}

function dispatch(message) {
  switch (message.action) {
    case 'init': return initialize(message.payload);
    case 'call': return call(message.payload, message.id);
    case 'close': return close();
    default: {
      const error = new Error(`Unknown Patris native worker action: ${message.action}`);
      error.code = 'WORKER_ACTION_INVALID';
      throw error;
    }
  }
}

process.on('message', (message = {}) => {
  chain = chain
    .then(() => dispatch(message))
    .then((result) => process.send && process.send({ id: message.id, ok: true, result }))
    .catch((error) => process.send && process.send({ id: message.id, ok: false, error: typedError(error) }));
});

process.on('disconnect', () => {
  try { close(); } catch (_) {}
  process.exit(0);
});

process.on('exit', () => {
  try { close(); } catch (_) {}
});
