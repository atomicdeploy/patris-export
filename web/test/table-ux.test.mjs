import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../src/table-ux.js', import.meta.url), 'utf8');
const viewerSource = await readFile(new URL('../src/viewer.html', import.meta.url), 'utf8');
const appSource = await readFile(new URL('../src/app.js', import.meta.url), 'utf8');
const buildSource = await readFile(new URL('../build.js', import.meta.url), 'utf8');
const stylesSource = await readFile(new URL('../src/styles.scss', import.meta.url), 'utf8');
const { LARGE_TABLE_ROW_COUNT, tablePerformanceFixture } = await import('./fixtures/table-performance-1002.mjs');
const {
    DEFAULT_ROW_ICON_FALLBACK,
    assignStableRecordKeys,
    canonicalColumnKey,
    canonicalRowValue,
    canonicalRuleField,
    clampColumnWidth,
    deriveGridFields,
    duplicateSafeRecordKeys,
    fitMenuPosition,
    formatStructuredValue,
    hasNamingConventionWarnings,
    iconMarkup,
    keyboardColumnWidth,
    isWarehouseColumnField,
    localizedColumnLabel,
    normalizeColumnPreferenceList,
    localizedRelativeTime,
    normalizeColumnWidths,
    normalizeRowIconRules,
    nextRovingKey,
    pruneSelectedKeys,
    resizedColumnWidth,
    resolvePersistedColumnPreferences,
    resolvedRovingKey,
    resolveRowIcon,
    rowCommandDefinitions,
    rowRuleMatches,
    rovingTabIndexes,
    selectionSummary,
    stableStructuredJSON,
    stableRecordKey,
    structuredValueText,
    TABLE_VIRTUAL_ROW_HEIGHT,
    tableVirtualizationEnabled,
    tableTranslationCoverage,
    tableText,
    virtualizedRowSegments,
    warehouseColumnField,
    warehouseColumnName
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

test('duplicate and noncanonical records receive stable distinct selection keys', () => {
    const rows = [
        { Code: '001', name: 'First' },
        { Code: '001', name: 'Duplicate' },
        { name: 'No code', weight_grams: 20 },
        { name: 'No code', weight_grams: 20 }
    ];
    const keys = duplicateSafeRecordKeys(rows);
    assert.deepEqual(keys.slice(0, 2), ['code:001', 'code:001#2']);
    assert.match(keys[2], /^row:[a-z0-9]+$/);
    assert.equal(keys[3], `${keys[2]}#2`);

    const keyByRecord = new Map(rows.map((row, index) => [row, keys[index]]));
    const selected = new Set([keys[1], keys[2]]);
    assert.deepEqual(selectionSummary(selected, rows, row => keyByRecord.get(row)), {
        selectable: 4,
        selected: 2,
        checked: false,
        indeterminate: true
    });
    assert.deepEqual(duplicateSafeRecordKeys([...rows].reverse()).sort(), [...keys].sort());

    const stableKeys = assignStableRecordKeys(rows);
    const beforeSort = rows.map(row => stableKeys.get(row));
    assignStableRecordKeys([...rows].reverse(), stableKeys);
    assert.deepEqual(rows.map(row => stableKeys.get(row)), beforeSort);
    assert.equal(new Set(beforeSort).size, rows.length);
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
    assert.equal(canonicalColumnKey('Code'), 'product_code');
    assert.deepEqual(
        normalizeColumnWidths({ Code: 900, product_code: 211, Name: '120', '': 100 }),
        { product_code: 211, name: 120 }
    );
    assert.deepEqual(normalizeColumnWidths({ product_code: 211, Code: 900, 'product_code ': 444 }), { product_code: 211 });
});

test('UIConfig column preferences override and migrate the legacy browser cache', () => {
    const warehouse = warehouseColumnField('North hub');
    const legacy = {
        hiddenColumns: [` ${warehouse} `, warehouse, 'warnings'],
        columnOrder: ['product_code', 'name', 'shipping_price_per_kg', 'shipping_price_per_kg_currency', warehouse]
    };
    assert.deepEqual(normalizeColumnPreferenceList(legacy.hiddenColumns), [warehouse, 'warnings']);

    const migration = resolvePersistedColumnPreferences({}, legacy);
    assert.deepEqual(migration.hiddenColumns, [warehouse, 'warnings']);
    assert.deepEqual(migration.columnOrder, legacy.columnOrder);
    assert.equal(migration.migrateHiddenColumns, true);
    assert.equal(migration.migrateColumnOrder, true);

    const configured = resolvePersistedColumnPreferences({
        hidden_columns: [],
        column_order: ['product_code', warehouse, 'shipping_price_per_kg', 'shipping_price_per_kg_currency']
    }, legacy);
    assert.deepEqual(configured.hiddenColumns, []);
    assert.deepEqual(configured.columnOrder, [
        'product_code', warehouse, 'shipping_price_per_kg', 'shipping_price_per_kg_currency'
    ]);
    assert.equal(configured.migrateHiddenColumns, false);
    assert.equal(configured.migrateColumnOrder, false);

    const clearedAfterRestart = resolvePersistedColumnPreferences({
        hidden_columns: [],
        column_order: []
    }, legacy);
    assert.deepEqual(clearedAfterRestart.hiddenColumns, []);
    assert.deepEqual(clearedAfterRestart.columnOrder, []);
    assert.equal(clearedAfterRestart.cacheHiddenColumns, true);
    assert.equal(clearedAfterRestart.cacheColumnOrder, true);
    assert.equal(clearedAfterRestart.migrateHiddenColumns, false);
    assert.equal(clearedAfterRestart.migrateColumnOrder, false);
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

    const rawRule = normalizeRowIconRules([
        { id: 'raw', field: 'Sharh1', operator: 'not_empty', icon: 'warning' }
    ]);
    assert.equal(canonicalRuleField('Sharh1'), '');
    assert.equal(rawRule[0].field, '');
    assert.equal(canonicalRowValue({ Sharh1: 'must not match' }, 'Sharh1'), undefined);
    assert.equal(resolveRowIcon({ Sharh1: 'must not match' }, rawRule, DEFAULT_ROW_ICON_FALLBACK).ruleId, '');
    assert.equal(canonicalRuleField('Code'), 'product_code');
    assert.equal(canonicalRowValue({ Code: '001' }, 'product_code'), '001');
});

test('naming-convention warnings visibly classify only affected rows', () => {
    assert.equal(hasNamingConventionWarnings({ warnings: ['naming_multiple_spaces:name'] }), true);
    assert.equal(hasNamingConventionWarnings({ warnings: 'naming_leading_space:name' }), true);
    assert.equal(hasNamingConventionWarnings({ warnings: ['price_missing'] }), false);
    assert.equal(hasNamingConventionWarnings({ warnings: [] }), false);
    assert.match(appSource, /classList\.toggle\('naming-warning', hasNamingConventionWarnings\(record\)\)/);
    assert.match(stylesSource, /tr\.naming-warning td/);
});

test('table rows use a single roving tab stop and predictable arrow commands', () => {
    const keys = ['row-1', 'row-2', 'row-3'];
    assert.equal(resolvedRovingKey('missing', keys), 'row-1');
    assert.deepEqual(rovingTabIndexes(keys, 'row-2'), [-1, 0, -1]);
    assert.equal(nextRovingKey(keys, 'row-2', 'ArrowDown'), 'row-3');
    assert.equal(nextRovingKey(keys, 'row-2', 'ArrowUp'), 'row-1');
    assert.equal(nextRovingKey(keys, 'row-2', 'Home'), 'row-1');
    assert.equal(nextRovingKey(keys, 'row-2', 'End'), 'row-3');

    const rovingUpdate = appSource.slice(
        appSource.indexOf('function updateRovingRow'),
        appSource.indexOf('function rowAccessibleLabel')
    );
    assert.match(rovingUpdate, /if \(mounted\) \{[\s\S]*scrollTableRowIntoView\(logicalIndex\);[\s\S]*mounted\.focus/);
});

test('structured warehouse stock has readable deterministic human and machine text', () => {
    const text = structuredValueText({ Tehran: 3, Karaj: { reserved: 1, available: 2 } });
    assert.equal(text, 'Karaj: available: 2, reserved: 1, Tehran: 3');
    assert.doesNotMatch(text, /\[object Object\]/);
    assert.equal(structuredValueText(['north', 0, false, null, '']), 'north, 0, false, null, ""');

    const nested = { z: null, a: [0, false, { تهران: 2 }] };
    assert.equal(formatStructuredValue(nested, { mode: 'machine' }), '{"a":[0,false,{"تهران":2}],"z":null}');
    assert.equal(structuredValueText(false, { locale: 'fa' }), 'نادرست');
    assert.match(structuredValueText({ انبار: 12 }, { locale: 'fa' }), /۱۲/);
});

test('structured JSON is stable, cycle-safe, and never leaks object placeholders', () => {
    const left = { warehouse_stock: { Tehran: 3, Karaj: 1 }, values: [null, 0, false] };
    const right = { values: [null, 0, false], warehouse_stock: { Karaj: 1, Tehran: 3 } };
    assert.equal(stableStructuredJSON(left), stableStructuredJSON(right));
    assert.doesNotMatch(stableStructuredJSON(left), /\[object Object\]/);

    const circular = { name: 'safe' };
    circular.self = circular;
    assert.equal(stableStructuredJSON(circular), '{"name":"safe","self":"[Circular]"}');

    const appPaths = [
        /valueDiv\.textContent = formatStructuredValue\(value/,
        /csvCell\(formatStructuredValue\(getFieldValue\(record, field\), \{ mode: 'machine' \}\)\)/,
        /copyTextToClipboard\(stableStructuredJSON\(record, 2\)\)/,
        /formatStructuredValue\(aVal, \{ mode: 'sort'/
    ];
    appPaths.forEach(pattern => assert.match(appSource, pattern));
    const inspectorBody = appSource.slice(appSource.indexOf('function inspectRecord'), appSource.indexOf('// Toggle theme'));
    assert.doesNotMatch(inspectorBody, /String\(value\)|\[object Object\]/);
});

test('viewer build inlines bundle text without interpreting JavaScript replacement tokens', () => {
    assert.match(buildSource, /\.replace\('<!-- STYLES -->', \(\) =>/);
    assert.match(buildSource, /\.replace\('<!-- SCRIPTS -->', \(\) =>/);
    const marker = '<!-- SCRIPTS -->';
    const bundle = 'value.replace(/x/g, "$&")';
    assert.equal(marker.replace(marker, () => bundle), bundle);
});

test('1,002-row fixture stays inside a bounded virtual DOM window', () => {
    const rows = tablePerformanceFixture();
    assert.equal(rows.length, LARGE_TABLE_ROW_COUNT);
    assert.equal(tableVirtualizationEnabled(rows.length), true);

    const plan = virtualizedRowSegments({
        rowCount: rows.length,
        rowHeight: TABLE_VIRTUAL_ROW_HEIGHT,
        viewportHeight: 600,
        headerHeight: 150,
        scrollTop: 27150,
        overscan: 8,
        pinnedIndices: [0, rows.length - 1]
    });
    assert.equal(plan.firstVisible, 500);
    assert.ok(plan.renderedRows <= 30, `mounted ${plan.renderedRows} rows`);
    assert.equal(plan.segments[0].start, 0);
    assert.equal(plan.segments.at(-1).end, rows.length);
    const represented = plan.segments.reduce((total, segment) => total + segment.spacerRows + segment.end - segment.start, 0)
        + plan.trailingSpacerRows;
    assert.equal(represented, rows.length);

    // Every mounted structured cell exercises deterministic formatting, while
    // work scales with the window rather than all 1,002 logical rows.
    const mounted = plan.segments.flatMap(segment => rows.slice(segment.start, segment.end));
    assert.ok(mounted.every(row => !formatStructuredValue(row.warehouse_stock).includes('[object Object]')));
    assert.ok(mounted.length < rows.length / 20);
});

test('shared grid schema expands canonical and legacy warehouse stock into independent columns', () => {
    const north = warehouseColumnField('North hub');
    const south = warehouseColumnField('South/hub');
    assert.deepEqual(deriveGridFields([
        { product_code: '1', name: 'First', warehouse_stock: { 'South/hub': 2 } },
        { product_code: '2', name: 'Second', warehouse_stock: { 'North hub': 3 }, warnings: [] }
    ]), ['product_code', 'name', 'warnings', north, south]);
    assert.equal(isWarehouseColumnField(north), true);
    assert.equal(warehouseColumnName(north), 'North hub');
    assert.deepEqual(deriveGridFields([{ Code: '1', Name: 'Legacy', ANBAR: [2, 4, 6] }]), [
        'Code', 'Name', 'ANBAR1', 'ANBAR2', 'ANBAR3'
    ]);
});

test('known column keys use human localized labels and custom labels remain authoritative', () => {
    assert.equal(localizedColumnLabel('part_number', 'en'), 'Part Number');
    assert.equal(localizedColumnLabel('part_number', 'fa'), 'پارت نامبر');
    assert.equal(localizedColumnLabel('final_price', 'fa'), 'قیمت نهایی (تومان)');
    assert.equal(localizedColumnLabel('some_machine_key', 'en'), 'Some Machine Key');
    assert.equal(localizedColumnLabel('name', 'fa', 'Marketing title'), 'Marketing title');
    assert.equal(localizedColumnLabel('Code', 'fa', 'Code'), 'کد');
    assert.equal(localizedColumnLabel('FOROSH', 'en', 'FOROSH'), 'Sale Price');
    assert.equal(localizedColumnLabel('FOROSH', 'fa', 'FOROSH'), 'قیمت فروش');
    assert.equal(localizedColumnLabel('Sefaresh', 'fa', 'Sefaresh'), 'حد سفارش');
    assert.equal(localizedColumnLabel('Vahed', 'fa', 'Vahed'), 'واحد');
    assert.equal(localizedColumnLabel('shipping_price_per_kg', 'en'), 'Shipping Rate per kg');
    assert.equal(localizedColumnLabel('shipping_price_per_kg', 'fa'), 'نرخ حمل هر کیلوگرم');
    assert.equal(localizedColumnLabel('shipping_price_per_kg_currency', 'en'), 'Shipping Rate Currency (CNY/IRR)');
    assert.equal(localizedColumnLabel('shipping_price_per_kg_currency', 'fa'), 'ارز نرخ حمل (یوان/ریال)');
    assert.doesNotMatch(source, /shipping_price_per_kg_cny|freight_cny_per_kg/);

    const machineFields = [
        'product_code', 'category_code', 'part_number', 'sale_price_source',
        'purchase_price_source', 'total_stock', 'minimum_stock', 'foreign_currency',
        'foreign_price', 'weight_grams', 'shipping_method_id', 'shipping_price_per_kg',
        'shipping_price_per_kg_currency',
        'markup_percent', 'irt_per_cny', 'final_price', 'source_updated_at', 'record_hash',
        'Dates', 'FOROSH', 'Invahed', 'KHARYD', 'Kharyd_E', 'Sefaresh', 'Tedad_k', 'Vahed'
    ];
    machineFields.forEach(field => {
        assert.doesNotMatch(localizedColumnLabel(field, 'en', field), /_/, `machine key leaked in English: ${field}`);
        assert.match(localizedColumnLabel(field, 'fa', field), /[\u0600-\u06ff]/, `Persian label missing: ${field}`);
    });
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
    assert.equal(tableText('fa', 'allFields'), 'همه فیلدها');
    assert.equal(tableText('fa', 'filterPlaceholder'), 'فیلتر...');
    assert.equal(tableText('fa', 'icon_warning'), 'هشدار');
    assert.equal(tableText('fa', 'ruleLabel_price_missing'), 'قیمت در دسترس نیست');
    assert.equal(tableText('fa', 'fallbackProduct'), 'محصول');
    assert.match(iconMarkup('warning'), /^<svg/);
    assert.doesNotMatch(iconMarkup('<script>'), /script/);
});

test('relative last-update text follows the active interface language', () => {
    const now = new Date('2026-07-21T12:00:00.000Z');
    const twoMinutesAgo = new Date('2026-07-21T11:58:00.000Z');
    const oneHourAhead = new Date('2026-07-21T13:00:00.000Z');

    assert.equal(localizedRelativeTime(twoMinutesAgo, 'en', now), '2 minutes ago');
    assert.equal(localizedRelativeTime(oneHourAhead, 'en', now), 'in 1 hour');
    assert.equal(localizedRelativeTime(twoMinutesAgo, 'fa', now), '۲ دقیقه پیش');
    assert.equal(localizedRelativeTime(oneHourAhead, 'fa', now), '۱ ساعت دیگر');
    assert.doesNotMatch(localizedRelativeTime(twoMinutesAgo, 'fa', now), /ago|minute/i);
});

test('every declared viewer translation key has English and Persian text', () => {
    const keys = [...viewerSource.matchAll(/data-table-i18n(?:-(?:placeholder|title|aria-label))?="([^"]+)"/g)].map(match => match[1]);
    assert.ok(keys.length > 50);
    [...new Set(keys)].forEach(key => {
        assert.notEqual(tableText('en', key), key, `missing English translation: ${key}`);
        assert.notEqual(tableText('fa', key), key, `missing Persian translation: ${key}`);
    });

    const coverage = tableTranslationCoverage();
    assert.ok(coverage.english >= 200);
    assert.equal(coverage.english, coverage.persian);
    assert.deepEqual(coverage.missingEnglish, []);
    assert.deepEqual(coverage.missingPersian, []);
});

test('viewer English titles and accessible labels are wired to translation keys', () => {
    const tags = viewerSource.match(/<[^>]+(?:title|aria-label)="[A-Za-z][^"]*"[^>]*>/g) || [];
    const untranslated = tags.filter(tag => {
        const titleNeedsKey = /title="[A-Za-z]/.test(tag) && !/data-table-i18n-title=/.test(tag);
        const labelNeedsKey = /aria-label="[A-Za-z]/.test(tag) && !/data-table-i18n-aria-label=/.test(tag);
        return titleNeedsKey || labelNeedsKey;
    });
    assert.deepEqual(untranslated, []);
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
