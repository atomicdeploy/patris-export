// Application state
const state = {
    records: [],
    filteredRecords: [],
    fields: [],
    ws: null,
    searchTerm: '',
    selectedField: '',
    settings: {
        autoScrollToChanged: false,
        highlightChanges: true,
        enablePagination: false,
        pageSize: 100
    }
};

// Load settings from localStorage
function loadSettings() {
    const saved = localStorage.getItem('patris-settings');
    if (saved) {
        state.settings = { ...state.settings, ...JSON.parse(saved) };
    }
}

// Save settings to localStorage
function saveSettings() {
    localStorage.setItem('patris-settings', JSON.stringify(state.settings));
}

// Apply settings to UI
function applySettings() {
    document.getElementById('autoScrollToChanged').checked = state.settings.autoScrollToChanged;
    document.getElementById('highlightChanges').checked = state.settings.highlightChanges;
    document.getElementById('enablePagination').checked = state.settings.enablePagination;
    document.getElementById('pageSize').value = state.settings.pageSize;
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
        // Initial load - set all records
        state.records = data.added || [];
        
        // Mark all as changed for initial highlight
        if (state.settings.highlightChanges) {
            state.records.forEach((_, index) => changedIndices.add(index));
        }
    } else if (data.type === 'update') {
        // Incremental update
        
        // Handle deleted records (from end to start to avoid index shifting)
        if (data.deleted && data.deleted.length > 0) {
            const sortedDeletes = [...data.deleted].sort((a, b) => b - a);
            sortedDeletes.forEach(index => {
                if (index >= 0 && index < state.records.length) {
                    state.records.splice(index, 1);
                }
            });
        }
        
        // Handle added records
        if (data.added && data.added.length > 0) {
            const startIndex = state.records.length;
            state.records.push(...data.added);
            
            // Mark added records as changed
            data.added.forEach((_, i) => {
                changedIndices.add(startIndex + i);
            });
        }
        
        // Handle modified records
        if (data.modified && data.modified.length > 0) {
            // For simplicity, we'll mark records as modified if they match by key
            data.modified.forEach(modifiedRecord => {
                const modKey = JSON.stringify(modifiedRecord);
                const index = state.records.findIndex(r => JSON.stringify(r) === modKey);
                if (index !== -1) {
                    state.records[index] = modifiedRecord;
                    changedIndices.add(index);
                }
            });
        }
    }
    
    // Extract fields from first record if not already set
    if (state.records.length > 0 && state.fields.length === 0) {
        state.fields = Object.keys(state.records[0]);
        renderTableHeader();
        updateFieldFilter();
    }
    
    filterRecords();
    renderTable(changedIndices);
    updateCounts();
}

// Update connection status
function updateStatus(status, text) {
    const indicator = document.getElementById('statusIndicator');
    const statusText = document.getElementById('statusText');
    
    indicator.className = 'status-indicator ' + status;
    statusText.textContent = text;
}

// Render table header
function renderTableHeader() {
    const thead = document.getElementById('tableHead');
    const headerRow = document.createElement('tr');
    
    state.fields.forEach(field => {
        const th = document.createElement('th');
        th.textContent = field;
        headerRow.appendChild(th);
    });
    
    // Add actions column
    const actionsHeader = document.createElement('th');
    actionsHeader.textContent = 'Actions';
    actionsHeader.style.width = '100px';
    headerRow.appendChild(actionsHeader);
    
    thead.innerHTML = '';
    thead.appendChild(headerRow);
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
    
    recordsToShow.forEach((record, index) => {
        const row = document.createElement('tr');
        
        // Add highlight class if changed
        if (changedIndices.has(index) && state.settings.highlightChanges) {
            row.classList.add('changed');
            
            // Scroll to changed item if setting is enabled
            if (state.settings.autoScrollToChanged && index === Math.min(...changedIndices)) {
                setTimeout(() => {
                    row.scrollIntoView({ behavior: 'smooth', block: 'center' });
                }, 100);
            }
        }
        
        // Add data cells
        state.fields.forEach(field => {
            const td = document.createElement('td');
            const value = record[field];
            td.textContent = value !== null && value !== undefined ? value : '';
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

// Filter records based on search term and selected field
function filterRecords() {
    if (!state.searchTerm && !state.selectedField) {
        state.filteredRecords = state.records;
        return;
    }
    
    state.filteredRecords = state.records.filter(record => {
        // Field filter
        if (state.selectedField) {
            const value = record[state.selectedField];
            if (!value) return false;
        }
        
        // Search filter
        if (state.searchTerm) {
            const searchLower = state.searchTerm.toLowerCase();
            return state.fields.some(field => {
                const value = record[field];
                if (value === null || value === undefined) return false;
                return String(value).toLowerCase().includes(searchLower);
            });
        }
        
        return true;
    });
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
        const value = record[field];
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
    localStorage.setItem('theme', isDark ? 'dark' : 'light');
    updateThemeIcon(isDark);
}

// Update theme icon
function updateThemeIcon(isDark) {
    const btn = document.getElementById('themeToggle');
    btn.textContent = isDark ? '☀️' : '🌙';
}

// Initialize theme
function initTheme() {
    const savedTheme = localStorage.getItem('theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const isDark = savedTheme === 'dark' || (!savedTheme && prefersDark);
    
    if (isDark) {
        document.body.classList.add('dark-mode');
    }
    updateThemeIcon(isDark);
}

// Initialize app
function init() {
    // Load settings
    loadSettings();
    applySettings();
    
    // Initialize theme
    initTheme();
    
    // Set up event listeners
    document.getElementById('searchInput').addEventListener('input', (e) => {
        state.searchTerm = e.target.value;
        filterRecords();
        renderTable();
        updateCounts();
    });
    
    document.getElementById('fieldFilter').addEventListener('change', (e) => {
        state.selectedField = e.target.value;
        filterRecords();
        renderTable();
        updateCounts();
    });
    
    document.getElementById('themeToggle').addEventListener('click', toggleTheme);
    
    document.getElementById('settingsBtn').addEventListener('click', () => {
        document.getElementById('settingsPanel').classList.toggle('open');
    });
    
    document.getElementById('closeSettings').addEventListener('click', () => {
        document.getElementById('settingsPanel').classList.remove('open');
    });
    
    document.getElementById('closeInspector').addEventListener('click', () => {
        document.getElementById('inspectorPanel').classList.remove('open');
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
    
    document.getElementById('enablePagination').addEventListener('change', (e) => {
        state.settings.enablePagination = e.target.checked;
        saveSettings();
        renderTable();
    });
    
    document.getElementById('pageSize').addEventListener('change', (e) => {
        state.settings.pageSize = parseInt(e.target.value);
        saveSettings();
        if (state.settings.enablePagination) {
            renderTable();
        }
    });
    
    // Initialize WebSocket
    initWebSocket();
    
    // Fetch initial data via HTTP
    fetchInitialData();
}

// Fetch initial data
async function fetchInitialData() {
    try {
        const response = await fetch('/api/records');
        const data = await response.json();
        
        if (data.success && data.records) {
            state.records = data.records;
            
            if (state.records.length > 0) {
                state.fields = Object.keys(state.records[0]);
                renderTableHeader();
                updateFieldFilter();
            }
            
            filterRecords();
            renderTable();
            updateCounts();
        }
    } catch (error) {
        console.error('Failed to fetch initial data:', error);
    }
}

// Start the application when DOM is ready
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
