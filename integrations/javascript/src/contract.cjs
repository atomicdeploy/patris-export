'use strict';

const ALLOWED_METHODS = Object.freeze([
  'app.get',
  'records.list',
  'info.get',
  'status.get',
  'config.get',
  'config.set',
  'toast.show',
  'refresh'
]);

const ALLOWED_METHOD_SET = new Set(ALLOWED_METHODS);

const HTTP_METHODS = Object.freeze({
  'app.get': Object.freeze(['GET', '/api/app']),
  'records.list': Object.freeze(['GET', '/api/records']),
  'info.get': Object.freeze(['GET', '/api/info']),
  'status.get': Object.freeze(['GET', '/api/status']),
  'config.get': Object.freeze(['GET', '/api/config']),
  'config.set': Object.freeze(['PUT', '/api/config']),
  'toast.show': Object.freeze(['POST', '/api/toast']),
  refresh: Object.freeze(['POST', '/api/refresh'])
});

class PatrisHostError extends Error {
  constructor(code, message, options = {}) {
    super(String(message || code));
    this.name = 'PatrisHostError';
    this.code = String(code || 'HOST_ERROR');
    this.retryable = options.retryable === true;
    if (options.cause !== undefined) this.cause = options.cause;
  }
}

function normalizeOrigin(value) {
  const raw = String(value || '').trim();
  if (!raw || raw === 'null') return '';
  let parsed;
  try {
    parsed = new URL(raw);
  } catch (error) {
    throw new PatrisHostError('ORIGIN_INVALID', 'The caller URL is not a valid absolute URL.', { cause: error });
  }
  if (parsed.username || parsed.password) {
    throw new PatrisHostError('ORIGIN_INVALID', 'Origins containing credentials are not accepted.');
  }
  if (parsed.origin !== 'null') return parsed.origin;
  if (parsed.protocol === 'file:') return 'file://';
  if (!parsed.host) {
    throw new PatrisHostError('ORIGIN_INVALID', 'Opaque caller origins are not accepted.');
  }
  return `${parsed.protocol}//${parsed.host}`;
}

function compileOriginAllowlist(origins) {
  if (!Array.isArray(origins) || origins.length === 0) {
    throw new PatrisHostError('ORIGIN_POLICY_INVALID', 'At least one trusted renderer origin is required.');
  }
  const normalized = new Set();
  for (const origin of origins) {
    if (String(origin).includes('*')) {
      throw new PatrisHostError('ORIGIN_POLICY_INVALID', 'Wildcard renderer origins are not supported.');
    }
    const value = normalizeOrigin(origin);
    if (!value) {
      throw new PatrisHostError('ORIGIN_POLICY_INVALID', 'Empty or opaque renderer origins are not supported.');
    }
    normalized.add(value);
  }
  return normalized;
}

function assertAllowedOrigin(sourceUrl, allowlist) {
  const origin = normalizeOrigin(sourceUrl);
  if (!origin || !allowlist.has(origin)) {
    throw new PatrisHostError('ORIGIN_NOT_ALLOWED', 'This renderer origin is not allowed to call Patris Export.');
  }
  return origin;
}

function assertAllowedMethod(method) {
  const value = String(method || '').trim();
  if (!ALLOWED_METHOD_SET.has(value)) {
    throw new PatrisHostError('METHOD_NOT_ALLOWED', `Patris method is not allowed: ${value || '(empty)'}`);
  }
  return value;
}

function publicError(error) {
  const value = error instanceof PatrisHostError
    ? error
    : new PatrisHostError('HOST_ERROR', error && error.message ? error.message : String(error || 'Unknown Patris host error.'));
  return Object.freeze({
    code: value.code,
    message: value.message,
    retryable: value.retryable === true
  });
}

function successEnvelope(result, meta = {}) {
  return Object.freeze({
    ok: true,
    result: result === undefined ? null : result,
    meta: Object.freeze({ ...meta })
  });
}

function errorEnvelope(error, meta = {}) {
  return Object.freeze({
    ok: false,
    error: publicError(error),
    meta: Object.freeze({ ...meta })
  });
}

function assertEnvelope(value) {
  if (!value || typeof value !== 'object' || typeof value.ok !== 'boolean') {
    throw new PatrisHostError('INVALID_RESPONSE', 'The privileged Patris host returned an invalid response envelope.');
  }
  if (value.ok && !Object.prototype.hasOwnProperty.call(value, 'result')) {
    throw new PatrisHostError('INVALID_RESPONSE', 'A successful Patris response is missing its result.');
  }
  if (!value.ok && (!value.error || typeof value.error.code !== 'string' || typeof value.error.message !== 'string')) {
    throw new PatrisHostError('INVALID_RESPONSE', 'A failed Patris response is missing its typed error.');
  }
  return value;
}

module.exports = {
  ALLOWED_METHODS,
  HTTP_METHODS,
  PatrisHostError,
  assertAllowedMethod,
  assertAllowedOrigin,
  assertEnvelope,
  compileOriginAllowlist,
  errorEnvelope,
  normalizeOrigin,
  publicError,
  successEnvelope
};
