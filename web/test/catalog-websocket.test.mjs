import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { coalesceCatalogReload, fetchCatalogProducts, websocketMatchesCollection } from '../src/catalog-api.mjs';

const source = await readFile(new URL('../src/app.js', import.meta.url), 'utf8');
const handler = source.slice(source.indexOf('function handleWebSocketMessage(data)'), source.indexOf('// Update connection status'));
const loader = source.slice(source.indexOf('async function fetchInitialDataOnce()'), source.indexOf('// Start the application when DOM is ready'));

test('settled pricing completion refreshes the selected collection without treating pending work as complete', () => {
    let reloads = 0;
    const context = vm.createContext({ Set, fetchInitialData() { reloads++; } });
    vm.runInContext(handler, context);
    for (const phase of ['dispatching', 'projecting', 'delivery_pending', 'recovery_required']) {
        context.handleWebSocketMessage({ type: 'pricing_progress', pricing: { phase, pending: false } });
    }
    context.handleWebSocketMessage({ type: 'pricing_progress', pricing: { phase: 'complete', pending: true } });
    assert.equal(reloads, 0);
    context.handleWebSocketMessage({ type: 'pricing_progress', pricing: { phase: 'complete', pending: false } });
    assert.equal(reloads, 1);
});

for (const products of [true, false]) {
    test(`${products ? 'KALA raw mode' : 'generic transformed mode'} keeps HTTP rows through initial, reconnect, and updates`, async () => {
        let rows = [{ Code: '101001001', price: 123 }];
        let productCapability = products;
        const state = { catalogProductsEndpoint: null, catalogProducts: [], records: [], catalogView: 'products' };
        let latest;
        const sandbox = {
            state, console, Set, fetchCatalogProducts, websocketMatchesCollection,
            normalizeRecordsPayload: value => value,
            normalizeCategoriesPayload: value => value,
            fetch: async path => ({ ok: true, json: async () => path === '/api/app'
                ? { capabilities: { products: productCapability } }
                : path === '/api/categories' ? [] : rows }),
            selectActiveCatalogRows: () => { state.records = state.catalogProducts; },
            localStorage: { removeItem() {} }
        };
        for (const name of ['refreshRecordSelectionKeys', 'clearTableErrorState', 'extractFields',
            'analyzeFields', 'renderTableHeader', 'updateFieldFilter', 'filterRecords', 'sortRecords',
            'renderTable', 'updateCounts', 'setLoadingState', 'updateFooterLastUpdate', 'updateFooterFileName']) {
            sandbox[name] = () => {};
        }
        const context = vm.createContext(sandbox);
        vm.runInContext(`${handler}\n${loader}`, context);
        const reload = coalesceCatalogReload(context.fetchInitialDataOnce);
        context.fetchInitialData = () => { latest = reload(); return latest; };
        context.fetchInitialData.isLoading = reload.isLoading;
        await context.fetchInitialData();
        for (const type of ['initial', 'initial', 'update']) {
            rows = [{ Code: '101001001', price: rows[0].price + 1 }];
            context.handleWebSocketMessage({ type, raw: products, added: [{ wrong_projection: true }] });
            await latest;
            assert.deepEqual(state.catalogProducts, rows);
            assert.equal(state.catalogProductsEndpoint, products ? 'products' : 'records');
        }
        productCapability = !products;
        context.handleWebSocketMessage({ type: 'initial', raw: !products, source_changed: true, added: [] });
        await latest;
        assert.equal(state.catalogProductsEndpoint, products ? 'records' : 'products');
        assert.deepEqual(state.catalogProducts, rows);
    });
}

test('matching projections reuse the stream; unknown projection and source change require discovery', () => {
	assert.equal(websocketMatchesCollection({ raw: false, pricing_authority: 'php' }, 'products'), false);
	assert.equal(websocketMatchesCollection({ raw: false, pricing_authority: 'unavailable' }, 'products'), false);
    assert.equal(websocketMatchesCollection({ raw: true }, 'records'), true);
    assert.equal(websocketMatchesCollection({ raw: false }, 'products'), true);
    assert.equal(websocketMatchesCollection({}, 'products'), false);
    assert.equal(websocketMatchesCollection({ raw: false }, null), false);
    assert.equal(websocketMatchesCollection({ raw: false, source_changed: true }, 'products'), false);
});

test('events during a read coalesce into a final fresh read without parallel requests', async () => {
    const releases = [];
    let calls = 0;
    let active = 0;
    const reload = coalesceCatalogReload(async () => {
        calls++;
        assert.equal(++active, 1);
        await new Promise(resolve => releases.push(resolve));
        active--;
    });
    const first = reload();
    assert.equal(reload.isLoading(), true);
    assert.equal(reload(), first);
    assert.equal(reload(), first);
    assert.equal(calls, 1);
    releases.shift()();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(calls, 2);
    reload();
    releases.shift()();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(calls, 3);
    releases.shift()();
    await first;
    assert.equal(active, 0);
    assert.equal(reload.isLoading(), false);
});

test('failed authority reload removes stale prices before later stream renders', async () => {
    const state = { catalogProducts: [{ final_price: 123 }], catalogCategories: [], records: [{ final_price: 123 }], filteredRecords: [{ final_price: 123 }], fields: ['final_price'] };
    const context = vm.createContext({ state, Error,
        console: { error() {} },
        fetchCatalogProducts: async () => { throw new Error('owner projection unavailable'); },
        fetch: async () => ({ ok: false }),
        setLoadingState() {}, showTableErrorState() {}, showInAppToast() {}, t: key => key
    });
    vm.runInContext(loader, context);
    await context.fetchInitialDataOnce();
    assert.equal(state.catalogProducts.length, 0);
    assert.equal(state.records.length, 0);
    assert.equal(state.filteredRecords.length, 0);
});
