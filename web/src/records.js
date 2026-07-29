// Convert every supported catalog response into the row array consumed by the
// viewer. Canonical KALA data comes from /api/products; /api/records may be an
// array or Code-keyed compatibility payload for generic datasets.
export function normalizeRecordsPayload(data) {
    if (Array.isArray(data)) {
        return data.map(recordWithCode);
    }
    if (data && Array.isArray(data.products)) {
        return data.products.map(recordWithCode);
    }
    if (!data || typeof data !== 'object') {
        return [];
    }
    return Object.entries(data).map(([code, record]) => recordWithCode(record, code));
}

// Category rows deliberately use their own immutable identity at the API
// boundary. The viewer still exposes it through the legacy `Code` display
// alias so the shared table, filters, selection, and context menu stay DRY.
export function normalizeCategoriesPayload(data) {
    if (Array.isArray(data)) {
        return data.map(categoryWithCode);
    }
    if (data && Array.isArray(data.categories)) {
        return data.categories.map(categoryWithCode);
    }
    if (!data || typeof data !== 'object') {
        return [];
    }
    return Object.entries(data).map(([code, category]) => categoryWithCode(category, code));
}

function recordWithCode(record, fallbackCode = '') {
    const row = record && typeof record === 'object' ? record : {};
    const code = row.Code ?? row.product_code ?? fallbackCode;
    return {
        ...row,
        Code: code === null || code === undefined ? '' : String(code)
    };
}

function categoryWithCode(category, fallbackCode = '') {
    const row = category && typeof category === 'object' ? category : {};
    const code = row.Code ?? row.category_code ?? fallbackCode;
    return {
        ...row,
        Code: code === null || code === undefined ? '' : String(code)
    };
}
