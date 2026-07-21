import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const tableSource = await readFile(new URL('../src/table-ux.js', import.meta.url), 'utf8');
const tableModuleUrl = `data:text/javascript;base64,${Buffer.from(tableSource).toString('base64')}`;
const { tableText } = await import(tableModuleUrl);
const eventLogSource = (await readFile(new URL('../src/event-log.js', import.meta.url), 'utf8'))
    .replace("'./table-ux.js'", JSON.stringify(tableModuleUrl));
const {
    createEventLogChangeSnapshot,
    deletedRecordIdentityKey,
    escapeHtml,
    eventLogChangeDetailsMarkup,
    eventLogChangeTotal,
    eventLogDisclosureText,
    eventLogLocalizedText,
    eventLogTokenLabel,
    eventLogValueText,
    isEventLogChangeDetails,
    modifiedRecordIdentityKey,
    normalizeEventLogContent,
    recordIdentityKey,
    retainRecentEventLogChanges,
    upgradeEventLogEntryLocalization
} = await import(`data:text/javascript;base64,${Buffer.from(eventLogSource).toString('base64')}`);

test('captures added, modified, and deleted values before the live table mutates', () => {
    const previous = [
        { sku: 'A-1', name: 'Old name', price: 10, stock: { Tehran: 2 } },
        { sku: 'D-1', name: 'Deleted product', price: 5 }
    ];
    const details = createEventLogChangeSnapshot({
        key_field: 'sku',
        total_count: 2,
        added: [{ sku: 'N-1', name: 'New product', price: 7 }],
        modified: [{
            code: 'A-1',
            changed_fields: ['name', 'price', 'stock'],
            old_values: { name: 'Old name', price: 10, stock: { Tehran: 2 } },
            new_values: { name: 'New name', price: 12, stock: { Tehran: 1, Karaj: 3 } }
        }],
        deleted: ['D-1']
    }, previous);

    assert.equal(isEventLogChangeDetails(details), true);
    assert.equal(eventLogChangeTotal(details), 3);
    assert.deepEqual(details.counts, { added: 1, modified: 1, deleted: 1 });
    assert.equal(details.added[0].key, 'N-1');
    assert.deepEqual(details.added[0].fields.find(field => field.field === 'name'), {
        field: 'name',
        value: 'New product'
    });
    assert.deepEqual(details.modified[0].fields.find(field => field.field === 'price'), {
        field: 'price',
        before: '10',
        after: '12'
    });
    assert.equal(details.modified[0].fields.find(field => field.field === 'stock').after, 'Karaj: 3, Tehran: 1');
    assert.equal(details.deleted[0].key, 'D-1');
    assert.equal(details.deleted[0].fields.find(field => field.field === 'name').value, 'Deleted product');
    assert.equal(details.omitted.rows, 0);
});

test('bounds large updates fairly and reports omitted rows, fields, and values', () => {
    const longValue = 'x'.repeat(200);
    const added = Array.from({ length: 8 }, (_, index) => ({ Code: `A-${index}`, name: longValue, price: index }));
    const modified = Array.from({ length: 8 }, (_, index) => ({
        code: `M-${index}`,
        changed_fields: ['name', 'price', 'weight'],
        old_values: { name: 'old', price: index, weight: index },
        new_values: { name: longValue, price: index + 1, weight: index + 2 }
    }));
    const deleted = Array.from({ length: 8 }, (_, index) => `D-${index}`);
    const details = createEventLogChangeSnapshot(
        { added, modified, deleted },
        [],
        { maxRows: 6, maxFields: 12, maxFieldsPerRow: 2, maxValueLength: 20 }
    );

    assert.deepEqual(details.displayed, { added: 2, modified: 2, deleted: 2 });
    assert.equal(details.omitted.rows, 18);
    assert.ok(details.omitted.fields > 0);
    assert.ok(details.omitted.values > 0);
    assert.match(details.added[0].fields.find(field => field.field === 'name').value, /^x+…x+$/);
});

test('renders semantic escaped change tables and a descriptive disclosure label', () => {
    const details = createEventLogChangeSnapshot({
        added: [{ Code: '<script>alert(1)</script>', name: '<img src=x onerror=alert(1)>' }],
        modified: [{
            code: 'safe',
            changed_fields: ['name'],
            old_values: { name: '<old>' },
            new_values: { name: '<new>' }
        }],
        deleted: ['gone']
    });
    const markup = eventLogChangeDetailsMarkup(details);

    assert.match(markup, /<table class="event-log-change-table modified">/);
    assert.match(markup, /<th scope="col">Before<\/th>/);
    assert.match(markup, /&lt;img src=x onerror=alert\(1\)&gt;/);
    assert.doesNotMatch(markup, /<script>|<img/);
    assert.equal(eventLogDisclosureText(details), 'View change details (3)');
    assert.equal(escapeHtml('"<>&\''), '&quot;&lt;&gt;&amp;&#39;');
});

test('keeps legacy entries valid and compacts only older structured details', () => {
    const changes = createEventLogChangeSnapshot({ added: [{ Code: '1', name: 'One' }] });
    const entries = [
        { id: 'newest', title: 'Rows changed', changes },
        { id: 'second', title: 'Rows changed', changes },
        { id: 'legacy', title: 'Rows changed', details: 'added=1 modified=0 deleted=0' },
        { id: 'oldest', title: 'Rows changed', changes }
    ];
    const compacted = retainRecentEventLogChanges(entries, 2);

    assert.equal(isEventLogChangeDetails(compacted[0].changes), true);
    assert.equal(isEventLogChangeDetails(compacted[1].changes), true);
    assert.equal(compacted[2].details, entries[2].details);
    assert.equal(compacted[3].changes, undefined);
    assert.equal(compacted[3].changeDetailsExpired, true);
    assert.equal(isEventLogChangeDetails(undefined), false);
    assert.equal(createEventLogChangeSnapshot({ added: 'invalid' }), null);
});

test('rejects malformed persisted details and resolves custom row identities consistently', () => {
    const malformed = {
        version: 1,
        counts: { added: 1, modified: 0, deleted: 0 },
        added: [{}],
        modified: [],
        deleted: []
    };

    assert.equal(isEventLogChangeDetails(malformed), false);
    assert.doesNotThrow(() => eventLogChangeDetailsMarkup(malformed));
    assert.equal(eventLogChangeDetailsMarkup(malformed), '');
    assert.equal(recordIdentityKey({ sku: 'SKU-1', Code: 'legacy' }, 'sku'), 'SKU-1');
    assert.equal(deletedRecordIdentityKey({ sku: 'SKU-2' }, 'sku'), 'SKU-2');
    assert.equal(modifiedRecordIdentityKey({ record: { sku: 'SKU-3' } }, 'sku'), 'SKU-3');
});

test('formats empty, missing, and nested values without object placeholders', () => {
    assert.equal(eventLogValueText(undefined), '—');
    assert.equal(eventLogValueText(null), 'null');
    assert.equal(eventLogValueText(''), '""');
    assert.equal(eventLogValueText([]), '[]');
    assert.equal(eventLogValueText({}), '{}');
    assert.equal(eventLogValueText({ b: 2, a: [1, 3] }), 'a: 1, 3, b: 2');
    assert.doesNotMatch(eventLogValueText({ nested: { value: 1 } }), /\[object Object\]/);
    assert.equal(eventLogValueText('prefix-' + 'x'.repeat(30) + '-important-tail', 24), 'prefix-xxxxx…ortant-tail');
    assert.equal(tableText('en', 'eventLogViewChanges', { count: 3 }), 'View change details (3)');
    assert.match(tableText('fa', 'eventLogViewChanges', { count: 3 }), /\(3\)$/);
    assert.notEqual(tableText('fa', 'eventLogAdded'), 'eventLogAdded');
    assert.equal(
        tableText('fa', 'eventLogChangeSummary', { added: 1, modified: 2, deleted: 3 }),
        '1 افزوده، 2 تغییریافته، 3 حذف‌شده'
    );
});

test('persists semantic event text and retranslates it after a language switch', () => {
    const semantic = normalizeEventLogContent({
        title: 'Rows changed',
        titleKey: 'eventLogRowsChanged',
        message: '1 added, 2 modified, 3 deleted',
        messageKey: 'eventLogChangeSummary',
        messageValues: { added: 1, modified: 2, deleted: 3 }
    });
    const persisted = JSON.parse(JSON.stringify(semantic));

    assert.equal(Object.hasOwn(persisted, 'title'), false);
    assert.equal(Object.hasOwn(persisted, 'message'), false);
    assert.doesNotMatch(JSON.stringify(persisted), /Rows changed|1 added, 2 modified, 3 deleted/);
    assert.equal(eventLogLocalizedText(persisted, 'title', 'en'), 'Rows changed');
    assert.equal(eventLogLocalizedText(persisted, 'message', 'en'), '1 added, 2 modified, 3 deleted');
    assert.equal(eventLogLocalizedText(persisted, 'title', 'fa'), 'ردیف‌ها تغییر کردند');
    assert.equal(
        eventLogLocalizedText(persisted, 'message', 'fa'),
        '1 افزوده، 2 تغییریافته، 3 حذف‌شده'
    );
});

test('upgrades existing row-update log entries without losing their technical data', () => {
    const legacy = {
        id: 'legacy-row-update',
        type: 'row_updated',
        source: 'websocket',
        title: 'Rows changed',
        message: '1 added, 2 modified, 3 deleted',
        details: 'added=1 modified=2 deleted=3'
    };
    const upgraded = upgradeEventLogEntryLocalization(legacy);

    assert.equal(upgraded.id, legacy.id);
    assert.equal(upgraded.type, legacy.type);
    assert.equal(upgraded.source, legacy.source);
    assert.equal(upgraded.details, legacy.details);
    assert.equal(Object.hasOwn(upgraded, 'title'), false);
    assert.equal(Object.hasOwn(upgraded, 'message'), false);
    assert.equal(eventLogLocalizedText(upgraded, 'message', 'en'), legacy.message);
    assert.equal(
        eventLogLocalizedText(upgraded, 'message', 'fa'),
        '1 افزوده، 2 تغییریافته، 3 حذف‌شده'
    );
});

test('upgrades known app-owned legacy toast text and retranslates existing history', () => {
    const legacyResourceUpdate = {
        id: 'legacy-resource-update',
        type: 'resource_update',
        source: 'resource_update',
        title: 'Updating interface',
        message: 'A newer embedded web UI is available. Reloading now.',
        details: 'resource=v2'
    };
    const upgraded = upgradeEventLogEntryLocalization(legacyResourceUpdate);

    assert.equal(upgraded.id, legacyResourceUpdate.id);
    assert.equal(upgraded.details, legacyResourceUpdate.details);
    assert.equal(Object.hasOwn(upgraded, 'title'), false);
    assert.equal(Object.hasOwn(upgraded, 'message'), false);
    assert.equal(eventLogLocalizedText(upgraded, 'title', 'en'), legacyResourceUpdate.title);
    assert.equal(eventLogLocalizedText(upgraded, 'message', 'en'), legacyResourceUpdate.message);
    assert.equal(eventLogLocalizedText(upgraded, 'title', 'fa'), tableText('fa', 'updatingInterface'));
    assert.equal(eventLogLocalizedText(upgraded, 'message', 'fa'), tableText('fa', 'updatingInterfaceMessage'));
    assert.doesNotMatch(eventLogLocalizedText(upgraded, 'message', 'fa'), /embedded web UI|Reloading/i);

    const safeAppHistory = [
        {
            entry: { type: 'refresh', source: 'manual_refresh', title: 'Refresh requested', message: 'The backend is reloading the data source.' },
            titleKey: 'refreshRequested',
            messageKey: 'refreshRequestedMessage'
        },
        {
            entry: { type: 'copy_status', source: 'connection', title: 'Copied', message: 'Connection status copied to the clipboard.' },
            titleKey: 'copied',
            messageKey: 'statusCopied'
        },
        {
            entry: { type: 'notification_test', source: 'sound_test', title: 'Sound test', message: 'Notification audio was triggered.' },
            titleKey: 'soundTest',
            messageKey: 'soundTestMessage'
        }
    ];
    safeAppHistory.forEach(({ entry, titleKey, messageKey }) => {
        const localized = upgradeEventLogEntryLocalization(entry);
        assert.equal(localized.titleKey, titleKey);
        assert.equal(localized.messageKey, messageKey);
        assert.equal(eventLogLocalizedText(localized, 'title', 'fa'), tableText('fa', titleKey));
        assert.equal(eventLogLocalizedText(localized, 'message', 'fa'), tableText('fa', messageKey));
    });
});

test('legacy upgrade leaves arbitrary external event text unchanged', () => {
    const external = {
        type: 'toast',
        source: 'server',
        title: 'Updating interface',
        message: 'A newer embedded web UI is available. Reloading now.'
    };
    assert.deepEqual(upgradeEventLogEntryLocalization(external), external);

    const appEventWithExternalDetail = {
        type: 'resource_update',
        source: 'resource_update',
        title: 'Updating interface',
        message: 'Operator-provided diagnostic text'
    };
    const partiallyUpgraded = upgradeEventLogEntryLocalization(appEventWithExternalDetail);
    assert.equal(partiallyUpgraded.titleKey, 'updatingInterface');
    assert.equal(partiallyUpgraded.message, appEventWithExternalDetail.message);
    assert.equal(Object.hasOwn(partiallyUpgraded, 'messageKey'), false);
});

test('renders stored protocol type and source tokens as localized human labels', () => {
    assert.equal(eventLogTokenLabel('row_updated', 'en', 'type'), 'Row update');
    assert.equal(eventLogTokenLabel('config_update', 'en', 'type'), 'Configuration update');
    assert.equal(eventLogTokenLabel('web-ui', 'en', 'source'), 'Web interface');
    assert.equal(eventLogTokenLabel('websocket', 'en', 'source'), 'Live connection');
    assert.equal(eventLogTokenLabel('row_updated', 'fa', 'type'), 'به‌روزرسانی ردیف‌ها');
    assert.equal(eventLogTokenLabel('config_update', 'fa', 'type'), 'به‌روزرسانی تنظیمات');
    assert.equal(eventLogTokenLabel('web-ui', 'fa', 'source'), 'رابط وب');
    assert.equal(eventLogTokenLabel('websocket', 'fa', 'source'), 'اتصال زنده');
    assert.equal(eventLogTokenLabel('future_machine_event', 'en', 'type'), 'Future Machine Event event');
    assert.equal(eventLogTokenLabel('future_machine_event', 'fa', 'type'), 'رویداد Future Machine Event');
});
