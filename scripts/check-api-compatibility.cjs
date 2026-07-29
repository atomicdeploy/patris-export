#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const REPOSITORY_ROOT = path.resolve(__dirname, '..');
const REPORT_PATH = 'docs/api-compatibility-audit.json';
const GENERAL_COLLECTIONS = new Set(['records', 'products', 'categories']);
const TEXT_EXTENSIONS = new Set([
  '.bas',
  '.cjs',
  '.conf',
  '.go',
  '.ini',
  '.js',
  '.json',
  '.md',
  '.mjs',
  '.proto',
  '.ps1',
  '.sh',
  '.toml',
  '.ts',
  '.tsx',
  '.txt',
  '.yaml',
  '.yml',
]);
const EXCLUDED_PATHS = new Set([
  REPORT_PATH,
  'docs/API-COMPATIBILITY-AUDIT.md',
  'scripts/check-api-compatibility.cjs',
  'scripts/check-api-compatibility.test.cjs',
]);

function normalizePath(filePath) {
  return filePath.replaceAll('\\', '/');
}

function trackedTextFiles(root = REPOSITORY_ROOT) {
  const result = spawnSync(
    'git',
    ['ls-files', '--cached', '--others', '--exclude-standard', '-z'],
    { cwd: root, encoding: 'utf8' },
  );
  if (result.status !== 0) {
    throw new Error(`git ls-files failed: ${String(result.stderr).trim()}`);
  }

  return [...new Set(result.stdout.split('\0').filter(Boolean).map(normalizePath))]
    .filter((filePath) => !EXCLUDED_PATHS.has(filePath))
    .filter((filePath) => !filePath.endsWith('package-lock.json'))
    .filter((filePath) => !filePath.startsWith('docs/api/'))
    .filter((filePath) => TEXT_EXTENSIONS.has(path.posix.extname(filePath).toLowerCase()))
    .sort();
}

function findingKinds(line) {
  const kinds = [];
  if (/\bschema_version\b/i.test(line)) {
    kinds.push('schema_version');
  }
  if (
    /\b(?:Schema(?:Name|Version)?|ContractName)\b\s*(?::=|=|:)/.test(line)
    || /json:"schema(?:_version)?/.test(line)
    || /["']schema(?:_version)?["']\s*:/.test(line)
    || /\.Schema\s*(?:==|!=)/.test(line)
  ) {
    kinds.push('schema_identity');
  }
  if (/DisallowUnknownFields|rejectUnknownJSONFields|decodeStrictJSON/.test(line)) {
    kinds.push('strict_decoder');
  }
  if (
    /unsupported.{0,80}version/i.test(line)
    || /version.{0,80}(?:unsupported|incompatible|mismatch)/i.test(line)
    || /schema.{0,80}(?:incompatible|mismatch)/i.test(line)
    || /expected.{0,40}schema/i.test(line)
  ) {
    kinds.push('version_or_schema_guard');
  }
  return kinds;
}

function parseMethods(rawMethods) {
  return [...rawMethods.matchAll(/"([^"]+)"/g)].map((match) => match[1]);
}

function analyzeText(filePath, text) {
  const routes = [];
  const findings = [];
  const lines = text.replaceAll('\r\n', '\n').split('\n');

  lines.forEach((line, index) => {
    const lineNumber = index + 1;
    const routeMatch = line.match(
      /HandleFunc\(\s*"([^"]+)"\s*,\s*([A-Za-z0-9_.]+)\s*\)(?:\.Methods\(([^)]*)\))?/,
    );
    if (routeMatch) {
      const routePath = routeMatch[1];
      routes.push({
        path: routePath,
        methods: routeMatch[3] ? parseMethods(routeMatch[3]) : ['ANY'],
        handler: routeMatch[2],
        file: filePath,
        line: lineNumber,
        suffix_kind: /\/v\d+(?:\/|$)/i.test(routePath)
          ? 'version'
          : /^\/api\/.*\.(?:\{format:|json$|csv$|xlsx$)/i.test(routePath)
            ? 'representation'
            : /\.[A-Za-z{][^/]*$/.test(routePath)
              ? 'asset'
            : null,
      });
    }

    const kinds = findingKinds(line);
    if (kinds.length > 0) {
      findings.push({
        file: filePath,
        line: lineNumber,
        kinds,
        excerpt: line.trim().replace(/\s+/g, ' '),
      });
    }
  });

  return { routes, findings };
}

function policyViolations(inventory) {
  const violations = [];

  for (const route of inventory.routes) {
    const versionedAPI = route.path.match(/^\/api\/v\d+\/([^/.{?]+)/i);
    if (versionedAPI) {
      const resource = versionedAPI[1].toLowerCase();
      if (GENERAL_COLLECTIONS.has(resource)) {
        violations.push(
          `${route.file}:${route.line} version-gates the general ${resource} collection at ${route.path}`,
        );
      } else {
        violations.push(
          `${route.file}:${route.line} adds versioned API route ${route.path}; register an actual compatibility boundary before adding a versioned route`,
        );
      }
    }
  }

  const replicationPaths = [
    'pkg/canonical/',
    'pkg/updateout/',
  ];
  for (const finding of inventory.findings) {
    if (
      finding.kinds.includes('schema_version')
      && (
        replicationPaths.some((prefix) => finding.file.startsWith(prefix))
        || finding.file === 'scripts/examples/patris-delivery-adapter.cjs'
      )
    ) {
      violations.push(
        `${finding.file}:${finding.line} adds schema_version to the product-sync replication boundary; negotiate capabilities and optional fields instead`,
      );
    }
  }

  return violations;
}

function buildInventory(root = REPOSITORY_ROOT) {
  const routes = [];
  const findings = [];
  for (const filePath of trackedTextFiles(root)) {
    const absolutePath = path.join(root, ...filePath.split('/'));
    const analyzed = analyzeText(filePath, fs.readFileSync(absolutePath, 'utf8'));
    routes.push(...analyzed.routes);
    findings.push(...analyzed.findings);
  }

  routes.sort((left, right) => (
    left.path.localeCompare(right.path)
    || left.file.localeCompare(right.file)
    || left.line - right.line
  ));
  findings.sort((left, right) => (
    left.file.localeCompare(right.file)
    || left.line - right.line
    || left.kinds.join(',').localeCompare(right.kinds.join(','))
  ));

  const inventory = {
    generated_from: 'tracked and unignored repository text; no production data',
    policy: {
      general_collections: [...GENERAL_COLLECTIONS].sort(),
      general_collection_rule: 'unversioned routes with capability negotiation, optional fields, and preserved extensions',
      product_sync_rule: 'schema identity retained for replication semantics; schema_version forbidden',
    },
    summary: {
      routes: routes.length,
      dotted_route_suffixes: routes.filter((route) => ['representation', 'asset'].includes(route.suffix_kind)).length,
      representation_route_suffixes: routes.filter((route) => route.suffix_kind === 'representation').length,
      versioned_routes: routes.filter((route) => route.suffix_kind === 'version').length,
      findings: findings.length,
      schema_version_findings: findings.filter((finding) => finding.kinds.includes('schema_version')).length,
      schema_identity_findings: findings.filter((finding) => finding.kinds.includes('schema_identity')).length,
      strict_decoder_findings: findings.filter((finding) => finding.kinds.includes('strict_decoder')).length,
      version_or_schema_guard_findings: findings.filter((finding) => finding.kinds.includes('version_or_schema_guard')).length,
    },
    routes,
    findings,
  };
  const violations = policyViolations(inventory);
  return { inventory, violations };
}

function serializedInventory(inventory) {
  return `${JSON.stringify(inventory, null, 2)}\n`;
}

function main(args = process.argv.slice(2)) {
  const mode = args[0] || '--check';
  if (!['--check', '--write'].includes(mode) || args.length > 1) {
    throw new Error('Usage: node scripts/check-api-compatibility.cjs [--check|--write]');
  }

  const { inventory, violations } = buildInventory();
  if (violations.length > 0) {
    throw new Error(`API compatibility policy violations:\n- ${violations.join('\n- ')}`);
  }

  const expected = serializedInventory(inventory);
  const absoluteReportPath = path.join(REPOSITORY_ROOT, ...REPORT_PATH.split('/'));
  if (mode === '--write') {
    fs.writeFileSync(absoluteReportPath, expected, 'utf8');
    process.stdout.write(`Wrote ${REPORT_PATH}\n`);
    return;
  }

  const actual = fs.existsSync(absoluteReportPath)
    ? fs.readFileSync(absoluteReportPath, 'utf8').replaceAll('\r\n', '\n')
    : '';
  if (actual !== expected) {
    throw new Error(
      `${REPORT_PATH} is stale. Review the compatibility change, then run `
      + '`node scripts/check-api-compatibility.cjs --write`.',
    );
  }
  process.stdout.write(`Verified ${REPORT_PATH}\n`);
}

if (require.main === module) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}

module.exports = {
  analyzeText,
  buildInventory,
  findingKinds,
  policyViolations,
  serializedInventory,
};
