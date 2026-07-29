import assert from 'node:assert/strict';
import test from 'node:test';

import { fetchCatalogProducts } from '../src/catalog-api.mjs';

function response(status) {
    return {
        ok: status >= 200 && status < 300,
        status
    };
}

test('canonical viewers use products without requesting raw records', async () => {
    const requests = [];
    const productsResponse = response(200);
    const result = await fetchCatalogProducts(async path => {
        requests.push(path);
        return productsResponse;
    });

    assert.deepEqual(requests, ['/api/products']);
    assert.deepEqual(result, {
        collection: 'products',
        response: productsResponse
    });
});

test('generic datasets fall back to records only when products is unavailable', async () => {
    const requests = [];
    const recordsResponse = response(200);
    const result = await fetchCatalogProducts(async path => {
        requests.push(path);
        return path === '/api/products' ? response(404) : recordsResponse;
    });

    assert.deepEqual(requests, ['/api/products', '/api/records']);
    assert.deepEqual(result, {
        collection: 'records',
        response: recordsResponse
    });
});

test('server errors do not silently fall back to a different data model', async () => {
    const requests = [];
    const productsResponse = response(503);
    const result = await fetchCatalogProducts(async path => {
        requests.push(path);
        return productsResponse;
    });

    assert.deepEqual(requests, ['/api/products']);
    assert.deepEqual(result, {
        collection: 'products',
        response: productsResponse
    });
});

test('network failures do not silently fall back to records', async () => {
    const requests = [];
    await assert.rejects(
        fetchCatalogProducts(async path => {
            requests.push(path);
            throw new Error('network unavailable');
        }),
        /network unavailable/
    );
    assert.deepEqual(requests, ['/api/products']);
});
