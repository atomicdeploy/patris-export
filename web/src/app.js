import { normalizeCategoriesPayload, normalizeRecordsPayload } from './records.js';
import { createExportMenuController } from './export-menu.js';
import { canonicalWorkbookPath } from './xlsx-export.mjs';
import {
    MAX_DETAILED_EVENT_LOG_ENTRIES,
    createEventLogChangeSnapshot,
    deletedRecordIdentityKey,
    escapeHtml,
    eventLogChangeDetailsMarkup,
    eventLogDisclosureText,
    eventLogLocalizedText,
    eventLogTokenLabel,
    isEventLogChangeDetails,
    modifiedRecordIdentityKey,
    normalizeEventLogContent,
    recordIdentityKey,
    retainRecentEventLogChanges,
    upgradeEventLogEntryLocalization
} from './event-log.js';
import {
    CANONICAL_ROW_FIELDS,
    DEFAULT_ROW_ICON_FALLBACK,
    ROW_ICON_NAMES,
    ROW_RULE_OPERATORS,
    assignStableRecordKeys,
    canonicalColumnKey,
    clampColumnWidth,
    defaultColumnWidth,
    deriveGridFields,
    duplicateSafeRecordKeys,
    fitMenuPosition,
    iconMarkup,
    keyboardColumnWidth,
    isWarehouseColumnField,
    localizedColumnLabel,
    normalizeColumnPreferenceList,
    localizedRelativeTime,
    normalizeColumnWidths,
    normalizeRowIconFallback,
    normalizeRowIconRule,
    normalizeRowIconRules,
    nextRovingKey,
    pruneSelectedKeys,
    resizedColumnWidth,
    resolvePersistedColumnPreferences,
    resolvedRovingKey,
    resolveRowIcon,
    rowCommandDefinitions,
    selectionSummary,
    stableRecordKey,
    structuredValueText,
    tableLanguage,
    tableText,
    warehouseColumnName
} from './table-ux.js';

// Application state
const state = {
    records: [],
    catalogProducts: [],
    catalogCategories: [],
    catalogCategoriesAvailable: false,
    catalogView: 'products',
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
    legacyColumnPreferences: {
        hiddenColumns: null,
        columnOrder: null
    },
    columnPreferenceMigrationAttempted: false,
    columnPreferenceMigrationPending: {
        hiddenColumns: false,
        columnOrder: false
    },
    selectedKeys: new Set(),
    recordSelectionKeys: new WeakMap(),
    rovingRowKey: '',
    rowMenu: {
        record: null,
        trigger: null,
        focusTarget: null,
        kind: ''
    },
    inspectedRecord: null,
    openRangePanel: null,
    openRangeAnchor: null,
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
        language: 'en',
        enableRowColoring: true,
        rowColorGroup: '#6366f1',
        rowColorSubgroup: '#0ea5e9',
        rowColorNoStock: '#6b7280',
        rowColorHasStock: '#10b981',
        enableRowIcons: true,
        freezeFirstColumn: true,
        columnWidths: {},
        rowIconRules: [],
        rowIconFallback: { ...DEFAULT_ROW_ICON_FALLBACK }
    },
    notificationAudio: null,
    originalTitle: document.title,
    originalFavicon: null,
    titleFlashInterval: null,
    faviconTimeout: null,
    tabId: '',
    broadcastChannel: null,
    seenBroadcastMessages: new Set(),
    revealedSettingsTabs: new Set(),
    dragDepth: 0,
    isUploadingSource: false
};

const CONFIG_STORAGE_KEY = 'patris-config';
const SETTINGS_STORAGE_KEY = 'patris-settings';
const HIDDEN_COLUMNS_STORAGE_KEY = 'patris-hidden-columns';
const COLUMN_ORDER_STORAGE_KEY = 'patris-column-order';
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

function loadLegacyColumnPreference(storageKey) {
    const raw = localStorage.getItem(storageKey);
    if (raw === null) return null;
    try {
        const parsed = JSON.parse(raw);
        return Array.isArray(parsed) ? normalizeColumnPreferenceList(parsed) : null;
    } catch (error) {
        console.warn(`Failed to read legacy column preference ${storageKey}:`, error);
        return null;
    }
}

// Load local settings and the legacy column cache used before UIConfig persistence.
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
    
    const legacyHiddenColumns = loadLegacyColumnPreference(HIDDEN_COLUMNS_STORAGE_KEY);
    const legacyColumnOrder = loadLegacyColumnPreference(COLUMN_ORDER_STORAGE_KEY);
    state.legacyColumnPreferences.hiddenColumns = legacyHiddenColumns;
    state.legacyColumnPreferences.columnOrder = legacyColumnOrder;
    if (legacyHiddenColumns !== null) {
        state.hiddenColumns = new Set(legacyHiddenColumns);
    }
    if (legacyColumnOrder !== null) {
        state.columnOrder = legacyColumnOrder;
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
            state.eventLog = retainRecentEventLogChanges(
                JSON.parse(savedEventLog)
                    .map(upgradeEventLogEntryLocalization)
                    .filter(entry => entry && (entry.title || entry.titleKey))
                    .slice(0, MAX_EVENT_LOG_ENTRIES),
                MAX_DETAILED_EVENT_LOG_ENTRIES
            );
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

function applyConfigColumnPreferences(ui, source) {
    const remoteConfig = source !== 'local';
    const allowInitialLegacyFallback = !remoteConfig || !state.columnPreferenceMigrationAttempted;
    const legacy = {
        hiddenColumns: allowInitialLegacyFallback || state.columnPreferenceMigrationPending.hiddenColumns
            ? state.legacyColumnPreferences.hiddenColumns : null,
        columnOrder: allowInitialLegacyFallback || state.columnPreferenceMigrationPending.columnOrder
            ? state.legacyColumnPreferences.columnOrder : null
    };
    const resolved = resolvePersistedColumnPreferences(ui, legacy);
    state.hiddenColumns = new Set(resolved.hiddenColumns);
    state.columnOrder = resolved.columnOrder;

    if (remoteConfig || resolved.cacheHiddenColumns) {
        const hiddenColumns = [...state.hiddenColumns];
        localStorage.setItem(HIDDEN_COLUMNS_STORAGE_KEY, JSON.stringify(hiddenColumns));
        state.legacyColumnPreferences.hiddenColumns = hiddenColumns;
    }
    if (remoteConfig || resolved.cacheColumnOrder) {
        const columnOrder = state.columnOrder.slice();
        localStorage.setItem(COLUMN_ORDER_STORAGE_KEY, JSON.stringify(columnOrder));
        state.legacyColumnPreferences.columnOrder = columnOrder;
    }

    const shouldMigrate = remoteConfig
        && !state.columnPreferenceMigrationAttempted
        && (resolved.migrateHiddenColumns || resolved.migrateColumnOrder);
    if (remoteConfig) {
        state.columnPreferenceMigrationAttempted = true;
        if (Array.isArray(ui.hidden_columns)) {
            state.columnPreferenceMigrationPending.hiddenColumns = false;
        }
        if (Array.isArray(ui.column_order)) {
            state.columnPreferenceMigrationPending.columnOrder = false;
        }
    }
    if (shouldMigrate) {
        if (resolved.migrateHiddenColumns) {
            ui.hidden_columns = [...state.hiddenColumns];
            state.columnPreferenceMigrationPending.hiddenColumns = true;
        }
        if (resolved.migrateColumnOrder) {
            ui.column_order = state.columnOrder.slice();
            state.columnPreferenceMigrationPending.columnOrder = true;
        }
    }
    return shouldMigrate;
}

function applyConfig(config, source = 'server') {
    if (!config) return null;
    const diff = buildConfigDiff(state.config, config);
    state.config = config;
    localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    if (config.ui) {
        const migrateColumnPreferences = applyConfigColumnPreferences(config.ui, source);
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
            showFooter: config.ui.show_footer === undefined ? state.settings.showFooter : config.ui.show_footer !== false,
            lastUpdateDisplayMode: config.ui.last_update_display_mode || state.settings.lastUpdateDisplayMode,
            language: tableLanguage(config.ui.language || state.settings.language),
            enableRowColoring: config.ui.enable_row_coloring !== false,
            rowColorGroup: config.ui.row_color_group || state.settings.rowColorGroup,
            rowColorSubgroup: config.ui.row_color_subgroup || state.settings.rowColorSubgroup,
            rowColorNoStock: config.ui.row_color_no_stock || state.settings.rowColorNoStock,
            rowColorHasStock: config.ui.row_color_has_stock || state.settings.rowColorHasStock,
            enableRowIcons: config.ui.enable_row_icons !== false,
            freezeFirstColumn: config.ui.freeze_first_column !== false,
            columnWidths: normalizeColumnWidths(config.ui.column_widths),
            rowIconRules: normalizeRowIconRules(config.ui.row_icon_rules),
            rowIconFallback: normalizeRowIconFallback(config.ui.row_icon_fallback)
        };
        localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(state.settings));
        applySettings();
        initTheme();
        if (state.fields.length > 0) {
            applyColumnOrder();
            removeHiddenColumnFilters({ broadcast: false });
            renderTableHeader();
            applyFilters();
        }
        if (migrateColumnPreferences) {
            saveConfigToServer(state.config);
        }
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
    const message = configDiffMessageDescriptor(changed);
    return {
        changed,
        signature,
        dedupeKey: hashString(signature),
        messageKey: message.key,
        messageValues: message.values,
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

function configDiffMessageDescriptor(changed) {
    if (changed.length === 1) {
        const change = changed[0];
        return {
            key: 'settingsChangeSingle',
            values: {
                path: change.path,
                before: formatConfigDiffValue(change.before),
                after: formatConfigDiffValue(change.after)
            }
        };
    }
    const names = changed.slice(0, 4).map(change => change.path).join(', ');
    const suffix = changed.length > 4 ? ', ...' : '';
    return {
        key: 'settingsChangeMultiple',
        values: { count: changed.length, names, suffix }
    };
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
        language: tableLanguage(state.settings.language),
        enable_row_coloring: state.settings.enableRowColoring,
        row_color_group: state.settings.rowColorGroup,
        row_color_subgroup: state.settings.rowColorSubgroup,
        row_color_no_stock: state.settings.rowColorNoStock,
        row_color_has_stock: state.settings.rowColorHasStock,
        enable_row_icons: state.settings.enableRowIcons,
        freeze_first_column: state.settings.freezeFirstColumn,
        column_widths: normalizeColumnWidths(state.settings.columnWidths),
        row_icon_rules: normalizeRowIconRules(state.settings.rowIconRules),
        row_icon_fallback: normalizeRowIconFallback(state.settings.rowIconFallback)
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
        showInAppToast(t('settingsSaveFailed'), error.message, { titleKey: 'settingsSaveFailed', error: true, broadcastToTabs: true, source: 'config_update', eventType: 'config_update' });
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

function cacheColumnPreferences() {
    const hiddenColumns = normalizeColumnPreferenceList([...state.hiddenColumns]);
    const columnOrder = normalizeColumnPreferenceList(state.columnOrder);
    state.hiddenColumns = new Set(hiddenColumns);
    state.columnOrder = columnOrder;
    state.legacyColumnPreferences.hiddenColumns = hiddenColumns;
    state.legacyColumnPreferences.columnOrder = columnOrder;
    localStorage.setItem(HIDDEN_COLUMNS_STORAGE_KEY, JSON.stringify(hiddenColumns));
    localStorage.setItem(COLUMN_ORDER_STORAGE_KEY, JSON.stringify(columnOrder));
}

function syncColumnPreferencesToConfig() {
    if (!state.config) return;
    state.config.ui = {
        ...(state.config.ui || {}),
        hidden_columns: [...state.hiddenColumns],
        column_order: state.columnOrder.slice()
    };
    saveConfigToServer(state.config);
}

// Keep localStorage as a cache/fallback while UIConfig remains authoritative.
function saveHiddenColumns(options = {}) {
    cacheColumnPreferences();
    if (options.persist !== false) {
        syncColumnPreferencesToConfig();
    }
    if (options.broadcast !== false) {
        publishFrontendBroadcast('columns:update', {
            hiddenColumns: [...state.hiddenColumns],
            columnOrder: state.columnOrder
        });
    }
}

function saveColumnOrder(options = {}) {
    state.columnOrder = state.fields.slice();
    cacheColumnPreferences();
    if (options.persist !== false) {
        syncColumnPreferencesToConfig();
    }
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
    saveHiddenColumns({ broadcast: false, persist: false });
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

    format(date) {
        if (!date) return '';
        return [
            String(date.year).padStart(2, '0'),
            String(date.month).padStart(2, '0'),
            String(date.day).padStart(2, '0')
        ].join('.');
    },

    daysInMonth(year, month) {
        if (month >= 1 && month <= 6) return 31;
        if (month >= 7 && month <= 11) return 30;
        return 29;
    },

    addMonths(date, delta) {
        if (!date) return { year: 0, month: 1, day: 1 };
        let year = date.year;
        let month = date.month + delta;
        while (month < 1) {
            month += 12;
            year -= 1;
        }
        while (month > 12) {
            month -= 12;
            year += 1;
        }
        const day = Math.min(date.day, JalaliUtils.daysInMonth(year, month));
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
    },

    clamp(date, min, max) {
        if (!date) return date;
        if (min && JalaliUtils.compare(date, min) < 0) return min;
        if (max && JalaliUtils.compare(date, max) > 0) return max;
        return date;
    }
};

// Detect field type based on values
function detectFieldType(field, records) {
    if (field === 'warehouse_stock') return 'text';
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
    const uniqueValues = new Set(values.map(structuredValueText));
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
            stats.uniqueValues.add(structuredValueText(value));
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
    const warehouseName = warehouseColumnName(field);
    const configured = labels[field]
        || (warehouseName ? labels[`warehouse_stock.${warehouseName}`] : '')
        || labels[field.replace(/[0-9]+$/, '')]
        || '';
    return localizedColumnLabel(field, state.settings.language, configured);
}

function displayStockGroupName() {
    const labels = state.config?.column_labels || {};
    const configured = String(labels.warehouse_stock || labels.Stock || labels.stock || labels.ANBAR || '').trim();
    if (configured && !/^(anbar|stock|warehouse|warehouse stock)$/i.test(configured)) return configured;
    return t('warehouseStock');
}

function parsePatrisCode(code) {
    const raw = String(code ?? '').replace(/\D/g, '');
    const groups = raw.match(/.{1,3}/g) || [];
    const depth = Math.min(groups.length, 3);
    const type = depth <= 1 ? 'group' : depth === 2 ? 'subgroup' : 'item';
    return { raw, groups, depth, type, group: groups[0] || '', subgroup: groups[1] || '', item: groups[2] || '' };
}

function anbarTotal(record) {
    const declared = Number(record?.total_stock);
    if (Number.isFinite(declared)) return declared;
    const stock = record?.warehouse_stock && typeof record.warehouse_stock === 'object' && !Array.isArray(record.warehouse_stock)
        ? Object.values(record.warehouse_stock)
        : Array.isArray(record?.ANBAR) ? record.ANBAR : [];
    return stock.reduce((sum, value) => {
        const n = typeof value === 'string' ? parseFloat(value) : value;
        return sum + (Number.isFinite(n) ? n : 0);
    }, 0);
}

// Apply settings to UI
function applySettings() {
    state.settings.language = tableLanguage(state.settings.language);
    state.settings.columnWidths = normalizeColumnWidths(state.settings.columnWidths);
    state.settings.rowIconRules = normalizeRowIconRules(state.settings.rowIconRules);
    state.settings.rowIconFallback = normalizeRowIconFallback(state.settings.rowIconFallback);
    setChecked('showFooter', state.settings.showFooter);
    setChecked('autoScrollToChanged', state.settings.autoScrollToChanged);
    setChecked('highlightChanges', state.settings.highlightChanges);
    setChecked('rtlTextDirection', state.settings.rtlTextDirection);
    setChecked('enablePagination', state.settings.enablePagination);
    setValue('pageSize', state.settings.pageSize);
    setChecked('playNotificationSound', state.settings.playNotificationSound);
    setValue('notificationSoundSource', state.settings.notificationSoundSource || 'external');
    setValue('lastUpdateDisplayMode', state.settings.lastUpdateDisplayMode || 'both');
    setValue('interfaceLanguage', state.settings.language);
    setChecked('enableRowColoring', state.settings.enableRowColoring);
    setValue('rowColorGroup', state.settings.rowColorGroup || '#6366f1');
    setValue('rowColorSubgroup', state.settings.rowColorSubgroup || '#0ea5e9');
    setValue('rowColorNoStock', state.settings.rowColorNoStock || '#6b7280');
    setValue('rowColorHasStock', state.settings.rowColorHasStock || '#10b981');
    setChecked('enableRowIcons', state.settings.enableRowIcons);
    setChecked('freezeFirstColumn', state.settings.freezeFirstColumn);
    setValue('rowIconFallbackIcon', state.settings.rowIconFallback.icon);
    setValue('rowIconFallbackColor', state.settings.rowIconFallback.color);
    setValue(
        'rowIconFallbackLabel',
        state.settings.rowIconFallback.label === DEFAULT_ROW_ICON_FALLBACK.label
            ? t('fallbackProduct')
            : state.settings.rowIconFallback.label
    );
    applyConfigToSettingsForm();
    document.body.classList.toggle('rtl-text-mode', !!state.settings.rtlTextDirection);
    document.body.classList.toggle('table-rtl', isTableRTL());
    document.body.classList.toggle('freeze-first-column', !!state.settings.freezeFirstColumn);
    document.documentElement.lang = state.settings.language;
    document.getElementById('dataTable')?.setAttribute('dir', isTableRTL() ? 'rtl' : 'ltr');
    applyFooterVisibility();
    applyRowColorSettings();
    applyTableTranslations();
    renderRowIconRulesEditor();
    updateSelectionCount();
    updateLastUpdateDisplay();
    renderEventLogPanel();
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

function isTableRTL() {
    return state.settings.rtlTextDirection === true;
}

function t(key, values = {}) {
    return tableText(state.settings.language, key, values);
}

const DEFAULT_RULE_LABELS = Object.freeze({
    warnings: 'Warnings',
    stale: 'Stale source data',
    'price-missing': 'Price unavailable',
    'weight-missing': 'Weight unavailable',
    'out-of-stock': 'Out of stock',
    'in-stock': 'In stock'
});

function localizedRuleLabel(rule, index = 0) {
    const defaultLabel = DEFAULT_RULE_LABELS[rule?.id];
    if (!rule?.label || (defaultLabel && rule.label === defaultLabel)) {
        const key = `ruleLabel_${String(rule?.id || '').replaceAll('-', '_')}`;
        const translated = t(key);
        if (translated !== key) return translated;
    }
    return rule?.label || t('rule', { number: index + 1 });
}

function localizedAppearanceLabel(appearance) {
    if (appearance?.ruleId) {
        const index = state.settings.rowIconRules.findIndex(rule => rule.id === appearance.ruleId);
        if (index >= 0) return localizedRuleLabel(state.settings.rowIconRules[index], index);
    }
    if (!appearance?.label || appearance.label === DEFAULT_ROW_ICON_FALLBACK.label) return t('fallbackProduct');
    return appearance.label;
}

function applyTableTranslations() {
    document.querySelectorAll('[data-table-i18n]').forEach(element => {
        element.textContent = t(element.dataset.tableI18n);
    });
    document.querySelectorAll('[data-table-i18n-placeholder]').forEach(element => {
        element.placeholder = t(element.dataset.tableI18nPlaceholder);
    });
    document.querySelectorAll('[data-table-i18n-title]').forEach(element => {
        element.title = t(element.dataset.tableI18nTitle);
    });
    document.querySelectorAll('[data-table-i18n-aria-label]').forEach(element => {
        element.setAttribute('aria-label', t(element.dataset.tableI18nAriaLabel));
    });
    document.querySelectorAll('[data-dialog-menu]').forEach(button => {
        button.title = t('moreActions');
        button.setAttribute('aria-label', t('moreActions'));
    });
    document.querySelectorAll('.dialog-close-btn').forEach(button => {
        button.title = t('close');
        button.setAttribute('aria-label', t('close'));
    });
    const rowMenu = document.getElementById('rowContextMenu');
    if (rowMenu) {
        rowMenu.setAttribute('aria-label', t('actions'));
        rowMenu.dir = isTableRTL() ? 'rtl' : 'ltr';
    }
    updateSelectionCount();
}

let tableUXSaveTimer = null;
let tableUXPreviewTimer = null;
function scheduleTableUXSave({ header = false, editor = false } = {}) {
    clearTimeout(tableUXSaveTimer);
    tableUXSaveTimer = setTimeout(() => {
        saveSettings();
        if (header) renderTableHeader();
        if (editor) renderRowIconRulesEditor();
        renderTable();
    }, 180);
}

function scheduleTableUXPreview() {
    clearTimeout(tableUXPreviewTimer);
    tableUXPreviewTimer = setTimeout(renderTable, 100);
}

function populateRowIconOptions(select, selected) {
    if (!select) return;
    select.innerHTML = '';
    ROW_ICON_NAMES.forEach(icon => {
        const option = document.createElement('option');
        option.value = icon;
        option.textContent = t(`icon_${icon}`);
        option.selected = icon === selected;
        select.appendChild(option);
    });
}

function createRuleField(labelKey, control) {
    const label = document.createElement('label');
    label.className = 'field-label row-icon-rule-field';
    const caption = document.createElement('span');
    caption.textContent = t(labelKey);
    label.appendChild(caption);
    label.appendChild(control);
    return label;
}

function createRuleIconButton(icon, label, action, disabled = false) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn btn-icon row-icon-rule-action';
    button.innerHTML = iconMarkup(icon);
    button.title = label;
    button.setAttribute('aria-label', label);
    button.disabled = disabled;
    button.addEventListener('click', action);
    return button;
}

function renderRowIconRulesEditor() {
    const container = document.getElementById('rowIconRules');
    if (!container) return;

    state.settings.rowIconRules = normalizeRowIconRules(state.settings.rowIconRules);
    container.innerHTML = '';
    state.settings.rowIconRules.forEach((rule, index) => {
        const card = document.createElement('section');
        card.className = 'row-icon-rule';
        card.dataset.ruleId = rule.id;

        const heading = document.createElement('div');
        heading.className = 'row-icon-rule-heading';
        const title = document.createElement('strong');
        title.textContent = localizedRuleLabel(rule, index);
        const enabledLabel = document.createElement('label');
        enabledLabel.className = 'checkbox-label compact-checkbox';
        const enabled = document.createElement('input');
        enabled.type = 'checkbox';
        enabled.checked = !rule.disabled;
        enabled.setAttribute('aria-label', t('enabled'));
        enabled.addEventListener('change', () => {
            rule.disabled = !enabled.checked;
            scheduleTableUXSave();
        });
        const enabledText = document.createElement('span');
        enabledText.textContent = t('enabled');
        enabledLabel.append(enabled, enabledText);
        const headingActions = document.createElement('div');
        headingActions.className = 'row-icon-rule-actions';
        headingActions.append(
            createRuleIconButton('arrowUp', t('moveUp'), () => moveRowIconRule(index, -1), index === 0),
            createRuleIconButton('arrowDown', t('moveDown'), () => moveRowIconRule(index, 1), index === state.settings.rowIconRules.length - 1),
            createRuleIconButton('trash', t('removeRule'), () => removeRowIconRule(index))
        );
        heading.append(title, enabledLabel, headingActions);

        const controls = document.createElement('div');
        controls.className = 'row-icon-rule-controls';

        const fieldInput = document.createElement('select');
        fieldInput.className = 'select-input';
        const fieldPlaceholder = document.createElement('option');
        fieldPlaceholder.value = '';
        fieldPlaceholder.textContent = t('selectCanonicalField');
        fieldPlaceholder.disabled = true;
        fieldPlaceholder.selected = !rule.field;
        fieldInput.appendChild(fieldPlaceholder);
        CANONICAL_ROW_FIELDS.forEach(field => {
            const option = document.createElement('option');
            option.value = field;
            option.textContent = field;
            option.selected = field === rule.field;
            fieldInput.appendChild(option);
        });
        fieldInput.addEventListener('change', () => {
            rule.field = fieldInput.value;
            scheduleTableUXSave();
        });

        const operatorSelect = document.createElement('select');
        operatorSelect.className = 'select-input';
        ROW_RULE_OPERATORS.forEach(operator => {
            const option = document.createElement('option');
            option.value = operator;
            option.textContent = t(`op_${operator}`);
            option.selected = operator === rule.operator;
            operatorSelect.appendChild(option);
        });
        operatorSelect.addEventListener('change', () => {
            rule.operator = operatorSelect.value;
            scheduleTableUXSave();
        });

        const valueInput = document.createElement('input');
        valueInput.type = 'text';
        valueInput.className = 'text-input';
        valueInput.value = rule.value;
        valueInput.disabled = ['empty', 'not_empty', 'truthy', 'falsy'].includes(rule.operator);
        valueInput.addEventListener('input', () => {
            rule.value = valueInput.value;
            scheduleTableUXPreview();
        });
        valueInput.addEventListener('change', () => scheduleTableUXSave());
        operatorSelect.addEventListener('change', () => {
            valueInput.disabled = ['empty', 'not_empty', 'truthy', 'falsy'].includes(operatorSelect.value);
        });

        const iconSelect = document.createElement('select');
        iconSelect.className = 'select-input';
        populateRowIconOptions(iconSelect, rule.icon);
        iconSelect.addEventListener('change', () => {
            rule.icon = iconSelect.value;
            scheduleTableUXSave();
        });

        const colorInput = document.createElement('input');
        colorInput.type = 'color';
        colorInput.className = 'color-input';
        colorInput.value = rule.color;
        colorInput.addEventListener('input', () => {
            rule.color = colorInput.value;
            scheduleTableUXPreview();
        });
        colorInput.addEventListener('change', () => scheduleTableUXSave());

        const labelInput = document.createElement('input');
        labelInput.type = 'text';
        labelInput.className = 'text-input';
        labelInput.value = localizedRuleLabel(rule, index);
        labelInput.addEventListener('input', () => {
            rule.label = labelInput.value.trim();
            title.textContent = localizedRuleLabel(rule, index);
            scheduleTableUXPreview();
        });
        labelInput.addEventListener('change', () => scheduleTableUXSave());

        controls.append(
            createRuleField('field', fieldInput),
            createRuleField('operator', operatorSelect),
            createRuleField('value', valueInput),
            createRuleField('icon', iconSelect),
            createRuleField('color', colorInput),
            createRuleField('label', labelInput)
        );
        card.append(heading, controls);
        container.appendChild(card);
    });

    const fallback = normalizeRowIconFallback(state.settings.rowIconFallback);
    state.settings.rowIconFallback = fallback;
    populateRowIconOptions(document.getElementById('rowIconFallbackIcon'), fallback.icon);
}

function addRowIconRule() {
    const index = state.settings.rowIconRules.length;
    state.settings.rowIconRules.push(normalizeRowIconRule({
        id: `rule-${Date.now().toString(36)}`,
        field: 'warnings',
        operator: 'not_empty',
        icon: 'info',
        color: '#6366f1',
        label: ''
    }, index));
    renderRowIconRulesEditor();
    scheduleTableUXSave();
}

function moveRowIconRule(index, direction) {
    const target = index + direction;
    if (target < 0 || target >= state.settings.rowIconRules.length) return;
    const [rule] = state.settings.rowIconRules.splice(index, 1);
    state.settings.rowIconRules.splice(target, 0, rule);
    renderRowIconRulesEditor();
    scheduleTableUXSave();
}

function removeRowIconRule(index) {
    state.settings.rowIconRules.splice(index, 1);
    renderRowIconRulesEditor();
    scheduleTableUXSave();
}

function applyFooterVisibility() {
    const showFooter = state.settings.showFooter !== false;
    document.body.classList.toggle('footer-hidden', !showFooter);
    const footer = document.getElementById('appFooter');
    if (footer) footer.hidden = !showFooter;
    renderFooterToggleButton(document.getElementById('footerToggleBtn'), showFooter);
    renderFooterToggleButton(document.getElementById('footerCollapseBtn'), showFooter);
}

function toggleFooterVisibility() {
    state.settings.showFooter = !(state.settings.showFooter !== false);
    applySettings();
    saveSettings();
}

function renderFooterToggleButton(button, showFooter) {
    if (!button) return;
    if (button.dataset.footerToggleBound !== '1') {
        const replacement = button.cloneNode(false);
        replacement.dataset.footerToggleBound = '1';
        replacement.addEventListener('click', handleFooterToggleClick);
        button.replaceWith(replacement);
        button = replacement;
    }
    button.title = showFooter ? 'Hide footer' : 'Show footer';
    button.setAttribute('aria-pressed', String(showFooter));
    button.setAttribute('aria-label', showFooter ? 'Hide footer' : 'Show footer');
    button.innerHTML = showFooter
        ? '<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>'
        : '<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M6 15l6-6 6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
}

function handleFooterToggleClick(event) {
    if (event) {
        event.preventDefault();
        event.stopPropagation();
    }
    toggleFooterVisibility();
}

function formatRelativeTime(date) {
    return localizedRelativeTime(date, state.settings.language);
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
    document.querySelectorAll('[data-dialog-menu]').forEach(menuButton => {
        if (menuButton.dataset.dialogMenuBound) return;
        menuButton.title = t('moreActions');
        menuButton.setAttribute('aria-label', t('moreActions'));
        menuButton.setAttribute('aria-haspopup', 'menu');
        menuButton.setAttribute('aria-expanded', 'false');
        menuButton.addEventListener('click', event => {
            event.stopPropagation();
            openDialogCommandMenu(menuButton.dataset.dialogMenu, menuButton);
        });
        menuButton.dataset.dialogMenuBound = '1';
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
    resetPartialRouteScroll();
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
        resetPartialRouteScroll();
    } catch (error) {
        if (error.name === 'AbortError') return;
        state.router.outlet.innerHTML = `<div class="route-error"><strong>${escapeHtml(t('couldNotLoadPage'))}</strong><span>${escapeHtml(error.message)}</span></div>`;
        resetPartialRouteScroll();
    }
}

function resetPartialRouteScroll() {
    document.querySelector('.main-content')?.scrollTo?.({ top: 0, left: 0, behavior: 'auto' });
    state.router.outlet?.scrollTo?.({ top: 0, left: 0, behavior: 'auto' });
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
    updateSettingsTabVisibility();
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
    const now = audioContext.currentTime;
    const masterGain = audioContext.createGain();
    const compressor = audioContext.createDynamicsCompressor();
    const softFilter = audioContext.createBiquadFilter();
    const delay = audioContext.createDelay(0.24);
    const delayGain = audioContext.createGain();
    const dryGain = audioContext.createGain();

    masterGain.gain.setValueAtTime(0.0001, now);
    masterGain.gain.linearRampToValueAtTime(0.16, now + 0.035);
    masterGain.gain.exponentialRampToValueAtTime(0.0001, now + 0.92);

    compressor.threshold.setValueAtTime(-26, now);
    compressor.knee.setValueAtTime(24, now);
    compressor.ratio.setValueAtTime(3, now);
    compressor.attack.setValueAtTime(0.012, now);
    compressor.release.setValueAtTime(0.18, now);

    softFilter.type = 'lowpass';
    softFilter.frequency.setValueAtTime(5200, now);
    softFilter.Q.setValueAtTime(0.35, now);

    delay.delayTime.setValueAtTime(0.085, now);
    delayGain.gain.setValueAtTime(0.12, now);
    dryGain.gain.setValueAtTime(0.88, now);

    compressor.connect(softFilter);
    softFilter.connect(audioContext.destination);
    masterGain.connect(dryGain);
    dryGain.connect(compressor);
    masterGain.connect(delay);
    delay.connect(delayGain);
    delayGain.connect(compressor);
    if (audioContext.state === 'suspended') {
        audioContext.resume().catch(() => {});
    }

    const tones = [
        { frequency: 659.25, start: 0.00, duration: 0.42, peak: 0.34, attack: 0.028 },
        { frequency: 987.77, start: 0.12, duration: 0.46, peak: 0.28, attack: 0.032 },
        { frequency: 493.88, start: 0.02, duration: 0.54, peak: 0.075, attack: 0.04 },
        { frequency: 1318.51, start: 0.03, duration: 0.28, peak: 0.045, attack: 0.02 },
        { frequency: 1975.53, start: 0.16, duration: 0.24, peak: 0.035, attack: 0.02 }
    ];

    tones.forEach(tone => {
        const oscillator = audioContext.createOscillator();
        const noteGain = audioContext.createGain();
        const startTime = now + 0.025 + tone.start;
        const endTime = startTime + tone.duration;

        oscillator.type = 'sine';
        oscillator.frequency.setValueAtTime(tone.frequency, startTime);
        oscillator.detune.setValueAtTime(tone.detune || 0, startTime);
        noteGain.gain.setValueAtTime(0.0001, startTime);
        noteGain.gain.linearRampToValueAtTime(tone.peak, startTime + tone.attack);
        noteGain.gain.exponentialRampToValueAtTime(tone.peak * 0.45, startTime + tone.duration * 0.45);
        noteGain.gain.exponentialRampToValueAtTime(0.0001, endTime);

        oscillator.connect(noteGain);
        noteGain.connect(masterGain);
        oscillator.start(startTime);
        oscillator.stop(endTime + 0.05);
    });

    setTimeout(() => {
        audioContext.close().catch(() => {});
    }, 1100);
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
    const displayTitle = options.titleKey
        ? t(options.titleKey, options.titleValues || {})
        : (title || 'Patris Export');
    const displayMessage = options.messageKey
        ? t(options.messageKey, options.messageValues || {})
        : (message || '');
    if (options.log !== false) {
        recordEventLog({
            title: displayTitle,
            titleKey: options.titleKey,
            titleValues: options.titleValues,
            message: displayMessage,
            messageKey: options.messageKey,
            messageValues: options.messageValues,
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
    titleEl.textContent = displayTitle;

    const messageEl = document.createElement('div');
    messageEl.className = 'app-toast-message';
    messageEl.textContent = displayMessage;

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
    const changes = isEventLogChangeDetails(entry.changes) ? entry.changes : null;
    const content = normalizeEventLogContent(entry, { title: 'Patris Export event' });
    const logEntry = {
        id: entry.id || createTabId(),
        time: entry.timestamp || new Date().toISOString(),
        level: normalizeEventLevel(entry.level),
        type: String(entry.type || 'event'),
        source: String(entry.source || entry.type || 'web-ui'),
        ...content,
        details: entry.details ? String(entry.details) : '',
        ...(changes ? { changes } : {})
    };
    state.eventLog.unshift(logEntry);
    state.eventLog = retainRecentEventLogChanges(
        state.eventLog.slice(0, MAX_EVENT_LOG_ENTRIES),
        MAX_DETAILED_EVENT_LOG_ENTRIES
    );
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
        const compacted = retainRecentEventLogChanges(state.eventLog, 4);
        try {
            localStorage.setItem(EVENT_LOG_STORAGE_KEY, JSON.stringify(compacted));
            state.eventLog = compacted;
        } catch (compactedError) {
            const summariesOnly = retainRecentEventLogChanges(state.eventLog, 0);
            try {
                localStorage.setItem(EVENT_LOG_STORAGE_KEY, JSON.stringify(summariesOnly));
                state.eventLog = summariesOnly;
            } catch (summaryError) {
                console.warn('Failed to persist compacted event log:', compactedError, summaryError);
            }
        }
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

function eventLogChangeLabels() {
    return {
        added: t('eventLogAdded'),
        modified: t('eventLogModified'),
        deleted: t('eventLogDeleted'),
        row: t('eventLogRow'),
        field: t('eventLogField'),
        before: t('eventLogBefore'),
        after: t('eventLogAfter'),
        value: t('eventLogValue'),
        noFields: t('eventLogNoFields'),
        moreFields: t('eventLogMoreFields'),
        boundedPreview: t('eventLogBoundedPreview')
    };
}

function eventLogEntrySummaryMarkup(entry, disclosure = '') {
    const parsedTime = new Date(entry.time);
    const displayTime = Number.isNaN(parsedTime.getTime()) ? entry.time : formatDateTime(parsedTime);
    const title = eventLogLocalizedText(entry, 'title', state.settings.language);
    const message = eventLogLocalizedText(entry, 'message', state.settings.language);
    const type = eventLogTokenLabel(entry.type, state.settings.language, 'type');
    const source = eventLogTokenLabel(entry.source, state.settings.language, 'source');
    return `
        <span class="event-log-entry-meta">
            <time datetime="${escapeHtml(entry.time)}">${escapeHtml(displayTime)}</time>
            <span>${escapeHtml(type)}</span>
            <span>${escapeHtml(source)}</span>
        </span>
        <span class="event-log-entry-body">
            <strong>${escapeHtml(title)}</strong>
            ${message ? `<span class="event-log-entry-message">${escapeHtml(message)}</span>` : ''}
            ${!disclosure && entry.details ? `<code>${escapeHtml(entry.details)}</code>` : ''}
            ${!disclosure && entry.changeDetailsExpired ? `<span class="event-log-detail-expired">${escapeHtml(t('eventLogDetailsExpired'))}</span>` : ''}
        </span>
        ${disclosure ? `
            <span class="event-log-entry-disclosure">
                <span>${escapeHtml(disclosure)}</span>
                ${iconMarkup('chevron')}
            </span>` : ''}
    `;
}

function renderEventLogPanel() {
    const summary = document.getElementById('eventLogSummary');
    const list = document.getElementById('eventLogList');
    if (!summary || !list) return;

    const openEntries = new Set(
        Array.from(list.querySelectorAll('details.event-log-entry[open][data-event-id]'))
            .map(entry => entry.dataset.eventId)
    );
    const activeSummary = document.activeElement instanceof Element
        ? document.activeElement.closest('summary.event-log-entry-summary')
        : null;
    const focusedEventId = activeSummary?.parentElement?.dataset?.eventId || '';
    const direction = isTableRTL() ? 'rtl' : 'ltr';
    summary.setAttribute('dir', direction);
    list.setAttribute('dir', direction);

    const counts = state.eventLog.reduce((acc, entry) => {
        acc[entry.level] = (acc[entry.level] || 0) + 1;
        return acc;
    }, {});
    summary.innerHTML = `
        <div><span>${escapeHtml(t('eventLogTotal'))}</span><strong>${state.eventLog.length.toLocaleString()}</strong></div>
        <div><span>${escapeHtml(t('eventLogUpdates'))}</span><strong>${(counts.update || 0).toLocaleString()}</strong></div>
        <div><span>${escapeHtml(t('eventLogWarnings'))}</span><strong>${((counts.warning || 0) + (counts.error || 0)).toLocaleString()}</strong></div>
    `;

    if (state.eventLog.length === 0) {
        list.innerHTML = `<div class="event-log-empty">${escapeHtml(t('eventLogEmpty'))}</div>`;
        return;
    }

    const changeLabels = eventLogChangeLabels();
    list.innerHTML = state.eventLog.map(entry => {
        const level = normalizeEventLevel(entry.level);
        if (!isEventLogChangeDetails(entry.changes)) {
            return `
                <article class="event-log-entry ${escapeHtml(level)}">
                    <div class="event-log-entry-static">
                        ${eventLogEntrySummaryMarkup(entry)}
                    </div>
                </article>`;
        }

        const disclosure = eventLogDisclosureText(entry.changes, t('eventLogViewChanges'));
        return `
            <details class="event-log-entry ${escapeHtml(level)}" data-event-id="${escapeHtml(entry.id)}">
                <summary class="event-log-entry-summary">
                    ${eventLogEntrySummaryMarkup(entry, disclosure)}
                </summary>
                <div class="event-log-entry-detail-content">
                    ${eventLogChangeDetailsMarkup(entry.changes, changeLabels)}
                    ${entry.details ? `
                        <div class="event-log-technical-details">
                            <strong>${escapeHtml(t('eventLogTechnicalDetails'))}</strong>
                            <code>${escapeHtml(entry.details)}</code>
                        </div>` : ''}
                </div>
            </details>`;
    }).join('');

    let summaryToRefocus = null;
    list.querySelectorAll('details.event-log-entry[data-event-id]').forEach(entry => {
        if (openEntries.has(entry.dataset.eventId)) entry.open = true;
        if (focusedEventId && entry.dataset.eventId === focusedEventId) {
            summaryToRefocus = entry.querySelector('summary.event-log-entry-summary');
        }
    });
    summaryToRefocus?.focus();
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
            title.textContent = t('loadingSource');
            message.textContent = t('uploadingSource');
        } else if (mode === 'invalid') {
            title.textContent = t('unsupportedFile');
            message.textContent = t('unsupportedFileHelp');
        } else {
            title.textContent = t('dropDatabase');
            message.textContent = t('dropDatabaseDescription');
        }
    }
}

async function uploadDroppedSource(file) {
    if (!file) {
        return;
    }
    if (!isSupportedSourceFile(file)) {
        setDropOverlayVisible(true, 'invalid');
        showInAppToast(t('unsupportedFile'), t('unsupportedFileHelp'), {
            titleKey: 'unsupportedFile',
            messageKey: 'unsupportedFileHelp',
            error: true,
            source: 'source_drop',
            eventType: 'source_switch'
        });
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
        const sourceLoadedValues = {
            file: payload.file || file.name,
            count: payload.records ?? t('unknown')
        };
        showInAppToast(t('sourceLoaded'), t('sourceLoadedMessage', sourceLoadedValues), {
            titleKey: 'sourceLoaded',
            messageKey: 'sourceLoadedMessage',
            messageValues: sourceLoadedValues,
            broadcastToTabs: true,
            source: 'source_drop',
            eventType: 'source_switch',
            level: 'success'
        });

        if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
            await fetchInitialData();
            await fetchFileInfo();
        }
    } catch (error) {
        console.error('Failed to switch dropped source:', error);
        showInAppToast(t('sourceSwitchFailed'), error.message, { titleKey: 'sourceSwitchFailed', error: true, source: 'source_drop', eventType: 'source_switch' });
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
    updateSettingsTabVisibility();
}

function setActiveSettingsTab(target) {
    if (!target) target = 'ui';
    const requestedTab = document.querySelector(`[data-settings-tab="${target}"]`);
    if (requestedTab?.hidden) {
        state.revealedSettingsTabs.add(target);
        updateSettingsTabVisibility({ preserveActive: true });
    }
    const tab = document.querySelector(`[data-settings-tab="${target}"]:not([hidden])`)
        || document.querySelector('[data-settings-tab="ui"]');
    const paneName = tab?.dataset.settingsTab || 'ui';
    document.querySelectorAll('[data-settings-tab]').forEach(item => {
        item.classList.toggle('active', item === tab);
    });
    document.querySelectorAll('[data-settings-pane]').forEach(pane => {
        pane.classList.toggle('active', pane.dataset.settingsPane === paneName);
    });
}

function updateSettingsTabVisibility(options = {}) {
    const cfg = state.config || {};
    const visibleTabs = new Set(['ui']);
    const isCustomized = (section, key, defaultValue) => {
        const value = cfg[section]?.[key];
        return typeof value !== 'undefined' && value !== defaultValue;
    };

    if (state.fileName || cfg.database?.path || cfg.database?.charmap || cfg.database?.direct_access || cfg.database?.rtl_conversion || cfg.database?.raw) {
        visibleTabs.add('database');
    }
    if (
        cfg.notifications?.enabled ||
        cfg.notifications?.client_connected ||
        cfg.notifications?.client_disconnected ||
        cfg.notifications?.file_updated ||
        cfg.notifications?.row_updated ||
        cfg.notifications?.include_row_values ||
        state.settings.playNotificationSound
    ) {
        visibleTabs.add('notifications');
    }
    if (
        cfg.runtime?.debug ||
        isCustomized('runtime', 'temp_dir', 'system') ||
        isCustomized('runtime', 'temp_strategy', 'auto') ||
        isCustomized('runtime', 'temp_memory_limit_mb', 100)
    ) {
        visibleTabs.add('runtime');
    }
    if (cfg.server?.ipc?.enabled || cfg.server?.http === false || state.revealedSettingsTabs.has('server')) {
        visibleTabs.add('server');
    }
    state.revealedSettingsTabs.forEach(tabName => visibleTabs.add(tabName));

    document.querySelectorAll('[data-settings-tab]').forEach(tab => {
        const show = visibleTabs.has(tab.dataset.settingsTab);
        tab.hidden = !show;
        tab.setAttribute('aria-hidden', show ? 'false' : 'true');
    });
    document.querySelectorAll('[data-settings-pane]').forEach(pane => {
        const show = visibleTabs.has(pane.dataset.settingsPane);
        pane.hidden = !show;
        pane.setAttribute('aria-hidden', show ? 'false' : 'true');
    });

    const activeTab = document.querySelector('[data-settings-tab].active');
    if (!options.preserveActive && activeTab?.hidden) {
        setActiveSettingsTab('ui');
    }
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
    updateStatus('connected', t('updatingUI'));
    showInAppToast(t('updatingInterface'), t('updatingInterfaceMessage'), {
        titleKey: 'updatingInterface',
        messageKey: 'updatingInterfaceMessage',
        source: 'resource_update',
        eventType: 'resource_update',
        level: 'update'
    });

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
        const patrisText = patris.running ? t('patrisRunning', { count: patris.count }) : t('patrisNotRunning');
        const lockText = fileAccess.in_use ? t('databaseLockedCount', { count: fileAccess.count }) : t('databaseUnlocked');
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
            showInAppToast(t('nativeToastUnavailable'), result.native_error, { titleKey: 'nativeToastUnavailable', error: true, source: 'native_toast', eventType: 'toast', nativeError: result.native_error });
        }
    } catch (error) {
        showInAppToast(t('toastRequestFailed'), error.message, { titleKey: 'toastRequestFailed', error: true, source: 'native_toast', eventType: 'toast' });
    }
}

async function requestSourceRefresh() {
    const button = document.getElementById('refreshNowBtn');
    if (button) {
        button.disabled = true;
        button.textContent = t('refreshing');
    }
    try {
        if (state.ws && state.ws.readyState === WebSocket.OPEN) {
            state.ws.send(JSON.stringify({ type: 'refresh' }));
            showInAppToast(t('refreshRequested'), t('refreshRequestedMessage'), { titleKey: 'refreshRequested', messageKey: 'refreshRequestedMessage', broadcastToTabs: true, source: 'manual_refresh', eventType: 'refresh', level: 'update' });
        } else {
            await fetchInitialData();
            showInAppToast(t('refreshed'), t('refreshedMessage'), { titleKey: 'refreshed', messageKey: 'refreshedMessage', broadcastToTabs: true, source: 'manual_refresh', eventType: 'refresh', level: 'success' });
        }
    } catch (error) {
        console.error('Failed to refresh data source:', error);
        showInAppToast(t('refreshFailed'), error.message, { titleKey: 'refreshFailed', error: true, source: 'manual_refresh', eventType: 'refresh' });
    } finally {
        if (button) {
            setTimeout(() => {
                button.disabled = false;
                button.innerHTML = `${iconMarkup('refresh')}<span data-table-i18n="refresh">${t('refresh')}</span>`;
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
        updateStatus('connected', t('connected'));
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
        updateStatus('disconnected', t('error'));
    };
    
    state.ws.onclose = () => {
        console.log('WebSocket disconnected');
        updateStatus('disconnected', t('disconnected'));
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
        state.catalogProducts = normalizeRecordsPayload(data.added || []);
        if (data.contract && Array.isArray(data.contract.categories)) {
            state.catalogCategories = normalizeCategoriesPayload(data.contract.categories);
            state.catalogCategoriesAvailable = true;
        } else {
            state.catalogCategories = [];
            state.catalogCategoriesAvailable = false;
            state.catalogView = 'products';
        }
        selectActiveCatalogRows();
        refreshRecordSelectionKeys({ reset: true });
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
        const keyField = String(data.key_field || data.keyField || 'Code');
        const changeCounts = {
            added: Array.isArray(data.added) ? data.added.length : 0,
            modified: Array.isArray(data.modified) ? data.modified.length : 0,
            deleted: Array.isArray(data.deleted) ? data.deleted.length : 0
        };
        const totalChanges = changeCounts.added + changeCounts.modified + changeCounts.deleted;
        const logChanges = createEventLogChangeSnapshot(data, state.catalogProducts);
        
        // Handle deleted records by the update's declared identity field.
        if (changeCounts.deleted > 0) {
            const deletedKeys = new Set(data.deleted.map((value, index) => (
                deletedRecordIdentityKey(value, keyField, index)
            )));
            state.catalogProducts = state.catalogProducts.filter((record, index) => {
                const key = recordIdentityKey(record, keyField, index);
                return !deletedKeys.has(key);
            });
        }
        
        // Handle added records
        if (changeCounts.added > 0) {
            const startIndex = state.catalogProducts.length;
            state.catalogProducts.push(...normalizeRecordsPayload(data.added));
            
            // Mark added records as changed
            data.added.forEach((_, i) => {
                changedIndices.add(startIndex + i);
            });
        }
        
        // Handle modified records (if any)
        if (changeCounts.modified > 0) {
            data.modified.forEach((change, changeIndex) => {
                const key = modifiedRecordIdentityKey(change, keyField, changeIndex);
                const index = state.catalogProducts.findIndex((record, recordIndex) => (
                    recordIdentityKey(record, keyField, recordIndex) === key
                ));
                if (index !== -1) {
                    // Merge the new values into the existing record
                    // Note: The server sends new_values (snake_case) not newValues (camelCase)
                    const newValues = change.new_values || change.newValues || {};
                    Object.assign(state.catalogProducts[index], newValues);
                    changedIndices.add(index);
                    console.log(`Updated record ${key}:`, newValues);
                }
            });
        }
        if (data.contract && Array.isArray(data.contract.categories)) {
            state.catalogCategories = normalizeCategoriesPayload(data.contract.categories);
            state.catalogCategoriesAvailable = true;
            if (state.catalogView === 'categories') {
                state.fields = [];
                state.fieldTypes = {};
                state.fieldStats = {};
            }
        }
        selectActiveCatalogRows();
        if (state.catalogView === 'categories') changedIndices.clear();
        refreshRecordSelectionKeys();
        
        // Trigger notifications if there were changes
        if (totalChanges > 0) {
            // Play notification sound
            playNotificationSound();
            
            // Flash title with change info (use detailed description if available)
            // Note: Title and favicon flashing always occur regardless of audio settings
            // This is by design to provide visual feedback even when sound is disabled
            const titleMessage = t('eventLogChangeSummary', changeCounts);
            flashTitle(titleMessage);
            
            // Flash favicon
            flashFavicon();

            recordEventLog({
                titleKey: 'eventLogRowsChanged',
                messageKey: 'eventLogChangeSummary',
                messageValues: changeCounts,
                level: 'update',
                type: 'row_updated',
                source: 'websocket',
                timestamp: data.timestamp,
                details: `added=${data.added?.length || 0} modified=${data.modified?.length || 0} deleted=${data.deleted?.length || 0}`,
                changes: logChanges
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
            const messageKey = diff.messageKey || 'settingsReloadedMessage';
            const messageValues = diff.messageValues || {};
            showInAppToast(t('settingsReloaded'), t(messageKey, messageValues), {
                titleKey: 'settingsReloaded',
                messageKey,
                messageValues,
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
            footerFile.textContent = t('unknown');
            footerFile.removeAttribute('title');
        }
        if (headerFile) {
            headerFile.textContent = t('unknown');
            headerFile.removeAttribute('title');
        }
        if (headerFileChip) headerFileChip.title = t('currentSourceFile');
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
    const wsState = state.ws ? [t('connecting'), t('open'), t('closing'), t('closed')][state.ws.readyState] || t('unknown') : t('notStarted');
    const file = state.fileName || t('unknown');
    const patris = state.processStatus?.patris81 || {};
    const fileAccess = state.processStatus?.file_access || {};
    details.innerHTML = `
        <div><span>${escapeHtml(t('status'))}</span><strong>${escapeHtml(state.connectionStatus.text)}</strong></div>
        <div><span>${escapeHtml(t('webSocket'))}</span><strong>${escapeHtml(wsState)}</strong></div>
        <div><span>${escapeHtml(t('source'))}</span><strong title="${escapeHtml(file)}">${escapeHtml(file.split('/').pop().split('\\').pop() || file)}</strong></div>
        <div><span>Patris81</span><strong>${patris.running ? `${escapeHtml(t('running'))} (${patris.count || 1})` : escapeHtml(t('notRunning'))}</strong></div>
        <div><span>${escapeHtml(t('databaseLock'))}</span><strong>${fileAccess.in_use ? `${escapeHtml(t('locked'))} (${fileAccess.count || 1})` : escapeHtml(t('unlocked'))}</strong></div>
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
    state.fields = deriveGridFields(state.records);
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
    const codeIndex = state.fields.findIndex(field => ['code', 'product_code'].includes(String(field).toLowerCase()));
    if (codeIndex > 0) {
        const [identityField] = state.fields.splice(codeIndex, 1);
        state.fields.unshift(identityField);
    }
}

function refreshRecordSelectionKeys({ reset = false } = {}) {
    if (reset) state.recordSelectionKeys = new WeakMap();
    state.recordSelectionKeys = assignStableRecordKeys(state.records, state.recordSelectionKeys);
    state.rovingRowKey = resolvedRovingKey(state.rovingRowKey, state.records.map(recordSelectionKey));
}

function recordSelectionKey(record) {
    if (!record || typeof record !== 'object') return '';
    let key = state.recordSelectionKeys.get(record);
    if (!key) {
        const base = duplicateSafeRecordKeys([record])[0];
        key = base || `row:${Date.now().toString(36)}`;
        state.recordSelectionKeys.set(record, key);
    }
    return key;
}

function captureTableHeaderFocus(thead) {
    const active = document.activeElement;
    if (!thead?.contains(active)) return null;
    if (active.matches('.selection-header-cell .table-selection-checkbox')) return { kind: 'selection' };
    if (active.closest('[data-header-control="clear-filters"]')) return { kind: 'clear-filters' };
    const filter = active.closest('th[data-filter-field]');
    if (filter) {
        const controls = [...filter.querySelectorAll('input, select, button, [tabindex]')];
        return { kind: 'filter', field: filter.dataset.filterField, index: Math.max(0, controls.indexOf(active)) };
    }
    const header = active.closest('th[data-field]');
    if (!header) return null;
    return {
        kind: active.matches('.column-resizer') ? 'resizer' : 'header',
        field: header.dataset.field
    };
}

function restoreTableHeaderFocus(thead, target) {
    if (!target) return;
    let element = null;
    if (target.kind === 'selection') {
        element = thead.querySelector('.selection-header-cell .table-selection-checkbox');
    } else if (target.kind === 'clear-filters') {
        element = thead.querySelector('[data-header-control="clear-filters"] button');
    } else if (target.kind === 'filter') {
        const filter = [...thead.querySelectorAll('th[data-filter-field]')]
            .find(candidate => candidate.dataset.filterField === target.field);
        element = filter?.querySelectorAll('input, select, button, [tabindex]')?.[target.index];
    } else {
        const header = [...thead.querySelectorAll('th[data-field]')]
            .find(candidate => candidate.dataset.field === target.field);
        element = target.kind === 'resizer' ? header?.querySelector('.column-resizer') : header;
    }
    element?.focus({ preventScroll: true });
}

// Render table header with filters
function renderTableHeader() {
    const thead = document.getElementById('tableHead');
    const focusTarget = captureTableHeaderFocus(thead);
    thead.innerHTML = '';

    const visibleFields = state.fields.filter(field => !state.hiddenColumns.has(field));
    renderColumnLayout(visibleFields);
    const visibleWarehouseFields = visibleFields.filter(field => isWarehouseColumnField(field));
    const hasWarehouseFields = visibleWarehouseFields.length > 0;
    const frozenField = state.settings.freezeFirstColumn ? visibleFields[0] : '';

    const groupRow = document.createElement('tr');
    groupRow.className = 'warehouse-group-row';
    const columnRow = document.createElement('tr');
    columnRow.className = hasWarehouseFields ? 'column-header-row has-warehouse-group' : 'column-header-row';
    const filterRow = document.createElement('tr');
    filterRow.className = 'filter-row';

    groupRow.appendChild(createSelectionHeaderCell(hasWarehouseFields ? 2 : 1));
    const selectionFilter = document.createElement('th');
    selectionFilter.className = 'selection-column selection-filter-cell';
    selectionFilter.setAttribute('aria-hidden', 'true');
    filterRow.appendChild(selectionFilter);

    let processedWarehouses = false;

    visibleFields.forEach(field => {
        if (isWarehouseColumnField(field) && !processedWarehouses) {
            if (visibleWarehouseFields.length > 0 && hasWarehouseFields) {
                const groupTh = document.createElement('th');
                groupTh.textContent = displayStockGroupName();
                groupTh.setAttribute('colspan', visibleWarehouseFields.length);
                groupTh.className = 'warehouse-group-header';
                groupTh.tabIndex = 0;
                groupTh.setAttribute('aria-haspopup', 'menu');
                groupTh.addEventListener('contextmenu', event => {
                    event.preventDefault();
                    openWarehouseGroupContextMenu({ point: { x: event.clientX, y: event.clientY }, trigger: groupTh, focusMenu: true });
                });
                groupTh.addEventListener('keydown', event => {
                    if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
                        event.preventDefault();
                        openWarehouseGroupContextMenu({ trigger: groupTh, focusMenu: true });
                    }
                });
                groupRow.appendChild(groupTh);

                visibleWarehouseFields.forEach(warehouseField => {
                    const th = createHeaderCell(warehouseField, {
                        label: displayFieldName(warehouseField),
                        className: 'warehouse-column'
                    });
                    columnRow.appendChild(th);

                    const filterTh = document.createElement('th');
                    filterTh.className = 'warehouse-column';
                    filterTh.dataset.filterField = warehouseField;
                    filterTh.appendChild(createFilterControl(warehouseField));
                    filterRow.appendChild(filterTh);
                });
            }

            processedWarehouses = true;
        } else if (!isWarehouseColumnField(field)) {
            const th = createHeaderCell(field, {
                label: displayFieldName(field),
                rowSpan: hasWarehouseFields ? 2 : 1,
                className: [field === frozenField ? 'sticky-column' : '', String(field).toLowerCase() === 'warnings' ? 'warning-column' : ''].filter(Boolean).join(' ')
            });
            groupRow.appendChild(th);
            
            // Create filter cell for this field
            const filterTh = document.createElement('th');
            filterTh.dataset.filterField = field;
            if (field === frozenField) {
                filterTh.classList.add('sticky-column');
            }
            if (String(field).toLowerCase() === 'warnings') filterTh.classList.add('warning-column');
            filterTh.appendChild(createFilterControl(field));
            filterRow.appendChild(filterTh);
        }
    });
    
    // Add actions column
    const actionsHeader = document.createElement('th');
    actionsHeader.textContent = t('actions');
    actionsHeader.className = 'actions-column';
    if (hasWarehouseFields) {
        actionsHeader.setAttribute('rowspan', '2');
    }
    groupRow.appendChild(actionsHeader);
    
    const actionsFilter = document.createElement('th');
    actionsFilter.className = 'actions-column';
    actionsFilter.dataset.headerControl = 'clear-filters';
    // Add clear all filters button
    const clearBtn = document.createElement('button');
    clearBtn.className = 'btn-clear-filters';
    clearBtn.innerHTML = `
        <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M5 7h14" />
            <path d="M10 11v6" />
            <path d="M14 11v6" />
            <path d="M9 7V5h6v2" />
            <path d="M7 7l1 13h8l1-13" />
        </svg>
        <span>${t('clearFilters')}</span>
    `;
    clearBtn.title = t('clearAllFilters');
    clearBtn.setAttribute('aria-label', t('clearAllFilters'));
    clearBtn.addEventListener('click', clearAllFilters);
    actionsFilter.appendChild(clearBtn);
    filterRow.appendChild(actionsFilter);

    thead.appendChild(groupRow);
    if (hasWarehouseFields) {
        thead.appendChild(columnRow);
    }
    thead.appendChild(filterRow);
    restoreTableHeaderFocus(thead, focusTarget);
}

const SELECTION_COLUMN_WIDTH = 68;
const ACTIONS_COLUMN_WIDTH = 60;

function renderColumnLayout(visibleFields) {
    const table = document.getElementById('dataTable');
    if (!table) return;
    table.querySelector('colgroup')?.remove();
    const colgroup = document.createElement('colgroup');

    const selectionCol = document.createElement('col');
    selectionCol.className = 'selection-column-layout';
    selectionCol.style.width = `${SELECTION_COLUMN_WIDTH}px`;
    colgroup.appendChild(selectionCol);

    visibleFields.forEach(field => {
        const col = document.createElement('col');
        col.dataset.columnField = field;
        col.style.width = `${columnWidth(field)}px`;
        colgroup.appendChild(col);
    });

    const actionsCol = document.createElement('col');
    actionsCol.className = 'actions-column-layout';
    actionsCol.style.width = `${ACTIONS_COLUMN_WIDTH}px`;
    colgroup.appendChild(actionsCol);
    table.insertBefore(colgroup, table.firstChild);
    updateTablePixelWidth(visibleFields);
}

function columnWidth(field) {
    return clampColumnWidth(state.settings.columnWidths?.[canonicalColumnKey(field)], defaultColumnWidth(field));
}

function updateTablePixelWidth(visibleFields = state.fields.filter(field => !state.hiddenColumns.has(field))) {
    const table = document.getElementById('dataTable');
    if (!table) return;
    const width = SELECTION_COLUMN_WIDTH + ACTIONS_COLUMN_WIDTH
        + visibleFields.reduce((total, field) => total + columnWidth(field), 0);
    table.style.width = `${width}px`;
}

function createSelectionHeaderCell(rowSpan) {
    const th = document.createElement('th');
    th.className = 'selection-column selection-header-cell';
    if (rowSpan > 1) th.setAttribute('rowspan', String(rowSpan));
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'table-selection-checkbox';
    checkbox.setAttribute('aria-label', t('selectAll'));
    checkbox.title = t('selectAll');
    const summary = selectionSummary(state.selectedKeys, state.filteredRecords, recordSelectionKey);
    checkbox.checked = summary.checked;
    checkbox.indeterminate = summary.indeterminate;
    checkbox.disabled = summary.selectable === 0;
    checkbox.addEventListener('click', event => event.stopPropagation());
    checkbox.addEventListener('change', () => setFilteredSelection(checkbox.checked));
    th.appendChild(checkbox);
    return th;
}

function setFilteredSelection(selected) {
    state.filteredRecords.forEach(record => {
        const key = recordSelectionKey(record);
        if (!key) return;
        if (selected) state.selectedKeys.add(key);
        else state.selectedKeys.delete(key);
    });
    renderTableHeader();
    renderTable();
    updateSelectionCount();
}

function updateSelectionCount() {
    pruneSelectedKeys(state.selectedKeys, state.records, recordSelectionKey);
    const count = state.selectedKeys.size;
    const element = document.getElementById('selectionCount');
    if (!element) return;
    element.hidden = count === 0;
    element.textContent = count ? t('selectedCount', { count }) : '';
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
    sortIndicator.innerHTML = iconMarkup('chevron');
    sortIndicator.classList.toggle('active', state.sortField === field);
    sortIndicator.classList.toggle('descending', state.sortField === field && state.sortDirection === 'desc');
    sortContainer.appendChild(sortIndicator);

    th.appendChild(sortContainer);
    th.dataset.field = field;
    th.tabIndex = 0;
    th.setAttribute('aria-haspopup', 'menu');
    th.setAttribute('aria-sort', state.sortField === field ? (state.sortDirection === 'asc' ? 'ascending' : 'descending') : 'none');
    th.addEventListener('click', event => {
        if (!event.target.closest('.column-resizer')) sortByField(field);
    });
    th.addEventListener('keydown', event => {
        if ((event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) && event.target === th) {
            event.preventDefault();
            openHeaderCommandMenu(field, { trigger: th, focusMenu: true });
        } else if ((event.key === 'Enter' || event.key === ' ') && event.target === th) {
            event.preventDefault();
            sortByField(field);
        }
    });
    th.addEventListener('contextmenu', event => {
        if (event.target.closest('.column-resizer')) return;
        event.preventDefault();
        openHeaderCommandMenu(field, {
            point: { x: event.clientX, y: event.clientY },
            trigger: th,
            focusMenu: true
        });
    });
    th.appendChild(createColumnResizer(field));
    return th;
}

function createColumnResizer(field) {
    const handle = document.createElement('span');
    handle.className = 'column-resizer';
    handle.tabIndex = 0;
    handle.setAttribute('role', 'separator');
    handle.setAttribute('aria-orientation', 'vertical');
    handle.setAttribute('aria-label', t('resizeColumn', { column: displayFieldName(field) }));
    handle.setAttribute('aria-description', t('resizeHelp'));
    handle.setAttribute('aria-valuemin', '80');
    handle.setAttribute('aria-valuemax', '480');
    handle.setAttribute('aria-valuenow', String(columnWidth(field)));

    handle.addEventListener('click', event => event.stopPropagation());
    handle.addEventListener('dblclick', event => {
        event.preventDefault();
        event.stopPropagation();
        resetColumnWidth(field);
    });
    handle.addEventListener('keydown', event => {
        const next = keyboardColumnWidth(columnWidth(field), event.key, isTableRTL());
        if (next === undefined) return;
        event.preventDefault();
        event.stopPropagation();
        if (next === null) {
            resetColumnWidth(field);
            return;
        }
        setColumnWidth(field, next, { persist: true });
        handle.setAttribute('aria-valuenow', String(next));
    });
    handle.addEventListener('pointerdown', event => {
        if (event.button !== 0) return;
        event.preventDefault();
        event.stopPropagation();
        const startX = event.clientX;
        const startWidth = columnWidth(field);
        handle.classList.add('resizing');
        handle.setPointerCapture?.(event.pointerId);
        setColumnResizeGuide(event.clientX, true);

        const move = moveEvent => {
            const width = resizedColumnWidth(startWidth, moveEvent.clientX - startX, isTableRTL());
            setColumnWidth(field, width, { persist: false });
            handle.setAttribute('aria-valuenow', String(width));
            setColumnResizeGuide(moveEvent.clientX, true);
        };
        const end = endEvent => {
            handle.classList.remove('resizing');
            setColumnResizeGuide(0, false);
            handle.releasePointerCapture?.(endEvent.pointerId);
            handle.removeEventListener('pointermove', move);
            handle.removeEventListener('pointerup', end);
            handle.removeEventListener('pointercancel', end);
            saveSettings();
        };
        handle.addEventListener('pointermove', move);
        handle.addEventListener('pointerup', end);
        handle.addEventListener('pointercancel', end);
    });
    return handle;
}

function setColumnResizeGuide(clientX, visible) {
    const guide = document.getElementById('columnResizeGuide');
    const container = document.querySelector('.table-container');
    if (!guide || !container || !visible) {
        if (guide) guide.hidden = true;
        return;
    }
    const rect = container.getBoundingClientRect();
    guide.style.left = `${Math.min(rect.right, Math.max(rect.left, clientX))}px`;
    guide.style.top = `${rect.top}px`;
    guide.style.height = `${rect.height}px`;
    guide.hidden = false;
}

function setColumnWidth(field, width, { persist = false } = {}) {
    const normalized = clampColumnWidth(width, defaultColumnWidth(field));
    const key = canonicalColumnKey(field);
    state.settings.columnWidths[key] = normalized;
    if (key !== field) delete state.settings.columnWidths[field];
    const col = [...document.querySelectorAll('#dataTable col[data-column-field]')]
        .find(element => element.dataset.columnField === field);
    if (col) col.style.width = `${normalized}px`;
    updateTablePixelWidth();
    if (persist) saveSettings();
}

function resetColumnWidth(field) {
    delete state.settings.columnWidths[canonicalColumnKey(field)];
    delete state.settings.columnWidths[field];
    renderTableHeader();
    saveSettings();
}

function resetAllColumnWidths() {
    state.settings.columnWidths = {};
    renderTableHeader();
    saveSettings();
    showInAppToast(t('widthsReset'), '', { titleKey: 'widthsReset', source: 'table_settings', eventType: 'table_settings' });
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
        select.setAttribute('aria-label', t('filterColumn', { column: displayFieldName(field) }));
        
        const defaultOption = document.createElement('option');
        defaultOption.value = '';
        defaultOption.textContent = t('all');
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
        container.appendChild(createJalaliDateFilterControl(field, currentFilter));
        
    } else {
        // Text search for text fields
        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'filter-input';
        input.placeholder = t('filterPlaceholder');
        input.value = currentFilter?.value ?? '';
        input.setAttribute('aria-label', t('filterColumnText', { column: displayFieldName(field) }));
        
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

function closeOpenRangePanel() {
    if (!state.openRangePanel) return;
    state.openRangePanel.classList.remove('open');
    state.openRangePanel = null;
    state.openRangeAnchor = null;
}

function positionFloatingRangePanel(anchor, panel) {
    if (!anchor || !panel || !anchor.isConnected || !panel.isConnected) {
        closeOpenRangePanel();
        return;
    }

    const viewportPadding = 8;
    const anchorRect = anchor.getBoundingClientRect();
    const panelRect = panel.getBoundingClientRect();
    const panelWidth = Math.max(panelRect.width || 230, 230);
    const panelHeight = Math.max(panelRect.height || 120, 120);
    const availableRight = window.innerWidth - viewportPadding - panelWidth;
    const left = Math.max(viewportPadding, Math.min(anchorRect.left, availableRight));
    const preferredTop = anchorRect.bottom + 6;
    const fallbackTop = anchorRect.top - panelHeight - 6;
    const top = preferredTop + panelHeight <= window.innerHeight - viewportPadding
        ? preferredTop
        : Math.max(viewportPadding, fallbackTop);

    panel.style.setProperty('--range-panel-left', `${Math.round(left)}px`);
    panel.style.setProperty('--range-panel-top', `${Math.round(top)}px`);
}

function openFloatingRangePanel(anchor, panel) {
    if (state.openRangePanel && state.openRangePanel !== panel) {
        closeOpenRangePanel();
    }

    panel.classList.toggle('open');
    if (panel.classList.contains('open')) {
        state.openRangePanel = panel;
        state.openRangeAnchor = anchor;
        positionFloatingRangePanel(anchor, panel);
    } else {
        state.openRangePanel = null;
        state.openRangeAnchor = null;
    }
}

function repositionOpenRangePanel() {
    if (state.openRangePanel) {
        positionFloatingRangePanel(state.openRangeAnchor, state.openRangePanel);
    }
}

function createRangePopover2(field, currentFilter, mode) {
    const wrapper = document.createElement('div');
    wrapper.className = 'range-popover range-combo';

    const stats = state.fieldStats[field] || {};
    const minLimit = Number.isFinite(stats.min) ? stats.min : 0;
    const maxLimit = Number.isFinite(stats.max) ? stats.max : Math.max(minLimit + 1, 1);
    const hasUsableRange = Number.isFinite(stats.min) && Number.isFinite(stats.max) && stats.min !== stats.max;
    const step = Number.isInteger(minLimit) && Number.isInteger(maxLimit) ? 1 : 0.01;

    const exactInput = document.createElement('input');
    exactInput.type = 'text';
    exactInput.inputMode = 'decimal';
    exactInput.className = 'filter-input range-direct-input';
    exactInput.placeholder = hasUsableRange ? `${formatRangeValue(minLimit)}-${formatRangeValue(maxLimit)}` : formatRangeValue(minLimit);
    exactInput.title = hasUsableRange
        ? t('exactRangeHelp', { minimum: formatRangeValue(minLimit), maximum: formatRangeValue(maxLimit) })
        : t('sampledValue', { value: formatRangeValue(minLimit) });
    exactInput.setAttribute('aria-label', t('exactOrRangeFilter', { column: displayFieldName(field) }));

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'range-trigger range-trigger-ellipsis';
    button.innerHTML = iconMarkup('more');
    button.setAttribute('aria-label', t('openRangeOptions', { column: displayFieldName(field) }));

    const panel = document.createElement('div');
    panel.className = 'range-panel';
    panel.addEventListener('click', event => event.stopPropagation());

    const meta = document.createElement('div');
    meta.className = 'range-meta';
    meta.innerHTML = hasUsableRange
        ? `<span>${escapeHtml(t('minimum'))} <strong>${escapeHtml(formatRangeValue(minLimit))}</strong></span><span>${escapeHtml(t('maximum'))} <strong>${escapeHtml(formatRangeValue(maxLimit))}</strong></span>`
        : `<span class="range-meta-note">${escapeHtml(t('sampledValue', { value: formatRangeValue(minLimit) }))}</span>`;

    const minInput = document.createElement('input');
    minInput.type = 'text';
    minInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    minInput.className = 'filter-input-small';
    minInput.placeholder = t('minimum');
    minInput.value = currentFilter?.min ?? '';
    minInput.setAttribute('aria-label', t('minimumColumn', { column: displayFieldName(field) }));

    const maxInput = document.createElement('input');
    maxInput.type = 'text';
    maxInput.inputMode = mode === 'numeric' ? 'decimal' : 'text';
    maxInput.className = 'filter-input-small';
    maxInput.placeholder = t('maximum');
    maxInput.value = currentFilter?.max ?? '';
    maxInput.setAttribute('aria-label', t('maximumColumn', { column: displayFieldName(field) }));

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
    minSlider.disabled = !hasUsableRange;
    maxSlider.disabled = !hasUsableRange;

    const clearBtn = document.createElement('button');
    clearBtn.type = 'button';
    clearBtn.className = 'range-clear';
    clearBtn.textContent = t('clearFilters');

    const updateDirectInput = () => {
        const latest = state.columnFilters[field];
        const active = latest?.min !== undefined && latest?.min !== null || latest?.max !== undefined && latest?.max !== null;
        if (!active) {
            exactInput.value = '';
        } else if (latest.min !== null && latest.min !== undefined && latest.max !== null && latest.max !== undefined) {
            exactInput.value = latest.min === latest.max ? formatRangeValue(latest.min) : `${formatRangeValue(latest.min)}-${formatRangeValue(latest.max)}`;
        } else if (latest.min !== null && latest.min !== undefined) {
            exactInput.value = `>=${formatRangeValue(latest.min)}`;
        } else {
            exactInput.value = `<=${formatRangeValue(latest.max)}`;
        }
        wrapper.classList.toggle('has-filter', !!active);
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
        updateDirectInput();
        applyFilters();
    }, 80);

    const commitDirect = debounce(() => {
        const parsed = parseNumericFilterText(exactInput.value);
        exactInput.setCustomValidity(parsed.error || '');
        if (parsed.error) return;
        if (parsed.empty) {
            minInput.value = '';
            maxInput.value = '';
            delete state.columnFilters[field];
        } else {
            minInput.value = parsed.min === null || parsed.min === undefined ? '' : String(parsed.min);
            maxInput.value = parsed.max === null || parsed.max === undefined ? '' : String(parsed.max);
            state.columnFilters[field] = { type: mode, min: parsed.min, max: parsed.max };
        }
        wrapper.classList.toggle('has-filter', !parsed.empty);
        syncSlidersFromInputs();
        saveColumnFilters();
        applyFilters();
    }, FILTER_INPUT_DEBOUNCE_MS);

    const syncSlidersFromInputs = () => {
        const min = parseFloat(minInput.value);
        const max = parseFloat(maxInput.value);
        if (Number.isFinite(min)) minSlider.value = String(Math.min(Math.max(min, minLimit), maxLimit));
        if (Number.isFinite(max)) maxSlider.value = String(Math.min(Math.max(max, minLimit), maxLimit));
    };

    const syncFromInput = () => {
        syncSlidersFromInputs();
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
        openFloatingRangePanel(wrapper, panel);
        if (panel.classList.contains('open')) {
            setTimeout(() => minInput.focus({ preventScroll: true }), 20);
        }
    });

    exactInput.addEventListener('input', commitDirect);
    exactInput.addEventListener('keydown', event => {
        if (event.key === 'Enter') {
            event.preventDefault();
            exactInput.blur();
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
        updateDirectInput();
        applyFilters();
    });

    panel.appendChild(meta);
    panel.appendChild(wrapRangeField('Min', minInput));
    panel.appendChild(wrapRangeField('Max', maxInput));
    panel.appendChild(minSlider);
    panel.appendChild(maxSlider);
    panel.appendChild(clearBtn);
    wrapper.appendChild(exactInput);
    wrapper.appendChild(button);
    wrapper.appendChild(panel);
    updateDirectInput();
    return wrapper;
}

function parseNumericFilterText(text) {
    const value = String(text || '').trim();
    if (!value) return { empty: true };
    const compact = value.replace(/\s+/g, '');
    let match = compact.match(/^>=(-?\d+(?:\.\d+)?)$/);
    if (match) return { min: parseFloat(match[1]), max: null };
    match = compact.match(/^<=(-?\d+(?:\.\d+)?)$/);
    if (match) return { min: null, max: parseFloat(match[1]) };
    match = compact.match(/^(-?\d+(?:\.\d+)?)(?:\.\.|-|:)(-?\d+(?:\.\d+)?)$/);
    if (match) {
        let min = parseFloat(match[1]);
        let max = parseFloat(match[2]);
        if (min > max) [min, max] = [max, min];
        return { min, max };
    }
    match = compact.match(/^-?\d+(?:\.\d+)?$/);
    if (match) {
        const exact = parseFloat(compact);
        return { min: exact, max: exact };
    }
    return { error: t('invalidNumberRange') };
}

function formatRangeValue(value) {
    if (!Number.isFinite(Number(value))) return '';
    const n = Number(value);
    return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)));
}

function createJalaliDateFilterControl(field, currentFilter) {
    const wrapper = document.createElement('div');
    wrapper.className = 'jalali-filter range-popover';
    const stats = state.fieldStats[field] || {};
    const minDate = stats.min || null;
    const maxDate = stats.max || null;

    const fromInput = document.createElement('input');
    fromInput.type = 'text';
    fromInput.inputMode = 'numeric';
    fromInput.className = 'filter-input jalali-date-input';
    fromInput.placeholder = minDate ? JalaliUtils.format(minDate) : t('from');
    fromInput.value = currentFilter?.min ?? '';
    fromInput.title = t('jalaliDateFormat');
    fromInput.setAttribute('aria-label', t('jalaliDateFrom', { column: displayFieldName(field) }));

    const toInput = document.createElement('input');
    toInput.type = 'text';
    toInput.inputMode = 'numeric';
    toInput.className = 'filter-input jalali-date-input';
    toInput.placeholder = maxDate ? JalaliUtils.format(maxDate) : t('to');
    toInput.value = currentFilter?.max ?? '';
    toInput.title = t('jalaliDateFormat');
    toInput.setAttribute('aria-label', t('jalaliDateTo', { column: displayFieldName(field) }));

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'range-trigger range-trigger-ellipsis date-picker-trigger';
    button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M7 3v3M17 3v3M4 9h16M6 5h12a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>';
    button.setAttribute('aria-label', t('jalaliDatePicker', { column: displayFieldName(field) }));

    const panel = document.createElement('div');
    panel.className = 'range-panel jalali-picker-panel';
    panel.addEventListener('click', event => event.stopPropagation());

    const meta = document.createElement('div');
    meta.className = 'range-meta';
    meta.innerHTML = minDate && maxDate
        ? `<span>${escapeHtml(t('minimum'))} <strong>${escapeHtml(JalaliUtils.format(minDate))}</strong></span><span>${escapeHtml(t('maximum'))} <strong>${escapeHtml(JalaliUtils.format(maxDate))}</strong></span>`
        : `<span class="range-meta-note">${escapeHtml(t('noJalaliDates'))}</span>`;

    const pickerState = {
        cursor: JalaliUtils.parse(fromInput.value) || minDate || maxDate || { year: 0, month: 1, day: 1 },
        target: 'min'
    };

    const commitDateFilter = debounce(() => {
        const minText = fromInput.value.trim();
        const maxText = toInput.value.trim();
        const parsedMin = minText ? JalaliUtils.parse(minText) : null;
        const parsedMax = maxText ? JalaliUtils.parse(maxText) : null;
        fromInput.setCustomValidity(minText && !parsedMin ? t('invalidJalaliDate') : '');
        toInput.setCustomValidity(maxText && !parsedMax ? t('invalidJalaliDate') : '');
        if ((minText && !parsedMin) || (maxText && !parsedMax)) return;

        let nextMin = minText;
        let nextMax = maxText;
        if (parsedMin && parsedMax && JalaliUtils.compare(parsedMin, parsedMax) > 0) {
            nextMin = maxText;
            nextMax = minText;
            fromInput.value = nextMin;
            toInput.value = nextMax;
        }

        if (nextMin || nextMax) {
            state.columnFilters[field] = { type: 'jalali-date', min: nextMin, max: nextMax };
            wrapper.classList.add('has-filter');
        } else {
            delete state.columnFilters[field];
            wrapper.classList.remove('has-filter');
        }
        saveColumnFilters();
        applyFilters();
    }, FILTER_INPUT_DEBOUNCE_MS);

    const renderPicker = () => {
        const title = document.createElement('div');
        title.className = 'jalali-picker-title';
        const prev = document.createElement('button');
        prev.type = 'button';
        prev.className = 'picker-nav';
        prev.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 6l-6 6 6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
        const next = document.createElement('button');
        next.type = 'button';
        next.className = 'picker-nav';
        next.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
        const label = document.createElement('strong');
        label.textContent = `${String(pickerState.cursor.year).padStart(2, '0')}.${String(pickerState.cursor.month).padStart(2, '0')}`;
        prev.addEventListener('click', () => {
            pickerState.cursor = JalaliUtils.addMonths(pickerState.cursor, -1);
            renderPicker();
        });
        next.addEventListener('click', () => {
            pickerState.cursor = JalaliUtils.addMonths(pickerState.cursor, 1);
            renderPicker();
        });
        title.append(prev, label, next);

        const targetSwitch = document.createElement('div');
        targetSwitch.className = 'jalali-target-switch';
        ['min', 'max'].forEach(target => {
            const targetButton = document.createElement('button');
            targetButton.type = 'button';
            targetButton.className = pickerState.target === target ? 'active' : '';
            targetButton.textContent = target === 'min' ? t('from') : t('to');
            targetButton.addEventListener('click', () => {
                pickerState.target = target;
                renderPicker();
            });
            targetSwitch.appendChild(targetButton);
        });

        const grid = document.createElement('div');
        grid.className = 'jalali-day-grid';
        const days = JalaliUtils.daysInMonth(pickerState.cursor.year, pickerState.cursor.month);
        for (let day = 1; day <= days; day += 1) {
            const date = { year: pickerState.cursor.year, month: pickerState.cursor.month, day };
            const dayButton = document.createElement('button');
            dayButton.type = 'button';
            dayButton.textContent = String(day);
            dayButton.disabled = (minDate && JalaliUtils.compare(date, minDate) < 0) || (maxDate && JalaliUtils.compare(date, maxDate) > 0);
            dayButton.addEventListener('click', () => {
                const formatted = JalaliUtils.format(date);
                if (pickerState.target === 'min') {
                    fromInput.value = formatted;
                    pickerState.target = 'max';
                } else {
                    toInput.value = formatted;
                }
                commitDateFilter();
                renderPicker();
            });
            grid.appendChild(dayButton);
        }

        const clearBtn = document.createElement('button');
        clearBtn.type = 'button';
        clearBtn.className = 'range-clear';
        clearBtn.textContent = t('clearFilters');
        clearBtn.addEventListener('click', () => {
            fromInput.value = '';
            toInput.value = '';
            delete state.columnFilters[field];
            wrapper.classList.remove('has-filter');
            saveColumnFilters();
            applyFilters();
        });

        panel.replaceChildren(meta, targetSwitch, title, grid, clearBtn);
    };

    fromInput.addEventListener('input', commitDateFilter);
    toInput.addEventListener('input', commitDateFilter);
    button.addEventListener('click', event => {
        event.stopPropagation();
        pickerState.cursor = JalaliUtils.parse(fromInput.value) || JalaliUtils.parse(toInput.value) || minDate || maxDate || pickerState.cursor;
        renderPicker();
        openFloatingRangePanel(wrapper, panel);
    });

    wrapper.classList.toggle('has-filter', !!(currentFilter?.min || currentFilter?.max));
    wrapper.append(fromInput, toInput, button, panel);
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

function createCodeFilterControl(currentFilter) {
    const wrapper = document.createElement('div');
    wrapper.className = 'code-filter';

    const typeSelect = document.createElement('select');
    typeSelect.className = 'filter-select code-filter-type';
    typeSelect.setAttribute('aria-label', t('filterCodeType'));
    [
        ['', t('all')],
        ['group', t('group')],
        ['subgroup', t('subgroup')],
        ['item', t('item')]
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
    segmentInput.setAttribute('aria-label', t('filterCodeSegments'));

    const badge = document.createElement('span');
    badge.className = 'filter-badge';
    badge.textContent = currentFilter ? t('code') : t('any');

    const update = () => {
        const codeType = typeSelect.value;
        const segment = segmentInput.value.trim();
        if (codeType || segment) {
            state.columnFilters.Code = { type: 'code', codeType, segment };
        } else {
            delete state.columnFilters.Code;
        }
        badge.textContent = codeType || segment ? t('code') : t('any');
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

function captureTableBodyFocus(tbody) {
    const row = document.activeElement?.closest?.('#tableBody tr[data-row-key]');
    return row && tbody.contains(row) ? row.dataset.rowKey : '';
}

function restoreTableBodyFocus(tbody, key) {
    if (!key) return;
    const rows = [...tbody.querySelectorAll('tr[data-row-key]')];
    const row = rows.find(candidate => candidate.dataset.rowKey === key)
        || rows.find(candidate => candidate.dataset.rowKey === state.rovingRowKey);
    row?.focus({ preventScroll: true });
}

function updateRovingRow(key, { focus = false } = {}) {
    const rows = [...document.querySelectorAll('#tableBody tr[data-row-key]')];
    const visibleKeys = rows.map(row => row.dataset.rowKey);
    state.rovingRowKey = resolvedRovingKey(key, visibleKeys);
    rows.forEach(row => {
        row.tabIndex = row.dataset.rowKey === state.rovingRowKey ? 0 : -1;
    });
    if (focus) {
        rows.find(row => row.dataset.rowKey === state.rovingRowKey)?.focus({ preventScroll: true });
    }
}

function rowAccessibleLabel(record, selectionKey) {
    const label = stableRecordKey(record) || String(record?.name ?? record?.Name ?? '').trim() || t('inspect');
    const duplicate = String(selectionKey || '').match(/#(\d+)$/)?.[1];
    return duplicate ? `${label} (${duplicate})` : label;
}

function renderTableCellValue(cell, field, value) {
    if (field !== 'Code' && field !== 'Serial' && typeof value !== 'object'
        && value !== null && value !== undefined && value !== '' && !isNaN(value)) {
        cell.textContent = formatNumberWithSeparator(value);
        cell.style.textAlign = 'right';
        return;
    }
    cell.textContent = structuredValueText(value);
}

// Render table body
function renderTable(changedIndices = new Set()) {
    const tbody = document.getElementById('tableBody');
    const loading = document.getElementById('loading');
    const emptyState = document.getElementById('emptyState');
    const focusedRowKey = captureTableBodyFocus(tbody);

    refreshRecordSelectionKeys();
    pruneSelectedKeys(state.selectedKeys, state.records, recordSelectionKey);
    updateSelectionHeaderState();
    updateSelectionCount();
    loading.style.display = 'none';

    if (state.filteredRecords.length === 0) {
        tbody.innerHTML = '';
        state.rovingRowKey = '';
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

    const visibleSelectionKeys = recordsToShow.map(recordSelectionKey);
    const visibleFields = state.fields.filter(field => !state.hiddenColumns.has(field));
    const frozenField = state.settings.freezeFirstColumn ? visibleFields[0] : '';
    state.rovingRowKey = resolvedRovingKey(focusedRowKey || state.rovingRowKey, visibleSelectionKeys);

    tbody.innerHTML = '';

    recordsToShow.forEach(record => {
        const row = document.createElement('tr');
        const codeInfo = parsePatrisCode(record.Code);
        const recordCode = stableRecordKey(record);
        const selectionKey = recordSelectionKey(record);
        if (recordCode) {
            row.dataset.code = recordCode;
        }
        row.dataset.rowKey = selectionKey;
        row.tabIndex = selectionKey === state.rovingRowKey ? 0 : -1;
        row.setAttribute('aria-selected', state.selectedKeys.has(selectionKey) ? 'true' : 'false');
        row.setAttribute('aria-label', rowAccessibleLabel(record, selectionKey));
        row.classList.toggle('selected', state.selectedKeys.has(selectionKey));
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

        row.appendChild(createRowSelectionCell(record, selectionKey, row));

        // Add data cells
        state.fields.forEach(field => {
            // Skip hidden columns
            if (state.hiddenColumns.has(field)) {
                return;
            }

            const td = document.createElement('td');

            const value = getFieldValue(record, field);
            renderTableCellValue(td, field, value);
            if (isWarehouseColumnField(field)) td.classList.add('warehouse-column');
            if (String(field).toLowerCase() === 'warnings') {
                td.classList.add('warning-column');
                td.title = structuredValueText(value);
            }

            if (field === frozenField) {
                td.classList.add('sticky-column');
            }
            row.appendChild(td);
        });

        // Add actions cell
        const actionsCell = document.createElement('td');
        actionsCell.className = 'action-cell actions-column';
        const actionsWrap = document.createElement('div');
        actionsWrap.className = 'action-cell-content';

        const menuButton = document.createElement('button');
        menuButton.type = 'button';
        menuButton.className = 'action-btn row-action-button';
        menuButton.innerHTML = iconMarkup('more');
        menuButton.title = t('rowActions', { code: recordCode || '—' });
        menuButton.setAttribute('aria-label', menuButton.title);
        menuButton.setAttribute('aria-haspopup', 'menu');
        menuButton.setAttribute('aria-expanded', 'false');
        menuButton.tabIndex = -1;
        menuButton.addEventListener('click', event => {
            event.stopPropagation();
            openRowCommandMenu(record, { trigger: menuButton, focusMenu: true });
        });

        actionsWrap.appendChild(menuButton);
        actionsCell.appendChild(actionsWrap);
        row.appendChild(actionsCell);

        // Make row clickable to inspect
        row.addEventListener('click', () => {
            updateRovingRow(selectionKey);
            persistScrollAnchorForCode(recordCode);
            inspectRecord(record);
        });
        row.addEventListener('focus', event => {
            if (event.target === row) updateRovingRow(selectionKey);
        });
        row.addEventListener('contextmenu', event => {
            event.preventDefault();
            openRowCommandMenu(record, { point: { x: event.clientX, y: event.clientY }, trigger: row, focusMenu: true });
        });
        row.addEventListener('keydown', event => {
            if (event.target !== row) return;
            if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
                event.preventDefault();
                const rect = row.getBoundingClientRect();
                openRowCommandMenu(record, {
                    point: { x: isTableRTL() ? rect.right - 8 : rect.left + 8, y: rect.top + Math.min(rect.height, 32) },
                    trigger: row,
                    focusMenu: true
                });
            } else if (['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) {
                event.preventDefault();
                updateRovingRow(nextRovingKey(visibleSelectionKeys, selectionKey, event.key), { focus: true });
            } else if (event.key === ' ') {
                event.preventDefault();
                executeRowCommand('toggle_selection', record);
            } else if (event.key === 'Enter') {
                event.preventDefault();
                persistScrollAnchorForCode(recordCode);
                inspectRecord(record);
            }
        });

        tbody.appendChild(row);
    });
    if (!(state.settings.autoScrollToChanged && changedIndices.size > 0)) {
        restoreScrollAnchorAfterRender();
    }
    restoreTableBodyFocus(tbody, focusedRowKey);
}

function createRowSelectionCell(record, selectionKey, row) {
    const cell = document.createElement('td');
    cell.className = 'selection-column selection-cell';
    const content = document.createElement('div');
    content.className = 'selection-cell-content';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'table-selection-checkbox';
    checkbox.checked = state.selectedKeys.has(selectionKey);
    checkbox.tabIndex = -1;
    checkbox.setAttribute('aria-label', t('selectRowLabel', { code: rowAccessibleLabel(record, selectionKey) }));
    checkbox.addEventListener('click', event => event.stopPropagation());
    checkbox.addEventListener('change', () => {
        if (checkbox.checked) state.selectedKeys.add(selectionKey);
        else state.selectedKeys.delete(selectionKey);
        updateRovingRow(selectionKey);
        row.classList.toggle('selected', checkbox.checked);
        row.setAttribute('aria-selected', checkbox.checked ? 'true' : 'false');
        updateSelectionHeaderState();
        updateSelectionCount();
    });
    content.appendChild(checkbox);

    if (state.settings.enableRowIcons) {
        const appearance = resolveRowIcon(record, state.settings.rowIconRules, state.settings.rowIconFallback);
        const icon = document.createElement('span');
        icon.className = 'conditional-row-icon';
        icon.style.color = appearance.color;
        icon.innerHTML = iconMarkup(appearance.icon);
        const appearanceLabel = localizedAppearanceLabel(appearance);
        icon.title = appearanceLabel;
        icon.setAttribute('role', 'img');
        icon.setAttribute('aria-label', appearanceLabel);
        if (appearance.ruleId) icon.dataset.ruleId = appearance.ruleId;
        content.appendChild(icon);
    }
    cell.appendChild(content);
    return cell;
}

function updateSelectionHeaderState() {
    const checkbox = document.querySelector('.selection-header-cell .table-selection-checkbox');
    if (!checkbox) return;
    const summary = selectionSummary(state.selectedKeys, state.filteredRecords, recordSelectionKey);
    checkbox.checked = summary.checked;
    checkbox.indeterminate = summary.indeterminate;
    checkbox.disabled = summary.selectable === 0;
}

function commandsForRow(record) {
    const recordCode = stableRecordKey(record);
    const selectionKey = recordSelectionKey(record);
    return rowCommandDefinitions({
        selected: state.selectedKeys.has(selectionKey),
        hasCode: !!recordCode
    }).map(definition => ({
        ...definition,
        label: t(definition.labelKey),
        execute: () => executeRowCommand(definition.id, record)
    }));
}

function openRowCommandMenu(record, { point = null, trigger = null, focusMenu = false } = {}) {
    state.rowMenu.record = record;
    openGridCommandMenu(commandsForRow(record), {
        point,
        trigger,
        focusMenu,
        kind: 'row',
        ariaLabel: t('rowActions', { code: stableRecordKey(record) || '—' })
    });
}

function headerCommands(field) {
    const identity = canonicalColumnKey(field) === 'product_code';
    return [
        { id: 'sort_ascending', icon: 'arrowUp', label: t('sortAscending'), execute: () => applySort(field, 'asc') },
        { id: 'sort_descending', icon: 'arrowDown', label: t('sortDescending'), execute: () => applySort(field, 'desc') },
        { id: 'reset_width', icon: 'refresh', label: t('resetColumnWidth'), execute: () => resetColumnWidth(field) },
        ...(!identity ? [{ id: 'hide_column', icon: 'close', label: t('hideColumn'), execute: () => hideColumn(field) }] : []),
        { id: 'manage_columns', icon: 'columns', label: t('manageColumns'), execute: () => openModalRoute('columns') }
    ];
}

function openHeaderCommandMenu(field, options = {}) {
    openGridCommandMenu(headerCommands(field), {
        ...options,
        kind: 'header',
        ariaLabel: t('headerActions', { column: displayFieldName(field) })
    });
}

function openWarehouseGroupContextMenu(options = {}) {
    openGridCommandMenu([
        { id: 'show_warehouses', icon: 'check-square', label: t('showAll'), execute: () => setWarehouseVisibility(true) },
        { id: 'hide_warehouses', icon: 'close', label: t('hideAll'), execute: () => setWarehouseVisibility(false) },
        { id: 'manage_columns', icon: 'columns', label: t('manageColumns'), execute: () => openModalRoute('columns') }
    ], { ...options, kind: 'header', ariaLabel: t('warehouseStock') });
}

function dialogCommands(panelID) {
    const definitions = {
        settingsPanel: [
            { id: 'open_columns', icon: 'columns', label: t('openColumns'), execute: () => openModalRoute('columns') },
            { id: 'open_connection', icon: 'info', label: t('openConnection'), execute: () => openModalRoute('connection') },
            { id: 'open_logs', icon: 'list', label: t('openEventLog'), execute: () => openModalRoute('logs') }
        ],
        columnsPanel: [
            { id: 'show_all', icon: 'check-square', label: t('showAll'), execute: showAllColumns },
            { id: 'hide_all', icon: 'close', label: t('hideAll'), execute: hideAllOptionalColumns },
            { id: 'reset_widths', icon: 'refresh', label: t('resetColumnWidths'), execute: resetAllColumnWidths }
        ],
        connectionPanel: [
            { id: 'refresh_source', icon: 'refresh', label: t('refreshSource'), execute: requestSourceRefresh },
            { id: 'copy_status', icon: 'copy', label: t('copyStatus'), execute: copyConnectionStatus }
        ],
        eventLogPanel: [
            { id: 'copy_event_log', icon: 'copy', label: t('copyEventLog'), execute: copyEventLog },
            { id: 'clear_event_log', icon: 'trash', label: t('eventLogClear'), execute: () => clearEventLog() }
        ],
        inspectorPanel: [
            { id: 'copy_inspected_json', icon: 'braces', label: t('copyJSON'), execute: () => copyTextToClipboard(JSON.stringify(state.inspectedRecord || {}, null, 2)) },
            { id: 'open_columns', icon: 'columns', label: t('openColumns'), execute: () => openModalRoute('columns') }
        ]
    };
    return definitions[panelID] || [];
}

function openDialogCommandMenu(panelID, trigger) {
    openGridCommandMenu(dialogCommands(panelID), {
        trigger,
        focusMenu: true,
        kind: 'dialog',
        ariaLabel: t('moreActions')
    });
}

function openGridCommandMenu(commands, { point = null, trigger = null, focusMenu = false, kind = '', ariaLabel = '' } = {}) {
    const menu = document.getElementById('rowContextMenu');
    if (!menu) return;
    closeRowCommandMenu({ restoreFocus: false });
    state.rowMenu.trigger = trigger;
    state.rowMenu.focusTarget = trigger?.closest?.('tr[data-row-key]') || trigger;
    state.rowMenu.kind = kind;
    trigger?.setAttribute?.('aria-expanded', 'true');
    menu.setAttribute('aria-label', ariaLabel || t('actions'));

    menu.innerHTML = '';
    commands.forEach(command => {
        const item = document.createElement('button');
        item.type = 'button';
        item.className = 'row-context-menu-item';
        item.setAttribute('role', 'menuitem');
        item.tabIndex = -1;
        item.dataset.command = command.id;
        item.innerHTML = `${iconMarkup(command.icon)}<span></span>`;
        item.querySelector('span').textContent = command.label;
        item.addEventListener('click', event => {
            event.stopPropagation();
            closeRowCommandMenu({ restoreFocus: true });
            Promise.resolve(command.execute()).catch(error => {
                showInAppToast(t('copyFailed'), error.message, { titleKey: 'copyFailed', error: true, source: `${kind || 'grid'}_action`, eventType: `${kind || 'grid'}_action` });
            });
        });
        menu.appendChild(item);
    });

    menu.hidden = false;
    menu.classList.add('open');
    menu.dir = isTableRTL() ? 'rtl' : 'ltr';
    menu.style.visibility = 'hidden';
    menu.style.left = '0px';
    menu.style.top = '0px';

    const menuWidth = menu.offsetWidth || 220;
    const menuHeight = menu.offsetHeight || 180;
    let x = point?.x;
    let y = point?.y;
    if (!point && trigger) {
        const rect = trigger.getBoundingClientRect();
        x = isTableRTL() ? rect.left : rect.right - menuWidth;
        y = rect.bottom + 4;
    }
    const position = fitMenuPosition({
        x,
        y,
        width: menuWidth,
        height: menuHeight,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight
    });
    menu.style.left = `${position.left}px`;
    menu.style.top = `${position.top}px`;
    menu.style.visibility = 'visible';
    if (focusMenu) menu.querySelector('[role="menuitem"]')?.focus({ preventScroll: true });
}

function closeRowCommandMenu({ restoreFocus = false } = {}) {
    const menu = document.getElementById('rowContextMenu');
    if (!menu || menu.hidden) return;
    const trigger = state.rowMenu.trigger;
    const focusTarget = state.rowMenu.focusTarget;
    trigger?.setAttribute?.('aria-expanded', 'false');
    menu.hidden = true;
    menu.classList.remove('open');
    menu.innerHTML = '';
    state.rowMenu.record = null;
    state.rowMenu.trigger = null;
    state.rowMenu.focusTarget = null;
    state.rowMenu.kind = '';
    if (restoreFocus && focusTarget?.isConnected) focusTarget.focus({ preventScroll: true });
}

function initRowCommandMenu() {
    document.addEventListener('pointerdown', event => {
        const menu = document.getElementById('rowContextMenu');
        if (!menu?.hidden && !menu.contains(event.target) && !event.target.closest('.row-action-button')) {
            closeRowCommandMenu({ restoreFocus: false });
        }
    }, true);
    document.addEventListener('keydown', event => {
        const menu = document.getElementById('rowContextMenu');
        if (!menu || menu.hidden) return;
        const items = [...menu.querySelectorAll('[role="menuitem"]')];
        const current = items.indexOf(document.activeElement);
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopImmediatePropagation();
            closeRowCommandMenu({ restoreFocus: true });
        } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            const delta = event.key === 'ArrowDown' ? 1 : -1;
            items[(current + delta + items.length) % items.length]?.focus();
        } else if (event.key === 'Home' || event.key === 'End') {
            event.preventDefault();
            items[event.key === 'Home' ? 0 : items.length - 1]?.focus();
        } else if (event.key === 'Tab') {
            event.preventDefault();
            closeRowCommandMenu({ restoreFocus: true });
        }
    });
    window.addEventListener('resize', () => closeRowCommandMenu({ restoreFocus: false }));
    document.addEventListener('scroll', () => closeRowCommandMenu({ restoreFocus: false }), true);
}

async function executeRowCommand(commandID, record) {
    const recordCode = stableRecordKey(record);
    const selectionKey = recordSelectionKey(record);
    switch (commandID) {
        case 'inspect':
            persistScrollAnchorForCode(recordCode);
            inspectRecord(record);
            return;
        case 'copy_code':
            await copyTextToClipboard(recordCode);
            showInAppToast(t('copied'), t('codeCopied'), { titleKey: 'copied', messageKey: 'codeCopied', source: 'row_action', eventType: 'row_action' });
            return;
        case 'copy_json':
            await copyTextToClipboard(JSON.stringify(record, null, 2));
            showInAppToast(t('copied'), t('jsonCopied'), { titleKey: 'copied', messageKey: 'jsonCopied', source: 'row_action', eventType: 'row_action' });
            return;
        case 'toggle_selection':
            if (state.selectedKeys.has(selectionKey)) state.selectedKeys.delete(selectionKey);
            else state.selectedKeys.add(selectionKey);
            state.rovingRowKey = selectionKey;
            renderTableHeader();
            renderTable();
            return;
        default:
            return;
    }
}

async function copyTextToClipboard(text) {
    if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        return;
    }
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const copied = document.execCommand('copy');
    textarea.remove();
    if (!copied) throw new Error(t('copyFailed'));
}

async function copyConnectionStatus() {
    await copyTextToClipboard(JSON.stringify({
        connection: state.connectionStatus,
        websocket: state.ws?.readyState ?? null,
        source: state.fileName || '',
        process: state.processStatus || null
    }, null, 2));
    showInAppToast(t('copied'), t('statusCopied'), { titleKey: 'copied', messageKey: 'statusCopied', source: 'connection', eventType: 'copy_status' });
}

async function copyEventLog() {
    await copyTextToClipboard(JSON.stringify(state.eventLog, null, 2));
    showInAppToast(t('copied'), t('eventLogCopied'), { titleKey: 'copied', messageKey: 'eventLogCopied', source: 'event_log', eventType: 'copy_event_log' });
}

// Sort by field
function sortByField(field) {
    const direction = state.sortField === field && state.sortDirection === 'asc' ? 'desc' : 'asc';
    applySort(field, direction);
}

function applySort(field, direction) {
    state.sortField = field;
    state.sortDirection = direction === 'desc' ? 'desc' : 'asc';
    saveSortPreferences();
    sortRecords();
    renderTableHeader();
    renderTable();
}

// Sort records based on current sort field and direction
function sortRecords() {
    state.filteredRecords.sort((a, b) => {
        let aVal = getFieldValue(a, state.sortField);
        let bVal = getFieldValue(b, state.sortField);
        
        // Special handling for Code field - right-pad to 9 characters for sorting
        if (canonicalColumnKey(state.sortField) === 'product_code') {
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

function downloadCanonicalWorkbook() {
	const exportConfig = state.config?.export || {};
	const workbookPath = canonicalWorkbookPath({
		language: state.settings.language,
		rtl: state.settings.rtlTextDirection,
		mode: exportConfig.xlsx_mode,
		zebra: exportConfig.xlsx_zebra_rows !== false
	});
	const link = document.createElement('a');
	link.href = workbookPath;
    link.download = 'patris-export.xlsx';
    link.hidden = true;
    document.body.appendChild(link);
    link.click();
    link.remove();
    showInAppToast(t('excelExportStarted'), t('excelExportStartedMessage'), {
        titleKey: 'excelExportStarted',
        messageKey: 'excelExportStartedMessage',
        source: 'xlsx_export',
        eventType: 'xlsx_export'
    });
}

// Export data. XLSX intentionally uses the Go endpoint so it shares the
// canonical projection and workbook writer with CLI/configured exports.
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
    } else if (format === 'xlsx') {
        downloadCanonicalWorkbook();
    }
}

// Render column manager with checkboxes for each column
function renderColumnManager() {
    const container = document.getElementById('columnCheckboxes');
    container.innerHTML = '';

    getColumnManagerEntries().forEach(entry => {
        container.appendChild(createColumnManagerRow(entry));
    });
}

function refreshColumnVisibilityUI() {
    removeHiddenColumnFilters();
    saveHiddenColumns();
    saveColumnFilters();
    renderColumnManager();
    renderTableHeader();
    applyFilters();
}

function showAllColumns() {
    state.hiddenColumns.clear();
    refreshColumnVisibilityUI();
}

function hideAllOptionalColumns() {
    state.fields.forEach(field => {
        if (canonicalColumnKey(field) !== 'product_code') state.hiddenColumns.add(field);
    });
    refreshColumnVisibilityUI();
}

function hideColumn(field) {
    if (canonicalColumnKey(field) === 'product_code') return;
    state.hiddenColumns.add(field);
    refreshColumnVisibilityUI();
}

function setWarehouseVisibility(visible) {
    state.fields.filter(isWarehouseColumnField).forEach(field => {
        if (visible) state.hiddenColumns.delete(field);
        else state.hiddenColumns.add(field);
    });
    refreshColumnVisibilityUI();
}

function getColumnManagerEntries() {
    const entries = [];
    const warehouseFields = state.fields.filter(isWarehouseColumnField);
    let warehousesAdded = false;
    state.fields.forEach(field => {
        if (isWarehouseColumnField(field)) {
            if (!warehousesAdded) {
                entries.push({ key: 'WAREHOUSE_GROUP', fields: warehouseFields, type: 'stock', draggable: true });
                warehousesAdded = true;
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
    const visibleFields = entry.fields.filter(field => !state.hiddenColumns.has(field));
    checkbox.checked = visibleFields.length === entry.fields.length;
    checkbox.indeterminate = visibleFields.length > 0 && visibleFields.length < entry.fields.length;
    checkbox.disabled = canonicalColumnKey(entry.key) === 'product_code';
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
    if (entry.key === 'WAREHOUSE_GROUP') {
        sourceCell.innerHTML = `<strong>warehouse_stock</strong><small>${escapeHtml(displayStockGroupName())}</small>`;
    } else {
        sourceCell.innerHTML = `<strong>${escapeHtml(entry.key)}</strong>${canonicalColumnKey(entry.key) === 'product_code' ? `<small>${escapeHtml(t('alwaysVisible'))}</small>` : ''}`;
    }

    const labelCell = document.createElement('div');
    labelCell.className = 'column-label-cell';
    const labelInput = document.createElement('input');
    labelInput.className = 'text-input';
    labelInput.type = 'text';
    labelInput.value = entry.key === 'WAREHOUSE_GROUP' ? displayStockGroupName() : displayFieldName(entry.key);
    labelInput.placeholder = entry.key === 'WAREHOUSE_GROUP' ? t('warehouseStock') : entry.key;
    labelInput.addEventListener('input', debounce(() => {
        state.config = state.config || {};
        state.config.column_labels = state.config.column_labels || {};
        if (entry.key === 'WAREHOUSE_GROUP') {
            state.config.column_labels.warehouse_stock = labelInput.value.trim() || t('warehouseStock');
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
    if (entry.key === 'WAREHOUSE_GROUP') {
        row.classList.add('warehouse-manager-row');
        row.appendChild(createWarehouseVisibilityGrid(entry));
    }
    attachColumnDragHandlers(row);
    return row;
}

function createWarehouseVisibilityGrid(entry) {
    const fieldset = document.createElement('fieldset');
    fieldset.className = 'warehouse-visibility-grid';
    const legend = document.createElement('legend');
    legend.textContent = t('warehouseVisibility');
    fieldset.appendChild(legend);
    entry.fields.forEach(field => {
        const label = document.createElement('label');
        label.className = 'warehouse-visibility-toggle';
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.checked = !state.hiddenColumns.has(field);
        checkbox.addEventListener('change', () => {
            if (checkbox.checked) state.hiddenColumns.delete(field);
            else {
                state.hiddenColumns.add(field);
                delete state.columnFilters[field];
            }
            saveHiddenColumns();
            saveColumnFilters();
            renderColumnManager();
            renderTableHeader();
            applyFilters();
        });
        const text = document.createElement('span');
        text.textContent = displayFieldName(field);
        label.append(checkbox, text);
        fieldset.appendChild(label);
    });
    return fieldset;
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
    const headers = state.fields.map(field => csvCell(displayFieldName(field))).join(',');
    
    // Create data rows
    const rows = data.map(record => {
        return state.fields.map(field => {
            return csvCell(structuredValueText(getFieldValue(record, field)));
        }).join(',');
    });
    
    return [headers, ...rows].join('\n');
}

function csvCell(value) {
    const text = value === null || value === undefined ? '' : String(value);
    return /[,\n"]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
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
                return structuredValueText(value).toLowerCase().includes(searchLower);
            });
        });
    }
    
    state.filteredRecords = filtered;
}

// Get field values through the shared virtual warehouse-column schema.
function getFieldValue(record, field) {
    const warehouseName = warehouseColumnName(field);
    if (isWarehouseColumnField(field) && !/^ANBAR\d+$/i.test(String(field))) {
        return record?.warehouse_stock && typeof record.warehouse_stock === 'object'
            ? record.warehouse_stock[warehouseName] ?? null
            : null;
    }
    if (/^ANBAR\d+$/i.test(String(field))) {
        const anbarIndex = parseInt(warehouseName, 10) - 1;
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
            return structuredValueText(value) === filter.value;
            
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
            return structuredValueText(value).toLowerCase().includes(filter.value.toLowerCase());

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
    if (!select) return;
    select.innerHTML = '';
    const allFields = document.createElement('option');
    allFields.value = '';
    allFields.textContent = t('allFields');
    allFields.selected = !state.selectedField;
    select.appendChild(allFields);
    
    state.fields.forEach(field => {
        const option = document.createElement('option');
        option.value = field;
        option.textContent = displayFieldName(field);
        option.selected = state.selectedField === field;
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

function activeCatalogRows() {
    return state.catalogView === 'categories' ? state.catalogCategories : state.catalogProducts;
}

function updateCatalogSwitch() {
    const container = document.getElementById('catalogSwitch');
    const productsButton = document.getElementById('productsViewBtn');
    const categoriesButton = document.getElementById('categoriesViewBtn');
    const productCount = document.getElementById('productDatasetCount');
    const categoryCount = document.getElementById('categoryDatasetCount');
    if (!container || !productsButton || !categoriesButton) return;

    container.hidden = !state.catalogCategoriesAvailable;
    productsButton.classList.toggle('active', state.catalogView === 'products');
    categoriesButton.classList.toggle('active', state.catalogView === 'categories');
    productsButton.setAttribute('aria-selected', String(state.catalogView === 'products'));
    categoriesButton.setAttribute('aria-selected', String(state.catalogView === 'categories'));
    if (productCount) productCount.textContent = state.catalogProducts.length.toLocaleString();
    if (categoryCount) categoryCount.textContent = state.catalogCategories.length.toLocaleString();
}

function selectActiveCatalogRows() {
    state.records = activeCatalogRows();
    updateCatalogSwitch();
}

function switchCatalogView(view) {
    if (view !== 'products' && view !== 'categories') return;
    if (view === 'categories' && !state.catalogCategoriesAvailable) return;
    if (view === state.catalogView && state.records === activeCatalogRows()) return;

    state.catalogView = view;
    selectActiveCatalogRows();
    state.filteredRecords = [];
    state.fields = [];
    state.fieldTypes = {};
    state.fieldStats = {};
    state.selectedField = '';
    state.columnFilters = {};
    refreshRecordSelectionKeys({ reset: true });

    if (state.records.length > 0) {
        extractFields();
        analyzeFields();
    }
    renderTableHeader();
    updateFieldFilter();
    filterRecords();
    sortRecords();
    renderTable();
    updateCounts();
    publishViewState();
}

// Inspect record
function inspectRecord(record) {
    const panel = document.getElementById('inspectorPanel');
    const body = document.getElementById('inspectorBody');
    
    body.innerHTML = '';
    state.inspectedRecord = record;
    
    state.fields.forEach(field => {
        const fieldDiv = document.createElement('div');
        fieldDiv.className = 'inspector-field';
        
        const nameDiv = document.createElement('div');
        nameDiv.className = 'inspector-field-name';
        nameDiv.textContent = displayFieldName(field);
        
        const valueDiv = document.createElement('div');
        valueDiv.className = 'inspector-field-value';
        
        const value = getFieldValue(record, field);
        
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
    if (!btn) return;
    btn.innerHTML = iconMarkup(isDark ? 'sun' : 'moon');
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
    initRowCommandMenu();
    initRouter();
    setInterval(updateLastUpdateDisplay, 30000);
    
    // Set up event listeners
    document.addEventListener('keydown', handleGlobalKeydown);
    document.querySelectorAll('[data-catalog-view]').forEach(button => {
        button.addEventListener('click', () => switchCatalogView(button.dataset.catalogView));
    });
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
    document.getElementById('footerLastUpdate')?.addEventListener('click', cycleLastUpdateMode);
    document.getElementById('headerConnectionButton')?.addEventListener('click', () => openModalRoute('connection'));
    document.getElementById('footerConnectionButton')?.addEventListener('click', () => openModalRoute('connection'));
    document.getElementById('eventLogBtn')?.addEventListener('click', () => openModalRoute('logs'));
    document.getElementById('closeEventLog')?.addEventListener('click', closeRouteDialog);
    document.getElementById('clearEventLog')?.addEventListener('click', () => clearEventLog());
    document.getElementById('headerFileChip')?.addEventListener('click', () => {
        if (state.fileName) showInAppToast(t('currentSourceFile'), state.fileName, { titleKey: 'currentSourceFile', broadcastToTabs: true, source: 'file_info', eventType: 'source_info' });
    });
    
    // Export button and dropdown
    const exportMenuController = createExportMenuController({
        button: document.getElementById('exportBtn'),
        menu: document.getElementById('exportDropdown'),
        nextFocusTarget: document.getElementById('columnsBtn'),
        onActivate: exportData
    });
    
    // Close export dropdown when clicking outside
    document.addEventListener('click', (e) => {
        const exportBtn = document.getElementById('exportBtn');
        const exportDropdown = document.getElementById('exportDropdown');
        if (!exportBtn.contains(e.target) && !exportDropdown.contains(e.target)) {
            exportMenuController.setOpen(false);
        }
        if (state.openRangePanel && !state.openRangePanel.contains(e.target) && !e.target.closest('.range-popover')) {
            closeOpenRangePanel();
        }
    });
    window.addEventListener('resize', repositionOpenRangePanel);
    document.addEventListener('scroll', repositionOpenRangePanel, true);
    
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

    document.getElementById('resetColumnWidths')?.addEventListener('click', resetAllColumnWidths);

    document.getElementById('refreshNowBtn').addEventListener('click', requestSourceRefresh);
    
    document.getElementById('showAllColumns').addEventListener('click', () => {
        showAllColumns();
    });
    
    document.getElementById('hideAllColumns').addEventListener('click', () => {
        hideAllOptionalColumns();
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
        renderTableHeader();
        updateFieldFilter();
        renderTable();
    });

    document.getElementById('freezeFirstColumn')?.addEventListener('change', event => {
        state.settings.freezeFirstColumn = event.target.checked;
        applySettings();
        saveSettings();
        renderTableHeader();
        renderTable();
    });

    document.getElementById('interfaceLanguage')?.addEventListener('change', event => {
        state.settings.language = tableLanguage(event.target.value);
        applySettings();
        saveSettings();
        renderTableHeader();
        updateFieldFilter();
        renderTable();
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
        showInAppToast(t('soundTest'), t('soundTestMessage'), { titleKey: 'soundTest', messageKey: 'soundTestMessage', broadcastToTabs: true, source: 'sound_test', eventType: 'notification_test' });
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

    document.getElementById('enableRowIcons')?.addEventListener('change', event => {
        state.settings.enableRowIcons = event.target.checked;
        saveSettings();
        renderTable();
    });
    document.getElementById('addRowIconRule')?.addEventListener('click', addRowIconRule);
    const fallbackIcon = document.getElementById('rowIconFallbackIcon');
    const fallbackColor = document.getElementById('rowIconFallbackColor');
    const fallbackLabel = document.getElementById('rowIconFallbackLabel');
    fallbackIcon?.addEventListener('change', () => {
        state.settings.rowIconFallback.icon = fallbackIcon.value;
        scheduleTableUXSave();
    });
    fallbackColor?.addEventListener('input', () => {
        state.settings.rowIconFallback.color = fallbackColor.value;
        scheduleTableUXPreview();
    });
    fallbackColor?.addEventListener('change', () => scheduleTableUXSave());
    fallbackLabel?.addEventListener('input', () => {
        state.settings.rowIconFallback.label = fallbackLabel.value.trim();
        scheduleTableUXPreview();
    });
    fallbackLabel?.addEventListener('change', () => scheduleTableUXSave());
    
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
        const targetTab = settingsTarget === '1' ? 'ui' : settingsTarget;
        if (targetTab) {
            state.revealedSettingsTabs.add(targetTab);
        }
        updateSettingsTabVisibility({ preserveActive: true });
        setActiveSettingsTab(targetTab);
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
        updateSettingsTabVisibility();
    } catch (error) {
        console.error('Failed to fetch file info:', error);
        updateFooterFileName();
        updateSettingsTabVisibility();
    }
}

// Fetch initial data
async function fetchInitialData() {
    try {
        const [response, categoriesResponse] = await Promise.all([
            fetch('/api/records'),
            fetch('/api/categories')
        ]);
        
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        
        const data = await response.json();
        
        state.catalogProducts = normalizeRecordsPayload(data);
        if (categoriesResponse.ok) {
            state.catalogCategories = normalizeCategoriesPayload(await categoriesResponse.json());
            state.catalogCategoriesAvailable = true;
        } else {
            state.catalogCategories = [];
            state.catalogCategoriesAvailable = false;
            state.catalogView = 'products';
        }
        selectActiveCatalogRows();
        refreshRecordSelectionKeys({ reset: true });
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
