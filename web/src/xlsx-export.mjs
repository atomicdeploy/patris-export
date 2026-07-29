export function canonicalWorkbookPath({
    collection = 'products',
    language = 'en',
    rtl = false,
    mode = 'precalculated',
    zebra = true
} = {}) {
    const normalizedCollection = String(collection || '').toLowerCase() === 'records' ? 'records' : 'products';
    const normalizedLanguage = String(language || '').toLowerCase() === 'fa' ? 'fa' : 'en';
    const normalizedMode = String(mode || '').toLowerCase() === 'formula' ? 'formula' : 'precalculated';
    const params = new URLSearchParams({
        download: '1',
        language: normalizedLanguage,
        rtl: rtl ? '1' : '0',
        mode: normalizedMode,
        zebra: zebra === false ? '0' : '1'
    });
    return `/api/${normalizedCollection}.xlsx?${params.toString()}`;
}
