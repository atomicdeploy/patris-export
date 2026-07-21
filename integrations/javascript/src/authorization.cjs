'use strict';

const { PatrisHostError } = require('./contract.cjs');

function requirePrivilegedAuthorizer(authorize, code, message) {
  if (typeof authorize !== 'function') {
    throw new PatrisHostError(code, message);
  }
  return authorize;
}

async function isPrivilegedCallAuthorized(authorize, event, context) {
  try {
    return await authorize(event, Object.freeze({ ...context })) === true;
  } catch {
    return false;
  }
}

module.exports = {
  isPrivilegedCallAuthorized,
  requirePrivilegedAuthorizer
};
