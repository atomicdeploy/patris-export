'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  analyzeText,
  buildInventory,
  findingKinds,
  policyViolations,
} = require('./check-api-compatibility.cjs');

test('inventories route suffixes and compatibility markers', () => {
  const analyzed = analyzeText('pkg/server/example.go', `
s.router.HandleFunc("/api/records.{format:json|csv}", s.handleRecords).Methods("GET")
decoder.DisallowUnknownFields()
const SchemaVersion = 1
return fmt.Errorf("unsupported ABI version %d", version)
`);

  assert.deepEqual(analyzed.routes, [{
    path: '/api/records.{format:json|csv}',
    methods: ['GET'],
    handler: 's.handleRecords',
    file: 'pkg/server/example.go',
    line: 2,
    suffix_kind: 'representation',
  }]);
  assert.deepEqual(analyzed.findings.map((finding) => finding.kinds), [
    ['strict_decoder'],
    ['schema_identity'],
    ['version_or_schema_guard'],
  ]);
});

test('inventories routes without an explicit method guard', () => {
  const analyzed = analyzeText(
    'pkg/server/server.go',
    's.router.HandleFunc("/ws", s.handleWebSocket)',
  );
  assert.deepEqual(analyzed.routes[0], {
    path: '/ws',
    methods: ['ANY'],
    handler: 's.handleWebSocket',
    file: 'pkg/server/server.go',
    line: 1,
    suffix_kind: null,
  });
});

test('rejects versioned general collections and product-sync schema versions', () => {
  const inventory = {
    routes: [{
      path: '/api/v2/products',
      methods: ['GET'],
      handler: 's.handleProducts',
      file: 'pkg/server/server.go',
      line: 10,
      suffix_kind: 'version',
    }],
    findings: [{
      file: 'pkg/canonical/contract.go',
      line: 20,
      kinds: ['schema_version'],
      excerpt: 'SchemaVersion int `json:"schema_version"`',
    }],
  };

  assert.deepEqual(policyViolations(inventory), [
    'pkg/server/server.go:10 version-gates the general products collection at /api/v2/products',
    'pkg/canonical/contract.go:20 adds schema_version to the product-sync replication boundary; negotiate capabilities and optional fields instead',
  ]);
});

test('recognizes schema identities without treating ordinary prose as a guard', () => {
  assert.deepEqual(findingKinds('Schema: canonical.ContractName,'), ['schema_identity']);
  assert.deepEqual(findingKinds('SchemaVersion int `json:"schema_version"`'), [
    'schema_version',
    'schema_identity',
  ]);
  assert.deepEqual(findingKinds('The schema is documented for operators.'), []);
});

test('current repository satisfies compatibility policy', () => {
  const { inventory, violations } = buildInventory();
  assert.equal(inventory.routes.length > 0, true);
  assert.deepEqual(violations, []);
});
