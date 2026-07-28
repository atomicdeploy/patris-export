export const MIN_COLUMN_WIDTH = 80;
export const MAX_COLUMN_WIDTH = 480;
export const COLUMN_RESIZE_STEP = 12;
export const WAREHOUSE_FIELD_PREFIX = 'warehouse_stock::';

const COLUMN_LABELS = Object.freeze({
    product_code: Object.freeze({ en: 'Product Code', fa: 'کد کالا' }),
    category_code: Object.freeze({ en: 'Category Code', fa: 'کد دسته‌بندی' }),
    name: Object.freeze({ en: 'Name', fa: 'نام' }),
    part_number: Object.freeze({ en: 'Part Number', fa: 'پارت نامبر' }),
    serial: Object.freeze({ en: 'Serial Number', fa: 'شماره سریال' }),
    unit: Object.freeze({ en: 'Unit', fa: 'واحد' }),
    sale_price_source: Object.freeze({ en: 'Sale Price', fa: 'قیمت فروش' }),
    partner_price_source: Object.freeze({ en: 'Partner Price', fa: 'قیمت همکار' }),
    purchase_price_source: Object.freeze({ en: 'Purchase Price', fa: 'قیمت خرید' }),
    total_stock: Object.freeze({ en: 'Total Stock', fa: 'موجودی کل' }),
    minimum_stock: Object.freeze({ en: 'Minimum Stock', fa: 'حداقل موجودی' }),
    foreign_currency: Object.freeze({ en: 'Foreign Currency', fa: 'ارز خارجی' }),
    foreign_price: Object.freeze({ en: 'Foreign Price', fa: 'قیمت ارزی' }),
    weight_grams: Object.freeze({ en: 'Weight (g)', fa: 'وزن (گرم)' }),
    location: Object.freeze({ en: 'Location', fa: 'مکان' }),
    shipping_method_id: Object.freeze({ en: 'Shipping Method', fa: 'روش حمل' }),
    import_freight_method_id: Object.freeze({ en: 'Shipping Method', fa: 'روش حمل' }),
    shipping_price_per_kg: Object.freeze({ en: 'Shipping Rate per kg', fa: 'نرخ حمل هر کیلوگرم' }),
    shipping_price_per_kg_currency: Object.freeze({ en: 'Shipping Rate Currency (CNY/IRR)', fa: 'ارز نرخ حمل (یوان/ریال)' }),
    markup_percent: Object.freeze({ en: 'Profit Margin (%)', fa: 'حاشیه سود (درصد)' }),
    irt_per_cny: Object.freeze({ en: 'CNY Rate (IRT)', fa: 'نرخ یوان (تومان)' }),
    pricing_catalog_revision: Object.freeze({ en: 'Pricing Revision', fa: 'نسخه قیمت‌گذاری' }),
    pricing_catalog_status: Object.freeze({ en: 'Pricing Status', fa: 'وضعیت قیمت‌گذاری' }),
    currency_effective_date: Object.freeze({ en: 'Currency Effective Date', fa: 'تاریخ اعمال نرخ ارز' }),
    price_source_amount: Object.freeze({ en: 'Selected Price Amount', fa: 'مبلغ منبع قیمت' }),
    price_source_currency: Object.freeze({ en: 'Selected Price Currency', fa: 'ارز منبع قیمت' }),
    price_source_kind: Object.freeze({ en: 'Selected Price Source', fa: 'نوع منبع قیمت' }),
    price_rounding_digits: Object.freeze({ en: 'Price Rounding Digits', fa: 'تعداد رقم گردکردن قیمت' }),
    price_rounding_mode: Object.freeze({ en: 'Price Rounding Mode', fa: 'روش گردکردن قیمت' }),
    final_price: Object.freeze({ en: 'Final Price (IRT)', fa: 'قیمت نهایی (تومان)' }),
    formula_version: Object.freeze({ en: 'Formula Version', fa: 'نسخه فرمول' }),
    source_updated_at: Object.freeze({ en: 'Updated At', fa: 'زمان به‌روزرسانی' }),
    warnings: Object.freeze({ en: 'Warnings', fa: 'هشدارها' }),
    record_hash: Object.freeze({ en: 'Record Hash', fa: 'هش رکورد' }),
    description: Object.freeze({ en: 'Description', fa: 'شرح' }),
    sharh: Object.freeze({ en: 'Description', fa: 'شرح' }),
    sharh1: Object.freeze({ en: 'Description', fa: 'شرح' }),
    sharh2: Object.freeze({ en: 'Additional Description', fa: 'شرح تکمیلی' }),
    allanbar: Object.freeze({ en: 'Total Stock', fa: 'موجودی کل' }),
    dates: Object.freeze({ en: 'Updated Date', fa: 'تاریخ به‌روزرسانی' }),
    forosh: Object.freeze({ en: 'Sale Price', fa: 'قیمت فروش' }),
    invahed: Object.freeze({ en: 'Units per Package', fa: 'تعداد در واحد' }),
    kharyd: Object.freeze({ en: 'Purchase Price', fa: 'قیمت خرید' }),
    kharyd_e: Object.freeze({ en: 'Purchase Price (E)', fa: 'قیمت خرید (E)' }),
    sefaresh: Object.freeze({ en: 'Minimum Stock', fa: 'حد سفارش' }),
    tedad_k: Object.freeze({ en: 'Package Quantity', fa: 'تعداد بسته' }),
    vahed: Object.freeze({ en: 'Unit', fa: 'واحد' })
});

const LEGACY_DEFAULT_COLUMN_LABELS = new Set([
    'anbar', 'code', 'name', 'stock', 'warehouse', 'warehouse stock'
]);

const PRODUCT_CODE_DEFAULT_LABELS = new Set([
    'code',
    'product code',
    'patris code',
    'کد',
    'کد کالا',
    'کد پاتریس'
]);

export const ROW_ICON_NAMES = [
    'warning',
    'clock',
    'price',
    'weight',
    'stock',
    'package',
    'check',
    'info'
];

export const CANONICAL_ROW_FIELDS = [
    'product_code',
    'category_code',
    'name',
    'serial',
    'sale_price_source',
    'partner_price_source',
    'purchase_price_source',
    'total_stock',
    'warehouse_stock',
    'minimum_stock',
    'foreign_price',
    'weight_grams',
    'location',
    'shipping_method_id',
    'shipping_price_per_kg',
    'shipping_price_per_kg_currency',
    'markup_percent',
    'irt_per_cny',
    'price_source_amount',
    'price_source_currency',
    'price_source_kind',
    'price_rounding_digits',
    'price_rounding_mode',
    'final_price',
    'source_updated_at',
    'warnings'
];

export const ROW_RULE_OPERATORS = [
    'equals',
    'not_equals',
    'contains',
    'not_contains',
    'empty',
    'not_empty',
    'gt',
    'gte',
    'lt',
    'lte',
    'truthy',
    'falsy',
    'stale_days'
];

export const DEFAULT_ROW_ICON_FALLBACK = Object.freeze({
    icon: 'info',
    color: '#6366f1',
    label: 'Product'
});

export const ROW_COMMAND_DEFINITIONS = Object.freeze([
    Object.freeze({ id: 'inspect', icon: 'search', labelKey: 'inspect' }),
    Object.freeze({ id: 'copy_code', icon: 'copy', labelKey: 'copyCode', requiresCode: true }),
    Object.freeze({ id: 'copy_json', icon: 'braces', labelKey: 'copyJSON' }),
    Object.freeze({ id: 'toggle_selection', icon: 'check-square', labelKey: 'selectRow', selectedLabelKey: 'deselectRow' })
]);

const TABLE_MESSAGES = {
    en: {
        homeDocumentTitle: 'Patris Export - Home',
        homeHeroTitle: 'Paradox Database Converter & Viewer',
        homeHeroDescription: 'Real-time database visualization with live updates and WebSocket streaming',
        launchVisualizer: 'Launch Visualizer',
        featureDarkModeTitle: 'Dark Mode',
        featureDarkModeDescription: 'A refined theme that follows your system preference and remains comfortable in low light.',
        featureRealtimeTitle: 'Real-Time Updates',
        featureRealtimeDescription: 'Live WebSocket streaming delivers database changes as soon as they are available.',
        featureFilteringTitle: 'Advanced Filtering',
        featureFilteringDescription: 'Search and filter every record field with immediate, keyboard-friendly results.',
        featureResponsiveTitle: 'Responsive Design',
        featureResponsiveDescription: 'A focused experience for desktop, tablet, and mobile screens.',
        featureCharmapTitle: 'Character Map Debugger',
        featureCharmapDescription: 'Inspect Patris81 byte mappings and preview custom character-map text safely.',
        apiEndpoints: 'API Endpoints',
        recordsJSONDescription: 'Retrieve all database records in JSON format',
        recordsCSVDescription: 'Download all database records in CSV format',
        productSyncDescription: 'Retrieve the current canonical integration envelope when available',
        databaseInfoDescription: 'Get database schema information and metadata',
        websocketDescription: 'Connect for real-time bidirectional updates',
        tryIt: 'Try it',
        serverWatchStatus: 'The server watches the database file and broadcasts changes automatically.',
        capabilities: 'Patris Export capabilities',
        primaryNavigation: 'Primary navigation',
        switchLanguage: 'Interface language',
        english: 'English',
        persian: 'Persian',
        useLightTheme: 'Use light theme',
        useDarkTheme: 'Use dark theme',
        charmapDocumentTitle: 'Patris Export - Character Map',
        charmapPageTitle: 'Character Map Viewer',
        charmapSubtitle: 'Inspect Patris81 legacy bytes, mapped characters, and custom character-map previews.',
        home: 'Home',
        viewer: 'Viewer',
        mapSource: 'Map Source',
        entries: 'Entries',
        debugMode: 'Debug mode',
        loading: 'Loading',
        checking: 'Checking',
        disabled: 'Disabled',
        activeMap: 'Active map',
        defaultMap: 'Default map',
        customPreview: 'Custom Preview',
        uploadTextFile: 'Upload a .txt file',
        chooseTextFile: 'Choose file',
        noFileSelected: 'No file selected',
        pasteMapContent: 'Paste map content',
        serverFilePath: 'Server file path',
        previewContent: 'Preview content',
        previewPath: 'Preview path',
        embeddedMap: 'Embedded map',
        filterCharmap: 'Filter by hex, decimal, character, or codepoint',
        hex: 'Hex',
        decimal: 'Decimal',
        character: 'Character',
        codepoint: 'Codepoint',
        preview: 'Preview',
        loadingCharmap: 'Loading character map...',
        noCharmapMatches: 'No entries match the current filter.',
        zwnjMarker: 'zero-width non-joiner marker',
        charmapFallbackName: 'Character map',
        charmapIgnoredLines: '{count} ignored line(s): {sample}',
        charmapIssueLine: 'line {line}: {reason}',
        charmapIssueExpectedTab: 'expected a hex value and character separated by a tab',
        charmapIssueSingleByte: 'the hex value must decode to exactly one byte',
        charmapIssueUnknown: 'invalid character-map line',
        pasteMapFirst: 'Paste map content or upload a .txt file first.',
        enterServerPathFirst: 'Enter a server-side file path first.',
        charmapRequestFailed: 'The character-map request failed (HTTP {status}).',
        charmapFileReadFailed: 'The selected text file could not be read.',
        products: 'Products',
        categories: 'Categories',
        settings: 'Settings',
        goHome: 'Go to home',
        currentSourceFile: 'Current source file',
        connectionDetails: 'Connection details',
        toggleFooter: 'Toggle footer',
        hideFooter: 'Hide footer',
        toggleTheme: 'Toggle theme',
        catalogRecordType: 'Catalog record type',
        searchShortcut: 'Press F2 to focus search',
        exportData: 'Export data',
        exportFormats: 'Export formats',
        downloadCanonicalExcel: 'Download canonical Excel workbook',
        settingsSections: 'Settings sections',
        recordInspector: 'Record Inspector',
        export: 'Export',
        columns: 'Columns',
        refresh: 'Refresh now',
        logs: 'Logs',
        eventLogTitle: 'Event Logs',
        eventLogDescription: 'Notification events and expandable row-level changes received by this viewer.',
        eventLogClear: 'Clear',
        eventLogTotal: 'Total',
        eventLogUpdates: 'Updates',
        eventLogWarnings: 'Warnings',
        eventLogEmpty: 'No notification-capable events have been captured yet.',
        eventLogRowsChanged: 'Rows changed',
        eventLogChangeSummary: '{added} added, {modified} modified, {deleted} deleted',
        eventLogViewChanges: 'View change details ({count})',
        eventLogAdded: 'Added',
        eventLogModified: 'Modified',
        eventLogDeleted: 'Deleted',
        eventLogRow: 'Row',
        eventLogField: 'Field',
        eventLogBefore: 'Before',
        eventLogAfter: 'After',
        eventLogValue: 'Value',
        eventLogNoFields: 'No field values available',
        eventLogMoreFields: '{count} more fields',
        eventLogBoundedPreview: 'Bounded preview: {count} rows, fields, or long values were omitted.',
        eventLogTechnicalDetails: 'Technical details',
        eventLogDetailsExpired: 'Detailed row values were compacted; the original counts remain available.',
        eventLogTypeOther: '{label} event',
        eventLogSourceOther: '{label} source',
        eventTokenRowUpdated: 'Row update',
        eventTokenConfigUpdate: 'Configuration update',
        eventTokenWebUI: 'Web interface',
        eventTokenWebSocket: 'Live connection',
        eventTokenServer: 'Server',
        eventTokenNotification: 'Notification',
        eventTokenManualRefresh: 'Manual refresh',
        eventTokenResourceUpdate: 'Interface update',
        eventTokenDataSource: 'Data source',
        eventTokenTableSettings: 'Table settings',
        eventTokenRowAction: 'Row action',
        eventTokenEventLog: 'Event log',
        eventTokenConnection: 'Connection',
        eventTokenNotificationTest: 'Notification test',
        eventTokenExcelExport: 'Excel export',
        total: 'Total',
        filtered: 'Filtered',
        records: 'Records',
        connection: 'Connection',
        allFields: 'All fields',
        all: 'All',
        any: 'Any',
        filterPlaceholder: 'Filter...',
        group: 'Group',
        subgroup: 'Subgroup',
        item: 'Item',
        code: 'Code',
        searchPlaceholder: 'Search records...',
        noRecords: 'No records found',
        recordsLoadFailed: 'Records could not be loaded',
        recordsLoadFailedAction: 'Check the source file lock, temp directory permissions, and server logs, then refresh.',
        actions: 'Actions',
        rowActions: 'Actions for {code}',
        inspect: 'Inspect record',
        copyCode: 'Copy code',
        copyJSON: 'Copy row JSON',
        selectRow: 'Select row',
        deselectRow: 'Deselect row',
        selectAll: 'Select all filtered rows',
        selectRowLabel: 'Select row {code}',
        selectedCount: '{count} selected',
        clearFilters: 'Clear',
        clearAllFilters: 'Clear all filters',
        filterColumn: 'Filter {column}',
        filterColumnText: 'Filter {column} text',
        exactOrRangeFilter: '{column} exact or range filter',
        openRangeOptions: 'Open {column} range options',
        minimumColumn: 'Minimum {column}',
        maximumColumn: 'Maximum {column}',
        minimum: 'Minimum',
        maximum: 'Maximum',
        from: 'From',
        to: 'To',
        sampledRange: 'Sampled range: {minimum} to {maximum}',
        sampledValue: 'All sampled values are {value}',
        exactRangeHelp: 'Type an exact value, a range such as {minimum}-{maximum}, >=value, or <=value',
        jalaliDateFormat: 'Jalali date, YY.MM.DD',
        jalaliDateFrom: 'From {column} Jalali date',
        jalaliDateTo: 'To {column} Jalali date',
        jalaliDatePicker: 'Open {column} Jalali date picker',
        noJalaliDates: 'No valid Jalali dates detected',
        invalidJalaliDate: 'Use YY.MM.DD',
        filterCodeType: 'Filter Code type',
        filterCodeSegments: 'Filter Code group segments',
        resizeColumn: 'Resize {column} column',
        resizeHelp: 'Use Left and Right Arrow keys to resize. Press Home or double-click to reset.',
        resetColumnWidths: 'Reset widths',
        resetColumnWidth: 'Reset column width',
        widthsReset: 'Column widths reset',
        warehouseStock: 'Warehouse Stock',
        warehouseVisibility: 'Warehouse visibility',
        warehouseColumn: 'Warehouse {name}',
        columnVisibility: 'Column Visibility',
        columnVisibilityDescription: 'Show, label, and reorder columns. Each warehouse remains an independent stock column.',
        visible: 'Visible',
        sourceKey: 'Source key',
        displayLabel: 'Display label',
        type: 'Type',
        showAll: 'Show all',
        hideAll: 'Hide all',
        alwaysVisible: 'Always visible',
        sortAscending: 'Sort ascending',
        sortDescending: 'Sort descending',
        hideColumn: 'Hide column',
        manageColumns: 'Manage columns',
        headerActions: 'Column actions for {column}',
        moreActions: 'More actions',
        openSettings: 'Open settings',
        openColumns: 'Open columns',
        openConnection: 'Open connection status',
        openEventLog: 'Open event log',
        openSQLTarget: 'Open SQL target operations',
        sqlTarget: 'SQL Target',
        sqlTargetTitle: 'SQL Target Operations',
        sqlTargetDescription: 'Test and run the protected MySQL or MariaDB export without revealing connection material.',
        sqlTargetUnlockTitle: 'Unlock operator session',
        sqlTargetUnlockDescription: 'Direct loopback access may use HTTP. A configured operator credential is required even locally; remote access also requires HTTPS.',
        sqlTargetOperatorCredential: 'Operator credential (when configured)',
        sqlTargetCredentialHelp: 'Leave this empty only for direct loopback when the server has no configured operator credential. It is sent once, never displayed, and never stored by the browser.',
        sqlTargetUnlock: 'Unlock session',
        sqlTargetLock: 'Lock',
        sqlTargetSessionLocked: 'Locked',
        sqlTargetSessionUnlocked: 'Operator session',
        sqlTargetProtectedConfiguration: 'Protected configuration',
        sqlTargetOverview: 'Target overview',
        sqlTargetRefresh: 'Refresh status',
        sqlTargetConfiguredLabel: 'Configured',
        sqlTargetDriver: 'Driver',
        sqlTargetTable: 'Table mapping',
        sqlTargetReconciliation: 'Reconciliation',
        sqlTargetBatchSize: 'Batch size',
        sqlTargetTimeout: 'Connection timeout',
        sqlTargetTLSSetup: 'Verified TLS setup',
        sqlTargetWorker: 'Worker',
        sqlTargetNonMutating: 'Non-mutating diagnostic',
        sqlTargetConnection: 'Connection assurance',
        sqlTargetTest: 'Test connection',
        sqlTargetConnected: 'Connected',
        sqlTargetVendor: 'Vendor',
        sqlTargetTLS: 'Transport security',
        sqlTargetProbeElapsed: 'Probe elapsed',
        sqlTargetBoundedOperations: 'Bounded operations',
        sqlTargetActions: 'Preview and synchronize',
        sqlTargetPreviewTitle: 'Dry-run preview',
        sqlTargetPreviewDescription: 'Calculate inserts, updates, unchanged rows, and reconciliation effects without changing the target, then issue a two-minute one-time apply grant.',
        sqlTargetPreview: 'Run preview',
        sqlTargetSyncTitle: 'Confirmed manual sync',
        sqlTargetSyncDescription: 'Apply the exact previewed canonical records once using the configured bounded transaction and reconciliation policy.',
        sqlTargetSyncConfirm: 'I reviewed the preview and confirm this manual synchronization.',
        sqlTargetSync: 'Synchronize now',
        sqlTargetPreviewRequired: 'Fresh preview required',
        sqlTargetPreviewReady: 'One-time grant active',
        sqlTargetPreviewUnsupported: 'Apply unavailable',
        sqlTargetDiagnostics: 'Secret-safe diagnostics',
        sqlTargetLastResult: 'Last operation',
        sqlTargetNoResult: 'No SQL target operation has been recorded in this server process.',
        sqlTargetOperation: 'Operation',
        sqlTargetOperationStatus: 'Status',
        sqlTargetRecordCount: 'Records',
        sqlTargetElapsed: 'Elapsed',
        sqlTargetInserted: 'Inserted',
        sqlTargetUpdated: 'Updated',
        sqlTargetUnchanged: 'Unchanged',
        sqlTargetDeleted: 'Deleted',
        sqlTargetFailed: 'Failed',
        sqlTargetPlanEvidence: 'Reconciliation evidence',
        sqlTargetPlanSafety: 'Soft-delete plan safety',
        sqlTargetEvidenceSource: 'Source rows',
        sqlTargetEvidenceProtected: 'Protected rows',
        sqlTargetEvidenceTarget: 'Target rows',
        sqlTargetEvidenceMissing: 'Missing rows',
        sqlTargetEvidenceWouldDelete: 'Would soft-delete',
        sqlTargetEvidenceAlreadyDeleted: 'Already soft-deleted',
        sqlTargetEvidenceWouldRestore: 'Would restore',
        sqlTargetEvidencePartialRisk: 'Partial-source risk',
        sqlTargetEvidenceApplyAllowed: 'Apply allowed',
        sqlTargetEvidenceConfirmation: 'Exact preview token',
        sqlTargetFailureTitle: 'Operation did not complete',
        sqlTargetFailureCode: 'Code',
        sqlTargetFailureStage: 'Stage',
        sqlTargetFailureReason: 'Reason',
        sqlTargetFailureRetryable: 'Retryable',
        sqlTargetConfigured: 'Target configured',
        sqlTargetNotConfigured: 'Target not configured',
        sqlTargetUnlockHelp: 'Unlock an operator session to inspect this target.',
        sqlTargetSessionExpired: 'The operator session expired. Unlock a new session to continue.',
        sqlTargetUnlockRejected: 'Operator authorization was rejected.',
        sqlTargetRequestFailed: 'The protected request could not be completed.',
        sqlTargetUnlocking: 'Creating a short-lived operator session…',
        sqlTargetLocking: 'Revoking the operator session…',
        sqlTargetLockedNotice: 'The operator session was revoked and the panel is locked.',
        sqlTargetLockFailed: 'Local authorization was cleared, but the server session could not be revoked. It will expire automatically.',
        sqlTargetSessionReady: 'Operator session established. Loading the protected target status…',
        sqlTargetRefreshing: 'Refreshing protected target status…',
        sqlTargetReadyNotice: 'The target is configured. Test the connection, then run a dry preview before synchronizing.',
        sqlTargetNotConfiguredNotice: 'The server has no complete SQL target configuration. Operations remain disabled.',
        sqlTargetTableRequiredNotice: 'The DSN is configured, but an explicit valid MySQL target table is required before preview or sync.',
        sqlTargetSyncConfirmationRequired: 'Review the preview and select the confirmation before synchronizing.',
        sqlTargetDeleteMissingBlocked: 'Browser apply is blocked for hard-delete reconciliation because no exact-plan token is available.',
        sqlTargetPreviewNotAuthorized: 'The preview is not safe to apply. Review the reconciliation evidence and guard state.',
        sqlTargetRunning_test: 'Running a bounded, non-mutating connection test…',
        sqlTargetRunning_preview: 'Calculating a non-mutating synchronization preview…',
        sqlTargetRunning_sync: 'Running the confirmed bounded synchronization…',
        sqlTargetCompleted_test: 'Connection test completed.',
        sqlTargetCompleted_preview: 'Dry-run preview completed. Review every count before confirming a sync.',
        sqlTargetCompleted_sync: 'Manual synchronization completed.',
        sqlTargetOperationFailed: 'The operation did not complete. Secret-safe diagnostics are shown below.',
        sqlTargetError_invalid_credential: 'The operator credential is not valid.',
        sqlTargetError_invalid_response: 'The server returned an invalid protected response.',
        sqlTargetError_operation_timeout: 'The operation exceeded its bounded deadline.',
        sqlTargetError_preview_required: 'Run a fresh preview before synchronizing.',
        sqlTargetError_preview_confirmation_required: 'The current soft-delete preview did not provide a valid exact-plan token.',
        sqlTargetError_invalid_preview_grant: 'The one-time preview grant was invalid. Run a fresh preview.',
        sqlTargetError_preview_unavailable: 'The server could not issue a one-time preview grant.',
        sqlTargetError_target_table_required: 'Configure an explicit MySQL target table before previewing or synchronizing.',
        sqlTargetError_target_table_invalid: 'The configured MySQL target table is not a valid normalized SQL identifier.',
        sqlTargetError_unsupported_reconciliation: 'This reconciliation policy cannot be applied from the browser.',
        sqlTargetReconciliation_upsert_only: 'Upsert only',
        sqlTargetReconciliation_soft_delete_missing: 'Soft-delete missing',
        sqlTargetReconciliation_delete_missing: 'Delete missing',
        sqlTargetTLS_unknown: 'Unknown',
        sqlTargetTLS_encrypted: 'Encrypted',
        sqlTargetTLS_plaintext: 'Plaintext',
        sqlTargetOperation_test: 'Connection test',
        sqlTargetOperation_preview: 'Dry-run preview',
        sqlTargetOperation_sync: 'Manual sync',
        sqlTargetOperationStatus_succeeded: 'Succeeded',
        sqlTargetOperationStatus_failed: 'Failed',
        sqlTargetGuard_clear: 'Plan ready',
        sqlTargetGuard_empty_source: 'Empty source blocked',
        sqlTargetGuard_preview_required: 'Preview required',
        sqlTargetGuard_preview_mismatch: 'Preview changed',
        sqlTargetGuard_reserved_field_collision: 'Reserved field conflict',
        sqlTargetGuard_invalid_tombstone_state: 'Invalid tombstone state',
        sqlTargetGuard_safety_guard: 'Safety guard',
        yes: 'Yes',
        no: 'No',
        ready: 'Ready',
        busy: 'Busy',
        refreshSource: 'Refresh source',
        copyStatus: 'Copy connection status',
        copyEventLog: 'Copy event log JSON',
        statusCopied: 'Connection status copied to the clipboard.',
        eventLogCopied: 'Event log copied to the clipboard.',
        freezeFirstColumn: 'Freeze the first data column',
        copied: 'Copied',
        codeCopied: 'Product code copied to the clipboard.',
        jsonCopied: 'Row JSON copied to the clipboard.',
        copyFailed: 'Clipboard access failed',
        error: 'Error',
        couldNotLoadPage: 'Could not load page.',
        settingsSaveFailed: 'Settings save failed',
        loadingSource: 'Loading source...',
        uploadingSource: 'Uploading the file and switching connected viewers.',
        unsupportedFile: 'Unsupported file',
        unsupportedFileHelp: 'Drop a .db or .json file to switch the active source.',
        sourceLoaded: 'Source loaded',
        sourceLoadedMessage: '{file} is now active ({count} records).',
        sourceSwitchFailed: 'Source switch failed',
        updatingUI: 'Updating UI...',
        updatingInterface: 'Updating interface',
        updatingInterfaceMessage: 'A newer embedded web UI is available. Reloading now.',
        nativeToastUnavailable: 'Native toast unavailable',
        toastRequestFailed: 'Toast request failed',
        refreshing: 'Refreshing...',
        refreshRequested: 'Refresh requested',
        refreshRequestedMessage: 'The backend is reloading the data source.',
        refreshed: 'Refreshed',
        refreshedMessage: 'Data was reloaded over HTTP.',
        refreshFailed: 'Refresh failed',
        settingsReloaded: 'Settings reloaded',
        settingsReloadedMessage: 'Configuration file changes were applied.',
        settingsChangeSingle: 'Setting {path} changed: {before} → {after}',
        settingsChangeMultiple: '{count} settings changed: {names}{suffix}',
        excelExportStarted: 'Excel export started',
        excelExportStartedMessage: 'The canonical workbook includes records and non-secret provenance metadata.',
        soundTest: 'Sound test',
        soundTestMessage: 'Notification audio was triggered.',
        patrisRunning: 'Patris81 running ({count})',
        patrisNotRunning: 'Patris81 not running',
        databaseLockedCount: 'DB locked ({count})',
        databaseUnlocked: 'DB unlocked',
        invalidNumberRange: 'Use a number, min-max, >=min, or <=max',
        language: 'Language',
        file: 'File',
        checkingPatris: 'Checking Patris81...',
        connecting: 'Connecting...',
        connected: 'Connected',
        disconnected: 'Disconnected',
        open: 'Open',
        closing: 'Closing',
        closed: 'Closed',
        unknown: 'Unknown',
        exportJSON: 'Export as JSON',
        exportJSONDescription: 'JavaScript Object Notation',
        exportCSV: 'Export as CSV',
        exportCSVDescription: 'Comma-Separated Values',
        exportExcel: 'Download Excel workbook',
        exportExcelDescription: 'Canonical XLSX with provenance',
        loadingData: 'Loading data...',
        dropDatabase: 'Drop database file',
        dropDatabaseDescription: 'Release a .db or .json file to load it in this viewer.',
        interface: 'Interface',
        server: 'Server',
        database: 'Database',
        notifications: 'Notifications',
        runtime: 'Runtime',
        theme: 'Theme',
        system: 'System',
        light: 'Light',
        dark: 'Dark',
        recordsPerPage: 'Records per page',
        notificationSound: 'Notification sound',
        externalFile: 'External file',
        generatedMelody: 'Generated melody',
        lastUpdate: 'Last update',
        absoluteRelative: 'Absolute and relative',
        absolute: 'Absolute',
        relative: 'Relative',
        showFooter: 'Show footer',
        autoScrollChanged: 'Auto-scroll to changed items',
        highlightChanged: 'Highlight changed items',
        rtlTableText: 'Display table text right-to-left',
        enablePagination: 'Enable pagination',
        playUpdateSound: 'Play notification sound on updates',
        groupRows: 'Group rows',
        subgroupRows: 'Subgroup rows',
        noStockText: 'No-stock text',
        stockAccent: 'Stock accent',
        testSound: 'Test sound',
        testToast: 'Test toast',
        host: 'Host',
        port: 'Port',
        debounce: 'Debounce',
        watchSource: 'Watch source and broadcast updates',
        databasePath: 'Database path or URL',
        customCharmap: 'Custom character map',
        directAccess: 'Read database directly without a temporary copy',
        rtlConversion: 'Enable opt-in RTL conversion',
        maxDetailedRows: 'Maximum detailed rows',
        enableNotifications: 'Enable event notifications',
        nativeToasts: 'Show native OS toast messages',
        relayNotifications: 'Relay notifications to connected web clients',
        clientConnected: 'Client connected',
        clientDisconnected: 'Client disconnected',
        sourceFileUpdated: 'Source file updated with old/new hash',
        rowsChanged: 'Rows changed',
        includeRowValues: 'Include old/new row values in notifications',
        tempDirectory: 'Temporary directory',
        tempStrategy: 'Temporary-file strategy',
        automatic: 'Automatic',
        systemTemp: 'System temporary directory',
        memoryTemp: 'Memory temporary directory',
        memoryTempLimit: 'Memory temporary limit (MiB)',
        debugTools: 'Enable debug tools and custom character-map previews',
        runtimeHelp: 'Use system for the operating-system temporary directory. Automatic mode prefers shared memory on Linux for known-size files within the limit.',
        connectionStatus: 'Connection Status',
        connectionDescription: 'Live WebSocket, source, and process status.',
        status: 'Status',
        webSocket: 'WebSocket',
        source: 'Source',
        databaseLock: 'Database lock',
        running: 'Running',
        notRunning: 'Not running',
        locked: 'Locked',
        unlocked: 'Unlocked',
        notStarted: 'Not started',
        selectRecord: 'Select a record to inspect',
        close: 'Close',
        rowIcons: 'Conditional row icons',
        rowIconsHelp: 'The first enabled rule that matches a transformed canonical key wins.',
        enableRowIcons: 'Show conditional row icons',
        addRule: 'Add rule',
        fallback: 'Fallback',
        field: 'Canonical key',
        selectCanonicalField: 'Select a canonical key',
        operator: 'Operator',
        value: 'Value',
        icon: 'Icon',
        color: 'Color',
        label: 'Accessible label',
        enabled: 'Enabled',
        moveUp: 'Move rule up',
        moveDown: 'Move rule down',
        removeRule: 'Remove rule',
        rule: 'Rule {number}',
        rowColoring: 'Row coloring',
        enableRowColoring: 'Enable row coloring rules',
        icon_warning: 'Warning',
        icon_clock: 'Clock',
        icon_price: 'Price',
        icon_weight: 'Weight',
        icon_stock: 'Stock',
        icon_package: 'Package',
        icon_check: 'Check',
        icon_info: 'Info',
        ruleLabel_warnings: 'Warnings',
        ruleLabel_stale: 'Stale source data',
        ruleLabel_price_missing: 'Price unavailable',
        ruleLabel_weight_missing: 'Weight unavailable',
        ruleLabel_out_of_stock: 'Out of stock',
        ruleLabel_in_stock: 'In stock',
        fallbackProduct: 'Product',
        op_equals: 'Equals',
        op_not_equals: 'Does not equal',
        op_contains: 'Contains',
        op_not_contains: 'Does not contain',
        op_empty: 'Is empty',
        op_not_empty: 'Is not empty',
        op_gt: 'Greater than',
        op_gte: 'Greater than or equal',
        op_lt: 'Less than',
        op_lte: 'Less than or equal',
        op_truthy: 'Is truthy',
        op_falsy: 'Is falsey',
        op_stale_days: 'Older than days'
    },
    fa: {
        homeDocumentTitle: 'Patris Export - صفحه اصلی',
        homeHeroTitle: 'مبدل و نمایشگر پایگاه‌داده Paradox',
        homeHeroDescription: 'نمایش زنده پایگاه‌داده با به‌روزرسانی آنی و جریان WebSocket',
        launchVisualizer: 'بازکردن نمایشگر',
        featureDarkModeTitle: 'حالت تیره',
        featureDarkModeDescription: 'پوسته‌ای حرفه‌ای که با ترجیح سیستم هماهنگ می‌شود و در نور کم خوانا می‌ماند.',
        featureRealtimeTitle: 'به‌روزرسانی آنی',
        featureRealtimeDescription: 'جریان زنده WebSocket تغییرات پایگاه‌داده را بلافاصله پس از آماده‌شدن نمایش می‌دهد.',
        featureFilteringTitle: 'فیلتر پیشرفته',
        featureFilteringDescription: 'در همه فیلدهای رکورد با نتیجه فوری و پشتیبانی کامل از صفحه‌کلید جست‌وجو و فیلتر کنید.',
        featureResponsiveTitle: 'طراحی واکنش‌گرا',
        featureResponsiveDescription: 'تجربه‌ای متمرکز و خوانا برای رایانه، تبلت و تلفن همراه.',
        featureCharmapTitle: 'اشکال‌زدای نگاشت نویسه',
        featureCharmapDescription: 'نگاشت بایت‌های Patris81 را بررسی و متن نگاشت سفارشی را به‌صورت امن پیش‌نمایش کنید.',
        apiEndpoints: 'نقاط پایانی API',
        recordsJSONDescription: 'دریافت همه رکوردهای پایگاه‌داده با قالب JSON',
        recordsCSVDescription: 'دریافت همه رکوردهای پایگاه‌داده با قالب CSV',
        productSyncDescription: 'دریافت بسته یکپارچه‌سازی استاندارد جاری در صورت موجودبودن',
        databaseInfoDescription: 'دریافت ساختار پایگاه‌داده و فراداده‌ها',
        websocketDescription: 'اتصال برای دریافت و ارسال به‌روزرسانی‌های آنی',
        tryIt: 'آزمایش',
        serverWatchStatus: 'سرور فایل پایگاه‌داده را پایش می‌کند و تغییرات را به‌صورت خودکار می‌فرستد.',
        capabilities: 'قابلیت‌های Patris Export',
        primaryNavigation: 'پیمایش اصلی',
        switchLanguage: 'زبان رابط',
        english: 'انگلیسی',
        persian: 'فارسی',
        useLightTheme: 'استفاده از پوسته روشن',
        useDarkTheme: 'استفاده از پوسته تیره',
        charmapDocumentTitle: 'Patris Export - نگاشت نویسه',
        charmapPageTitle: 'نمایشگر نگاشت نویسه',
        charmapSubtitle: 'بایت‌های قدیمی Patris81، نویسه‌های نگاشت‌شده و پیش‌نمایش نگاشت سفارشی را بررسی کنید.',
        home: 'صفحه اصلی',
        viewer: 'نمایشگر',
        mapSource: 'منبع نگاشت',
        entries: 'مدخل‌ها',
        debugMode: 'حالت اشکال‌زدایی',
        loading: 'در حال بارگیری',
        checking: 'در حال بررسی',
        disabled: 'غیرفعال',
        activeMap: 'نگاشت فعال',
        defaultMap: 'نگاشت پیش‌فرض',
        customPreview: 'پیش‌نمایش سفارشی',
        uploadTextFile: 'بارگذاری فایل .txt',
        chooseTextFile: 'انتخاب فایل',
        noFileSelected: 'فایلی انتخاب نشده است',
        pasteMapContent: 'چسباندن محتوای نگاشت',
        serverFilePath: 'مسیر فایل روی سرور',
        previewContent: 'پیش‌نمایش محتوا',
        previewPath: 'پیش‌نمایش مسیر',
        embeddedMap: 'نگاشت داخلی',
        filterCharmap: 'فیلتر بر اساس هگز، عدد ده‌دهی، نویسه یا نقطه‌کد',
        hex: 'هگز',
        decimal: 'ده‌دهی',
        character: 'نویسه',
        codepoint: 'نقطه‌کد',
        preview: 'پیش‌نمایش',
        loadingCharmap: 'در حال بارگیری نگاشت نویسه...',
        noCharmapMatches: 'هیچ مدخلی با فیلتر فعلی مطابقت ندارد.',
        zwnjMarker: 'نشانگر نیم‌فاصله',
        charmapFallbackName: 'نگاشت نویسه',
        charmapIgnoredLines: '{count} خط نادیده گرفته شد: {sample}',
        charmapIssueLine: 'خط {line}: {reason}',
        charmapIssueExpectedTab: 'مقدار هگز و نویسه باید با یک نویسه تب جدا شوند',
        charmapIssueSingleByte: 'مقدار هگز باید دقیقاً به یک بایت تبدیل شود',
        charmapIssueUnknown: 'خط نگاشت نویسه معتبر نیست',
        pasteMapFirst: 'ابتدا محتوای نگاشت را بچسبانید یا یک فایل .txt بارگذاری کنید.',
        enterServerPathFirst: 'ابتدا مسیر فایل روی سرور را وارد کنید.',
        charmapRequestFailed: 'درخواست نگاشت نویسه ناموفق بود (HTTP {status}).',
        charmapFileReadFailed: 'فایل متنی انتخاب‌شده خوانده نشد.',
        products: 'محصولات',
        categories: 'دسته‌بندی‌ها',
        settings: 'تنظیمات',
        goHome: 'رفتن به صفحه اصلی',
        currentSourceFile: 'فایل منبع فعلی',
        connectionDetails: 'جزئیات اتصال',
        toggleFooter: 'نمایش یا پنهان‌کردن نوار پایین',
        hideFooter: 'پنهان‌کردن نوار پایین',
        toggleTheme: 'تغییر پوسته',
        catalogRecordType: 'نوع رکورد فهرست',
        searchShortcut: 'برای تمرکز روی جست‌وجو F2 را بزنید',
        exportData: 'خروجی گرفتن از داده‌ها',
        exportFormats: 'قالب‌های خروجی',
        downloadCanonicalExcel: 'دریافت فایل اکسل استاندارد',
        settingsSections: 'بخش‌های تنظیمات',
        recordInspector: 'بررسی رکورد',
        export: 'خروجی',
        columns: 'ستون‌ها',
        refresh: 'به‌روزرسانی',
        logs: 'رویدادها',
        eventLogTitle: 'گزارش رویدادها',
        eventLogDescription: 'رویدادهای اعلان و تغییرات ردیف‌ها را باز کنید و جزئیات آن‌ها را ببینید.',
        eventLogClear: 'پاک‌کردن',
        eventLogTotal: 'کل',
        eventLogUpdates: 'به‌روزرسانی‌ها',
        eventLogWarnings: 'هشدارها',
        eventLogEmpty: 'هنوز رویدادی که قابلیت اعلان داشته باشد ثبت نشده است.',
        eventLogRowsChanged: 'ردیف‌ها تغییر کردند',
        eventLogChangeSummary: '{added} افزوده، {modified} تغییریافته، {deleted} حذف‌شده',
        eventLogViewChanges: 'مشاهده جزئیات تغییرات ({count})',
        eventLogAdded: 'افزوده‌شده',
        eventLogModified: 'تغییریافته',
        eventLogDeleted: 'حذف‌شده',
        eventLogRow: 'ردیف',
        eventLogField: 'فیلد',
        eventLogBefore: 'قبل',
        eventLogAfter: 'بعد',
        eventLogValue: 'مقدار',
        eventLogNoFields: 'مقدار فیلدی در دسترس نیست',
        eventLogMoreFields: '{count} فیلد دیگر',
        eventLogBoundedPreview: 'برای حفظ کارایی، {count} ردیف، فیلد یا مقدار طولانی دیگر نمایش داده نشده است.',
        eventLogTechnicalDetails: 'جزئیات فنی',
        eventLogDetailsExpired: 'جزئیات ردیف‌ها فشرده شده‌اند؛ شمارش اصلی همچنان در دسترس است.',
        eventLogTypeOther: 'رویداد {label}',
        eventLogSourceOther: 'منبع {label}',
        eventTokenRowUpdated: 'به‌روزرسانی ردیف‌ها',
        eventTokenConfigUpdate: 'به‌روزرسانی تنظیمات',
        eventTokenWebUI: 'رابط وب',
        eventTokenWebSocket: 'اتصال زنده',
        eventTokenServer: 'سرور',
        eventTokenNotification: 'اعلان',
        eventTokenManualRefresh: 'به‌روزرسانی دستی',
        eventTokenResourceUpdate: 'به‌روزرسانی رابط',
        eventTokenDataSource: 'منبع داده',
        eventTokenTableSettings: 'تنظیمات جدول',
        eventTokenRowAction: 'عملیات ردیف',
        eventTokenEventLog: 'گزارش رویدادها',
        eventTokenConnection: 'اتصال',
        eventTokenNotificationTest: 'آزمون اعلان',
        eventTokenExcelExport: 'خروجی اکسل',
        total: 'کل',
        filtered: 'فیلترشده',
        records: 'رکوردها',
        connection: 'اتصال',
        allFields: 'همه فیلدها',
        all: 'همه',
        any: 'هر مقدار',
        filterPlaceholder: 'فیلتر...',
        group: 'گروه',
        subgroup: 'زیرگروه',
        item: 'کالا',
        code: 'کد',
        searchPlaceholder: 'جست‌وجوی رکوردها...',
        noRecords: 'رکوردی پیدا نشد',
        recordsLoadFailed: 'بارگیری رکوردها ناموفق بود',
        recordsLoadFailedAction: 'قفل فایل منبع، مجوزهای پوشه موقت و گزارش‌های سرور را بررسی کنید، سپس دوباره بارگیری کنید.',
        actions: 'عملیات',
        rowActions: 'عملیات ردیف {code}',
        inspect: 'بررسی رکورد',
        copyCode: 'کپی کد',
        copyJSON: 'کپی JSON ردیف',
        selectRow: 'انتخاب ردیف',
        deselectRow: 'لغو انتخاب ردیف',
        selectAll: 'انتخاب همه ردیف‌های فیلترشده',
        selectRowLabel: 'انتخاب ردیف {code}',
        selectedCount: '{count} انتخاب‌شده',
        clearFilters: 'پاک‌کردن',
        clearAllFilters: 'پاک‌کردن همه فیلترها',
        filterColumn: 'فیلتر ستون {column}',
        filterColumnText: 'فیلتر متنی ستون {column}',
        exactOrRangeFilter: 'فیلتر دقیق یا بازه‌ای ستون {column}',
        openRangeOptions: 'بازکردن گزینه‌های بازه ستون {column}',
        minimumColumn: 'کمینه {column}',
        maximumColumn: 'بیشینه {column}',
        minimum: 'کمینه',
        maximum: 'بیشینه',
        from: 'از',
        to: 'تا',
        sampledRange: 'بازه نمونه: {minimum} تا {maximum}',
        sampledValue: 'همه مقادیر نمونه {value} هستند',
        exactRangeHelp: 'یک مقدار دقیق، بازه‌ای مانند {minimum}-{maximum}، >=مقدار یا <=مقدار وارد کنید',
        jalaliDateFormat: 'تاریخ شمسی با قالب YY.MM.DD',
        jalaliDateFrom: 'تاریخ شمسی شروع ستون {column}',
        jalaliDateTo: 'تاریخ شمسی پایان ستون {column}',
        jalaliDatePicker: 'بازکردن انتخابگر تاریخ شمسی ستون {column}',
        noJalaliDates: 'تاریخ شمسی معتبری پیدا نشد',
        invalidJalaliDate: 'از قالب YY.MM.DD استفاده کنید',
        filterCodeType: 'فیلتر نوع کد',
        filterCodeSegments: 'فیلتر بخش‌های گروه کد',
        resizeColumn: 'تغییر اندازه ستون {column}',
        resizeHelp: 'برای تغییر اندازه از کلیدهای جهت استفاده کنید. برای بازنشانی Home را بزنید یا دوبار کلیک کنید.',
        resetColumnWidths: 'بازنشانی عرض‌ها',
        resetColumnWidth: 'بازنشانی عرض ستون',
        widthsReset: 'عرض ستون‌ها بازنشانی شد',
        warehouseStock: 'موجودی انبارها',
        warehouseVisibility: 'نمایش انبارها',
        warehouseColumn: 'انبار {name}',
        columnVisibility: 'نمایش ستون‌ها',
        columnVisibilityDescription: 'ستون‌ها را نمایش دهید، نام‌گذاری و مرتب کنید. موجودی هر انبار در ستون مستقل می‌ماند.',
        visible: 'نمایش',
        sourceKey: 'کلید منبع',
        displayLabel: 'عنوان نمایشی',
        type: 'نوع',
        showAll: 'نمایش همه',
        hideAll: 'پنهان‌کردن همه',
        alwaysVisible: 'همیشه نمایش داده می‌شود',
        sortAscending: 'مرتب‌سازی صعودی',
        sortDescending: 'مرتب‌سازی نزولی',
        hideColumn: 'پنهان‌کردن ستون',
        manageColumns: 'مدیریت ستون‌ها',
        headerActions: 'عملیات ستون {column}',
        moreActions: 'عملیات بیشتر',
        openSettings: 'بازکردن تنظیمات',
        openColumns: 'بازکردن ستون‌ها',
        openConnection: 'نمایش وضعیت اتصال',
        openEventLog: 'بازکردن گزارش رویدادها',
        openSQLTarget: 'بازکردن عملیات مقصد SQL',
        sqlTarget: 'مقصد SQL',
        sqlTargetTitle: 'عملیات مقصد SQL',
        sqlTargetDescription: 'خروجی محافظت‌شده MySQL یا MariaDB را بدون نمایش اطلاعات اتصال آزمایش و اجرا کنید.',
        sqlTargetUnlockTitle: 'بازکردن نشست اپراتور',
        sqlTargetUnlockDescription: 'دسترسی مستقیم لوپ‌بک می‌تواند از HTTP استفاده کند. اگر سرور اعتبارنامه اپراتور را پیکربندی کرده باشد، این اعتبارنامه در دسترسی محلی نیز الزامی است؛ دسترسی راه دور نیز به HTTPS نیاز دارد.',
        sqlTargetOperatorCredential: 'اعتبارنامه اپراتور (هنگام پیکربندی)',
        sqlTargetCredentialHelp: 'این فیلد را فقط برای دسترسی مستقیم لوپ‌بک و زمانی خالی بگذارید که سرور اعتبارنامه اپراتور پیکربندی نکرده باشد. مقدار فقط یک‌بار ارسال می‌شود، نمایش داده نمی‌شود و در مرورگر ذخیره نخواهد شد.',
        sqlTargetUnlock: 'بازکردن نشست',
        sqlTargetLock: 'قفل‌کردن',
        sqlTargetSessionLocked: 'قفل',
        sqlTargetSessionUnlocked: 'نشست اپراتور',
        sqlTargetProtectedConfiguration: 'پیکربندی محافظت‌شده',
        sqlTargetOverview: 'نمای کلی مقصد',
        sqlTargetRefresh: 'به‌روزرسانی وضعیت',
        sqlTargetConfiguredLabel: 'پیکربندی‌شده',
        sqlTargetDriver: 'راه‌انداز',
        sqlTargetTable: 'نگاشت جدول',
        sqlTargetReconciliation: 'همگام‌سازی رکوردهای مفقود',
        sqlTargetBatchSize: 'اندازه دسته',
        sqlTargetTimeout: 'مهلت اتصال',
        sqlTargetTLSSetup: 'تنظیم TLS تأییدشده',
        sqlTargetWorker: 'پردازشگر',
        sqlTargetNonMutating: 'آزمایش بدون تغییر داده',
        sqlTargetConnection: 'اطمینان از اتصال',
        sqlTargetTest: 'آزمایش اتصال',
        sqlTargetConnected: 'متصل',
        sqlTargetVendor: 'سازنده',
        sqlTargetTLS: 'امنیت انتقال',
        sqlTargetProbeElapsed: 'زمان آزمایش',
        sqlTargetBoundedOperations: 'عملیات محدود و کنترل‌شده',
        sqlTargetActions: 'پیش‌نمایش و همگام‌سازی',
        sqlTargetPreviewTitle: 'پیش‌نمایش بدون تغییر',
        sqlTargetPreviewDescription: 'تعداد درج، به‌روزرسانی، بدون‌تغییر و اثر سیاست رکوردهای مفقود را بدون تغییر مقصد محاسبه و سپس مجوز یک‌بارمصرف دو دقیقه‌ای برای اعمال صادر کنید.',
        sqlTargetPreview: 'اجرای پیش‌نمایش',
        sqlTargetSyncTitle: 'همگام‌سازی دستی تأییدشده',
        sqlTargetSyncDescription: 'رکوردهای استاندارد دقیقاً پیش‌نمایش‌شده را فقط یک بار با تراکنش دسته‌ای و سیاست پیکربندی‌شده اعمال کنید.',
        sqlTargetSyncConfirm: 'پیش‌نمایش را بررسی کرده‌ام و این همگام‌سازی دستی را تأیید می‌کنم.',
        sqlTargetSync: 'همگام‌سازی اکنون',
        sqlTargetPreviewRequired: 'پیش‌نمایش تازه لازم است',
        sqlTargetPreviewReady: 'مجوز یک‌بارمصرف فعال',
        sqlTargetPreviewUnsupported: 'اعمال در دسترس نیست',
        sqlTargetDiagnostics: 'عیب‌یابی بدون اطلاعات محرمانه',
        sqlTargetLastResult: 'آخرین عملیات',
        sqlTargetNoResult: 'در این فرایند سرور هنوز عملیاتی برای مقصد SQL ثبت نشده است.',
        sqlTargetOperation: 'عملیات',
        sqlTargetOperationStatus: 'وضعیت',
        sqlTargetRecordCount: 'رکوردها',
        sqlTargetElapsed: 'زمان اجرا',
        sqlTargetInserted: 'درج‌شده',
        sqlTargetUpdated: 'به‌روزشده',
        sqlTargetUnchanged: 'بدون تغییر',
        sqlTargetDeleted: 'حذف‌شده',
        sqlTargetFailed: 'ناموفق',
        sqlTargetPlanEvidence: 'شواهد همگام‌سازی',
        sqlTargetPlanSafety: 'ایمنی برنامه حذف نرم',
        sqlTargetEvidenceSource: 'رکوردهای منبع',
        sqlTargetEvidenceProtected: 'رکوردهای محافظت‌شده',
        sqlTargetEvidenceTarget: 'رکوردهای مقصد',
        sqlTargetEvidenceMissing: 'رکوردهای مفقود',
        sqlTargetEvidenceWouldDelete: 'حذف نرم پیش‌بینی‌شده',
        sqlTargetEvidenceAlreadyDeleted: 'از قبل حذف نرم شده',
        sqlTargetEvidenceWouldRestore: 'بازیابی پیش‌بینی‌شده',
        sqlTargetEvidencePartialRisk: 'خطر منبع ناقص',
        sqlTargetEvidenceApplyAllowed: 'اجازه اعمال',
        sqlTargetEvidenceConfirmation: 'توکن دقیق پیش‌نمایش',
        sqlTargetFailureTitle: 'عملیات تکمیل نشد',
        sqlTargetFailureCode: 'کد',
        sqlTargetFailureStage: 'مرحله',
        sqlTargetFailureReason: 'دلیل',
        sqlTargetFailureRetryable: 'قابل تلاش دوباره',
        sqlTargetConfigured: 'مقصد پیکربندی شده است',
        sqlTargetNotConfigured: 'مقصد پیکربندی نشده است',
        sqlTargetUnlockHelp: 'برای بررسی مقصد، یک نشست اپراتور باز کنید.',
        sqlTargetSessionExpired: 'نشست اپراتور منقضی شد. برای ادامه نشست تازه‌ای باز کنید.',
        sqlTargetUnlockRejected: 'مجوز اپراتور پذیرفته نشد.',
        sqlTargetRequestFailed: 'درخواست محافظت‌شده تکمیل نشد.',
        sqlTargetUnlocking: 'در حال ساخت نشست کوتاه‌مدت اپراتور…',
        sqlTargetLocking: 'در حال لغو نشست اپراتور…',
        sqlTargetLockedNotice: 'نشست اپراتور لغو و پنل قفل شد.',
        sqlTargetLockFailed: 'مجوز محلی پاک شد، اما نشست سرور لغو نشد و به‌طور خودکار منقضی خواهد شد.',
        sqlTargetSessionReady: 'نشست اپراتور برقرار شد. وضعیت محافظت‌شده مقصد در حال دریافت است…',
        sqlTargetRefreshing: 'در حال به‌روزرسانی وضعیت محافظت‌شده مقصد…',
        sqlTargetReadyNotice: 'مقصد پیکربندی شده است. ابتدا اتصال و سپس پیش‌نمایش بدون تغییر را اجرا کنید.',
        sqlTargetNotConfiguredNotice: 'پیکربندی مقصد SQL روی سرور کامل نیست و عملیات غیرفعال می‌ماند.',
        sqlTargetTableRequiredNotice: 'DSN پیکربندی شده است، اما پیش از پیش‌نمایش یا همگام‌سازی یک جدول مقصد صریح و معتبر MySQL لازم است.',
        sqlTargetSyncConfirmationRequired: 'پیش‌نمایش را بررسی و گزینه تأیید را برای همگام‌سازی انتخاب کنید.',
        sqlTargetDeleteMissingBlocked: 'اعمال حذف سخت در مرورگر مسدود است، چون توکن برنامه دقیق وجود ندارد.',
        sqlTargetPreviewNotAuthorized: 'اعمال این پیش‌نمایش ایمن نیست. شواهد و وضعیت محافظ ایمنی را بررسی کنید.',
        sqlTargetRunning_test: 'آزمایش اتصال محدود و بدون تغییر در حال اجرا است…',
        sqlTargetRunning_preview: 'پیش‌نمایش همگام‌سازی بدون تغییر در حال محاسبه است…',
        sqlTargetRunning_sync: 'همگام‌سازی دسته‌ای تأییدشده در حال اجرا است…',
        sqlTargetCompleted_test: 'آزمایش اتصال تکمیل شد.',
        sqlTargetCompleted_preview: 'پیش‌نمایش تکمیل شد. پیش از تأیید همگام‌سازی همه شمارش‌ها را بررسی کنید.',
        sqlTargetCompleted_sync: 'همگام‌سازی دستی تکمیل شد.',
        sqlTargetOperationFailed: 'عملیات تکمیل نشد. عیب‌یابی بدون اطلاعات محرمانه در پایین نمایش داده شده است.',
        sqlTargetError_invalid_credential: 'اعتبارنامه اپراتور معتبر نیست.',
        sqlTargetError_invalid_response: 'پاسخ محافظت‌شده سرور معتبر نبود.',
        sqlTargetError_operation_timeout: 'عملیات از مهلت محدود خود عبور کرد.',
        sqlTargetError_preview_required: 'پیش از همگام‌سازی یک پیش‌نمایش تازه اجرا کنید.',
        sqlTargetError_preview_confirmation_required: 'پیش‌نمایش حذف نرم فعلی توکن معتبر برنامه دقیق ارائه نکرد.',
        sqlTargetError_invalid_preview_grant: 'مجوز یک‌بارمصرف پیش‌نمایش نامعتبر بود. یک پیش‌نمایش تازه اجرا کنید.',
        sqlTargetError_preview_unavailable: 'سرور نتوانست مجوز یک‌بارمصرف پیش‌نمایش را صادر کند.',
        sqlTargetError_target_table_required: 'پیش از پیش‌نمایش یا همگام‌سازی، جدول مقصد صریح MySQL را پیکربندی کنید.',
        sqlTargetError_target_table_invalid: 'جدول مقصد MySQL پیکربندی‌شده یک شناسه SQL استاندارد معتبر نیست.',
        sqlTargetError_unsupported_reconciliation: 'این سیاست همگام‌سازی از مرورگر قابل اعمال نیست.',
        sqlTargetReconciliation_upsert_only: 'فقط درج یا به‌روزرسانی',
        sqlTargetReconciliation_soft_delete_missing: 'غیرفعال‌سازی نرم رکوردهای مفقود',
        sqlTargetReconciliation_delete_missing: 'حذف رکوردهای مفقود',
        sqlTargetTLS_unknown: 'نامشخص',
        sqlTargetTLS_encrypted: 'رمزشده',
        sqlTargetTLS_plaintext: 'بدون رمزنگاری',
        sqlTargetOperation_test: 'آزمایش اتصال',
        sqlTargetOperation_preview: 'پیش‌نمایش بدون تغییر',
        sqlTargetOperation_sync: 'همگام‌سازی دستی',
        sqlTargetOperationStatus_succeeded: 'موفق',
        sqlTargetOperationStatus_failed: 'ناموفق',
        sqlTargetGuard_clear: 'برنامه آماده است',
        sqlTargetGuard_empty_source: 'منبع خالی مسدود شد',
        sqlTargetGuard_preview_required: 'پیش‌نمایش لازم است',
        sqlTargetGuard_preview_mismatch: 'پیش‌نمایش تغییر کرده است',
        sqlTargetGuard_reserved_field_collision: 'تداخل فیلد رزروشده',
        sqlTargetGuard_invalid_tombstone_state: 'وضعیت حذف نرم نامعتبر',
        sqlTargetGuard_safety_guard: 'محافظ ایمنی',
        yes: 'بله',
        no: 'خیر',
        ready: 'آماده',
        busy: 'در حال کار',
        refreshSource: 'به‌روزرسانی منبع',
        copyStatus: 'کپی وضعیت اتصال',
        copyEventLog: 'کپی JSON گزارش رویدادها',
        statusCopied: 'وضعیت اتصال در کلیپ‌بورد کپی شد.',
        eventLogCopied: 'JSON گزارش رویدادها در کلیپ‌بورد کپی شد.',
        freezeFirstColumn: 'ثابت نگه‌داشتن اولین ستون داده',
        copied: 'کپی شد',
        codeCopied: 'کد محصول در کلیپ‌بورد کپی شد.',
        jsonCopied: 'JSON ردیف در کلیپ‌بورد کپی شد.',
        copyFailed: 'دسترسی به کلیپ‌بورد ناموفق بود',
        error: 'خطا',
        couldNotLoadPage: 'بارگذاری صفحه ممکن نشد.',
        settingsSaveFailed: 'ذخیره تنظیمات ناموفق بود',
        loadingSource: 'در حال بارگذاری منبع...',
        uploadingSource: 'فایل در حال بارگذاری و نمایشگرهای متصل در حال تغییر منبع هستند.',
        unsupportedFile: 'فایل پشتیبانی نمی‌شود',
        unsupportedFileHelp: 'برای تغییر منبع فعال، یک فایل db. یا json. را رها کنید.',
        sourceLoaded: 'منبع بارگذاری شد',
        sourceLoadedMessage: '{file} اکنون فعال است ({count} رکورد).',
        sourceSwitchFailed: 'تغییر منبع ناموفق بود',
        updatingUI: 'در حال به‌روزرسانی رابط...',
        updatingInterface: 'به‌روزرسانی رابط کاربری',
        updatingInterfaceMessage: 'نسخه جدیدتری از رابط توکار موجود است؛ صفحه اکنون بازخوانی می‌شود.',
        nativeToastUnavailable: 'اعلان بومی در دسترس نیست',
        toastRequestFailed: 'درخواست اعلان ناموفق بود',
        refreshing: 'در حال به‌روزرسانی...',
        refreshRequested: 'به‌روزرسانی درخواست شد',
        refreshRequestedMessage: 'بخش پشتیبان در حال بارگذاری دوباره منبع داده است.',
        refreshed: 'به‌روزرسانی شد',
        refreshedMessage: 'داده‌ها از طریق HTTP دوباره بارگذاری شدند.',
        refreshFailed: 'به‌روزرسانی ناموفق بود',
        settingsReloaded: 'تنظیمات دوباره بارگذاری شد',
        settingsReloadedMessage: 'تغییرات فایل تنظیمات اعمال شد.',
        settingsChangeSingle: 'تنظیم {path} تغییر کرد: {before} ← {after}',
        settingsChangeMultiple: '{count} تنظیم تغییر کرد: {names}{suffix}',
        excelExportStarted: 'ساخت خروجی اکسل آغاز شد',
        excelExportStartedMessage: 'فایل استاندارد شامل رکوردها و اطلاعات غیرمحرمانه منبع است.',
        soundTest: 'آزمایش صدا',
        soundTestMessage: 'صدای اعلان پخش شد.',
        patrisRunning: 'Patris81 در حال اجرا است ({count})',
        patrisNotRunning: 'Patris81 اجرا نشده است',
        databaseLockedCount: 'پایگاه داده قفل است ({count})',
        databaseUnlocked: 'پایگاه داده آزاد است',
        invalidNumberRange: 'یک عدد، بازه کمینه-بیشینه، >=کمینه یا <=بیشینه وارد کنید',
        language: 'زبان',
        file: 'فایل',
        checkingPatris: 'در حال بررسی Patris81...',
        connecting: 'در حال اتصال...',
        connected: 'متصل',
        disconnected: 'قطع‌شده',
        open: 'باز',
        closing: 'در حال بسته‌شدن',
        closed: 'بسته',
        unknown: 'نامشخص',
        exportJSON: 'خروجی JSON',
        exportJSONDescription: 'قالب تبادل داده جاوااسکریپت',
        exportCSV: 'خروجی CSV',
        exportCSVDescription: 'مقادیر جداشده با ویرگول',
        exportExcel: 'دریافت فایل اکسل',
        exportExcelDescription: 'فایل استاندارد XLSX همراه با اطلاعات منبع',
        loadingData: 'در حال بارگذاری داده‌ها...',
        dropDatabase: 'فایل پایگاه داده را رها کنید',
        dropDatabaseDescription: 'فایل db. یا json. را برای بارگذاری در این نمایشگر رها کنید.',
        interface: 'رابط کاربری',
        server: 'سرور',
        database: 'پایگاه داده',
        notifications: 'اعلان‌ها',
        runtime: 'اجرای برنامه',
        theme: 'پوسته',
        system: 'سیستم',
        light: 'روشن',
        dark: 'تیره',
        recordsPerPage: 'تعداد رکورد در صفحه',
        notificationSound: 'صدای اعلان',
        externalFile: 'فایل خارجی',
        generatedMelody: 'آهنگ تولیدشده',
        lastUpdate: 'آخرین به‌روزرسانی',
        absoluteRelative: 'زمان دقیق و نسبی',
        absolute: 'زمان دقیق',
        relative: 'زمان نسبی',
        showFooter: 'نمایش نوار پایین',
        autoScrollChanged: 'حرکت خودکار به موارد تغییریافته',
        highlightChanged: 'برجسته‌کردن موارد تغییریافته',
        rtlTableText: 'نمایش متن جدول از راست به چپ',
        enablePagination: 'فعال‌کردن صفحه‌بندی',
        playUpdateSound: 'پخش صدای اعلان هنگام به‌روزرسانی',
        groupRows: 'ردیف‌های گروه',
        subgroupRows: 'ردیف‌های زیرگروه',
        noStockText: 'متن کالای ناموجود',
        stockAccent: 'نشان موجودی',
        testSound: 'آزمایش صدا',
        testToast: 'آزمایش اعلان',
        host: 'میزبان',
        port: 'درگاه',
        debounce: 'تأخیر تجمیع',
        watchSource: 'پایش منبع و ارسال به‌روزرسانی‌ها',
        databasePath: 'مسیر یا نشانی پایگاه داده',
        customCharmap: 'نگاشت نویسه سفارشی',
        directAccess: 'خواندن مستقیم پایگاه داده بدون کپی موقت',
        rtlConversion: 'فعال‌کردن اختیاری تبدیل راست‌به‌چپ',
        maxDetailedRows: 'بیشترین ردیف دارای جزئیات',
        enableNotifications: 'فعال‌کردن اعلان رویدادها',
        nativeToasts: 'نمایش اعلان بومی سیستم‌عامل',
        relayNotifications: 'ارسال اعلان به کاربران وب متصل',
        clientConnected: 'کاربر متصل شد',
        clientDisconnected: 'کاربر قطع شد',
        sourceFileUpdated: 'فایل منبع با هش قبلی و جدید به‌روز شد',
        rowsChanged: 'ردیف‌ها تغییر کردند',
        includeRowValues: 'افزودن مقادیر قبلی و جدید ردیف‌ها به اعلان',
        tempDirectory: 'پوشه موقت',
        tempStrategy: 'روش استفاده از فضای موقت',
        automatic: 'خودکار',
        systemTemp: 'پوشه موقت سیستم',
        memoryTemp: 'فضای موقت حافظه',
        memoryTempLimit: 'سقف حافظه موقت (مگابایت)',
        debugTools: 'فعال‌کردن ابزارهای اشکال‌زدایی و پیش‌نمایش نگاشت نویسه',
        runtimeHelp: 'برای پوشه موقت سیستم‌عامل از system استفاده کنید. حالت خودکار در لینوکس برای فایل‌های دارای اندازه مشخص و در محدوده مجاز، حافظه اشتراکی را ترجیح می‌دهد.',
        connectionStatus: 'وضعیت اتصال',
        connectionDescription: 'وضعیت زنده وب‌سوکت، منبع و فرایند برنامه.',
        status: 'وضعیت',
        webSocket: 'وب‌سوکت',
        source: 'منبع',
        databaseLock: 'قفل پایگاه داده',
        running: 'در حال اجرا',
        notRunning: 'اجرا نشده',
        locked: 'قفل‌شده',
        unlocked: 'آزاد',
        notStarted: 'شروع نشده',
        selectRecord: 'برای بررسی، یک رکورد را انتخاب کنید',
        close: 'بستن',
        rowIcons: 'آیکون شرطی ردیف',
        rowIconsHelp: 'اولین قانون فعال که با کلید استاندارد تبدیل‌شده مطابقت داشته باشد اعمال می‌شود.',
        enableRowIcons: 'نمایش آیکون‌های شرطی ردیف',
        addRule: 'افزودن قانون',
        fallback: 'حالت پیش‌فرض',
        field: 'کلید استاندارد',
        selectCanonicalField: 'یک کلید استاندارد انتخاب کنید',
        operator: 'عملگر',
        value: 'مقدار',
        icon: 'آیکون',
        color: 'رنگ',
        label: 'برچسب دسترس‌پذیر',
        enabled: 'فعال',
        moveUp: 'انتقال قانون به بالا',
        moveDown: 'انتقال قانون به پایین',
        removeRule: 'حذف قانون',
        rule: 'قانون {number}',
        rowColoring: 'رنگ‌آمیزی ردیف',
        enableRowColoring: 'فعال‌کردن قوانین رنگ‌آمیزی ردیف',
        icon_warning: 'هشدار',
        icon_clock: 'زمان',
        icon_price: 'قیمت',
        icon_weight: 'وزن',
        icon_stock: 'موجودی',
        icon_package: 'بسته',
        icon_check: 'تأیید',
        icon_info: 'اطلاعات',
        ruleLabel_warnings: 'هشدارها',
        ruleLabel_stale: 'داده منبع قدیمی',
        ruleLabel_price_missing: 'قیمت در دسترس نیست',
        ruleLabel_weight_missing: 'وزن در دسترس نیست',
        ruleLabel_out_of_stock: 'ناموجود',
        ruleLabel_in_stock: 'موجود',
        fallbackProduct: 'محصول',
        op_equals: 'برابر است',
        op_not_equals: 'برابر نیست',
        op_contains: 'شامل است',
        op_not_contains: 'شامل نیست',
        op_empty: 'خالی است',
        op_not_empty: 'خالی نیست',
        op_gt: 'بزرگ‌تر از',
        op_gte: 'بزرگ‌تر یا مساوی',
        op_lt: 'کوچک‌تر از',
        op_lte: 'کوچک‌تر یا مساوی',
        op_truthy: 'درست است',
        op_falsy: 'نادرست است',
        op_stale_days: 'قدیمی‌تر از تعداد روز'
    }
};

const ICON_PATHS = {
    warning: '<path d="M12 3 2.8 20h18.4L12 3z"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    price: '<path d="M4 7v10h16V7H4z"/><path d="M8 12h.01M16 12h.01"/><circle cx="12" cy="12" r="2.5"/>',
    weight: '<path d="M6 9h12l2 11H4L6 9z"/><path d="M9 9a3 3 0 0 1 6 0"/>',
    stock: '<path d="m4 8 8-4 8 4-8 4-8-4z"/><path d="m4 8v8l8 4 8-4V8M12 12v8"/>',
    package: '<path d="m4 8 8-4 8 4-8 4-8-4z"/><path d="m4 8v8l8 4 8-4V8M8 6l8 4"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m16 16 4 4"/>',
    copy: '<rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
    braces: '<path d="M8 4H6a2 2 0 0 0-2 2v4a2 2 0 0 1-2 2 2 2 0 0 1 2 2v4a2 2 0 0 0 2 2h2M16 4h2a2 2 0 0 1 2 2v4a2 2 0 0 0 2 2 2 2 0 0 0-2 2v4a2 2 0 0 1-2 2h-2"/>',
    'check-square': '<rect x="3" y="3" width="18" height="18" rx="3"/><path d="m7 12 3 3 7-7"/>',
    more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
    chevron: '<path d="m8 10 4 4 4-4"/>',
    download: '<path d="M12 3v12M7 10l5 5 5-5"/><path d="M4 19h16"/>',
    columns: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M15 4v16"/>',
    refresh: '<path d="M20 7v5h-5"/><path d="M4 17v-5h5"/><path d="M18 9a7 7 0 0 0-12-2l-2 5M6 15a7 7 0 0 0 12 2l2-5"/>',
    list: '<path d="M8 6h12M8 12h12M8 18h12"/><path d="M4 6h.01M4 12h.01M4 18h.01"/>',
    database: '<ellipse cx="12" cy="5" rx="7" ry="3"/><path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5"/><path d="M5 12v7c0 1.7 3.1 3 7 3 1.3 0 2.5-.1 3.5-.4"/><path d="m17 17 2 2 3-4"/>',
    inbox: '<path d="M4 5h16v14H4z"/><path d="m4 13 4-4h8l4 4M8 13l2 2h4l2-2"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5 9 9 0 1 0 20.5 14.5z"/>',
    close: '<path d="M6 6l12 12M18 6 6 18"/>',
    trash: '<path d="M5 7h14M10 11v6M14 11v6M9 7V5h6v2M7 7l1 13h8l1-13"/>',
    arrowUp: '<path d="m7 14 5-5 5 5"/>',
    arrowDown: '<path d="m7 10 5 5 5-5"/>'
};

export function tableLanguage(value) {
    return String(value || '').toLowerCase() === 'fa' ? 'fa' : 'en';
}

export function tableText(language, key, values = {}) {
    const locale = tableLanguage(language);
    const template = TABLE_MESSAGES[locale][key] ?? TABLE_MESSAGES.en[key] ?? key;
    return Object.entries(values).reduce(
        (text, [name, value]) => text.replaceAll(`{${name}}`, String(value)),
        template
    );
}

const RELATIVE_TIME_UNITS = Object.freeze({
    en: Object.freeze({
        day: Object.freeze(['day', 'days']),
        hour: Object.freeze(['hour', 'hours']),
        minute: Object.freeze(['minute', 'minutes']),
        second: Object.freeze(['second', 'seconds'])
    }),
    fa: Object.freeze({
        day: Object.freeze(['روز', 'روز']),
        hour: Object.freeze(['ساعت', 'ساعت']),
        minute: Object.freeze(['دقیقه', 'دقیقه']),
        second: Object.freeze(['ثانیه', 'ثانیه'])
    })
});

export function localizedRelativeTime(value, language = 'en', now = Date.now()) {
    const date = value instanceof Date ? value : new Date(value);
    const referenceTime = now instanceof Date ? now.getTime() : Number(now);
    if (Number.isNaN(date.getTime()) || !Number.isFinite(referenceTime)) {
        return tableLanguage(language) === 'fa' ? 'هرگز' : 'Never';
    }

    const diffMs = date.getTime() - referenceTime;
    const absoluteMs = Math.abs(diffMs);
    const units = [
        ['day', 86400000],
        ['hour', 3600000],
        ['minute', 60000],
        ['second', 1000]
    ];
    const [unit, size] = units.find(([, milliseconds]) => absoluteMs >= milliseconds) || units[units.length - 1];
    const count = Math.max(1, Math.round(absoluteMs / size));
    const locale = tableLanguage(language);
    const label = RELATIVE_TIME_UNITS[locale][unit][count === 1 ? 0 : 1];
    const localizedCount = locale === 'fa'
        ? new Intl.NumberFormat('fa-IR', { useGrouping: false }).format(count)
        : String(count);

    if (locale === 'fa') {
        return diffMs > 0
            ? `${localizedCount} ${label} دیگر`
            : `${localizedCount} ${label} پیش`;
    }
    return diffMs > 0
        ? `in ${localizedCount} ${label}`
        : `${localizedCount} ${label} ago`;
}

export function tableTranslationCoverage() {
    const englishKeys = Object.keys(TABLE_MESSAGES.en);
    const persianKeys = Object.keys(TABLE_MESSAGES.fa);
    return {
        english: englishKeys.length,
        persian: persianKeys.length,
        missingEnglish: persianKeys.filter(key => !Object.prototype.hasOwnProperty.call(TABLE_MESSAGES.en, key)),
        missingPersian: englishKeys.filter(key => !Object.prototype.hasOwnProperty.call(TABLE_MESSAGES.fa, key))
    };
}

export function iconMarkup(name, className = '') {
    const safeName = Object.prototype.hasOwnProperty.call(ICON_PATHS, name) ? name : 'info';
    const safeClass = String(className || '').replace(/[^a-zA-Z0-9 _-]/g, '');
    return `<svg${safeClass ? ` class="${safeClass}"` : ''} viewBox="0 0 24 24" aria-hidden="true" focusable="false">${ICON_PATHS[safeName]}</svg>`;
}

export function stableRecordKey(record) {
    const value = record?.Code ?? record?.product_code ?? '';
    return value === null || value === undefined ? '' : String(value).trim();
}

const STRUCTURED_VALUE_MAX_DEPTH = 32;
const STRUCTURED_VALUE_LOCALES = {
    en: {
        collator: new Intl.Collator('en-US', { numeric: true, sensitivity: 'base' }),
        number: new Intl.NumberFormat('en-US', { useGrouping: false, maximumFractionDigits: 20 })
    },
    fa: {
        collator: new Intl.Collator('fa-IR', { numeric: true, sensitivity: 'base' }),
        number: new Intl.NumberFormat('fa-IR', { useGrouping: false, maximumFractionDigits: 20 })
    }
};

export const TABLE_VIRTUALIZATION_THRESHOLD = 200;
// The existing 28px row action plus vertical cell padding requires 54px.
// Keep this synchronized with the virtualized-grid rule in styles.scss.
export const TABLE_VIRTUAL_ROW_HEIGHT = 54;
export const TABLE_VIRTUAL_OVERSCAN = 8;

// Keep machine output independent of the host locale. Human output deliberately
// uses the selected interface locale, with an ordinal tie-breaker so Collator
// equivalence never makes object-key ordering depend on insertion order.
function compareMachineKeys(left, right) {
    return left < right ? -1 : (left > right ? 1 : 0);
}

function compareDisplayKeys(left, right, locale) {
    const collator = STRUCTURED_VALUE_LOCALES[locale === 'fa' ? 'fa' : 'en'].collator;
    const compared = collator.compare(left, right);
    return compared || compareMachineKeys(left, right);
}

function normalizeStructuredValue(value, seen = new WeakSet(), depth = 0) {
    if (value === null || value === undefined) return null;
    if (typeof value === 'string' || typeof value === 'boolean') return value;
    if (typeof value === 'number') return Number.isFinite(value) ? value : null;
    if (typeof value === 'bigint') return value.toString();
    if (typeof value === 'function' || typeof value === 'symbol') return null;
    if (depth >= STRUCTURED_VALUE_MAX_DEPTH) return '[MaxDepth]';
    if (typeof value !== 'object') return String(value);

    if (Object.prototype.toString.call(value) === '[object Date]') {
        try {
            return value.toISOString();
        } catch {
            return null;
        }
    }
    if (seen.has(value)) return '[Circular]';
    seen.add(value);
    try {
        if (Array.isArray(value)) {
            return value.map(item => normalizeStructuredValue(item, seen, depth + 1));
        }
        const normalized = Object.create(null);
        Object.keys(value).sort(compareMachineKeys).forEach(key => {
            try {
                Object.defineProperty(normalized, key, {
                    value: normalizeStructuredValue(value[key], seen, depth + 1),
                    enumerable: true,
                    configurable: true,
                    writable: true
                });
            } catch {
                Object.defineProperty(normalized, key, {
                    value: '[Unavailable]',
                    enumerable: true,
                    configurable: true,
                    writable: true
                });
            }
        });
        return normalized;
    } finally {
        seen.delete(value);
    }
}

export function stableStructuredJSON(value, space = 0) {
    return JSON.stringify(normalizeStructuredValue(value), null, space);
}

function humanStructuredValue(value, options, seen = new WeakSet(), depth = 0, nested = false) {
    const locale = options.locale === 'fa' ? 'fa' : 'en';
    if (value === null) return options.nullText ?? (locale === 'fa' ? 'تهی' : 'null');
    if (value === undefined) return options.nullText ?? (locale === 'fa' ? 'تعریف‌نشده' : 'undefined');
    if (typeof value === 'string') return nested && value === '' ? '""' : value;
    if (typeof value === 'boolean') {
        if (locale === 'fa') return value ? 'درست' : 'نادرست';
        return value ? 'true' : 'false';
    }
    if (typeof value === 'number') {
        if (!Number.isFinite(value)) return String(value);
        return STRUCTURED_VALUE_LOCALES[locale].number.format(value);
    }
    if (typeof value === 'bigint') return value.toString();
    if (typeof value === 'function' || typeof value === 'symbol') return options.nullText ?? '';
    if (depth >= STRUCTURED_VALUE_MAX_DEPTH) return '[MaxDepth]';
    if (typeof value !== 'object') return String(value);
    if (Object.prototype.toString.call(value) === '[object Date]') {
        try {
            return value.toISOString();
        } catch {
            return options.nullText ?? '';
        }
    }
    if (seen.has(value)) return '[Circular]';
    seen.add(value);
    try {
        if (Array.isArray(value)) {
            return value.map(item => humanStructuredValue(item, options, seen, depth + 1, true)).join(', ');
        }
        const keys = Object.keys(value).sort((left, right) => compareDisplayKeys(left, right, locale));
        const entries = keys.map(key => {
            let nestedValue;
            try {
                nestedValue = humanStructuredValue(value[key], options, seen, depth + 1, true);
            } catch {
                nestedValue = '[Unavailable]';
            }
            return `${key}: ${nestedValue}`;
        });
        return entries.join(', ');
    } finally {
        seen.delete(value);
    }
}

// All viewer presentation/export paths call this formatter. `display` is
// locale-aware for people; `machine` preserves nested values as stable JSON;
// `sort` prefixes the stable machine representation with a type rank.
export function formatStructuredValue(value, options = {}) {
    const mode = options.mode || 'display';
    if (mode === 'json') return stableStructuredJSON(value, options.space || 0);
    if (mode === 'machine') {
        if (value === null || value === undefined) return '';
        if (typeof value === 'object') return stableStructuredJSON(value);
        if (typeof value === 'number' && !Number.isFinite(value)) return '';
        return String(value);
    }
    if (mode === 'sort') {
        const rank = value === null || value === undefined ? 0
            : typeof value === 'boolean' ? 1
                : typeof value === 'number' || typeof value === 'bigint' ? 2
                    : typeof value === 'string' ? 3
                        : Array.isArray(value) ? 4 : 5;
        return `${rank}:${formatStructuredValue(value, { ...options, mode: 'machine' })}`;
    }
    return humanStructuredValue(value, options);
}

export function structuredValueText(value, options = {}) {
    return formatStructuredValue(value, { ...options, mode: 'display' });
}

export function tableVirtualizationEnabled(rowCount, threshold = TABLE_VIRTUALIZATION_THRESHOLD) {
    return Math.max(0, Number(rowCount) || 0) > Math.max(0, Number(threshold) || 0);
}

// Produces one viewport interval plus any pinned logical-focus/anchor rows.
// Sparse intervals keep the active row mounted without forcing every row
// between it and the visible viewport into the DOM.
export function virtualizedRowSegments(options = {}) {
    const rowCount = Math.max(0, Math.floor(Number(options.rowCount) || 0));
    const rowHeight = Math.max(1, Number(options.rowHeight) || TABLE_VIRTUAL_ROW_HEIGHT);
    const viewportHeight = Math.max(rowHeight, Number(options.viewportHeight) || rowHeight);
    const headerHeight = Math.max(0, Number(options.headerHeight) || 0);
    const scrollTop = Math.max(0, Number(options.scrollTop) || 0);
    const overscan = Math.max(0, Math.floor(Number(options.overscan) || 0));
    const viewportTop = Math.max(0, scrollTop - headerHeight);
    const firstVisible = Math.min(rowCount, Math.floor(viewportTop / rowHeight));
    const visibleRows = Math.max(1, Math.ceil(viewportHeight / rowHeight));
    const start = Math.max(0, firstVisible - overscan);
    const end = Math.min(rowCount, firstVisible + visibleRows + overscan);
    const intervals = end > start ? [[start, end]] : [];

    const pinned = [...new Set(options.pinnedIndices || [])]
        .map(index => Math.floor(Number(index)))
        .filter(index => Number.isFinite(index) && index >= 0 && index < rowCount)
        .sort((left, right) => left - right);
    pinned.forEach(index => intervals.push([index, index + 1]));
    intervals.sort((left, right) => left[0] - right[0]);

    const merged = [];
    intervals.forEach(([intervalStart, intervalEnd]) => {
        const previous = merged[merged.length - 1];
        if (previous && intervalStart <= previous[1]) {
            previous[1] = Math.max(previous[1], intervalEnd);
        } else {
            merged.push([intervalStart, intervalEnd]);
        }
    });

    let cursor = 0;
    const segments = merged.map(([segmentStart, segmentEnd]) => {
        const segment = {
            start: segmentStart,
            end: segmentEnd,
            spacerRows: segmentStart - cursor
        };
        cursor = segmentEnd;
        return segment;
    });
    return {
        rowCount,
        rowHeight,
        firstVisible,
        segments,
        trailingSpacerRows: rowCount - cursor,
        renderedRows: segments.reduce((total, segment) => total + segment.end - segment.start, 0)
    };
}

export function duplicateSafeRecordKeys(records) {
    const counts = new Map();
    return (records || []).map(record => {
        const code = stableRecordKey(record);
        const base = code ? `code:${code}` : `row:${hashText(stableValue(record))}`;
        const occurrence = (counts.get(base) || 0) + 1;
        counts.set(base, occurrence);
        return occurrence === 1 ? base : `${base}#${occurrence}`;
    });
}

export function assignStableRecordKeys(records, keyMap = new WeakMap()) {
    const source = Array.isArray(records) ? records : [];
    const proposed = duplicateSafeRecordKeys(source);
    const used = new Set();

    source.forEach(record => {
        if (record && typeof record === 'object') {
            const existing = keyMap.get(record);
            if (existing) used.add(existing);
        }
    });

    source.forEach((record, index) => {
        if (!record || typeof record !== 'object' || keyMap.has(record)) return;
        const base = proposed[index] || `row:${index + 1}`;
        let key = base;
        let suffix = 2;
        while (used.has(key)) {
            key = `${base}~${suffix}`;
            suffix += 1;
        }
        keyMap.set(record, key);
        used.add(key);
    });
    return keyMap;
}

export function pruneSelectedKeys(selectedKeys, records, keyForRecord = stableRecordKey) {
    const existing = new Set((records || []).map(keyForRecord).filter(Boolean));
    for (const key of selectedKeys) {
        if (!existing.has(key)) selectedKeys.delete(key);
    }
    return selectedKeys;
}

export function selectionSummary(selectedKeys, records, keyForRecord = stableRecordKey) {
    const keys = [...new Set((records || []).map(keyForRecord).filter(Boolean))];
    const selected = keys.reduce((count, key) => count + (selectedKeys.has(key) ? 1 : 0), 0);
    return {
        selectable: keys.length,
        selected,
        checked: keys.length > 0 && selected === keys.length,
        indeterminate: selected > 0 && selected < keys.length
    };
}

export function resolvedRovingKey(preferredKey, visibleKeys) {
    const keys = (visibleKeys || []).filter(Boolean);
    if (preferredKey && keys.includes(preferredKey)) return preferredKey;
    return keys[0] || '';
}

export function nextRovingKey(visibleKeys, currentKey, command) {
    const keys = (visibleKeys || []).filter(Boolean);
    if (keys.length === 0) return '';
    if (command === 'Home') return keys[0];
    if (command === 'End') return keys[keys.length - 1];
    const current = Math.max(0, keys.indexOf(currentKey));
    if (command === 'ArrowUp') return keys[Math.max(0, current - 1)];
    if (command === 'ArrowDown') return keys[Math.min(keys.length - 1, current + 1)];
    return resolvedRovingKey(currentKey, keys);
}

export function rovingTabIndexes(visibleKeys, preferredKey) {
    const active = resolvedRovingKey(preferredKey, visibleKeys);
    return (visibleKeys || []).map(key => key === active ? 0 : -1);
}

export function warehouseColumnField(name) {
    return `${WAREHOUSE_FIELD_PREFIX}${encodeURIComponent(String(name ?? '').trim())}`;
}

export function warehouseColumnName(field) {
    const source = String(field || '');
    if (source.startsWith(WAREHOUSE_FIELD_PREFIX)) {
        try {
            return decodeURIComponent(source.slice(WAREHOUSE_FIELD_PREFIX.length));
        } catch {
            return source.slice(WAREHOUSE_FIELD_PREFIX.length);
        }
    }
    const legacy = source.match(/^ANBAR(\d+)$/i);
    return legacy ? legacy[1] : '';
}

export function isWarehouseColumnField(field) {
    return String(field || '').startsWith(WAREHOUSE_FIELD_PREFIX) || /^ANBAR\d+$/i.test(String(field || ''));
}

export function deriveGridFields(records) {
    const rows = Array.isArray(records) ? records.filter(record => record && typeof record === 'object') : [];
    const sourceFields = [];
    const seenFields = new Set();
    const warehouseNames = new Set();
    let legacyWarehouseCount = 0;

    rows.forEach(record => {
        Object.keys(record).forEach(field => {
            if (!seenFields.has(field)) {
                seenFields.add(field);
                sourceFields.push(field);
            }
        });
        if (record.warehouse_stock && typeof record.warehouse_stock === 'object' && !Array.isArray(record.warehouse_stock)) {
            Object.keys(record.warehouse_stock).forEach(name => warehouseNames.add(name));
        }
        if (Array.isArray(record.ANBAR)) legacyWarehouseCount = Math.max(legacyWarehouseCount, record.ANBAR.length);
    });

    const identityField = sourceFields.includes('Code') ? 'Code'
        : sourceFields.includes('product_code') ? 'product_code'
            : sourceFields.includes('category_code') ? 'category_code' : sourceFields[0];
    const nameField = sourceFields.find(field => canonicalColumnKey(field) === 'name');
    const hiddenSources = new Set(['ANBAR', 'warehouse_stock']);
    if (identityField === 'Code') hiddenSources.add('product_code');
    const regularFields = sourceFields.filter(field => field !== identityField && field !== nameField && !hiddenSources.has(field));
    const warehouseFields = warehouseNames.size > 0
        ? [...warehouseNames]
            .sort((left, right) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }))
            .map(warehouseColumnField)
        : Array.from({ length: legacyWarehouseCount }, (_, index) => `ANBAR${index + 1}`);

    return [identityField, nameField, ...regularFields, ...warehouseFields].filter(Boolean);
}

export function localizedColumnLabel(field, language = 'en', configuredLabel = '') {
    const locale = tableLanguage(language);
    const warehouse = warehouseColumnName(field);
    if (warehouse) return warehouse;

    const source = String(field || '').trim();
    const canonical = canonicalColumnKey(source).toLowerCase();
    const configured = String(configuredLabel || '').trim();
    const humanized = humanizeColumnKey(source);
    const configuredIsGenerated = configured.localeCompare(source, undefined, { sensitivity: 'base' }) === 0
        || configured.localeCompare(humanized, undefined, { sensitivity: 'base' }) === 0;
    const configuredIsDefault = LEGACY_DEFAULT_COLUMN_LABELS.has(configured.toLowerCase())
        || (canonical === 'product_code' && PRODUCT_CODE_DEFAULT_LABELS.has(normalizedPresentationLabel(configured)));
    if (configured && !configuredIsGenerated && !configuredIsDefault) return configured;
    if (COLUMN_LABELS[canonical]) return COLUMN_LABELS[canonical][locale];
    return humanized;
}

function normalizedPresentationLabel(value) {
    return String(value || '')
        .normalize('NFKC')
        .replace(/[يى]/g, 'ی')
        .replace(/ك/g, 'ک')
        .replace(/[\u200c\u200d]/g, ' ')
        .replace(/[\u200e\u200f\u202a-\u202e\u2066-\u2069]/g, '')
        .replace(/\s+/g, ' ')
        .trim()
        .toLocaleLowerCase('en-US');
}

function humanizeColumnKey(field) {
    return String(field || '')
        .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
        .replace(/[_-]+/g, ' ')
        .replace(/\s+/g, ' ')
        .trim()
        .replace(/\b\w/g, letter => letter.toUpperCase());
}

export function defaultColumnWidth(field) {
    if (field === 'Code' || field === 'product_code') return 156;
    if (field === 'Name' || field === 'name') return 240;
    if (isWarehouseColumnField(field)) return 112;
    if (String(field).toLowerCase() === 'warnings') return 190;
    if (/description|warning|sharh|title|name/i.test(field)) return 220;
    return 144;
}

export function clampColumnWidth(value, fallback = 144) {
    const numeric = Number(value);
    const safe = Number.isFinite(numeric) ? numeric : Number(fallback);
    return Math.round(Math.min(MAX_COLUMN_WIDTH, Math.max(MIN_COLUMN_WIDTH, safe)));
}

export function resizedColumnWidth(startWidth, movementX, rtl = false) {
    const direction = rtl ? -1 : 1;
    return clampColumnWidth(Number(startWidth) + Number(movementX || 0) * direction, startWidth);
}

export function keyboardColumnWidth(currentWidth, key, rtl = false) {
    if (key === 'Home') return null;
    if (key !== 'ArrowLeft' && key !== 'ArrowRight') return undefined;
    const physicalDirection = key === 'ArrowRight' ? 1 : -1;
    return clampColumnWidth(Number(currentWidth) + physicalDirection * (rtl ? -1 : 1) * COLUMN_RESIZE_STEP, currentWidth);
}

export function normalizeColumnWidths(widths) {
    if (!widths || typeof widths !== 'object' || Array.isArray(widths)) return {};
    return Object.entries(widths).reduce((result, [field, width]) => {
        const key = canonicalColumnKey(field);
        if (key && (result[key] === undefined || String(field) === key)) {
            result[key] = clampColumnWidth(width, defaultColumnWidth(key));
        }
        return result;
    }, {});
}

export function normalizeColumnPreferenceList(fields) {
    if (!Array.isArray(fields)) return [];
    const seen = new Set();
    return fields.reduce((result, field) => {
        const key = String(field ?? '').trim();
        if (key && !seen.has(key)) {
            seen.add(key);
            result.push(key);
        }
        return result;
    }, []);
}

export function resolvePersistedColumnPreferences(ui = {}, legacy = {}) {
    const configuredHiddenColumns = Array.isArray(ui?.hidden_columns);
    const configuredColumnOrder = Array.isArray(ui?.column_order);
    const legacyHiddenColumns = Array.isArray(legacy?.hiddenColumns)
        ? normalizeColumnPreferenceList(legacy.hiddenColumns) : null;
    const legacyColumnOrder = Array.isArray(legacy?.columnOrder)
        ? normalizeColumnPreferenceList(legacy.columnOrder) : null;
    return {
        hiddenColumns: configuredHiddenColumns
            ? normalizeColumnPreferenceList(ui.hidden_columns)
            : legacyHiddenColumns || [],
        columnOrder: configuredColumnOrder
            ? normalizeColumnPreferenceList(ui.column_order)
            : legacyColumnOrder || [],
        cacheHiddenColumns: configuredHiddenColumns || legacyHiddenColumns !== null,
        cacheColumnOrder: configuredColumnOrder || legacyColumnOrder !== null,
        migrateHiddenColumns: !configuredHiddenColumns && legacyHiddenColumns !== null,
        migrateColumnOrder: !configuredColumnOrder && legacyColumnOrder !== null
    };
}

export function canonicalColumnKey(field) {
    const source = String(field || '').trim();
    const lowered = source.toLowerCase();
    if (lowered === 'code' || lowered === 'product_code') return 'product_code';
    if (lowered === 'name') return 'name';
    if (lowered === 'serial') return 'serial';
    return source;
}

export function canonicalRuleField(field) {
    const lowered = String(field || '').trim().toLowerCase();
    if (lowered === 'code') return 'product_code';
    if (lowered === 'name') return 'name';
    if (lowered === 'serial') return 'serial';
    return CANONICAL_ROW_FIELDS.includes(lowered) ? lowered : '';
}

export function normalizeRowIconRule(rule, index = 0) {
    const source = rule && typeof rule === 'object' ? rule : {};
    const operator = ROW_RULE_OPERATORS.includes(source.operator) ? source.operator : 'equals';
    const icon = ROW_ICON_NAMES.includes(source.icon) ? source.icon : 'info';
    return {
        id: String(source.id || `rule-${index + 1}`).trim() || `rule-${index + 1}`,
        field: canonicalRuleField(source.field),
        operator,
        value: source.value === null || source.value === undefined ? '' : String(source.value),
        icon,
        color: validColor(source.color) ? source.color : '#6366f1',
        label: String(source.label || '').trim(),
        disabled: source.disabled === true
    };
}

export function normalizeRowIconRules(rules) {
    const source = Array.isArray(rules) ? rules : [];
    return source.map(normalizeRowIconRule);
}

export function normalizeRowIconFallback(fallback) {
    const source = fallback && typeof fallback === 'object' ? fallback : DEFAULT_ROW_ICON_FALLBACK;
    return {
        icon: ROW_ICON_NAMES.includes(source.icon) ? source.icon : DEFAULT_ROW_ICON_FALLBACK.icon,
        color: validColor(source.color) ? source.color : DEFAULT_ROW_ICON_FALLBACK.color,
        label: String(source.label || DEFAULT_ROW_ICON_FALLBACK.label).trim() || DEFAULT_ROW_ICON_FALLBACK.label
    };
}

export function resolveRowIcon(record, rules, fallback) {
    const normalizedRules = Array.isArray(rules) ? rules : normalizeRowIconRules(rules);
    for (const rule of normalizedRules) {
        if (!rule.disabled && rule.field && rowRuleMatches(canonicalRowValue(record, rule.field), rule.operator, rule.value)) {
            return { icon: rule.icon, color: rule.color, label: rule.label, ruleId: rule.id };
        }
    }
    return { ...normalizeRowIconFallback(fallback), ruleId: '' };
}

export function hasNamingConventionWarnings(record) {
    const warnings = record?.warnings;
    const values = Array.isArray(warnings) ? warnings : (typeof warnings === 'string' ? [warnings] : []);
    return values.some(warning => String(warning).trim().startsWith('naming_'));
}

export function canonicalRowValue(record, field) {
    if (!record || typeof record !== 'object') return undefined;
    const canonicalField = canonicalRuleField(field);
    if (!canonicalField) return undefined;
    if (canonicalField === 'product_code') return record.product_code ?? record.Code;
    if (canonicalField === 'name') return record.name ?? record.Name;
    if (canonicalField === 'serial') return record.serial ?? record.Serial;
    if (canonicalField === 'total_stock' && record.total_stock === undefined && Array.isArray(record.ANBAR)) {
        return record.ANBAR.reduce((total, value) => {
            const numeric = Number(value);
            return total + (Number.isFinite(numeric) ? numeric : 0);
        }, 0);
    }
    return record[canonicalField];
}

export function rowRuleMatches(actual, operator, expected) {
    const flattened = flattenRuleValue(actual);
    const expectedText = String(expected ?? '').trim();
    const actualText = flattened.text;
    const empty = flattened.empty;
    switch (operator) {
        case 'equals':
            return actualText.localeCompare(expectedText, undefined, { sensitivity: 'base' }) === 0;
        case 'not_equals':
            return actualText.localeCompare(expectedText, undefined, { sensitivity: 'base' }) !== 0;
        case 'contains':
            return actualText.toLocaleLowerCase().includes(expectedText.toLocaleLowerCase());
        case 'not_contains':
            return !actualText.toLocaleLowerCase().includes(expectedText.toLocaleLowerCase());
        case 'empty':
            return empty;
        case 'not_empty':
            return !empty;
        case 'truthy':
            return Boolean(actual) && !empty && actualText !== '0' && actualText.toLowerCase() !== 'false';
        case 'falsy':
            return !actual || empty || actualText === '0' || actualText.toLowerCase() === 'false';
        case 'gt':
        case 'gte':
        case 'lt':
        case 'lte': {
            const left = Number(actual);
            const right = Number(expectedText);
            if (!Number.isFinite(left) || !Number.isFinite(right)) return false;
            if (operator === 'gt') return left > right;
            if (operator === 'gte') return left >= right;
            if (operator === 'lt') return left < right;
            return left <= right;
        }
        case 'stale_days': {
            const jalaliAge = jalaliAgeDays(actual);
            const timestamp = parseRuleDate(actual);
            const days = Number(expectedText);
            if (!Number.isFinite(days) || days < 0) return false;
            if (Number.isFinite(jalaliAge)) return jalaliAge > days;
            if (!Number.isFinite(timestamp)) return false;
            return Date.now() - timestamp > days * 24 * 60 * 60 * 1000;
        }
        default:
            return false;
    }
}

export function rowCommandDefinitions({ selected = false, hasCode = true } = {}) {
    return ROW_COMMAND_DEFINITIONS
        .filter(command => !command.requiresCode || hasCode)
        .map(command => ({
            ...command,
            labelKey: command.selectedLabelKey && selected ? command.selectedLabelKey : command.labelKey
        }));
}

export function fitMenuPosition({ x, y, width, height, viewportWidth, viewportHeight, margin = 8 }) {
    const maxX = Math.max(margin, Number(viewportWidth) - Number(width) - margin);
    const maxY = Math.max(margin, Number(viewportHeight) - Number(height) - margin);
    return {
        left: Math.max(margin, Math.min(Number(x) || margin, maxX)),
        top: Math.max(margin, Math.min(Number(y) || margin, maxY))
    };
}

function flattenRuleValue(value) {
    if (Array.isArray(value)) {
        const values = value.filter(item => item !== null && item !== undefined && String(item).trim() !== '');
        return { text: values.join(', '), empty: values.length === 0 };
    }
    if (value === null || value === undefined) return { text: '', empty: true };
    if (typeof value === 'object') {
        const keys = Object.keys(value);
        return { text: keys.length ? JSON.stringify(value) : '', empty: keys.length === 0 };
    }
    const text = String(value).trim();
    return { text, empty: text === '' };
}

function parseRuleDate(value) {
    if (value instanceof Date) return value.getTime();
    const text = String(value ?? '').trim();
    if (!text) return NaN;
    const timestamp = Date.parse(text);
    return Number.isFinite(timestamp) ? timestamp : NaN;
}

function jalaliAgeDays(value, now = new Date()) {
    const match = String(value ?? '').trim().match(/^(\d{2}|\d{4})[./-](\d{2})[./-](\d{2})$/);
    if (!match) return NaN;
    const year = match[1].length === 2 ? 1400 + Number(match[1]) : Number(match[1]);
    const month = Number(match[2]);
    const day = Number(match[3]);
    if (!validJalaliParts(year, month, day)) return NaN;

    try {
        const parts = new Intl.DateTimeFormat('en-US-u-ca-persian', {
            year: 'numeric',
            month: 'numeric',
            day: 'numeric'
        }).formatToParts(now).reduce((result, part) => {
            if (part.type === 'year' || part.type === 'month' || part.type === 'day') {
                result[part.type] = Number(part.value);
            }
            return result;
        }, {});
        if (!validJalaliParts(parts.year, parts.month, parts.day)) return NaN;
        return jalaliOrdinal(parts.year, parts.month, parts.day) - jalaliOrdinal(year, month, day);
    } catch {
        return NaN;
    }
}

function validJalaliParts(year, month, day) {
    if (!Number.isInteger(year) || !Number.isInteger(month) || !Number.isInteger(day)) return false;
    if (month < 1 || month > 12 || day < 1) return false;
    const maxDay = month <= 6 ? 31 : month <= 11 ? 30 : 30;
    return day <= maxDay;
}

function jalaliOrdinal(year, month, day) {
    // The 2,820-year arithmetic Persian-calendar cycle keeps day differences
    // stable across year boundaries without treating YY.MM.DD as Gregorian.
    const cycleYear = ((year - 474) % 2820 + 2820) % 2820 + 474;
    const leapDays = Math.floor(((cycleYear + 38) * 682) / 2816);
    const cycles = Math.floor((year - cycleYear) / 2820);
    const beforeMonth = month <= 7 ? (month - 1) * 31 : 186 + (month - 7) * 30;
    return cycles * 1029983 + (cycleYear - 1) * 365 + leapDays + beforeMonth + day;
}

function validColor(value) {
    return /^#[0-9a-f]{6}$/i.test(String(value || ''));
}

function stableValue(value) {
    if (Array.isArray(value)) return `[${value.map(stableValue).join(',')}]`;
    if (value && typeof value === 'object') {
        return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stableValue(value[key])}`).join(',')}}`;
    }
    return JSON.stringify(value ?? null);
}

function hashText(value) {
    let hash = 2166136261;
    for (let index = 0; index < value.length; index += 1) {
        hash ^= value.charCodeAt(index);
        hash = Math.imul(hash, 16777619);
    }
    return (hash >>> 0).toString(36);
}
