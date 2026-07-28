'use strict';

const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const {
  assertCodeAnchors,
  assertCompleteness,
  assertRouteParity,
  assertSamplesSanitized,
  assertVersionParity,
  buildDocumentation,
  createDeterministicZip,
  generateSamples,
  materializeAsyncSharedSchemas,
  normalizeMuxPath,
  openAPIOperations,
  parseServerRoutes,
  publicAsyncAPI,
  publicOpenAPI,
  readDocument,
  renderProtocols,
  sha256File,
  stableYAML,
  validateAsyncAPI,
} = require('./api-docs-lib.cjs');

const repoRoot = path.resolve(__dirname, '..', '..');
const realDocsRoot = path.join(repoRoot, 'docs', 'api');

function jsonMedia(example) {
  return {
    'application/json': {
      schema: { type: 'object', additionalProperties: true, example },
      example,
    },
  };
}

function responses(example) {
  return {
    200: {
      description: 'Successful response.',
      headers: {
        'X-Source-Revision': {
          description: 'Stable source revision used for this response.',
          schema: { type: 'string' },
        },
      },
      content: jsonMedia(example),
    },
    400: {
      description: 'The request was invalid.',
      content: jsonMedia({ error: 'invalid_request' }),
    },
    500: {
      description: 'The server could not complete the request.',
      content: jsonMedia({ error: 'internal_error' }),
    },
  };
}

function operation({
  operationId,
  summary,
  description,
  visibility,
  security = [],
  example = { ok: true },
  schema,
  internal = !(visibility.startsWith('public') || visibility.startsWith('protected')),
}) {
  const result = {
    operationId,
    summary,
    description,
    'x-visibility': visibility,
    'x-internal': internal,
    security,
    responses: responses(example),
  };
  if (schema) {
    result.responses[200].content['application/json'] = {
      schema: { $ref: schema },
      example,
    };
  }
  return result;
}

function fixtureOpenAPI() {
  return {
    openapi: '3.1.0',
    info: {
      title: 'Patris Export Test API',
      version: '1.0.0',
      description: 'Fixture used to verify the offline API documentation builder.',
    },
    servers: [{ url: 'http://127.0.0.1:18080' }],
    tags: [
      { name: 'Records', description: 'Public data reads.' },
      { name: 'Configuration', description: 'Private configuration operations.' },
      { name: 'Assets', description: 'Internal static assets.' },
    ],
    paths: {
      '/api/records': {
        get: {
          ...operation({
            operationId: 'getRecords',
            summary: 'Read transformed records',
            description: 'Returns the currently transformed record collection.',
            visibility: 'public-api',
            schema: '#/components/schemas/PublicPayload',
          }),
          tags: ['Records'],
          'x-code-anchors': ['pkg/server/server.go#Server.handleGetRecords'],
        },
      },
      '/api/config': {
        get: {
          ...operation({
            operationId: 'getConfig',
            summary: 'Read runtime configuration',
            description: 'Returns the private runtime configuration projection.',
            visibility: 'private-configuration',
            security: [{ bearerAuth: [] }],
            schema: '#/components/schemas/PrivatePayload',
          }),
          tags: ['Configuration'],
          'x-code-anchors': ['pkg/server/server.go#Server.handleGetConfig'],
        },
      },
      '/api/recent-sales': {
        get: {
          ...operation({
            operationId: 'getRecentSales',
            summary: 'Read aggregate recent sales',
            description: 'Returns a privacy-safe aggregate recent-sales integration feed.',
            visibility: 'protected-integration-api',
            security: [{ bearerAuth: [] }],
            example: { products: [{ code: '1001', sold_quantity: 3 }] },
          }),
          tags: ['Records'],
          'x-code-anchors': ['pkg/server/server.go#Server.handleRecentSales'],
        },
      },
      '/static/icon.png': {
        head: {
          operationId: 'headIcon',
          summary: 'Inspect the icon',
          description: 'Returns icon metadata without a response body.',
          'x-visibility': 'internal-static',
          'x-internal': true,
          'x-errors-not-applicable': true,
          security: [],
          tags: ['Assets'],
          'x-code-anchors': ['pkg/server/server.go#Server.handleIcon'],
          responses: {
            200: {
              description: 'Icon metadata is available.',
              headers: {
                'Content-Length': {
                  description: 'Icon length in bytes.',
                  schema: { type: 'integer' },
                },
              },
            },
          },
        },
      },
    },
    components: {
      securitySchemes: {
        bearerAuth: {
          type: 'http',
          scheme: 'bearer',
          bearerFormat: 'opaque',
        },
      },
      schemas: {
        PublicPayload: {
          type: 'object',
          properties: {
            records: {
              type: 'array',
              items: { $ref: '#/components/schemas/PublicRecord' },
            },
          },
          example: { records: [{ code: '1001' }] },
        },
        PublicRecord: {
          type: 'object',
          properties: { code: { type: 'string', example: '1001' } },
        },
        PrivatePayload: {
          type: 'object',
          properties: { enabled: { type: 'boolean', example: true } },
          example: { enabled: true },
        },
      },
    },
  };
}

function fixtureAsyncAPI() {
  return {
    asyncapi: '3.0.0',
    info: {
      title: 'Patris Export Test Events',
      version: '1.0.0',
      description: 'Fixture event contract.',
    },
    servers: {
      local: {
        host: '127.0.0.1:18080',
        pathname: '/ws',
        protocol: 'ws',
        description: 'Local Patris Export WebSocket server.',
      },
    },
    channels: {
      updates: {
        address: '/ws',
        title: 'Live updates',
        description: 'Carries server updates and bounded client commands.',
        'x-visibility': 'public-realtime',
        'x-internal': false,
        'x-code-anchors': ['pkg/server/server.go#Server.handleWebSocket'],
        messages: {
          serverEvent: { $ref: '#/components/messages/ServerEvent' },
          clientCommand: { $ref: '#/components/messages/ClientCommand' },
        },
      },
    },
    operations: {
      receiveUpdates: {
        action: 'receive',
        channel: { $ref: '#/channels/updates' },
        summary: 'Receive live updates',
        description: 'Receives transformed record events from Patris Export.',
        'x-visibility': 'public-realtime',
        'x-internal': false,
        'x-code-anchors': ['pkg/server/server.go#Server.sendEvent'],
        messages: [{ $ref: '#/channels/updates/messages/serverEvent' }],
      },
      sendCommand: {
        action: 'send',
        channel: { $ref: '#/channels/updates' },
        summary: 'Send a client command',
        description: 'Sends a bounded client command to Patris Export.',
        'x-visibility': 'public-realtime',
        'x-internal': false,
        'x-code-anchors': ['pkg/server/server.go#Server.receiveCommand'],
        messages: [{ $ref: '#/channels/updates/messages/clientCommand' }],
      },
    },
    components: {
      messages: {
        ServerEvent: {
          name: 'serverEvent',
          title: 'Server event',
          summary: 'A live server event.',
          contentType: 'application/json',
          payload: { $ref: '#/components/schemas/ServerEventPayload' },
        },
        ClientCommand: {
          name: 'clientCommand',
          title: 'Client command',
          summary: 'A bounded client command.',
          contentType: 'application/json',
          payload: { $ref: '#/components/schemas/ClientCommandPayload' },
        },
      },
      schemas: {
        ServerEventPayload: {
          type: 'object',
          properties: { event: { type: 'string' } },
        },
        ClientCommandPayload: {
          type: 'object',
          properties: { command: { type: 'string' } },
        },
      },
    },
  };
}

function fixtureSource() {
  return `package server
func (s *Server) setupRoutes() {
  s.router.HandleFunc("/api/records", s.handleGetRecords).Methods("GET")
  s.router.HandleFunc("/api/config", s.handleGetConfig).Methods("GET")
  s.router.HandleFunc("/api/recent-sales", s.handleRecentSales).Methods("GET")
  s.router.HandleFunc("/static/icon.png", s.handleIcon).Methods("HEAD")
  s.router.HandleFunc("/ws", s.handleWebSocket)
}
func (s *Server) handleGetRecords() {}
func (s *Server) handleGetConfig() {}
func (s *Server) handleRecentSales() {}
func (s *Server) handleIcon() {}
func (s *Server) handleWebSocket() {}
func (s *Server) sendEvent() {}
func (s *Server) receiveCommand() {}
`;
}

test('normalizes Gorilla mux route regexes and expands GET/HEAD registrations', () => {
  assert.equal(
    normalizeMuxPath('/api/records.{format:json|csv|xlsx}'),
    '/api/records.{format}',
  );
  const routes = parseServerRoutes(`func routes() {
    s.router.HandleFunc("/api/records.{format:json|csv|xlsx}", s.records).Methods("GET")
    s.router.HandleFunc("/api/source/file", s.file).Methods("GET", "HEAD")
    s.router.HandleFunc("/ws", s.ws)
  }`);
  assert.deepEqual(
    routes.map((route) => route.kind === 'http' ? `${route.method} ${route.path}` : `WS ${route.path}`),
    [
      'GET /api/records.{format}',
      'GET /api/source/file',
      'HEAD /api/source/file',
      'WS /ws',
    ],
  );
});

test('route auditor fails closed on unsupported or non-literal router registrations', () => {
  assert.throws(
    () => parseServerRoutes(`
      func (s *Server) setupRoutes() {
        s.router.Handle("/hidden", http.HandlerFunc(s.hidden)).Methods("GET")
      }
    `),
    /Unsupported router registration method\(s\): Handle/,
  );
  assert.throws(
    () => parseServerRoutes(`
      func (s *Server) setupRoutes() {
        route := "/hidden"
        s.router.HandleFunc(route, s.hidden).Methods("GET")
      }
    `),
    /Parsed 0 of 1 s\.router\.HandleFunc registrations/,
  );
});

test('code anchors resolve stable Go symbols and include each registered handler', () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-anchors-test-'));
  try {
    const sourceFile = path.join(temporary, 'pkg', 'server', 'server.go');
    fs.mkdirSync(path.dirname(sourceFile), { recursive: true });
    fs.writeFileSync(sourceFile, fixtureSource());
    const openapi = fixtureOpenAPI();
    const asyncapi = fixtureAsyncAPI();
    assert.doesNotThrow(() => assertCodeAnchors({
      repoRoot: temporary,
      sourceText: fixtureSource(),
      openapi,
      asyncapi,
    }));

    openapi.paths['/api/records'].get['x-code-anchors'] = [
      'pkg/server/server.go:12-18',
    ];
    assert.throws(
      () => assertCodeAnchors({
        repoRoot: temporary,
        sourceText: fixtureSource(),
        openapi,
        asyncapi,
      }),
      /numeric line anchors are forbidden[\s\S]*registered handler Server\.handleGetRecords/,
    );

    openapi.paths['/api/records'].get['x-code-anchors'] = [
      'pkg/server/server.go#Server.handleGetConfig',
    ];
    assert.throws(
      () => assertCodeAnchors({
        repoRoot: temporary,
        sourceText: fixtureSource(),
        openapi,
        asyncapi,
      }),
      /must anchor its registered handler Server\.handleGetRecords/,
    );
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});

test('route parity rejects missing HEAD operations and stale documented routes', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  const result = assertRouteParity({
    sourceText: fixtureSource(),
    openapi,
    asyncapi,
  });
  assert.deepEqual(result, { httpOperations: 4, websocketChannels: 1 });

  delete openapi.paths['/static/icon.png'].head;
  openapi.paths['/stale'] = {
    get: operation({
      operationId: 'getStale',
      summary: 'Stale route',
      description: 'This route is deliberately not registered.',
      visibility: 'internal',
    }),
  };
  assert.throws(
    () => assertRouteParity({ sourceText: fixtureSource(), openapi, asyncapi }),
    /Missing OpenAPI operations:[\s\S]*HEAD \/static\/icon\.png[\s\S]*GET \/stale/,
  );
});

test('contract versions stay aligned with the product version', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  const version = openapi.info.version;
  asyncapi.info.version = version;
  assert.equal(
    assertVersionParity({
      sourceText: `package version\n\nvar Version = ${JSON.stringify(version)}\n`,
      openapi,
      asyncapi,
    }),
    version,
  );
  asyncapi.info.version = '0.0.0-stale';
  assert.throws(
    () => assertVersionParity({
      sourceText: `package version\n\nvar Version = ${JSON.stringify(version)}\n`,
      openapi,
      asyncapi,
    }),
    /AsyncAPI: 0\.0\.0-stale/,
  );
});

test('completeness gate requires explicit security and media examples', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  assert.doesNotThrow(() => assertCompleteness(openapi, asyncapi));

  delete openapi.paths['/api/records'].get.security;
  delete openapi.paths['/api/config'].get.responses[200].content['application/json'].example;
  delete openapi.components.schemas.PrivatePayload.example;
  delete openapi.components.schemas.PrivatePayload.properties.enabled.example;
  assert.throws(
    () => assertCompleteness(openapi, asyncapi),
    /must explicitly define security[\s\S]*must define a sanitized example/,
  );
});

test('completeness resolves chained response refs and merges description siblings', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  openapi.components.responses = {
    ErrorBase: {
      description: 'Base integration error.',
      content: jsonMedia({ error: 'base_error' }),
    },
    InvalidRequest: {
      $ref: '#/components/responses/ErrorBase',
      description: 'The integration request was invalid.',
    },
    ExcelInvalidRequest: {
      $ref: '#/components/responses/InvalidRequest',
      description: 'The workbook request was invalid.',
    },
  };
  openapi.paths['/api/recent-sales'].get.responses[400] = {
    $ref: '#/components/responses/ExcelInvalidRequest',
  };
  assert.doesNotThrow(() => assertCompleteness(openapi, asyncapi));

  delete openapi.components.responses.ErrorBase.content;
  assert.throws(
    () => assertCompleteness(openapi, asyncapi),
    /GET \/api\/recent-sales response 400 must document at least one response\/request media type/,
  );
});

test('completeness accepts only explicitly bodyless runtime responses without media', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  openapi.components.responses = {
    MethodNotAllowed: {
      description: 'The router writes only the 405 status.',
      'x-bodyless': true,
    },
  };
  openapi.paths['/api/records'].get.responses[405] = {
    $ref: '#/components/responses/MethodNotAllowed',
  };
  assert.doesNotThrow(() => assertCompleteness(openapi, asyncapi));

  delete openapi.components.responses.MethodNotAllowed['x-bodyless'];
  assert.throws(
    () => assertCompleteness(openapi, asyncapi),
    /GET \/api\/records response 405 must document at least one response\/request media type/,
  );

  openapi.components.responses.MethodNotAllowed['x-bodyless'] = true;
  openapi.components.responses.MethodNotAllowed.content = jsonMedia({ error: 'unexpected-body' });
  assert.throws(
    () => assertCompleteness(openapi, asyncapi),
    /response 405 is marked x-bodyless but also defines content/,
  );
});

test('synchronized wrapper examples cannot drift from their canonical schema example', () => {
  const openapi = fixtureOpenAPI();
  const asyncapi = fixtureAsyncAPI();
  const schema = openapi.components.schemas.PrivatePayload;
  const media = openapi.paths['/api/config'].get.responses[200].content['application/json'];
  media.example = { config: structuredClone(schema.example) };
  media['x-example-config-schema'] = '#/components/schemas/PrivatePayload';
  assert.doesNotThrow(() => assertCompleteness(openapi, asyncapi));
  media.example.config.enabled = false;
  assert.throws(
    () => assertCompleteness(openapi, asyncapi),
    /example\.config must exactly match #\/components\/schemas\/PrivatePayload\.example/,
  );
});

test('public contracts remove private operations and unreachable components', () => {
  const openapi = publicOpenAPI(fixtureOpenAPI());
  assert.ok(openapi.paths['/api/records']);
  assert.ok(openapi.paths['/api/recent-sales']);
  assert.equal(openapi.paths['/api/config'], undefined);
  assert.equal(openapi.paths['/static/icon.png'], undefined);
  assert.ok(openapi.components.schemas.PublicPayload);
  assert.ok(openapi.components.schemas.PublicRecord);
  assert.equal(openapi.components.schemas.PrivatePayload, undefined);
  assert.ok(openapi.components.securitySchemes.bearerAuth);

  const asyncapi = fixtureAsyncAPI();
  asyncapi.channels.private = {
    address: '/private',
    description: 'Private fixture channel.',
    'x-visibility': 'private',
    messages: {},
  };
  const filteredAsyncAPI = publicAsyncAPI(asyncapi);
  assert.ok(filteredAsyncAPI.channels.updates);
  assert.equal(filteredAsyncAPI.channels.private, undefined);
  assert.ok(filteredAsyncAPI.components.messages.ServerEvent);
});

test('AsyncAPI shared schemas materialize the exact OpenAPI schema graph', () => {
  const openapi = {
    components: {
      schemas: {
        BrowserConfig: {
          type: 'object',
          additionalProperties: false,
          properties: {
            ui: { $ref: '#/components/schemas/BrowserUIConfig' },
          },
        },
        BrowserUIConfig: {
          type: 'object',
          additionalProperties: false,
          properties: { theme: { type: 'string' } },
        },
        ProcessInfo: {
          type: 'object',
          additionalProperties: false,
          properties: { pid: { type: 'integer', format: 'int64' } },
        },
      },
    },
  };
  const asyncapi = {
    components: {
      schemas: {
        BrowserConfig: {
          $ref: './openapi.yaml#/components/schemas/BrowserConfig',
        },
        ProcessInfo: {
          $ref: './openapi.yaml#/components/schemas/ProcessInfo',
        },
      },
    },
  };
  const materialized = materializeAsyncSharedSchemas(openapi, asyncapi);
  assert.deepEqual(
    materialized.components.schemas.BrowserConfig,
    openapi.components.schemas.BrowserConfig,
  );
  assert.deepEqual(
    materialized.components.schemas.BrowserUIConfig,
    openapi.components.schemas.BrowserUIConfig,
  );
  assert.deepEqual(
    materialized.components.schemas.ProcessInfo,
    openapi.components.schemas.ProcessInfo,
  );
  assert.throws(
    () => materializeAsyncSharedSchemas(openapi, {
      components: { schemas: { BrowserConfig: { type: 'object' } } },
    }),
    /shared schema BrowserConfig must reference the canonical OpenAPI schema/,
  );
});

test('real AsyncAPI shared markers materialize exact standalone schemas', () => {
  const openapi = readDocument(path.join(realDocsRoot, 'openapi.yaml'));
  const asyncapi = readDocument(path.join(realDocsRoot, 'asyncapi.yaml'));
  const expectedShared = [
    'BrowserConfig',
    'CanonicalEnvelope',
    'DynamicRecord',
    'FileAccessStatus',
    'ProcessGroup',
    'ProcessInfo',
    'ResourceInfo',
    'RuntimeStatus',
    'VersionInfo',
  ];
  const shared = Object.keys(asyncapi.components.schemas)
    .filter((name) => openapi.components.schemas[name])
    .sort();
  assert.deepEqual(shared, expectedShared);

  const materialized = materializeAsyncSharedSchemas(openapi, asyncapi);
  for (const name of expectedShared) {
    assert.deepEqual(
      materialized.components.schemas[name],
      openapi.components.schemas[name],
      `${name} must be identical across HTTP and WebSocket contracts`,
    );
  }
  assert.doesNotMatch(JSON.stringify(materialized), /\.\/openapi\.yaml#/);

  const browser = openapi.components.schemas.BrowserConfig;
  assert.equal(browser.additionalProperties, false);
  assert.equal(browser.properties.extra, undefined);
  for (const field of ['mysql_dsn', 'mysql_tls_ca_file', 'mysql_tls_server_name']) {
    assert.equal(browser.properties.export.$ref.endsWith('/BrowserExportConfig'), true);
    assert.equal(openapi.components.schemas.BrowserExportConfig.properties[field], undefined);
  }
  assert.equal(openapi.components.schemas.BrowserSendUpdatesConfig.properties.headers, undefined);
  assert.equal(openapi.components.schemas.BrowserSendUpdatesConfig.properties.command, undefined);
  assert.equal(openapi.components.schemas.BrowserEdgeConfig.properties.token, undefined);
});

test('real plain-text http.Error responses declare nosniff and 405 stays bodyless', () => {
  const openapi = readDocument(path.join(realDocsRoot, 'openapi.yaml'));
  for (const name of [
    'CanonicalCategoriesTimedOut',
    'CanonicalNotAvailable',
    'PlainBadRequest',
    'PlainForbidden',
    'PlainInternalError',
    'PlainPayloadTooLarge',
    'PlainServiceUnavailable',
    'ProductSyncNotAvailable',
    'ProductSyncTimedOut',
    'UnsupportedRecordsFormat',
  ]) {
    assert.equal(
      openapi.components.responses[name].headers?.['X-Content-Type-Options']?.$ref,
      '#/components/headers/NoSniff',
      `${name} must document net/http's nosniff header`,
    );
  }
  const methodNotAllowed = openapi.components.responses.MethodNotAllowed;
  assert.equal(methodNotAllowed['x-bodyless'], true);
  assert.equal(methodNotAllowed.content, undefined);
  assert.equal(methodNotAllowed.headers, undefined);
});

test('AsyncAPI parser accepts the fixture and samples contain no literal credentials', async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-docs-test-'));
  try {
    const asyncFile = path.join(temporary, 'asyncapi.yaml');
    fs.writeFileSync(asyncFile, stableYAML(fixtureAsyncAPI()));
    const parsed = await validateAsyncAPI(asyncFile);
    assert.equal(parsed.asyncapi, '3.0.0');

    const samples = path.join(temporary, 'examples');
    generateSamples(fixtureOpenAPI(), samples, 'internal');
    assert.doesNotThrow(() => assertSamplesSanitized(samples));
    for (const expected of [
      'curl.sh',
      'httpie.sh',
      'javascript-fetch.mjs',
      'python-requests.py',
      'go-net-http.go',
      'php-curl.php',
      'csharp-httpclient.cs',
      'powershell.ps1',
    ]) {
      assert.ok(fs.existsSync(path.join(samples, expected)), `${expected} should be generated`);
    }
    const goDiff = execFileSync('gofmt', ['-d', path.join(samples, 'go-net-http.go')], { encoding: 'utf8' });
    assert.equal(goDiff, '', 'generated Go client must already be gofmt-clean');
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});

test('human WebSocket reference renders every message, fields, examples, and runnable clients', async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-websocket-test-'));
  try {
    const openapi = readDocument(path.join(realDocsRoot, 'openapi.yaml'));
    const asyncapi = materializeAsyncSharedSchemas(
      openapi,
      await validateAsyncAPI(path.join(realDocsRoot, 'asyncapi.yaml')),
    );
    const html = renderProtocols(asyncapi, 'internal');
    for (const messageReference of Object.values(asyncapi.channels.liveUpdates.messages)) {
      const name = messageReference.$ref.split('/').at(-1);
      const message = asyncapi.components.messages[name];
      const escapedTitle = message.title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      assert.match(html, new RegExp(escapedTitle));
    }
    for (const expected of ['Payload fields', 'Sanitized example', 'Origin', 'Sec-WebSocket-Protocol', 'connection lifecycle']) {
      assert.match(html, new RegExp(expected, 'i'));
    }

    generateSamples(openapi, temporary, 'internal', { asyncapi });
    for (const file of ['websocket-browser.html', 'websocket-node.mjs']) {
      assert.ok(fs.existsSync(path.join(temporary, file)), `${file} should be generated`);
    }
    execFileSync(process.execPath, ['--check', path.join(temporary, 'websocket-node.mjs')]);
    assert.match(
      fs.readFileSync(path.join(temporary, 'websocket-browser.html'), 'utf8'),
      /Send refresh[\s\S]*Send sample toast[\s\S]*new WebSocket/,
    );
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});

test('HTTPie samples use portable raw-body syntax', () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-httpie-test-'));
  try {
    const openapi = fixtureOpenAPI();
    openapi.paths['/api/config'].put = {
      ...operation({
        operationId: 'putConfig',
        summary: 'Update runtime configuration',
        description: 'Updates the browser-managed runtime configuration fields.',
        visibility: 'private-configuration',
        security: [{ bearerAuth: [] }],
        schema: '#/components/schemas/PrivatePayload',
      }),
      tags: ['Configuration'],
      requestBody: {
        required: true,
        description: 'Browser-managed configuration fields.',
        content: {
          'application/json': {
            schema: { $ref: '#/components/schemas/PrivatePayload' },
            example: { enabled: true },
          },
        },
      },
    };
    const entry = openAPIOperations(openapi)
      .find((operation) => operation.method === 'PUT' && operation.path === '/api/config');
    assert.ok(entry, 'PUT /api/config fixture operation should exist');
    generateSamples(openapi, temporary, 'internal', { entry });
    const httpie = fs.readFileSync(path.join(temporary, 'httpie.sh'), 'utf8');
    assert.match(httpie, /\s--raw\s/);
    assert.doesNotMatch(httpie, /<<</);
    execFileSync('sh', ['-n', path.join(temporary, 'httpie.sh')]);

    const authenticated = openAPIOperations(openapi)
      .find((operation) => operation.method === 'GET' && operation.path === '/api/recent-sales');
    assert.ok(authenticated, 'GET /api/recent-sales fixture operation should exist');
    const authenticatedDirectory = path.join(temporary, 'authenticated');
    generateSamples(openapi, authenticatedDirectory, 'public', { entry: authenticated });
    const authenticatedHTTPie = fs.readFileSync(
      path.join(authenticatedDirectory, 'httpie.sh'),
      'utf8',
    );
    assert.match(
      authenticatedHTTPie,
      /"Authorization:Bearer \$PATRIS_EXPORT_API_TOKEN"/,
    );
    assert.doesNotMatch(
      authenticatedHTTPie,
      /'Authorization:[^']*\$PATRIS_EXPORT_API_TOKEN/,
    );
    execFileSync('sh', ['-n', path.join(authenticatedDirectory, 'httpie.sh')]);
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});

test('ZIP output is byte-for-byte deterministic across source mtimes', async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-zip-test-'));
  try {
    const source = path.join(temporary, 'source');
    fs.mkdirSync(path.join(source, 'nested'), { recursive: true });
    fs.writeFileSync(path.join(source, 'a.txt'), 'alpha\n');
    fs.writeFileSync(path.join(source, 'nested', 'b.txt'), 'beta\n');
    const first = path.join(temporary, 'first.zip');
    const second = path.join(temporary, 'second.zip');
    await createDeterministicZip(source, first, 'docs');
    fs.utimesSync(path.join(source, 'a.txt'), new Date(), new Date());
    await createDeterministicZip(source, second, 'docs');
    assert.equal(sha256File(first), sha256File(second));
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});

test('full builder emits Pages-ready sites and strict public/internal artifacts', async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-api-build-test-'));
  try {
    const openapiFile = path.join(temporary, 'openapi.yaml');
    const asyncapiFile = path.join(temporary, 'asyncapi.yaml');
    const serverFile = path.join(temporary, 'pkg', 'server', 'server.go');
    fs.mkdirSync(path.dirname(serverFile), { recursive: true });
    fs.writeFileSync(openapiFile, stableYAML(fixtureOpenAPI()));
    fs.writeFileSync(asyncapiFile, stableYAML(fixtureAsyncAPI()));
    fs.writeFileSync(serverFile, fixtureSource());
    const versionFile = path.join(temporary, 'pkg', 'version', 'version.go');
    fs.mkdirSync(path.dirname(versionFile), { recursive: true });
    fs.writeFileSync(versionFile, 'package version\n\nvar Version = "1.0.0"\n');
    fs.writeFileSync(path.join(temporary, 'LICENSE'), 'Test license.\n');
    fs.writeFileSync(path.join(temporary, 'NOTICE'), 'Test notice.\n');
    const buildRoot = path.join(temporary, 'build');
    const distRoot = path.join(temporary, 'dist');
    const result = await buildDocumentation({
      docsRoot: realDocsRoot,
      repoRoot: temporary,
      openapi: openapiFile,
      asyncapi: asyncapiFile,
      server: serverFile,
      buildRoot,
      distRoot,
      skipRedocly: true,
      skipVersionParity: true,
    });
    assert.equal(result.manifest.packages.length, 2);
    for (const expected of [
      path.join(buildRoot, 'public', 'index.html'),
      path.join(buildRoot, 'public', 'reference.html'),
      path.join(buildRoot, 'public', 'examples', 'index.html'),
      path.join(buildRoot, 'public', 'examples', 'websocket-browser.html'),
      path.join(buildRoot, 'public', 'examples', 'websocket-node.mjs'),
      path.join(buildRoot, 'public', 'LICENSE'),
      path.join(buildRoot, 'public', 'NOTICE'),
      path.join(buildRoot, 'public', 'THIRD_PARTY_NOTICES.md'),
      path.join(buildRoot, 'internal', 'index.html'),
      path.join(buildRoot, 'internal', 'examples', 'index.html'),
      path.join(buildRoot, 'internal', 'examples', 'authenticated-integration', 'python-requests.py'),
      path.join(buildRoot, 'internal', 'examples', 'authenticated-integration', 'index.html'),
      path.join(distRoot, 'patris-export-api-docs-public.zip'),
      path.join(distRoot, 'patris-export-api-docs-internal.zip'),
      path.join(distRoot, 'SHA256SUMS'),
      path.join(distRoot, 'manifest.json'),
    ]) {
      assert.ok(fs.existsSync(expected), `${expected} should exist`);
    }
    const publicSpec = JSON.parse(fs.readFileSync(path.join(buildRoot, 'public', 'openapi.json'), 'utf8'));
    assert.ok(publicSpec.paths['/api/records']);
    assert.equal(publicSpec.paths['/api/config'], undefined);
    assert.match(
      fs.readFileSync(path.join(buildRoot, 'public', 'index.html'), 'utf8'),
      /href="\.\/examples\/index\.html"/,
    );
    assert.match(
      fs.readFileSync(path.join(buildRoot, 'public', 'examples', 'index.html'), 'utf8'),
      /eight HTTP clients[\s\S]*javascript-fetch\.mjs[\s\S]*PowerShell[\s\S]*WebSocket clients/,
    );
    const referenceHTML = fs.readFileSync(path.join(buildRoot, 'public', 'reference.html'), 'utf8');
    assert.match(referenceHTML, /patris-openapi/);
    const scalarJS = fs.readFileSync(path.join(buildRoot, 'public', 'assets', 'scalar.js'), 'utf8');
    assert.match(scalarJS, /hideTestRequestButton:\s*true/);
    assert.doesNotMatch(scalarJS, /hideTestRequestButton:\s*false/);
    assert.match(scalarJS, /showDeveloperTools:\s*'never'/);
    assert.equal(result.manifest.schema_version, 2);
    assert.equal(result.manifest.source_commit, null);
    assert.match(scalarJS, /telemetry/);
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
});
