export function canonicalWorkbookPath({ format = 'xlsx', language = 'en', rtl = false, mode = 'precalculated', zebra = true } = {}) {
    const normalizedFormat = ['xlsx', 'xlsm', 'xltm'].includes(String(format || '').toLowerCase())
        ? String(format).toLowerCase()
        : 'xlsx';
    const workbookRoutes = {
        xlsx: '/api/records.xlsx',
        xlsm: '/api/records.xlsm',
        xltm: '/api/records.xltm'
    };
    const normalizedLanguage = String(language || '').toLowerCase() === 'fa' ? 'fa' : 'en';
    const normalizedMode = String(mode || '').toLowerCase() === 'formula' ? 'formula' : 'precalculated';
    const params = new URLSearchParams({
        download: '1',
        language: normalizedLanguage,
        rtl: rtl ? '1' : '0',
        mode: normalizedMode,
        zebra: zebra === false ? '0' : '1'
    });
    return `${workbookRoutes[normalizedFormat]}?${params.toString()}`;
}
