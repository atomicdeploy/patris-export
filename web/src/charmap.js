import {
    applyStandaloneLocale,
    loadStandalonePreferences,
    persistStandaloneLanguage,
    standaloneText
} from './standalone-runtime.js';

const state = {
    config: null,
    language: 'en',
    entries: [],
    debugEnabled: false,
    source: '',
    payload: null
};

const els = {
    languageSelect: document.getElementById('languageSelect'),
    sourceName: document.getElementById('sourceName'),
    entryCount: document.getElementById('entryCount'),
    debugState: document.getElementById('debugState'),
    tableBody: document.getElementById('tableBody'),
    tableCaption: document.getElementById('tableCaption'),
    filterInput: document.getElementById('filterInput'),
    fileInput: document.getElementById('fileInput'),
    fileName: document.getElementById('fileName'),
    mapContent: document.getElementById('mapContent'),
    mapPath: document.getElementById('mapPath'),
    debugNotice: document.getElementById('debugNotice'),
    issues: document.getElementById('issues'),
    loadActive: document.getElementById('loadActive'),
    loadDefault: document.getElementById('loadDefault'),
    previewContent: document.getElementById('previewContent'),
    previewPath: document.getElementById('previewPath')
};

function t(key, values = {}) {
    return standaloneText(state.language, key, values);
}

function escapeHtml(value) {
    return String(value ?? '').replace(/[&<>"']/g, character => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    }[character]));
}

function localizedCount(value) {
    return new Intl.NumberFormat(state.language === 'fa' ? 'fa-IR' : 'en-US', {
        useGrouping: true,
        maximumFractionDigits: 0
    }).format(Number(value) || 0);
}

function localizedIssueReason(reason) {
    if (reason === 'expected hex and character separated by a tab') return t('charmapIssueExpectedTab');
    if (reason === 'hex value must decode to exactly one byte') return t('charmapIssueSingleByte');
    return t('charmapIssueUnknown');
}

async function fetchJSON(url, options) {
    const response = await fetch(url, options);
    if (!response.ok) {
        console.warn('Character-map request failed:', response.status, await response.text());
        const error = new Error(t('charmapRequestFailed', { status: response.status }));
        error.status = response.status;
        throw error;
    }
    return response.json();
}

function applyPayload(payload) {
    state.payload = payload;
    state.entries = payload.entries || [];
    state.debugEnabled = !!payload.debug_enabled;
    state.source = payload.path || payload.source || 'map';
    renderDynamicContent();
}

function renderDynamicContent() {
    const payload = state.payload;
    if (!payload) {
        els.sourceName.textContent = t('loading');
        els.entryCount.textContent = localizedCount(0);
        els.debugState.textContent = t('checking');
        renderTable();
        return;
    }

    els.sourceName.textContent = payload.path ? payload.path.split(/[\\/]/).pop() : (payload.source || t('charmapFallbackName'));
    els.entryCount.textContent = localizedCount(payload.count || state.entries.length);
    els.debugState.textContent = t(state.debugEnabled ? 'enabled' : 'disabled');
    els.debugState.className = state.debugEnabled ? 'ok' : 'danger';
    els.tableCaption.textContent = payload.path || payload.source || t('charmapFallbackName');

    if (payload.issues?.length) {
        const sample = payload.issues.slice(0, 5).map(issue => t('charmapIssueLine', {
            line: localizedCount(issue.line),
            reason: localizedIssueReason(issue.reason)
        })).join(' · ');
        els.issues.textContent = t('charmapIgnoredLines', {
            count: localizedCount(payload.issues.length),
            sample
        });
        els.issues.classList.remove('hidden');
    } else {
        els.issues.textContent = '';
        els.issues.classList.add('hidden');
    }
    renderTable();
}

function renderTable() {
    const filter = els.filterInput.value.trim().toLowerCase();
    const entries = state.entries.filter(entry => {
        if (!filter) return true;
        return [entry.hex, entry.decimal, entry.character, ...(entry.codepoints || [])]
            .join(' ')
            .toLowerCase()
            .includes(filter);
    });
    if (!entries.length) {
        const message = state.payload ? t('noCharmapMatches') : t('loadingCharmap');
        els.tableBody.innerHTML = `<tr><td colspan="5" class="empty">${escapeHtml(message)}</td></tr>`;
        return;
    }
    els.tableBody.innerHTML = entries.map(entry => {
        const character = entry.character || '';
        const codepoints = (entry.codepoints || []).join(' ');
        return `<tr>
            <td class="technical-cell">0x${escapeHtml(entry.hex)}</td>
            <td class="technical-cell">${escapeHtml(entry.decimal)}</td>
            <td class="char-cell">${escapeHtml(character)}</td>
            <td class="technical-cell">${escapeHtml(codepoints)}</td>
            <td>${escapeHtml(character === '[zwnj]' ? t('zwnjMarker') : character)}</td>
        </tr>`;
    }).join('');
}

function showNotice(message) {
    els.debugNotice.textContent = message;
    els.debugNotice.classList.toggle('hidden', !message);
}

async function loadMap(source = 'active') {
    showNotice('');
    applyPayload(await fetchJSON(`/api/charmap?source=${encodeURIComponent(source)}`));
}

async function previewMapContent() {
    showNotice('');
    const content = els.mapContent.value;
    if (!content.trim()) {
        showNotice(t('pasteMapFirst'));
        return;
    }
    try {
        applyPayload(await fetchJSON('/api/charmap/preview', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content })
        }));
    } catch (error) {
        showNotice(error.message);
    }
}

async function previewMapPath() {
    showNotice('');
    const path = els.mapPath.value.trim();
    if (!path) {
        showNotice(t('enterServerPathFirst'));
        return;
    }
    try {
        applyPayload(await fetchJSON('/api/charmap/preview', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ path })
        }));
    } catch (error) {
        showNotice(error.message);
    }
}

function applyLanguage() {
    state.language = applyStandaloneLocale(document, state.language, 'charmapDocumentTitle');
    els.languageSelect.value = state.language;
    renderDynamicContent();
}

async function setLanguage(language) {
    state.language = language;
    applyLanguage();
    try {
        state.config = await persistStandaloneLanguage(state.language, state.config);
    } catch (error) {
        console.warn('Could not persist the standalone-page language:', error);
    }
}

async function init() {
    const preferences = await loadStandalonePreferences();
    state.config = preferences.config;
    state.language = preferences.language;
    applyLanguage();

    els.languageSelect.addEventListener('change', event => setLanguage(event.target.value));
    els.loadActive.addEventListener('click', () => loadMap('active').catch(error => showNotice(error.message)));
    els.loadDefault.addEventListener('click', () => loadMap('default').catch(error => showNotice(error.message)));
    els.previewContent.addEventListener('click', previewMapContent);
    els.previewPath.addEventListener('click', previewMapPath);
    els.filterInput.addEventListener('input', renderTable);
    els.fileInput.addEventListener('change', async event => {
        const file = event.target.files?.[0];
        if (!file) return;
        els.fileName.removeAttribute('data-page-i18n');
        els.fileName.textContent = file.name;
        try {
            els.mapContent.value = await file.text();
            await previewMapContent();
        } catch {
            showNotice(t('charmapFileReadFailed'));
        }
    });

    loadMap('active').catch(error => showNotice(error.message));
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
} else {
    init();
}
