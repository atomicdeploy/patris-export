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
    fileName: '',  // Track the actual data file name from server
    columnFilters: {},  // Store active filters per column
    hiddenColumns: new Set(),  // Track hidden columns
    settings: {
        autoScrollToChanged: false,
        highlightChanges: true,
        enablePagination: false,
        pageSize: 100,
        playNotificationSound: false
    },
    notificationAudio: null,
    originalTitle: document.title,
    originalFavicon: null
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
    document.getElementById('playNotificationSound').checked = state.settings.playNotificationSound;
}

// Initialize notification audio
function initNotificationAudio() {
    state.notificationAudio = new Audio('/api/notification.wav');
    state.notificationAudio.volume = 0.5; // Set volume to 50%
    state.notificationAudio.preload = 'auto'; // Preload for faster playback
}

// Play notification sound
function playNotificationSound() {
    if (state.settings.playNotificationSound && state.notificationAudio) {
        // Reset and play
        state.notificationAudio.currentTime = 0;
        state.notificationAudio.play().catch(err => {
            console.log('Could not play notification sound:', err);
        });
    }
}

// Flash page title with notification info
function flashTitle(message) {
    const originalTitle = state.originalTitle;
    let flashCount = 0;
    const maxFlashes = 6; // Flash 3 times (on/off cycle)
    
    const flashInterval = setInterval(() => {
        document.title = flashCount % 2 === 0 ? `🔔 ${message}` : originalTitle;
        flashCount++;
        
        if (flashCount >= maxFlashes) {
            clearInterval(flashInterval);
            document.title = originalTitle;
        }
    }, 500); // Flash every 500ms
}

// Change favicon temporarily
function flashFavicon() {
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
    setTimeout(() => {
        if (state.originalFavicon) {
            setFavicon(state.originalFavicon);
        }
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
            
            // Flash title with change info
            flashTitle(`${totalChanges} record${totalChanges > 1 ? 's' : ''} updated`);
            
            // Flash favicon
            flashFavicon();
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

// Render table header
function renderTableHeader() {
    const thead = document.getElementById('tableHead');
    
    // Check if we have ANBAR fields to create grouped headers
    const anbarFields = state.fields.filter(f => f.startsWith('ANBAR') && f.length > 5);
    const hasAnbarFields = anbarFields.length > 0;
    
    if (hasAnbarFields) {
        // Create two-row header with ANBAR group
        const groupRow = document.createElement('tr');
        const fieldRow = document.createElement('tr');
        
        // Track which fields we've processed
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
                
                if (visibleAnbarFields.length > 0) {
                    // Create group header for all visible ANBAR columns
                    const groupTh = document.createElement('th');
                    groupTh.textContent = 'ANBAR';
                    groupTh.setAttribute('colspan', visibleAnbarFields.length);
                    groupTh.className = 'anbar-group-header';
                    groupRow.appendChild(groupTh);
                    
                    // Create individual ANBAR column headers
                    visibleAnbarFields.forEach(anbarField => {
                        const anbarNum = anbarField.substring(5); // Extract number
                        const th = document.createElement('th');
                        th.className = 'sortable anbar-column';
                        
                        const sortContainer = document.createElement('div');
                        sortContainer.style.display = 'flex';
                        sortContainer.style.alignItems = 'center';
                        sortContainer.style.gap = '0.5rem';
                        sortContainer.style.cursor = 'pointer';
                        
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
                        fieldRow.appendChild(th);
                    });
                }
                
                processedAnbar = true;
            } else if (!field.startsWith('ANBAR') || field.length <= 5) {
                // Regular field
                const groupTh = document.createElement('th');
                groupTh.setAttribute('rowspan', '2');
                groupTh.className = 'sortable';
                
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
                
                groupTh.appendChild(sortContainer);
                
                // Make Code column sticky
                if (field === 'Code') {
                    groupTh.classList.add('sticky-column');
                }
                
                groupTh.addEventListener('click', () => sortByField(field));
                groupRow.appendChild(groupTh);
            }
        });
        
        // Add actions column
        const actionsHeader = document.createElement('th');
        actionsHeader.textContent = 'Actions';
        actionsHeader.setAttribute('rowspan', '2');
        actionsHeader.style.width = '100px';
        groupRow.appendChild(actionsHeader);
        
        thead.innerHTML = '';
        thead.appendChild(groupRow);
        thead.appendChild(fieldRow);
    } else {
        // Simple single-row header
        const headerRow = document.createElement('tr');
        
        state.fields.forEach(field => {
            // Skip hidden columns
            if (state.hiddenColumns.has(field)) {
                return;
            }
            
            const th = document.createElement('th');
            th.className = 'sortable';
            
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
            
            if (field === 'Code') {
                th.classList.add('sticky-column');
            }
            
            th.addEventListener('click', () => sortByField(field));
            headerRow.appendChild(th);
        });
        
        const actionsHeader = document.createElement('th');
        actionsHeader.textContent = 'Actions';
        actionsHeader.style.width = '100px';
        headerRow.appendChild(actionsHeader);
        
        thead.innerHTML = '';
        thead.appendChild(headerRow);
    }
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
    
    document.getElementById('playNotificationSound').addEventListener('change', (e) => {
        state.settings.playNotificationSound = e.target.checked;
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
    
    // Initialize notification audio
    initNotificationAudio();
    
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
