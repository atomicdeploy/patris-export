// Application state
const state = {
    records: [],
    filteredRecords: [],
    fields: [],
    fieldTypes: {},  // Store detected field types
    fieldStats: {},  // Store field statistics for filter UI
    ws: null,
    searchTerm: '',
    selectedField: '',
    sortField: 'Code',
    sortDirection: 'asc',
    fileName: '',  // Track the actual data file name from server
    appInfo: null,
    resourceVersion: '',
    resourcePollTimer: null,
    isReloadingForUpdate: false,
    config: null,
    processStatus: null,
    connectionStatus: { state: 'connecting', text: 'Connecting...' },
    connectionLog: [],
    eventLog: [],
    configNotificationDedupe: new Map(),
    lastUpdateAt: null,
    isInitialLoad: true,
    columnFilters: {},  // Store active filters per column: { fieldName: { type, value, ... } }
    hiddenColumns: new Set(),  // Track hidden columns
    columnOrder: [],
    openRangePanel: null,
    scrollAnchor: null,
    isRestoringScroll: false,
    scrollSaveTimer: null,
    router: {
        route: '/viewer',
        outlet: null,
        tableContainer: null,
        toolbar: null,
        partialController: null,
        partialScripts: []
    },
    settings: {
        autoScrollToChanged: false,
        highlightChanges: true,
        rtlTextDirection: false,
        enablePagination: false,
        pageSize: 100,
        playNotificationSound: false,
        notificationSoundSource: 'external',
        showFooter: true,
        lastUpdateDisplayMode: 'both',
        enableRowColoring: true,
        rowColorGroup: '#6366f1',
        rowColorSubgroup: '#0ea5e9',
        rowColorNoStock: '#6b7280',
        rowColorHasStock: '#10b981'
    },
    notificationAudio: null,
    originalTitle: document.title,
    originalFavicon: null,
    titleFlashInterval: null,
    faviconTimeout: null,
    tabId: '',
    broadcastChannel: null,
    seenBroadcastMessages: new Set(),
    dragDepth: 0,
    isUploadingSource: false
};

const CONFIG_STORAGE_KEY = 'patris-config';
const SETTINGS_STORAGE_KEY = 'patris-settings';
const SCROLL_ANCHOR_STORAGE_KEY = 'patris-viewer-scroll-anchor';
const EVENT_LOG_STORAGE_KEY = 'patris-event-log';
const MAX_EVENT_LOG_ENTRIES = 200;
const CONFIG_NOTIFICATION_DEDUPE_STORAGE_KEY = 'patris-config-notification-dedupe';
const CONFIG_NOTIFICATION_DEDUPE_TTL_MS = 5000;
const RESOURCE_POLL_INTERVAL_MS = 30000;
const BROADCAST_CHANNEL_NAME = 'patris-export-frontend';
const BROADCAST_STORAGE_KEY = 'patris-broadcast-message';
const BROADCAST_MESSAGE_TTL_MS = 30000;
const INTERNAL_RELOAD_QUERY_PARAMS = ['resource_version', 'reloaded_at'];

cleanInternalReloadUrl();

function createTabId() {
    if (window.crypto?.randomUUID) {
        return window.crypto.randomUUID();
    }
    return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

function cleanInternalReloadUrl() {
    const cleanUrl = stripInternalReloadParams(new URL(window.location.href));
    if (cleanUrl.href !== window.location.href) {
        history.replaceState(history.state, '', cleanUrl.pathname + cleanUrl.search + cleanUrl.hash);
    }
}

function stripInternalReloadParams(url) {
    INTERNAL_RELOAD_QUERY_PARAMS.forEach(param => url.searchParams.delete(param));
    return url;
}

function visibleLocationBase() {
    const url = stripInternalReloadParams(new URL(window.location.href));
    return url.pathname + url.search;
}

function initFrontendBroadcast() {
    state.tabId = createTabId();

    if ('BroadcastChannel' in window) {
        state.broadcastChannel = new BroadcastChannel(BROADCAST_CHANNEL_NAME);
        state.broadcastChannel.addEventListener('message', event => {
            handleFrontendBroadcast(event.data);
        });
    }

    window.addEventListener('storage', event => {
        if (event.key !== BROADCAST_STORAGE_KEY || !event.newValue) {
            return;
        }
        try {
            handleFrontendBroadcast(JSON.parse(event.newValue));
        } catch (error) {
            console.error('Failed to parse cross-tab message:', error);
        }
    });

    window.addEventListener('beforeunload', () => {
        if (state.broadcastChannel) {
            state.broadcastChannel.close();
        }
    });
}

function publishFrontendBroadcast(type, payload = {}) {
    if (!state.tabId) {
        return;
    }

    const message = {
        id: createTabId(),
        type,
        payload,
        source: state.tabId,
        timestamp: Date.now()
    };

    if (state.broadcastChannel) {
        state.broadcastChannel.postMessage(message);
    }

    try {
        localStorage.setItem(BROADCAST_STORAGE_KEY, JSON.stringify(message));
        localStorage.removeItem(BROADCAST_STORAGE_KEY);
    } catch (error) {
        console.warn('Cross-tab storage fallback unavailable:', error);
    }
}

function handleFrontendBroadcast(message) {
    if (!message || message.source === state.tabId) {
        return;
    }
    if (message.id) {
        if (state.seenBroadcastMessages.has(message.id)) {
            return;
        }
        state.seenBroadcastMessages.add(message.id);
        setTimeout(() => state.seenBroadcastMessages.delete(message.id), BROADCAST_MESSAGE_TTL_MS);
    }
    if (Date.now() - (message.timestamp || 0) > BROADCAST_MESSAGE_TTL_MS) {
        return;
    }

    switch (message.type) {
        case 'settings:update':
            applyRemoteSettings(message.payload);
            break;
        case 'theme:update':
            applyRemoteTheme(message.payload);
            break;
        case 'sort:update':
            applyRemoteSort(message.payload);
            break;
        case 'columns:update':
            applyRemoteColumns(message.payload);
            break;
        case 'filters:update':
            applyRemoteFilters(message.payload);
            break;
        case 'view:update':
            applyRemoteView(message.payload);
            break;
        case 'app-info:update':
            applyAppInfo(message.payload, 'tab broadcast');
            break;
        case 'resource:update':
            if (message.payload?.version) {
                reloadForResourceUpdate(message.payload.version, 'tab broadcast');
            }
            break;
        case 'toast':
            showInAppToast(message.payload?.title, message.payload?.message, message.payload?.options || {});
            break;
        case 'event-log:clear':
            clearEventLog({ broadcast: false });
            break;
        default:
            console.info('Ignored unknown cross-tab message:', message.type);
    }
}

function logIntro() {
    const version = state.appInfo?.version || {};
    const styles = [
        'background:#111827;color:#facc15;padding:6px 10px;border-radius:6px 0 0 6px;font:700 13px ui-monospace,Cascadia Code,Consolas,monospace',
        'background:#312e81;color:#e0e7ff;padding:6px 10px;border-radius:0 6px 6px 0;font:600 13px ui-monospace,Cascadia Code,Consolas,monospace',
        'color:#6b7280;font:12px ui-monospace,Cascadia Code,Consolas,monospace'
    ];
    console.info('%cPatris Export%cModern HTML5 SPA', styles[0], styles[1]);
    console.info('%c✨ version=%s commit=%s build=%s', styles[2], version.version || 'dev', version.commit || 'unknown', version.build_date || 'unknown');
    console.info('%c📦 resources=%s', styles[2], getResourceVersion(state.appInfo) || 'unknown');
    console.info('%c🔌 WebSocket, REST, native toasts, generated audio fallback, and persistent config are active.', styles[2]);
}

// Load settings from localStorage
function loadSettings() {
    const saved = localStorage.getItem(SETTINGS_STORAGE_KEY);
    if (saved) {
        try {
            state.settings = { ...state.settings, ...JSON.parse(saved) };
        } catch (error) {
            console.error('❌ Failed to parse local settings:', error);
        }
    }
    
    // Load sort preferences
    const sortPrefs = localStorage.getItem('patris-sort');
    if (sortPrefs) {
        const { field, direction } = JSON.parse(sortPrefs);
        state.sortField = field || 'Code';
        state.sortDirection = direction || 'asc';
    }
    
    // Load hidden columns
    const hiddenCols = localStorage.getItem('patris-hidden-columns');
    if (hiddenCols) {
        state.hiddenColumns = new Set(JSON.parse(hiddenCols));
    }

    const savedOrder = localStorage.getItem('patris-column-order');
    if (savedOrder) {
        try {
            state.columnOrder = JSON.parse(savedOrder).filter(Boolean);
        } catch (e) {
            state.columnOrder = [];
        }
    }
    
    // Load column filters
    const savedFilters = localStorage.getItem('patris-column-filters');
    if (savedFilters) {
        try {
            state.columnFilters = JSON.parse(savedFilters);
        } catch (e) {
            state.columnFilters = {};
        }
    }

    const savedScrollAnchor = localStorage.getItem(SCROLL_ANCHOR_STORAGE_KEY);
    if (savedScrollAnchor) {
        try {
            state.scrollAnchor = JSON.parse(savedScrollAnchor);
        } catch (e) {
            state.scrollAnchor = null;
        }
    }

    const savedEventLog = localStorage.getItem(EVENT_LOG_STORAGE_KEY);
    if (savedEventLog) {
        try {
            state.eventLog = JSON.parse(savedEventLog)
                .filter(entry => entry && entry.title)
                .slice(0, MAX_EVENT_LOG_ENTRIES);
        } catch (e) {
            state.eventLog = [];
        }
    }
}

// Save settings to localStorage
function saveSettings(options = {}) {
    localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(state.settings));
    syncSettingsToConfig();
    if (options.broadcast !== false) {
        publishFrontendBroadcast('settings:update', { settings: state.settings });
    }
}

function applyConfig(config, source = 'server') {
    if (!config) return null;
    const diff = buildConfigDiff(state.config, config);
    state.config = config;
    localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    if (config.ui) {
        if (config.ui.theme) {
            localStorage.setItem('theme', config.ui.theme);
        }
        state.settings = {
            ...state.settings,
            autoScrollToChanged: !!config.ui.auto_scroll_to_changed,
            highlightChanges: config.ui.highlight_changes !== false,
            rtlTextDirection: !!config.ui.rtl_text_direction,
            enablePagination: !!config.ui.enable_pagination,
            pageSize: config.ui.page_size || state.settings.pageSize,
            playNotificationSound: !!config.ui.play_notification_sound,
            notificationSoundSource: config.ui.notification_sound_source || state.settings.notificationSoundSource,
            showFooter: config.ui.show_footer !== false,
            lastUpdateDisplayMode: config.ui.last_update_display_mode || state.settings.lastUpdateDisplayMode,
            enableRowColoring: config.ui.enable_row_coloring !== false,
            rowColorGroup: config.ui.row_color_group || state.settings.rowColorGroup,
            rowColorSubgroup: config.ui.row_color_subgroup || state.settings.rowColorSubgroup,
            rowColorNoStock: config.ui.row_color_no_stock || state.settings.rowColorNoStock,
            rowColorHasStock: config.ui.row_color_has_stock || state.settings.rowColorHasStock
        };
        localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(state.settings));
        applySettings();
        initTheme();
    }
    if (source !== 'local') {
        console.info('⚙️ Configuration applied from %s', source, config);
    }
    return diff;
}

function buildConfigDiff(previousConfig, nextConfig) {
    const before = flattenConfigForDiff(previousConfig || {});
    const after = flattenConfigForDiff(nextConfig || {});
    const paths = new Set([...Object.keys(before), ...Object.keys(after)]);
    const changed = [];

    [...paths].sort().forEach(path => {
        const beforeExists = Object.prototype.hasOwnProperty.call(before, path);
        const afterExists = Object.prototype.hasOwnProperty.call(after, path);
        const beforeValue = beforeExists ? before[path] : undefined;
        const afterValue = afterExists ? after[path] : undefined;
        if (beforeExists !== afterExists || stableStringify(beforeValue) !== stableStringify(afterValue)) {
            changed.push({ path, before: beforeValue, after: afterValue });
        }
    });

    if (changed.length === 0) {
        return { changed: [], signature: '', dedupeKey: '', message: '', details: '' };
    }

    const signature = stableStringify(changed);
    return {
        changed,
        signature,
        dedupeKey: hashString(signature),
        message: formatConfigDiffMessage(changed),
        details: formatConfigDiffDetails(changed)
    };
}

function flattenConfigForDiff(value, prefix = '', output = {}) {
    if (Array.isArray(value)) {
        output[prefix || '$'] = normalizeConfigValue(value);
        return output;
    }

    if (value && typeof value === 'object') {
        const keys = Object.keys(value).sort();
        if (keys.length === 0 && prefix) {
            output[prefix] = {};
        }
        keys.forEach(key => {
            const nextPrefix = prefix ? `${prefix}.${key}` : key;
            flattenConfigForDiff(value[key], nextPrefix, output);
        });
        return output;
    }

    output[prefix || '$'] = value;
    return output;
}

function normalizeConfigValue(value) {
    if (Array.isArray(value)) {
        return value.map(normalizeConfigValue);
    }
    if (value && typeof value === 'object') {
        return Object.keys(value).sort().reduce((acc, key) => {
            acc[key] = normalizeConfigValue(value[key]);
            return acc;
        }, {});
    }
    return value;
}

function stableStringify(value) {
    return JSON.stringify(normalizeConfigValue(value));
}

function hashString(value) {
    let hash = 2166136261;
    for (let i = 0; i < value.length; i += 1) {
        hash ^= value.charCodeAt(i);
        hash = Math.imul(hash, 16777619);
    }
    return (hash >>> 0).toString(16);
}

function formatConfigDiffMessage(changed) {
    if (changed.length === 1) {
        const change = changed[0];
        return `${change.path}: ${formatConfigDiffValue(change.before)} -> ${formatConfigDiffValue(change.after)}`;
    }
    const names = changed.slice(0, 4).map(change => change.path).join(', ');
    const suffix = changed.length > 4 ? ', ...' : '';
    return `${changed.length} settings changed: ${names}${suffix}`;
}

function formatConfigDiffDetails(changed) {
    return changed.slice(0, 12)
        .map(change => `${change.path}: ${formatConfigDiffValue(change.before)} -> ${formatConfigDiffValue(change.after)}`)
        .join('\n');
}

function formatConfigDiffValue(value) {
    if (typeof value === 'undefined') return 'unset';
    if (value === null) return 'null';
    if (typeof value === 'string') return value === '' ? 'empty' : value;
    if (typeof value === 'boolean' || typeof value === 'number') return String(value);
    const text = stableStringify(value);
    return text.length > 80 ? `${text.slice(0, 77)}...` : text;
}

function shouldNotifyConfigReload(diff) {
    if (!diff || !diff.changed?.length || !diff.dedupeKey) {
        return false;
    }

    const now = Date.now();
    for (const [key, timestamp] of state.configNotificationDedupe) {
        if (now - timestamp > CONFIG_NOTIFICATION_DEDUPE_TTL_MS) {
            state.configNotificationDedupe.delete(key);
        }
    }

    const localTimestamp = state.configNotificationDedupe.get(diff.dedupeKey);
    if (localTimestamp && now - localTimestamp <= CONFIG_NOTIFICATION_DEDUPE_TTL_MS) {
        return false;
    }

    const sharedCache = loadConfigNotificationDedupeCache(now);
    const sharedTimestamp = sharedCache[diff.dedupeKey];
    if (sharedTimestamp && now - sharedTimestamp <= CONFIG_NOTIFICATION_DEDUPE_TTL_MS) {
        state.configNotificationDedupe.set(diff.dedupeKey, sharedTimestamp);
        return false;
    }

    state.configNotificationDedupe.set(diff.dedupeKey, now);
    sharedCache[diff.dedupeKey] = now;
    saveConfigNotificationDedupeCache(sharedCache, now);
    return true;
}

function loadConfigNotificationDedupeCache(now = Date.now()) {
    try {
        const raw = localStorage.getItem(CONFIG_NOTIFICATION_DEDUPE_STORAGE_KEY);
        const parsed = raw ? JSON.parse(raw) : {};
        return Object.entries(parsed).reduce((acc, [key, timestamp]) => {
            if (Number.isFinite(timestamp) && now - timestamp <= CONFIG_NOTIFICATION_DEDUPE_TTL_MS) {
                acc[key] = timestamp;
            }
            return acc;
        }, {});
    } catch (error) {
        console.warn('Failed to read config notification dedupe cache:', error);
        return {};
    }
}

function saveConfigNotificationDedupeCache(cache, now = Date.now()) {
    try {
        const compact = Object.entries(cache).reduce((acc, [key, timestamp]) => {
            if (Number.isFinite(timestamp) && now - timestamp <= CONFIG_NOTIFICATION_DEDUPE_TTL_MS) {
                acc[key] = timestamp;
            }
            return acc;
        }, {});
        localStorage.setItem(CONFIG_NOTIFICATION_DEDUPE_STORAGE_KEY, JSON.stringify(compact));
    } catch (error) {
        console.warn('Failed to persist config notification dedupe cache:', error);
    }
}

async function loadServerConfig() {
    try {
        const response = await fetch('/api/config');
        if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
        const config = await response.json();
        applyConfig(config, 'server');
    } catch (error) {
        console.error('❌ Failed to load server config:', error);
        const cached = localStorage.getItem(CONFIG_STORAGE_KEY);
        if (cached) {
            try {
                applyConfig(JSON.parse(cached), 'local');
            } catch (parseError) {
                console.error('❌ Failed to parse cached config:', parseError);
            }
        }
    }
}

function syncSettingsToConfig() {
    if (!state.config) return;
    state.config.ui = {
        ...(state.config.ui || {}),
        theme: document.getElementById('settingsTheme')?.value || localStorage.getItem('theme') || 'system',
        auto_scroll_to_changed: state.settings.autoScrollToChanged,
        highlight_changes: state.settings.highlightChanges,
        rtl_text_direction: state.settings.rtlTextDirection,
        enable_pagination: state.settings.enablePagination,
        page_size: state.settings.pageSize,
        play_notification_sound: state.settings.playNotificationSound,
        notification_sound_source: state.settings.notificationSoundSource,
        show_footer: state.settings.showFooter,
        last_update_display_mode: state.settings.lastUpdateDisplayMode,
        enable_row_coloring: state.settings.enableRowColoring,
        row_color_group: state.settings.rowColorGroup,
        row_color_subgroup: state.settings.rowColorSubgroup,
        row_color_no_stock: state.settings.rowColorNoStock,
        row_color_has_stock: state.settings.rowColorHasStock
    };
    saveConfigToServer(state.config);
}

let configSaveTimer = null;
async function saveConfigToServer(config) {
    localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    clearTimeout(configSaveTimer);
    try {
        const response = await fetch('/api/config', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
        if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
        console.info('💾 Settings synced to config file.');
    } catch (error) {
        console.error('❌ Failed to sync settings to config file:', error);
        showInAppToast('Settings save failed', error.message, { error: true, broadcastToTabs: true, source: 'config_update', eventType: 'config_update' });
    }
}

// Save sort preferences to localStorage
function saveSortPreferences(options = {}) {
    localStorage.setItem('patris-sort', JSON.stringify({
        field: state.sortField,
        direction: state.sortDirection
    }));
    if (options.broadcast !== false) {
        publishFrontendBroadcast('sort:update', {
            field: state.sortField,
            direction: state.sortDirection
        });
    }
}

// Save hidden columns to localStorage
function saveHiddenColumns(options = {}) {
    localStorage.setItem('patris-hidden-columns', JSON.stringify([...state.hiddenColumns]));
    if (options.broadcast !== false) {
        publishFrontendBroadcast('columns:update', {
            hiddenColumns: [...state.hiddenColumns],
            columnOrder: state.columnOrder
        });
    }
}

function saveColumnOrder(options = {}) {
    state.columnOrder = state.fields.slice();
    localStorage.setItem('patris-column-order', JSON.stringify(state.columnOrder));
    if (options.broadcast !== false) {
        publishFrontendBroadcast('columns:update', {
            hiddenColumns: [...state.hiddenColumns],
            columnOrder: state.columnOrder
        });
    }
}

// Save column filters to localStorage
function saveColumnFilters(options = {}) {
    localStorage.setItem('patris-column-filters', JSON.stringify(state.columnFilters));
    if (options.broadcast !== false) {
        publishFrontendBroadcast('filters:update', {
            columnFilters: state.columnFilters
        });
    }
}

let viewStatePublishTimer = null;
function publishViewState() {
    clearTimeout(viewStatePublishTimer);
    viewStatePublishTimer = setTimeout(() => {
        publishFrontendBroadcast('view:update', {
            searchTerm: state.searchTerm,
            selectedField: state.selectedField
        });
    }, 120);
}

function applyRemoteSettings(payload = {}) {
    if (!payload.settings) return;
    state.settings = { ...state.settings, ...payload.settings };
    localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(state.settings));
    applySettings();
    renderTable();
}

function applyRemoteTheme(payload = {}) {
    if (!payload.theme) return;
    localStorage.setItem('theme', payload.theme);
    initTheme();
}

function applyRemoteSort(payload = {}) {
    state.sortField = payload.field || 'Code';
    state.sortDirection = payload.direction === 'desc' ? 'desc' : 'asc';
    saveSortPreferences({ broadcast: false });
    sortRecords();
    renderTableHeader();
    renderTable();
}

function applyRemoteColumns(payload = {}) {
    const hiddenColumns = Array.isArray(payload.hiddenColumns) ? payload.hiddenColumns : [];
    state.hiddenColumns = new Set(hiddenColumns);
    if (Array.isArray(payload.columnOrder)) {
        state.columnOrder = payload.columnOrder.filter(Boolean);
        applyColumnOrder();
    }
    saveHiddenColumns({ broadcast: false });
    removeHiddenColumnFilters({ broadcast: false });
    renderColumnManager();
    renderTableHeader();
    applyFilters();
}

function applyRemoteFilters(payload = {}) {
    state.columnFilters = payload.columnFilters && typeof payload.columnFilters === 'object'
        ? payload.columnFilters
        : {};
    saveColumnFilters({ broadcast: false });
    renderTableHeader();
    applyFilters();
}

function applyRemoteView(payload = {}) {
    state.searchTerm = typeof payload.searchTerm === 'string' ? payload.searchTerm : '';
    state.selectedField = typeof payload.selectedField === 'string' ? payload.selectedField : '';

    const searchInput = document.getElementById('searchInput');
    if (searchInput) searchInput.value = state.searchTerm;

    const fieldFilter = document.getElementById('fieldFilter');
    if (fieldFilter) fieldFilter.value = state.selectedField;

    applyFilters();
}

function removeHiddenColumnFilters(options = {}) {
    let changed = false;
    for (const field of Object.keys(state.columnFilters)) {
        if (state.hiddenColumns.has(field)) {
            delete state.columnFilters[field];
            changed = true;
        }
    }
    if (changed) {
        saveColumnFilters(options);
    }
}

function removeUnknownColumnFilters(options = {}) {
    const knownFields = new Set(state.fields);
    let changed = false;
    for (const field of Object.keys(state.columnFilters)) {
        if (!knownFields.has(field)) {
            delete state.columnFilters[field];
            changed = true;
        }
    }
    if (changed) {
        saveColumnFilters(options);
    }
}

// Field detection constants
const FIELD_DETECTION = {
    SAMPLE_SIZE: 100,              // Number of records to sample for type detection
    STATS_SAMPLE_SIZE: 1000,        // Number of records to sample for filter statistics
    DATE_CONFIDENCE_THRESHOLD: 0.8, // 80% of values must match date pattern
    CATEGORICAL_MAX_UNIQUE: 20,     // Max unique values for categorical
    CATEGORICAL_RATIO: 0.5          // Max ratio of unique/total for categorical
};

const FILTER_INPUT_DEBOUNCE_MS = 180;

function debounce(fn, delay) {
    let timeout = null;
    return (...args) => {
        clearTimeout(timeout);
        timeout = setTimeout(() => fn(...args), delay);
    };
}

// Jalali date utilities
const JalaliUtils = {
    // Parse Jalali date string in format YY.mm.dd or YY/mm/dd
    parse(dateStr) {
        if (!dateStr || typeof dateStr !== 'string') return null;
        const value = dateStr.trim();
        if (!/^\d{2}[.\/]\d{2}[.\/]\d{2}$/.test(value)) return null;
        const parts = value.split(/[.\/]/);
        if (parts.length !== 3) return null;
        
        const year = parseInt(parts[0], 10);
        const month = parseInt(parts[1], 10);
        const day = parseInt(parts[2], 10);
        
        if (isNaN(year) || isNaN(month) || isNaN(day)) return null;
        if (month < 1 || month > 12) return null;
        const maxDay = month <= 6 ? 31 : month <= 11 ? 30 : 29;
        if (day < 1 || day > maxDay) return null;
        
        // Return as object for comparison
        return { year, month, day };
    },
    
    // Compare two Jalali dates (-1 if a < b, 0 if equal, 1 if a > b)
    compare(a, b) {
        if (!a || !b) return 0;
        if (a.year !== b.year) return a.year - b.year;
        if (a.month !== b.month) return a.month - b.month;
        return a.day - b.day;
    },
    
    // Check if string looks like a Jalali date
    isJalaliDate(str) {
        return JalaliUtils.parse(str) !== null;
    }
};

// Detect field type based on values
function detectFieldType(field, records) {
    if (records.length === 0) return 'text';
    
    // Get sample values (configurable sample size for performance)
    const sampleSize = Math.min(FIELD_DETECTION.SAMPLE_SIZE, records.length);
    const values = [];
    
    for (let i = 0; i < sampleSize; i++) {
        const value = getFieldValue(records[i], field);
        
        if (value !== null && value !== undefined && value !== '') {
            values.push(value);
        }
    }
    
    if (values.length === 0) return 'text';
    
    // Check for date pattern (Jalali: YY.mm.dd or YY/mm/dd)
    const jalaliDateCount = values.filter(v => 
        typeof v === 'string' && JalaliUtils.isJalaliDate(v)
    ).length;
    
    if (jalaliDateCount > values.length * FIELD_DETECTION.DATE_CONFIDENCE_THRESHOLD) {
        return 'jalali-date';
    }
    
    // Check for numeric values
    const numericCount = values.filter(v => {
        const num = typeof v === 'string' ? parseFloat(v) : v;
        return !isNaN(num) && isFinite(num);
    }).length;
    
    if (numericCount === values.length) {
        return 'numeric';
    }
    
    // Check for categorical (limited unique values)
    const uniqueValues = new Set(values.map(v => String(v)));
    if (uniqueValues.size <= FIELD_DETECTION.CATEGORICAL_MAX_UNIQUE && 
        uniqueValues.size < values.length * FIELD_DETECTION.CATEGORICAL_RATIO) {
        return 'categorical';
    }
    
    return 'text';
}

// Calculate field statistics for filter UI
function calculateFieldStats(field, records) {
    const stats = {
        type: 'text',
        uniqueValues: new Set(),
        min: null,
        max: null,
        hasNull: false
    };
    
    const values = [];
    
    const sampleSize = Math.min(FIELD_DETECTION.STATS_SAMPLE_SIZE, records.length);
    for (let i = 0; i < sampleSize; i++) {
        const value = getFieldValue(records[i], field);
        
        if (value === null || value === undefined || value === '') {
            stats.hasNull = true;
        } else {
            values.push(value);
            stats.uniqueValues.add(String(value));
        }
    }
    
    stats.type = detectFieldType(field, records);
    
    if (stats.type === 'numeric') {
        const numValues = values.map(v => typeof v === 'string' ? parseFloat(v) : v).filter(v => !isNaN(v));
        if (numValues.length > 0) {
            stats.min = Math.min(...numValues);
            stats.max = Math.max(...numValues);
        }
    } else if (stats.type === 'jalali-date') {
        const dates = values.map(v => JalaliUtils.parse(String(v))).filter(d => d !== null);
        if (dates.length > 0) {
            dates.sort(JalaliUtils.compare);
            stats.min = dates[0];
            stats.max = dates[dates.length - 1];
        }
    }
    
    return stats;
}

// Analyze all fields
function analyzeFields() {
    if (state.records.length === 0) return;
    
    state.fields.forEach(field => {
        state.fieldTypes[field] = detectFieldType(field, state.records);
        state.fieldStats[field] = calculateFieldStats(field, state.records);
    });
    removeUnknownColumnFilters();
    removeHiddenColumnFilters();
}

// Format number with thousand separators (e.g., 1234567 -> 1,234,567)
function formatNumberWithSeparator(value) {
    // Don't format if it's not a number or if it's null/undefined
    if (value === null || value === undefined || value === '' || isNaN(value)) {
        return value;
    }
    
    // Convert to number and format with thousand separators
    const num = typeof value === 'string' ? parseFloat(value) : value;
    return num.toLocaleString('en-US');
}

function displayFieldName(field) {
    const labels = state.config?.column_labels || {};
    return labels[field] || labels[field.replace(/[0-9]+$/, '')] || field;
}

function displayStockGroupName() {
    const labels = state.config?.column_labels || {};
    if (labels.Stock || labels.stock) return labels.Stock || labels.stock;
    if (labels.ANBAR && !/^warehouse$/i.test(String(labels.ANBAR).trim())) return labels.ANBAR;
    return Object.keys(labels).length ? 'Stock' : 'ANBAR';
}

function parsePatrisCode(code) {
    const raw = String(code ?? '').replace(/\D/g, '');
    const groups = raw.match(/.{1,3}/g) || [];
    const depth = Math.min(groups.length, 3);
    const type = depth <= 1 ? 'group' : depth === 2 ? 'subgroup' : 'item';
    return { raw, groups, depth, type, group: groups[0] || '', subgroup: groups[1] || '', item: groups[2] || '' };
}

function anbarTotal(record) {
    if (!Array.isArray(record.ANBAR)) return 0;
    return record.ANBAR.reduce((sum, value) => {
        const n = typeof value === 'string' ? parseFloat(value) : value;
        return sum + (Number.isFinite(n) ? n : 0);
    }, 0);
}

// Apply settings to UI
function applySettings() {
    setChecked('showFooter', state.settings.showFooter);
    setChecked('autoScrollToChanged', state.settings.autoScrollToChanged);
    setChecked('highlightChanges', state.settings.highlightChanges);
    setChecked('rtlTextDirection', state.settings.rtlTextDirection);
    setChecked('enablePagination', state.settings.enablePagination);
    setValue('pageSize', state.settings.pageSize);
    setChecked('playNotificationSound', state.settings.playNotificationSound);
    setValue('notificationSoundSource', state.settings.notificationSoundSource || 'external');
    setValue('lastUpdateDisplayMode', state.settings.lastUpdateDisplayMode || 'both');
    setChecked('enableRowColoring', state.settings.enableRowColoring);
    setValue('rowColorGroup', state.settings.rowColorGroup || '#6366f1');
    setValue('rowColorSubgroup', state.settings.rowColorSubgroup || '#0ea5e9');
    setValue('rowColorNoStock', state.settings.rowColorNoStock || '#6b7280');
    setValue('rowColorHasStock', state.settings.rowColorHasStock || '#10b981');
    applyConfigToSettingsForm();
    document.body.classList.toggle('rtl-text-mode', !!state.settings.rtlTextDirection);
    applyFooterVisibility();
    applyRowColorSettings();
    updateLastUpdateDisplay();
}

function setValue(id, value) {
    const el = document.getElementById(id);
    if (el) el.value = value ?? '';
}

function setChecked(id, value) {
    const el = document.getElementById(id);
    if (el) el.checked = !!value;
}

function hexToRgba(hex, alpha) {
    const normalized = String(hex || '').replace('#', '');
    if (!/^[0-9a-f]{6}$/i.test(normalized)) return `rgba(99, 102, 241, ${alpha})`;
    const value = parseInt(normalized, 16);
    const r = (value >> 16) & 255;
    const g = (value >> 8) & 255;
    const b = value & 255;
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function applyRowColorSettings() {
    const root = document.documentElement;
    root.style.setProperty('--row-group-bg', hexToRgba(state.settings.rowColorGroup, 0.12));
    root.style.setProperty('--row-subgroup-bg', hexToRgba(state.settings.rowColorSubgroup, 0.1));
    root.style.setProperty('--row-no-stock-color', hexToRgba(state.settings.rowColorNoStock, 0.9));
    root.style.setProperty('--row-stock-accent', state.settings.rowColorHasStock || '#10b981');
    document.body.classList.toggle('row-coloring-disabled', !state.settings.enableRowColoring);
}

function applyFooterVisibility() {
    const showFooter = state.settings.showFooter !== false;
    document.body.classList.toggle('footer-hidden', !showFooter);
    const footer = document.getElementById('appFooter');
    if (footer) footer.hidden = !showFooter;
    const footerToggleBtn = document.getElementById('footerToggleBtn');
    if (footerToggleBtn) {
        footerToggleBtn.title = showFooter ? 'Hide footer' : 'Show footer';
    }
}

function toggleFooterVisibility() {
    state.settings.showFooter = state.settings.showFooter === false;
    applySettings();
    saveSettings();
}

function formatRelativeTime(date) {
    if (!date) return 'never';
    const diffMs = Date.now() - date.getTime();
    const future = diffMs < 0;
    const abs = Math.abs(diffMs);
    const units = [
        ['day', 86400000],
        ['hour', 3600000],
        ['minute', 60000],
        ['second', 1000]
    ];
    const [unit, size] = units.find(([, ms]) => abs >= ms) || units[units.length - 1];
    const count = Math.max(1, Math.round(abs / size));
    return future ? `in ${count} ${unit}${count === 1 ? '' : 's'}` : `${count} ${unit}${count === 1 ? '' : 's'} ago`;
}

function updateLastUpdateDisplay() {
    const el = document.getElementById('footerLastUpdate');
    if (!el) return;
    const date = state.lastUpdateAt || new Date();
    const absolute = formatDateTime(date);
    const relative = formatRelativeTime(date);
    const mode = state.settings.lastUpdateDisplayMode || 'both';
    el.textContent = mode === 'absolute' ? absolute : mode === 'relative' ? relative : `${absolute} (${relative})`;
    el.title = `${absolute} · ${relative}`;
}

function cycleLastUpdateMode() {
    const modes = ['absolute', 'relative', 'both'];
    const current = state.settings.lastUpdateDisplayMode || 'both';
    state.settings.lastUpdateDisplayMode = modes[(modes.indexOf(current) + 1) % modes.length];
    applySettings();
    saveSettings();
}

function syncProcessStatusMirror() {
    const source = document.getElementById('processStatus');
    const target = document.getElementById('footerProcessStatus');
    if (!source || !target) return;
    target.textContent = source.textContent;
    target.className = source.className + ' footer-process-status';
}

function initChromeMirrors() {
    syncProcessStatusMirror();
    const processStatus = document.getElementById('processStatus');
    if (processStatus) {
        new MutationObserver(syncProcessStatusMirror).observe(processStatus, {
            childList: true,
            characterData: true,
            subtree: true,
            attributes: true,
            attributeFilter: ['class']
        });
    }
}

function initTableWheelScroll() {
    const tableContainer = document.querySelector('.table-container');
    if (!tableContainer) return;
    tableContainer.addEventListener('wheel', event => {
        const canScrollX = tableContainer.scrollWidth > tableContainer.clientWidth;
        if (!canScrollX) return;
        if (Math.abs(event.deltaX) > Math.abs(event.deltaY) && event.deltaX !== 0) {
            tableContainer.scrollLeft += event.deltaX;
            event.preventDefault();
        } else if (event.shiftKey && event.deltaY !== 0) {
            tableContainer.scrollLeft += event.deltaY;
            event.preventDefault();
        }
    }, { passive: false });
}

function initScrollAnchorTracking() {
    const tableContainer = document.querySelector('.table-container');
    if (!tableContainer) return;
    tableContainer.addEventListener('scroll', () => scheduleSaveScrollAnchor(), { passive: true });
    window.addEventListener('beforeunload', () => saveScrollAnchorFromViewport({ immediate: true }));
    document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'hidden') {
            saveScrollAnchorFromViewport({ immediate: true });
        }
    });
}

function scheduleSaveScrollAnchor() {
    if (state.isRestoringScroll) return;
    clearTimeout(state.scrollSaveTimer);
    state.scrollSaveTimer = setTimeout(() => saveScrollAnchorFromViewport(), 120);
}

function saveScrollAnchorFromViewport(options = {}) {
    const anchor = getViewportScrollAnchor();
    if (!anchor) return;
    state.scrollAnchor = anchor;
    try {
        localStorage.setItem(SCROLL_ANCHOR_STORAGE_KEY, JSON.stringify(anchor));
    } catch (error) {
        if (options.immediate) return;
        console.warn('Failed to save viewer scroll anchor:', error);
    }
}

function getViewportScrollAnchor() {
    const tableContainer = document.querySelector('.table-container');
    if (!tableContainer || tableContainer.hidden) return null;
    const rows = [...document.querySelectorAll('#tableBody tr[data-code]')];
    if (rows.length === 0) return null;
    const containerRect = tableContainer.getBoundingClientRect();
    const stickyHeader = document.querySelector('.data-table thead');
    const headerHeight = stickyHeader?.getBoundingClientRect().height || 0;
    const targetY = containerRect.top + headerHeight + 2;
    let best = rows[0];
    let bestDistance = Number.POSITIVE_INFINITY;
    for (const row of rows) {
        const rect = row.getBoundingClientRect();
        if (rect.bottom < targetY) continue;
        const distance = Math.abs(rect.top - targetY);
        if (distance < bestDistance) {
            best = row;
            bestDistance = distance;
        }
        if (rect.top >= targetY) break;
    }
    return {
        code: best.dataset.code,
        offset: Math.round(best.getBoundingClientRect().top - targetY),
        scrollTop: Math.round(tableContainer.scrollTop),
        scrollLeft: Math.round(tableContainer.scrollLeft),
        source: state.fileName || '',
        sortField: state.sortField,
        sortDirection: state.sortDirection,
        savedAt: Date.now()
    };
}

function persistScrollAnchorForCode(code) {
    if (!code) return;
    const tableContainer = document.querySelector('.table-container');
    const anchor = {
        code: String(code),
        offset: 0,
        scrollTop: Math.round(tableContainer?.scrollTop || 0),
        scrollLeft: Math.round(tableContainer?.scrollLeft || 0),
        source: state.fileName || '',
        sortField: state.sortField,
        sortDirection: state.sortDirection,
        savedAt: Date.now()
    };
    state.scrollAnchor = anchor;
    try {
        localStorage.setItem(SCROLL_ANCHOR_STORAGE_KEY, JSON.stringify(anchor));
    } catch (error) {
        console.warn('Failed to save viewer scroll anchor:', error);
    }
}

function restoreScrollAnchorAfterRender(options = {}) {
    const anchor = state.scrollAnchor;
    if (!anchor?.code) return;
    if (anchor.source && state.fileName && anchor.source !== state.fileName) return;
    const tableContainer = document.querySelector('.table-container');
    if (!tableContainer || tableContainer.hidden) return;
    requestAnimationFrame(() => {
        const row = document.querySelector(`#tableBody tr[data-code="${cssEscape(anchor.code)}"]`);
        if (!row) return;
        const stickyHeader = document.querySelector('.data-table thead');
        const headerHeight = stickyHeader?.getBoundingClientRect().height || 0;
        const currentTop = row.offsetTop;
        const targetTop = Math.max(0, currentTop - headerHeight - (Number.isFinite(anchor.offset) ? anchor.offset : 0));
        state.isRestoringScroll = true;
        tableContainer.scrollTop = targetTop;
        if (Number.isFinite(anchor.scrollLeft)) {
            tableContainer.scrollLeft = anchor.scrollLeft;
        }
        row.classList.toggle('scroll-anchor-restored', options.flash !== false);
        setTimeout(() => {
            row.classList.remove('scroll-anchor-restored');
            state.isRestoringScroll = false;
        }, 350);
    });
}

function cssEscape(value) {
    if (window.CSS?.escape) return CSS.escape(String(value));
    return String(value).replace(/["\\]/g, '\\$&');
}

function initDialogActionButtons() {
    [
        ['settingsPanel', 'closeSettings'],
        ['inspectorPanel', 'closeInspector']
    ].forEach(([panelId, closeId]) => {
        const closeButton = document.getElementById(closeId);
        if (!closeButton || closeButton.dataset.normalizedDialogActions) return;
        closeButton.classList.add('dialog-close-btn');
        closeButton.innerHTML = '&times;';
        const menuButton = document.createElement('button');
        menuButton.type = 'button';
        menuButton.className = 'btn btn-icon dialog-menu-btn';
        menuButton.title = 'More actions';
        menuButton.dataset.dialogMenu = panelId;
        menuButton.innerHTML = '&hellip;';
        closeButton.parentElement?.insertBefore(menuButton, closeButton);
        closeButton.dataset.normalizedDialogActions = '1';
    });
}

const ROUTES = {
    '/': { title: 'Patris Export', partial: '/partials/welcome' },
    '/viewer': { title: 'Patris Export - Live Data Viewer', viewer: true },
    '/debug/charmap': { title: 'Patris Export - Character Map Viewer', partial: '/partials/charmap' }
};

const MODAL_ROUTES = {
    settings: 'settingsPanel',
    columns: 'columnsPanel',
    connection: 'connectionPanel',
    logs: 'eventLogPanel'
};

function initRouter() {
    state.router.toolbar = document.querySelector('.toolbar');
    state.router.tableContainer = document.querySelector('.table-container');
    const main = document.querySelector('.main-content');
    if (main && !document.getElementById('routeOutlet')) {
        const outlet = document.createElement('div');
        outlet.id = 'routeOutlet';
        outlet.className = 'route-outlet';
        outlet.hidden = true;
        main.appendChild(outlet);
        state.router.outlet = outlet;
    } else {
        state.router.outlet = document.getElementById('routeOutlet');
    }

    document.addEventListener('click', handleRouterClick);
    window.addEventListener('popstate', () => applyCurrentRoute({ push: false }));
    window.addEventListener('hashchange', applyModalHash);
    document.addEventListener('keydown', event => {
        if (event.key === 'Escape' && activePanelId()) {
            event.preventDefault();
            closeRouteDialog();
        }
    });
    applyCurrentRoute({ push: false });
}

function handleRouterClick(event) {
    const anchor = event.target.closest('a[href]');
    if (!anchor || event.defaultPrevented || anchor.target || anchor.hasAttribute('download')) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    const url = new URL(anchor.href, window.location.href);
    if (url.origin !== window.location.origin) return;
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/static/') || url.pathname === '/ws') return;
    if (url.pathname in ROUTES) {
        event.preventDefault();
        stripInternalReloadParams(url);
        navigateTo(url.pathname + url.search + url.hash);
    }
}

function navigateTo(url, options = {}) {
    const { replace = false } = options;
    const next = stripInternalReloadParams(new URL(url, window.location.href));
    const current = visibleLocationBase() + window.location.hash;
    const target = next.pathname + next.search + next.hash;
    if (current !== target) {
        if (replace) {
            history.replaceState({ route: next.pathname }, '', target);
        } else {
            history.pushState({ route: next.pathname }, '', target);
        }
    }
    applyCurrentRoute({ push: false });
}

async function applyCurrentRoute() {
    const path = normalizeRoutePath(window.location.pathname);
    const route = ROUTES[path] || ROUTES['/viewer'];
    state.router.route = path;
    document.title = route.title || state.originalTitle;
    if (route.viewer) {
        showViewerRoute();
    } else {
        await showPartialRoute(route);
    }
    applyModalHash();
}

function normalizeRoutePath(path) {
    if (path === '' || path === '/') return '/';
    if (path.endsWith('/') && path.length > 1) return path.slice(0, -1);
    return path;
}

function showViewerRoute() {
    cancelPartialRoute();
    if (state.router.toolbar) state.router.toolbar.hidden = false;
    if (state.router.tableContainer) state.router.tableContainer.hidden = false;
    if (state.router.outlet) {
        state.router.outlet.hidden = true;
        state.router.outlet.innerHTML = '';
    }
    document.body.classList.remove('partial-route-active');
}

async function showPartialRoute(route) {
    if (!state.router.outlet || !route.partial) return;
    cancelPartialRoute();
    if (state.router.toolbar) state.router.toolbar.hidden = true;
    if (state.router.tableContainer) state.router.tableContainer.hidden = true;
    document.body.classList.add('partial-route-active');
    state.router.outlet.hidden = false;
    state.router.outlet.innerHTML = '<div class="route-loading">Loading...</div>';
    const controller = new AbortController();
    state.router.partialController = controller;
    try {
        const response = await fetch(route.partial, {
            headers: { 'X-Patris-Partial': '1' },
            signal: controller.signal,
            cache: 'no-store'
        });
        if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
        const html = await response.text();
        if (state.router.partialController !== controller) return;
        renderPartialHTML(html);
    } catch (error) {
        if (error.name === 'AbortError') return;
        state.router.outlet.innerHTML = `<div class="route-error"><strong>Could not load page.</strong><span>${escapeHtml(error.message)}</span></div>`;
    }
}

function cancelPartialRoute() {
    if (state.router.partialController) {
        state.router.partialController.abort();
        state.router.partialController = null;
    }
    state.router.partialScripts = [];
}

function renderPartialHTML(html) {
    const template = document.createElement('template');
    template.innerHTML = html;
    const scripts = [...template.content.querySelectorAll('script')];
    scripts.forEach(script => script.remove());
    state.router.outlet.replaceChildren(template.content.cloneNode(true));
    scripts.forEach(script => runPartialScript(script));
}

function runPartialScript(sourceScript) {
    const script = document.createElement('script');
    for (const attr of sourceScript.attributes) {
        script.setAttribute(attr.name, attr.value);
    }
    if (!sourceScript.src) {
        script.textContent = `(() => {
const __routeOutlet = window.document.currentScript?.closest('#routeOutlet') || window.document;
const document = new Proxy(window.document, {
    get(target, prop) {
        if (prop === 'getElementById') {
            return id => __routeOutlet.querySelector?.('#' + CSS.escape(id)) || target.getElementById(id);
        }
        if (prop === 'querySelector') {
            return selector => __routeOutlet.querySelector?.(selector) || target.querySelector(selector);
        }
        if (prop === 'querySelectorAll') {
            return selector => {
                const local = __routeOutlet.querySelectorAll?.(selector);
                return local && local.length ? local : target.querySelectorAll(selector);
            };
        }
        const value = target[prop];
        return typeof value === 'function' ? value.bind(target) : value;
    }
});
${sourceScript.textContent}
})();`;
    }
    state.router.outlet.appendChild(script);
    state.router.partialScripts.push(script);
}

function openModalRoute(name, options = {}) {
    if (!(name in MODAL_ROUTES)) return;
    const base = visibleLocationBase();
    if (options.replace) {
        history.replaceState(history.state, '', `${base}#${name}`);
    } else if (window.location.hash !== `#${name}`) {
        history.pushState(history.state, '', `${base}#${name}`);
    }
    applyModalHash();
}

function applyModalHash() {
    const name = window.location.hash.replace(/^#/, '').split(/[?&]/)[0];
    const panelId = MODAL_ROUTES[name];
    if (panelId) {
        if (panelId === 'columnsPanel') renderColumnManager();
        if (panelId === 'settingsPanel') applyConfigToSettingsForm();
        if (panelId === 'connectionPanel') renderConnectionPanel();
        if (panelId === 'eventLogPanel') renderEventLogPanel();
        openPanel(panelId);
        return;
    }
    if (!window.location.hash) {
        closePanels();
    }
}

function activePanelId() {
    return ['settingsPanel', 'columnsPanel', 'connectionPanel', 'eventLogPanel']
        .find(id => document.getElementById(id)?.classList.contains('open')) || '';
}

function closeRouteDialog() {
    closePanels();
    if (window.location.hash) {
        history.pushState(history.state, '', visibleLocationBase());
    }
}

function applyConfigToSettingsForm() {
    const cfg = state.config || {};
    setValue('settingsTheme', cfg.ui?.theme || localStorage.getItem('theme') || 'system');
    setValue('serverHost', cfg.server?.host || '');
    setValue('serverPort', cfg.server?.port || '');
    setChecked('serverWatch', cfg.server?.watch);
    setValue('serverDebounce', cfg.server?.debounce || '');
    setValue('databasePath', cfg.database?.path || '');
    setValue('databaseCharmap', cfg.database?.charmap || '');
    setChecked('directAccess', cfg.database?.direct_access);
    setChecked('rtlConversion', cfg.database?.rtl_conversion);
    setChecked('notifyEnabled', cfg.notifications?.enabled);
    setChecked('notifyNative', cfg.notifications?.native !== false);
    setChecked('notifyInApp', cfg.notifications?.in_app !== false);
    setChecked('notifyClientConnected', cfg.notifications?.client_connected);
    setChecked('notifyClientDisconnected', cfg.notifications?.client_disconnected);
    setChecked('notifyFileUpdated', cfg.notifications?.file_updated);
    setChecked('notifyRowUpdated', cfg.notifications?.row_updated);
    setChecked('notifyIncludeRowValues', cfg.notifications?.include_row_values);
    setValue('notifyMaxRows', cfg.notifications?.max_rows || 3);
    setValue('runtimeTempDir', cfg.runtime?.temp_dir || 'system');
    setValue('runtimeTempStrategy', cfg.runtime?.temp_strategy || 'auto');
    setValue('runtimeTempMemoryLimitMB', cfg.runtime?.temp_memory_limit_mb || 100);
    setChecked('runtimeDebug', cfg.runtime?.debug);
    const pathEl = document.getElementById('settingsConfigPath');
    if (pathEl) {
        pathEl.textContent = state.appInfo?.config_path ? `Config: ${state.appInfo.config_path}` : '';
    }
}

function updateConfigFromSettingsForm() {
    if (!state.config) return;
    state.config.server = {
        ...(state.config.server || {}),
        host: document.getElementById('serverHost')?.value.trim() || '127.0.0.1',
        port: parseInt(document.getElementById('serverPort')?.value || '0', 10) || 8080,
        watch: !!document.getElementById('serverWatch')?.checked,
        debounce: document.getElementById('serverDebounce')?.value.trim() || '0s'
    };
    state.config.database = {
        ...(state.config.database || {}),
        path: document.getElementById('databasePath')?.value.trim() || '',
        charmap: document.getElementById('databaseCharmap')?.value.trim() || '',
        direct_access: !!document.getElementById('directAccess')?.checked,
        rtl_conversion: !!document.getElementById('rtlConversion')?.checked
    };
    state.config.runtime = {
        ...(state.config.runtime || {}),
        temp_dir: document.getElementById('runtimeTempDir')?.value.trim() || 'system',
        temp_strategy: document.getElementById('runtimeTempStrategy')?.value || 'auto',
        temp_memory_limit_mb: parseInt(document.getElementById('runtimeTempMemoryLimitMB')?.value || '100', 10) || 100,
        debug: !!document.getElementById('runtimeDebug')?.checked
    };
    state.config.notifications = {
        ...(state.config.notifications || {}),
        enabled: !!document.getElementById('notifyEnabled')?.checked,
        native: !!document.getElementById('notifyNative')?.checked,
        in_app: !!document.getElementById('notifyInApp')?.checked,
        client_connected: !!document.getElementById('notifyClientConnected')?.checked,
        client_disconnected: !!document.getElementById('notifyClientDisconnected')?.checked,
        file_updated: !!document.getElementById('notifyFileUpdated')?.checked,
        row_updated: !!document.getElementById('notifyRowUpdated')?.checked,
        include_row_values: !!document.getElementById('notifyIncludeRowValues')?.checked,
        max_rows: parseInt(document.getElementById('notifyMaxRows')?.value || '3', 10) || 3
    };
    syncSettingsToConfig();
}

// Initialize notification audio
function initNotificationAudio() {
    // Simply store the URL for creating new instances
    // We'll create fresh Audio instances in playNotificationSound()
    state.notificationAudio = '/static/notification.ogg';
}

// Play notification sound
// Creates a new Audio instance for each notification to support overlapping sounds.
function playNotificationSound(force = false) {
    if (!force && !state.settings.playNotificationSound) {
        return;
    }

    if (state.settings.notificationSoundSource === 'generated' || !state.notificationAudio) {
        playGeneratedNotificationSound();
        return;
    }

    try {
        const audio = new Audio(state.notificationAudio);
        audio.volume = 0.5;

        audio.play().catch(err => {
            console.log('Could not play notification sound, using generated melody:', err);
            playGeneratedNotificationSound();
        });
    } catch (err) {
        console.warn('Failed to create notification audio, using generated melody:', err);
        playGeneratedNotificationSound();
    }
}

function playGeneratedNotificationSound() {
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    if (!AudioContextClass) {
        return;
    }

    const audioContext = new AudioContextClass();
    const masterGain = audioContext.createGain();
    masterGain.gain.setValueAtTime(0.0001, audioContext.currentTime);
    masterGain.gain.exponentialRampToValueAtTime(0.18, audioContext.currentTime + 0.015);
    masterGain.gain.exponentialRampToValueAtTime(0.0001, audioContext.currentTime + 0.62);
    masterGain.connect(audioContext.destination);

    const notes = [
        { frequency: 659.25, start: 0.00, duration: 0.13 },
        { frequency: 783.99, start: 0.12, duration: 0.13 },
        { frequency: 987.77, start: 0.24, duration: 0.16 },
        { frequency: 1318.51, start: 0.40, duration: 0.18 }
    ];

    notes.forEach(note => {
        const oscillator = audioContext.createOscillator();
        const noteGain = audioContext.createGain();
        const startTime = audioContext.currentTime + note.start;
        const endTime = startTime + note.duration;

        oscillator.type = 'triangle';
        oscillator.frequency.setValueAtTime(note.frequency, startTime);
        noteGain.gain.setValueAtTime(0.0001, startTime);
        noteGain.gain.exponentialRampToValueAtTime(0.5, startTime + 0.015);
        noteGain.gain.exponentialRampToValueAtTime(0.0001, endTime);

        oscillator.connect(noteGain);
        noteGain.connect(masterGain);
        oscillator.start(startTime);
        oscillator.stop(endTime + 0.02);
    });

    setTimeout(() => {
        audioContext.close().catch(() => {});
    }, 800);
}

// Flash page title with notification info
function flashTitle(message) {
    // Clear any existing flash interval to avoid conflicts
    if (state.titleFlashInterval) {
        clearInterval(state.titleFlashInterval);
        document.title = state.originalTitle;
    }
    
    const originalTitle = state.originalTitle;
    let flashCount = 0;
    const maxFlashes = 6; // Flash 3 times (on/off cycle)
    
    state.titleFlashInterval = setInterval(() => {
        document.title = flashCount % 2 === 0 ? `🔔 ${message}` : originalTitle;
        flashCount++;
        
        if (flashCount >= maxFlashes) {
            clearInterval(state.titleFlashInterval);
            state.titleFlashInterval = null;
            document.title = originalTitle;
        }
    }, 500); // Flash every 500ms
}

// Change favicon temporarily
function flashFavicon() {
    // Clear any existing timeout to prevent overlapping flashes
    if (state.faviconTimeout) {
        clearTimeout(state.faviconTimeout);
        state.faviconTimeout = null;
    }
    
    // Store original favicon if not already stored
    if (!state.originalFavicon) {
        const existing = document.querySelector('link[rel="icon"]');
        if (existing) {
            state.originalFavicon = existing.href;
        }
    }
    
    // Create notification favicon (red circle with white dot)
    const canvas = document.createElement('canvas');
    canvas.width = 32;
    canvas.height = 32;
    const ctx = canvas.getContext('2d');
    if (!ctx) {
        return;
    }
    
    // Draw red circle background
    ctx.fillStyle = '#ff4444';
    ctx.beginPath();
    ctx.arc(16, 16, 16, 0, 2 * Math.PI);
    ctx.fill();
    
    // Draw white dot in center
    ctx.fillStyle = '#ffffff';
    ctx.beginPath();
    ctx.arc(16, 16, 6, 0, 2 * Math.PI);
    ctx.fill();
    
    // Set as favicon
    const notificationFavicon = canvas.toDataURL('image/png');
    setFavicon(notificationFavicon);
    
    // Restore original favicon after 2 seconds
    state.faviconTimeout = setTimeout(() => {
        if (state.originalFavicon) {
            setFavicon(state.originalFavicon);
        }
        state.faviconTimeout = null;
    }, 2000);
}

// Helper to set favicon
function setFavicon(href) {
    let link = document.querySelector('link[rel="icon"]');
    if (!link) {
        link = document.createElement('link');
        link.rel = 'icon';
        document.head.appendChild(link);
    }
    link.href = href;
}

function showInAppToast(title, message, options = {}) {
    if (options.log !== false) {
        recordEventLog({
            title: title || 'Patris Export',
            message: message || '',
            level: options.error || options.nativeError ? 'warning' : (options.level || 'info'),
            type: options.eventType || options.source || 'toast',
            source: options.source || 'web-ui',
            timestamp: options.timestamp,
            details: options.nativeError || options.details || ''
        });
    }

    let container = document.getElementById('toastContainer');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toastContainer';
        container.className = 'toast-container';
        document.body.appendChild(container);
    }

    const toast = document.createElement('div');
    toast.className = 'app-toast';
    if (options.error) {
        toast.classList.add('warning');
    }

    const titleEl = document.createElement('div');
    titleEl.className = 'app-toast-title';
    titleEl.textContent = title || 'Patris Export';

    const messageEl = document.createElement('div');
    messageEl.className = 'app-toast-message';
    messageEl.textContent = message || '';

    toast.appendChild(titleEl);
    toast.appendChild(messageEl);
    container.appendChild(toast);

    setTimeout(() => {
        toast.classList.add('closing');
        setTimeout(() => toast.remove(), 180);
    }, options.duration || 4200);

    if (options.broadcastToTabs) {
        publishFrontendBroadcast('toast', {
            title,
            message,
            options: { ...options, broadcastToTabs: false }
        });
    }
}

function recordEventLog(entry = {}) {
    const logEntry = {
        id: entry.id || createTabId(),
        time: entry.timestamp || new Date().toISOString(),
        level: normalizeEventLevel(entry.level),
        type: String(entry.type || 'event'),
        source: String(entry.source || entry.type || 'web-ui'),
        title: String(entry.title || 'Patris Export event'),
        message: String(entry.message || ''),
        details: entry.details ? String(entry.details) : ''
    };
    state.eventLog.unshift(logEntry);
    state.eventLog = state.eventLog.slice(0, MAX_EVENT_LOG_ENTRIES);
    saveEventLog();
    renderEventLogCount();
    renderEventLogPanel();
}

function normalizeEventLevel(level) {
    if (['error', 'warning', 'success', 'update', 'info'].includes(level)) {
        return level;
    }
    return 'info';
}

function saveEventLog() {
    try {
        localStorage.setItem(EVENT_LOG_STORAGE_KEY, JSON.stringify(state.eventLog));
    } catch (error) {
        console.warn('Failed to persist event log:', error);
    }
}

function clearEventLog(options = {}) {
    state.eventLog = [];
    saveEventLog();
    renderEventLogCount();
    renderEventLogPanel();
    if (options.broadcast !== false) {
        publishFrontendBroadcast('event-log:clear');
    }
}

function renderEventLogCount() {
    const count = document.getElementById('eventLogCount');
    if (count) {
        count.textContent = state.eventLog.length.toLocaleString();
        count.hidden = state.eventLog.length === 0;
    }
}

function renderEventLogPanel() {
    const summary = document.getElementById('eventLogSummary');
    const list = document.getElementById('eventLogList');
    if (!summary || !list) return;

    const counts = state.eventLog.reduce((acc, entry) => {
        acc[entry.level] = (acc[entry.level] || 0) + 1;
        return acc;
    }, {});
    summary.innerHTML = `
        <div><span>Total</span><strong>${state.eventLog.length.toLocaleString()}</strong></div>
        <div><span>Updates</span><strong>${(counts.update || 0).toLocaleString()}</strong></div>
        <div><span>Warnings</span><strong>${((counts.warning || 0) + (counts.error || 0)).toLocaleString()}</strong></div>
    `;

    if (state.eventLog.length === 0) {
        list.innerHTML = '<div class="event-log-empty">No notification-capable events have been captured yet.</div>';
        return;
    }

    list.innerHTML = state.eventLog.map(entry => `
        <article class="event-log-entry ${escapeHtml(entry.level)}">
            <div class="event-log-entry-meta">
                <time>${formatDateTime(new Date(entry.time))}</time>
                <span>${escapeHtml(entry.type)}</span>
                <span>${escapeHtml(entry.source)}</span>
            </div>
            <div class="event-log-entry-body">
                <strong>${escapeHtml(entry.title)}</strong>
                ${entry.message ? `<p>${escapeHtml(entry.message)}</p>` : ''}
                ${entry.details ? `<code>${escapeHtml(entry.details)}</code>` : ''}
            </div>
        </article>
    `).join('');
}

function isSupportedSourceFile(file) {
    return !!file && /\.(db|json)$/i.test(file.name || '');
}

function supportedSourceFromTransfer(dataTransfer) {
    const files = Array.from(dataTransfer?.files || []);
    return files.find(isSupportedSourceFile) || files[0] || null;
}

function setDropOverlayVisible(visible, mode = 'ready') {
    const overlay = document.getElementById('dropOverlay');
    if (!overlay) {
        return;
    }
    overlay.classList.toggle('visible', visible);
    overlay.classList.toggle('uploading', mode === 'uploading');
    overlay.setAttribute('aria-hidden', visible ? 'false' : 'true');
    const title = overlay.querySelector('[data-drop-title]');
    const message = overlay.querySelector('[data-drop-message]');
    if (title && message) {
        if (mode === 'uploading') {
            title.textContent = 'Loading source...';
            message.textContent = 'Uploading the file and switching connected viewers.';
        } else if (mode === 'invalid') {
            title.textContent = 'Unsupported file';
            message.textContent = 'Drop a .db or .json file to switch the active source.';
        } else {
            title.textContent = 'Drop database file';
            message.textContent = 'Release a .db or .json file to load it in this viewer.';
        }
    }
}

async function uploadDroppedSource(file) {
    if (!file) {
        return;
    }
    if (!isSupportedSourceFile(file)) {
        setDropOverlayVisible(true, 'invalid');
        showInAppToast('Unsupported file', 'Drop a .db or .json file to switch the active source.', { error: true, source: 'source_drop', eventType: 'source_switch' });
        setTimeout(() => setDropOverlayVisible(false), 1500);
        return;
    }
    if (state.isUploadingSource) {
        return;
    }

    state.isUploadingSource = true;
    setDropOverlayVisible(true, 'uploading');
    setLoadingState(true);

    try {
        const form = new FormData();
        form.append('source_id', 'viewer-drop');
        form.append('file_name', file.name);
        form.append('size', String(file.size));
        form.append('mod_time', new Date(file.lastModified || Date.now()).toISOString());
        form.append('file', file, file.name);

        const response = await fetch('/api/edge/upload', {
            method: 'POST',
            body: form
        });
        const text = await response.text();
        let payload = {};
        try {
            payload = text ? JSON.parse(text) : {};
        } catch (error) {
            payload = { message: text };
        }
        if (!response.ok || payload.success === false) {
            throw new Error(payload.message || `Upload failed with HTTP ${response.status}`);
        }

        state.fileName = payload.path || payload.file || file.name;
        updateFooterFileName();
        showInAppToast('Source loaded', `${payload.file || file.name} is now active (${payload.records ?? 'unknown'} records).`, { broadcastToTabs: true, source: 'source_drop', eventType: 'source_switch', level: 'success' });

        if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
            await fetchInitialData();
            await fetchFileInfo();
        }
    } catch (error) {
        console.error('Failed to switch dropped source:', error);
        showInAppToast('Source switch failed', error.message, { error: true, source: 'source_drop', eventType: 'source_switch' });
        setLoadingState(false);
    } finally {
        state.isUploadingSource = false;
        state.dragDepth = 0;
        setDropOverlayVisible(false);
    }
}

function initSourceDragDrop() {
    const hasFiles = event => Array.from(event.dataTransfer?.types || []).includes('Files');

    window.addEventListener('dragenter', event => {
        if (!hasFiles(event)) {
            return;
        }
        event.preventDefault();
        state.dragDepth += 1;
        const file = supportedSourceFromTransfer(event.dataTransfer);
        setDropOverlayVisible(true, file && !isSupportedSourceFile(file) ? 'invalid' : 'ready');
    });

    window.addEventListener('dragover', event => {
        if (!hasFiles(event)) {
            return;
        }
        event.preventDefault();
        event.dataTransfer.dropEffect = 'copy';
        const file = supportedSourceFromTransfer(event.dataTransfer);
        setDropOverlayVisible(true, file && !isSupportedSourceFile(file) ? 'invalid' : 'ready');
    });

    window.addEventListener('dragleave', event => {
        if (!hasFiles(event)) {
            return;
        }
        state.dragDepth = Math.max(0, state.dragDepth - 1);
        if (state.dragDepth === 0 && !state.isUploadingSource) {
            setDropOverlayVisible(false);
        }
    });

    window.addEventListener('drop', event => {
        if (!hasFiles(event)) {
            return;
        }
        event.preventDefault();
        state.dragDepth = 0;
        uploadDroppedSource(supportedSourceFromTransfer(event.dataTransfer));
    });
}

function openPanel(panelId) {
    closePanels();
    document.getElementById(panelId).classList.add('open');
    document.getElementById('panelBackdrop').classList.add('open');
}

function closePanels() {
    ['settingsPanel', 'columnsPanel', 'connectionPanel', 'eventLogPanel'].forEach(id => {
        const panel = document.getElementById(id);
        if (panel) panel.classList.remove('open');
    });
    const backdrop = document.getElementById('panelBackdrop');
    if (backdrop) backdrop.classList.remove('open');
}

function initSettingsTabs() {
    document.querySelectorAll('[data-settings-tab]').forEach(tab => {
        tab.addEventListener('click', () => {
            setActiveSettingsTab(tab.dataset.settingsTab);
        });
    });
}

function setActiveSettingsTab(target) {
    const tab = document.querySelector(`[data-settings-tab="${target}"]`) || document.querySelector('[data-settings-tab="ui"]');
    const paneName = tab?.dataset.settingsTab || 'ui';
    document.querySelectorAll('[data-settings-tab]').forEach(item => {
        item.classList.toggle('active', item === tab);
    });
    document.querySelectorAll('[data-settings-pane]').forEach(pane => {
        pane.classList.toggle('active', pane.dataset.settingsPane === paneName);
    });
}

function bindConfigField(id, eventName = 'change') {
    const el = document.getElementById(id);
    if (!el) return;
    el.addEventListener(eventName, updateConfigFromSettingsForm);
}

async function fetchAppInfo(options = {}) {
    const { source = 'api', log = true } = options;
    try {
        const response = await fetch('/api/app', { cache: 'no-store' });
        if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
        const appInfo = await response.json();
        const applied = applyAppInfo(appInfo, source);
        if (applied && log) {
            logIntro();
        }
    } catch (error) {
        console.error('❌ Failed to fetch app metadata:', error);
        if (log) {
            logIntro();
        }
    }
}

function applyAppInfo(appInfo, source = 'api') {
    if (!appInfo) return false;

    const nextResourceVersion = getResourceVersion(appInfo);
    if (nextResourceVersion && state.resourceVersion && nextResourceVersion !== state.resourceVersion) {
        if (source !== 'tab broadcast') {
            publishFrontendBroadcast('resource:update', { version: nextResourceVersion });
        }
        reloadForResourceUpdate(nextResourceVersion, source);
        return false;
    }

    state.appInfo = {
        ...(state.appInfo || {}),
        ...appInfo
    };
    if (nextResourceVersion && !state.resourceVersion) {
        state.resourceVersion = nextResourceVersion;
    }
    updateAppMetadata();
    applyConfigToSettingsForm();
    if (source !== 'tab broadcast') {
        publishFrontendBroadcast('app-info:update', state.appInfo);
    }
    return true;
}

function getResourceVersion(appInfo) {
    return appInfo?.resources?.version || appInfo?.resource_version || '';
}

function startResourceVersionPolling() {
    if (state.resourcePollTimer) {
        clearInterval(state.resourcePollTimer);
    }
    state.resourcePollTimer = setInterval(() => {
        if (document.visibilityState === 'hidden' || state.isReloadingForUpdate) {
            return;
        }
        fetchAppInfo({ source: 'poll', log: false });
    }, RESOURCE_POLL_INTERVAL_MS);
}

function reloadForResourceUpdate(nextResourceVersion, source) {
    if (state.isReloadingForUpdate) return;
    state.isReloadingForUpdate = true;

    console.info('🔄 Embedded web resources changed from %s to %s via %s. Reloading viewer...', state.resourceVersion, nextResourceVersion, source);
    updateStatus('connected', 'Updating UI...');
    showInAppToast('Updating interface', 'A newer embedded web UI is available. Reloading now.', { source: 'resource_update', eventType: 'resource_update', level: 'update' });

    sessionStorage.setItem('patris-resource-reload', JSON.stringify({
        from: state.resourceVersion,
        to: nextResourceVersion,
        source,
        reloadedAt: new Date().toISOString()
    }));

    setTimeout(() => {
        window.location.reload();
    }, 350);
}

async function fetchProcessStatus() {
    try {
        const response = await fetch('/api/status');
        if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
        applyProcessStatus(await response.json());
    } catch (error) {
        console.error('❌ Failed to fetch process status:', error);
    }
}

function updateAppMetadata() {
    const logo = document.getElementById('appLogo');
    const title = document.getElementById('appTitle');
    const version = state.appInfo?.version || {};
    const resources = getResourceVersion(state.appInfo);
    const resourceText = resources ? ` resources ${resources}` : '';
    const text = `Patris Export ${version.version || ''} (${version.commit || 'unknown'})${resourceText}`;
    if (logo) logo.title = text;
    if (title) title.title = text;
}

function applyProcessStatus(status) {
    if (!status) return;
    state.processStatus = status.status || status;
    const patris = state.processStatus.patris81 || {};
    const fileAccess = state.processStatus.file_access || {};
    const el = document.getElementById('processStatus');
    if (el) {
        const patrisText = patris.running ? `Patris81 running (${patris.count})` : 'Patris81 not running';
        const lockText = fileAccess.in_use ? `DB locked (${fileAccess.count})` : 'DB unlocked';
        el.textContent = `${patrisText} · ${lockText}`;
        el.classList.toggle('warning', !!patris.running || !!fileAccess.in_use);
    }
}

async function sendNativeToast(title, message) {
    try {
        const response = await fetch('/api/toast', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title, message })
        });
        const result = await response.json();
        if (result.native_error) {
            showInAppToast('Native toast unavailable', result.native_error, { error: true, source: 'native_toast', eventType: 'toast', nativeError: result.native_error });
        }
    } catch (error) {
        showInAppToast('Toast request failed', error.message, { error: true, source: 'native_toast', eventType: 'toast' });
    }
}

async function requestSourceRefresh() {
    const button = document.getElementById('refreshNowBtn');
    if (button) {
        button.disabled = true;
        button.textContent = 'Refreshing...';
    }
    try {
        if (state.ws && state.ws.readyState === WebSocket.OPEN) {
            state.ws.send(JSON.stringify({ type: 'refresh' }));
            showInAppToast('Refresh requested', 'The backend is reloading the data source.', { broadcastToTabs: true, source: 'manual_refresh', eventType: 'refresh', level: 'update' });
        } else {
            await fetchInitialData();
            showInAppToast('Refreshed', 'Data was reloaded over HTTP.', { broadcastToTabs: true, source: 'manual_refresh', eventType: 'refresh', level: 'success' });
        }
    } catch (error) {
        console.error('Failed to refresh data source:', error);
        showInAppToast('Refresh failed', error.message, { error: true, source: 'manual_refresh', eventType: 'refresh' });
    } finally {
        if (button) {
            setTimeout(() => {
                button.disabled = false;
                button.textContent = '🔄 Refresh Now';
            }, 500);
        }
    }
}

// Initialize WebSocket connection
function initWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;
    
    state.ws = new WebSocket(wsUrl);
    
    state.ws.onopen = () => {
        console.log('WebSocket connected');
        updateStatus('connected', 'Connected');
    };
    
    state.ws.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data);
            handleWebSocketMessage(data);
        } catch (error) {
            console.error('Failed to parse WebSocket message:', error);
        }
    };
    
    state.ws.onerror = (error) => {
        console.error('WebSocket error:', error);
        updateStatus('disconnected', 'Error');
    };
    
    state.ws.onclose = () => {
        console.log('WebSocket disconnected');
        updateStatus('disconnected', 'Disconnected');
        // Attempt to reconnect after 3 seconds
        setTimeout(initWebSocket, 3000);
    };
}

// Handle WebSocket messages
function handleWebSocketMessage(data) {
    const changedIndices = new Set();
    
    if (data.type === 'initial') {
        if (data.version || data.resources) {
            const metadata = {};
            if (data.version) metadata.version = data.version;
            if (data.resources) metadata.resources = data.resources;
            const applied = applyAppInfo(metadata, 'websocket');
            if (!applied && state.isReloadingForUpdate) {
                return;
            }
        }
        if (data.config) {
            applyConfig(data.config, 'websocket');
        }
        if (data.status) {
            applyProcessStatus(data.status);
        }
        // Initial load - records are already transformed with ANBAR as array
        state.records = data.added || [];
        state.filteredRecords = [];
        state.fields = [];
        state.fieldTypes = {};
        state.fieldStats = {};
        if (data.source_changed) {
            state.columnFilters = {};
            localStorage.removeItem('patris-column-filters');
        }
        
        // Store file name if provided
        if (data.file_path || data.file_name) {
            state.fileName = data.file_path || data.file_name;
            updateFooterFileName();
        }
        
        // Mark all as changed for initial highlight
        if (state.settings.highlightChanges) {
            state.records.forEach((_, index) => changedIndices.add(index));
        }
    } else if (data.type === 'update') {
        // Incremental update
        
        // Update footer timestamp
        updateFooterLastUpdate();
        
        // Track changes for notification
        let totalChanges = 0;
        let changeDescription = '';
        
        // Handle deleted records (by Code)
        if (data.deleted && data.deleted.length > 0) {
            const deletedCodes = new Set(data.deleted.map(String));
            state.records = state.records.filter(record => {
                const code = String(record.Code);
                return !deletedCodes.has(code);
            });
            totalChanges += data.deleted.length;
            changeDescription = `${data.deleted.length} deleted`;
        }
        
        // Handle added records
        if (data.added && data.added.length > 0) {
            const startIndex = state.records.length;
            state.records.push(...data.added);
            
            // Mark added records as changed
            data.added.forEach((_, i) => {
                changedIndices.add(startIndex + i);
            });
            totalChanges += data.added.length;
            if (changeDescription) changeDescription += ', ';
            changeDescription += `${data.added.length} added`;
        }
        
        // Handle modified records (if any)
        if (data.modified && data.modified.length > 0) {
            data.modified.forEach(change => {
                const code = String(change.code);
                const index = state.records.findIndex(r => String(r.Code) === code);
                if (index !== -1) {
                    // Merge the new values into the existing record
                    // Note: The server sends new_values (snake_case) not newValues (camelCase)
                    const newValues = change.new_values || change.newValues || {};
                    Object.assign(state.records[index], newValues);
                    changedIndices.add(index);
                    console.log(`Updated record ${code}:`, newValues);
                }
            });
            totalChanges += data.modified.length;
            if (changeDescription) changeDescription += ', ';
            changeDescription += `${data.modified.length} modified`;
        }
        
        // Trigger notifications if there were changes
        if (totalChanges > 0) {
            // Play notification sound
            playNotificationSound();
            
            // Flash title with change info (use detailed description if available)
            // Note: Title and favicon flashing always occur regardless of audio settings
            // This is by design to provide visual feedback even when sound is disabled
            const titleMessage = changeDescription || `${totalChanges} record${totalChanges > 1 ? 's' : ''} updated`;
            flashTitle(titleMessage);
            
            // Flash favicon
            flashFavicon();

            recordEventLog({
                title: 'Rows changed',
                message: titleMessage,
                level: 'update',
                type: 'row_updated',
                source: 'websocket',
                details: `added=${data.added?.length || 0} modified=${data.modified?.length || 0} deleted=${data.deleted?.length || 0}`
            });
        }
    } else if (data.type === 'toast') {
        showInAppToast(data.title, data.message, {
            error: !!data.native_error,
            source: data.source || 'server',
            eventType: data.source || 'toast',
            timestamp: data.timestamp,
            nativeError: data.native_error
        });
        if (data.native_error) {
            console.warn('Native toast failed:', data.native_error);
        }
    } else if (data.type === 'config_update') {
        const diff = applyConfig(data.config, 'file watcher');
        if (shouldNotifyConfigReload(diff)) {
            showInAppToast('Settings reloaded', diff.message || 'Configuration file changes were applied.', {
                source: 'config_update',
                eventType: 'config_update',
                level: 'update',
                details: diff.details
            });
        }
    } else if (data.type === 'process_info') {
        applyProcessStatus(data);
    }
    
    // Extract fields from first record if not already set
    if (state.records.length > 0 && state.fields.length === 0) {
        extractFields();
        analyzeFields(); // Analyze field types and stats
        renderTableHeader();
        updateFieldFilter();
    }
    
    filterRecords();
    sortRecords();
    renderTable(changedIndices);
    updateCounts();
    state.isInitialLoad = false;
    setLoadingState(false);
}

// Update connection status
function updateStatus(status, text) {
    const indicator = document.getElementById('statusIndicator');
    const statusText = document.getElementById('statusText');
    const footerIndicator = document.getElementById('footerStatusIndicator');
    
    state.connectionStatus = { state: status, text };
    if (indicator) indicator.className = 'status-indicator ' + status;
    if (footerIndicator) footerIndicator.className = 'status-indicator ' + status;
    if (statusText) statusText.textContent = text;
    
    // Update footer connection status
    updateFooterConnection(text);
    addConnectionLog(status, text);
}

// Update footer information
function updateFooter() {
    // Update file name
    updateFooterFileName();
    
    // Update last update time
    updateFooterLastUpdate();
    
    // Update record count
    updateFooterRecordCount();
}

function updateFooterFileName() {
    const footerFile = document.getElementById('footerFile');
    const headerFile = document.getElementById('headerFile');
    const headerFileChip = document.getElementById('headerFileChip');
    // Use basename of the file (just the file name, not full path)
    if (state.fileName) {
        const baseName = state.fileName.split('/').pop().split('\\').pop();
        if (footerFile) {
            footerFile.textContent = baseName;
            footerFile.title = state.fileName;
        }
        if (headerFile) {
            headerFile.textContent = baseName;
            headerFile.title = state.fileName;
        }
        if (headerFileChip) headerFileChip.title = state.fileName;
    } else {
        if (footerFile) {
            footerFile.textContent = 'Unknown';
            footerFile.removeAttribute('title');
        }
        if (headerFile) {
            headerFile.textContent = 'Unknown';
            headerFile.removeAttribute('title');
        }
        if (headerFileChip) headerFileChip.title = 'Current source file';
    }
}

// Format date as Y/m/d H:i:s (e.g., 2025/12/17 07:45:30)
function formatDateTime(date) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    const seconds = String(date.getSeconds()).padStart(2, '0');
    return `${year}/${month}/${day} ${hours}:${minutes}:${seconds}`;
}

function escapeHtml(value) {
    return String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function updateFooterLastUpdate(timestamp) {
    state.lastUpdateAt = timestamp ? new Date(timestamp) : new Date();
    updateLastUpdateDisplay();
}

function updateFooterRecordCount() {
    const footerRecordCount = document.getElementById('footerRecordCount');
    footerRecordCount.textContent = state.records.length.toLocaleString();
}

function updateFooterConnection(status) {
    const footerConnection = document.getElementById('footerConnection');
    if (footerConnection) footerConnection.textContent = status;
}

function addConnectionLog(level, message, details = '') {
    state.connectionLog.unshift({
        level,
        message,
        details,
        time: new Date().toISOString()
    });
    state.connectionLog = state.connectionLog.slice(0, 80);
    renderConnectionPanel();
}

function renderConnectionPanel() {
    const details = document.getElementById('connectionDetails');
    const log = document.getElementById('connectionLog');
    if (!details || !log) return;
    const wsState = state.ws ? ['Connecting', 'Open', 'Closing', 'Closed'][state.ws.readyState] || 'Unknown' : 'Not started';
    const file = state.fileName || 'Unknown';
    const patris = state.processStatus?.patris81 || {};
    const fileAccess = state.processStatus?.file_access || {};
    details.innerHTML = `
        <div><span>Status</span><strong>${escapeHtml(state.connectionStatus.text)}</strong></div>
        <div><span>WebSocket</span><strong>${escapeHtml(wsState)}</strong></div>
        <div><span>Source</span><strong title="${escapeHtml(file)}">${escapeHtml(file.split('/').pop().split('\\').pop() || file)}</strong></div>
        <div><span>Patris81</span><strong>${patris.running ? `Running (${patris.count || 1})` : 'Not running'}</strong></div>
        <div><span>Database lock</span><strong>${fileAccess.in_use ? `Locked (${fileAccess.count || 1})` : 'Unlocked'}</strong></div>
    `;
    log.innerHTML = state.connectionLog.map(entry => `
        <div class="connection-log-entry ${escapeHtml(entry.level)}">
            <time>${formatDateTime(new Date(entry.time))}</time>
            <strong>${escapeHtml(entry.message)}</strong>
            ${entry.details ? `<span>${escapeHtml(entry.details)}</span>` : ''}
        </div>
    `).join('');
}

function openConnectionPanel() {
    renderConnectionPanel();
    openPanel('connectionPanel');
}

// Extract and organize fields from records
function extractFields() {
    if (state.records.length === 0) return;
    
    const firstRecord = state.records[0];
    const allFields = Object.keys(firstRecord);
    
    // Separate ANBAR array from other fields
    const nonAnbarFields = allFields.filter(f => f !== 'ANBAR');
    
    // If ANBAR is an array, create separate ANBAR1, ANBAR2, etc. columns
    if (firstRecord.ANBAR && Array.isArray(firstRecord.ANBAR)) {
        const anbarLength = firstRecord.ANBAR.length;
        const anbarFields = [];
        for (let i = 0; i < anbarLength; i++) {
            anbarFields.push(`ANBAR${i + 1}`);
        }
        
        // Ensure Code is first, Name is second (if it exists), then other fields, then ANBAR columns
        const otherFields = nonAnbarFields.filter(f => f !== 'Code' && f !== 'Name');
        if (nonAnbarFields.includes('Name')) {
            state.fields = ['Code', 'Name', ...otherFields, ...anbarFields];
        } else {
            state.fields = ['Code', ...otherFields, ...anbarFields];
        }
    } else {
        // Ensure Code is first, Name is second (if it exists)
        const otherFields = nonAnbarFields.filter(f => f !== 'Code' && f !== 'Name');
        if (nonAnbarFields.includes('Name')) {
            state.fields = ['Code', 'Name', ...otherFields];
        } else {
            state.fields = ['Code', ...otherFields];
        }
    }
    applyColumnOrder();
}

function applyColumnOrder() {
    if (!Array.isArray(state.columnOrder) || state.columnOrder.length === 0 || state.fields.length === 0) return;
    const available = new Set(state.fields);
    const ordered = state.columnOrder.filter(field => available.has(field));
    const missing = state.fields.filter(field => !ordered.includes(field));
    state.fields = [...ordered, ...missing];
    ensureCodeFirst();
}

// Ensure Code column is always first
function ensureCodeFirst() {
    const codeIndex = state.fields.indexOf('Code');
    if (codeIndex > 0) {
        // Remove Code from its current position
        state.fields.splice(codeIndex, 1);
        // Add Code to the beginning
        state.fields.unshift('Code');
    }
}

// Render table header with filters
function renderTableHeader() {
    const thead = document.getElementById('tableHead');
    thead.innerHTML = '';

    const visibleFields = state.fields.filter(field => !state.hiddenColumns.has(field));
    const visibleAnbarFields = visibleFields.filter(field => isAnbarField(field));
    const hasAnbarFields = visibleAnbarFields.length > 0;

    const groupRow = document.createElement('tr');
    groupRow.className = 'anbar-group-row';
    const columnRow = document.createElement('tr');
    columnRow.className = hasAnbarFields ? 'column-header-row has-anbar-group' : 'column-header-row';
    const filterRow = document.createElement('tr');
    filterRow.className = 'filter-row';

    let processedAnbar = false;

    visibleFields.forEach(field => {
        // Handle ANBAR grouped columns
        if (isAnbarField(field) && !processedAnbar) {
            if (visibleAnbarFields.length > 0 && hasAnbarFields) {
                const groupTh = document.createElement('th');
                groupTh.textContent = displayStockGroupName();
                groupTh.setAttribute('colspan', visibleAnbarFields.length);
                groupTh.className = 'anbar-group-header';
                groupRow.appendChild(groupTh);

                // Create individual ANBAR column headers and filters
                visibleAnbarFields.forEach(anbarField => {
                    const anbarNum = anbarField.substring(5); // Extract number
                    const label = anbarNum;
                    const th = createHeaderCell(anbarField, { label, className: 'anbar-column' });
                    columnRow.appendChild(th);
                    
                    // Create filter cell for this ANBAR field
                    const filterTh = document.createElement('th');
                    filterTh.appendChild(createFilterControl(anbarField));
                    filterRow.appendChild(filterTh);
                });
            }
            
            processedAnbar = true;
        } else if (!isAnbarField(field)) {
            // Regular field header
            const th = createHeaderCell(field, {
                label: displayFieldName(field),
                rowSpan: hasAnbarFields ? 2 : 1,
                className: field === 'Code' ? 'sticky-column' : ''
            });
            groupRow.appendChild(th);
            
            // Create filter cell for this field
            const filterTh = document.createElement('th');
            if (field === 'Code') {
                filterTh.classList.add('sticky-column');
            }
            filterTh.appendChild(createFilterControl(field));
            filterRow.appendChild(filterTh);
        }
    });
    
    // Add actions column
    const actionsHeader = document.createElement('th');
    actionsHeader.textContent = 'Actions';
    actionsHeader.style.width = '100px';
    if (hasAnbarFields) {
        actionsHeader.setAttribute('rowspan', '2');
    }
    groupRow.appendChild(actionsHeader);
    
    const actionsFilter = document.createElement('th');
    actionsFilter.style.width = '100px';
    // Add clear all filters button
    const clearBtn = document.createElement('button');
    clearBtn.className = 'btn-clear-filters';
    clearBtn.textContent = '✕ Clear';
    clearBtn.title = 'Clear all filters';
    clearBtn.addEventListener('click', clearAllFilters);
    actionsFilter.appendChild(clearBtn);
    filterRow.appendChild(actionsFilter);

    thead.appendChild(groupRow);
    if (hasAnbarFields) {
        thead.appendChild(columnRow);
    }
    thead.appendChild(filterRow);
}

function isAnbarField(field) {
    return field.startsWith('ANBAR') && field.length > 5;
}

function createHeaderCell(field, { label, rowSpan = 1, className = '' } = {}) {
    const th = document.createElement('th');
    th.className = ['sortable', className].filter(Boolean).join(' ');
    if (rowSpan > 1) {
        th.setAttribute('rowspan', String(rowSpan));
    }

    const sortContainer = document.createElement('div');
    sortContainer.className = 'table-header-content';

    const fieldName = document.createElement('span');
    fieldName.textContent = label || displayFieldName(field);
    sortContainer.appendChild(fieldName);

    const sortIndicator = document.createElement('span');
    sortIndicator.className = 'sort-indicator';
    if (state.sortField === field) {
        sortIndicator.textContent = state.sortDirection === 'asc' ? '▲' : '▼';
        sortIndicator.style.opacity = '1';
    } else {
        sortIndicator.textContent = '▲';
        sortIndicator.style.opacity = '0.3';
    }
    sortContainer.appendChild(sortIndicator);

    th.appendChild(sortContainer);
    th.addEventListener('click', () => sortByField(field));
    return th;
}

// Create filter control based on field type
function createFilterControl(field) {
    const container = document.createElement('div');
    container.className = 'filter-control';
    
    const stats = state.fieldStats[field];
    if (!stats) return container;
    
    const fieldType = stats.type;
    const currentFilter = state.columnFilters[field];
    
    if (field === 'Code') {
        container.appendChild(createCodeFilterControl(currentFilter));
    } else if (fieldType === 'categorical') {
        // Dropdown for categorical fields
        const select = document.createElement('select');
        select.className = 'filter-select';
        select.setAttribute('aria-label', `Filter ${displayFieldName(field)}`);
        
        const defaultOption = document.createElement('option');
        defaultOption.value = '';
        defaultOption.textContent = 'All';
        select.appendChild(defaultOption);
        
        // Sort unique values
        const values = Array.from(stats.uniqueValues).sort((a, b) => {
            return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' });
        });
        
        values.forEach(value => {
            const option = document.createElement('option');
            option.value = value;
            option.textContent = value;
            if (currentFilter && currentFilter.value === value) {
                option.selected = true;
            }
            select.appendChild(option);
        });
        
        select.addEventListener('change', (e) => {
            if (e.target.value) {
                state.columnFilters[field] = { type: 'categorical', value: e.target.value };
            } else {
                delete state.columnFilters[field];
            }
            saveColumnFilters();
            applyFilters();
        });
        
        container.appendChild(select);
        
    } else if (fieldType === 'numeric') {
        container.appendChild(createRangePopover2(field, currentFilter, 'numeric'));
        
    } else if (fieldType === 'jalali-date') {
        // Date range for Jalali dates (YY.mm.dd format)
        const wrapper = document.createElement('div');
        wrapper.className = 'filter-range';
        
        const minInput = document.createElement('input');
        minInput.type = 'text';
        minInput.className = 'filter-input-small';
        minInput.placeholder = 'From';
        minInput.value = currentFilter?.min ?? '';
        minInput.title = 'Format: YY.mm.dd';
        minInput.setAttribute('aria-label', `Minimum ${displayFieldName(field)} date`);
        
        const maxInput = document.createElement('input');
        maxInput.type = 'text';
        maxInput.className = 'filter-input-small';
        maxInput.placeholder = 'To';
        maxInput.value = currentFilter?.max ?? '';
        maxInput.title = 'Format: YY.mm.dd';
        maxInput.setAttribute('aria-label', `Maximum ${displayFieldName(field)} date`);
        
        const updateFilter = () => {
            const min = minInput.value.trim();
            const max = maxInput.value.trim();
            const validMin = min ? JalaliUtils.parse(min) : null;
            const validMax = max ? JalaliUtils.parse(max) : null;
            minInput.setCustomValidity(min && !validMin ? 'Use YY.mm.dd' : '');
            maxInput.setCustomValidity(max && !validMax ? 'Use YY.mm.dd' : '');
            if ((min && !validMin) || (max && !validMax)) {
                return;
            }
            let nextMin = min;
            let nextMax = max;
            if (validMin && validMax && JalaliUtils.compare(validMin, validMax) > 0) {
                nextMin = max;
                nextMax = min;
                minInput.value = nextMin;
                maxInput.value = nextMax;
            }
            
            if (nextMin || nextMax) {
                state.columnFilters[field] = { type: 'jalali-date', min: nextMin, max: nextMax };
            } else {
                delete state.columnFilters[field];
            }
            saveColumnFilters();
            applyFilters();
        };
        
        minInput.addEventListener('change', updateFilter);
        maxInput.addEventListener('change', updateFilter);
        
        wrapper.appendChild(minInput);
        wrapper.appendChild(maxInput);
        container.appendChild(wrapper);
        
    } else {
        // Text search for text fields
        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'filter-input';
        input.placeholder = 'Filter...';
        input.value = currentFilter?.value ?? '';
        input.setAttribute('aria-label', `Filter ${displayFieldName(field)} text`);
        
        const updateTextFilter = debounce((e) => {
            const value = e.target.value.trim();
            if (value) {
                state.columnFilters[field] = { type: 'text', value };
            } else {
                delete state.columnFilters[field];
            }
            saveColumnFilters();
            applyFilters();
        }, FILTER_INPUT_DEBOUNCE_MS);
        input.addEventListener('input', updateTextFilter);
        
        container.appendChild(input);
    }
    
    return container;
}

function createRangePopover2(field, currentFilter, mode) {
    const wrapper = document.createElement('div');
    wrapper.className = 'range-popover';

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'range-trigger';
    button.setAttribute('aria-label', `Set ${displayFieldName(field)} range filter`);

    const panel = document.createElement('div');
    panel.className = 'range-panel';
    panel.addEventListener('click', event => event.stopPropagation());

    const stats = state.fieldStats[field] || {};
    const minLimit = Number.isFinite(stats.min) ? stats.min : 0;
    const maxLimit = Number.isFinite(stats.max) ? stats.max : Math.max(minLimit + 1, 1);
    const step = Number.isInteger(minLimit) && Number.isInteger(maxLimit) ? 1 : 0.01;

    const minInput = document.createElement('input');
    minInput.type = 'text';
    minInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    minInput.className = 'filter-input-small';
    minInput.placeholder = 'Min';
    minInput.value = currentFilter?.min ?? '';
    minInput.setAttribute('aria-label', `Minimum ${displayFieldName(field)}`);

    const maxInput = document.createElement('input');
    maxInput.type = 'text';
    maxInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    maxInput.className = 'filter-input-small';
    maxInput.placeholder = 'Max';
    maxInput.value = currentFilter?.max ?? '';
    maxInput.setAttribute('aria-label', `Maximum ${displayFieldName(field)}`);

    const minSlider = document.createElement('input');
    minSlider.type = 'range';
    minSlider.className = 'range-slider';
    minSlider.min = String(minLimit);
    minSlider.max = String(maxLimit);
    minSlider.step = String(step);
    minSlider.value = String(currentFilter?.min ?? minLimit);

    const maxSlider = document.createElement('input');
    maxSlider.type = 'range';
    maxSlider.className = 'range-slider';
    maxSlider.min = String(minLimit);
    maxSlider.max = String(maxLimit);
    maxSlider.step = String(step);
    maxSlider.value = String(currentFilter?.max ?? maxLimit);

    const clearBtn = document.createElement('button');
    clearBtn.type = 'button';
    clearBtn.className = 'range-clear';
    clearBtn.textContent = 'Clear';

    const updateTrigger = () => {
        const latest = state.columnFilters[field];
        const active = latest?.min !== undefined && latest?.min !== null || latest?.max !== undefined && latest?.max !== null;
        button.innerHTML = active ? `<span class="filter-badge">${latest.min ?? 'min'}-${latest.max ?? 'max'}</span>` : '<span class="ellipsis">&bull;&bull;&bull;</span>';
    };

    const commit = debounce(() => {
        const rawMin = minInput.value.trim();
        const rawMax = maxInput.value.trim();
        let min = rawMin ? parseFloat(rawMin) : null;
        let max = rawMax ? parseFloat(rawMax) : null;
        minInput.setCustomValidity(rawMin && !Number.isFinite(min) ? 'Enter a number' : '');
        maxInput.setCustomValidity(rawMax && !Number.isFinite(max) ? 'Enter a number' : '');
        if ((rawMin && !Number.isFinite(min)) || (rawMax && !Number.isFinite(max))) return;
        if (min !== null && max !== null && min > max) {
            [min, max] = [max, min];
            minInput.value = String(min);
            maxInput.value = String(max);
        }
        if (min !== null || max !== null) {
            state.columnFilters[field] = { type: mode, min, max };
        } else {
            delete state.columnFilters[field];
        }
        saveColumnFilters();
        updateTrigger();
        applyFilters();
    }, 80);

    const syncFromInput = () => {
        const min = parseFloat(minInput.value);
        const max = parseFloat(maxInput.value);
        if (Number.isFinite(min)) minSlider.value = String(Math.min(Math.max(min, minLimit), maxLimit));
        if (Number.isFinite(max)) maxSlider.value = String(Math.min(Math.max(max, minLimit), maxLimit));
        commit();
    };

    const syncFromSlider = () => {
        let min = parseFloat(minSlider.value);
        let max = parseFloat(maxSlider.value);
        if (min > max) [min, max] = [max, min];
        minInput.value = String(min);
        maxInput.value = String(max);
        commit();
    };

    button.addEventListener('click', event => {
        event.stopPropagation();
        if (state.openRangePanel && state.openRangePanel !== panel) {
            state.openRangePanel.classList.remove('open');
        }
        panel.classList.toggle('open');
        state.openRangePanel = panel.classList.contains('open') ? panel : null;
        if (panel.classList.contains('open')) {
            setTimeout(() => minInput.focus({ preventScroll: true }), 20);
        }
    });

    minInput.addEventListener('input', syncFromInput);
    maxInput.addEventListener('input', syncFromInput);
    minSlider.addEventListener('input', syncFromSlider);
    maxSlider.addEventListener('input', syncFromSlider);
    clearBtn.addEventListener('click', () => {
        delete state.columnFilters[field];
        minInput.value = '';
        maxInput.value = '';
        minSlider.value = String(minLimit);
        maxSlider.value = String(maxLimit);
        saveColumnFilters();
        updateTrigger();
        applyFilters();
    });

    panel.appendChild(wrapRangeField('Min', minInput));
    panel.appendChild(wrapRangeField('Max', maxInput));
    panel.appendChild(minSlider);
    panel.appendChild(maxSlider);
    panel.appendChild(clearBtn);
    wrapper.appendChild(button);
    wrapper.appendChild(panel);
    updateTrigger();
    return wrapper;
}

function wrapRangeField(label, input) {
    const wrapper = document.createElement('label');
    wrapper.className = 'range-field';
    const span = document.createElement('span');
    span.textContent = label;
    wrapper.appendChild(span);
    wrapper.appendChild(input);
    return wrapper;
}

function createRangePopover(field, currentFilter, mode) {
    const wrapper = document.createElement('div');
    wrapper.className = 'range-popover';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'range-trigger';
    button.setAttribute('aria-label', `Set ${displayFieldName(field)} range filter`);
    const active = currentFilter?.min !== undefined && currentFilter?.min !== null || currentFilter?.max !== undefined && currentFilter?.max !== null;
    button.innerHTML = active ? `<span class="filter-badge">${currentFilter.min ?? 'min'}–${currentFilter.max ?? 'max'}</span>` : '<span class="ellipsis">•••</span>';
    const panel = document.createElement('div');
    panel.className = 'range-panel';

    const minInput = document.createElement('input');
    minInput.type = mode === 'numeric' ? 'text' : 'text';
    minInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    minInput.className = 'filter-input-small';
    minInput.placeholder = mode === 'numeric' ? 'Min' : 'From';
    minInput.value = currentFilter?.min ?? '';
    minInput.setAttribute('aria-label', `Minimum ${displayFieldName(field)}`);

    const maxInput = document.createElement('input');
    maxInput.type = mode === 'numeric' ? 'text' : 'text';
    maxInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    maxInput.className = 'filter-input-small';
    maxInput.placeholder = mode === 'numeric' ? 'Max' : 'To';
    maxInput.value = currentFilter?.max ?? '';
    maxInput.setAttribute('aria-label', `Maximum ${displayFieldName(field)}`);

    const applyBtn = document.createElement('button');
    applyBtn.type = 'button';
    applyBtn.className = 'range-apply';
    applyBtn.textContent = 'Apply';

    const clearBtn = document.createElement('button');
    clearBtn.type = 'button';
    clearBtn.className = 'range-clear';
    clearBtn.textContent = 'Clear';

    const apply = () => {
        const rawMin = minInput.value.trim();
        const rawMax = maxInput.value.trim();
        let min = mode === 'numeric' && rawMin ? parseFloat(rawMin) : rawMin || null;
        let max = mode === 'numeric' && rawMax ? parseFloat(rawMax) : rawMax || null;
        if (mode === 'numeric') {
            minInput.setCustomValidity(rawMin && !Number.isFinite(min) ? 'Enter a number' : '');
            maxInput.setCustomValidity(rawMax && !Number.isFinite(max) ? 'Enter a number' : '');
            if ((rawMin && !Number.isFinite(min)) || (rawMax && !Number.isFinite(max))) {
                return;
            }
            if (min !== null && max !== null && min > max) {
                [min, max] = [max, min];
                minInput.value = min;
                maxInput.value = max;
            }
        }
        if (min !== null || max !== null) {
            state.columnFilters[field] = { type: mode, min, max };
        } else {
            delete state.columnFilters[field];
        }
        saveColumnFilters();
        renderTableHeader();
        applyFilters();
    };
    button.addEventListener('click', (event) => {
        event.stopPropagation();
        panel.classList.toggle('open');
    });
    applyBtn.addEventListener('click', apply);
    clearBtn.addEventListener('click', () => {
        delete state.columnFilters[field];
        saveColumnFilters();
        renderTableHeader();
        applyFilters();
    });
    panel.appendChild(minInput);
    panel.appendChild(maxInput);
    panel.appendChild(applyBtn);
    panel.appendChild(clearBtn);
    wrapper.appendChild(button);
    wrapper.appendChild(panel);
    return wrapper;
}

function createCodeFilterControl(currentFilter) {
    const wrapper = document.createElement('div');
    wrapper.className = 'code-filter';

    const typeSelect = document.createElement('select');
    typeSelect.className = 'filter-select code-filter-type';
    typeSelect.setAttribute('aria-label', 'Filter Code type');
    [
        ['', 'All'],
        ['group', 'Group'],
        ['subgroup', 'Subgroup'],
        ['item', 'Item']
    ].forEach(([value, label]) => {
        const option = document.createElement('option');
        option.value = value;
        option.textContent = label;
        option.selected = (currentFilter?.codeType || '') === value;
        typeSelect.appendChild(option);
    });

    const segmentInput = document.createElement('input');
    segmentInput.type = 'text';
    segmentInput.inputMode = 'numeric';
    segmentInput.className = 'filter-input code-segment-input';
    segmentInput.placeholder = '100/200';
    segmentInput.value = currentFilter?.segment || '';
    segmentInput.setAttribute('aria-label', 'Filter Code group segments');

    const badge = document.createElement('span');
    badge.className = 'filter-badge';
    badge.textContent = currentFilter ? 'Code' : 'Any';

    const update = () => {
        const codeType = typeSelect.value;
        const segment = segmentInput.value.trim();
        if (codeType || segment) {
            state.columnFilters.Code = { type: 'code', codeType, segment };
        } else {
            delete state.columnFilters.Code;
        }
        saveColumnFilters();
        applyFilters();
    };

    typeSelect.addEventListener('change', update);
    segmentInput.addEventListener('input', update);
    wrapper.appendChild(typeSelect);
    wrapper.appendChild(segmentInput);
    wrapper.appendChild(badge);
    return wrapper;
}

// Apply all column filters
function applyFilters() {
    filterRecords();
    sortRecords();
    renderTable();
    updateCounts();
}

// Clear all filters
function clearAllFilters() {
    state.columnFilters = {};
    saveColumnFilters();
    renderTableHeader();
    applyFilters();
}

// Render table body
function renderTable(changedIndices = new Set()) {
    const tbody = document.getElementById('tableBody');
    const loading = document.getElementById('loading');
    const emptyState = document.getElementById('emptyState');
    
    loading.style.display = 'none';
    
    if (state.filteredRecords.length === 0) {
        tbody.innerHTML = '';
        emptyState.style.display = 'flex';
        return;
    }
    
    emptyState.style.display = 'none';
    
    let recordsToShow = state.settings.enablePagination
        ? state.filteredRecords.slice(0, state.settings.pageSize)
        : state.filteredRecords;
    const anchorCode = state.scrollAnchor?.code ? String(state.scrollAnchor.code) : '';
    if (state.settings.enablePagination && anchorCode) {
        const anchorIndex = state.filteredRecords.findIndex(record => String(record.Code ?? '') === anchorCode);
        if (anchorIndex >= state.settings.pageSize) {
            recordsToShow = state.filteredRecords.slice(0, anchorIndex + 1);
        }
    }
    
    tbody.innerHTML = '';
    
    recordsToShow.forEach((record, displayIndex) => {
        const row = document.createElement('tr');
        const codeInfo = parsePatrisCode(record.Code);
        const recordCode = String(record.Code ?? '');
        if (recordCode) {
            row.dataset.code = recordCode;
        }
        row.classList.add(`code-${codeInfo.type}`);
        row.classList.add(anbarTotal(record) > 0 ? 'has-stock' : 'no-stock');
        
        // Find the original index in state.records
        const originalIndex = state.records.indexOf(record);
        
        // Add highlight class if changed
        if (changedIndices.has(originalIndex) && state.settings.highlightChanges) {
            row.classList.add('changed');
            
            // Scroll to changed item if setting is enabled
            if (state.settings.autoScrollToChanged && originalIndex === Math.min(...changedIndices)) {
                setTimeout(() => {
                    row.scrollIntoView({ behavior: 'smooth', block: 'center' });
                }, 100);
            }
        }
        
        // Add data cells
        state.fields.forEach(field => {
            // Skip hidden columns
            if (state.hiddenColumns.has(field)) {
                return;
            }
            
            const td = document.createElement('td');
            
            // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
            if (field.startsWith('ANBAR') && field.length > 5) {
                const anbarIndex = parseInt(field.substring(5), 10) - 1;
                if (record.ANBAR && Array.isArray(record.ANBAR) && anbarIndex < record.ANBAR.length) {
                    const value = record.ANBAR[anbarIndex];
                    // Apply thousand separator to ANBAR values
                    td.textContent = value !== null && value !== undefined ? formatNumberWithSeparator(value) : '';
                } else {
                    td.textContent = '';
                }
                td.classList.add('anbar-column');
                // Right-align numeric ANBAR values
                td.style.textAlign = 'right';
            } else {
                const value = record[field];
                
                // Apply thousand separator to numeric fields (except Code and Serial)
                if (field !== 'Code' && field !== 'Serial' && value !== null && value !== undefined && !isNaN(value)) {
                    td.textContent = formatNumberWithSeparator(value);
                    // Right-align numeric fields
                    td.style.textAlign = 'right';
                } else {
                    td.textContent = value !== null && value !== undefined ? value : '';
                }
            }
            
            // Make Code column sticky
            if (field === 'Code') {
                td.classList.add('sticky-column');
            }
            row.appendChild(td);
        });
        
        // Add actions cell
        const actionsCell = document.createElement('td');
        actionsCell.className = 'action-cell';
        
        const inspectBtn = document.createElement('button');
        inspectBtn.className = 'action-btn';
        inspectBtn.textContent = '🔍 Inspect';
        inspectBtn.onclick = (e) => {
            e.stopPropagation();
            persistScrollAnchorForCode(recordCode);
            inspectRecord(record);
        };
        
        actionsCell.appendChild(inspectBtn);
        row.appendChild(actionsCell);
        
        // Make row clickable to inspect
        row.onclick = () => {
            persistScrollAnchorForCode(recordCode);
            inspectRecord(record);
        };
        
        tbody.appendChild(row);
    });
    if (!(state.settings.autoScrollToChanged && changedIndices.size > 0)) {
        restoreScrollAnchorAfterRender();
    }
}

// Sort by field
function sortByField(field) {
    // Toggle direction if same field, otherwise reset to ascending
    if (state.sortField === field) {
        state.sortDirection = state.sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
        state.sortField = field;
        state.sortDirection = 'asc';
    }
    
    // Save sort preferences
    saveSortPreferences();
    
    sortRecords();
    renderTableHeader();  // Re-render header to update sort indicators
    renderTable();
}

// Sort records based on current sort field and direction
function sortRecords() {
    state.filteredRecords.sort((a, b) => {
        let aVal, bVal;
        
        // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
        if (state.sortField.startsWith('ANBAR') && state.sortField.length > 5) {
            const anbarIndex = parseInt(state.sortField.substring(5), 10) - 1;
            aVal = a.ANBAR && Array.isArray(a.ANBAR) && anbarIndex < a.ANBAR.length ? a.ANBAR[anbarIndex] : '';
            bVal = b.ANBAR && Array.isArray(b.ANBAR) && anbarIndex < b.ANBAR.length ? b.ANBAR[anbarIndex] : '';
        } else {
            aVal = a[state.sortField];
            bVal = b[state.sortField];
        }
        
        // Special handling for Code field - right-pad to 9 characters for sorting
        if (state.sortField === 'Code') {
            aVal = String(aVal || '').padEnd(9, ' ');
            bVal = String(bVal || '').padEnd(9, ' ');
            // Use pure string comparison for Code to respect padding
            let result = aVal < bVal ? -1 : (aVal > bVal ? 1 : 0);
            return state.sortDirection === 'asc' ? result : -result;
        } else {
            // Convert to string for comparison
            aVal = String(aVal || '');
            bVal = String(bVal || '');
            // Use locale comparison with numeric support for other fields
            let result = aVal.localeCompare(bVal, undefined, { numeric: true, sensitivity: 'base' });
            return state.sortDirection === 'asc' ? result : -result;
        }
    });
}

// Export data
function exportData(format) {
    const data = state.filteredRecords.length > 0 ? state.filteredRecords : state.records;
    
    if (format === 'json') {
        // Export as JSON in transformed format (Code as keys)
        const transformed = transformRecordsForExport(data);
        const jsonStr = JSON.stringify(transformed, null, 2);
        const blob = new Blob([jsonStr], { type: 'application/json' });
        downloadFile(blob, 'patris-export.json');
    } else if (format === 'csv') {
        // Export as CSV
        const csv = convertToCSV(data);
        const blob = new Blob([csv], { type: 'text/csv' });
        downloadFile(blob, 'patris-export.csv');
    }
    
    // Close export dropdown
    document.getElementById('exportDropdown').classList.remove('open');
}

// Render column manager with checkboxes for each column
function renderColumnManager() {
    const container = document.getElementById('columnCheckboxes');
    container.innerHTML = '';

    getColumnManagerEntries().forEach(entry => {
        container.appendChild(createColumnManagerRow(entry));
    });
}

function getColumnManagerEntries() {
    const entries = [];
    const anbarFields = state.fields.filter(isAnbarField);
    let anbarAdded = false;
    state.fields.forEach(field => {
        if (isAnbarField(field)) {
            if (!anbarAdded) {
                entries.push({ key: 'ANBAR_GROUP', fields: anbarFields, type: 'stock', draggable: true });
                anbarAdded = true;
            }
            return;
        }
        entries.push({ key: field, fields: [field], type: field === 'Code' ? 'identity' : state.fieldTypes[field] || 'text', draggable: field !== 'Code' });
    });
    return entries;
}

function createColumnManagerRow(entry) {
    const row = document.createElement('div');
    row.className = 'column-manager-row';
    row.draggable = !!entry.draggable;
    row.dataset.columnKey = entry.key;

    const visibleCell = document.createElement('label');
    visibleCell.className = 'checkbox-label column-visible-toggle';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.checked = entry.fields.some(field => !state.hiddenColumns.has(field));
    checkbox.disabled = entry.key === 'Code';
    checkbox.addEventListener('change', event => {
        entry.fields.forEach(field => {
            if (event.target.checked) {
                state.hiddenColumns.delete(field);
            } else {
                state.hiddenColumns.add(field);
                delete state.columnFilters[field];
            }
        });
        saveHiddenColumns();
        saveColumnFilters();
        renderTableHeader();
        applyFilters();
    });
    visibleCell.appendChild(checkbox);
    visibleCell.appendChild(document.createElement('span'));

    const sourceCell = document.createElement('div');
    sourceCell.className = 'column-source-cell';
    if (entry.key === 'ANBAR_GROUP') {
        sourceCell.innerHTML = `<strong>ANBAR</strong><div class="warehouse-chip-grid">${entry.fields.map(field => `<span>${escapeHtml(field.replace('ANBAR', ''))}</span>`).join('')}</div>`;
    } else {
        sourceCell.innerHTML = `<strong>${escapeHtml(entry.key)}</strong>${entry.key === 'Code' ? '<small>Always visible</small>' : ''}`;
    }

    const labelCell = document.createElement('div');
    labelCell.className = 'column-label-cell';
    const labelInput = document.createElement('input');
    labelInput.className = 'text-input';
    labelInput.type = 'text';
    labelInput.value = entry.key === 'ANBAR_GROUP' ? displayStockGroupName() : displayFieldName(entry.key);
    labelInput.placeholder = entry.key === 'ANBAR_GROUP' ? 'Stock' : entry.key;
    labelInput.addEventListener('input', debounce(() => {
        state.config = state.config || {};
        state.config.column_labels = state.config.column_labels || {};
        if (entry.key === 'ANBAR_GROUP') {
            state.config.column_labels.ANBAR = labelInput.value.trim() || 'Stock';
        } else {
            state.config.column_labels[entry.key] = labelInput.value.trim() || entry.key;
        }
        saveConfigToServer(state.config);
        renderTableHeader();
    }, 180));
    labelCell.appendChild(labelInput);

    const typeCell = document.createElement('div');
    typeCell.className = 'column-type-cell';
    typeCell.textContent = entry.type;

    row.appendChild(visibleCell);
    row.appendChild(sourceCell);
    row.appendChild(labelCell);
    row.appendChild(typeCell);
    attachColumnDragHandlers(row);
    return row;
}

function attachColumnDragHandlers(row) {
    row.addEventListener('dragstart', event => {
        if (!row.draggable) {
            event.preventDefault();
            return;
        }
        row.classList.add('dragging');
        event.dataTransfer.setData('text/plain', row.dataset.columnKey);
        event.dataTransfer.effectAllowed = 'move';
    });
    row.addEventListener('dragend', () => row.classList.remove('dragging'));
    row.addEventListener('dragover', event => {
        event.preventDefault();
        row.classList.add('drag-over');
    });
    row.addEventListener('dragleave', () => row.classList.remove('drag-over'));
    row.addEventListener('drop', event => {
        event.preventDefault();
        row.classList.remove('drag-over');
        const draggedKey = event.dataTransfer.getData('text/plain');
        reorderColumnEntries(draggedKey, row.dataset.columnKey);
    });
}

function reorderColumnEntries(draggedKey, targetKey) {
    if (!draggedKey || !targetKey || draggedKey === targetKey || draggedKey === 'Code') return;
    const entries = getColumnManagerEntries();
    const from = entries.findIndex(entry => entry.key === draggedKey);
    const to = entries.findIndex(entry => entry.key === targetKey);
    if (from < 0 || to < 0) return;
    const [entry] = entries.splice(from, 1);
    entries.splice(to, 0, entry);
    state.fields = entries.flatMap(item => item.fields);
    ensureCodeFirst();
    saveColumnOrder();
    renderColumnManager();
    renderTableHeader();
    applyFilters();
}

// Transform records to Code-keyed format for export
function transformRecordsForExport(records) {
    const result = {};
    
    records.forEach(record => {
        const code = record.Code;
        if (!code) return; // Skip records without Code
        
        // Create a copy of the record without Code field (it becomes the key)
        const transformedRecord = {};
        
        // Copy all fields except Code and ANBAR (we'll handle ANBAR specially)
        Object.keys(record).forEach(key => {
            if (key !== 'Code' && key !== 'ANBAR') {
                transformedRecord[key] = record[key];
            }
        });
        
        // Add ANBAR array if it exists
        if (record.ANBAR && Array.isArray(record.ANBAR)) {
            transformedRecord.ANBAR = record.ANBAR;
        }
        
        result[code] = transformedRecord;
    });
    
    return result;
}

// Convert data to CSV format
function convertToCSV(data) {
    if (data.length === 0) return '';
    
    // Create header row
    const headers = state.fields.join(',');
    
    // Create data rows
    const rows = data.map(record => {
        return state.fields.map(field => {
            let value;
            
            // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
            if (field.startsWith('ANBAR') && field.length > 5) {
                const anbarIndex = parseInt(field.substring(5), 10) - 1;
                if (record.ANBAR && Array.isArray(record.ANBAR) && anbarIndex < record.ANBAR.length) {
                    value = record.ANBAR[anbarIndex];
                } else {
                    value = '';
                }
            } else {
                value = record[field];
            }
            
            // Escape value for CSV
            const str = value !== null && value !== undefined ? String(value) : '';
            // Quote if contains comma, newline, or quote
            if (str.includes(',') || str.includes('\n') || str.includes('"')) {
                return `"${str.replace(/"/g, '""')}"`;
            }
            return str;
        }).join(',');
    });
    
    return [headers, ...rows].join('\n');
}

// Download file helper
function downloadFile(blob, filename) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
}

// Filter records based on search term, selected field, and column filters
function filterRecords() {
    // Start with all records
    let filtered = state.records;
    
    // Apply column filters (AND logic - all filters must pass)
    if (Object.keys(state.columnFilters).length > 0) {
        filtered = filtered.filter(record => {
            // Check each active filter
            for (const [field, filter] of Object.entries(state.columnFilters)) {
                if (state.hiddenColumns.has(field)) {
                    continue;
                }
                if (!passesFilter(record, field, filter)) {
                    return false;
                }
            }
            return true;
        });
    }
    
    // Apply field-specific search filter
    if (state.selectedField) {
        filtered = filtered.filter(record => {
            const value = getFieldValue(record, state.selectedField);
            return value !== null && value !== undefined && value !== '';
        });
    }
    
    // Apply global search filter
    if (state.searchTerm) {
        const searchLower = state.searchTerm.toLowerCase();
        filtered = filtered.filter(record => {
            return state.fields.some(field => {
                const value = getFieldValue(record, field);
                if (value === null || value === undefined) return false;
                return String(value).toLowerCase().includes(searchLower);
            });
        });
    }
    
    state.filteredRecords = filtered;
}

// Get field value from record (handles ANBAR fields)
function getFieldValue(record, field) {
    if (field.startsWith('ANBAR') && field.length > 5) {
        const anbarIndex = parseInt(field.substring(5), 10) - 1;
        if (record.ANBAR && Array.isArray(record.ANBAR) && anbarIndex < record.ANBAR.length) {
            return record.ANBAR[anbarIndex];
        }
        return null;
    }
    return record[field];
}

// Check if record passes a specific filter
function passesFilter(record, field, filter) {
    const value = getFieldValue(record, field);
    
    // Null/undefined/empty values handling
    if (value === null || value === undefined || value === '') {
        return false; // Exclude null values from filtered results
    }
    
    switch (filter.type) {
        case 'categorical':
            return String(value) === filter.value;
            
        case 'numeric':
            const numValue = typeof value === 'string' ? parseFloat(value) : value;
            if (isNaN(numValue)) return false;
            
            if (filter.min !== null && numValue < filter.min) return false;
            if (filter.max !== null && numValue > filter.max) return false;
            return true;
            
        case 'jalali-date':
            const dateValue = JalaliUtils.parse(String(value));
            if (!dateValue) return false;
            
            if (filter.min) {
                const minDate = JalaliUtils.parse(filter.min);
                if (minDate && JalaliUtils.compare(dateValue, minDate) < 0) {
                    return false;
                }
            }
            
            if (filter.max) {
                const maxDate = JalaliUtils.parse(filter.max);
                if (maxDate && JalaliUtils.compare(dateValue, maxDate) > 0) {
                    return false;
                }
            }
            return true;
            
        case 'text':
            return String(value).toLowerCase().includes(filter.value.toLowerCase());

        case 'code':
            const parsed = parsePatrisCode(value);
            if (filter.codeType && parsed.type !== filter.codeType) return false;
            if (filter.segment) {
                const segments = filter.segment.split(/[\/\s.-]+/).filter(Boolean);
                return segments.every((segment, index) => parsed.groups[index] === segment.padStart(3, '0') || parsed.groups[index] === segment);
            }
            return true;
            
        default:
            return true;
    }
}

// Update field filter dropdown
function updateFieldFilter() {
    const select = document.getElementById('fieldFilter');
    select.innerHTML = '<option value="">All Fields</option>';
    
    state.fields.forEach(field => {
        const option = document.createElement('option');
        option.value = field;
        option.textContent = field;
        select.appendChild(option);
    });
}

// Update record counts
function updateCounts() {
    document.getElementById('totalCount').textContent = state.records.length;
    document.getElementById('filteredCount').textContent = state.filteredRecords.length;
    
    // Update footer record count
    updateFooterRecordCount();
}

// Inspect record
function inspectRecord(record) {
    const panel = document.getElementById('inspectorPanel');
    const body = document.getElementById('inspectorBody');
    
    body.innerHTML = '';
    
    state.fields.forEach(field => {
        const fieldDiv = document.createElement('div');
        fieldDiv.className = 'inspector-field';
        
        const nameDiv = document.createElement('div');
        nameDiv.className = 'inspector-field-name';
        nameDiv.textContent = field;
        
        const valueDiv = document.createElement('div');
        valueDiv.className = 'inspector-field-value';
        
        // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
        let value;
        if (field.startsWith('ANBAR') && field.length > 5) {
            const anbarIndex = parseInt(field.substring(5), 10) - 1;
            if (record.ANBAR && Array.isArray(record.ANBAR) && anbarIndex < record.ANBAR.length) {
                value = record.ANBAR[anbarIndex];
            } else {
                value = null;
            }
        } else {
            value = record[field];
        }
        
        valueDiv.textContent = value !== null && value !== undefined ? String(value) : '(null)';
        
        fieldDiv.appendChild(nameDiv);
        fieldDiv.appendChild(valueDiv);
        body.appendChild(fieldDiv);
    });
    
    panel.classList.add('open');
}

// Toggle theme
function toggleTheme() {
    const isDark = document.body.classList.toggle('dark-mode');
    document.documentElement.classList.toggle('dark-mode', isDark);
    const theme = isDark ? 'dark' : 'light';
    localStorage.setItem('theme', theme);
    setValue('settingsTheme', theme);
    updateThemeIcon(isDark);
    syncSettingsToConfig();
    publishFrontendBroadcast('theme:update', { theme });
}

// Update theme icon
function updateThemeIcon(isDark) {
    const btn = document.getElementById('themeToggle');
    btn.textContent = isDark ? '☀️' : '🌙';
}

function setLoadingState(isLoading) {
    state.isInitialLoad = isLoading;
    document.body.classList.toggle('is-loading', isLoading);
    const loading = document.getElementById('loading');
    if (loading) {
        loading.style.display = isLoading ? 'flex' : 'none';
    }
}

function focusSearchInput() {
    const searchInput = document.getElementById('searchInput');
    if (!searchInput) {
        return;
    }
    searchInput.focus({ preventScroll: true });
    searchInput.select();
}

function eventMatchesAriaShortcut(event, element) {
    const shortcuts = (element?.getAttribute('aria-keyshortcuts') || '')
        .split(/\s+/)
        .filter(Boolean);
    if (shortcuts.length === 0) {
        return false;
    }

    const pressed = [
        event.ctrlKey ? 'Control' : '',
        event.altKey ? 'Alt' : '',
        event.shiftKey ? 'Shift' : '',
        event.metaKey ? 'Meta' : '',
        event.key,
    ].filter(Boolean).join('+').toLowerCase();

    return shortcuts.some((shortcut) => shortcut.toLowerCase() === pressed);
}

function handleGlobalKeydown(event) {
    const searchInput = document.getElementById('searchInput');
    if (!eventMatchesAriaShortcut(event, searchInput)) {
        return;
    }
    event.preventDefault();
    focusSearchInput();
}

// Initialize theme
function initTheme() {
    const savedTheme = localStorage.getItem('theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const isDark = savedTheme === 'dark' || ((!savedTheme || savedTheme === 'system') && prefersDark);
    
    if (isDark) {
        document.body.classList.add('dark-mode');
        document.documentElement.classList.add('dark-mode');
    } else {
        document.body.classList.remove('dark-mode');
        document.documentElement.classList.remove('dark-mode');
    }
    setValue('settingsTheme', savedTheme || 'system');
    updateThemeIcon(isDark);
}

// Initialize app
function init() {
    setLoadingState(true);
    initFrontendBroadcast();
    initSourceDragDrop();

    // Load settings
    loadSettings();
    applySettings();
    renderEventLogCount();
    
    // Initialize theme
    initTheme();
    
    // Initialize footer
    updateFooter();
    initDialogActionButtons();
    initChromeMirrors();
    initTableWheelScroll();
    initScrollAnchorTracking();
    initRouter();
    setInterval(updateLastUpdateDisplay, 30000);
    
    // Set up event listeners
    document.addEventListener('keydown', handleGlobalKeydown);

    document.getElementById('searchInput').addEventListener('input', (e) => {
        state.searchTerm = e.target.value;
        filterRecords();
        sortRecords();
        renderTable();
        updateCounts();
        publishViewState();
    });
    
    document.getElementById('fieldFilter').addEventListener('change', (e) => {
        state.selectedField = e.target.value;
        filterRecords();
        sortRecords();
        renderTable();
        updateCounts();
        publishViewState();
    });
    
    document.getElementById('themeToggle').addEventListener('click', toggleTheme);
    document.getElementById('footerToggleBtn')?.addEventListener('click', toggleFooterVisibility);
    document.getElementById('footerCollapseBtn')?.addEventListener('click', toggleFooterVisibility);
    document.getElementById('footerLastUpdate')?.addEventListener('click', cycleLastUpdateMode);
    document.getElementById('headerConnectionButton')?.addEventListener('click', () => openModalRoute('connection'));
    document.getElementById('footerConnectionButton')?.addEventListener('click', () => openModalRoute('connection'));
    document.getElementById('eventLogBtn')?.addEventListener('click', () => openModalRoute('logs'));
    document.getElementById('closeEventLog')?.addEventListener('click', closeRouteDialog);
    document.getElementById('clearEventLog')?.addEventListener('click', () => clearEventLog());
    document.getElementById('headerFileChip')?.addEventListener('click', () => {
        if (state.fileName) showInAppToast('Current source file', state.fileName, { broadcastToTabs: true, source: 'file_info', eventType: 'source_info' });
    });
    
    // Export button and dropdown
    document.getElementById('exportBtn').addEventListener('click', () => {
        document.getElementById('exportDropdown').classList.toggle('open');
    });
    
    document.getElementById('exportJSON').addEventListener('click', () => exportData('json'));
    document.getElementById('exportCSV').addEventListener('click', () => exportData('csv'));
    
    // Close export dropdown when clicking outside
    document.addEventListener('click', (e) => {
        const exportBtn = document.getElementById('exportBtn');
        const exportDropdown = document.getElementById('exportDropdown');
        if (!exportBtn.contains(e.target) && !exportDropdown.contains(e.target)) {
            exportDropdown.classList.remove('open');
        }
        if (state.openRangePanel && !state.openRangePanel.contains(e.target) && !e.target.closest('.range-trigger')) {
            state.openRangePanel.classList.remove('open');
            state.openRangePanel = null;
        }
    });
    
    document.getElementById('settingsBtn').addEventListener('click', () => {
        openModalRoute('settings');
    });
    
    document.getElementById('closeSettings').addEventListener('click', () => {
        closeRouteDialog();
    });
    document.getElementById('closeConnection')?.addEventListener('click', closeRouteDialog);
    document.getElementById('panelBackdrop').addEventListener('click', closeRouteDialog);
    initSettingsTabs();
    
    document.getElementById('closeInspector').addEventListener('click', () => {
        document.getElementById('inspectorPanel').classList.remove('open');
    });
    
    // Column manager
    document.getElementById('columnsBtn').addEventListener('click', () => {
        openModalRoute('columns');
    });
    
    document.getElementById('closeColumns').addEventListener('click', () => {
        closeRouteDialog();
    });

    document.getElementById('refreshNowBtn').addEventListener('click', requestSourceRefresh);
    
    document.getElementById('showAllColumns').addEventListener('click', () => {
        state.hiddenColumns.clear();
        saveHiddenColumns();
        renderColumnManager();
        renderTableHeader();
        applyFilters();
    });
    
    document.getElementById('hideAllColumns').addEventListener('click', () => {
        // Don't allow hiding Code column
        state.fields.forEach(field => {
            if (field !== 'Code') {
                state.hiddenColumns.add(field);
            }
        });
        removeHiddenColumnFilters();
        saveHiddenColumns();
        renderColumnManager();
        renderTableHeader();
        applyFilters();
    });
    
    // Settings checkboxes
    document.getElementById('autoScrollToChanged').addEventListener('change', (e) => {
        state.settings.autoScrollToChanged = e.target.checked;
        saveSettings();
    });
    
    document.getElementById('highlightChanges').addEventListener('change', (e) => {
        state.settings.highlightChanges = e.target.checked;
        saveSettings();
    });

    document.getElementById('rtlTextDirection').addEventListener('change', (e) => {
        state.settings.rtlTextDirection = e.target.checked;
        applySettings();
        saveSettings();
    });
    
    document.getElementById('playNotificationSound').addEventListener('change', (e) => {
        state.settings.playNotificationSound = e.target.checked;
        saveSettings();
    });

    document.getElementById('showFooter').addEventListener('change', (e) => {
        state.settings.showFooter = e.target.checked;
        applySettings();
        saveSettings();
    });

    document.getElementById('lastUpdateDisplayMode').addEventListener('change', (e) => {
        state.settings.lastUpdateDisplayMode = e.target.value;
        applySettings();
        saveSettings();
    });

    document.getElementById('notificationSoundSource').addEventListener('change', (e) => {
        state.settings.notificationSoundSource = e.target.value;
        saveSettings();
    });

    document.getElementById('settingsTheme').addEventListener('change', (e) => {
        localStorage.setItem('theme', e.target.value);
        initTheme();
        saveSettings();
        publishFrontendBroadcast('theme:update', { theme: e.target.value });
    });

    [
        'serverHost',
        'serverPort',
        'serverWatch',
        'serverDebounce',
        'databasePath',
        'databaseCharmap',
        'directAccess',
        'rtlConversion',
        'notifyEnabled',
        'notifyNative',
        'notifyInApp',
        'notifyClientConnected',
        'notifyClientDisconnected',
        'notifyFileUpdated',
        'notifyRowUpdated',
        'notifyIncludeRowValues',
        'notifyMaxRows',
        'runtimeTempDir',
        'runtimeTempStrategy',
        'runtimeTempMemoryLimitMB',
        'runtimeDebug'
    ]
        .forEach(id => bindConfigField(id));

    document.getElementById('testNotificationSound').addEventListener('click', () => {
        playNotificationSound(true);
        showInAppToast('Sound test', 'Notification audio was triggered.', { broadcastToTabs: true, source: 'sound_test', eventType: 'notification_test' });
    });

    document.getElementById('testNativeToast').addEventListener('click', () => {
        sendNativeToast('Patris Export', 'Native desktop notifications are connected.');
    });
    
    document.getElementById('enablePagination').addEventListener('change', (e) => {
        state.settings.enablePagination = e.target.checked;
        saveSettings();
        renderTable();
    });
    
    document.getElementById('pageSize').addEventListener('change', (e) => {
        state.settings.pageSize = parseInt(e.target.value, 10);
        saveSettings();
        if (state.settings.enablePagination) {
            renderTable();
        }
    });

    ['enableRowColoring', 'rowColorGroup', 'rowColorSubgroup', 'rowColorNoStock', 'rowColorHasStock'].forEach(id => {
        const el = document.getElementById(id);
        if (!el) return;
        el.addEventListener('input', () => {
            if (id === 'enableRowColoring') {
                state.settings.enableRowColoring = el.checked;
            } else {
                state.settings[id] = el.value;
            }
            applySettings();
            saveSettings();
        });
        el.addEventListener('change', () => {
            if (id === 'enableRowColoring') {
                state.settings.enableRowColoring = el.checked;
            } else {
                state.settings[id] = el.value;
            }
            applySettings();
            saveSettings();
        });
    });
    
    // Initialize notification audio
    initNotificationAudio();
    
    fetchAppInfo();
    startResourceVersionPolling();
    loadServerConfig();

    // Initialize WebSocket
    initWebSocket();
    
    // Fetch database metadata
    fetchFileInfo();
    fetchProcessStatus();

    // Fetch initial data via HTTP
    fetchInitialData();

    const settingsTarget = new URLSearchParams(window.location.search).get('settings');
    if (settingsTarget) {
        setActiveSettingsTab(settingsTarget === '1' ? 'ui' : settingsTarget);
        applyConfigToSettingsForm();
        openModalRoute('settings', { replace: true });
    }
}

// Fetch database file metadata
async function fetchFileInfo() {
    try {
        const response = await fetch('/api/info');
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        const info = await response.json();
        state.fileName = info.path || info.file || '';
        updateFooterFileName();
    } catch (error) {
        console.error('Failed to fetch file info:', error);
        updateFooterFileName();
    }
}

// Fetch initial data
async function fetchInitialData() {
    try {
        const response = await fetch('/api/records');
        
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        
        const data = await response.json();
        
        // data is usually transformed as { "101": {...}, "102": {...}, ... }.
        // Some API modes can return an array, so handle both shapes.
        state.records = Array.isArray(data)
            ? data
            : Object.entries(data).map(([code, record]) => ({
                Code: code,
                ...record
            }));
        state.filteredRecords = [];
        state.fields = [];
        state.fieldTypes = {};
        state.fieldStats = {};
        
        if (state.records.length > 0) {
            extractFields();
            analyzeFields(); // Analyze field types and stats
            renderTableHeader();
            updateFieldFilter();
        }
        
        filterRecords();
        renderTable();
        updateCounts();
        setLoadingState(false);
    } catch (error) {
        console.error('Failed to fetch initial data:', error);
        setLoadingState(false);
    }
}

// Start the application when DOM is ready
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
