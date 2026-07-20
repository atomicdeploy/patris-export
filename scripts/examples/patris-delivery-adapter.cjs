#!/usr/bin/env node

'use strict';

const http = require('node:http');
const { timingSafeEqual } = require('node:crypto');
const { URL, URLSearchParams } = require('node:url');

const CONTRACT = 'patris.product-sync';
const SECRET_HEADER = 'X-Patris-Product-Sync-Secret';
const DEFAULT_LISTEN = '127.0.0.1:18081';
const DEFAULT_MAX_BODY_BYTES = 16 * 1024 * 1024;

class AdapterError extends Error {
  constructor(message, options = {}) {
    super(message);
    this.name = 'AdapterError';
    this.retryable = options.retryable === true;
    this.httpStatus = Number.isInteger(options.httpStatus) ? options.httpStatus : 0;
  }
}

function parseArgs(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    if (!token.startsWith('--')) {
      throw new AdapterError(`unexpected argument: ${token}`);
    }
    const equals = token.indexOf('=');
    const rawName = token.slice(2, equals === -1 ? undefined : equals);
    const inlineValue = equals === -1 ? undefined : token.slice(equals + 1);
    const name = rawName.replaceAll('-', '_');
    if (inlineValue !== undefined) {
      options[name] = inlineValue;
      continue;
    }
    const next = argv[index + 1];
    if (next === undefined || next.startsWith('--')) {
      options[name] = true;
      continue;
    }
    options[name] = next;
    index += 1;
  }
  return options;
}

function option(options, name, environmentName, fallback = '') {
  const value = options[name] ?? process.env[environmentName] ?? fallback;
  return typeof value === 'string' ? value.trim() : value;
}

function requireEnvironmentSecret(name) {
  if (!name) return '';
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) {
    throw new AdapterError('secret environment-variable name is invalid');
  }
  const secret = process.env[name];
  if (!secret) {
    throw new AdapterError(`required secret environment variable ${name} is empty or missing`);
  }
  return secret;
}

function equalSecret(left, right) {
  if (typeof left !== 'string' || typeof right !== 'string') return false;
  const leftBytes = Buffer.from(left);
  const rightBytes = Buffer.from(right);
  return leftBytes.length === rightBytes.length && timingSafeEqual(leftBytes, rightBytes);
}

function parseListen(value) {
  const match = /^([^:]+):(\d{1,5})$/.exec(value);
  if (!match) throw new AdapterError('listen address must use HOST:PORT');
  const port = Number(match[2]);
  if (port < 1 || port > 65535) throw new AdapterError('listen port is out of range');
  if (!['127.0.0.1', 'localhost'].includes(match[1].toLowerCase())) {
    throw new AdapterError('the adapter is loopback-only; listen on 127.0.0.1 or localhost');
  }
  return { host: match[1], port };
}

function validateTarget(target, hasSecret) {
  let parsed;
  try {
    parsed = new URL(target);
  } catch {
    throw new AdapterError('target must be an absolute HTTP or HTTPS URL');
  }
  if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname) {
    throw new AdapterError('target must be an absolute HTTP or HTTPS URL');
  }
  if (parsed.username || parsed.password) {
    throw new AdapterError('target credentials must be supplied through a secret environment variable, not the URL');
  }
  if (hasSecret && parsed.protocol !== 'https:' && !['127.0.0.1', 'localhost', '::1'].includes(parsed.hostname.toLowerCase())) {
    throw new AdapterError('a secret-bearing remote target requires HTTPS');
  }
  return parsed.toString();
}

function validateEnvelope(value, headers = {}) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new AdapterError('request body must be a JSON object');
  }
  if (value.schema !== CONTRACT) {
    throw new AdapterError(`request schema must be ${CONTRACT}`);
  }
  if (typeof value.event_id !== 'string' || value.event_id.trim() === '') {
    throw new AdapterError('request is missing event_id');
  }
  if (typeof value.schema_version !== 'string' || value.schema_version.trim() === '') {
    throw new AdapterError('request is missing schema_version');
  }
  if (!Array.isArray(value.products)) {
    throw new AdapterError('request products must be an array');
  }

  const normalizedHeaders = new Map(
    Object.entries(headers).map(([key, item]) => [key.toLowerCase(), String(item)]),
  );
  const headerContract = normalizedHeaders.get('x-patris-contract');
  const headerVersion = normalizedHeaders.get('x-patris-contract-version');
  const headerEventID = normalizedHeaders.get('x-patris-event-id');
  if (headerContract && headerContract !== value.schema) {
    throw new AdapterError('X-Patris-Contract does not match the body');
  }
  if (headerVersion && headerVersion !== String(value.schema_version ?? '')) {
    throw new AdapterError('X-Patris-Contract-Version does not match the body');
  }
  if (headerEventID && headerEventID !== value.event_id) {
    throw new AdapterError('X-Patris-Event-ID does not match the body');
  }
  return value;
}

function identityHeaders(envelope, secret, transport) {
  const headers = {
    'Content-Type': 'application/json; charset=utf-8',
    'Idempotency-Key': envelope.event_id,
    'X-Patris-Contract': envelope.schema,
    'X-Patris-Contract-Version': String(envelope.schema_version ?? ''),
    'X-Patris-Event-ID': envelope.event_id,
    'X-Patris-Adapter': transport,
  };
  if (secret) headers.Authorization = `Bearer ${secret}`;
  return headers;
}

function buildOutboundRequest(config, envelope) {
  const target = validateTarget(config.target, config.targetSecret !== '');
  const headers = identityHeaders(envelope, config.targetSecret, config.transport);

  switch (config.transport) {
    case 'json-rpc':
      return {
        url: target,
        method: 'POST',
        headers,
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: envelope.event_id,
          method: config.method || 'patris.productSync',
          params: envelope,
        }),
      };
    case 'wordpress-ajax': {
      const form = new URLSearchParams({
        action: config.action || 'patris_product_sync',
        event_id: envelope.event_id,
        payload: JSON.stringify(envelope),
      });
      headers['Content-Type'] = 'application/x-www-form-urlencoded; charset=utf-8';
      if (config.targetSecret) {
        delete headers.Authorization;
        headers[SECRET_HEADER] = config.targetSecret;
      }
      return { url: target, method: 'POST', headers, body: form.toString() };
    }
    case 'grpc-gateway':
      return {
        url: target,
        method: 'POST',
        headers,
        body: JSON.stringify(envelope),
      };
    default:
      throw new AdapterError(`unsupported outbound transport: ${config.transport}`);
  }
}

function retryableStatus(status) {
  return status === 408 || status === 425 || status === 429 || (status >= 500 && status <= 599);
}

async function readResponseJSON(response) {
  const text = await response.text();
  if (!text.trim()) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new AdapterError('downstream returned invalid JSON', {
      retryable: retryableStatus(response.status),
      httpStatus: response.status,
    });
  }
}

async function forwardEnvelope(config, envelope, fetchImpl = globalThis.fetch) {
  if (typeof fetchImpl !== 'function') {
    throw new AdapterError('this adapter requires Node.js 18 or newer');
  }
  const outbound = buildOutboundRequest(config, envelope);
  let response;
  try {
    response = await fetchImpl(outbound.url, {
      method: outbound.method,
      headers: outbound.headers,
      body: outbound.body,
      signal: AbortSignal.timeout(config.timeoutMs),
      redirect: 'manual',
    });
  } catch {
    throw new AdapterError('downstream request failed', { retryable: true });
  }

  const responseBody = await readResponseJSON(response);
  if (!response.ok) {
    throw new AdapterError(`downstream returned HTTP ${response.status}`, {
      retryable: retryableStatus(response.status),
      httpStatus: response.status,
    });
  }
  if (config.transport === 'json-rpc') {
    if (!responseBody || responseBody.jsonrpc !== '2.0' || String(responseBody.id) !== envelope.event_id) {
      throw new AdapterError('JSON-RPC response identity is invalid');
    }
    if (responseBody.error) {
      throw new AdapterError('JSON-RPC method rejected the update', {
        retryable: responseBody.error?.data?.retryable === true,
      });
    }
  }
  if (config.transport === 'wordpress-ajax' && responseBody?.success !== true) {
    throw new AdapterError('WordPress AJAX action rejected the update', {
      retryable: responseBody?.data?.retryable === true,
    });
  }
  return responseBody;
}

function readBody(request, maxBytes) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > maxBytes) {
        reject(new AdapterError('request body is too large'));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on('end', () => resolve(Buffer.concat(chunks)));
    request.on('error', () => reject(new AdapterError('failed to read request body', { retryable: true })));
  });
}

function sendJSON(response, status, value) {
  const body = `${JSON.stringify(value)}\n`;
  response.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  });
  response.end(body);
}

function acceptedResponse(eventID) {
  return {
    success: true,
    data: {
      status: 'accepted',
      event_id: eventID,
      retryable: false,
      pending_products: 0,
      deferred_products: 0,
    },
  };
}

function rejectedResponse(error) {
  return {
    success: false,
    code: error.retryable ? 'patris_adapter_downstream_unavailable' : 'patris_adapter_rejected',
    details: { retryable: error.retryable === true },
  };
}

function createAdapterServer(config, dependencies = {}) {
  const fetchImpl = dependencies.fetchImpl ?? globalThis.fetch;
  let received = 0;
  return http.createServer(async (request, response) => {
    if (request.method !== 'POST' || request.url !== '/ingest') {
      sendJSON(response, 404, { success: false, code: 'not_found' });
      return;
    }

    let envelope;
    try {
      if (config.ingressSecret) {
        const supplied = request.headers[SECRET_HEADER.toLowerCase()];
        if (!equalSecret(supplied, config.ingressSecret)) {
          throw new AdapterError('ingress authentication failed');
        }
      }
      const body = await readBody(request, config.maxBodyBytes);
      envelope = validateEnvelope(JSON.parse(body.toString('utf8')), request.headers);
      received += 1;

      if (config.transport === 'mock') {
        if (received <= config.failFirst) {
          throw new AdapterError('mock receiver requested a retry', { retryable: true, httpStatus: 503 });
        }
      } else {
        await forwardEnvelope(config, envelope, fetchImpl);
      }

      console.log(`[adapter] accepted event_id=${envelope.event_id} products=${envelope.products.length}`);
      sendJSON(response, 200, acceptedResponse(envelope.event_id));
    } catch (caught) {
      const error = caught instanceof AdapterError
        ? caught
        : new AdapterError(caught instanceof SyntaxError ? 'request body is invalid JSON' : 'adapter failed');
      const status = error.retryable ? 503 : 422;
      console.error(`[adapter] ${error.retryable ? 'retryable' : 'rejected'} event_id=${envelope?.event_id ?? 'unknown'} reason=${error.message}`);
      sendJSON(response, status, rejectedResponse(error));
    }
  });
}

function usage() {
  return `Patris Export delivery adapter

Usage:
  node scripts/examples/patris-delivery-adapter.cjs \\
    --transport mock|json-rpc|wordpress-ajax|grpc-gateway \\
    [--listen ${DEFAULT_LISTEN}] [--target https://service.example/path]

Options can also use PATRIS_ADAPTER_TRANSPORT, PATRIS_ADAPTER_LISTEN,
PATRIS_ADAPTER_TARGET, PATRIS_ADAPTER_METHOD, PATRIS_ADAPTER_ACTION,
PATRIS_ADAPTER_TARGET_SECRET_ENV, and PATRIS_ADAPTER_INGRESS_SECRET_ENV.
The *_SECRET_ENV values name environment variables; they never contain secrets.
`;
}

function configFromOptions(options) {
  const transport = option(options, 'transport', 'PATRIS_ADAPTER_TRANSPORT', 'mock');
  const supported = new Set(['mock', 'json-rpc', 'wordpress-ajax', 'grpc-gateway']);
  if (!supported.has(transport)) throw new AdapterError(`unsupported transport: ${transport}`);

  const timeoutSeconds = Number(option(options, 'timeout_seconds', 'PATRIS_ADAPTER_TIMEOUT_SECONDS', '10'));
  if (!Number.isFinite(timeoutSeconds) || timeoutSeconds <= 0 || timeoutSeconds > 120) {
    throw new AdapterError('timeout-seconds must be greater than zero and at most 120');
  }
  const failFirst = Number(option(options, 'fail_first', 'PATRIS_ADAPTER_FAIL_FIRST', '0'));
  if (!Number.isInteger(failFirst) || failFirst < 0) {
    throw new AdapterError('fail-first must be a non-negative integer');
  }

  const target = option(options, 'target', 'PATRIS_ADAPTER_TARGET');
  if (transport !== 'mock' && !target) throw new AdapterError(`${transport} requires --target`);
  const ingressSecretEnv = option(options, 'ingress_secret_env', 'PATRIS_ADAPTER_INGRESS_SECRET_ENV');
  const targetSecretEnv = option(options, 'target_secret_env', 'PATRIS_ADAPTER_TARGET_SECRET_ENV');
  if (transport !== 'mock' && !ingressSecretEnv) {
    throw new AdapterError(`${transport} requires --ingress-secret-env`);
  }

  return {
    transport,
    listen: parseListen(option(options, 'listen', 'PATRIS_ADAPTER_LISTEN', DEFAULT_LISTEN)),
    target,
    method: option(options, 'method', 'PATRIS_ADAPTER_METHOD', 'patris.productSync'),
    action: option(options, 'action', 'PATRIS_ADAPTER_ACTION', 'patris_product_sync'),
    timeoutMs: Math.round(timeoutSeconds * 1000),
    failFirst,
    maxBodyBytes: DEFAULT_MAX_BODY_BYTES,
    ingressSecret: requireEnvironmentSecret(ingressSecretEnv),
    targetSecret: requireEnvironmentSecret(targetSecretEnv),
  };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help || options.h) {
    process.stdout.write(usage());
    return;
  }
  const config = configFromOptions(options);
  const server = createAdapterServer(config);
  server.on('error', (error) => {
    console.error(`[adapter] listener failed: ${error.message}`);
    process.exitCode = 1;
  });
  server.listen(config.listen.port, config.listen.host, () => {
    console.log(`[adapter] listening=http://${config.listen.host}:${config.listen.port}/ingest transport=${config.transport}`);
  });
}

if (require.main === module) {
  main().catch((error) => {
    console.error(`[adapter] startup failed: ${error.message}`);
    process.exitCode = 1;
  });
}

module.exports = {
  AdapterError,
  CONTRACT,
  SECRET_HEADER,
  acceptedResponse,
  buildOutboundRequest,
  configFromOptions,
  createAdapterServer,
  forwardEnvelope,
  parseArgs,
  retryableStatus,
  validateEnvelope,
};
