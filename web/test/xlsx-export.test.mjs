import assert from 'node:assert/strict';
import test from 'node:test';

import { canonicalWorkbookPath } from '../src/xlsx-export.mjs';

test('workbook URL carries the active UI language and configured presentation mode', () => {
    const url = new URL(canonicalWorkbookPath({
        language: 'fa',
        rtl: true,
        mode: 'formula',
        zebra: false
    }), 'http://localhost');
    assert.equal(url.pathname, '/api/records.xlsx');
    assert.deepEqual(Object.fromEntries(url.searchParams), {
        download: '1',
        language: 'fa',
        rtl: '1',
        mode: 'formula',
        zebra: '0'
    });
});

test('workbook URL rejects unknown enum values and keeps safe defaults', () => {
    const url = new URL(canonicalWorkbookPath({ language: 'de', mode: 'raw' }), 'http://localhost');
    assert.equal(url.searchParams.get('language'), 'en');
    assert.equal(url.searchParams.get('mode'), 'precalculated');
    assert.equal(url.searchParams.get('zebra'), '1');
});

test('workbook URL supports normal and macro-enabled download routes', () => {
    for (const format of ['xlsx', 'xlsm', 'xltm']) {
        const url = new URL(canonicalWorkbookPath({ format }), 'http://localhost');
        assert.equal(url.pathname, `/api/records.${format}`);
    }
    const fallback = new URL(canonicalWorkbookPath({ format: 'xlsb' }), 'http://localhost');
    assert.equal(fallback.pathname, '/api/records.xlsx');
});
