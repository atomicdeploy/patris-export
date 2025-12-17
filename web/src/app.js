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
    columnFilters: {},  // Store active filters per column: { fieldName: { type, value, ... } }
    hiddenColumns: new Set(),  // Track hidden columns
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
function saveSettings() {
    localStorage.setItem('patris-settings', JSON.stringify(state.settings));
}

// Save sort preferences to localStorage
function saveSortPreferences() {
    localStorage.setItem('patris-sort', JSON.stringify({
        field: state.sortField,
        direction: state.sortDirection
    }));
}

// Save hidden columns to localStorage
function saveHiddenColumns() {
    localStorage.setItem('patris-hidden-columns', JSON.stringify([...state.hiddenColumns]));
}

// Save column filters to localStorage
function saveColumnFilters() {
    localStorage.setItem('patris-column-filters', JSON.stringify(state.columnFilters));
}

// Jalali date utilities
const JalaliUtils = {
    // Parse Jalali date string in format YY.mm.dd or YY/mm/dd
    parse(dateStr) {
        if (!dateStr || typeof dateStr !== 'string') return null;
        const parts = dateStr.split(/[.\/]/);
        if (parts.length !== 3) return null;
        
        const year = parseInt(parts[0]);
        const month = parseInt(parts[1]);
        const day = parseInt(parts[2]);
        
        if (isNaN(year) || isNaN(month) || isNaN(day)) return null;
        if (month < 1 || month > 12 || day < 1 || day > 31) return null;
        
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
        if (!str || typeof str !== 'string') return false;
        // Match YY.mm.dd or YY/mm/dd format
        return /^\d{2}[.\/]\d{2}[.\/]\d{2}$/.test(str.trim());
    }
};

// Detect field type based on values
function detectFieldType(field, records) {
    if (records.length === 0) return 'text';
    
    // Get sample values (up to 100 records for performance)
    const sampleSize = Math.min(100, records.length);
    const values = [];
    
    for (let i = 0; i < sampleSize; i++) {
        let value;
        // Handle ANBAR fields
        if (field.startsWith('ANBAR') && field.length > 5) {
            const anbarIndex = parseInt(field.substring(5)) - 1;
            if (records[i].ANBAR && Array.isArray(records[i].ANBAR)) {
                value = records[i].ANBAR[anbarIndex];
            }
        } else {
            value = records[i][field];
        }
        
        if (value !== null && value !== undefined && value !== '') {
            values.push(value);
        }
    }
    
    if (values.length === 0) return 'text';
    
    // Check for date pattern (Jalali: YY.mm.dd or YY/mm/dd)
    const jalaliDateCount = values.filter(v => 
        typeof v === 'string' && JalaliUtils.isJalaliDate(v)
    ).length;
    
    if (jalaliDateCount > values.length * 0.8) {
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
    if (uniqueValues.size <= 20 && uniqueValues.size < values.length * 0.5) {
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
    
    for (const record of records) {
        let value;
        // Handle ANBAR fields
        if (field.startsWith('ANBAR') && field.length > 5) {
            const anbarIndex = parseInt(field.substring(5)) - 1;
            if (record.ANBAR && Array.isArray(record.ANBAR)) {
                value = record.ANBAR[anbarIndex];
            }
        } else {
            value = record[field];
        }
        
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
        
        // Store file name if provided
        if (data.file_name) {
            state.fileName = data.file_name;
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
        analyzeFields(); // Analyze field types and stats
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
    } else {
        footerFile.textContent = 'Loading...';
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
    
    // Check if we have ANBAR fields to create grouped headers
    const anbarFields = state.fields.filter(f => f.startsWith('ANBAR') && f.length > 5);
    const hasAnbarFields = anbarFields.length > 0;
    
    const headerRow = document.createElement('tr');
    const filterRow = document.createElement('tr');
    filterRow.className = 'filter-row';
    
    // Track which fields we've processed for ANBAR grouping
    let processedAnbar = false;
    
    state.fields.forEach(field => {
        // Skip hidden columns
        if (state.hiddenColumns.has(field)) {
            return;
        }
        
        // Handle ANBAR grouped columns
        if (field.startsWith('ANBAR') && field.length > 5 && !processedAnbar) {
            // Filter visible ANBAR fields
            const visibleAnbarFields = anbarFields.filter(f => !state.hiddenColumns.has(f));
            
            if (visibleAnbarFields.length > 0 && hasAnbarFields) {
                // Create group header for all visible ANBAR columns (spans across ANBAR columns)
                const groupTh = document.createElement('th');
                groupTh.textContent = 'ANBAR';
                groupTh.setAttribute('colspan', visibleAnbarFields.length);
                groupTh.className = 'anbar-group-header';
                // Note: This is only used when we have ANBAR grouping
                
                // Create individual ANBAR column headers and filters
                visibleAnbarFields.forEach(anbarField => {
                    const anbarNum = anbarField.substring(5); // Extract number
                    const th = document.createElement('th');
                    th.className = 'sortable anbar-column';
                    
                    const sortContainer = document.createElement('div');
                    sortContainer.className = 'header-content';
                    
                    const fieldName = document.createElement('span');
                    fieldName.textContent = anbarNum;
                    sortContainer.appendChild(fieldName);
                    
                    const sortIndicator = document.createElement('span');
                    sortIndicator.className = 'sort-indicator';
                    if (state.sortField === anbarField) {
                        sortIndicator.textContent = state.sortDirection === 'asc' ? '▲' : '▼';
                        sortIndicator.style.opacity = '1';
                    } else {
                        sortIndicator.textContent = '▲';
                        sortIndicator.style.opacity = '0.3';
                    }
                    sortContainer.appendChild(sortIndicator);
                    
                    th.appendChild(sortContainer);
                    th.addEventListener('click', () => sortByField(anbarField));
                    headerRow.appendChild(th);
                    
                    // Create filter cell for this ANBAR field
                    const filterTh = document.createElement('th');
                    filterTh.appendChild(createFilterControl(anbarField));
                    filterRow.appendChild(filterTh);
                });
            }
            
            processedAnbar = true;
        } else if (!field.startsWith('ANBAR') || field.length <= 5) {
            // Regular field header
            const th = document.createElement('th');
            th.className = 'sortable';
            
            const sortContainer = document.createElement('div');
            sortContainer.className = 'header-content';
            
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
            
            if (field === 'Code') {
                th.classList.add('sticky-column');
            }
            
            th.addEventListener('click', () => sortByField(field));
            headerRow.appendChild(th);
            
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
    headerRow.appendChild(actionsHeader);
    
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
    
    thead.appendChild(headerRow);
    thead.appendChild(filterRow);
}

// Create filter control based on field type
function createFilterControl(field) {
    const container = document.createElement('div');
    container.className = 'filter-control';
    
    const stats = state.fieldStats[field];
    if (!stats) return container;
    
    const fieldType = stats.type;
    const currentFilter = state.columnFilters[field];
    
    if (fieldType === 'categorical') {
        // Dropdown for categorical fields
        const select = document.createElement('select');
        select.className = 'filter-select';
        
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
        // Range inputs for numeric fields
        const wrapper = document.createElement('div');
        wrapper.className = 'filter-range';
        
        const minInput = document.createElement('input');
        minInput.type = 'number';
        minInput.className = 'filter-input-small';
        minInput.placeholder = 'Min';
        minInput.value = currentFilter?.min ?? '';
        
        const maxInput = document.createElement('input');
        maxInput.type = 'number';
        maxInput.className = 'filter-input-small';
        maxInput.placeholder = 'Max';
        maxInput.value = currentFilter?.max ?? '';
        
        const updateFilter = () => {
            const min = minInput.value ? parseFloat(minInput.value) : null;
            const max = maxInput.value ? parseFloat(maxInput.value) : null;
            
            if (min !== null || max !== null) {
                state.columnFilters[field] = { type: 'numeric', min, max };
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
        
        const maxInput = document.createElement('input');
        maxInput.type = 'text';
        maxInput.className = 'filter-input-small';
        maxInput.placeholder = 'To';
        maxInput.value = currentFilter?.max ?? '';
        maxInput.title = 'Format: YY.mm.dd';
        
        const updateFilter = () => {
            const min = minInput.value.trim();
            const max = maxInput.value.trim();
            
            if (min || max) {
                state.columnFilters[field] = { type: 'jalali-date', min, max };
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
        
        input.addEventListener('input', (e) => {
            const value = e.target.value.trim();
            if (value) {
                state.columnFilters[field] = { type: 'text', value };
            } else {
                delete state.columnFilters[field];
            }
            saveColumnFilters();
            applyFilters();
        });
        
        container.appendChild(input);
    }
    
    return container;
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
                const anbarIndex = parseInt(field.substring(5)) - 1;
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
            }
            saveHiddenColumns();
            renderTableHeader();
            renderTable();
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

// Filter records based on search term, selected field, and column filters
function filterRecords() {
    // Start with all records
    let filtered = state.records;
    
    // Apply column filters (AND logic - all filters must pass)
    if (Object.keys(state.columnFilters).length > 0) {
        filtered = filtered.filter(record => {
            // Check each active filter
            for (const [field, filter] of Object.entries(state.columnFilters)) {
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
        const anbarIndex = parseInt(field.substring(5)) - 1;
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
    
    // Initialize footer
    updateFooter();
    
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
    
    // Column manager
    document.getElementById('columnsBtn').addEventListener('click', () => {
        renderColumnManager();
        document.getElementById('columnsPanel').classList.toggle('open');
    });
    
    document.getElementById('closeColumns').addEventListener('click', () => {
        document.getElementById('columnsPanel').classList.remove('open');
    });
    
    document.getElementById('showAllColumns').addEventListener('click', () => {
        state.hiddenColumns.clear();
        saveHiddenColumns();
        renderColumnManager();
        renderTableHeader();
        renderTable();
    });
    
    document.getElementById('hideAllColumns').addEventListener('click', () => {
        // Don't allow hiding Code column
        state.fields.forEach(field => {
            if (field !== 'Code') {
                state.hiddenColumns.add(field);
            }
        });
        saveHiddenColumns();
        renderColumnManager();
        renderTableHeader();
        renderTable();
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
            analyzeFields(); // Analyze field types and stats
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
