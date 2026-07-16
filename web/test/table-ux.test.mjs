import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../src/table-ux.js', import.meta.url), 'utf8');
const {
    DEFAULT_ROW_ICON_FALLBACK,
    clampColumnWidth,
    fitMenuPosition,
    iconMarkup,
    keyboardColumnWidth,
    normalizeColumnWidths,
    normalizeRowIconRules,
    pruneSelectedKeys,
    resizedColumnWidth,
    resolveRowIcon,
    rowCommandDefinitions,
    rowRuleMatches,
    selectionSummary,
    stableRecordKey,
    tableText
} = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('selection is Code-keyed, filter-aware, and pruned only when rows disappear', () => {
    const rows = [{ Code: '001' }, { product_code: 2 }, { Code: '' }];
    const selected = new Set(['001', '2', 'gone']);

    assert.equal(stableRecordKey(rows[1]), '2');
    pruneSelectedKeys(selected, rows);
    assert.deepEqual([...selected], ['001', '2']);
    assert.deepEqual(selectionSummary(selected, rows), {
        selectable: 2,
        selected: 2,
        checked: true,
        indeterminate: false
    });

    assert.deepEqual(selectionSummary(new Set(['001']), rows), {
        selectable: 2,
        selected: 1,
        checked: false,
        indeterminate: true
    });
});

test('column widths clamp and keyboard/pointer resizing reverse in RTL', () => {
    assert.equal(clampColumnWidth(20), 80);
    assert.equal(clampColumnWidth(999), 480);
    assert.equal(resizedColumnWidth(140, 20, false), 160);
    assert.equal(resizedColumnWidth(140, 20, true), 120);
    assert.equal(keyboardColumnWidth(140, 'ArrowRight', false), 152);
    assert.equal(keyboardColumnWidth(140, 'ArrowRight', true), 128);
    assert.equal(keyboardColumnWidth(140, 'Home', false), null);
    assert.equal(keyboardColumnWidth(140, 'Escape', false), undefined);
    assert.deepEqual(normalizeColumnWidths({ Code: 900, Name: '120', '': 100 }), { Code: 480, Name: 120 });
});

test('ordered icon rules match transformed canonical values and keep a fallback', () => {
    const rules = normalizeRowIconRules([
        { id: 'warning', field: 'warnings', operator: 'not_empty', icon: 'warning', color: '#d97706', label: 'Warnings' },
        { id: 'stock', field: 'total_stock', operator: 'gt', value: '0', icon: 'stock', color: '#059669', label: 'In stock' }
    ]);

    assert.equal(resolveRowIcon({ warnings: ['price_stale'], total_stock: 3 }, rules, DEFAULT_ROW_ICON_FALLBACK).ruleId, 'warning');
    assert.equal(resolveRowIcon({ warnings: [], total_stock: 3 }, rules, DEFAULT_ROW_ICON_FALLBACK).ruleId, 'stock');
    assert.equal(resolveRowIcon({ warnings: [], total_stock: 0 }, rules, DEFAULT_ROW_ICON_FALLBACK).ruleId, '');
    assert.equal(rowRuleMatches('2020-01-01T00:00:00Z', 'stale_days', '1'), true);
    assert.equal(rowRuleMatches('04.09.10', 'stale_days', '7'), true);
    assert.equal(rowRuleMatches([], 'empty', ''), true);
    assert.equal(rowRuleMatches(['a', 'b'], 'contains', 'B'), true);
});

test('row command metadata is shared and selection label is state-aware', () => {
    assert.deepEqual(rowCommandDefinitions({ selected: false, hasCode: true }).map(item => item.id), [
        'inspect', 'copy_code', 'copy_json', 'toggle_selection'
    ]);
    assert.equal(rowCommandDefinitions({ selected: true }).at(-1).labelKey, 'deselectRow');
    assert.equal(rowCommandDefinitions({ hasCode: false }).some(item => item.id === 'copy_code'), false);
});

test('menu positioning stays inside the viewport', () => {
    assert.deepEqual(fitMenuPosition({ x: 990, y: 790, width: 240, height: 300, viewportWidth: 1000, viewportHeight: 800 }), {
        left: 752,
        top: 492
    });
});

test('English and Persian table strings interpolate and SVG names are allowlisted', () => {
    assert.equal(tableText('en', 'selectedCount', { count: 3 }), '3 selected');
    assert.equal(tableText('fa', 'selectedCount', { count: 3 }), '3 انتخاب‌شده');
    assert.match(iconMarkup('warning'), /^<svg/);
    assert.doesNotMatch(iconMarkup('<script>'), /script/);
});

test('icon evaluation remains a single small ordered pass for 1,000 rows', () => {
    const rows = Array.from({ length: 1000 }, (_, index) => ({
        Code: String(index),
        warnings: index % 20 === 0 ? ['warning'] : [],
        total_stock: index % 3
    }));
    const rules = normalizeRowIconRules([
        { field: 'warnings', operator: 'not_empty', icon: 'warning' },
        { field: 'total_stock', operator: 'gt', value: 0, icon: 'stock' }
    ]);
    const resolved = rows.map(row => resolveRowIcon(row, rules, DEFAULT_ROW_ICON_FALLBACK));
    assert.equal(resolved.length, 1000);
    assert.equal(resolved[0].icon, 'warning');
    assert.equal(resolved[1].icon, 'stock');
});
