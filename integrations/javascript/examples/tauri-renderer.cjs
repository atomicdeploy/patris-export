'use strict';

const { createTauriRendererClient } = require('..');

function createPatrisClient(invoke) {
  return createTauriRendererClient({ invoke });
}

module.exports = { createPatrisClient };
