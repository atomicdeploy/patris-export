'use strict';

const { createTauriRendererClient } = require('../src/tauri.cjs');

function createPatrisClient(invoke) {
  return createTauriRendererClient({ invoke });
}

module.exports = { createPatrisClient };
