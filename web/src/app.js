// Application state
const state = {
    records: [],
    filteredRecords: [],
    fields: [],
    ws: null,
    searchTerm: '',
    selectedField: '',
    sortField: 'Code',
    sortDirection: 'asc',
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
        // Initial load - records are already transformed with ANBAR as array
        state.records = data.added || [];
        
        // Mark all as changed for initial highlight
        if (state.settings.highlightChanges) {
            state.records.forEach((_, index) => changedIndices.add(index));
        }
    } else if (data.type === 'update') {
        // Incremental update
        
        // Handle deleted records (by Code)
        if (data.deleted && data.deleted.length > 0) {
            const deletedCodes = new Set(data.deleted.map(String));
            state.records = state.records.filter(record => {
                const code = String(record.Code);
                return !deletedCodes.has(code);
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
        }
    }
    
    // Extract fields from first record if not already set
    if (state.records.length > 0 && state.fields.length === 0) {
        extractFields();
        renderTableHeader();
        updateFieldFilter();
    }
    
    filterRecords();
    sortRecords();
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
        
        // Ensure Code is first, then other fields, then ANBAR columns
        state.fields = ['Code', ...nonAnbarFields.filter(f => f !== 'Code'), ...anbarFields];
    } else {
        // Just ensure Code is first
        state.fields = ['Code', ...nonAnbarFields.filter(f => f !== 'Code')];
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

// Render table header
function renderTableHeader() {
    const thead = document.getElementById('tableHead');
    const headerRow = document.createElement('tr');
    
    state.fields.forEach(field => {
        const th = document.createElement('th');
        th.className = 'sortable';
        
        // Create sort indicator container
        const sortContainer = document.createElement('div');
        sortContainer.style.display = 'flex';
        sortContainer.style.alignItems = 'center';
        sortContainer.style.gap = '0.5rem';
        sortContainer.style.cursor = 'pointer';
        
        const fieldName = document.createElement('span');
        fieldName.textContent = field;
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
        
        // Make Code column sticky
        if (field === 'Code') {
            th.classList.add('sticky-column');
        }
        
        // Add click handler for sorting
        th.addEventListener('click', () => sortByField(field));
        
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
    
    recordsToShow.forEach((record, displayIndex) => {
        const row = document.createElement('tr');
        
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
            const td = document.createElement('td');
            
            // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
            if (field.startsWith('ANBAR') && field.length > 5) {
                const anbarIndex = parseInt(field.substring(5)) - 1;
                if (record.ANBAR && Array.isArray(record.ANBAR) && anbarIndex < record.ANBAR.length) {
                    const value = record.ANBAR[anbarIndex];
                    td.textContent = value !== null && value !== undefined ? value : '';
                } else {
                    td.textContent = '';
                }
            } else {
                const value = record[field];
                td.textContent = value !== null && value !== undefined ? value : '';
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
            const anbarIndex = parseInt(state.sortField.substring(5)) - 1;
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
                const anbarIndex = parseInt(field.substring(5)) - 1;
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
        
        // Handle ANBAR fields (ANBAR1, ANBAR2, etc.)
        let value;
        if (field.startsWith('ANBAR') && field.length > 5) {
            const anbarIndex = parseInt(field.substring(5)) - 1;
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
        sortRecords();
        renderTable();
        updateCounts();
    });
    
    document.getElementById('fieldFilter').addEventListener('change', (e) => {
        state.selectedField = e.target.value;
        filterRecords();
        sortRecords();
        renderTable();
        updateCounts();
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
            renderTableHeader();
            updateFieldFilter();
        }
        
        filterRecords();
        renderTable();
        updateCounts();
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
