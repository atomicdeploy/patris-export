import test from 'node:test';
import assert from 'node:assert/strict';
import { fetchCatalogProducts } from '../src/catalog-api.mjs';

for (const products of [true, false]) {
    test(`selects the declared ${products ? 'products' : 'records'} collection`, async () => {
        const paths = [];
        const response = { ok: true, status: 200 };
        const result = await fetchCatalogProducts(async path => {
            paths.push(path);
            return path === '/api/app'
                ? { ok: true, json: async () => ({ capabilities: { products, future: true } }) }
                : response;
        });
        const collection = products ? 'products' : 'records';
        assert.deepEqual(paths, ['/api/app', `/api/${collection}`]);
        assert.deepEqual(result, { collection, response });
    });
}
test('missing optional products capability selects the raw collection', async () => {
    const paths = [];
    await fetchCatalogProducts(async path => {
        paths.push(path);
        return { ok: true, json: async () => ({}) };
    });
    assert.deepEqual(paths, ['/api/app', '/api/records']);
});
for (const status of [404, 503]) {
    test(`a ${status} from an advertised collection does not switch data models`, async () => {
        const paths = [];
        const result = await fetchCatalogProducts(async path => {
            paths.push(path);
            return path === '/api/app'
                ? { ok: true, json: async () => ({ capabilities: { products: true } }) }
                : { ok: false, status };
        });
        assert.equal(result.response.status, status);
        assert.deepEqual(paths, ['/api/app', '/api/products']);
    });
}
test('discovery failure does not guess a collection or retry', async () => {
    const paths = [];
    await assert.rejects(fetchCatalogProducts(async path => {
        paths.push(path);
        return { ok: false, status: 503 };
    }), /Collection discovery failed/);
    assert.deepEqual(paths, ['/api/app']);
});
