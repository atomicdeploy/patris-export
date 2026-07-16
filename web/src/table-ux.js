export const MIN_COLUMN_WIDTH = 80;
export const MAX_COLUMN_WIDTH = 480;
export const COLUMN_RESIZE_STEP = 12;

export const ROW_ICON_NAMES = [
    'warning',
    'clock',
    'price',
    'weight',
    'stock',
    'package',
    'check',
    'info'
];

export const CANONICAL_ROW_FIELDS = [
    'product_code',
    'name',
    'serial',
    'total_stock',
    'minimum_stock',
    'foreign_price',
    'weight_grams',
    'location',
    'import_freight_method_id',
    'freight_cny_per_kg',
    'markup_percent',
    'irt_per_cny',
    'final_price',
    'source_updated_at',
    'warnings'
];

export const ROW_RULE_OPERATORS = [
    'equals',
    'not_equals',
    'contains',
    'not_contains',
    'empty',
    'not_empty',
    'gt',
    'gte',
    'lt',
    'lte',
    'truthy',
    'falsy',
    'stale_days'
];

export const DEFAULT_ROW_ICON_FALLBACK = Object.freeze({
    icon: 'info',
    color: '#6366f1',
    label: 'Product'
});

export const ROW_COMMAND_DEFINITIONS = Object.freeze([
    Object.freeze({ id: 'inspect', icon: 'search', labelKey: 'inspect' }),
    Object.freeze({ id: 'copy_code', icon: 'copy', labelKey: 'copyCode', requiresCode: true }),
    Object.freeze({ id: 'copy_json', icon: 'braces', labelKey: 'copyJSON' }),
    Object.freeze({ id: 'toggle_selection', icon: 'check-square', labelKey: 'selectRow', selectedLabelKey: 'deselectRow' })
]);

const TABLE_MESSAGES = {
    en: {
        export: 'Export',
        columns: 'Columns',
        refresh: 'Refresh now',
        logs: 'Logs',
        total: 'Total',
        filtered: 'Filtered',
        searchPlaceholder: 'Search records...',
        noRecords: 'No records found',
        actions: 'Actions',
        rowActions: 'Actions for {code}',
        inspect: 'Inspect record',
        copyCode: 'Copy code',
        copyJSON: 'Copy row JSON',
        selectRow: 'Select row',
        deselectRow: 'Deselect row',
        selectAll: 'Select all filtered rows',
        selectRowLabel: 'Select row {code}',
        selectedCount: '{count} selected',
        clearFilters: 'Clear',
        clearAllFilters: 'Clear all filters',
        resizeColumn: 'Resize {column} column',
        resizeHelp: 'Use Left and Right Arrow keys to resize. Press Home or double-click to reset.',
        resetColumnWidths: 'Reset widths',
        widthsReset: 'Column widths reset',
        copied: 'Copied',
        codeCopied: 'Product code copied to the clipboard.',
        jsonCopied: 'Row JSON copied to the clipboard.',
        copyFailed: 'Clipboard access failed',
        language: 'Language',
        rowIcons: 'Conditional row icons',
        rowIconsHelp: 'The first enabled rule that matches a transformed canonical key wins.',
        enableRowIcons: 'Show conditional row icons',
        addRule: 'Add rule',
        fallback: 'Fallback',
        field: 'Canonical key',
        operator: 'Operator',
        value: 'Value',
        icon: 'Icon',
        color: 'Color',
        label: 'Accessible label',
        enabled: 'Enabled',
        moveUp: 'Move rule up',
        moveDown: 'Move rule down',
        removeRule: 'Remove rule',
        rule: 'Rule {number}',
        rowColoring: 'Row coloring',
        enableRowColoring: 'Enable row coloring rules',
        op_equals: 'Equals',
        op_not_equals: 'Does not equal',
        op_contains: 'Contains',
        op_not_contains: 'Does not contain',
        op_empty: 'Is empty',
        op_not_empty: 'Is not empty',
        op_gt: 'Greater than',
        op_gte: 'Greater than or equal',
        op_lt: 'Less than',
        op_lte: 'Less than or equal',
        op_truthy: 'Is truthy',
        op_falsy: 'Is falsey',
        op_stale_days: 'Older than days'
    },
    fa: {
        export: 'خروجی',
        columns: 'ستون‌ها',
        refresh: 'به‌روزرسانی',
        logs: 'رویدادها',
        total: 'کل',
        filtered: 'فیلترشده',
        searchPlaceholder: 'جست‌وجوی رکوردها...',
        noRecords: 'رکوردی پیدا نشد',
        actions: 'عملیات',
        rowActions: 'عملیات ردیف {code}',
        inspect: 'بررسی رکورد',
        copyCode: 'کپی کد',
        copyJSON: 'کپی JSON ردیف',
        selectRow: 'انتخاب ردیف',
        deselectRow: 'لغو انتخاب ردیف',
        selectAll: 'انتخاب همه ردیف‌های فیلترشده',
        selectRowLabel: 'انتخاب ردیف {code}',
        selectedCount: '{count} انتخاب‌شده',
        clearFilters: 'پاک‌کردن',
        clearAllFilters: 'پاک‌کردن همه فیلترها',
        resizeColumn: 'تغییر اندازه ستون {column}',
        resizeHelp: 'برای تغییر اندازه از کلیدهای جهت استفاده کنید. برای بازنشانی Home را بزنید یا دوبار کلیک کنید.',
        resetColumnWidths: 'بازنشانی عرض‌ها',
        widthsReset: 'عرض ستون‌ها بازنشانی شد',
        copied: 'کپی شد',
        codeCopied: 'کد محصول در کلیپ‌بورد کپی شد.',
        jsonCopied: 'JSON ردیف در کلیپ‌بورد کپی شد.',
        copyFailed: 'دسترسی به کلیپ‌بورد ناموفق بود',
        language: 'زبان',
        rowIcons: 'آیکون شرطی ردیف',
        rowIconsHelp: 'اولین قانون فعال که با کلید استاندارد تبدیل‌شده مطابقت داشته باشد اعمال می‌شود.',
        enableRowIcons: 'نمایش آیکون‌های شرطی ردیف',
        addRule: 'افزودن قانون',
        fallback: 'حالت پیش‌فرض',
        field: 'کلید استاندارد',
        operator: 'عملگر',
        value: 'مقدار',
        icon: 'آیکون',
        color: 'رنگ',
        label: 'برچسب دسترس‌پذیر',
        enabled: 'فعال',
        moveUp: 'انتقال قانون به بالا',
        moveDown: 'انتقال قانون به پایین',
        removeRule: 'حذف قانون',
        rule: 'قانون {number}',
        rowColoring: 'رنگ‌آمیزی ردیف',
        enableRowColoring: 'فعال‌کردن قوانین رنگ‌آمیزی ردیف',
        op_equals: 'برابر است',
        op_not_equals: 'برابر نیست',
        op_contains: 'شامل است',
        op_not_contains: 'شامل نیست',
        op_empty: 'خالی است',
        op_not_empty: 'خالی نیست',
        op_gt: 'بزرگ‌تر از',
        op_gte: 'بزرگ‌تر یا مساوی',
        op_lt: 'کوچک‌تر از',
        op_lte: 'کوچک‌تر یا مساوی',
        op_truthy: 'درست است',
        op_falsy: 'نادرست است',
        op_stale_days: 'قدیمی‌تر از تعداد روز'
    }
};

const ICON_PATHS = {
    warning: '<path d="M12 3 2.8 20h18.4L12 3z"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    price: '<path d="M4 7v10h16V7H4z"/><path d="M8 12h.01M16 12h.01"/><circle cx="12" cy="12" r="2.5"/>',
    weight: '<path d="M6 9h12l2 11H4L6 9z"/><path d="M9 9a3 3 0 0 1 6 0"/>',
    stock: '<path d="m4 8 8-4 8 4-8 4-8-4z"/><path d="m4 8v8l8 4 8-4V8M12 12v8"/>',
    package: '<path d="m4 8 8-4 8 4-8 4-8-4z"/><path d="m4 8v8l8 4 8-4V8M8 6l8 4"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m16 16 4 4"/>',
    copy: '<rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
    braces: '<path d="M8 4H6a2 2 0 0 0-2 2v4a2 2 0 0 1-2 2 2 2 0 0 1 2 2v4a2 2 0 0 0 2 2h2M16 4h2a2 2 0 0 1 2 2v4a2 2 0 0 0 2 2 2 2 0 0 0-2 2v4a2 2 0 0 1-2 2h-2"/>',
    'check-square': '<rect x="3" y="3" width="18" height="18" rx="3"/><path d="m7 12 3 3 7-7"/>',
    more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
    chevron: '<path d="m8 10 4 4 4-4"/>',
    download: '<path d="M12 3v12M7 10l5 5 5-5"/><path d="M4 19h16"/>',
    columns: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M15 4v16"/>',
    refresh: '<path d="M20 7v5h-5"/><path d="M4 17v-5h5"/><path d="M18 9a7 7 0 0 0-12-2l-2 5M6 15a7 7 0 0 0 12 2l2-5"/>',
    list: '<path d="M8 6h12M8 12h12M8 18h12"/><path d="M4 6h.01M4 12h.01M4 18h.01"/>',
    inbox: '<path d="M4 5h16v14H4z"/><path d="m4 13 4-4h8l4 4M8 13l2 2h4l2-2"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5 9 9 0 1 0 20.5 14.5z"/>',
    close: '<path d="M6 6l12 12M18 6 6 18"/>',
    trash: '<path d="M5 7h14M10 11v6M14 11v6M9 7V5h6v2M7 7l1 13h8l1-13"/>',
    arrowUp: '<path d="m7 14 5-5 5 5"/>',
    arrowDown: '<path d="m7 10 5 5 5-5"/>'
};

export function tableLanguage(value) {
    return String(value || '').toLowerCase() === 'fa' ? 'fa' : 'en';
}

export function tableText(language, key, values = {}) {
    const locale = tableLanguage(language);
    const template = TABLE_MESSAGES[locale][key] ?? TABLE_MESSAGES.en[key] ?? key;
    return Object.entries(values).reduce(
        (text, [name, value]) => text.replaceAll(`{${name}}`, String(value)),
        template
    );
}

export function iconMarkup(name, className = '') {
    const safeName = Object.prototype.hasOwnProperty.call(ICON_PATHS, name) ? name : 'info';
    const safeClass = String(className || '').replace(/[^a-zA-Z0-9 _-]/g, '');
    return `<svg${safeClass ? ` class="${safeClass}"` : ''} viewBox="0 0 24 24" aria-hidden="true" focusable="false">${ICON_PATHS[safeName]}</svg>`;
}

export function stableRecordKey(record) {
    const value = record?.Code ?? record?.product_code ?? '';
    return value === null || value === undefined ? '' : String(value).trim();
}

export function pruneSelectedKeys(selectedKeys, records) {
    const existing = new Set((records || []).map(stableRecordKey).filter(Boolean));
    for (const key of selectedKeys) {
        if (!existing.has(key)) selectedKeys.delete(key);
    }
    return selectedKeys;
}

export function selectionSummary(selectedKeys, records) {
    const keys = [...new Set((records || []).map(stableRecordKey).filter(Boolean))];
    const selected = keys.reduce((count, key) => count + (selectedKeys.has(key) ? 1 : 0), 0);
    return {
        selectable: keys.length,
        selected,
        checked: keys.length > 0 && selected === keys.length,
        indeterminate: selected > 0 && selected < keys.length
    };
}

export function defaultColumnWidth(field) {
    if (field === 'Code' || field === 'product_code') return 156;
    if (field === 'Name' || field === 'name') return 240;
    if (/^ANBAR\d+$/i.test(field)) return 92;
    if (/description|warning|sharh|title|name/i.test(field)) return 220;
    return 144;
}

export function clampColumnWidth(value, fallback = 144) {
    const numeric = Number(value);
    const safe = Number.isFinite(numeric) ? numeric : Number(fallback);
    return Math.round(Math.min(MAX_COLUMN_WIDTH, Math.max(MIN_COLUMN_WIDTH, safe)));
}

export function resizedColumnWidth(startWidth, movementX, rtl = false) {
    const direction = rtl ? -1 : 1;
    return clampColumnWidth(Number(startWidth) + Number(movementX || 0) * direction, startWidth);
}

export function keyboardColumnWidth(currentWidth, key, rtl = false) {
    if (key === 'Home') return null;
    if (key !== 'ArrowLeft' && key !== 'ArrowRight') return undefined;
    const physicalDirection = key === 'ArrowRight' ? 1 : -1;
    return clampColumnWidth(Number(currentWidth) + physicalDirection * (rtl ? -1 : 1) * COLUMN_RESIZE_STEP, currentWidth);
}

export function normalizeColumnWidths(widths) {
    if (!widths || typeof widths !== 'object' || Array.isArray(widths)) return {};
    return Object.entries(widths).reduce((result, [field, width]) => {
        const key = String(field || '').trim();
        if (key) result[key] = clampColumnWidth(width, defaultColumnWidth(key));
        return result;
    }, {});
}

export function normalizeRowIconRule(rule, index = 0) {
    const source = rule && typeof rule === 'object' ? rule : {};
    const operator = ROW_RULE_OPERATORS.includes(source.operator) ? source.operator : 'equals';
    const icon = ROW_ICON_NAMES.includes(source.icon) ? source.icon : 'info';
    return {
        id: String(source.id || `rule-${index + 1}`).trim() || `rule-${index + 1}`,
        field: String(source.field || '').trim(),
        operator,
        value: source.value === null || source.value === undefined ? '' : String(source.value),
        icon,
        color: validColor(source.color) ? source.color : '#6366f1',
        label: String(source.label || '').trim(),
        disabled: source.disabled === true
    };
}

export function normalizeRowIconRules(rules) {
    const source = Array.isArray(rules) ? rules : [];
    return source.map(normalizeRowIconRule);
}

export function normalizeRowIconFallback(fallback) {
    const source = fallback && typeof fallback === 'object' ? fallback : DEFAULT_ROW_ICON_FALLBACK;
    return {
        icon: ROW_ICON_NAMES.includes(source.icon) ? source.icon : DEFAULT_ROW_ICON_FALLBACK.icon,
        color: validColor(source.color) ? source.color : DEFAULT_ROW_ICON_FALLBACK.color,
        label: String(source.label || DEFAULT_ROW_ICON_FALLBACK.label).trim() || DEFAULT_ROW_ICON_FALLBACK.label
    };
}

export function resolveRowIcon(record, rules, fallback) {
    const normalizedRules = Array.isArray(rules) ? rules : normalizeRowIconRules(rules);
    for (const rule of normalizedRules) {
        if (!rule.disabled && rule.field && rowRuleMatches(canonicalRowValue(record, rule.field), rule.operator, rule.value)) {
            return { icon: rule.icon, color: rule.color, label: rule.label, ruleId: rule.id };
        }
    }
    return { ...normalizeRowIconFallback(fallback), ruleId: '' };
}

export function canonicalRowValue(record, field) {
    if (!record || typeof record !== 'object') return undefined;
    if (field === 'product_code') return record.product_code ?? record.Code;
    if (field === 'name') return record.name ?? record.Name;
    if (field === 'serial') return record.serial ?? record.Serial;
    if (field === 'total_stock' && record.total_stock === undefined && Array.isArray(record.ANBAR)) {
        return record.ANBAR.reduce((total, value) => {
            const numeric = Number(value);
            return total + (Number.isFinite(numeric) ? numeric : 0);
        }, 0);
    }
    return record[field];
}

export function rowRuleMatches(actual, operator, expected) {
    const flattened = flattenRuleValue(actual);
    const expectedText = String(expected ?? '').trim();
    const actualText = flattened.text;
    const empty = flattened.empty;
    switch (operator) {
        case 'equals':
            return actualText.localeCompare(expectedText, undefined, { sensitivity: 'base' }) === 0;
        case 'not_equals':
            return actualText.localeCompare(expectedText, undefined, { sensitivity: 'base' }) !== 0;
        case 'contains':
            return actualText.toLocaleLowerCase().includes(expectedText.toLocaleLowerCase());
        case 'not_contains':
            return !actualText.toLocaleLowerCase().includes(expectedText.toLocaleLowerCase());
        case 'empty':
            return empty;
        case 'not_empty':
            return !empty;
        case 'truthy':
            return Boolean(actual) && !empty && actualText !== '0' && actualText.toLowerCase() !== 'false';
        case 'falsy':
            return !actual || empty || actualText === '0' || actualText.toLowerCase() === 'false';
        case 'gt':
        case 'gte':
        case 'lt':
        case 'lte': {
            const left = Number(actual);
            const right = Number(expectedText);
            if (!Number.isFinite(left) || !Number.isFinite(right)) return false;
            if (operator === 'gt') return left > right;
            if (operator === 'gte') return left >= right;
            if (operator === 'lt') return left < right;
            return left <= right;
        }
        case 'stale_days': {
            const jalaliAge = jalaliAgeDays(actual);
            const timestamp = parseRuleDate(actual);
            const days = Number(expectedText);
            if (!Number.isFinite(days) || days < 0) return false;
            if (Number.isFinite(jalaliAge)) return jalaliAge > days;
            if (!Number.isFinite(timestamp)) return false;
            return Date.now() - timestamp > days * 24 * 60 * 60 * 1000;
        }
        default:
            return false;
    }
}

export function rowCommandDefinitions({ selected = false, hasCode = true } = {}) {
    return ROW_COMMAND_DEFINITIONS
        .filter(command => !command.requiresCode || hasCode)
        .map(command => ({
            ...command,
            labelKey: command.selectedLabelKey && selected ? command.selectedLabelKey : command.labelKey
        }));
}

export function fitMenuPosition({ x, y, width, height, viewportWidth, viewportHeight, margin = 8 }) {
    const maxX = Math.max(margin, Number(viewportWidth) - Number(width) - margin);
    const maxY = Math.max(margin, Number(viewportHeight) - Number(height) - margin);
    return {
        left: Math.max(margin, Math.min(Number(x) || margin, maxX)),
        top: Math.max(margin, Math.min(Number(y) || margin, maxY))
    };
}

function flattenRuleValue(value) {
    if (Array.isArray(value)) {
        const values = value.filter(item => item !== null && item !== undefined && String(item).trim() !== '');
        return { text: values.join(', '), empty: values.length === 0 };
    }
    if (value === null || value === undefined) return { text: '', empty: true };
    if (typeof value === 'object') {
        const keys = Object.keys(value);
        return { text: keys.length ? JSON.stringify(value) : '', empty: keys.length === 0 };
    }
    const text = String(value).trim();
    return { text, empty: text === '' };
}

function parseRuleDate(value) {
    if (value instanceof Date) return value.getTime();
    const text = String(value ?? '').trim();
    if (!text) return NaN;
    const timestamp = Date.parse(text);
    return Number.isFinite(timestamp) ? timestamp : NaN;
}

function jalaliAgeDays(value, now = new Date()) {
    const match = String(value ?? '').trim().match(/^(\d{2}|\d{4})[./-](\d{2})[./-](\d{2})$/);
    if (!match) return NaN;
    const year = match[1].length === 2 ? 1400 + Number(match[1]) : Number(match[1]);
    const month = Number(match[2]);
    const day = Number(match[3]);
    if (!validJalaliParts(year, month, day)) return NaN;

    try {
        const parts = new Intl.DateTimeFormat('en-US-u-ca-persian', {
            year: 'numeric',
            month: 'numeric',
            day: 'numeric'
        }).formatToParts(now).reduce((result, part) => {
            if (part.type === 'year' || part.type === 'month' || part.type === 'day') {
                result[part.type] = Number(part.value);
            }
            return result;
        }, {});
        if (!validJalaliParts(parts.year, parts.month, parts.day)) return NaN;
        return jalaliOrdinal(parts.year, parts.month, parts.day) - jalaliOrdinal(year, month, day);
    } catch {
        return NaN;
    }
}

function validJalaliParts(year, month, day) {
    if (!Number.isInteger(year) || !Number.isInteger(month) || !Number.isInteger(day)) return false;
    if (month < 1 || month > 12 || day < 1) return false;
    const maxDay = month <= 6 ? 31 : month <= 11 ? 30 : 30;
    return day <= maxDay;
}

function jalaliOrdinal(year, month, day) {
    // The 2,820-year arithmetic Persian-calendar cycle keeps day differences
    // stable across year boundaries without treating YY.MM.DD as Gregorian.
    const cycleYear = ((year - 474) % 2820 + 2820) % 2820 + 474;
    const leapDays = Math.floor(((cycleYear + 38) * 682) / 2816);
    const cycles = Math.floor((year - cycleYear) / 2820);
    const beforeMonth = month <= 7 ? (month - 1) * 31 : 186 + (month - 7) * 30;
    return cycles * 1029983 + (cycleYear - 1) * 365 + leapDays + beforeMonth + day;
}

function validColor(value) {
    return /^#[0-9a-f]{6}$/i.test(String(value || ''));
}
