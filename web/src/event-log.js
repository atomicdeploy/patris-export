import { structuredValueText, tableLanguage, tableText } from './table-ux.js';

export const EVENT_LOG_CHANGE_LIMITS = Object.freeze({
    maxRows: 30,
    maxFields: 240,
    maxFieldsPerRow: 24,
    maxValueLength: 320
});

export const MAX_DETAILED_EVENT_LOG_ENTRIES = 20;

const CHANGE_TYPES = ['added', 'modified', 'deleted'];
const IDENTITY_FIELDS = ['Code', 'product_code', 'code', 'id'];
const EVENT_TOKEN_TRANSLATION_KEYS = Object.freeze({
    row_updated: 'eventTokenRowUpdated',
    config_update: 'eventTokenConfigUpdate',
    'web-ui': 'eventTokenWebUI',
    websocket: 'eventTokenWebSocket',
    server: 'eventTokenServer',
    toast: 'eventTokenNotification',
    native_toast: 'eventTokenNotification',
    notification: 'eventTokenNotification',
    manual_refresh: 'eventTokenManualRefresh',
    refresh: 'eventTokenManualRefresh',
    resource_update: 'eventTokenResourceUpdate',
    source_drop: 'eventTokenDataSource',
    source_switch: 'eventTokenDataSource',
    source_info: 'eventTokenDataSource',
    file_info: 'eventTokenDataSource',
    table_settings: 'eventTokenTableSettings',
    row_action: 'eventTokenRowAction',
    grid_action: 'eventTokenRowAction',
    catalog_action: 'eventTokenRowAction',
    event_log: 'eventTokenEventLog',
    copy_event_log: 'eventTokenEventLog',
    connection: 'eventTokenConnection',
    copy_status: 'eventTokenConnection',
    notification_test: 'eventTokenNotificationTest',
    sound_test: 'eventTokenNotificationTest',
    xlsx_export: 'eventTokenExcelExport'
});
const LEGACY_EVENT_LOCALIZATION_RULES = Object.freeze([
    Object.freeze({
        identities: ['resource_update'],
        titleKeys: ['updatingInterface'],
        messageKeys: ['updatingInterfaceMessage']
    }),
    Object.freeze({
        identities: ['source_switch', 'source_drop'],
        titleKeys: ['unsupportedFile', 'sourceLoaded', 'sourceSwitchFailed'],
        messageKeys: ['unsupportedFileHelp']
    }),
    Object.freeze({
        identities: ['config_update'],
        titleKeys: ['settingsSaveFailed', 'settingsReloaded'],
        messageKeys: ['settingsReloadedMessage']
    }),
    Object.freeze({
        identities: ['toast', 'native_toast'],
        titleKeys: ['nativeToastUnavailable', 'toastRequestFailed'],
        messageKeys: []
    }),
    Object.freeze({
        identities: ['refresh', 'manual_refresh'],
        titleKeys: ['refreshRequested', 'refreshed', 'refreshFailed'],
        messageKeys: ['refreshRequestedMessage', 'refreshedMessage']
    }),
    Object.freeze({
        identities: ['table_settings'],
        titleKeys: ['widthsReset'],
        messageKeys: []
    }),
    Object.freeze({
        identities: ['row_action', 'grid_action', 'catalog_action', 'dialog_action'],
        titleKeys: ['copied', 'copyFailed'],
        messageKeys: ['codeCopied', 'jsonCopied']
    }),
    Object.freeze({
        identities: ['copy_status', 'connection'],
        titleKeys: ['copied'],
        messageKeys: ['statusCopied']
    }),
    Object.freeze({
        identities: ['copy_event_log', 'event_log'],
        titleKeys: ['copied'],
        messageKeys: ['eventLogCopied']
    }),
    Object.freeze({
        identities: ['xlsx_export'],
        titleKeys: ['excelExportStarted'],
        messageKeys: ['excelExportStartedMessage']
    }),
    Object.freeze({
        identities: ['source_info', 'file_info'],
        titleKeys: ['currentSourceFile'],
        messageKeys: []
    }),
    Object.freeze({
        identities: ['notification_test', 'sound_test'],
        titleKeys: ['soundTest'],
        messageKeys: ['soundTestMessage']
    })
]);

function isObject(value) {
    return !!value && typeof value === 'object' && !Array.isArray(value);
}

function semanticTemplateValues(value) {
    if (!isObject(value)) return {};
    try {
        const cloned = JSON.parse(JSON.stringify(value));
        return isObject(cloned) ? cloned : {};
    } catch (_error) {
        return {};
    }
}

function semanticTextField(entry, field, defaultText = '') {
    const keyField = `${field}Key`;
    const valuesField = `${field}Values`;
    const key = String(entry?.[keyField] || '').trim();
    if (!key) {
        return { [field]: String(entry?.[field] || defaultText) };
    }

    const values = semanticTemplateValues(entry?.[valuesField]);
    return {
        [keyField]: key,
        ...(Object.keys(values).length > 0 ? { [valuesField]: values } : {})
    };
}

export function normalizeEventLogContent(entry = {}, defaults = {}) {
    return {
        ...semanticTextField(entry, 'title', defaults.title || 'Patris Export event'),
        ...semanticTextField(entry, 'message', defaults.message || '')
    };
}

export function eventLogLocalizedText(entry, field, language = 'en') {
    if (!['title', 'message'].includes(field)) return '';
    const key = String(entry?.[`${field}Key`] || '').trim();
    if (key) {
        const translated = tableText(language, key, semanticTemplateValues(entry?.[`${field}Values`]));
        if (translated !== key) return translated;
    }
    return String(entry?.[field] || key || '');
}

function rowChangeCounts(entry) {
    if (isObject(entry?.changes?.counts)) {
        const counts = entry.changes.counts;
        if (CHANGE_TYPES.every(type => Number.isFinite(Number(counts[type])))) {
            return Object.fromEntries(CHANGE_TYPES.map(type => [type, Math.max(0, Number(counts[type]))]));
        }
    }

    const details = String(entry?.details || '');
    const matches = Object.fromEntries(
        CHANGE_TYPES.map(type => [type, details.match(new RegExp(`(?:^|\\s)${type}=(\\d+)`))])
    );
    if (CHANGE_TYPES.every(type => matches[type])) {
        return Object.fromEntries(CHANGE_TYPES.map(type => [type, Number(matches[type][1])]));
    }
    return null;
}

function legacyLocalizationRule(entry) {
    const identities = new Set([
        String(entry?.type || '').trim().toLowerCase(),
        String(entry?.source || '').trim().toLowerCase()
    ].filter(Boolean));
    return LEGACY_EVENT_LOCALIZATION_RULES.find(rule =>
        rule.identities.some(identity => identities.has(identity))
    );
}

function exactLegacyTranslationKey(value, candidateKeys) {
    const text = String(value || '');
    if (!text) return '';
    return candidateKeys.find(key =>
        text === tableText('en', key) || text === tableText('fa', key)
    ) || '';
}

export function upgradeEventLogEntryLocalization(entry = {}) {
    if (!isObject(entry)) return entry;
    const next = { ...entry };
    if (String(next.type || '').toLowerCase() === 'row_updated') {
        next.titleKey = 'eventLogRowsChanged';
        const counts = rowChangeCounts(next);
        if (counts) {
            next.messageKey = 'eventLogChangeSummary';
            next.messageValues = counts;
        }
    }

    const legacyRule = legacyLocalizationRule(next);
    if (legacyRule) {
        if (!next.titleKey) {
            const titleKey = exactLegacyTranslationKey(next.title, legacyRule.titleKeys);
            if (titleKey) next.titleKey = titleKey;
        }
        if (!next.messageKey) {
            const messageKey = exactLegacyTranslationKey(next.message, legacyRule.messageKeys);
            if (messageKey) next.messageKey = messageKey;
        }
    }

    if (!next.titleKey && !next.messageKey) return next;
    const content = normalizeEventLogContent(next);
    delete next.title;
    delete next.titleKey;
    delete next.titleValues;
    delete next.message;
    delete next.messageKey;
    delete next.messageValues;
    return { ...next, ...content };
}

function humanEventToken(value) {
    return String(value || '')
        .trim()
        .replaceAll('_', ' ')
        .replaceAll('-', ' ')
        .replace(/\s+/g, ' ')
        .replace(/\b\w/g, letter => letter.toUpperCase());
}

export function eventLogTokenLabel(value, language = 'en', kind = 'type') {
    const token = String(value || '').trim().toLowerCase();
    const translationKey = EVENT_TOKEN_TRANSLATION_KEYS[token];
    if (translationKey) return tableText(language, translationKey);

    const label = humanEventToken(token) || (tableLanguage(language) === 'fa' ? 'نامشخص' : 'Unknown');
    return tableText(language, kind === 'source' ? 'eventLogSourceOther' : 'eventLogTypeOther', { label });
}

function hasOwn(object, key) {
    return isObject(object) && Object.prototype.hasOwnProperty.call(object, key);
}

function boundedInteger(value, fallback, minimum = 1) {
    const number = Number.parseInt(value, 10);
    return Number.isFinite(number) ? Math.max(minimum, number) : fallback;
}

export function escapeHtml(value) {
    return String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function boundedValueText(value, maxLength) {
    let text;
    if (value === undefined) {
        text = '—';
    } else if (value === null) {
        text = 'null';
    } else if (value === '') {
        text = '""';
    } else if (Array.isArray(value) && value.length === 0) {
        text = '[]';
    } else if (isObject(value) && Object.keys(value).length === 0) {
        text = '{}';
    } else {
        text = structuredValueText(value);
    }

    if (text.length <= maxLength) {
        return { text, truncated: false };
    }
    const separator = '…';
    const visibleLength = Math.max(2, maxLength - separator.length);
    const headLength = Math.ceil(visibleLength / 2);
    const tailLength = Math.max(1, visibleLength - headLength);
    return {
        text: `${text.slice(0, headLength)}${separator}${text.slice(-tailLength)}`,
        truncated: true
    };
}

export function eventLogValueText(value, maxLength = EVENT_LOG_CHANGE_LIMITS.maxValueLength) {
    return boundedValueText(value, boundedInteger(maxLength, EVENT_LOG_CHANGE_LIMITS.maxValueLength)).text;
}

function rowIdentity(record, keyField, fallbackIndex = 0) {
    const source = isObject(record) ? record : {};
    const candidates = [keyField, ...IDENTITY_FIELDS]
        .map(field => String(field || '').trim())
        .filter((field, index, fields) => field && fields.indexOf(field) === index);

    for (const field of candidates) {
        const value = source[field];
        if (value !== undefined && value !== null && String(value).trim() !== '') {
            return { key: String(value), field };
        }
    }
    return { key: `#${fallbackIndex + 1}`, field: '' };
}

function deletedIdentity(value, keyField, fallbackIndex) {
    if (isObject(value)) {
        return rowIdentity(value, keyField, fallbackIndex);
    }
    if (value !== undefined && value !== null && String(value).trim() !== '') {
        return { key: String(value), field: String(keyField || '') };
    }
    return { key: `#${fallbackIndex + 1}`, field: String(keyField || '') };
}

export function recordIdentityKey(record, keyField = 'Code', fallbackIndex = 0) {
    return rowIdentity(record, keyField, fallbackIndex).key;
}

export function deletedRecordIdentityKey(value, keyField = 'Code', fallbackIndex = 0) {
    return deletedIdentity(value, keyField, fallbackIndex).key;
}

export function modifiedRecordIdentityKey(change, keyField = 'Code', fallbackIndex = 0) {
    const source = isObject(change) ? change : {};
    const identity = rowIdentity(source.record, keyField, fallbackIndex);
    return String(source.code ?? source[keyField] ?? identity.key);
}

function allocationByType(counts, maxRows) {
    const active = CHANGE_TYPES.filter(type => counts[type] > 0);
    const allocated = { added: 0, modified: 0, deleted: 0 };
    let remaining = maxRows;

    active.forEach((type, index) => {
        const activeRemaining = active.length - index;
        const share = Math.ceil(remaining / activeRemaining);
        allocated[type] = Math.min(counts[type], share);
        remaining -= allocated[type];
    });

    while (remaining > 0) {
        const type = active.find(candidate => allocated[candidate] < counts[candidate]);
        if (!type) break;
        allocated[type] += 1;
        remaining -= 1;
    }
    return allocated;
}

function sortedSnapshotFields(record, identityField) {
    if (!isObject(record)) return [];
    return Object.keys(record)
        .filter(field => field !== identityField)
        .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }));
}

function valuesEqual(left, right) {
    if (Object.is(left, right)) return true;
    try {
        return JSON.stringify(left) === JSON.stringify(right);
    } catch (_error) {
        return false;
    }
}

function modifiedFields(change, previousRecord, keyField) {
    const oldValues = isObject(change?.old_values) ? change.old_values : (isObject(change?.oldValues) ? change.oldValues : {});
    const newValues = isObject(change?.new_values) ? change.new_values : (isObject(change?.newValues) ? change.newValues : {});
    let fields = Array.isArray(change?.changed_fields)
        ? change.changed_fields
        : (Array.isArray(change?.changedFields) ? change.changedFields : []);

    if (fields.length === 0) {
        fields = [...new Set([...Object.keys(oldValues), ...Object.keys(newValues)])];
    }
    if (fields.length === 0 && isObject(previousRecord) && isObject(change?.record)) {
        fields = [...new Set([...Object.keys(previousRecord), ...Object.keys(change.record)])]
            .filter(field => !valuesEqual(previousRecord[field], change.record[field]));
    }
    return fields
        .map(String)
        .filter(field => field && field !== keyField)
        .filter((field, index, values) => values.indexOf(field) === index)
        .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }));
}

function interpolated(template, values = {}) {
    return Object.entries(values).reduce(
        (text, [name, value]) => String(text).replaceAll(`{${name}}`, String(value)),
        String(template || '')
    );
}

export function createEventLogChangeSnapshot(update = {}, previousRows = [], customLimits = {}) {
    const added = Array.isArray(update?.added) ? update.added : [];
    const modified = Array.isArray(update?.modified) ? update.modified : [];
    const deleted = Array.isArray(update?.deleted) ? update.deleted : [];
    const counts = { added: added.length, modified: modified.length, deleted: deleted.length };
    const totalRows = counts.added + counts.modified + counts.deleted;
    if (totalRows === 0) return null;

    const maxRows = boundedInteger(customLimits.maxRows, EVENT_LOG_CHANGE_LIMITS.maxRows);
    const maxFields = boundedInteger(customLimits.maxFields, EVENT_LOG_CHANGE_LIMITS.maxFields);
    const maxFieldsPerRow = boundedInteger(customLimits.maxFieldsPerRow, EVENT_LOG_CHANGE_LIMITS.maxFieldsPerRow);
    const maxValueLength = boundedInteger(customLimits.maxValueLength, EVENT_LOG_CHANGE_LIMITS.maxValueLength, 8);
    const allocation = allocationByType(counts, Math.min(maxRows, maxFields));
    const keyField = String(update?.key_field || update?.keyField || 'Code');
    const previousByKey = new Map();

    (Array.isArray(previousRows) ? previousRows : []).forEach((record, index) => {
        const identity = rowIdentity(record, keyField, index);
        previousByKey.set(identity.key, record);
    });

    const selected = [
        ...added.slice(0, allocation.added).map((record, index) => ({ type: 'added', record, index })),
        ...modified.slice(0, allocation.modified).map((change, index) => ({ type: 'modified', change, index })),
        ...deleted.slice(0, allocation.deleted).map((value, index) => ({ type: 'deleted', value, index }))
    ];
    const fieldsPerRow = Math.min(maxFieldsPerRow, Math.max(1, Math.floor(maxFields / Math.max(1, selected.length))));
    let omittedFields = 0;
    let truncatedValues = 0;

    const formatValue = value => {
        const bounded = boundedValueText(value, maxValueLength);
        if (bounded.truncated) truncatedValues += 1;
        return bounded.text;
    };

    const formatted = { added: [], modified: [], deleted: [] };
    selected.forEach(item => {
        if (item.type === 'modified') {
            const change = isObject(item.change) ? item.change : {};
            const key = modifiedRecordIdentityKey(change, keyField, item.index);
            const previousRecord = previousByKey.get(key);
            const oldValues = isObject(change.old_values) ? change.old_values : (isObject(change.oldValues) ? change.oldValues : {});
            const newValues = isObject(change.new_values) ? change.new_values : (isObject(change.newValues) ? change.newValues : {});
            const fields = modifiedFields(change, previousRecord, keyField);
            const visibleFields = fields.slice(0, fieldsPerRow);
            const hiddenFields = Math.max(0, fields.length - visibleFields.length);
            omittedFields += hiddenFields;
            formatted.modified.push({
                key,
                fields: visibleFields.map(field => ({
                    field,
                    before: formatValue(hasOwn(oldValues, field) ? oldValues[field] : previousRecord?.[field]),
                    after: formatValue(hasOwn(newValues, field) ? newValues[field] : change.record?.[field])
                })),
                omittedFields: hiddenFields
            });
            return;
        }

        const identity = item.type === 'added'
            ? rowIdentity(item.record, keyField, item.index)
            : deletedIdentity(item.value, keyField, item.index);
        const record = item.type === 'added'
            ? item.record
            : (isObject(item.value) ? item.value : previousByKey.get(identity.key));
        const fields = sortedSnapshotFields(record, identity.field);
        const visibleFields = fields.slice(0, fieldsPerRow);
        const hiddenFields = Math.max(0, fields.length - visibleFields.length);
        omittedFields += hiddenFields;
        formatted[item.type].push({
            key: identity.key,
            fields: visibleFields.map(field => ({ field, value: formatValue(record[field]) })),
            omittedFields: hiddenFields
        });
    });

    const displayedRows = formatted.added.length + formatted.modified.length + formatted.deleted.length;
    return {
        version: 1,
        keyField,
        totalCount: Number.isFinite(Number(update?.total_count ?? update?.totalCount))
            ? Number(update.total_count ?? update.totalCount)
            : null,
        counts,
        displayed: {
            added: formatted.added.length,
            modified: formatted.modified.length,
            deleted: formatted.deleted.length
        },
        omitted: {
            rows: Math.max(0, totalRows - displayedRows),
            fields: omittedFields,
            values: truncatedValues
        },
        ...formatted
    };
}

export function isEventLogChangeDetails(value) {
    if (!isObject(value) || value.version !== 1 || !isObject(value.counts)) return false;
    if (!CHANGE_TYPES.every(type => Number.isFinite(Number(value.counts[type])) && Number(value.counts[type]) >= 0)) {
        return false;
    }

    return CHANGE_TYPES.every(type => {
        if (!Array.isArray(value[type])) return false;
        return value[type].every(entry => {
            if (!isObject(entry) || !Array.isArray(entry.fields)) return false;
            if (!Number.isFinite(Number(entry.omittedFields)) || Number(entry.omittedFields) < 0) return false;
            return entry.fields.every(field => {
                if (!isObject(field) || typeof field.field !== 'string') return false;
                if (type === 'modified') {
                    return typeof field.before === 'string' && typeof field.after === 'string';
                }
                return typeof field.value === 'string';
            });
        });
    });
}

export function eventLogChangeTotal(changes) {
    if (!isEventLogChangeDetails(changes)) return 0;
    return CHANGE_TYPES.reduce((total, type) => total + Math.max(0, Number(changes.counts[type]) || 0), 0);
}

export function eventLogDisclosureText(changes, template = 'View change details ({count})') {
    return interpolated(template, { count: eventLogChangeTotal(changes).toLocaleString() });
}

export function retainRecentEventLogChanges(entries, maxDetailedEntries = MAX_DETAILED_EVENT_LOG_ENTRIES) {
    const limit = Math.max(0, Number.parseInt(maxDetailedEntries, 10) || 0);
    let retained = 0;
    return (Array.isArray(entries) ? entries : []).map(entry => {
        if (!isObject(entry) || !isEventLogChangeDetails(entry.changes)) return entry;
        retained += 1;
        if (retained <= limit) return entry;
        const compacted = { ...entry, changeDetailsExpired: true };
        delete compacted.changes;
        return compacted;
    });
}

function labelsWithDefaults(labels = {}) {
    return {
        added: labels.added || 'Added',
        modified: labels.modified || 'Modified',
        deleted: labels.deleted || 'Deleted',
        row: labels.row || 'Row',
        field: labels.field || 'Field',
        before: labels.before || 'Before',
        after: labels.after || 'After',
        value: labels.value || 'Value',
        noFields: labels.noFields || 'No field values available',
        moreFields: labels.moreFields || '{count} more fields',
        boundedPreview: labels.boundedPreview || 'Bounded preview: {count} rows, fields, or long values were omitted.'
    };
}

function snapshotRowsMarkup(entries, labels) {
    return entries.map(entry => {
        const rows = entry.fields.length > 0
            ? entry.fields.map(field => `
                <tr>
                    <th scope="row"><code dir="auto">${escapeHtml(entry.key)}</code></th>
                    <td><code dir="auto">${escapeHtml(field.field)}</code></td>
                    <td><code class="event-log-change-value" dir="auto">${escapeHtml(field.value)}</code></td>
                </tr>`).join('')
            : `
                <tr>
                    <th scope="row"><code dir="auto">${escapeHtml(entry.key)}</code></th>
                    <td colspan="2" class="event-log-change-empty">${escapeHtml(labels.noFields)}</td>
                </tr>`;
        const omitted = entry.omittedFields > 0
            ? `<tr><td colspan="3" class="event-log-change-omitted">${escapeHtml(interpolated(labels.moreFields, { count: entry.omittedFields.toLocaleString() }))}</td></tr>`
            : '';
        return rows + omitted;
    }).join('');
}

function modifiedRowsMarkup(entries, labels) {
    return entries.map(entry => {
        const rows = entry.fields.length > 0
            ? entry.fields.map(field => `
                <tr>
                    <th scope="row"><code dir="auto">${escapeHtml(entry.key)}</code></th>
                    <td><code dir="auto">${escapeHtml(field.field)}</code></td>
                    <td><code class="event-log-change-value before" dir="auto">${escapeHtml(field.before)}</code></td>
                    <td><code class="event-log-change-value after" dir="auto">${escapeHtml(field.after)}</code></td>
                </tr>`).join('')
            : `
                <tr>
                    <th scope="row"><code dir="auto">${escapeHtml(entry.key)}</code></th>
                    <td colspan="3" class="event-log-change-empty">${escapeHtml(labels.noFields)}</td>
                </tr>`;
        const omitted = entry.omittedFields > 0
            ? `<tr><td colspan="4" class="event-log-change-omitted">${escapeHtml(interpolated(labels.moreFields, { count: entry.omittedFields.toLocaleString() }))}</td></tr>`
            : '';
        return rows + omitted;
    }).join('');
}

function changeSectionMarkup(type, entries, labels) {
    if (!entries.length) return '';
    const modified = type === 'modified';
    const body = modified ? modifiedRowsMarkup(entries, labels) : snapshotRowsMarkup(entries, labels);
    return `
        <section class="event-log-change-section ${escapeHtml(type)}">
            <div class="event-log-change-section-heading">
                <h3>${escapeHtml(labels[type])}</h3>
                <span>${entries.length.toLocaleString()}</span>
            </div>
            <div class="event-log-change-table-wrap" tabindex="0" role="region" aria-label="${escapeHtml(labels[type])}">
                <table class="event-log-change-table${modified ? ' modified' : ''}">
                    <thead>
                        <tr>
                            <th scope="col">${escapeHtml(labels.row)}</th>
                            <th scope="col">${escapeHtml(labels.field)}</th>
                            ${modified
                                ? `<th scope="col">${escapeHtml(labels.before)}</th><th scope="col">${escapeHtml(labels.after)}</th>`
                                : `<th scope="col">${escapeHtml(labels.value)}</th>`}
                        </tr>
                    </thead>
                    <tbody>${body}</tbody>
                </table>
            </div>
        </section>`;
}

export function eventLogChangeDetailsMarkup(changes, customLabels = {}) {
    if (!isEventLogChangeDetails(changes)) return '';
    const labels = labelsWithDefaults(customLabels);
    const omitted = Math.max(0, Number(changes.omitted?.rows) || 0)
        + Math.max(0, Number(changes.omitted?.fields) || 0)
        + Math.max(0, Number(changes.omitted?.values) || 0);
    return `
        <div class="event-log-change-details">
            <div class="event-log-change-counts">
                ${CHANGE_TYPES.map(type => `<span class="${type}"><strong>${Math.max(0, Number(changes.counts[type]) || 0).toLocaleString()}</strong> ${escapeHtml(labels[type])}</span>`).join('')}
            </div>
            ${CHANGE_TYPES.map(type => changeSectionMarkup(type, changes[type], labels)).join('')}
            ${omitted > 0
                ? `<p class="event-log-change-preview-note">${escapeHtml(interpolated(labels.boundedPreview, { count: omitted.toLocaleString() }))}</p>`
                : ''}
        </div>`;
}
