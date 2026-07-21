export const MIN_COLUMN_WIDTH = 80;
export const MAX_COLUMN_WIDTH = 480;
export const COLUMN_RESIZE_STEP = 12;
export const WAREHOUSE_FIELD_PREFIX = 'warehouse_stock::';

const COLUMN_LABELS = Object.freeze({
    product_code: Object.freeze({ en: 'Code', fa: 'کد' }),
    category_code: Object.freeze({ en: 'Category Code', fa: 'کد دسته‌بندی' }),
    name: Object.freeze({ en: 'Name', fa: 'نام' }),
    part_number: Object.freeze({ en: 'Part Number', fa: 'پارت نامبر' }),
    serial: Object.freeze({ en: 'Serial Number', fa: 'شماره سریال' }),
    unit: Object.freeze({ en: 'Unit', fa: 'واحد' }),
    sale_price_source: Object.freeze({ en: 'Sale Price', fa: 'قیمت فروش' }),
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

export function structuredValueText(value) {
    if (value === null || value === undefined) return '';
    if (Array.isArray(value)) return value.map(structuredValueText).filter(Boolean).join(', ');
    if (typeof value === 'object') {
        return Object.entries(value)
            .sort(([left], [right]) => left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' }))
            .map(([key, nested]) => `${key}: ${structuredValueText(nested)}`)
            .join(', ');
    }
    return String(value);
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
    if (configured && !configuredIsGenerated && !LEGACY_DEFAULT_COLUMN_LABELS.has(configured.toLowerCase())) return configured;
    if (COLUMN_LABELS[canonical]) return COLUMN_LABELS[canonical][locale];
    return humanized;
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
