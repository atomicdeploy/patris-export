// Convert every supported records response into the row array consumed by the
// viewer. /api/records is Code-keyed; the product-sync envelope fallback keeps
// the UI safe if an older/mismatched server accidentally returns `products`.
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

function recordWithCode(record, fallbackCode = '') {
    const row = record && typeof record === 'object' ? record : {};
    const code = row.Code ?? row.product_code ?? fallbackCode;
    return {
        ...row,
        Code: code === null || code === undefined ? '' : String(code)
    };
}
