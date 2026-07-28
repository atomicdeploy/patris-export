#!/usr/bin/env node
'use strict';

const path = require('node:path');
const {
  assertRouteParity,
  buildDocumentation,
  bundleDocumentation,
  checkDeterminism,
  lintDocumentation,
  readDocument,
} = require('./api-docs-lib.cjs');

const repoRoot = path.resolve(__dirname, '..', '..');
const docsRoot = path.join(repoRoot, 'docs', 'api');
const options = { docsRoot, repoRoot };

function usage() {
  process.stdout.write(`Patris Export API documentation tools

Usage:
  node scripts/docs/api-docs.cjs lint
  node scripts/docs/api-docs.cjs parity
  node scripts/docs/api-docs.cjs bundle
  node scripts/docs/api-docs.cjs build
  node scripts/docs/api-docs.cjs check-determinism

Commands:
  lint               Validate OpenAPI, AsyncAPI, visibility, and source-route parity.
  parity             Compare pkg/server/server.go routes with OpenAPI and AsyncAPI.
  bundle             Resolve and normalize machine-readable source contracts.
  build              Create offline public/internal sites, ZIPs, and SHA-256 manifests.
  check-determinism  Build twice in isolated directories and compare all output bytes.
`);
}

async function main() {
  const command = process.argv[2];
  switch (command) {
    case 'lint': {
      const result = await lintDocumentation(options);
      if (result.redoclyOutput) {
        process.stdout.write(`${result.redoclyOutput}\n`);
      }
      process.stdout.write(`Validated ${result.httpOperations} HTTP operations and ${result.websocketChannels} WebSocket channel(s).\n`);
      break;
    }
    case 'parity': {
      const result = assertRouteParity({
        sourceText: require('node:fs').readFileSync(path.join(repoRoot, 'pkg', 'server', 'server.go'), 'utf8'),
        openapi: readDocument(path.join(docsRoot, 'openapi.yaml')),
        asyncapi: readDocument(path.join(docsRoot, 'asyncapi.yaml')),
      });
      process.stdout.write(`Route parity passed: ${result.httpOperations} HTTP operations and ${result.websocketChannels} WebSocket channel(s).\n`);
      break;
    }
    case 'bundle': {
      const result = await bundleDocumentation(options);
      process.stdout.write(`Bundled OpenAPI: ${result.openapi}\nBundled AsyncAPI: ${result.asyncapi}\n`);
      break;
    }
    case 'build': {
      const result = await buildDocumentation(options);
      process.stdout.write(`Built ${result.manifest.packages.length} offline API documentation packages in ${result.dist}\n`);
      for (const artifact of result.manifest.packages) {
        process.stdout.write(`  ${artifact.sha256}  ${artifact.file}\n`);
      }
      break;
    }
    case 'check-determinism': {
      const digest = await checkDeterminism(options);
      process.stdout.write(`Deterministic API documentation build passed: ${digest}\n`);
      break;
    }
    case '--help':
    case '-h':
    case undefined:
      usage();
      break;
    default:
      usage();
      throw new Error(`Unknown command: ${command}`);
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error.message}\n`);
  process.exitCode = 1;
});
