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
    isInitialLoad: true,
    columnFilters: {},  // Store active filters per column: { fieldName: { type, value, ... } }
    hiddenColumns: new Set(),  // Track hidden columns
    settings: {
        autoScrollToChanged: false,
        highlightChanges: true,
        enablePagination: false,
        pageSize: 100,
        playNotificationSound: false,
        notificationSoundSource: 'external'
    },
    notificationAudio: null,
    originalTitle: document.title,
    originalFavicon: null,
    titleFlashInterval: null,
    faviconTimeout: null,
    tabId: '',
    broadcastChannel: null,
    seenBroadcastMessages: new Set()
};

const CONFIG_STORAGE_KEY = 'patris-config';
const SETTINGS_STORAGE_KEY = 'patris-settings';
const RESOURCE_POLL_INTERVAL_MS = 30000;
const BROADCAST_CHANNEL_NAME = 'patris-export-frontend';
const BROADCAST_STORAGE_KEY = 'patris-broadcast-message';
const BROADCAST_MESSAGE_TTL_MS = 30000;

function createTabId() {
    if (window.crypto?.randomUUID) {
        return window.crypto.randomUUID();
    }
    return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
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
    
    // Load column filters
    const savedFilters = localStorage.getItem('patris-column-filters');
    if (savedFilters) {
        try {
            state.columnFilters = JSON.parse(savedFilters);
        } catch (e) {
            state.columnFilters = {};
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
    if (!config) return;
    state.config = config;
    localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    if (config.ui) {
        state.settings = {
            ...state.settings,
            autoScrollToChanged: !!config.ui.auto_scroll_to_changed,
            highlightChanges: config.ui.highlight_changes !== false,
            enablePagination: !!config.ui.enable_pagination,
            pageSize: config.ui.page_size || state.settings.pageSize,
            playNotificationSound: !!config.ui.play_notification_sound,
            notificationSoundSource: config.ui.notification_sound_source || state.settings.notificationSoundSource
        };
        localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(state.settings));
        applySettings();
        initTheme();
    }
    if (source !== 'local') {
        console.info('⚙️ Configuration applied from %s', source, config);
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
        theme: localStorage.getItem('theme') || 'system',
        auto_scroll_to_changed: state.settings.autoScrollToChanged,
        highlight_changes: state.settings.highlightChanges,
        enable_pagination: state.settings.enablePagination,
        page_size: state.settings.pageSize,
        play_notification_sound: state.settings.playNotificationSound,
        notification_sound_source: state.settings.notificationSoundSource
    };
    saveConfigToServer(state.config);
}

let configSaveTimer = null;
function saveConfigToServer(config) {
    localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
    clearTimeout(configSaveTimer);
    configSaveTimer = setTimeout(async () => {
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
        }
    }, 250);
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
            hiddenColumns: [...state.hiddenColumns]
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
    document.getElementById('autoScrollToChanged').checked = state.settings.autoScrollToChanged;
    document.getElementById('highlightChanges').checked = state.settings.highlightChanges;
    document.getElementById('enablePagination').checked = state.settings.enablePagination;
    document.getElementById('pageSize').value = state.settings.pageSize;
    document.getElementById('playNotificationSound').checked = state.settings.playNotificationSound;
    document.getElementById('notificationSoundSource').value = state.settings.notificationSoundSource || 'external';
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

function openPanel(panelId) {
    closePanels();
    document.getElementById(panelId).classList.add('open');
    document.getElementById('panelBackdrop').classList.add('open');
}

function closePanels() {
    ['settingsPanel', 'columnsPanel'].forEach(id => {
        const panel = document.getElementById(id);
        if (panel) panel.classList.remove('open');
    });
    const backdrop = document.getElementById('panelBackdrop');
    if (backdrop) backdrop.classList.remove('open');
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
    showInAppToast('Updating interface', 'A newer embedded web UI is available. Reloading now.');

    const url = new URL(window.location.href);
    url.searchParams.set('resource_version', nextResourceVersion);
    url.searchParams.set('reloaded_at', Date.now().toString());

    setTimeout(() => {
        window.location.replace(url.toString());
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
            showInAppToast('Native toast unavailable', result.native_error, { error: true });
        }
    } catch (error) {
        showInAppToast('Toast request failed', error.message, { error: true });
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
            showInAppToast('Refresh requested', 'The backend is reloading the data source.', { broadcastToTabs: true });
        } else {
            await fetchInitialData();
            showInAppToast('Refreshed', 'Data was reloaded over HTTP.', { broadcastToTabs: true });
        }
    } catch (error) {
        console.error('Failed to refresh data source:', error);
        showInAppToast('Refresh failed', error.message, { error: true });
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
        }
    } else if (data.type === 'toast') {
        showInAppToast(data.title, data.message, { error: !!data.native_error });
        if (data.native_error) {
            console.warn('Native toast failed:', data.native_error);
        }
    } else if (data.type === 'config_update') {
        applyConfig(data.config, 'file watcher');
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
    
    indicator.className = 'status-indicator ' + status;
    statusText.textContent = text;
    
    // Update footer connection status
    updateFooterConnection(text);
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
    // Use basename of the file (just the file name, not full path)
    if (state.fileName) {
        const baseName = state.fileName.split('/').pop().split('\\').pop();
        footerFile.textContent = baseName;
        footerFile.title = state.fileName;
    } else {
        footerFile.textContent = 'Unknown';
        footerFile.removeAttribute('title');
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
    const footerLastUpdate = document.getElementById('footerLastUpdate');
    if (timestamp) {
        const date = new Date(timestamp);
        footerLastUpdate.textContent = formatDateTime(date);
    } else {
        const now = new Date();
        footerLastUpdate.textContent = formatDateTime(now);
    }
}

function updateFooterRecordCount() {
    const footerRecordCount = document.getElementById('footerRecordCount');
    footerRecordCount.textContent = state.records.length.toLocaleString();
}

function updateFooterConnection(status) {
    const footerConnection = document.getElementById('footerConnection');
    footerConnection.textContent = status;
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
                groupTh.textContent = 'ANBAR';
                groupTh.setAttribute('colspan', visibleAnbarFields.length);
                groupTh.className = 'anbar-group-header';
                groupRow.appendChild(groupTh);

                // Create individual ANBAR column headers and filters
                visibleAnbarFields.forEach(anbarField => {
                    const anbarNum = anbarField.substring(5); // Extract number
                    const label = displayFieldName(anbarField) === anbarField ? anbarNum : displayFieldName(anbarField);
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
    sortContainer.className = 'header-content';

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
        container.appendChild(createRangePopover(field, currentFilter, 'numeric'));
        
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
    
    const recordsToShow = state.settings.enablePagination 
        ? state.filteredRecords.slice(0, state.settings.pageSize)
        : state.filteredRecords;
    
    tbody.innerHTML = '';
    
    recordsToShow.forEach((record, displayIndex) => {
        const row = document.createElement('tr');
        const codeInfo = parsePatrisCode(record.Code);
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
            inspectRecord(record);
        };
        
        actionsCell.appendChild(inspectBtn);
        row.appendChild(actionsCell);
        
        // Make row clickable to inspect
        row.onclick = () => inspectRecord(record);
        
        tbody.appendChild(row);
    });
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
    
    state.fields.forEach(field => {
        const label = document.createElement('label');
        label.className = 'checkbox-label';
        
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.checked = !state.hiddenColumns.has(field);
        // Disable Code column checkbox (always visible)
        checkbox.disabled = field === 'Code';
        
        checkbox.addEventListener('change', (e) => {
            if (e.target.checked) {
                state.hiddenColumns.delete(field);
            } else {
                state.hiddenColumns.add(field);
                delete state.columnFilters[field];
            }
            saveHiddenColumns();
            saveColumnFilters();
            renderTableHeader();
            applyFilters();
        });
        
        const span = document.createElement('span');
        span.textContent = field + (field === 'Code' ? ' (always visible)' : '');
        
        label.appendChild(checkbox);
        label.appendChild(span);
        container.appendChild(label);
    });
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

// Initialize theme
function initTheme() {
    const savedTheme = localStorage.getItem('theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const isDark = savedTheme === 'dark' || (!savedTheme && prefersDark);
    
    if (isDark) {
        document.body.classList.add('dark-mode');
        document.documentElement.classList.add('dark-mode');
    } else {
        document.body.classList.remove('dark-mode');
        document.documentElement.classList.remove('dark-mode');
    }
    updateThemeIcon(isDark);
}

// Initialize app
function init() {
    setLoadingState(true);
    initFrontendBroadcast();

    // Load settings
    loadSettings();
    applySettings();
    
    // Initialize theme
    initTheme();
    
    // Initialize footer
    updateFooter();
    
    // Set up event listeners
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
    });
    
    document.getElementById('settingsBtn').addEventListener('click', () => {
        openPanel('settingsPanel');
    });
    
    document.getElementById('closeSettings').addEventListener('click', () => {
        closePanels();
    });
    document.getElementById('panelBackdrop').addEventListener('click', closePanels);
    
    document.getElementById('closeInspector').addEventListener('click', () => {
        document.getElementById('inspectorPanel').classList.remove('open');
    });
    
    // Column manager
    document.getElementById('columnsBtn').addEventListener('click', () => {
        renderColumnManager();
        openPanel('columnsPanel');
    });
    
    document.getElementById('closeColumns').addEventListener('click', () => {
        closePanels();
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
    
    document.getElementById('playNotificationSound').addEventListener('change', (e) => {
        state.settings.playNotificationSound = e.target.checked;
        saveSettings();
    });

    document.getElementById('notificationSoundSource').addEventListener('change', (e) => {
        state.settings.notificationSoundSource = e.target.value;
        saveSettings();
    });

    document.getElementById('testNotificationSound').addEventListener('click', () => {
        playNotificationSound(true);
        showInAppToast('Sound test', 'Notification audio was triggered.', { broadcastToTabs: true });
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
        
        // data is now in transformed format: { "101": {...}, "102": {...}, ... }
        // Convert to array, adding Code field from the key
        state.records = Object.entries(data).map(([code, record]) => ({
            Code: code,
            ...record
        }));
        
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
