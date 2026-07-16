import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../src/records.js', import.meta.url), 'utf8');
const { normalizeRecordsPayload } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('normalizes Code-keyed API rows without treating metadata as records', () => {
    const rows = normalizeRecordsPayload({
        '102001011': { name: 'LM75', final_price: 111999 },
        '102001012': { name: 'LM35', final_price: 222000 }
    });
    assert.deepEqual(rows, [
        { Code: '102001011', name: 'LM75', final_price: 111999 },
        { Code: '102001012', name: 'LM35', final_price: 222000 }
    ]);
});

test('consumes product-sync products and ignores envelope metadata', () => {
    const rows = normalizeRecordsPayload({
        schema: 'digitalogic.product-sync',
        schema_version: '1.0',
        event_id: 'sha256:event',
        products: [
            { product_code: '102001011', name: 'LM75' },
            { product_code: '102001012', name: 'LM35' }
        ]
    });
    assert.equal(rows.length, 2);
    assert.deepEqual(rows.map(row => row.Code), ['102001011', '102001012']);
    assert.ok(rows.every(row => !('schema' in row) && !('event_id' in row)));
});
