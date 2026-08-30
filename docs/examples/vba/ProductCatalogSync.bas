Attribute VB_Name = "ProductCatalogSync"
Option Explicit

Private Const PRODUCTS_TABLE As String = "Products"
Private Const SYNC_TABLE As String = "SyncData"
Private Const YUAN_TABLE As String = "Yuan_Price"
Private Const SHIPPING_TABLE As String = "Shipping"
Private Const PROFIT_TABLE As String = "Profit"
Private Const PRODUCT_COLUMN_COUNT As Long = 10
Private Const SYNC_COLUMN_COUNT As Long = 24
Private Const SNAPSHOT_PAGE_SIZE As Long = 250
Private Const MAX_STATE_PAGES As Long = 8
Private Const MAX_SNAPSHOT_ROWS As Long = 2000
Private Const HTTP_TIMEOUT_MS As Long = 150000
Private Const PRICING_HTTP_TIMEOUT_MS As Long = 600000
Private Const SNAPSHOT_WAIT_TIMEOUT_MS As Long = 360000
Private Const MAX_REFRESH_WALL_SECONDS As Double = 125#
Private Const OPEN_REFRESH_DELAY_SECONDS As Long = 2
Private Const SEARCH_DELAY_SECONDS As Double = 0.55
Private Const SEARCH_RETRY_LIMIT As Long = 6
Private Const SEARCH_PASTE_MAX_CHARS As Long = 2048
Private Const PROGRESS_RESET_SECONDS As Long = 6
Private Const UI_PUMP_ROW_INTERVAL As Long = 25
Private Const MAX_PRICING_RESPONSE_CHARS As Long = 4194304
Private Const MAX_PRICING_RESPONSE_BYTES As Long = 4194304
Private Const SSE_BACKLOG_CAP_BYTES As Long = 1048576
Private Const SSE_RECONNECT_MIN_SECONDS As Long = 1
Private Const SSE_RECONNECT_MAX_SECONDS As Long = 30
Private Const SSE_RECEIVE_TIMEOUT_MS As Long = 0
Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"
Private Const PRICING_CLIENT_ID As String = "digitalogic-price-calculator/v1"
Private Const PRICING_CONTRACT_CLIENT_ID As String = "digitalogic-price-calculator"
Private Const PRICING_CONTRACT_CHANNEL As String = "excel-workbook"
Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"
Private Const PRICING_REQUEST_SCHEMA As String = "patris.excel-pricing-companion-request/v1"
Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"
Private Const PRICING_REVISION_SCHEMA As String = "patris.excel-pricing-companion-revision/v1"
Private Const PRICING_WRITEBACK_REQUEST_SCHEMA As String = "patris.pricing-input-writeback-request/v1"
Private Const PRICING_WRITEBACK_BATCH_REQUEST_SCHEMA As String = "patris.pricing-settings-writeback-request/v2"
Private Const PRICING_WRITEBACK_JOB_SCHEMA As String = "patris.pricing-input-writeback-job/v1"
Private Const PRICING_CONFIRMATION_REQUEST_SCHEMA As String = "patris.pricing-confirmation-ack-request/v1"
Private Const PRICING_CONFIRMATION_SCHEMA As String = "digitalogic.pricing-confirmation/v1"
Private Const PRICING_STATE_SCHEMA As String = "digitalogic.pricing-sync-state/v1"
Private Const PRICING_SNAPSHOT_REQUEST_SCHEMA As String = "patris.pricing-snapshot-request/v1"
Private Const PRICING_SNAPSHOT_JOB_SCHEMA As String = "patris.pricing-snapshot-job/v1"
Private Const PRICING_SNAPSHOT_PAYLOAD_SCHEMA As String = "patris.pricing-snapshot/v1"
Private Const PRICING_SNAPSHOT_EVENT_SCHEMA As String = "patris.pricing-state-event/v1"
Private Const PRICING_SNAPSHOT_PROJECTION As String = "excel-v1"
Private Const PRICING_SNAPSHOT_ROW_FIELD_COUNT As Long = 27
Private Const PRICING_SNAPSHOT_ROW_FIELDS As String = _
    "sync_key,reconciliation_status,patris_code,woocommerce_id,sku," & _
    "weight_grams,foreign_price,patris_location,categories," & _
    "foreign_currency,shipping_price_per_kg," & _
    "shipping_price_per_kg_currency,profit_margin_percent," & _
    "price_source_amount,price_source_currency,price_source_kind," & _
    "effective_price,patris_total_stock,stock_quantity,name,updated_at," & _
    "record_revision,permalink,patris_final_price,sale_price," & _
    "publication_status,image_url"
Private Const SNAPSHOT_FIELD_SYNC_KEY As Long = 1
Private Const SNAPSHOT_FIELD_RECONCILIATION_STATUS As Long = 2
Private Const SNAPSHOT_FIELD_PATRIS_CODE As Long = 3
Private Const SNAPSHOT_FIELD_WOOCOMMERCE_ID As Long = 4
Private Const SNAPSHOT_FIELD_SKU As Long = 5
Private Const SNAPSHOT_FIELD_WEIGHT_GRAMS As Long = 6
Private Const SNAPSHOT_FIELD_FOREIGN_PRICE As Long = 7
Private Const SNAPSHOT_FIELD_PATRIS_LOCATION As Long = 8
Private Const SNAPSHOT_FIELD_CATEGORIES As Long = 9
Private Const SNAPSHOT_FIELD_FOREIGN_CURRENCY As Long = 10
Private Const SNAPSHOT_FIELD_SHIPPING_PRICE_PER_KG As Long = 11
Private Const SNAPSHOT_FIELD_SHIPPING_CURRENCY As Long = 12
Private Const SNAPSHOT_FIELD_PROFIT_MARGIN_PERCENT As Long = 13
Private Const SNAPSHOT_FIELD_PRICE_SOURCE_AMOUNT As Long = 14
Private Const SNAPSHOT_FIELD_PRICE_SOURCE_CURRENCY As Long = 15
Private Const SNAPSHOT_FIELD_PRICE_SOURCE_KIND As Long = 16
Private Const SNAPSHOT_FIELD_EFFECTIVE_PRICE As Long = 17
Private Const SNAPSHOT_FIELD_PATRIS_TOTAL_STOCK As Long = 18
Private Const SNAPSHOT_FIELD_STOCK_QUANTITY As Long = 19
Private Const SNAPSHOT_FIELD_NAME As Long = 20
Private Const SNAPSHOT_FIELD_UPDATED_AT As Long = 21
Private Const SNAPSHOT_FIELD_RECORD_REVISION As Long = 22
Private Const SNAPSHOT_FIELD_PERMALINK As Long = 23
Private Const SNAPSHOT_FIELD_PATRIS_FINAL_PRICE As Long = 24
Private Const SNAPSHOT_FIELD_SALE_PRICE As Long = 25
Private Const SNAPSHOT_FIELD_PUBLICATION_STATUS As Long = 26
Private Const SNAPSHOT_FIELD_IMAGE_URL As Long = 27
' WordPress validates the exact live source and state revision before it reuses
' a snapshot. Keep that authenticated snapshot warm long enough for ordinary
' consecutive refreshes; changed revisions still force the full fail-closed
' path, while the companion additionally requires a live verified revision
' before it reuses its own in-process copy.
' The companion stores only a revision-fenced in-memory projection. This does
' not persist product rows in the template or on disk; any event/disconnect
' makes the cache ineligible until the live composite is authenticated again.
Private Const PRICING_SNAPSHOT_CACHE_SECONDS As Long = 86400
Private Const PRICE_ROUNDING_MODE As String = "nearest_half_up"
Private Const LOOPBACK_PREFIX As String = "http://127.0.0.1:18080/"
Private Const SEARCH_BUTTON_SHAPE As String = "ProductSearchButton"
Private Const PROGRESS_TRACK_SHAPE As String = "OperationProgressTrack"
Private Const PROGRESS_FILL_SHAPE As String = "OperationProgressFill"
Private Const PROGRESS_TEXT_SHAPE As String = "OperationProgressText"
Private Const PRODUCT_IMAGE_SHAPE As String = "ProductImagePreview"
Private Const PRODUCT_IMAGE_CACHE_LIMIT As Long = 16
Private Const PRODUCT_IMAGE_MAX_BYTES As Long = 2097152
Private Const PRODUCT_IMAGE_TIMEOUT_MS As Long = 15000
Private Const IMAGE_PREVIEW_DELAY_SECONDS As Double = 0.35
Private Const DEFAULT_PERSIAN_FONT As String = "Yekan Bakh"
Private Const DEFAULT_LATIN_FONT As String = "Segoe UI"
Private Const DEFAULT_FANUM_FONT As String = "Yekan Bakh FaNum"
Private Const RECONCILED_COLUMN_KEYS As String = _
    "sync_key,reconciliation_status,patris_code,woocommerce_id,parent_id," & _
    "product_type,publication_status,name,part_number,sku,categories," & _
    "category_ids,currency,regular_price,sale_price,effective_price," & _
    "patris_final_price,price_status,stock_quantity,stock_status," & _
    "patris_total_stock,patris_minimum_stock,patris_location,weight_grams," & _
    "woocommerce_weight,woocommerce_weight_unit,foreign_price," & _
    "foreign_currency,partner_price_irr,price_source_amount," & _
    "price_source_currency,price_source_kind,price_rounding_digits," & _
    "price_rounding_mode,shipping_method_id,shipping_method_name_en," & _
    "shipping_method_name_fa,shipping_price_per_kg," & _
    "shipping_price_per_kg_currency,profit_margin_percent,permalink," & _
    "image_url,updated_at,sync_status,sync_error,record_revision"
Private Const MB_RIGHT As Long = &H80000
Private Const MB_RTLREADING As Long = &H100000
Private Const CF_UNICODETEXT As Long = 13

Private mSourceID As String
Private mSourceDataset As String
Private mSourceRevision As String
Private mLastPreviewDigest As String
Private mLastPreviewExpiresAt As String
Private mLastPreviewStateRevision As String
Private mLastPreviewSettings As String
Private mLastPreviewWarningCount As Long
Private mLastApplyRequestID As String
Private mProposalSyncActive As Boolean
Private mLastRefreshSucceeded As Boolean
Private mSaveFlowActive As Boolean
Private mSaveRenameSchedulesSuspended As Boolean
Private mSaveRenameRefreshPending As Boolean
Private mSaveRenameAsyncPending As Boolean
Private mSaveRenamePreviewPending As Boolean
Private mSaveRenameImagePreviewPending As Boolean
Private mSaveRenameWritebackPending As Boolean
Private mSaveRenameEventRefreshPending As Boolean
Private mSaveRenameSseReconnectPending As Boolean
Private mSaveRenameSseRenewSession As Boolean
Private mSearchQuery As String
Private mSearchCurrentRow As Long
Private mSearchScheduled As Boolean
Private mScheduledSearchTime As Date
Private mSearchRetryCount As Long
Private mPendingSearchQuery As String
Private mSearchRequestGeneration As Long
Private mPendingSearchGeneration As Long
Private mSearchNativeEnterPending As Boolean
Private mSearchPasteHotkeyRegistered As Boolean
Private mLastSearchSelectionSheet As String
Private mLastSearchSelectionRow As Long
Private mLastSearchSelectionColumn As Long
Private mSearchIndexReady As Boolean
Private mSearchIndexRowCount As Long
Private mSearchIndexRevision As String
Private mSearchIndexNames() As String
Private mSearchIndexProductCodes() As String
Private mSearchIndexWooIDs() As String
Private mPricingCSRFToken As String
Private mSessionSeconds As Double
Private mStatePageTimingText As String
Private mSnapshotValidationStage As String
Private mReconcileSeconds As Double
Private mPricingSeconds As Double
Private mTableWriteSeconds As Double
Private mFormattingSeconds As Double
Private mSaveTimingStartedAt As Double
Private mSaveTimingActive As Boolean
Private mRefreshInProgress As Boolean
Private mSearchInProgress As Boolean
Private mPricingActionInProgress As Boolean
Private mInternalPricingRefresh As Boolean
Private mRequiredSnapshotStateRevision As String
Private mRefreshCancelRequested As Boolean
Private mRefreshCancelHotkeyRegistered As Boolean
Private mRefreshScheduled As Boolean
Private mRefreshScheduleFailed As Boolean
Private mScheduledRefreshTime As Date
Private mPricingPreviewQueued As Boolean
Private mPricingPreviewScheduled As Boolean
Private mScheduledPricingPreviewTime As Date
Private mWritebackPending As Object
Private mWritebackPendingValues As Object
Private mWritebackPendingGenerations As Object
Private mPricingDirty As Object
Private mPricingDirtyGenerations As Object
Private mWritebackRequest As AsyncWinHttpRequest
Private mWritebackScheduled As Boolean
Private mScheduledWritebackTime As Date
Private mWritebackStage As String
Private mWritebackSettingKey As String
Private mWritebackCellAddress As String
Private mWritebackRequestID As String
Private mWritebackJobID As String
Private mWritebackCSRFToken As String
Private mWritebackPollCount As Long
Private mPricingEditGeneration As Long
Private mActiveWritebackGeneration As Long
Private mActiveWritebackDesiredValue As String
Private mActiveWritebackKeys As String
Private mActiveWritebackRequestBody As String
Private mPendingConfirmationTransactionID As String
Private mPendingConfirmationRevision As String
Private mPendingConfirmationDigest As String
Private mPendingConfirmationDeadline As Double
Private mForceFreshSnapshot As Boolean
Private mImageRequest As AsyncWinHttpRequest
Private mImageCache As Object
Private mImageRequestURL As String
Private mImageRequestRow As Long
Private mImageGeneration As Long
Private mImageRequestGeneration As Long
Private mImagePreviewScheduled As Boolean
Private mScheduledImagePreviewTime As Date
Private mPendingImagePreviewRow As Long

' Finite HTTP work is serialized through one callback-driven request lane.
' The SSE request is deliberately separate so it can remain connected after
' the snapshot payload has been committed.
Private mOperationRequest As AsyncWinHttpRequest
Private mCancelRequest As AsyncWinHttpRequest
Private mSseRequest As AsyncWinHttpRequest
Private mSseSessionRequest As AsyncWinHttpRequest
Private mSseParser As PricingSseParser
Private mAsyncDispatchPending As Object
Private mAsyncDispatchActive As Boolean
Private mAsyncDispatchScheduled As Boolean
Private mAsyncDispatchTime As Date
Private mAsyncDispatchErrorNumber As Long
Private mAsyncDispatchErrorDescription As String
Private mAsyncTokenCounter As Long
Private mOperationKind As String
Private mOperationStage As String
Private mOperationSilent As Boolean
Private mOperationShowMessage As Boolean
Private mOperationRequireConfirmation As Boolean
Private mOperationCancelRequested As Boolean
Private mOperationFinishing As Boolean
Private mOperationPreviousEnableCancelKey As XlEnableCancelKey
Private mOperationCancelKeyCaptured As Boolean
Private mOperationPreviousStatusBar As Variant
Private mOperationStatusBarCaptured As Boolean
Private mProgressResetScheduled As Boolean
Private mProgressResetTime As Date
Private mOperationStartedAt As Double
Private mOperationContractStartedAt As Double
Private mOperationStateStartedAt As Double
Private mOperationContractSeconds As Double
Private mOperationStateSeconds As Double
Private mOperationRequestID As String
Private mOperationRequestedSettings As String
Private mOperationAppliedStatus As String
Private mOperationAppliedStateRevision As String
Private mOperationDeliveredRevision As String
Private mOperationOriginalSourceID As String
Private mOperationOriginalSourceDataset As String
Private mOperationOriginalSourceRevision As String
Private mOperationRepairAttempted As Boolean
Private mOperationSnapshotRetryCount As Long
Private mOperationPricingStateSnapshot As Variant
Private mOperationPricingStateCaptured As Boolean
Private mOperationPricingEditGeneration As Long
Private mOperationSavedPreviewDigest As String
Private mOperationSavedPreviewExpiresAt As String
Private mOperationSavedPreviewStateRevision As String
Private mOperationSavedPreviewSettings As String
Private mOperationSavedPreviewWarningCount As Long
Private mOperationSavedApplyRequestID As String
Private mSnapshotJobID As String
Private mSnapshotJobStatus As String
Private mSnapshotJobCode As String
Private mSnapshotWaitURL As String
Private mSnapshotEventsURL As String
Private mSnapshotJobEventsURL As String
Private mSnapshotPayloadURL As String
Private mSnapshotCancelURL As String
Private mSnapshotExpectedETag As String
Private mSnapshotRevision As String
Private mSnapshotStateRevision As String
Private mSnapshotPricingStateRevision As String
Private mSnapshotDatasetRevision As String
Private mSnapshotCompletedPages As Long
Private mSnapshotTotalPages As Long
Private mSnapshotRowCount As Long
Private mSseJobID As String
Private mSseEventsURL As String
Private mSseCSRFToken As String
Private mSseLastEventID As String
Private mSseReconnectAttempt As Long
Private mSseReconnectDelaySeconds As Long
Private mSseReconnectScheduled As Boolean
Private mSseReconnectTime As Date
Private mSseRenewSessionBeforeReconnect As Boolean
Private mSseManualStop As Boolean
Private mSseRefreshRequired As Boolean
Private mSseRefreshRevision As String
Private mRefreshAfterSiteConfirmation As Boolean
Private mEventRefreshScheduled As Boolean
Private mEventRefreshTime As Date
Private mPricingEventValidationMode As Boolean
Private mPricingEventValidationRefreshActive As Boolean
Private mPricingEventValidationRefreshScheduled As Boolean
Private mPricingEventValidationConfirmationQueued As Boolean
Private mPricingEventValidationRefreshCount As Long
Private mPricingEventValidationAppliedCount As Long
Private mWorkbookClosing As Boolean
Private mResumeRefreshAfterCancelledClose As Boolean
Private mCatalogCommitInProgress As Boolean
Private mLastOperationName As String
Private mLastOperationSucceeded As Boolean
Private mLastOperationError As String
Private mLastOperationDiagnostic As String

#If VBA7 Then
Private Declare PtrSafe Function MessageBoxW Lib "user32" ( _
    ByVal windowHandle As LongPtr, _
    ByVal messagePointer As LongPtr, _
    ByVal titlePointer As LongPtr, _
    ByVal messageType As Long) As Long
Private Declare PtrSafe Function OpenClipboard Lib "user32" ( _
    ByVal windowHandle As LongPtr) As Long
Private Declare PtrSafe Function CloseClipboard Lib "user32" () As Long
Private Declare PtrSafe Function IsClipboardFormatAvailable Lib "user32" ( _
    ByVal clipboardFormat As Long) As Long
Private Declare PtrSafe Function GetClipboardData Lib "user32" ( _
    ByVal clipboardFormat As Long) As LongPtr
Private Declare PtrSafe Function GlobalLock Lib "kernel32" ( _
    ByVal memoryHandle As LongPtr) As LongPtr
Private Declare PtrSafe Function GlobalUnlock Lib "kernel32" ( _
    ByVal memoryHandle As LongPtr) As Long
Private Declare PtrSafe Function lstrlenW Lib "kernel32" ( _
    ByVal valuePointer As LongPtr) As Long
Private Declare PtrSafe Sub CopyMemory Lib "kernel32" Alias "RtlMoveMemory" ( _
    ByVal destinationPointer As LongPtr, _
    ByVal sourcePointer As LongPtr, _
    ByVal byteCount As LongPtr)
Private Declare PtrSafe Function BCryptOpenAlgorithmProvider Lib "bcrypt.dll" ( _
    ByRef algorithmHandle As LongPtr, ByVal algorithmIdentifier As LongPtr, _
    ByVal implementation As LongPtr, ByVal flags As Long) As Long
Private Declare PtrSafe Function BCryptGetProperty Lib "bcrypt.dll" ( _
    ByVal objectHandle As LongPtr, ByVal propertyName As LongPtr, _
    ByRef outputValue As Any, ByVal outputSize As Long, _
    ByRef resultSize As Long, ByVal flags As Long) As Long
Private Declare PtrSafe Function BCryptCreateHash Lib "bcrypt.dll" ( _
    ByVal algorithmHandle As LongPtr, ByRef hashHandle As LongPtr, _
    ByRef hashObject As Any, ByVal hashObjectSize As Long, _
    ByVal secret As LongPtr, ByVal secretSize As Long, _
    ByVal flags As Long) As Long
Private Declare PtrSafe Function BCryptHashData Lib "bcrypt.dll" ( _
    ByVal hashHandle As LongPtr, ByRef inputValue As Any, _
    ByVal inputSize As Long, ByVal flags As Long) As Long
Private Declare PtrSafe Function BCryptFinishHash Lib "bcrypt.dll" ( _
    ByVal hashHandle As LongPtr, ByRef outputValue As Any, _
    ByVal outputSize As Long, ByVal flags As Long) As Long
Private Declare PtrSafe Function BCryptDestroyHash Lib "bcrypt.dll" ( _
    ByVal hashHandle As LongPtr) As Long
Private Declare PtrSafe Function BCryptCloseAlgorithmProvider Lib "bcrypt.dll" ( _
    ByVal algorithmHandle As LongPtr, ByVal flags As Long) As Long
#Else
Private Declare Function MessageBoxW Lib "user32" ( _
    ByVal windowHandle As Long, _
    ByVal messagePointer As Long, _
    ByVal titlePointer As Long, _
    ByVal messageType As Long) As Long
Private Declare Function OpenClipboard Lib "user32" ( _
    ByVal windowHandle As Long) As Long
Private Declare Function CloseClipboard Lib "user32" () As Long
Private Declare Function IsClipboardFormatAvailable Lib "user32" ( _
    ByVal clipboardFormat As Long) As Long
Private Declare Function GetClipboardData Lib "user32" ( _
    ByVal clipboardFormat As Long) As Long
Private Declare Function GlobalLock Lib "kernel32" ( _
    ByVal memoryHandle As Long) As Long
Private Declare Function GlobalUnlock Lib "kernel32" ( _
    ByVal memoryHandle As Long) As Long
Private Declare Function lstrlenW Lib "kernel32" ( _
    ByVal valuePointer As Long) As Long
Private Declare Sub CopyMemory Lib "kernel32" Alias "RtlMoveMemory" ( _
    ByVal destinationPointer As Long, _
    ByVal sourcePointer As Long, _
    ByVal byteCount As Long)
Private Declare Function BCryptOpenAlgorithmProvider Lib "bcrypt.dll" ( _
    ByRef algorithmHandle As Long, ByVal algorithmIdentifier As Long, _
    ByVal implementation As Long, ByVal flags As Long) As Long
Private Declare Function BCryptGetProperty Lib "bcrypt.dll" ( _
    ByVal objectHandle As Long, ByVal propertyName As Long, _
    ByRef outputValue As Any, ByVal outputSize As Long, _
    ByRef resultSize As Long, ByVal flags As Long) As Long
Private Declare Function BCryptCreateHash Lib "bcrypt.dll" ( _
    ByVal algorithmHandle As Long, ByRef hashHandle As Long, _
    ByRef hashObject As Any, ByVal hashObjectSize As Long, _
    ByVal secret As Long, ByVal secretSize As Long, _
    ByVal flags As Long) As Long
Private Declare Function BCryptHashData Lib "bcrypt.dll" ( _
    ByVal hashHandle As Long, ByRef inputValue As Any, _
    ByVal inputSize As Long, ByVal flags As Long) As Long
Private Declare Function BCryptFinishHash Lib "bcrypt.dll" ( _
    ByVal hashHandle As Long, ByRef outputValue As Any, _
    ByVal outputSize As Long, ByVal flags As Long) As Long
Private Declare Function BCryptDestroyHash Lib "bcrypt.dll" ( _
    ByVal hashHandle As Long) As Long
Private Declare Function BCryptCloseAlgorithmProvider Lib "bcrypt.dll" ( _
    ByVal algorithmHandle As Long, ByVal flags As Long) As Long
#End If

Public Sub ValidateWorkbook()
    Dim table As ListObject
    Dim syncTable As ListObject

    ValidateUnicodeRuntime
    ValidateProposalDateNormalization
    ValidateProjectionIntegrityGuard
    ValidateRoundingRuntime
    ValidateProductNameNormalizer
    ValidateFontPolicyRuntime
    ValidateStatusSummaryFormatter
    ValidateSearchLiteralRuntime
    ValidateSearchSurfaceRuntime
    If Not ValidateProductSearchPasteForValidation() Then
        Err.Raise vbObjectError + 773, "ValidateWorkbook", T("invalid_workbook")
    End If
    ValidateAsyncComponentsRuntime
    If Not ValidatePricingWritebackUIForValidation() Then
        Err.Raise vbObjectError + 771, "ValidateWorkbook", T("invalid_workbook")
    End If
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)

    If table.Range.Row <> 5 Or table.Range.Column <> 2 Then
        Err.Raise vbObjectError + 90, "ValidateWorkbook", T("invalid_workbook")
    End If
    If table.ListColumns.Count <> PRODUCT_COLUMN_COUNT Then
        Err.Raise vbObjectError + 91, "ValidateWorkbook", T("invalid_workbook")
    End If
    If syncTable.ListColumns.Count <> SYNC_COLUMN_COUNT Then
        Err.Raise vbObjectError + 92, "ValidateWorkbook", T("invalid_workbook")
    End If
    If PriceSheet().Columns("B").ColumnWidth < 20# Or _
       PriceSheet().Columns("K").ColumnWidth < 34# Then
        Err.Raise vbObjectError + 93, "ValidateWorkbook", T("invalid_workbook")
    End If
End Sub

Private Sub ValidateSearchSurfaceRuntime()
    Dim searchAnchor As Range
    Dim searchSurface As Range
    Dim searchSheet As Worksheet

    On Error GoTo InvalidSurface
    Set searchAnchor = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange
    Set searchSurface = searchAnchor.MergeArea
    Set searchSheet = PriceSheet()
    If Not CBool(searchAnchor.MergeCells) Or _
       Not searchAnchor.Worksheet Is searchSheet Or _
       searchAnchor.Address(False, False) <> "C3" Or _
       searchSurface.Address(False, False) <> "C3:E3" Or _
       CStr(searchSurface.NumberFormat) <> "@" Then GoTo InvalidSurface
    Exit Sub

InvalidSurface:
    Err.Raise vbObjectError + 772, "ValidateSearchSurfaceRuntime", _
              T("invalid_workbook")
End Sub

Private Sub ValidateAsyncComponentsRuntime()
    Dim requestValue As AsyncWinHttpRequest
    Dim parserValue As PricingSseParser

    Set requestValue = New AsyncWinHttpRequest
    Set parserValue = New PricingSseParser
    parserValue.Reset
    If requestValue.Terminal Or requestValue.Opened Or _
       parserValue.HasFailed Or parserValue.BufferedByteCount <> 0 Then
        Err.Raise vbObjectError + 762, _
                  "ValidateAsyncComponentsRuntime", T("invalid_workbook")
    End If
End Sub

Public Function RefreshAllDataForValidation() As Boolean
    RefreshAllData True
    RefreshAllDataForValidation = Not AsyncPricingIdleForValidation()
End Function

Public Sub HandleWorkbookBeforeSave(ByVal saveAsUI As Boolean, _
                                    ByRef cancel As Boolean)
    Dim selectedPath As Variant
    Dim outputPath As String
    Dim extension As String
    Dim fileSystem As Object
    Dim answer As Long
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    If mSaveFlowActive Then Exit Sub
    If mRefreshInProgress Or mPricingActionInProgress Then
        cancel = True
        Exit Sub
    End If
    If Not saveAsUI Then
        If WorkbookIsCanonicalTemplateAtRestTarget() Then
            cancel = True
            On Error Resume Next
            ConfigSheet().Range("B6").Value2 = T("template_save_copy")
            On Error GoTo 0
            Exit Sub
        End If
        BeginWorkbookSaveTiming
        Exit Sub
    End If
    cancel = True
    selectedPath = Application.GetSaveAsFilename( _
        InitialFileName:=Application.DefaultFilePath & _
            Application.PathSeparator & _
            U("064406CC0633062A0020064206CC0645062A0020062F06CC062C06CC062A062706440627062C06CC06A9") & _
            ".xlsx", _
        FileFilter:=T("xlsx_filter") & ",*.xlsx," & _
            T("xlsm_filter") & ",*.xlsm", _
        FilterIndex:=1, _
        Title:=T("save_title"))
    If VarType(selectedPath) = vbBoolean Then Exit Sub

    outputPath = Trim$(CStr(selectedPath))
    extension = LCase$(Mid$(outputPath, InStrRev(outputPath, ".")))
    If extension = vbNullString Then
        outputPath = outputPath & ".xlsx"
        extension = ".xlsx"
    End If
    If extension <> ".xlsx" And extension <> ".xlsm" Then
        On Error Resume Next
        ConfigSheet().Range("B6").Value2 = T("save_extension")
        On Error GoTo 0
        Exit Sub
    End If

    Set fileSystem = CreateObject("Scripting.FileSystemObject")
    If fileSystem.FileExists(outputPath) Then
        answer = ConfirmUnicodeMessage( _
            T("save_overwrite"), T("save_title"))
        If answer <> vbYes Then Exit Sub
    End If

    If extension = ".xlsx" Then
        BeginWorkbookSaveTiming
        ExportMacroFreeCopy outputPath
        FinishWorkbookSaveTiming True
        On Error Resume Next
        ConfigSheet().Range("B6").Value2 = T("save_nomacro_done")
        On Error GoTo SaveFailed
    Else
        BeginWorkbookSaveTiming
        mSaveFlowActive = True
        On Error GoTo SaveFailed
        SuspendQualifiedSchedulesForSaveAs
        ThisWorkbook.SaveAs Filename:=outputPath, _
            FileFormat:=xlOpenXMLWorkbookMacroEnabled
        ResumeQualifiedSchedulesAfterSaveAs
        mSaveFlowActive = False
    End If
    Exit Sub

SaveFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    On Error Resume Next
    ResumeQualifiedSchedulesAfterSaveAs
    On Error GoTo 0
    mSaveFlowActive = False
    FinishWorkbookSaveTiming False
    On Error Resume Next
    ConfigSheet().Range("B6").Value2 = _
        SafeStatusError(savedErrorDescription)
    On Error GoTo 0
    Err.Raise savedErrorNumber, "HandleWorkbookBeforeSave", _
              savedErrorDescription
End Sub

Private Function WorkbookIsCanonicalTemplateAtRestTarget() As Boolean
    On Error GoTo NotCanonicalTemplate
    If ThisWorkbook.FileFormat <> xlOpenXMLTemplateMacroEnabled Then Exit Function
    If Len(Trim$(ThisWorkbook.Path)) = 0 Then Exit Function
    WorkbookIsCanonicalTemplateAtRestTarget = True
NotCanonicalTemplate:
End Function

Private Function CanonicalTemplateHasPendingExternalWrite() As Boolean
    If mOperationKind = "apply" Or mOperationKind = "apply_refresh" Then
        CanonicalTemplateHasPendingExternalWrite = True
        Exit Function
    End If
    If Not mWritebackRequest Is Nothing Then
        CanonicalTemplateHasPendingExternalWrite = True
        Exit Function
    End If
    If Len(mWritebackStage) > 0 Or _
       Len(mPendingConfirmationTransactionID) > 0 Then
        CanonicalTemplateHasPendingExternalWrite = True
        Exit Function
    End If
    If Not mWritebackPending Is Nothing Then
        CanonicalTemplateHasPendingExternalWrite = (mWritebackPending.Count > 0)
    End If
End Function

Public Function PrepareCanonicalTemplateForClose() As Boolean
    PrepareCanonicalTemplateForClose = True
    If Not WorkbookIsCanonicalTemplateAtRestTarget() Then Exit Function
    If CanonicalTemplateHasPendingExternalWrite() Then
        PrepareCanonicalTemplateForClose = False
        On Error Resume Next
        ConfigSheet().Range("B6").Value2 = T("template_close_wait")
        On Error GoTo 0
        Exit Function
    End If

    ' Runtime catalog rows belong only to the live session. Marking the directly
    ' opened canonical template clean makes close discard them without a save
    ' prompt; the on-disk .xltm remains the verified empty template.
    ThisWorkbook.Saved = True
End Function

Public Sub BeginWorkbookSaveTiming()
    If mSaveTimingActive Then Exit Sub
    mSaveTimingStartedAt = PhaseTimestamp()
    mSaveTimingActive = True
End Sub

Public Sub FinishWorkbookSaveTiming(ByVal success As Boolean)
    Dim elapsedSeconds As Double

    If Not mSaveTimingActive Then Exit Sub
    elapsedSeconds = PhaseElapsed(mSaveTimingStartedAt)
    mSaveTimingActive = False
    On Error Resume Next
    ConfigSheet().Range("B55").Value2 = elapsedSeconds
    If Not success Then ConfigSheet().Range("B55").Font.Color = RGB(156, 0, 6)
    ' Workbook_AfterSave records telemetry after Excel has committed the file.
    ' That bookkeeping must not make an otherwise successful save look dirty or
    ' trigger a second close prompt. The persisted workbook was already written;
    ' B55 remains runtime-only timing evidence until the next intentional save.
    If success Then ThisWorkbook.Saved = True
    On Error GoTo 0
End Sub

Private Sub ExportMacroFreeCopy(ByVal outputPath As String)
    Dim copyBook As Workbook
    Dim syncSheetValue As Worksheet
    Dim syncSheetName As String
    Dim originalSyncVisibility As XlSheetVisibility
    Dim syncVisibilityChanged As Boolean
    Dim sourceWasSaved As Boolean
    Dim previousAlerts As Boolean
    Dim previousEvents As Boolean
    Dim previousScreenUpdating As Boolean
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo Failed
    previousAlerts = Application.DisplayAlerts
    previousEvents = Application.EnableEvents
    previousScreenUpdating = Application.ScreenUpdating
    Application.DisplayAlerts = False
    Application.EnableEvents = False
    Application.ScreenUpdating = False

    Set syncSheetValue = SyncSheet()
    syncSheetName = syncSheetValue.Name
    originalSyncVisibility = syncSheetValue.Visible
    sourceWasSaved = ThisWorkbook.Saved
    If originalSyncVisibility <> xlSheetVisible Then
        syncSheetValue.Visible = xlSheetVisible
        syncVisibilityChanged = True
    End If

    ' Excel omits xlSheetVeryHidden worksheets when copying the collection.
    ' Briefly expose SyncData for the copy, then restore both source and copy.
    ThisWorkbook.Worksheets.Copy
    Set copyBook = ActiveWorkbook
    If syncVisibilityChanged Then
        syncSheetValue.Visible = originalSyncVisibility
        If syncSheetValue.Visible <> originalSyncVisibility Then
            Err.Raise vbObjectError + 2182, "ExportMacroFreeCopy", _
                      "SyncData visibility could not be restored."
        End If
        syncVisibilityChanged = False
    End If
    copyBook.Worksheets(syncSheetName).Visible = originalSyncVisibility
    RemoveMacroOnlyUI copyBook
    copyBook.SaveAs Filename:=outputPath, FileFormat:=xlOpenXMLWorkbook
    copyBook.Close SaveChanges:=False
    Set copyBook = Nothing

CleanExit:
    Application.DisplayAlerts = previousAlerts
    Application.EnableEvents = previousEvents
    Application.ScreenUpdating = previousScreenUpdating
    If sourceWasSaved And Not syncVisibilityChanged Then _
        ThisWorkbook.Saved = True
    If savedErrorNumber <> 0 Then
        Err.Raise savedErrorNumber, "ExportMacroFreeCopy", _
                  savedErrorDescription
    End If
    Exit Sub

Failed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    On Error Resume Next
    If syncVisibilityChanged And Not syncSheetValue Is Nothing Then
        syncSheetValue.Visible = originalSyncVisibility
        If syncSheetValue.Visible = originalSyncVisibility Then _
            syncVisibilityChanged = False
    End If
    If Not copyBook Is Nothing Then copyBook.Close SaveChanges:=False
    Set copyBook = Nothing
    Set syncSheetValue = Nothing
    On Error GoTo 0
    Resume CleanExit
End Sub

Private Sub RemoveMacroOnlyUI(ByVal book As Workbook)
    Dim sheet As Worksheet
    Dim shapeIndex As Long
    Dim macroName As String

    For Each sheet In book.Worksheets
        For shapeIndex = sheet.Shapes.Count To 1 Step -1
            macroName = vbNullString
            On Error Resume Next
            macroName = CStr(sheet.Shapes(shapeIndex).OnAction)
            On Error GoTo 0
            If Len(Trim$(macroName)) > 0 Then
                sheet.Shapes(shapeIndex).Delete
            End If
        Next shapeIndex
    Next sheet

    On Error Resume Next
    book.Worksheets(3).Range("G30").Value2 = 0
    book.Worksheets(3).Range("G48").Value2 = 0
    book.Worksheets(1).ListObjects(PRODUCTS_TABLE).DataBodyRange.Calculate
    book.Worksheets(1).Range("B3:K3").ClearContents
    book.Worksheets(1).ListObjects(PRODUCTS_TABLE). _
        DataBodyRange.FormatConditions.Delete
    book.Worksheets(3).Range("A44:F44").ClearContents
    book.Worksheets(3).Range("B44").Validation.Delete
    book.Names("ProductSearchQuery").Delete
    book.Names("SelectedProductRow").Delete
    book.Names("ProjectedPricePreviewRow").Delete
    book.Names("PriceDisplayFaNum").Delete
    On Error GoTo 0
End Sub

Private Sub SuspendQualifiedSchedulesForSaveAs()
    If mSaveRenameSchedulesSuspended Then Exit Sub
    mSaveRenameRefreshPending = mRefreshScheduled
    mSaveRenameAsyncPending = mAsyncDispatchScheduled
    If Not mAsyncDispatchPending Is Nothing Then
        If mAsyncDispatchPending.Count > 0 Then _
            mSaveRenameAsyncPending = True
    End If
    mSaveRenamePreviewPending = mPricingPreviewScheduled
    mSaveRenameImagePreviewPending = mImagePreviewScheduled
    mSaveRenameWritebackPending = mWritebackScheduled
    If Not mWritebackPending Is Nothing Then
        If mWritebackPending.Count > 0 Or Len(mWritebackStage) > 0 Then _
            mSaveRenameWritebackPending = True
    End If
    mSaveRenameEventRefreshPending = mEventRefreshScheduled
    mSaveRenameSseReconnectPending = mSseReconnectScheduled
    mSaveRenameSseRenewSession = mSseRenewSessionBeforeReconnect
    mSaveRenameSchedulesSuspended = True

    CancelScheduledRefresh
    UnscheduleQueuedAsyncDispatch
    CancelScheduledPricingPreview False
    CancelScheduledProductImagePreview
    UnschedulePricingWriteback
    CancelEventDrivenRefresh
    If mSseReconnectScheduled Then
        On Error Resume Next
        Application.OnTime EarliestTime:=mSseReconnectTime, _
            Procedure:=QualifiedWorkbookMacro("RunSseReconnect"), _
            Schedule:=False
        On Error GoTo 0
        mSseReconnectScheduled = False
    End If
End Sub

Private Sub ResumeQualifiedSchedulesAfterSaveAs()
    Dim refreshPending As Boolean
    Dim asyncPending As Boolean
    Dim previewPending As Boolean
    Dim imagePreviewPending As Boolean
    Dim writebackPending As Boolean
    Dim eventRefreshPending As Boolean
    Dim sseReconnectPending As Boolean
    Dim sseRenewSession As Boolean

    If Not mSaveRenameSchedulesSuspended Then Exit Sub
    refreshPending = mSaveRenameRefreshPending
    asyncPending = mSaveRenameAsyncPending
    previewPending = mSaveRenamePreviewPending
    imagePreviewPending = mSaveRenameImagePreviewPending
    writebackPending = mSaveRenameWritebackPending
    eventRefreshPending = mSaveRenameEventRefreshPending
    sseReconnectPending = mSaveRenameSseReconnectPending
    sseRenewSession = mSaveRenameSseRenewSession
    If Not mAsyncDispatchPending Is Nothing Then
        If mAsyncDispatchPending.Count > 0 Then asyncPending = True
    End If
    mSaveRenameRefreshPending = False
    mSaveRenameAsyncPending = False
    mSaveRenamePreviewPending = False
    mSaveRenameImagePreviewPending = False
    mSaveRenameWritebackPending = False
    mSaveRenameEventRefreshPending = False
    mSaveRenameSseReconnectPending = False
    mSaveRenameSseRenewSession = False
    mSaveRenameSchedulesSuspended = False
    If mWorkbookClosing Then Exit Sub

    On Error Resume Next
    If refreshPending Then ScheduleRefreshOnOpen
    If asyncPending Then ScheduleQueuedAsyncDispatch
    If previewPending Then SchedulePricingPreview
    If imagePreviewPending Then ScheduleProductImagePreview
    If writebackPending And mWritebackRequest Is Nothing Then _
        SchedulePricingWriteback 1
    If eventRefreshPending Then ScheduleEventDrivenRefresh
    If sseReconnectPending Then ScheduleSseReconnect sseRenewSession
    On Error GoTo 0
End Sub

Public Sub RefreshAllData(Optional ByVal silent As Boolean = False)
    ResumeAfterCancelledClose False
    If mRefreshInProgress Then Exit Sub
    If mPricingActionInProgress And Not mInternalPricingRefresh Then Exit Sub
    BeginRefreshPipeline silent, False
End Sub

Private Sub BeginRefreshPipeline(ByVal silent As Boolean, _
                                 ByVal afterApply As Boolean)
    Dim settings As Worksheet

    On Error GoTo BeginFailed
    CancelScheduledProductImagePreview
    CancelProductImagePreview True
    CancelScheduledRefresh
    CancelEventDrivenRefresh
    ConsumeSseRefreshRequirement
    If Not afterApply Then
        ResetFiniteOperationContext
        mOperationKind = "refresh"
        mOperationOriginalSourceID = mSourceID
        mOperationOriginalSourceDataset = mSourceDataset
        mOperationOriginalSourceRevision = mSourceRevision
        InvalidatePricingPreview
    Else
        mOperationKind = "apply_refresh"
    End If
    mOperationSilent = silent
    mOperationCancelRequested = False
    mRefreshCancelRequested = False
    mRefreshInProgress = True
    mLastRefreshSucceeded = False
    mOperationStartedAt = PhaseTimestamp()
    mOperationContractStartedAt = mOperationStartedAt
    mOperationPricingStateCaptured = False
    mPricingCSRFToken = vbNullString
    mSessionSeconds = 0#
    mStatePageTimingText = vbNullString
    ResetProductSearchState False
    CaptureOperationStatusBar
    CaptureOperationCancelKey
    RegisterRefreshCancelHotkey

    Set settings = ConfigSheet()
    mOperationPricingStateSnapshot = CapturePricingStateSnapshot(settings)
    mOperationPricingEditGeneration = mPricingEditGeneration
    mOperationPricingStateCaptured = True
    settings.Range("G30").Value2 = 0
    settings.Range("G48").Value2 = 0
    PreserveSearchLiteral
    SetRefreshProgress "1/4"
    BeginContractRequest "contract"
    Exit Sub

BeginFailed:
    FailActiveOperation Err.Number, "BeginRefreshPipeline", Err.Description
End Sub

Private Sub CommitRefreshSnapshot(ByVal reconciledRows As Object, _
                                  ByVal state As JsonValue, _
                                  ByVal catalog As JsonValue, _
                                  ByVal datasetRevision As String, _
                                  ByVal sourceRevision As String, _
                                  ByVal countSignature As String, _
                                  ByVal expectedRows As Long)
    Dim previousCalculation As XlCalculation
    Dim previousScreenUpdating As Boolean
    Dim previousEnableEvents As Boolean
    Dim settings As Worksheet
    Dim productTableSnapshot As Variant
    Dim syncTableSnapshot As Variant
    Dim productTableRows As Long
    Dim syncTableRows As Long
    Dim catalogSnapshotCaptured As Boolean
    Dim catalogMutationStarted As Boolean
    Dim appStateCaptured As Boolean
    Dim productRows As Long
    Dim wooRows As Long
    Dim parity As Variant
    Dim statusText As String
    Dim calculationStartedAt As Double
    Dim calculationSeconds As Double
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo CommitFailed
    Set settings = ConfigSheet()
    previousCalculation = Application.Calculation
    previousScreenUpdating = Application.ScreenUpdating
    previousEnableEvents = Application.EnableEvents
    appStateCaptured = True
    CaptureCatalogTableSnapshot productTableSnapshot, productTableRows, _
        syncTableSnapshot, syncTableRows
    catalogSnapshotCaptured = True

    ' Network callbacks have already validated the complete immutable payload.
    ' Freeze Excel only for this short, rollback-protected atomic commit.
    mCatalogCommitInProgress = True
    ReleaseSearchEnterHotkey
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual
    catalogMutationStarted = True
    settings.Range("G31:G47").ClearContents
    settings.Range("G31").Value2 = False
    ApplyGlobalState state
    ApplyReconciliationCounts catalog
    If expectedRows <> CLng(Val(CStr(settings.Range("G38").Value2))) Then
        Err.Raise vbObjectError + 130, "CommitRefreshSnapshot", _
                  T("invalid_workbook")
    End If
    settings.Range("G44").Value2 = datasetRevision
    settings.Range("G45").Value2 = sourceRevision
    settings.Range("G46").Value2 = expectedRows
    settings.Range("G47").Value2 = countSignature
    settings.Range("G56").Value2 = mSnapshotStateRevision

    InvalidateProductSearchIndex
    productRows = ImportReconciledCatalog(reconciledRows)
    EnforceConfiguredFontsAfterRefresh
    If productRows <> expectedRows Then
        Err.Raise vbObjectError + 115, "CommitRefreshSnapshot", _
                  T("invalid_workbook")
    End If
    wooRows = CLng(Val(CStr(settings.Range("G32").Value2)))
    SetRefreshProgress "4/4"
    calculationStartedAt = PhaseTimestamp()
    CalculateRefreshedWorkbook
    calculationSeconds = PhaseElapsed(calculationStartedAt)
    parity = PriceParitySummary()

    If mOperationKind = "apply_refresh" Then
        If Trim$(CStr(settings.Range("G14").Value2)) <> _
               mOperationAppliedStateRevision Or _
           StrictPriceParityMismatchCount() <> 0 Then
            Err.Raise vbObjectError + 162, "CommitRefreshSnapshot", _
                      T("sync_failed")
        End If
    End If

    statusText = FormatNonzeroStatusSummary(productRows, wooRows, _
        CLng(settings.Range("G34").Value2), _
        CLng(settings.Range("G35").Value2), _
        CLng(settings.Range("G36").Value2), _
        CLng(parity(0)), CLng(parity(1)))
    settings.Range("B46:F54").ClearContents
    settings.Range("B46").Value2 = mSessionSeconds
    settings.Range("B47").Value2 = mOperationContractSeconds
    settings.Range("B48").Value2 = mOperationStateSeconds
    settings.Range("B49").NumberFormat = "@"
    settings.Range("B49").Value2 = mStatePageTimingText
    settings.Range("B50").Value2 = mReconcileSeconds
    settings.Range("B51").Value2 = mPricingSeconds
    settings.Range("B52").Value2 = mTableWriteSeconds
    settings.Range("B53").Value2 = mFormattingSeconds
    settings.Range("B54").Value2 = calculationSeconds
    If CBool(settings.Range("G15").Value2) Then
        statusText = statusText & U("061B") & " " & T("stale_rate")
    End If
    If CBool(settings.Range("G40").Value2) Then
        statusText = statusText & U("061B") & " " & _
            T("proposal_drift_critical")
    ElseIf CBool(settings.Range("G39").Value2) Then
        statusText = statusText & U("061B") & " " & T("proposal_drift")
    End If
    If CBool(settings.Range("G31").Value2) Then
        statusText = statusText & U("061B") & " " & T("identity_warning")
    End If
    settings.Range("B6").Value = statusText
    settings.Range("B7").Value = Now
    settings.Range("B7").NumberFormat = "yyyy-mm-dd hh:mm"
    mLastRefreshSucceeded = True

CommitExit:
    On Error Resume Next
    If appStateCaptured Then
        Application.Calculation = previousCalculation
        Application.EnableEvents = previousEnableEvents
        Application.ScreenUpdating = previousScreenUpdating
    End If
    mCatalogCommitInProgress = False
    RefreshSearchEnterHotkey
    On Error GoTo 0
    If savedErrorNumber = 0 And _
       Len(mPendingConfirmationTransactionID) > 0 And _
       Len(mWritebackStage) = 0 Then
        EnsureWritebackQueue
        mWritebackPending("site_confirmation") = "B18"
        MarkWritebackState "site_confirmation", "sending", _
            U("062A063A06CC06CC06310020064806280633062706CC062A0020062F06310020062706A9063306440020062A062706CC06CC062F00200634062F061B0020062F0631002006270646062A0638062706310020062B0628062A002006460647062706CC06CC002006480628200C0633062706CC062A002006270633062A002E")
        SchedulePricingWriteback 1
    End If
    If savedErrorNumber <> 0 Then
        Err.Raise savedErrorNumber, "CommitRefreshSnapshot", _
                  savedErrorDescription
    End If
    Exit Sub

CommitFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    On Error Resume Next
    If mOperationPricingStateCaptured Then
        RestorePricingStateSnapshot settings, mOperationPricingStateSnapshot
    End If
    If catalogSnapshotCaptured And catalogMutationStarted Then
        RestoreCatalogTableSnapshot productTableSnapshot, productTableRows, _
            syncTableSnapshot, syncTableRows
    End If
    On Error GoTo 0
    Resume CommitExit
End Sub

Public Sub RefreshOnOpen()
    ScheduleRefreshOnOpen
End Sub

Public Sub ScheduleRefreshOnOpen()
    On Error GoTo ScheduleFailed
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameRefreshPending = True
        Exit Sub
    End If
    mRefreshScheduleFailed = False
    If Trim$(CStr(ConfigSheet().Range("B5").Value2)) = U("062806440647") Then
        If HasCoherentLocalCatalog() Then
            SetOperationProgressSurface "listener_wait", -1, _
                U("062F06310020062D062706440020062806310631063306CC0020062A063A06CC06CC06310627062A0020062A0627063206470020062F06310020067E0633200C0632064506CC06460647061B00200641064706310633062A002006410639064406CC00200642062706270644002006270633062A06410627062F0647002006270633062A2026"), _
                "active", True
            Exit Sub
        End If
        If mEventRefreshScheduled Then Exit Sub
        CancelScheduledRefresh
        mScheduledRefreshTime = Now + _
            TimeSerial(0, 0, OPEN_REFRESH_DELAY_SECONDS)
        mRefreshScheduled = True
        Application.OnTime _
            EarliestTime:=mScheduledRefreshTime, _
            Procedure:=QualifiedWorkbookMacro("RunScheduledRefresh"), _
            Schedule:=True
        mRefreshScheduleFailed = False
    End If
    Exit Sub

ScheduleFailed:
    mRefreshScheduled = False
    mRefreshScheduleFailed = True
    Err.Clear
End Sub

Private Function HasCoherentLocalCatalog() As Boolean
    Dim table As ListObject
    Dim expectedRows As Long

    On Error GoTo CleanExit
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Function
    expectedRows = CLng(Val(CStr(ConfigSheet().Range("G46").Value2)))
    If expectedRows <= 0 Or expectedRows <> table.ListRows.Count Then _
        Exit Function
    If Len(Trim$(CStr(ConfigSheet().Range("G44").Value2))) = 0 Or _
       Len(Trim$(CStr(ConfigSheet().Range("G45").Value2))) = 0 Then _
        Exit Function
    HasCoherentLocalCatalog = True
CleanExit:
End Function

Public Sub PrepareLocalSearchOnOpen()
    On Error GoTo PrepareFailed
    If Not HasCoherentLocalCatalog() Then Exit Sub
    SetOperationProgressSurface "search_index", -1, _
        U("062F06310020062D062706440020062206450627062F0647200C06330627063206CC0020062C0633062A200C0648062C064806CC00200633063106CC06392026"), _
        "active", True
    WarmProductSearchIndex
    Exit Sub
PrepareFailed:
    InvalidateProductSearchIndex
    Err.Clear
End Sub

Public Sub RunScheduledRefresh()
    mRefreshScheduled = False
    mRefreshScheduleFailed = False
    If mRefreshInProgress Then Exit Sub
    If Trim$(CStr(ConfigSheet().Range("B5").Value2)) <> _
       U("062806440647") Then Exit Sub
    RefreshAllData True
End Sub

Public Sub CancelScheduledRefresh()
    If Not mRefreshScheduled Then Exit Sub
    On Error Resume Next
    Application.OnTime _
        EarliestTime:=mScheduledRefreshTime, _
        Procedure:=QualifiedWorkbookMacro("RunScheduledRefresh"), _
        Schedule:=False
    mRefreshScheduled = False
    On Error GoTo 0
End Sub

Private Function QualifiedWorkbookMacro(ByVal procedureName As String) As String
    QualifiedWorkbookMacro = "'" & _
        Replace(ThisWorkbook.Name, "'", "''") & _
        "'!ProductCatalogSync." & procedureName
End Function

Private Sub SetRefreshProgress(ByVal phaseText As String)
    Select Case Left$(phaseText, 3)
        Case "1/4"
            SetOperationProgressSurface "refresh_request", 10, _
                U("062F0631062E064806270633062A002006280647200C063106480632063106330627064606CC0020062B0628062A00200634062F"), _
                "active", False
        Case "2/4"
            SetOperationProgressSurface "refresh_wait", -1, _
                U("062F0631002006270646062A0638062706310020062206450627062F0647200C06330627063206CC00200645064606280639"), _
                "active", False
        Case "3/4"
            SetOperationProgressSurface "refresh_validate", 70, _
                U("062F06310020062D06270644002006270639062A06280627063106330646062C06CC0020062F0627062F0647200C06470627"), _
                "active", False
        Case "4/4"
            SetOperationProgressSurface "refresh_apply", 90, _
                U("062F06310020062D0627064400200627063906450627064400200631062F06CC0641200C0647062700200648002006410631064506480644200C06470627"), _
                "active", False
        Case Else
            SetOperationProgressSurface "refresh", -1, phaseText, _
                "active", False
    End Select
    PumpExcelMessages
End Sub

Public Sub InitializeOperationProgress()
    CancelOperationProgressReset
    SetOperationProgressSurface "ready", 0, _
        U("062206450627062F0647"), "neutral", False
End Sub

Public Sub SetOperationProgressSurface(ByVal stageName As String, _
                                       ByVal percentValue As Long, _
                                       ByVal messageText As String, _
                                       ByVal visualState As String, _
                                       Optional ByVal scheduleReset As Boolean = False)
    Dim displayText As String
    Dim settings As Worksheet
    Dim previousEvents As Boolean

    If percentValue > 100 Then percentValue = 100
    If percentValue < -1 Then percentValue = -1
    If visualState = "neutral" Then
        displayText = messageText
    ElseIf percentValue < 0 Then
        displayText = messageText
    Else
        displayText = CStr(percentValue) & "% - " & messageText
    End If
    On Error Resume Next
    UpdateOperationProgressShapes PriceSheet(), stageName, percentValue, _
        displayText, visualState
    UpdateOperationProgressShapes ConfigSheet(), stageName, percentValue, _
        displayText, visualState
    Set settings = ConfigSheet()
    previousEvents = Application.EnableEvents
    Application.EnableEvents = False
    settings.Range("B6").Value2 = displayText
    Application.EnableEvents = previousEvents
    If visualState = "neutral" Then
        Application.StatusBar = False
    Else
        Application.StatusBar = displayText
    End If
    On Error GoTo 0
    If scheduleReset Then ScheduleOperationProgressReset
End Sub

Private Sub UpdateOperationProgressShapes(ByVal targetSheet As Worksheet, _
                                          ByVal stageName As String, _
                                          ByVal percentValue As Long, _
                                          ByVal displayText As String, _
                                          ByVal visualState As String)
    Dim track As Shape
    Dim fill As Shape
    Dim label As Shape
    Dim fillColor As Long
    Dim targetWidth As Double

    Set track = targetSheet.Shapes(PROGRESS_TRACK_SHAPE)
    Set fill = targetSheet.Shapes(PROGRESS_FILL_SHAPE)
    Set label = targetSheet.Shapes(PROGRESS_TEXT_SHAPE)
    Select Case LCase$(visualState)
        Case "pending": fillColor = RGB(245, 158, 11)
        Case "confirmed": fillColor = RGB(22, 163, 74)
        Case "failed": fillColor = RGB(220, 38, 38)
        Case "warning": fillColor = RGB(234, 179, 8)
        Case Else: fillColor = RGB(37, 99, 235)
    End Select
    fill.Left = track.Left
    fill.Top = track.Top
    fill.Height = track.Height
    If visualState = "neutral" Then
        track.Visible = msoFalse
        fill.Visible = msoFalse
        label.Visible = msoFalse
    Else
        track.Visible = msoTrue
        fill.Visible = msoTrue
        label.Visible = msoTrue
        If percentValue < 0 Then
            targetWidth = track.Width * 0.22
        Else
            targetWidth = track.Width * (percentValue / 100#)
        End If
        If targetWidth < 2# Then targetWidth = 2#
        fill.Width = targetWidth
        fill.Fill.ForeColor.RGB = fillColor
    End If
    label.TextFrame2.TextRange.Text = displayText
    label.TextFrame2.TextRange.Font.Name = "Yekan Bakh"
    label.TextFrame2.TextRange.Font.NameComplexScript = "Yekan Bakh"
    label.TextFrame2.TextRange.Font.NameFarEast = "Yekan Bakh"
    label.AlternativeText = "stage=" & stageName & "; percent=" & _
        CStr(percentValue) & "; state=" & visualState & "; message=" & _
        displayText
End Sub

Private Sub ScheduleOperationProgressReset()
    CancelOperationProgressReset
    If mWorkbookClosing Then Exit Sub
    mProgressResetTime = Now + TimeSerial(0, 0, PROGRESS_RESET_SECONDS)
    Application.OnTime EarliestTime:=mProgressResetTime, _
        Procedure:=QualifiedWorkbookMacro("ResetOperationProgressSurface"), _
        Schedule:=True
    mProgressResetScheduled = True
End Sub

Public Sub CancelOperationProgressReset()
    If Not mProgressResetScheduled Then Exit Sub
    On Error Resume Next
    Application.OnTime EarliestTime:=mProgressResetTime, _
        Procedure:=QualifiedWorkbookMacro("ResetOperationProgressSurface"), _
        Schedule:=False
    mProgressResetScheduled = False
    On Error GoTo 0
End Sub

Public Sub ResetOperationProgressSurface()
    mProgressResetScheduled = False
    InitializeOperationProgress
End Sub

Private Sub CalculateRefreshedWorkbook()
    PriceSheet().Calculate
    PumpExcelMessages
    ConfigSheet().Calculate
    PumpExcelMessages
    ThisWorkbook.Worksheets(2).Calculate
    PumpExcelMessages
End Sub

Private Sub PumpExcelMessages()
    If mRefreshInProgress And mRefreshCancelRequested Then
        Err.Raise vbObjectError + 239, "PumpExcelMessages", _
                  T("sync_retry")
    End If
End Sub

Private Sub RegisterRefreshCancelHotkey()
    ' Excel does not expose the previous OnKey target. Keep this override
    ' scoped strictly to an active refresh and always release it in cleanup.
    Application.OnKey "{ESC}", _
        QualifiedWorkbookMacro("RequestRefreshCancel")
    mRefreshCancelHotkeyRegistered = True
End Sub

Private Sub ReleaseRefreshCancelHotkey()
    If Not mRefreshCancelHotkeyRegistered Then Exit Sub
    Application.OnKey "{ESC}"
    mRefreshCancelHotkeyRegistered = False
End Sub

Public Sub RequestRefreshCancel()
    If Not mRefreshInProgress And Not mPricingActionInProgress Then Exit Sub
    mRefreshCancelRequested = True
    mOperationCancelRequested = True
    CancelActivePricingOperations False
End Sub

Public Sub CancelActivePricingOperations( _
        Optional ByVal workbookIsClosing As Boolean = False)
    Dim cancellationMessage As String

    If workbookIsClosing Then
        mResumeRefreshAfterCancelledClose = _
            (mResumeRefreshAfterCancelledClose Or _
             Len(mOperationKind) > 0 Or mRefreshScheduled Or _
             mEventRefreshScheduled Or mSseReconnectScheduled Or _
             Len(mSseEventsURL) > 0 Or Not mSseRequest Is Nothing Or _
             Not mSseSessionRequest Is Nothing)
        mWorkbookClosing = True
        CancelQueuedAsyncDispatch
        CancelScheduledProductImagePreview
        CancelProductImagePreview True
    End If
    CancelScheduledRefresh
    CancelScheduledPricingPreview True
    UnschedulePricingWriteback
    CancelEventDrivenRefresh
    If workbookIsClosing Then CancelSseReconnect
    mOperationCancelRequested = True
    mRefreshCancelRequested = True
    cancellationMessage = T("sync_retry")

    On Error Resume Next
    If Len(mSnapshotCancelURL) > 0 And Len(mPricingCSRFToken) > 0 Then
        BeginBestEffortSnapshotCancel mSnapshotCancelURL, mPricingCSRFToken
    End If
    If Not mOperationRequest Is Nothing Then mOperationRequest.Abort
    If workbookIsClosing And Not mWritebackRequest Is Nothing Then
        mWritebackRequest.Abort
        Set mWritebackRequest = Nothing
    End If
    If workbookIsClosing And Not mCancelRequest Is Nothing Then
        mCancelRequest.Abort
        Set mCancelRequest = Nothing
    End If
    If workbookIsClosing Then StopSseListener True
    On Error GoTo 0

    If Len(mOperationKind) > 0 Then
        FailActiveOperation vbObjectError + 239, _
            "CancelActivePricingOperations", cancellationMessage
    Else
        mRefreshInProgress = False
        mPricingActionInProgress = False
        mInternalPricingRefresh = False
        ReleaseRefreshCancelHotkey
        RestoreOperationCancelKey
        RestoreOperationStatusBar
        If Not workbookIsClosing Then
            SetOperationProgressSurface "cancelled", 0, _
                U("06280647200C063106480632063106330627064606CC00200644063A064800200634062F"), _
                "warning", True
        End If
    End If
End Sub

Public Sub ResumeAfterCancelledClose( _
        Optional ByVal scheduleRefresh As Boolean = True)
    Dim shouldRefresh As Boolean

    If Not mWorkbookClosing Then Exit Sub
    shouldRefresh = mResumeRefreshAfterCancelledClose
    mResumeRefreshAfterCancelledClose = False
    mWorkbookClosing = False
    mOperationCancelRequested = False
    mRefreshCancelRequested = False
    mSseManualStop = False
    RegisterSearchHotkey
    If shouldRefresh And scheduleRefresh Then
        mForceFreshSnapshot = True
        ScheduleEventDrivenRefresh
    End If
    If Not mWritebackPending Is Nothing Then
        If mWritebackPending.Count > 0 And mWritebackRequest Is Nothing Then _
            SchedulePricingWriteback 1
    End If
End Sub

Private Sub ResetFiniteOperationContext()
    Set mOperationRequest = Nothing
    mOperationStage = vbNullString
    mOperationKind = vbNullString
    mOperationSilent = False
    mOperationShowMessage = False
    mOperationRequireConfirmation = False
    mOperationCancelRequested = False
    mOperationFinishing = False
    mOperationStartedAt = 0#
    mOperationContractStartedAt = 0#
    mOperationStateStartedAt = 0#
    mOperationContractSeconds = 0#
    mOperationStateSeconds = 0#
    mOperationRequestID = vbNullString
    mOperationRequestedSettings = vbNullString
    mOperationAppliedStatus = vbNullString
    mOperationAppliedStateRevision = vbNullString
    mOperationDeliveredRevision = vbNullString
    mOperationOriginalSourceID = vbNullString
    mOperationOriginalSourceDataset = vbNullString
    mOperationOriginalSourceRevision = vbNullString
    mOperationRepairAttempted = False
    mOperationSnapshotRetryCount = 0
    mOperationPricingStateSnapshot = Empty
    mOperationPricingStateCaptured = False
    mOperationSavedPreviewDigest = vbNullString
    mOperationSavedPreviewExpiresAt = vbNullString
    mOperationSavedPreviewStateRevision = vbNullString
    mOperationSavedPreviewSettings = vbNullString
    mOperationSavedPreviewWarningCount = 0
    mOperationSavedApplyRequestID = vbNullString
    mSnapshotJobID = vbNullString
    mSnapshotJobStatus = vbNullString
    mSnapshotJobCode = vbNullString
    mSnapshotWaitURL = vbNullString
    mSnapshotEventsURL = vbNullString
    mSnapshotJobEventsURL = vbNullString
    mSnapshotPayloadURL = vbNullString
    mSnapshotCancelURL = vbNullString
    mSnapshotExpectedETag = vbNullString
    mSnapshotRevision = vbNullString
    mSnapshotStateRevision = vbNullString
    mSnapshotDatasetRevision = vbNullString
    mSnapshotCompletedPages = 0
    mSnapshotTotalPages = 0
    mSnapshotRowCount = 0
End Sub

Private Sub CaptureOperationCancelKey()
    If mOperationCancelKeyCaptured Then Exit Sub
    mOperationPreviousEnableCancelKey = Application.EnableCancelKey
    mOperationCancelKeyCaptured = True
    Application.EnableCancelKey = xlErrorHandler
End Sub

Private Sub RestoreOperationCancelKey()
    If Not mOperationCancelKeyCaptured Then Exit Sub
    On Error Resume Next
    Application.EnableCancelKey = mOperationPreviousEnableCancelKey
    On Error GoTo 0
    mOperationCancelKeyCaptured = False
End Sub

Private Sub CaptureOperationStatusBar()
    If mOperationStatusBarCaptured Then Exit Sub
    mOperationPreviousStatusBar = Application.StatusBar
    mOperationStatusBarCaptured = True
End Sub

Private Sub RestoreOperationStatusBar()
    If Not mOperationStatusBarCaptured Then Exit Sub
    On Error Resume Next
    Application.StatusBar = mOperationPreviousStatusBar
    On Error GoTo 0
    mOperationPreviousStatusBar = Empty
    mOperationStatusBarCaptured = False
End Sub

Private Function NextAsyncToken() As Long
    If mAsyncTokenCounter >= 2147483000 Then
        mAsyncTokenCounter = 1
    Else
        mAsyncTokenCounter = mAsyncTokenCounter + 1
        If mAsyncTokenCounter < 1 Then mAsyncTokenCounter = 1
    End If
    NextAsyncToken = mAsyncTokenCounter
End Function

Public Sub QueueAsyncDispatch(ByVal requestToken As Long)
    If requestToken < 1 Or mWorkbookClosing Then Exit Sub
    If mAsyncDispatchPending Is Nothing Then
        Set mAsyncDispatchPending = CreateObject("Scripting.Dictionary")
        mAsyncDispatchPending.CompareMode = vbBinaryCompare
    End If
    mAsyncDispatchPending(CStr(requestToken)) = True
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameAsyncPending = True
        Exit Sub
    End If
    If mAsyncDispatchActive Or mAsyncDispatchScheduled Then Exit Sub

    ScheduleQueuedAsyncDispatch
End Sub

Private Sub ScheduleQueuedAsyncDispatch()
    If mWorkbookClosing Or mAsyncDispatchScheduled Then Exit Sub
    If mAsyncDispatchPending Is Nothing Then Exit Sub
    If mAsyncDispatchPending.Count = 0 Then Exit Sub
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameAsyncPending = True
        Exit Sub
    End If

    On Error GoTo DispatchFailed
    mAsyncDispatchTime = Now + TimeSerial(0, 0, 1)
    mAsyncDispatchScheduled = True
    Application.OnTime EarliestTime:=mAsyncDispatchTime, _
        Procedure:=QualifiedWorkbookMacro("DispatchQueuedAsyncRequests"), _
        Schedule:=True
    Exit Sub

DispatchFailed:
    mAsyncDispatchErrorNumber = Err.Number
    mAsyncDispatchErrorDescription = Err.Description
    mAsyncDispatchScheduled = False
    Err.Clear
End Sub

Public Sub KickQueuedAsyncDispatch()
    Dim hasPendingDispatch As Boolean

    If mWorkbookClosing Or mSaveRenameSchedulesSuspended Then Exit Sub
    If Not mAsyncDispatchPending Is Nothing Then
        hasPendingDispatch = (mAsyncDispatchPending.Count > 0 Or _
                              mAsyncDispatchErrorNumber <> 0)
    End If

    ' This entrypoint is called only from normal Excel workbook/input events,
    ' never from a WinHTTP callback. It is the deterministic non-polling
    ' handoff if Excel transiently rejected the callback's one-shot OnTime
    ' request while another top-level macro (for example product search) was
    ' running.
    If hasPendingDispatch And Not mAsyncDispatchActive And _
       Not mAsyncDispatchScheduled Then
        If mAsyncDispatchErrorNumber <> 0 Then
            DispatchQueuedAsyncRequests
        Else
            ScheduleQueuedAsyncDispatch
        End If
    End If

    If Len(mOperationKind) = 0 Then
        If mRefreshScheduleFailed Then ScheduleRefreshOnOpen
        If mPricingPreviewQueued And Not mPricingPreviewScheduled Then _
            SchedulePricingPreview
        If mSseRefreshRequired And Not mEventRefreshScheduled Then _
            ScheduleEventDrivenRefresh
    End If
    If Not mWritebackPending Is Nothing Then
        If mWritebackPending.Count > 0 And mWritebackRequest Is Nothing And _
           Not mWritebackScheduled Then SchedulePricingWriteback 1
    End If
    If Not mSseManualStop And Len(mSseEventsURL) > 0 And _
       mSseReconnectAttempt > 0 And Not mSseReconnectScheduled And _
       mSseRequest Is Nothing And mSseSessionRequest Is Nothing Then _
        ScheduleSseReconnect mSseRenewSessionBeforeReconnect
End Sub

Public Sub DispatchQueuedAsyncRequests()
    Dim pendingToken As Long
    Dim pendingKey As Variant

    mAsyncDispatchScheduled = False
    If mWorkbookClosing Then
        CancelQueuedAsyncDispatch
        Exit Sub
    End If
    If mAsyncDispatchPending Is Nothing Then Exit Sub
    If mAsyncDispatchActive Then
        ScheduleQueuedAsyncDispatch
        Exit Sub
    End If

    On Error GoTo DispatchFailed
    mAsyncDispatchActive = True
    If mAsyncDispatchErrorNumber <> 0 Then
        ' Application.OnTime may reject a callback only because Excel is busy
        ' in another top-level macro. Reaching this public entrypoint proves
        ' Excel is callable again, so keep the completed request tokens and
        ' drain them here instead of turning a transient scheduler collision
        ' into a failed refresh.
        mAsyncDispatchErrorNumber = 0
        mAsyncDispatchErrorDescription = vbNullString
    End If
    Do
        pendingToken = 0
        If mAsyncDispatchPending.Count > 0 Then
            For Each pendingKey In mAsyncDispatchPending.Keys
                pendingToken = CLng(pendingKey)
                mAsyncDispatchPending.Remove CStr(pendingKey)
                Exit For
            Next pendingKey
        End If
        If pendingToken = 0 Then Exit Do
        DispatchAsyncRequest pendingToken
    Loop

DispatchExit:
    mAsyncDispatchActive = False
    If Not mWorkbookClosing Then ScheduleQueuedAsyncDispatch
    Exit Sub

DispatchFailed:
    mAsyncDispatchActive = False
    If Len(mOperationKind) > 0 Then
        FailActiveOperation Err.Number, "DispatchQueuedAsyncRequests", _
            Err.Description
    End If
End Sub

Private Sub UnscheduleQueuedAsyncDispatch()
    If mAsyncDispatchScheduled Then
        On Error Resume Next
        Application.OnTime EarliestTime:=mAsyncDispatchTime, _
            Procedure:=QualifiedWorkbookMacro("DispatchQueuedAsyncRequests"), _
            Schedule:=False
        On Error GoTo 0
    End If
    mAsyncDispatchScheduled = False
End Sub

Private Sub CancelQueuedAsyncDispatch()
    UnscheduleQueuedAsyncDispatch
    mAsyncDispatchErrorNumber = 0
    mAsyncDispatchErrorDescription = vbNullString
    If Not mAsyncDispatchPending Is Nothing Then _
        mAsyncDispatchPending.RemoveAll
End Sub

Private Sub DispatchAsyncRequest(ByVal requestToken As Long)
    If Not mImageRequest Is Nothing Then
        If mImageRequest.Token = requestToken Then
            If mImageRequest.Terminal Then HandleProductImageTerminal
            Exit Sub
        End If
    End If
    If Not mWritebackRequest Is Nothing Then
        If mWritebackRequest.Token = requestToken Then
            If mWritebackRequest.Terminal Then HandleWritebackTerminal
            Exit Sub
        End If
    End If
    If Not mOperationRequest Is Nothing Then
        If mOperationRequest.Token = requestToken Then
            If mOperationRequest.Terminal Then HandleOperationTerminal
            Exit Sub
        End If
    End If
    If Not mSseRequest Is Nothing Then
        If mSseRequest.Token = requestToken Then
            HandleSseDispatch
            Exit Sub
        End If
    End If
    If Not mSseSessionRequest Is Nothing Then
        If mSseSessionRequest.Token = requestToken Then
            HandleSseSessionDispatch
            Exit Sub
        End If
    End If
    If Not mCancelRequest Is Nothing Then
        If mCancelRequest.Token = requestToken Then
            If mCancelRequest.Terminal Then Set mCancelRequest = Nothing
        End If
    End If
End Sub

Private Sub BeginContractRequest(ByVal stageName As String)
    Dim endpoint As String

    endpoint = Trim$(CStr(ConfigSheet().Range("B3").Value2))
    If Not IsAllowedPatrisUrl(endpoint) Then
        Err.Raise vbObjectError + 100, "BeginContractRequest", _
                  T("bridge_missing")
    End If
    mOperationStage = stageName
    StartFiniteRequest "GET", endpoint, _
        "application/vnd.patris.product-sync+json, application/json", _
        vbNullString, vbNullString, vbNullString, False, HTTP_TIMEOUT_MS
End Sub

Private Sub BeginSessionRequest()
    mOperationStage = "session"
    mOperationStateStartedAt = PhaseTimestamp()
    StartFiniteRequest "POST", PricingBaseURL() & "/session", _
        "application/json", "{}", vbNullString, vbNullString, True, _
        HTTP_TIMEOUT_MS
End Sub

Private Sub BeginSnapshotStartRequest()
    mOperationRequestID = NewRequestID("snapshot")
    mSnapshotJobID = vbNullString
    mSnapshotJobStatus = vbNullString
    mSnapshotJobCode = vbNullString
    mSnapshotWaitURL = vbNullString
    mSnapshotEventsURL = vbNullString
    mSnapshotJobEventsURL = vbNullString
    mSnapshotPayloadURL = vbNullString
    mSnapshotCancelURL = vbNullString
    mSnapshotExpectedETag = vbNullString
    mSnapshotRevision = vbNullString
    mSnapshotStateRevision = vbNullString
    mSnapshotPricingStateRevision = vbNullString
    mSnapshotDatasetRevision = vbNullString
    mSnapshotCompletedPages = 0
    mSnapshotTotalPages = 0
    mSnapshotRowCount = 0
    mOperationStage = "snapshot_start"
    SetRefreshProgress "2/4"
    StartFiniteRequest "POST", PricingBaseURL() & "/snapshots", _
        "application/json", PricingSnapshotRequestJson(mOperationRequestID), _
        mOperationRequestID, vbNullString, True, HTTP_TIMEOUT_MS
End Sub

Private Sub BeginRevisionProbeRequest()
    mOperationRequestID = NewRequestID("revision")
    mOperationStage = "revision"
    SetRefreshProgress "2/4"
    StartFiniteRequest "POST", PricingBaseURL() & "/revision", _
        "application/json", PricingRevisionRequestJson(mOperationRequestID), _
        vbNullString, vbNullString, True, HTTP_TIMEOUT_MS
End Sub

Private Sub BeginSnapshotWaitRequest()
    mOperationStage = "snapshot_wait"
    StartFiniteRequest "GET", mSnapshotWaitURL, "application/json", _
        vbNullString, vbNullString, vbNullString, True, _
        SNAPSHOT_WAIT_TIMEOUT_MS
End Sub

Private Sub BeginSnapshotPayloadRequest()
    mOperationStage = "snapshot_payload"
    StartFiniteRequest "GET", mSnapshotPayloadURL, "application/json", _
        vbNullString, vbNullString, vbNullString, True, HTTP_TIMEOUT_MS
End Sub

Private Sub BeginPreviewRequest()
    mOperationRequestID = NewRequestID("preview")
    mOperationStage = "preview"
    StartFiniteRequest "POST", PricingEndpoint("preview"), _
        "application/json", _
        BuildPricingRequest("preview", mOperationRequestID, _
            vbNullString, False), _
        mOperationRequestID, Trim$(CStr(ConfigSheet().Range("G14").Value2)), _
        True, PRICING_HTTP_TIMEOUT_MS
End Sub

Private Sub BeginApplyRequest()
    If Len(mLastApplyRequestID) = 0 Then
        mLastApplyRequestID = NewRequestID("apply")
        ConfigSheet().Range("G28").Value2 = mLastApplyRequestID
    End If
    mOperationRequestID = mLastApplyRequestID
    mOperationStage = "apply"
    StartFiniteRequest "POST", PricingEndpoint("apply"), _
        "application/json", _
        BuildPricingRequest("apply", mOperationRequestID, _
            mLastPreviewDigest, True, mLastPreviewStateRevision), _
        mOperationRequestID, mLastPreviewStateRevision, True, _
        PRICING_HTTP_TIMEOUT_MS
End Sub

Private Sub BeginRepairRequest()
    If mOperationRepairAttempted Then
        Err.Raise vbObjectError + 149, "BeginRepairRequest", _
                  T("source_sync_failed")
    End If
    mOperationRepairAttempted = True
    mOperationStage = "repair"
    StartFiniteRequest "POST", UniversalRefreshURL(), "application/json", _
        "{""delivery"":""wait""}", vbNullString, vbNullString, True, _
        PRICING_HTTP_TIMEOUT_MS
End Sub

Private Sub StartFiniteRequest(ByVal methodName As String, _
                               ByVal endpoint As String, _
                               ByVal acceptHeader As String, _
                               ByVal requestBody As String, _
                               ByVal idempotencyKey As String, _
                               ByVal expectedRevision As String, _
                               ByVal pricingRequest As Boolean, _
                               ByVal receiveTimeoutMS As Long)
    Dim requestValue As AsyncWinHttpRequest
    Dim requestToken As Long
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo StartFailed
    If Not mOperationRequest Is Nothing Then
        Err.Raise vbObjectError + 760, "StartFiniteRequest", _
                  T("sync_retry")
    End If
    If pricingRequest And Not IsAllowedPricingAuthenticatedUrl(endpoint) Then
        Err.Raise vbObjectError + 143, "StartFiniteRequest", _
                  T("bridge_missing")
    End If
    requestToken = NextAsyncToken()
    Set requestValue = New AsyncWinHttpRequest
    Set mOperationRequest = requestValue
    requestValue.OpenAsync UCase$(methodName), endpoint, requestToken, _
        mOperationStage, False, MAX_PRICING_RESPONSE_BYTES, _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, receiveTimeoutMS
    requestValue.SetRequestHeader "Accept", acceptHeader
    If pricingRequest Then
        requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
        If Len(mPricingCSRFToken) > 0 Then
            requestValue.SetRequestHeader PRICING_CSRF_HEADER, _
                mPricingCSRFToken
        End If
    End If
    If Len(idempotencyKey) > 0 Then
        requestValue.SetRequestHeader "Idempotency-Key", idempotencyKey
    End If
    If Len(expectedRevision) > 0 Then
        requestValue.SetRequestHeader "If-Match", _
            Chr$(34) & expectedRevision & Chr$(34)
    End If
    If UCase$(methodName) = "POST" Then
        requestValue.SetRequestHeader "Content-Type", _
            "application/json; charset=utf-8"
        requestValue.Send Utf8Bytes(requestBody)
    Else
        requestValue.Send
    End If
    Exit Sub

StartFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    Set mOperationRequest = Nothing
    Err.Raise savedErrorNumber, "StartFiniteRequest", _
              savedErrorDescription
End Sub

Private Sub BeginBestEffortSnapshotCancel(ByVal endpoint As String, _
                                          ByVal csrfToken As String)
    Dim requestValue As AsyncWinHttpRequest
    Dim requestToken As Long

    If mWorkbookClosing Then
        ' Aborting the wait/SSE requests cancels a running server job through
        ' request-context cancellation even if Excel closes immediately.
        Exit Sub
    End If
    If Not IsAllowedPricingBridgeUrl(endpoint) Then Exit Sub
    If Not mCancelRequest Is Nothing Then Exit Sub
    requestToken = NextAsyncToken()
    Set requestValue = New AsyncWinHttpRequest
    Set mCancelRequest = requestValue
    requestValue.OpenAsync "DELETE", endpoint, requestToken, _
        "snapshot_cancel", False, 262144, HTTP_TIMEOUT_MS, _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS
    requestValue.SetRequestHeader "Accept", "application/json"
    requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    requestValue.SetRequestHeader PRICING_CSRF_HEADER, csrfToken
    requestValue.Send
End Sub

Private Sub HandleOperationTerminal()
    Dim requestValue As AsyncWinHttpRequest
    Dim responseBody As Variant
    Dim responseHeaders As String
    Dim statusCode As Long
    Dim errorNumber As Long
    Dim errorDescription As String
    Dim stageName As String

    On Error GoTo TerminalFailed
    Set requestValue = mOperationRequest
    If requestValue Is Nothing Then Exit Sub
    If Not requestValue.Terminal Then Exit Sub
    stageName = mOperationStage
    statusCode = requestValue.StatusCode
    responseHeaders = requestValue.ResponseHeaders
    responseBody = requestValue.TakeResponseBody()
    If requestValue.HasError Then
        errorNumber = requestValue.ErrorNumber
        errorDescription = requestValue.ErrorDescription
    End If
    Set mOperationRequest = Nothing

    If mOperationCancelRequested Or requestValue.Aborted Then
        FailActiveOperation vbObjectError + 239, stageName, T("sync_retry")
        Exit Sub
    End If
    If errorNumber <> 0 Then
        FailActiveOperation errorNumber, stageName, errorDescription
        Exit Sub
    End If

    Select Case stageName
        Case "contract", "repair_contract"
            HandleContractResponse statusCode, responseBody, stageName
        Case "session"
            HandleSessionResponse statusCode, responseBody
        Case "revision"
            HandleRevisionResponse statusCode, responseBody
        Case "snapshot_start"
            HandleSnapshotStartResponse statusCode, responseBody
        Case "snapshot_wait"
            HandleSnapshotWaitResponse statusCode, responseBody
        Case "snapshot_payload"
            HandleSnapshotPayloadResponse statusCode, responseHeaders, _
                responseBody
        Case "preview"
            HandlePreviewResponse statusCode, responseBody
        Case "apply"
            HandleApplyResponse statusCode, responseBody
        Case "repair"
            HandleRepairResponse statusCode, responseBody
        Case Else
            Err.Raise vbObjectError + 761, "HandleOperationTerminal", _
                      T("sync_retry")
    End Select
    Exit Sub

TerminalFailed:
    FailActiveOperation Err.Number, "HandleOperationTerminal", Err.Description
End Sub

Private Sub HandleContractResponse(ByVal statusCode As Long, _
                                   ByVal responseBody As Variant, _
                                   ByVal stageName As String)
    Dim responseText As String
    Dim root As JsonValue
    Dim previousSourceID As String
    Dim previousDataset As String
    Dim previousRevision As String

    RequireHTTPSuccess statusCode, responseBody, "contract"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(root, "schema")) <> "patris.product-sync" Then
        Err.Raise vbObjectError + 101, "HandleContractResponse", _
                  T("invalid_workbook")
    End If
    previousSourceID = mSourceID
    previousDataset = mSourceDataset
    previousRevision = mSourceRevision
    ReadSourceIdentity root
    mOperationContractSeconds = PhaseElapsed(mOperationContractStartedAt)

    If stageName = "repair_contract" Then
        If mSourceRevision <> mOperationDeliveredRevision Then
            Err.Raise vbObjectError + 149, "HandleContractResponse", _
                      T("source_sync_failed")
        End If
    ElseIf (mOperationKind = "preview" Or mOperationKind = "apply") And _
           Len(previousRevision) > 0 Then
        If previousSourceID <> mSourceID Or _
           previousDataset <> mSourceDataset Or _
           previousRevision <> mSourceRevision Then
            mSseRefreshRequired = True
            mSseRefreshRevision = vbNullString
            Err.Raise vbObjectError + 121, "HandleContractResponse", _
                      T("preview_first")
        End If
    End If
    BeginSessionRequest
End Sub

Private Sub HandleSessionResponse(ByVal statusCode As Long, _
                                  ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue
    Dim csrfToken As String

    RequireHTTPSuccess statusCode, responseBody, "session"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(root, "schema")) <> PRICING_SESSION_SCHEMA Then
        Err.Raise vbObjectError + 144, "HandleSessionResponse", _
                  T("bridge_missing")
    End If
    csrfToken = Trim$(CStr(JsonRuntime.JsonText(root, "csrf_token")))
    If Len(csrfToken) <> 43 Then
        Err.Raise vbObjectError + 145, "HandleSessionResponse", _
                  T("bridge_missing")
    End If
    mPricingCSRFToken = csrfToken
    mSessionSeconds = PhaseElapsed(mOperationStateStartedAt)
    Select Case mOperationKind
        Case "refresh"
            mOperationStateStartedAt = PhaseTimestamp()
            If mForceFreshSnapshot Then
                BeginSnapshotStartRequest
            Else
                BeginRevisionProbeRequest
            End If
        Case "apply_refresh"
            mOperationStateStartedAt = PhaseTimestamp()
            BeginSnapshotStartRequest
        Case "preview"
            BeginPreviewRequest
        Case "apply"
            BeginApplyRequest
        Case Else
            Err.Raise vbObjectError + 761, "HandleSessionResponse", _
                      T("sync_retry")
    End Select
End Sub

Private Sub HandleRevisionResponse(ByVal statusCode As Long, _
                                   ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue

    If statusCode < 200 Or statusCode >= 300 Then
        BeginSnapshotStartRequest
        Exit Sub
    End If
    On Error GoTo FullSnapshot
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    If TryCompleteUnchangedRevision(root) Then Exit Sub

FullSnapshot:
    Err.Clear
    BeginSnapshotStartRequest
End Sub

Private Function TryCompleteUnchangedRevision( _
    ByVal root As JsonValue) As Boolean
    Dim settings As Worksheet
    Dim sourceValue As JsonValue
    Dim stateRevision As String
    Dim pricingStateRevision As String
    Dim catalogRevision As String
    Dim revisionETag As String

    On Error GoTo NotUnchanged
    If mOperationKind <> "refresh" Or mForceFreshSnapshot Then Exit Function
    If root Is Nothing Or root.Kind <> "object" Or _
       SiteText(root, "schema") <> PRICING_REVISION_SCHEMA Or _
       RequiredWholeNumber(root, "schema_version") <> 1 Then Exit Function
    Set sourceValue = JsonRuntime.JsonMember(root, "source")
    If sourceValue Is Nothing Or sourceValue.Kind <> "object" Or _
       SiteText(sourceValue, "id") <> mSourceID Or _
       SiteText(sourceValue, "dataset") <> mSourceDataset Or _
       SiteText(sourceValue, "revision") <> mSourceRevision Then Exit Function
    stateRevision = SiteText(root, "state_revision")
    pricingStateRevision = SiteText(root, "pricing_state_revision")
    catalogRevision = SiteText(root, "catalog_revision")
    revisionETag = SiteText(root, "etag")
    If Not IsSHA256RevisionText(stateRevision) Or _
       Not IsSHA256RevisionText(pricingStateRevision) Or _
       Not IsSHA256RevisionText(catalogRevision) Or _
       StrongETagRevision(revisionETag) <> stateRevision Then Exit Function

    Set settings = ConfigSheet()
    If StrComp(Trim$(CStr(settings.Range("G56").Value2)), _
           stateRevision, vbBinaryCompare) <> 0 Or _
       StrComp(Trim$(CStr(settings.Range("G14").Value2)), _
           pricingStateRevision, vbBinaryCompare) <> 0 Or _
       StrComp(Trim$(CStr(settings.Range("G45").Value2)), _
           mSourceRevision, vbBinaryCompare) <> 0 Or _
       CLng(Val(CStr(settings.Range("G46").Value2))) <= 0 Then Exit Function
    ' catalogRevision is the upstream composite catalog identity. G44 stores
    ' the reconciled Excel dataset revision instead, so comparing those two
    ' distinct typed identities makes every unchanged refresh fall through to
    ' a full snapshot. The strong state ETag already binds the composite
    ' pricing/catalog state; retain the separately typed pricing/source/count
    ' and local-coherence guards here, while G44 remains guarded against the
    ' snapshot dataset revision in TryCompleteUnchangedSnapshot.
    If Not HasCoherentLocalCatalog() Or _
       CBool(settings.Range("G31").Value2) Or _
       StrictPriceParityMismatchCount() <> 0 Then Exit Function

    mOperationStateSeconds = PhaseElapsed(mOperationStateStartedAt)
    mStatePageTimingText = "revision_noop=" & _
        Format$(mOperationStateSeconds, "0.000") & "s; source=" & _
        mSourceRevision & "; state=" & stateRevision
    settings.Range("B46").Value2 = mSessionSeconds
    settings.Range("B47").Value2 = mOperationContractSeconds
    settings.Range("B48").Value2 = mOperationStateSeconds
    settings.Range("B49").NumberFormat = "@"
    settings.Range("B49").Value2 = mStatePageTimingText
    settings.Range("B50:B54").Value2 = 0
    settings.Range("B7").Value = Now
    settings.Range("B7").NumberFormat = "yyyy-mm-dd hh:mm"
    mLastRefreshSucceeded = True
    mForceFreshSnapshot = False
    SetRefreshProgress "4/4"
    CompleteActiveOperation True
    TryCompleteUnchangedRevision = True
    Exit Function

NotUnchanged:
    Err.Clear
End Function

Private Sub HandleSnapshotStartResponse(ByVal statusCode As Long, _
                                        ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue
    Dim errorCode As String

    responseText = Utf8TextFromBytes(responseBody)
    If statusCode = 404 Or statusCode = 405 Then
        Err.Raise vbObjectError + 234, "HandleSnapshotStartResponse", _
                  T("bridge_missing")
    End If
    If statusCode < 200 Or statusCode >= 300 Then
        errorCode = LCase$(ResponseErrorCode(responseText))
        If errorCode = "canonical_source_mismatch" And _
           RetrySnapshotAfterSourceDrift() Then
            Exit Sub
        End If
        If RetrySnapshotAfterTransientFailure(errorCode) Then Exit Sub
        RaiseSnapshotHTTPError statusCode, responseText
    End If
    Set root = JsonRuntime.ParseJson(responseText)
    ParsePricingSnapshotJob root, mOperationRequestID, mSnapshotJobID, _
        mSnapshotJobStatus, mSnapshotJobCode, mSnapshotWaitURL, _
        mSnapshotEventsURL, mSnapshotJobEventsURL, mSnapshotPayloadURL, _
        mSnapshotCancelURL, _
        mSnapshotExpectedETag, mSnapshotRevision, mSnapshotStateRevision, _
        mSnapshotPricingStateRevision, _
        mSnapshotDatasetRevision, _
        mSnapshotCompletedPages, mSnapshotTotalPages, mSnapshotRowCount
    SetSnapshotProgress mSnapshotCompletedPages, mSnapshotTotalPages, _
        mSnapshotRowCount
    ' Workbook_Open owns the lightweight listener independently of this slow
    ' snapshot lane. Bind that listener to the new job identity, but never make
    ' snapshot start responsible for listener creation or availability.
    mSseJobID = mSnapshotJobID
    If mSnapshotJobStatus = "ready" Then
        If TryCompleteUnchangedSnapshot() Then Exit Sub
        BeginSnapshotPayloadRequest
    Else
        BeginSnapshotWaitRequest
    End If
End Sub

Private Sub HandleSnapshotWaitResponse(ByVal statusCode As Long, _
                                       ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue

    RequireHTTPSuccess statusCode, responseBody, "snapshot_wait"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    ParsePricingSnapshotJob root, mOperationRequestID, mSnapshotJobID, _
        mSnapshotJobStatus, mSnapshotJobCode, mSnapshotWaitURL, _
        mSnapshotEventsURL, mSnapshotJobEventsURL, mSnapshotPayloadURL, _
        mSnapshotCancelURL, _
        mSnapshotExpectedETag, mSnapshotRevision, mSnapshotStateRevision, _
        mSnapshotPricingStateRevision, _
        mSnapshotDatasetRevision, _
        mSnapshotCompletedPages, mSnapshotTotalPages, mSnapshotRowCount
    SetSnapshotProgress mSnapshotCompletedPages, mSnapshotTotalPages, _
        mSnapshotRowCount
    If mSnapshotJobStatus <> "ready" Then
        If RetrySnapshotAfterTransientFailure(mSnapshotJobCode) Then Exit Sub
        If LCase$(Trim$(mSnapshotJobCode)) = _
           "snapshot_source_revision_conflict" And _
           RetrySnapshotAfterSourceDrift() Then
            Exit Sub
        End If
        If IsSnapshotDriftCode(mSnapshotJobCode) And _
           mOperationSnapshotRetryCount < 3 And _
           Not RefreshWallBudgetExhausted() Then
            mOperationSnapshotRetryCount = mOperationSnapshotRetryCount + 1
            mSourceID = vbNullString
            mSourceDataset = vbNullString
            mSourceRevision = vbNullString
            mOperationContractStartedAt = PhaseTimestamp()
            BeginContractRequest "contract"
            Exit Sub
        End If
        RaiseSnapshotJobFailure mSnapshotJobCode
    End If
    If TryCompleteUnchangedSnapshot() Then Exit Sub
    BeginSnapshotPayloadRequest
End Sub

Private Function TryCompleteUnchangedSnapshot() As Boolean
    Dim settings As Worksheet
    Dim committedEventsURL As String
    Dim committedJobID As String
    Dim committedCSRFToken As String

    On Error GoTo NotUnchanged
    If mOperationKind <> "refresh" Or mForceFreshSnapshot Or _
       mSnapshotJobStatus <> "ready" Then Exit Function

    Set settings = ConfigSheet()
    If StrComp(Trim$(CStr(settings.Range("G56").Value2)), _
           mSnapshotStateRevision, vbBinaryCompare) <> 0 Or _
       StrComp(Trim$(CStr(settings.Range("G14").Value2)), _
           mSnapshotPricingStateRevision, vbBinaryCompare) <> 0 Or _
       StrComp(Trim$(CStr(settings.Range("G44").Value2)), _
           mSnapshotDatasetRevision, vbBinaryCompare) <> 0 Or _
       StrComp(Trim$(CStr(settings.Range("G45").Value2)), _
           mSourceRevision, vbBinaryCompare) <> 0 Or _
       CLng(Val(CStr(settings.Range("G46").Value2))) <> _
           mSnapshotRowCount Then Exit Function
    If Not HasCoherentLocalCatalog() Or _
       CBool(settings.Range("G31").Value2) Or _
       StrictPriceParityMismatchCount() <> 0 Then Exit Function

    ValidateSseListenerPrerequisites mSnapshotEventsURL, mSnapshotJobID, _
        mPricingCSRFToken
    committedEventsURL = mSnapshotEventsURL
    committedJobID = mSnapshotJobID
    committedCSRFToken = mPricingCSRFToken
    mOperationStateSeconds = PhaseElapsed(mOperationStateStartedAt)
    mStatePageTimingText = "snapshot_noop=" & _
        Format$(mOperationStateSeconds, "0.000") & "s; source=" & _
        mSourceRevision & "; snapshot=" & mSnapshotRevision
    settings.Range("B46").Value2 = mSessionSeconds
    settings.Range("B47").Value2 = mOperationContractSeconds
    settings.Range("B48").Value2 = mOperationStateSeconds
    settings.Range("B49").NumberFormat = "@"
    settings.Range("B49").Value2 = mStatePageTimingText
    settings.Range("B50:B54").Value2 = 0
    settings.Range("B7").Value = Now
    settings.Range("B7").NumberFormat = "yyyy-mm-dd hh:mm"
    mLastRefreshSucceeded = True
    mForceFreshSnapshot = False
    SetRefreshProgress "4/4"
    CompleteActiveOperation True
    ArmSseListenerAfterCommit committedEventsURL, committedJobID, _
        committedCSRFToken
    TryCompleteUnchangedSnapshot = True
    Exit Function

NotUnchanged:
    Err.Clear
End Function

Private Function RetrySnapshotAfterSourceDrift() As Boolean
    If mOperationSnapshotRetryCount >= 3 Or _
       RefreshWallBudgetExhausted() Then Exit Function

    mOperationSnapshotRetryCount = mOperationSnapshotRetryCount + 1
    mSourceID = vbNullString
    mSourceDataset = vbNullString
    mSourceRevision = vbNullString
    mOperationContractStartedAt = PhaseTimestamp()
    SetOperationProgressSurface "refresh_request", -1, _
        T("source_changed"), "active", False
    BeginContractRequest "contract"
    RetrySnapshotAfterSourceDrift = True
End Function

Private Function RetrySnapshotAfterTransientFailure( _
    ByVal errorCode As String) As Boolean

    Select Case LCase$(Trim$(errorCode))
        Case "remote_unavailable", "canonical_source_unavailable"
            ' A verified local bridge can briefly lose either the canonical
            ' source read or the protected WordPress fetch. Retry only these
            ' explicitly transient failures. Configuration, identity, and
            ' integrity errors remain fail-closed.
        Case Else
            Exit Function
    End Select

    If mOperationSnapshotRetryCount >= 3 Or _
       RefreshWallBudgetExhausted() Then Exit Function

    mOperationSnapshotRetryCount = mOperationSnapshotRetryCount + 1
    mSourceID = vbNullString
    mSourceDataset = vbNullString
    mSourceRevision = vbNullString
    mOperationContractStartedAt = PhaseTimestamp()
    SetOperationProgressSurface "refresh_request", -1, _
        T("sync_retry"), "active", False
    BeginContractRequest "contract"
    RetrySnapshotAfterTransientFailure = True
End Function

Private Function RefreshWallBudgetExhausted() As Boolean
    If mOperationStartedAt <= 0# Then Exit Function
    RefreshWallBudgetExhausted = _
        (PhaseElapsed(mOperationStartedAt) >= MAX_REFRESH_WALL_SECONDS)
End Function

Private Sub HandleSnapshotPayloadResponse(ByVal statusCode As Long, _
                                          ByVal responseHeaders As String, _
                                          ByVal responseBody As Variant)
    Dim responseText As String
    Dim responseETag As String
    Dim rawDigest As String
    Dim reconciledRows As Object
    Dim validatedState As JsonValue
    Dim validatedCatalog As JsonValue
    Dim validatedDatasetRevision As String
    Dim validatedSourceRevision As String
    Dim validatedCountSignature As String
    Dim fetchedRows As Long
    Dim committedSseEventsURL As String
    Dim committedSseJobID As String
    Dim committedSseCSRFToken As String

    RequireHTTPSuccess statusCode, responseBody, "snapshot_payload"
    responseETag = ResponseHeaderValue(responseHeaders, "ETag")
    If Len(mSnapshotExpectedETag) = 0 Or _
       StrComp(Trim$(responseETag), Trim$(mSnapshotExpectedETag), _
               vbBinaryCompare) <> 0 Then
        Err.Raise vbObjectError + 130, "HandleSnapshotPayloadResponse", _
                  T("invalid_workbook")
    End If
    rawDigest = SHA256RevisionBytes(responseBody)
    If rawDigest <> StrongETagRevision(mSnapshotExpectedETag) Then
        Err.Raise vbObjectError + 130, "HandleSnapshotPayloadResponse", _
                  T("invalid_workbook")
    End If
    responseText = Utf8TextFromBytes(responseBody)
    Set reconciledRows = CreateObject("Scripting.Dictionary")
    reconciledRows.CompareMode = vbBinaryCompare
    fetchedRows = ImportPricingSnapshotPayload( _
        responseText, mSnapshotExpectedETag, mSnapshotRevision, _
        mSnapshotStateRevision, reconciledRows, validatedState, _
        validatedCatalog, validatedDatasetRevision, _
        validatedSourceRevision, validatedCountSignature)
    ValidateSseListenerPrerequisites mSnapshotEventsURL, mSnapshotJobID, _
        mPricingCSRFToken
    committedSseEventsURL = mSnapshotEventsURL
    committedSseJobID = mSnapshotJobID
    committedSseCSRFToken = mPricingCSRFToken
    mOperationStateSeconds = PhaseElapsed(mOperationStateStartedAt)
    mStatePageTimingText = "snapshot=" & _
        Format$(mOperationStateSeconds, "0.000") & "s; source=" & _
        mSourceRevision & "; snapshot=" & mSnapshotRevision & _
        "; etag=" & mSnapshotExpectedETag
    SetRefreshProgress "3/4"
    CommitRefreshSnapshot reconciledRows, validatedState, validatedCatalog, _
        validatedDatasetRevision, validatedSourceRevision, _
        validatedCountSignature, fetchedRows
    mForceFreshSnapshot = False

    If mOperationKind = "apply_refresh" Then
        ConfigSheet().Range("B23").Value2 = mOperationAppliedStatus
        InvalidatePricingPreview
        mRequiredSnapshotStateRevision = vbNullString
    End If
    CompleteActiveOperation True
    ' Listener failures are isolated from the already-committed snapshot. Its
    ' first replay therefore cannot cancel or roll back the cold atomic load.
    ArmSseListenerAfterCommit committedSseEventsURL, committedSseJobID, _
        committedSseCSRFToken
End Sub

Private Sub HandlePreviewResponse(ByVal statusCode As Long, _
                                  ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue
    Dim result As JsonValue
    Dim statusText As String

    RequireHTTPSuccess statusCode, responseBody, "preview"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)
    If PricingSettingsCanonical() <> mOperationRequestedSettings Then
        Err.Raise vbObjectError + 167, "HandlePreviewResponse", _
                  T("preview_first")
    End If
    mLastPreviewWarningCount = ValidatedWarningCount(result)
    mLastPreviewDigest = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "preview_digest"))))
    mLastPreviewExpiresAt = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "expires_at"))))
    mLastPreviewStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "state_revision"))))
    mLastPreviewSettings = mOperationRequestedSettings
    If Len(mLastPreviewDigest) = 0 Or _
       Not IsSHA256RevisionText(mLastPreviewStateRevision) Then
        Err.Raise vbObjectError + 160, "HandlePreviewResponse", _
                  T("preview_failed")
    End If
    ConfigSheet().Range("G26").Value2 = mLastPreviewDigest
    ConfigSheet().Range("G27").Value2 = mLastPreviewExpiresAt
    statusText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "status"))))
    ConfigSheet().Range("B23").Value2 = _
        T("preview_ready") & " " & statusText & WarningSummary(result)
    CompleteActiveOperation True
End Sub

Private Sub HandleApplyResponse(ByVal statusCode As Long, _
                                ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue
    Dim result As JsonValue
    Dim statusText As String
    Dim appliedStateRevision As String

    RequireHTTPSuccess statusCode, responseBody, "apply"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)
    appliedStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "state_revision"))))
    If Not IsSHA256RevisionText(appliedStateRevision) Then
        Err.Raise vbObjectError + 161, "HandleApplyResponse", _
                  T("sync_failed")
    End If
    statusText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "status"))))
    mOperationAppliedStatus = T("apply_done") & " " & statusText & _
        WarningSummary(result)
    mOperationAppliedStateRevision = appliedStateRevision
    mRequiredSnapshotStateRevision = appliedStateRevision
    mInternalPricingRefresh = True
    ConsumeSseRefreshRequirement
    BeginRefreshPipeline True, True
End Sub

Private Sub HandleRepairResponse(ByVal statusCode As Long, _
                                 ByVal responseBody As Variant)
    Dim responseText As String
    Dim root As JsonValue

    RequireHTTPSuccess statusCode, responseBody, "repair"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Or _
       Not BooleanValue(JsonRuntime.JsonText(root, "refreshed")) Or _
       Not BooleanValue(JsonRuntime.JsonText(root, "delivered")) Then
        Err.Raise vbObjectError + 149, "HandleRepairResponse", _
                  T("source_sync_failed")
    End If
    mOperationDeliveredRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(root, "source_revision"))))
    If Not IsSHA256RevisionText(mOperationDeliveredRevision) Then
        Err.Raise vbObjectError + 149, "HandleRepairResponse", _
                  T("source_sync_failed")
    End If
    mSourceID = vbNullString
    mSourceDataset = vbNullString
    mSourceRevision = vbNullString
    mOperationContractStartedAt = PhaseTimestamp()
    BeginContractRequest "repair_contract"
End Sub

Private Sub RequireHTTPSuccess(ByVal statusCode As Long, _
                               ByVal responseBody As Variant, _
                               ByVal sourceName As String)
    Dim responseText As String
    Dim errorMessage As String

    If statusCode >= 200 And statusCode < 300 Then Exit Sub
    responseText = Utf8TextFromBytes(responseBody)
    errorMessage = ResponseErrorMessage(responseText)
    If Len(errorMessage) = 0 Then errorMessage = "HTTP " & CStr(statusCode)
    Err.Raise vbObjectError + 146, sourceName, errorMessage
End Sub

Private Function Utf8TextFromBytes(ByVal value As Variant) As String
    Dim stream As Object

    If Not IsByteArrayVariant(value) Then
        Err.Raise vbObjectError + 147, "Utf8TextFromBytes", _
                  T("bridge_missing")
    End If
    If ByteArrayVariantLength(value) > MAX_PRICING_RESPONSE_BYTES Then
        Err.Raise vbObjectError + 147, "Utf8TextFromBytes", _
                  T("bridge_missing")
    End If
    Set stream = CreateObject("ADODB.Stream")
    stream.Type = 1
    stream.Open
    stream.Write value
    stream.Position = 0
    stream.Type = 2
    stream.Charset = "utf-8"
    Utf8TextFromBytes = stream.ReadText
    stream.Close
    If Len(Utf8TextFromBytes) > MAX_PRICING_RESPONSE_CHARS Then
        Err.Raise vbObjectError + 147, "Utf8TextFromBytes", _
                  T("bridge_missing")
    End If
End Function

Private Function IsByteArrayVariant(ByVal value As Variant) As Boolean
    IsByteArrayVariant = (VarType(value) = (vbArray Or vbByte))
End Function

Private Function ByteArrayVariantLength(ByVal value As Variant) As Long
    On Error GoTo EmptyValue
    If Not IsByteArrayVariant(value) Then Exit Function
    ByteArrayVariantLength = UBound(value) - LBound(value) + 1
    Exit Function
EmptyValue:
    ByteArrayVariantLength = 0
End Function

Private Function ResponseHeaderValue(ByVal headers As String, _
                                     ByVal headerName As String) As String
    Dim lines As Variant
    Dim lineValue As Variant
    Dim lineText As String
    Dim separatorPosition As Long

    headers = Replace$(headers, vbCrLf, vbLf)
    headers = Replace$(headers, vbCr, vbLf)
    lines = Split(headers, vbLf)
    For Each lineValue In lines
        lineText = CStr(lineValue)
        separatorPosition = InStr(1, lineText, ":", vbBinaryCompare)
        If separatorPosition > 0 Then
            If StrComp(Trim$(Left$(lineText, separatorPosition - 1)), _
                       headerName, vbTextCompare) = 0 Then
                ResponseHeaderValue = Trim$(Mid$( _
                    lineText, separatorPosition + 1))
                Exit Function
            End If
        End If
    Next lineValue
End Function

Private Sub CompleteActiveOperation(ByVal success As Boolean)
    Dim completedKind As String

    completedKind = mOperationKind
    mLastOperationName = completedKind
    mLastOperationSucceeded = success
    mLastOperationError = vbNullString
    mLastOperationDiagnostic = vbNullString
    Set mOperationRequest = Nothing
    mPricingCSRFToken = vbNullString
    mRefreshCancelRequested = False
    mOperationCancelRequested = False
    mRefreshInProgress = False
    mInternalPricingRefresh = False
    RestoreExcelInteractivityAfterOperation
    If completedKind = "preview" Or completedKind = "apply" Or _
       completedKind = "apply_refresh" Then
        mPricingActionInProgress = False
    End If
    PreserveSearchLiteral
    ReleaseRefreshCancelHotkey
    RestoreOperationCancelKey
    RestoreOperationStatusBar
    If success Then
        If completedKind = "refresh" Then MarkRefreshPricingConvergenceState
        SetOperationProgressSurface "completed", 100, _
            U("06280647200C063106480632063106330627064606CC002006A906270645064400200634062F"), _
            "confirmed", True
    End If
    RefreshSearchEnterHotkey
    ResetFiniteOperationContext
    If Not mWorkbookClosing Then ReevaluateSseRefreshRequirement
    If mPricingPreviewQueued And Not mWorkbookClosing Then _
        SchedulePricingPreview
End Sub

Private Sub MarkRefreshPricingConvergenceState()
    Dim settings As Worksheet
    Dim productsSheet As Worksheet
    Dim confirmedRate As Double
    Dim noteText As String
    Dim guardIssue As String
    Dim savedErrorNumber As Long
    Dim savedErrorSource As String

    On Error GoTo WarnState
    Set settings = ConfigSheet()
    Set productsSheet = PriceSheet()
    ' M7 is a visible formula mirror of G18. A snapshot commit can finish while
    ' Excel still exposes its pre-calculation value, producing a false warning
    ' even though all authoritative rates already agree. Calculate the one
    ' bounded consumer cell before evaluating the fail-closed guard.
    productsSheet.Range("M7").Calculate
    guardIssue = RefreshPricingConvergenceIssue(settings, productsSheet)
    If Len(guardIssue) > 0 Then GoTo WarnState
    confirmedRate = CDbl(settings.Range("B10").Value2)
    noteText = U("0647064506AF06270645200C06330627063206CC002006280627002006480628200C0633062706CC062A0020062A062306CC06CC062F00200634062F") & _
        vbCrLf & U("06450642062F06270631003A") & " " & _
        CanonicalCellText(confirmedRate) & _
        vbCrLf & U("06460633062E0647003A") & " " & _
        CStr(settings.Range("G14").Value2) & _
        vbCrLf & "state_revision: " & CStr(settings.Range("G56").Value2)
    MarkWritebackState "site_confirmation", "confirmed", noteText
    Exit Sub

WarnState:
    savedErrorNumber = Err.Number
    savedErrorSource = Err.Source
    Err.Clear
    If Len(guardIssue) = 0 Then
        guardIssue = "guard_error=" & CStr(savedErrorNumber)
        If Len(savedErrorSource) > 0 Then _
            guardIssue = guardIssue & "; source=" & savedErrorSource
    End If
    MarkWritebackState "site_confirmation", "warning", _
        U("06470634062F06270631003A002006460631062E00200647064506AF062706450020064606CC0633062A") & _
        vbCrLf & guardIssue
End Sub

Private Function RefreshPricingConvergenceIssue( _
    ByVal settings As Worksheet, ByVal productsSheet As Worksheet) As String
    Dim confirmedRate As Double

    On Error GoTo GuardError
    If Not IsNumeric(settings.Range("B10").Value2) Then
        RefreshPricingConvergenceIssue = "non_numeric=B10"
        Exit Function
    End If
    If Not IsNumeric(settings.Range("B18").Value2) Then
        RefreshPricingConvergenceIssue = "non_numeric=B18"
        Exit Function
    End If
    If Not IsNumeric(settings.Range("G18").Value2) Then
        RefreshPricingConvergenceIssue = "non_numeric=G18"
        Exit Function
    End If
    If Not IsNumeric(productsSheet.Range("M7").Value2) Then
        RefreshPricingConvergenceIssue = "non_numeric=M7"
        Exit Function
    End If

    confirmedRate = CDbl(settings.Range("B10").Value2)
    If confirmedRate <= 0 Then
        RefreshPricingConvergenceIssue = "non_positive=B10"
        Exit Function
    End If
    If CDbl(settings.Range("B18").Value2) <> confirmedRate Or _
       CDbl(settings.Range("G18").Value2) <> confirmedRate Or _
       CDbl(productsSheet.Range("M7").Value2) <> confirmedRate Then
        RefreshPricingConvergenceIssue = "rate_mismatch=" & _
            "B10:" & CanonicalCellText(settings.Range("B10").Value2) & _
            ";B18:" & CanonicalCellText(settings.Range("B18").Value2) & _
            ";G18:" & CanonicalCellText(settings.Range("G18").Value2) & _
            ";M7:" & CanonicalCellText(productsSheet.Range("M7").Value2)
        Exit Function
    End If
    If Not IsSHA256RevisionText(CStr(settings.Range("G14").Value2)) Then
        RefreshPricingConvergenceIssue = "invalid_revision=G14"
        Exit Function
    End If
    If Not IsSHA256RevisionText(CStr(settings.Range("G56").Value2)) Then
        RefreshPricingConvergenceIssue = "invalid_revision=G56"
    End If
    Exit Function

GuardError:
    RefreshPricingConvergenceIssue = "guard_error=" & CStr(Err.Number)
    Err.Clear
End Function

Private Sub FailActiveOperation(ByVal errorNumber As Long, _
                                ByVal errorSource As String, _
                                ByVal errorDescription As String)
    Dim failedKind As String
    Dim statusText As String
    Dim wasCancelled As Boolean

    If mOperationFinishing Then Exit Sub
    mOperationFinishing = True
    failedKind = mOperationKind
    statusText = SafeStatusError(errorDescription)
    If Len(Trim$(statusText)) = 0 Then statusText = T("sync_failed")
    wasCancelled = mOperationCancelRequested

    On Error Resume Next
    If (mSnapshotJobStatus = "running" Or _
        mSnapshotJobStatus = "cancelling") And _
       Len(mSnapshotCancelURL) > 0 And Len(mPricingCSRFToken) > 0 Then
        BeginBestEffortSnapshotCancel mSnapshotCancelURL, mPricingCSRFToken
    End If
    If Not mOperationRequest Is Nothing Then mOperationRequest.Abort
    Set mOperationRequest = Nothing
    If mOperationPricingStateCaptured Then
        RestorePricingStateSnapshot ConfigSheet(), _
            mOperationPricingStateSnapshot
    End If
    mSourceID = mOperationOriginalSourceID
    mSourceDataset = mOperationOriginalSourceDataset
    mSourceRevision = mOperationOriginalSourceRevision
    If failedKind = "apply" Or failedKind = "apply_refresh" Then
        RestoreSavedPreviewAfterApplyFailure
    End If
    If Not mWorkbookClosing Then
        If failedKind = "preview" Then
            ConfigSheet().Range("B23").Value2 = _
                T("preview_failed") & " " & statusText
        ElseIf failedKind = "apply" Or failedKind = "apply_refresh" Then
            ConfigSheet().Range("B23").Value2 = statusText
        Else
            ConfigSheet().Range("B6").Value2 = statusText
            If Len(mStatePageTimingText) > 0 Then
                ConfigSheet().Range("B49").NumberFormat = "@"
                ConfigSheet().Range("B49").Value2 = mStatePageTimingText
            End If
        End If
    End If
    On Error GoTo 0

    mLastRefreshSucceeded = False
    mLastOperationName = failedKind
    mLastOperationSucceeded = False
    mLastOperationError = statusText
    mLastOperationDiagnostic = errorSource & " (" & CStr(errorNumber) & "): " & _
                               errorDescription
    mPricingCSRFToken = vbNullString
    mRequiredSnapshotStateRevision = vbNullString
    mRefreshInProgress = False
    mPricingActionInProgress = False
    mInternalPricingRefresh = False
    RestoreExcelInteractivityAfterOperation
    mRefreshCancelRequested = False
    ReleaseRefreshCancelHotkey
    RestoreOperationCancelKey
    RestoreOperationStatusBar
    If Not mWorkbookClosing Then
        If wasCancelled Then
            SetOperationProgressSurface "cancelled", 0, _
                U("06280647200C063106480632063106330627064606CC00200644063A064800200634062F"), _
                "warning", True
        Else
            SetOperationProgressSurface "failed", 0, _
                U("06280647200C063106480632063106330627064606CC0020064606270645064806410642002006280648062F") & _
                ": " & statusText, "failed", True
        End If
    End If
    RefreshSearchEnterHotkey
    ResetFiniteOperationContext
    If Not wasCancelled And Not mWorkbookClosing Then _
        ReevaluateSseRefreshRequirement
    If mPricingPreviewQueued And Not mWorkbookClosing Then _
        SchedulePricingPreview
End Sub

Private Sub RestoreExcelInteractivityAfterOperation()
    ' Finite network callbacks finish at a top-level Excel dispatch. Recover
    ' from an interrupted or opened-with-events-disabled acceptance run so a
    ' bounded refresh failure cannot silently disable search, image preview,
    ' or pricing writeback. The atomic catalog commit restores its own captured
    ' application state before this terminal cleanup runs.
    If mWorkbookClosing Or mCatalogCommitInProgress Then Exit Sub
    On Error Resume Next
    Application.EnableEvents = True
    Application.ScreenUpdating = True
    On Error GoTo 0
End Sub

Private Sub RestoreSavedPreviewAfterApplyFailure()
    If Len(mOperationSavedPreviewDigest) = 0 Then Exit Sub
    If PricingSettingsCanonical() <> mOperationSavedPreviewSettings Then _
        Exit Sub
    mLastPreviewDigest = mOperationSavedPreviewDigest
    mLastPreviewExpiresAt = mOperationSavedPreviewExpiresAt
    mLastPreviewStateRevision = mOperationSavedPreviewStateRevision
    mLastPreviewSettings = mOperationSavedPreviewSettings
    mLastPreviewWarningCount = mOperationSavedPreviewWarningCount
    mLastApplyRequestID = mOperationSavedApplyRequestID
    ConfigSheet().Range("G26").Value2 = mLastPreviewDigest
    ConfigSheet().Range("G27").Value2 = mLastPreviewExpiresAt
    ConfigSheet().Range("G28").Value2 = mLastApplyRequestID
End Sub

Public Function AsyncPricingIdleForValidation() As Boolean
    If Len(mOperationKind) <> 0 Then Exit Function
    If mOperationRequest Is Nothing Then _
        AsyncPricingIdleForValidation = True
End Function

Public Function LastPricingOperationForValidation() As String
    LastPricingOperationForValidation = mLastOperationName
End Function

Public Function LastPricingOperationSucceededForValidation() As Boolean
    LastPricingOperationSucceededForValidation = mLastOperationSucceeded
End Function

Public Function LastPricingOperationErrorForValidation() As String
    LastPricingOperationErrorForValidation = mLastOperationError
End Function

Public Function LastPricingOperationDiagnosticForValidation() As String
    LastPricingOperationDiagnosticForValidation = mLastOperationDiagnostic
End Function

Public Sub StartPricingEventListenerOnOpen()
    ' Pricing events are a lightweight, independent channel. Restore them on
    ' every cold open without waiting for a full Patris/Woo catalog snapshot;
    ' the latter may be slow while the live source is changing continuously.
    If mWorkbookClosing Then Exit Sub
    If Len(mSseEventsURL) = 0 Then
        mSseEventsURL = PricingBaseURL() & "/events"
        mSseJobID = vbNullString
        mSseLastEventID = vbNullString
    End If
    mSseManualStop = False
    If mSseRequest Is Nothing And mSseSessionRequest Is Nothing Then _
        BeginSseSessionRenewal
End Sub

Public Function PricingEventListenerActiveForValidation() As Boolean
    PricingEventListenerActiveForValidation = _
        Not mSseManualStop And Len(mSseEventsURL) > 0 And _
        ((Not mSseRequest Is Nothing) Or _
         (Not mSseSessionRequest Is Nothing) Or mSseReconnectScheduled)
End Function

Private Sub EnsureSseListener(ByVal eventsURL As String, _
                              ByVal jobID As String, _
                              ByVal csrfToken As String)
    Dim normalizedURL As String
    Dim routeChanged As Boolean

    normalizedURL = SnapshotRouteURL(eventsURL)
    If normalizedURL <> PricingBaseURL() & "/events" Then
        Err.Raise vbObjectError + 130, "EnsureSseListener", _
                  T("invalid_workbook")
    End If
    routeChanged = (Len(mSseEventsURL) > 0 And _
        StrComp(mSseEventsURL, normalizedURL, vbBinaryCompare) <> 0)
    If routeChanged Then
        StopSseListener False
        mSseCSRFToken = vbNullString
        mSseLastEventID = vbNullString
    End If

    mSseEventsURL = normalizedURL
    mSseJobID = jobID
    mSseManualStop = False
    If Not mSseRequest Is Nothing Or _
       Not mSseSessionRequest Is Nothing Then Exit Sub

    CancelSseReconnect
    AdoptSseSessionToken csrfToken
    StartSseConnection
End Sub

Private Sub ValidateSseListenerPrerequisites(ByVal eventsURL As String, _
                                             ByVal jobID As String, _
                                             ByVal csrfToken As String)
    If SnapshotRouteURL(eventsURL) <> PricingBaseURL() & "/events" Or _
       Len(Trim$(jobID)) = 0 Or Len(Trim$(csrfToken)) <> 43 Then
        Err.Raise vbObjectError + 130, _
                  "ValidateSseListenerPrerequisites", T("invalid_workbook")
    End If
End Sub

Private Sub ArmSseListenerAfterCommit(ByVal eventsURL As String, _
                                      ByVal jobID As String, _
                                      ByVal csrfToken As String)
    On Error GoTo ListenerFailed
    EnsureSseListener eventsURL, jobID, csrfToken
    Exit Sub

ListenerFailed:
    ' The table is already a fully validated atomic snapshot. Preserve it and
    ' recover only the durable listener through its bounded event reconnect.
    On Error Resume Next
    mSseEventsURL = SnapshotRouteURL(eventsURL)
    mSseJobID = jobID
    mSseCSRFToken = vbNullString
    mSseManualStop = False
    ScheduleSseReconnect True
    Err.Clear
    On Error GoTo 0
End Sub

Private Sub AdoptSseSessionToken(ByVal csrfToken As String)
    csrfToken = Trim$(csrfToken)
    If Len(csrfToken) <> 43 Then
        Err.Raise vbObjectError + 145, "AdoptSseSessionToken", _
                  T("bridge_missing")
    End If
    If StrComp(mSseCSRFToken, csrfToken, vbBinaryCompare) <> 0 Then
        ' No server-generation identifier is exposed. A cursor is therefore
        ' bound only to the exact in-memory session that observed it.
        mSseLastEventID = vbNullString
    End If
    mSseCSRFToken = csrfToken
End Sub

Private Sub StartSseConnection()
    Dim requestValue As AsyncWinHttpRequest
    Dim requestToken As Long

    If mWorkbookClosing Or mSseManualStop Then Exit Sub
    If Len(mSseEventsURL) = 0 Or Len(mSseCSRFToken) = 0 Then Exit Sub
    If mSseEventsURL <> PricingBaseURL() & "/events" Or _
       Not IsAllowedPricingBridgeUrl(mSseEventsURL) Then
        mSseRefreshRequired = True
        mSseRefreshRevision = vbNullString
        ScheduleEventDrivenRefresh
        Exit Sub
    End If
    If Not mSseRequest Is Nothing Or _
       Not mSseSessionRequest Is Nothing Then Exit Sub

    On Error GoTo ConnectionFailed
    Set mSseParser = New PricingSseParser
    requestToken = NextAsyncToken()
    Set requestValue = New AsyncWinHttpRequest
    Set mSseRequest = requestValue
    requestValue.OpenAsync "GET", mSseEventsURL, requestToken, _
        "pricing_sse", True, SSE_BACKLOG_CAP_BYTES, HTTP_TIMEOUT_MS, _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, SSE_RECEIVE_TIMEOUT_MS
    requestValue.SetRequestHeader "Accept", "text/event-stream"
    requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    requestValue.SetRequestHeader PRICING_CSRF_HEADER, mSseCSRFToken
    If IsDecimalEventID(mSseLastEventID) Then
        requestValue.SetRequestHeader "Last-Event-ID", mSseLastEventID
    End If
    requestValue.Send
    Exit Sub

ConnectionFailed:
    Set mSseRequest = Nothing
    Set mSseParser = Nothing
    ScheduleSseReconnect False
End Sub

Private Sub BeginSseSessionRenewal()
    Dim requestValue As AsyncWinHttpRequest
    Dim requestToken As Long

    If mWorkbookClosing Or mSseManualStop Then Exit Sub
    If Not mSseRequest Is Nothing Or _
       Not mSseSessionRequest Is Nothing Then Exit Sub

    On Error GoTo RenewalFailed
    requestToken = NextAsyncToken()
    Set requestValue = New AsyncWinHttpRequest
    Set mSseSessionRequest = requestValue
    requestValue.OpenAsync "POST", PricingBaseURL() & "/session", _
        requestToken, "pricing_sse_session", False, 262144, _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS
    requestValue.SetRequestHeader "Accept", "application/json"
    requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    requestValue.SetRequestHeader "Content-Type", _
        "application/json; charset=utf-8"
    requestValue.Send Utf8Bytes("{}")
    Exit Sub

RenewalFailed:
    Set mSseSessionRequest = Nothing
    ScheduleSseReconnect True
End Sub

Private Sub HandleSseSessionDispatch()
    Dim requestValue As AsyncWinHttpRequest
    Dim responseBody As Variant
    Dim responseText As String
    Dim root As JsonValue
    Dim csrfToken As String
    Dim errorNumber As Long

    On Error GoTo RenewalRejected
    Set requestValue = mSseSessionRequest
    If requestValue Is Nothing Or Not requestValue.Terminal Then Exit Sub
    responseBody = requestValue.TakeResponseBody()
    If requestValue.HasError Then errorNumber = requestValue.ErrorNumber
    Set mSseSessionRequest = Nothing
    If mWorkbookClosing Or mSseManualStop Then Exit Sub
    If errorNumber <> 0 Then GoTo RenewalRejected

    RequireHTTPSuccess requestValue.StatusCode, responseBody, _
        "pricing_sse_session"
    responseText = Utf8TextFromBytes(responseBody)
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Or _
       SiteText(root, "schema") <> PRICING_SESSION_SCHEMA Then _
        GoTo RenewalRejected
    csrfToken = SiteText(root, "csrf_token")
    AdoptSseSessionToken csrfToken
    mSseRenewSessionBeforeReconnect = False
    StartSseConnection
    Exit Sub

RenewalRejected:
    Set mSseSessionRequest = Nothing
    ScheduleSseReconnect True
End Sub

Private Sub StopSseListener(ByVal manualStop As Boolean)
    Dim streamRequest As AsyncWinHttpRequest
    Dim sessionRequest As AsyncWinHttpRequest

    CancelSseReconnect
    mSseManualStop = manualStop
    Set streamRequest = mSseRequest
    Set sessionRequest = mSseSessionRequest
    Set mSseRequest = Nothing
    Set mSseSessionRequest = Nothing
    Set mSseParser = Nothing
    On Error Resume Next
    If Not streamRequest Is Nothing Then streamRequest.Abort
    If Not sessionRequest Is Nothing Then sessionRequest.Abort
    On Error GoTo 0
    If manualStop Then
        mSseEventsURL = vbNullString
        mSseJobID = vbNullString
        mSseCSRFToken = vbNullString
        mSseLastEventID = vbNullString
        ClearSseRefreshRequirement
    End If
End Sub

Private Sub HandleSseDispatch()
    Dim requestValue As AsyncWinHttpRequest
    Dim statusCode As Long
    Dim contentType As String
    Dim responseAccepted As Boolean
    Dim renewSession As Boolean

    On Error GoTo StreamFailed
    Set requestValue = mSseRequest
    If requestValue Is Nothing Then Exit Sub
    If requestValue.ResponseStarted Then
        statusCode = requestValue.StatusCode
        contentType = LCase$(Trim$(requestValue.ContentType))
        If Len(contentType) = 0 Then
            contentType = LCase$(ResponseHeaderValue( _
                requestValue.ResponseHeaders, "Content-Type"))
        End If
        responseAccepted = (statusCode = 200 And _
            Left$(contentType, Len("text/event-stream")) = _
                "text/event-stream")
        If responseAccepted Then
            mSseReconnectAttempt = 0
        Else
            If statusCode = 403 Then
                renewSession = True
            Else
                mSseLastEventID = vbNullString
                mSseRefreshRequired = True
                mSseRefreshRevision = vbNullString
            End If
            requestValue.Abort
        End If
    End If
    If responseAccepted Then DrainSseChunks requestValue
    If Not requestValue.Terminal Then Exit Sub

    If requestValue.ResponseFinished And requestValue.StatusCode = 200 And _
       Not requestValue.HasError Then
        ' The durable stream closes normally when its ten-minute session
        ' expires. Mint a new session and deliberately drop the old cursor.
        renewSession = True
    End If
    If mSseRenewSessionBeforeReconnect Then renewSession = True
    Set mSseRequest = Nothing
    Set mSseParser = Nothing
    If mSseRefreshRequired Then ScheduleEventDrivenRefresh
    If Not mSseManualStop And Not mWorkbookClosing Then _
        ScheduleSseReconnect renewSession
    Exit Sub

StreamFailed:
    mSseLastEventID = vbNullString
    mSseRefreshRequired = True
    mSseRefreshRevision = vbNullString
    On Error Resume Next
    If Not mSseRequest Is Nothing Then mSseRequest.Abort
    Set mSseRequest = Nothing
    Set mSseParser = Nothing
    On Error GoTo 0
    ScheduleEventDrivenRefresh
    ScheduleSseReconnect False
End Sub

Private Sub DrainSseChunks(ByVal requestValue As AsyncWinHttpRequest)
    Dim chunks As Collection
    Dim events As Collection
    Dim chunk As Variant
    Dim eventValue As Variant
    Dim retryMilliseconds As Long

    If mSseParser Is Nothing Then Set mSseParser = New PricingSseParser
    Set chunks = requestValue.TakeStreamChunks()
    For Each chunk In chunks
        Set events = mSseParser.Feed(chunk)
        If mSseParser.HasFailed Then
            mSseLastEventID = vbNullString
            mSseRefreshRequired = True
            mSseRefreshRevision = vbNullString
            requestValue.Abort
            Exit Sub
        End If
        For Each eventValue In events
            HandlePricingSseEvent eventValue
            If requestValue.Terminal Then Exit Sub
        Next eventValue
    Next chunk
    retryMilliseconds = mSseParser.ReconnectDelayMilliseconds
    If retryMilliseconds >= 0 Then
        mSseReconnectDelaySeconds = (retryMilliseconds + 999) \ 1000
        If mSseReconnectDelaySeconds < SSE_RECONNECT_MIN_SECONDS Then _
            mSseReconnectDelaySeconds = SSE_RECONNECT_MIN_SECONDS
        If mSseReconnectDelaySeconds > SSE_RECONNECT_MAX_SECONDS Then _
            mSseReconnectDelaySeconds = SSE_RECONNECT_MAX_SECONDS
    End If
End Sub

Private Function PricingEventRevisionChanged(ByVal eventKind As String, _
                                             ByVal stateRevision As String, _
                                             ByVal pricingRevision As String) As Boolean
    Dim currentRevision As String
    Dim candidateRevision As String

    Select Case eventKind
        Case "source_changed", "catalog_changed"
            candidateRevision = Trim$(stateRevision)
            If Len(candidateRevision) = 0 Then
                ' A local Patris file notification has no remote composite yet.
                ' Its new identity can only be established by a fresh snapshot.
                PricingEventRevisionChanged = True
                Exit Function
            End If
            currentRevision = Trim$(CStr(ConfigSheet().Range("G56").Value2))

        Case "pricing_state_changed", "pricing_state_invalidated"
            candidateRevision = Trim$(pricingRevision)
            If Len(candidateRevision) = 0 Then _
                candidateRevision = Trim$(stateRevision)
            currentRevision = Trim$(CStr(ConfigSheet().Range("G14").Value2))

        Case Else
            Exit Function
    End Select

    PricingEventRevisionChanged = _
        (StrComp(candidateRevision, currentRevision, vbBinaryCompare) <> 0)
End Function

Private Sub HandlePricingSseEvent(ByVal eventValue As Object)
    Dim eventID As String
    Dim eventName As String
    Dim eventData As String
    Dim root As JsonValue
    Dim payloadSequence As String
    Dim payloadKind As String
    Dim change As JsonValue
    Dim eventJobID As String
    Dim eventReason As String
    Dim currentSnapshotConfirmation As Boolean
    Dim expectedApplyMutationEvent As Boolean
    Dim eventStateRevision As String
    Dim eventPricingRevision As String
    Dim revisionChanged As Boolean
    Dim verified As Boolean
    Dim stale As Boolean
    Dim alreadyForcingFreshSnapshot As Boolean

    On Error GoTo InvalidEvent
    If eventValue Is Nothing Then GoTo InvalidEvent
    If Not eventValue.Exists("id") Or Not eventValue.Exists("event") Or _
       Not eventValue.Exists("data") Then GoTo InvalidEvent
    eventID = NormalizeDecimalEventID(CStr(eventValue("id")))
    If Not IsDecimalEventID(eventID) Or eventID = "0" Then GoTo InvalidEvent
    eventName = CStr(eventValue("event"))
    eventData = CStr(eventValue("data"))
    Set root = JsonRuntime.ParseJson(eventData)
    If root Is Nothing Or root.Kind <> "object" Or _
       SiteText(root, "schema") <> PRICING_SNAPSHOT_EVENT_SCHEMA Then _
        GoTo InvalidEvent
    payloadSequence = RawUnsignedDecimalMember(eventData, "sequence")
    If payloadSequence <> eventID Then GoTo InvalidEvent
    payloadKind = SiteText(root, "kind")
    If payloadKind <> eventName Then GoTo InvalidEvent
    Set change = JsonRuntime.JsonMember(root, "change")
    If change Is Nothing Or change.Kind <> "object" Or _
       SiteText(change, "schema") <> PRICING_SNAPSHOT_EVENT_SCHEMA Or _
       SiteText(change, "kind") <> payloadKind Then GoTo InvalidEvent

    eventReason = LCase$(SiteText(change, "reason"))
    If payloadKind = "replay_required" Then
        Select Case eventReason
            Case "cursor_expired", "cursor_ahead", _
                 "initial_history_expired", "initial_state_unavailable"
            Case Else
                GoTo InvalidEvent
        End Select
        ' A replay reset never becomes a reconnect cursor. Reconnect to the
        ' initial authoritative semantic state and rebuild conditionally.
        mSseLastEventID = vbNullString
        alreadyForcingFreshSnapshot = mForceFreshSnapshot Or _
            Len(mRequiredSnapshotStateRevision) > 0
        InvalidatePricingPreview
        If (mOperationKind = "refresh" Or _
            mOperationKind = "apply_refresh") And _
           alreadyForcingFreshSnapshot Then
            ' The active request is already a max_age_seconds=0 rebuild.
            mForceFreshSnapshot = True
        Else
            ' A history gap proves that a semantic event may have been missed,
            ' not that the authoritative revision changed. Probe the protected
            ' composite first; only a verified difference starts a fresh build.
            MarkSseRefreshRequired False
        End If
        If Not mSseRequest Is Nothing Then mSseRequest.Abort
        Exit Sub
    End If
    If Len(mSseLastEventID) > 0 Then
        If Not DecimalEventIDGreaterThan(eventID, mSseLastEventID) Then _
            GoTo InvalidEvent
    End If

    eventJobID = SiteText(change, "job_id")
    eventStateRevision = SiteText(change, "state_revision")
    eventPricingRevision = SiteText(change, "pricing_state_revision")
    verified = BooleanValue(JsonRuntime.JsonText(change, "verified"))
    stale = BooleanValue(JsonRuntime.JsonText(change, "stale"))
    revisionChanged = PricingEventRevisionChanged( _
        payloadKind, eventStateRevision, eventPricingRevision)
    currentSnapshotConfirmation = (Len(mSseJobID) > 0 And _
        eventJobID = mSseJobID And verified)
    Select Case payloadKind
        Case "snapshot_ready"
            If Not verified Or stale Or Len(eventJobID) = 0 Then _
                GoTo InvalidEvent
            ' Snapshot readiness is a completion notification, not semantic
            ' drift. Starting another refresh here makes every successful
            ' refresh publish the event that immediately starts its successor.
            ' Actual Patris/WooCommerce changes arrive through the explicit
            ' source/catalog/pricing-state cases below and remain coalesced.

        Case "source_changed", "catalog_changed"
            If Not stale Or (Len(eventStateRevision) > 0 And _
               Not IsSHA256RevisionText(eventStateRevision)) Then _
                GoTo InvalidEvent
            If Not currentSnapshotConfirmation And _
               (revisionChanged Or RefreshPipelineActiveForSseRetention()) Then _
                MarkSseRefreshRequired True, eventStateRevision

        Case "pricing_state_changed"
            If Len(eventPricingRevision) = 0 Then _
                eventPricingRevision = eventStateRevision
            If Not stale Or _
               Not IsSHA256RevisionText(eventPricingRevision) Then _
                GoTo InvalidEvent
            If revisionChanged Then
                expectedApplyMutationEvent = _
                    IsExpectedApplyMutationEvent(eventPricingRevision)
                If expectedApplyMutationEvent Then
                    PreserveExpectedApplyMutationEvent
                ElseIf Not currentSnapshotConfirmation Then
                    ' Apply and ACK the website-confirmed settings first.
                    ' Starting a full catalog refresh here can project the
                    ' previous value back into the writeback queue while the
                    ' confirmation request is active.
                    mRefreshAfterSiteConfirmation = True
                    QueueSiteConfirmationDiscovery
                End If
            End If

        Case "pricing_state_invalidated"
            If Len(eventPricingRevision) = 0 Then _
                eventPricingRevision = eventStateRevision
            If Not stale Or _
               Not IsSHA256RevisionText(eventPricingRevision) Then _
                GoTo InvalidEvent
            If revisionChanged Then
                expectedApplyMutationEvent = _
                    IsExpectedApplyMutationEvent(eventPricingRevision)
                If expectedApplyMutationEvent Then
                    PreserveExpectedApplyMutationEvent
                Else
                    MarkSseRefreshRequired True, vbNullString
                End If
            End If

        Case Else
            GoTo InvalidEvent
    End Select
    mSseLastEventID = eventID
    Exit Sub

InvalidEvent:
    mSseLastEventID = vbNullString
    mSseRefreshRequired = True
    mSseRefreshRevision = vbNullString
    InvalidatePricingPreview
    If Not mSseRequest Is Nothing Then mSseRequest.Abort
End Sub

Private Sub QueueSiteConfirmationDiscovery()
    If mPricingEventValidationMode Then
        mPricingEventValidationConfirmationQueued = True
        Exit Sub
    End If
    If mWorkbookClosing Then Exit Sub
    EnsureWritebackQueue
    If Len(mWritebackStage) > 0 And _
       mWritebackSettingKey = "site_confirmation" Then Exit Sub
    mWritebackPending("site_confirmation") = "B18"
    MarkWritebackState "site_confirmation", "sending", _
        U("062A063A06CC06CC06310020064806280633062706CC062A0020062F06310020062706A9063306440020062A062706CC06CC062F00200634062F061B0020062F0631002006270646062A0638062706310020062B0628062A002006460647062706CC06CC002006480628200C0633062706CC062A002006270633062A002E")
    SchedulePricingWriteback 1
End Sub

Private Function IsExpectedApplyMutationEvent( _
    ByVal eventStateRevision As String) As Boolean
    If Not IsSHA256RevisionText(eventStateRevision) Then Exit Function

    Select Case mOperationKind
        Case "apply"
            ' The bridge publishes the committed/verified revisions before the
            ' apply HTTP response can reach Excel. The response remains the
            ' authoritative transition into the exact-state refresh.
            IsExpectedApplyMutationEvent = True
        Case "apply_refresh"
            IsExpectedApplyMutationEvent = _
                (Len(mRequiredSnapshotStateRevision) > 0 And _
                 StrComp(eventStateRevision, _
                         mRequiredSnapshotStateRevision, _
                         vbBinaryCompare) = 0)
    End Select
End Function

Private Sub PreserveExpectedApplyMutationEvent()
    mForceFreshSnapshot = True
    InvalidatePricingPreview
    If mOperationKind = "apply" Then
        ' If post-apply verification fails after the remote mutation committed,
        ' the terminal failure path must still schedule a fresh snapshot.
        mSseRefreshRequired = True
        mSseRefreshRevision = vbNullString
    End If
End Sub

Private Sub MarkSseRefreshRequired( _
        Optional ByVal forceFreshSnapshot As Boolean = True, _
        Optional ByVal eventStateRevision As String = vbNullString)
    If IsSHA256RevisionText(Trim$(eventStateRevision)) Then
        eventStateRevision = Trim$(eventStateRevision)
    Else
        eventStateRevision = vbNullString
    End If
    mSseRefreshRequired = True
    mSseRefreshRevision = eventStateRevision
    If forceFreshSnapshot Then mForceFreshSnapshot = True
    If mPricingEventValidationMode Then
        If Not mPricingEventValidationRefreshActive Then _
            ScheduleEventDrivenRefresh
        Exit Sub
    End If
    InvalidatePricingPreview
    If Len(mOperationKind) > 0 Then
        If mOperationKind = "refresh" Or _
           mOperationKind = "apply_refresh" Then Exit Sub
        If mOperationKind = "preview" Or mOperationKind = "apply" Then _
            mPricingPreviewQueued = True
        If Not mOperationRequest Is Nothing Then
            mOperationRequest.Abort
        Else
            FailActiveOperation vbObjectError + 130, _
                "MarkSseRefreshRequired", T("sync_retry")
        End If
    Else
        ScheduleEventDrivenRefresh
    End If
End Sub

Private Function RefreshPipelineActiveForSseRetention() As Boolean
    If mPricingEventValidationMode Then
        RefreshPipelineActiveForSseRetention = _
            mPricingEventValidationRefreshActive
    Else
        RefreshPipelineActiveForSseRetention = _
            (mOperationKind = "refresh" Or _
             mOperationKind = "apply_refresh")
    End If
End Function

Private Function SseRefreshRevisionIsCurrent() As Boolean
    If Not mSseRefreshRequired Or _
       Not IsSHA256RevisionText(mSseRefreshRevision) Then Exit Function
    On Error GoTo NotCurrent
    SseRefreshRevisionIsCurrent = _
        (StrComp(mSseRefreshRevision, _
                 Trim$(CStr(ConfigSheet().Range("G56").Value2)), _
                 vbBinaryCompare) = 0)
NotCurrent:
    Err.Clear
End Function

Private Sub ClearSseRefreshRequirement()
    mSseRefreshRequired = False
    mSseRefreshRevision = vbNullString
End Sub

Private Sub ConsumeSseRefreshRequirement()
    ' A refresh that is about to start owns all semantic events observed before
    ' its start. Only later events remain pending for terminal re-evaluation.
    ClearSseRefreshRequirement
End Sub

Private Sub ReevaluateSseRefreshRequirement()
    If Not mSseRefreshRequired Then Exit Sub
    If SseRefreshRevisionIsCurrent() Then
        ClearSseRefreshRequirement
        Exit Sub
    End If
    ScheduleEventDrivenRefresh
End Sub

Private Function RawUnsignedDecimalMember(ByVal jsonText As String, _
                                          ByVal memberName As String) As String
    Dim marker As String
    Dim valueStart As Long
    Dim valueEnd As Long
    Dim boundaryIndex As Long
    Dim characterCode As Long
    Dim candidate As String

    marker = Chr$(34) & memberName & Chr$(34) & ":"
    valueStart = InStr(1, jsonText, marker, vbBinaryCompare)
    If valueStart = 0 Then Exit Function
    valueStart = valueStart + Len(marker)
    Do While valueStart <= Len(jsonText) And _
             InStr(1, " " & vbTab & vbCr & vbLf, _
                   Mid$(jsonText, valueStart, 1), vbBinaryCompare) > 0
        valueStart = valueStart + 1
    Loop
    valueEnd = valueStart
    Do While valueEnd <= Len(jsonText)
        characterCode = AscW(Mid$(jsonText, valueEnd, 1))
        If characterCode < 48 Or characterCode > 57 Then Exit Do
        valueEnd = valueEnd + 1
    Loop
    If valueEnd = valueStart Then Exit Function
    boundaryIndex = valueEnd
    Do While boundaryIndex <= Len(jsonText) And _
             InStr(1, " " & vbTab & vbCr & vbLf, _
                   Mid$(jsonText, boundaryIndex, 1), vbBinaryCompare) > 0
        boundaryIndex = boundaryIndex + 1
    Loop
    If boundaryIndex > Len(jsonText) Then Exit Function
    If Mid$(jsonText, boundaryIndex, 1) <> "," And _
       Mid$(jsonText, boundaryIndex, 1) <> "}" Then Exit Function
    candidate = NormalizeDecimalEventID( _
        Mid$(jsonText, valueStart, valueEnd - valueStart))
    If Not IsDecimalEventID(candidate) Then Exit Function
    RawUnsignedDecimalMember = candidate
End Function

Private Function IsDecimalEventID(ByVal value As String) As Boolean
    value = NormalizeDecimalEventID(value)
    If Len(value) = 0 Or Len(value) > 20 Then Exit Function
    If Len(value) = 20 Then
        If StrComp(value, "18446744073709551615", _
                   vbBinaryCompare) > 0 Then Exit Function
    End If
    IsDecimalEventID = True
End Function

Private Function NormalizeDecimalEventID(ByVal value As String) As String
    Dim index As Long
    Dim characterCode As Long

    value = Trim$(value)
    If Len(value) = 0 Then Exit Function
    For index = 1 To Len(value)
        characterCode = AscW(Mid$(value, index, 1))
        If characterCode < 48 Or characterCode > 57 Then Exit Function
    Next index
    index = 1
    Do While index < Len(value) And Mid$(value, index, 1) = "0"
        index = index + 1
    Loop
    NormalizeDecimalEventID = Mid$(value, index)
End Function

Private Function DecimalEventIDGreaterThan(ByVal candidate As String, _
                                           ByVal previous As String) As Boolean
    candidate = NormalizeDecimalEventID(candidate)
    previous = NormalizeDecimalEventID(previous)
    If Not IsDecimalEventID(candidate) Or _
       Not IsDecimalEventID(previous) Then Exit Function
    If Len(candidate) <> Len(previous) Then
        DecimalEventIDGreaterThan = (Len(candidate) > Len(previous))
    Else
        DecimalEventIDGreaterThan = _
            (StrComp(candidate, previous, vbBinaryCompare) > 0)
    End If
End Function

Private Sub ScheduleSseReconnect(ByVal renewSession As Boolean)
    Dim delaySeconds As Long
    Dim attempt As Long

    If renewSession Then mSseRenewSessionBeforeReconnect = True
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameSseReconnectPending = True
        If renewSession Then mSaveRenameSseRenewSession = True
        Exit Sub
    End If
    If mWorkbookClosing Or mSseManualStop Or _
       mSseReconnectScheduled Then Exit Sub
    If Not mSseRequest Is Nothing Or _
       Not mSseSessionRequest Is Nothing Then Exit Sub
    On Error GoTo ScheduleFailed
    mSseReconnectAttempt = mSseReconnectAttempt + 1
    delaySeconds = mSseReconnectDelaySeconds
    If delaySeconds < SSE_RECONNECT_MIN_SECONDS Then _
        delaySeconds = SSE_RECONNECT_MIN_SECONDS
    For attempt = 2 To mSseReconnectAttempt
        If delaySeconds >= SSE_RECONNECT_MAX_SECONDS Then Exit For
        delaySeconds = delaySeconds * 2
    Next attempt
    If delaySeconds > SSE_RECONNECT_MAX_SECONDS Then _
        delaySeconds = SSE_RECONNECT_MAX_SECONDS
    mSseReconnectTime = Now + TimeSerial(0, 0, delaySeconds)
    mSseReconnectScheduled = True
    Application.OnTime EarliestTime:=mSseReconnectTime, _
        Procedure:=QualifiedWorkbookMacro("RunSseReconnect"), _
        Schedule:=True
    Exit Sub

ScheduleFailed:
    mSseReconnectScheduled = False
    Err.Clear
End Sub

Public Sub RunSseReconnect()
    mSseReconnectScheduled = False
    If mWorkbookClosing Or mSseManualStop Then Exit Sub
    If mSseRenewSessionBeforeReconnect Or Len(mSseCSRFToken) = 0 Then
        BeginSseSessionRenewal
    Else
        StartSseConnection
    End If
End Sub

Private Sub CancelSseReconnect()
    If mSseReconnectScheduled Then
        On Error Resume Next
        Application.OnTime EarliestTime:=mSseReconnectTime, _
            Procedure:=QualifiedWorkbookMacro("RunSseReconnect"), _
            Schedule:=False
        On Error GoTo 0
    End If
    mSseReconnectScheduled = False
    mSseRenewSessionBeforeReconnect = False
End Sub

Private Sub ScheduleEventDrivenRefresh()
    If Not mSseRefreshRequired Then
        mSseRefreshRequired = True
        mSseRefreshRevision = vbNullString
    End If
    If SseRefreshRevisionIsCurrent() Then
        ClearSseRefreshRequirement
        Exit Sub
    End If
    If mPricingEventValidationMode Then
        If mPricingEventValidationRefreshActive Then Exit Sub
        If Not mPricingEventValidationRefreshScheduled Then
            mPricingEventValidationRefreshCount = _
                mPricingEventValidationRefreshCount + 1
        End If
        mPricingEventValidationRefreshScheduled = True
        Exit Sub
    End If
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameEventRefreshPending = True
        Exit Sub
    End If
    If mWorkbookClosing Or mEventRefreshScheduled Or _
       mRefreshScheduled Then Exit Sub
    If Len(mOperationKind) > 0 Then
        Exit Sub
    End If
    On Error GoTo ScheduleFailed
    mEventRefreshTime = Now + TimeSerial(0, 0, 1)
    mEventRefreshScheduled = True
    Application.OnTime EarliestTime:=mEventRefreshTime, _
        Procedure:=QualifiedWorkbookMacro("RunEventDrivenRefresh"), _
        Schedule:=True
    Exit Sub

ScheduleFailed:
    mEventRefreshScheduled = False
    Err.Clear
End Sub

Public Sub RunEventDrivenRefresh()
    If mPricingEventValidationMode Then
        If mPricingEventValidationRefreshScheduled Then
            mPricingEventValidationAppliedCount = _
                mPricingEventValidationAppliedCount + 1
        End If
        mPricingEventValidationRefreshScheduled = False
        ConsumeSseRefreshRequirement
        Exit Sub
    End If
    mEventRefreshScheduled = False
    If mWorkbookClosing Then Exit Sub
    If mRefreshInProgress Or mPricingActionInProgress Then
        Exit Sub
    End If
    RefreshAllData True
End Sub

Private Sub CancelEventDrivenRefresh()
    If Not mEventRefreshScheduled Then Exit Sub
    On Error Resume Next
    Application.OnTime EarliestTime:=mEventRefreshTime, _
        Procedure:=QualifiedWorkbookMacro("RunEventDrivenRefresh"), _
        Schedule:=False
    On Error GoTo 0
    mEventRefreshScheduled = False
End Sub

Private Function PricingEventFixtureForValidation( _
        ByVal eventID As Long, ByVal eventKind As String, _
        ByVal stateRevision As String, _
        ByVal pricingRevision As String, _
        Optional ByVal staleEvent As Boolean = True, _
        Optional ByVal verifiedEvent As Boolean = True, _
        Optional ByVal jobID As String = vbNullString) As Object
    Dim eventValue As Object
    Dim eventData As String

    eventData = "{""schema"":" & _
        JsonString(PRICING_SNAPSHOT_EVENT_SCHEMA) & "," & _
        """sequence"":" & CStr(eventID) & "," & _
        """kind"":" & JsonString(eventKind) & "," & _
        """change"":{""schema"":" & _
        JsonString(PRICING_SNAPSHOT_EVENT_SCHEMA) & "," & _
        """kind"":" & JsonString(eventKind) & "," & _
        """state_revision"":" & JsonString(stateRevision)
    If Len(pricingRevision) > 0 Then
        eventData = eventData & ",""pricing_state_revision"":" & _
            JsonString(pricingRevision)
    End If
    If Len(jobID) > 0 Then
        eventData = eventData & ",""job_id"":" & JsonString(jobID)
    End If
    eventData = eventData & ",""stale"":" & _
        IIf(staleEvent, "true", "false") & ",""verified"":" & _
        IIf(verifiedEvent, "true", "false") & "}}"

    Set eventValue = CreateObject("Scripting.Dictionary")
    eventValue.CompareMode = vbBinaryCompare
    eventValue.Add "id", CStr(eventID)
    eventValue.Add "event", eventKind
    eventValue.Add "data", eventData
    Set PricingEventFixtureForValidation = eventValue
End Function

Public Function ValidatePricingEventAutoPullForValidation() As Boolean
    Dim settings As Worksheet
    Dim eventValue As Object
    Dim originalStateRevision As Variant
    Dim originalPricingRevision As Variant
    Dim originalLastEventID As String
    Dim originalSseJobID As String
    Dim originalRefreshRequired As Boolean
    Dim originalRefreshRevision As String
    Dim originalForceFreshSnapshot As Boolean
    Dim originalRefreshAfterConfirmation As Boolean
    Dim originalValidationRefreshActive As Boolean
    Dim originalInternalRefresh As Boolean
    Dim originalEvents As Boolean
    Dim originalSaved As Boolean
    Dim runtimeStateCaptured As Boolean
    Dim cellStateCaptured As Boolean
    Dim currentStateRevision As String
    Dim nextStateRevision As String
    Dim thirdStateRevision As String
    Dim fourthStateRevision As String
    Dim currentPricingRevision As String
    Dim nextPricingRevision As String

    On Error GoTo ValidationFailed
    If Len(mOperationKind) > 0 Or mRefreshInProgress Or _
       mPricingActionInProgress Or mPricingEventValidationMode Then _
        Exit Function

    originalLastEventID = mSseLastEventID
    originalSseJobID = mSseJobID
    originalRefreshRequired = mSseRefreshRequired
    originalRefreshRevision = mSseRefreshRevision
    originalForceFreshSnapshot = mForceFreshSnapshot
    originalRefreshAfterConfirmation = mRefreshAfterSiteConfirmation
    originalValidationRefreshActive = _
        mPricingEventValidationRefreshActive
    originalInternalRefresh = mInternalPricingRefresh
    originalEvents = Application.EnableEvents
    originalSaved = ThisWorkbook.Saved
    runtimeStateCaptured = True

    Set settings = ConfigSheet()
    originalStateRevision = settings.Range("G56").Value2
    originalPricingRevision = settings.Range("G14").Value2
    cellStateCaptured = True

    currentStateRevision = "sha256:" & String$(64, "1")
    nextStateRevision = "sha256:" & String$(64, "2")
    thirdStateRevision = "sha256:" & String$(64, "5")
    fourthStateRevision = "sha256:" & String$(64, "6")
    currentPricingRevision = "sha256:" & String$(64, "3")
    nextPricingRevision = "sha256:" & String$(64, "4")

    Application.EnableEvents = False
    mInternalPricingRefresh = True
    settings.Range("G56").Value2 = currentStateRevision
    settings.Range("G14").Value2 = currentPricingRevision

    mPricingEventValidationMode = True
    mPricingEventValidationRefreshActive = False
    ' Model the one refresh already scheduled by Workbook_Open. The event
    ' fixtures below must not add a second load for completion or the revision
    ' that the cold load is already about to cover.
    mPricingEventValidationRefreshScheduled = True
    mPricingEventValidationConfirmationQueued = False
    mPricingEventValidationRefreshCount = 1
    mPricingEventValidationAppliedCount = 0
    ClearSseRefreshRequirement
    mForceFreshSnapshot = False
    mRefreshAfterSiteConfirmation = False
    mSseLastEventID = "100"
    mSseJobID = "job_cold_open"

    Set eventValue = PricingEventFixtureForValidation( _
        101, "source_changed", nextStateRevision, vbNullString, _
        True, True, "job_cold_open")
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        102, "snapshot_ready", nextStateRevision, _
        currentPricingRevision, False, True, "job_cold_open")
    HandlePricingSseEvent eventValue
    settings.Range("G56").Value2 = nextStateRevision
    Set eventValue = PricingEventFixtureForValidation( _
        103, "source_changed", nextStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        104, "catalog_changed", nextStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        105, "pricing_state_changed", nextStateRevision, _
        currentPricingRevision)
    HandlePricingSseEvent eventValue
    If Not mPricingEventValidationRefreshScheduled Or _
       mSseRefreshRequired Or mPricingEventValidationConfirmationQueued Or _
       mPricingEventValidationRefreshCount <> 1 Then _
        GoTo ValidationFailed
    RunEventDrivenRefresh
    If mPricingEventValidationAppliedCount <> 1 Or _
       mSseRefreshRequired Then GoTo ValidationFailed

    Set eventValue = PricingEventFixtureForValidation( _
        106, "catalog_changed", thirdStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        107, "source_changed", thirdStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    If Not mPricingEventValidationRefreshScheduled Or _
       Not mSseRefreshRequired Or Not mForceFreshSnapshot Or _
       mSseRefreshRevision <> thirdStateRevision Or _
       mPricingEventValidationRefreshCount <> 2 Then _
        GoTo ValidationFailed
    RunEventDrivenRefresh
    If mPricingEventValidationAppliedCount <> 2 Or _
       mSseRefreshRequired Then GoTo ValidationFailed

    ' A semantic event received while a refresh is active is retained, not
    ' allowed to abort that request. If the terminal commit contains the same
    ' composite revision, re-evaluation clears it without a successor.
    mPricingEventValidationRefreshActive = True
    Set eventValue = PricingEventFixtureForValidation( _
        108, "source_changed", thirdStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    If Not mSseRefreshRequired Or _
       mSseRefreshRevision <> thirdStateRevision Or _
       mPricingEventValidationRefreshScheduled Then _
        GoTo ValidationFailed
    mSseJobID = "job_active_refresh"
    Set eventValue = PricingEventFixtureForValidation( _
        109, "snapshot_ready", thirdStateRevision, _
        currentPricingRevision, False, True, "job_active_refresh")
    HandlePricingSseEvent eventValue
    settings.Range("G56").Value2 = thirdStateRevision
    mPricingEventValidationRefreshActive = False
    mForceFreshSnapshot = False
    ReevaluateSseRefreshRequirement
    If mSseRefreshRequired Or mPricingEventValidationRefreshScheduled Or _
       mPricingEventValidationRefreshCount <> 2 Then _
        GoTo ValidationFailed

    ' Active events are ordered semantic state. If the source moves from the
    ' pre-refresh revision to a new revision and then rolls back, retain the
    ' rollback even though it still equals G56 before the active commit. After
    ' that commit moves G56 forward, exactly one successor applies the rollback.
    mPricingEventValidationRefreshActive = True
    Set eventValue = PricingEventFixtureForValidation( _
        110, "catalog_changed", fourthStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        111, "source_changed", fourthStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    Set eventValue = PricingEventFixtureForValidation( _
        112, "source_changed", thirdStateRevision, vbNullString)
    HandlePricingSseEvent eventValue
    If mPricingEventValidationRefreshScheduled Or _
       mSseRefreshRevision <> thirdStateRevision Then _
        GoTo ValidationFailed
    settings.Range("G56").Value2 = fourthStateRevision
    mPricingEventValidationRefreshActive = False
    ReevaluateSseRefreshRequirement
    If Not mPricingEventValidationRefreshScheduled Or _
       mPricingEventValidationRefreshCount <> 3 Then _
        GoTo ValidationFailed
    RunEventDrivenRefresh
    settings.Range("G56").Value2 = thirdStateRevision
    mForceFreshSnapshot = False
    If mPricingEventValidationAppliedCount <> 3 Or _
       mSseRefreshRequired Then GoTo ValidationFailed

    Set eventValue = PricingEventFixtureForValidation( _
        113, "pricing_state_changed", thirdStateRevision, _
        currentPricingRevision)
    HandlePricingSseEvent eventValue
    If mPricingEventValidationConfirmationQueued Or _
       mPricingEventValidationRefreshScheduled Then _
        GoTo ValidationFailed
    Set eventValue = PricingEventFixtureForValidation( _
        114, "pricing_state_changed", thirdStateRevision, _
        nextPricingRevision)
    HandlePricingSseEvent eventValue
    If Not mPricingEventValidationConfirmationQueued Or _
       mPricingEventValidationRefreshScheduled Then _
        GoTo ValidationFailed

    Set eventValue = PricingEventFixtureForValidation( _
        115, "pricing_state_invalidated", nextPricingRevision, _
        vbNullString)
    HandlePricingSseEvent eventValue
    If Not mPricingEventValidationRefreshScheduled Or _
       mPricingEventValidationRefreshCount <> 4 Then _
        GoTo ValidationFailed
    RunEventDrivenRefresh
    If mPricingEventValidationAppliedCount <> 4 Or _
       mSseLastEventID <> "115" Or _
       CStr(settings.Range("G56").Value2) <> thirdStateRevision Or _
       CStr(settings.Range("G14").Value2) <> currentPricingRevision Then _
        GoTo ValidationFailed

    ValidatePricingEventAutoPullForValidation = True
    GoTo ValidationExit

ValidationFailed:
    ValidatePricingEventAutoPullForValidation = False

ValidationExit:
    On Error Resume Next
    mPricingEventValidationMode = False
    If cellStateCaptured And Not settings Is Nothing Then
        Application.EnableEvents = False
        mInternalPricingRefresh = True
        settings.Range("G56").Value2 = originalStateRevision
        settings.Range("G14").Value2 = originalPricingRevision
    End If
    If runtimeStateCaptured Then
        mInternalPricingRefresh = originalInternalRefresh
        Application.EnableEvents = originalEvents
        mSseLastEventID = originalLastEventID
        mSseJobID = originalSseJobID
        mSseRefreshRequired = originalRefreshRequired
        mSseRefreshRevision = originalRefreshRevision
        mForceFreshSnapshot = originalForceFreshSnapshot
        mRefreshAfterSiteConfirmation = originalRefreshAfterConfirmation
        mPricingEventValidationRefreshActive = _
            originalValidationRefreshActive
    End If
    mPricingEventValidationRefreshScheduled = False
    mPricingEventValidationConfirmationQueued = False
    mPricingEventValidationRefreshCount = 0
    mPricingEventValidationAppliedCount = 0
    If runtimeStateCaptured And originalSaved Then ThisWorkbook.Saved = True
    On Error GoTo 0
End Function

Public Sub RegisterSearchHotkey()
    Dim activeBook As Workbook
    Dim workbookMacro As String
    Dim nextMacro As String

    If mWorkbookClosing Then Exit Sub
    Set activeBook = Application.ActiveWorkbook
    If activeBook Is Nothing Then Exit Sub
    If Not activeBook Is ThisWorkbook Then Exit Sub
    workbookMacro = "'" & Replace(ThisWorkbook.Name, "'", "''") & _
        "'!ProductCatalogSync.FocusProductSearch"
    nextMacro = "'" & Replace(ThisWorkbook.Name, "'", "''") & _
        "'!ProductCatalogSync.SearchProducts"
    Application.OnKey "{F2}", workbookMacro
    Application.OnKey "{F3}", nextMacro
    RefreshSearchEnterHotkey
End Sub

Public Sub UnregisterSearchHotkey()
    Application.OnKey "{F2}"
    Application.OnKey "{F3}"
    ReleaseSearchPasteHotkey
    ReleaseSearchEnterHotkey
End Sub

Public Sub RefreshSearchEnterHotkey()
    ' Enter remains Excel-native. Committing the search cell raises SheetChange,
    ' which queues one top-level search after edit mode has ended.
    RefreshSearchPasteHotkey
End Sub

Public Function NormalizeProductSearchSelection( _
        ByRef target As Range) As Boolean
    Dim searchAnchor As Range
    Dim searchSurface As Range
    Dim previousEvents As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    If target Is Nothing Then Exit Function
    Set searchAnchor = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange
    Set searchSurface = PriceSheet().Range("C3:E3")
    If Intersect(target.Cells(1, 1), searchSurface) Is Nothing Then _
        Exit Function
    If Not Intersect(target.Cells(1, 1), searchAnchor) Is Nothing Then _
        Exit Function

    Application.EnableEvents = False
    searchAnchor.Select
    RememberSearchSelection searchAnchor
    RegisterSearchPasteHotkey
    NormalizeProductSearchSelection = True
CleanExit:
    Application.EnableEvents = previousEvents
End Function

Public Function UpdateSearchEnterHotkey(ByVal target As Range) As Boolean
    Dim queueNativeEnter As Boolean
    Dim searchInput As Range

    ' Enter remains native. Detect only the completed selection movement after
    ' Excel has left edit mode, then queue one top-level callback. Never bind or
    ' mutate Enter from this SelectionChange path: doing so used to recurse
    ' through Excel/VBA until the native stack was exhausted.
    UpdateSearchPasteHotkey target
    queueNativeEnter = IsNativeSearchEnterTransition(target)
    RememberSearchSelection target
    If queueNativeEnter Then
        RunNativeEnterProductSearch
        UpdateSearchEnterHotkey = True
        Exit Function
    End If
    On Error Resume Next
    Set searchInput = ThisWorkbook.Names("ProductSearchQuery"). _
        RefersToRange.MergeArea
    On Error GoTo 0
    If Not searchInput Is Nothing And Not target Is Nothing Then
        If Not Intersect(target.Cells(1, 1), searchInput) Is Nothing Then _
            UpdateSearchEnterHotkey = True
    End If
End Function

Private Sub RunNativeEnterProductSearch()
    Dim query As String
    Dim generation As Long

    ' SelectionChange is already a top-level Excel event. SearchProductsForQuery
    ' disables events before selecting the result, so this direct native-Enter
    ' path cannot re-enter SelectionChange. It also cancels the SheetChange
    ' timer, preventing a delayed duplicate search or a one-query lag.
    On Error GoTo CleanExit
    CancelScheduledProductSearch
    mSearchNativeEnterPending = False
    mSearchRequestGeneration = mSearchRequestGeneration + 1
    generation = mSearchRequestGeneration
    query = ReadSearchLiteral()
    mPendingSearchQuery = query
    mPendingSearchGeneration = generation
    SearchProductsForQuery query, generation
CleanExit:
End Sub

Public Sub ReleaseSearchEnterHotkey()
    ' Enter is never rebound. This compatibility entry point intentionally
    ' remains a no-op for existing workbook lifecycle calls.
End Sub

Private Sub RefreshSearchPasteHotkey()
    Dim activeBook As Workbook
    Dim activeCellValue As Range

    On Error GoTo ReleaseBinding
    Set activeBook = Application.ActiveWorkbook
    If activeBook Is Nothing Then GoTo ReleaseBinding
    If Not activeBook Is ThisWorkbook Then GoTo ReleaseBinding
    Set activeCellValue = Application.ActiveCell
    UpdateSearchPasteHotkey activeCellValue
    Exit Sub

ReleaseBinding:
    ReleaseSearchPasteHotkey
End Sub

Private Sub UpdateSearchPasteHotkey(ByVal target As Range)
    Dim searchSurface As Range

    On Error GoTo ReleaseBinding
    If target Is Nothing Then GoTo ReleaseBinding
    If Not target.Worksheet Is PriceSheet() Then GoTo ReleaseBinding
    Set searchSurface = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange.MergeArea
    If Intersect(target.Cells(1, 1), searchSurface) Is Nothing Then _
        GoTo ReleaseBinding
    RegisterSearchPasteHotkey
    Exit Sub

ReleaseBinding:
    ReleaseSearchPasteHotkey
End Sub

Private Sub RegisterSearchPasteHotkey()
    Dim pasteMacro As String

    If mSearchPasteHotkeyRegistered Then Exit Sub
    pasteMacro = QualifiedWorkbookMacro("PasteProductSearchText")
    Application.OnKey "^v", pasteMacro
    Application.OnKey "+{INSERT}", pasteMacro
    mSearchPasteHotkeyRegistered = True
End Sub

Public Sub ReleaseSearchPasteHotkey()
    If Not mSearchPasteHotkeyRegistered Then Exit Sub
    On Error Resume Next
    Application.OnKey "^v"
    Application.OnKey "+{INSERT}"
    mSearchPasteHotkeyRegistered = False
    On Error GoTo 0
End Sub

Public Sub PasteProductSearchText()
    Dim activeBook As Workbook
    Dim activeCellValue As Range
    Dim searchSurface As Range
    Dim clipboardText As String

    On Error GoTo CleanExit
    Set activeBook = Application.ActiveWorkbook
    If activeBook Is Nothing Then Exit Sub
    If Not activeBook Is ThisWorkbook Then Exit Sub
    Set activeCellValue = Application.ActiveCell
    If activeCellValue Is Nothing Then Exit Sub
    Set searchSurface = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange.MergeArea
    If Not activeCellValue.Worksheet Is searchSurface.Worksheet Then Exit Sub
    If Intersect(activeCellValue.Cells(1, 1), searchSurface) Is Nothing Then _
        Exit Sub
    If Not TryReadUnicodeClipboardText(clipboardText) Then Exit Sub
    If ApplyProductSearchPasteText(clipboardText, True) Then _
        RegisterSearchPasteHotkey
CleanExit:
End Sub

Private Function ApplyProductSearchPasteText(ByVal plainText As String, _
        ByVal queueSearch As Boolean) As Boolean
    Dim searchAnchor As Range
    Dim previousEvents As Boolean
    Dim writeSucceeded As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    Set searchAnchor = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange
    Application.EnableEvents = False
    searchAnchor.MergeArea.NumberFormat = "@"
    searchAnchor.Value2 = NormalizeProductSearchPasteText(plainText)
    RememberSearchSelection searchAnchor
    writeSucceeded = True
CleanExit:
    Application.EnableEvents = previousEvents
    If writeSucceeded And queueSearch Then QueueProductSearch
    ApplyProductSearchPasteText = writeSucceeded
End Function

Private Function NormalizeProductSearchPasteText( _
        ByVal plainText As String) As String
    Dim normalized As String

    normalized = Left$(plainText, SEARCH_PASTE_MAX_CHARS)
    normalized = Replace(normalized, vbNullChar, vbNullString)
    normalized = Replace(normalized, vbCrLf, " ")
    normalized = Replace(normalized, vbCr, " ")
    normalized = Replace(normalized, vbLf, " ")
    normalized = Replace(normalized, vbTab, " ")
    Do While InStr(1, normalized, "  ", vbBinaryCompare) > 0
        normalized = Replace(normalized, "  ", " ")
    Loop
    NormalizeProductSearchPasteText = Trim$(normalized)
End Function

Private Function TryReadUnicodeClipboardText( _
        ByRef clipboardText As String) As Boolean
    Dim clipboardOpened As Boolean
    Dim characterCount As Long
#If VBA7 Then
    Dim dataHandle As LongPtr
    Dim dataPointer As LongPtr
#Else
    Dim dataHandle As Long
    Dim dataPointer As Long
#End If

    On Error GoTo CleanExit
    If IsClipboardFormatAvailable(CF_UNICODETEXT) = 0 Then Exit Function
    If OpenClipboard(Application.hwnd) = 0 Then Exit Function
    clipboardOpened = True
    dataHandle = GetClipboardData(CF_UNICODETEXT)
    If dataHandle = 0 Then GoTo CleanExit
    dataPointer = GlobalLock(dataHandle)
    If dataPointer = 0 Then GoTo CleanExit
    characterCount = lstrlenW(dataPointer)
    If characterCount > SEARCH_PASTE_MAX_CHARS Then _
        characterCount = SEARCH_PASTE_MAX_CHARS
    If characterCount > 0 Then
        clipboardText = String$(characterCount, vbNullChar)
        CopyMemory StrPtr(clipboardText), dataPointer, characterCount * 2
    Else
        clipboardText = vbNullString
    End If
    TryReadUnicodeClipboardText = True
CleanExit:
    On Error Resume Next
    If dataPointer <> 0 Then GlobalUnlock dataHandle
    If clipboardOpened Then CloseClipboard
    On Error GoTo 0
End Function

Public Function ValidateProductSearchPasteForValidation() As Boolean
    Dim searchAnchor As Range
    Dim originalValue As Variant
    Dim previousEvents As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo Failed
    Set searchAnchor = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange
    originalValue = searchAnchor.Value2
    If Not ApplyProductSearchPasteText( _
        "001234" & vbCrLf & vbTab, False) Then GoTo Failed
    If ReadSearchLiteral() <> "001234" Or _
       searchAnchor.MergeArea.Address(False, False) <> "C3:E3" Or _
       CStr(searchAnchor.MergeArea.NumberFormat) <> "@" Then GoTo Failed
    Application.EnableEvents = False
    searchAnchor.Value2 = originalValue
    Application.EnableEvents = previousEvents
    ValidateProductSearchPasteForValidation = True
    Exit Function

Failed:
    On Error Resume Next
    Application.EnableEvents = False
    If Not searchAnchor Is Nothing Then searchAnchor.Value2 = originalValue
    Application.EnableEvents = previousEvents
    On Error GoTo 0
End Function

Public Sub HandleProductSearchEnter()
    QueueProductSearch
End Sub

Private Function IsProductSearchEnterTarget(ByVal target As Range) As Boolean
    Dim searchInput As Range
    Dim table As ListObject
    Dim currentPriceCell As Range

    On Error GoTo CleanExit
    If target Is Nothing Then Exit Function
    Set searchInput = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange.MergeArea
    If Not Intersect(target.Cells(1, 1), searchInput) Is Nothing Then
        IsProductSearchEnterTarget = True
        Exit Function
    End If
    If mSearchCurrentRow <= 0 Then Exit Function
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Function
    If mSearchCurrentRow > table.DataBodyRange.Rows.Count Then Exit Function
    Set currentPriceCell = table.DataBodyRange.Cells(mSearchCurrentRow, 1)
    IsProductSearchEnterTarget = _
        Not Intersect(target.Cells(1, 1), currentPriceCell) Is Nothing
CleanExit:
End Function

Public Sub FocusProductSearch()
    Dim searchInput As Range
    Dim table As ListObject

    If mCatalogCommitInProgress Then Exit Sub
    On Error GoTo CleanExit
    PriceSheet().Activate
    Set searchInput = ThisWorkbook.Names("ProductSearchQuery").RefersToRange
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    searchInput.MergeArea.NumberFormat = "@"
    searchInput.Select
    RegisterSearchPasteHotkey
    ActiveWindow.ScrollColumn = ProductViewportColumn(table)
CleanExit:
End Sub

Public Sub QueueProductSearch()
    On Error GoTo CleanExit
    mSearchNativeEnterPending = True
    mSearchRequestGeneration = mSearchRequestGeneration + 1
    mPendingSearchQuery = ReadSearchLiteral()
    mPendingSearchGeneration = mSearchRequestGeneration
    mSearchRetryCount = 0
    SchedulePendingProductSearch
CleanExit:
End Sub

Private Sub SchedulePendingProductSearch()
    If mWorkbookClosing Then Exit Sub
    CancelScheduledProductSearch
    ' Application.OnTime can drop a callback scheduled at the already-passed
    ' exact value of Now. A sub-second future tick remains locally immediate
    ' while guaranteeing execution outside SheetChange/SelectionChange.
    mScheduledSearchTime = Now + (SEARCH_DELAY_SECONDS / 86400#)
    Application.OnTime EarliestTime:=mScheduledSearchTime, _
        Procedure:=QualifiedWorkbookMacro("RunScheduledProductSearch"), _
        Schedule:=True
    mSearchScheduled = True
End Sub

Public Sub RunScheduledProductSearch()
    Dim queuedQuery As String
    Dim queuedGeneration As Long

    mSearchScheduled = False
    mSearchNativeEnterPending = False
    If mWorkbookClosing Then Exit Sub
    If SearchOperationBusy() Then
        mSearchRetryCount = mSearchRetryCount + 1
        If mSearchRetryCount <= SEARCH_RETRY_LIMIT Then
            SchedulePendingProductSearch
        Else
            SetOperationProgressSurface "search_wait", -1, _
                U("062F06310020062D0627064400200627063906450627064400200631062F06CC0641200C0647062706CC0020062C062F06480644061B0020062C0633062A200C0648062C06480020062806440627064106270635064406470020067E06330020062706320020067E062706CC062706460020062706CC0646002006450631062D0644064700200627062C063106270020064506CC200C06340648062F002E"), _
                "warning", True
        End If
        Exit Sub
    End If
    mSearchRetryCount = 0
    queuedQuery = mPendingSearchQuery
    queuedGeneration = mPendingSearchGeneration
    SearchProductsForQuery queuedQuery, queuedGeneration
End Sub

Public Sub CancelScheduledProductSearch()
    If Not mSearchScheduled Then Exit Sub
    On Error Resume Next
    Application.OnTime EarliestTime:=mScheduledSearchTime, _
        Procedure:=QualifiedWorkbookMacro("RunScheduledProductSearch"), _
        Schedule:=False
    mSearchScheduled = False
    On Error GoTo 0
End Sub

Private Function SearchOperationBusy() As Boolean
    ' Network refresh, pricing delivery, ACK, and event callbacks do not own
    ' the local table. Search stays immediate during those background stages.
    ' Only the short atomic row commit and another search hold the local read
    ' surface; this avoids both stale results and generic operation queuing.
    If mCatalogCommitInProgress Or mSearchInProgress Then
        SearchOperationBusy = True
    End If
End Function

Public Sub SearchProducts()
    mSearchNativeEnterPending = False
    SearchProductsForQuery ReadSearchLiteral(), 0
End Sub

Private Sub SearchProductsForQuery(ByVal requestedQuery As String, _
                                   ByVal requestGeneration As Long)
    Dim table As ListObject
    Dim query As String
    Dim matches As Collection
    Dim anchor As Range
    Dim matchIndex As Long
    Dim matchCount As Long
    Dim rowIndex As Long
    Dim eventsWereEnabled As Boolean
    Dim screenWasUpdating As Boolean
    Dim previousEnableCancelKey As XlEnableCancelKey
    Dim cancelKeyCaptured As Boolean

    ResumeAfterCancelledClose
    If SearchOperationBusy() Then
        If requestGeneration = 0 Then
            mSearchRequestGeneration = mSearchRequestGeneration + 1
            requestGeneration = mSearchRequestGeneration
            requestedQuery = ReadSearchLiteral()
        End If
        mPendingSearchQuery = requestedQuery
        mPendingSearchGeneration = requestGeneration
        SchedulePendingProductSearch
        Exit Sub
    End If
    If requestGeneration > 0 And _
       requestGeneration <> mPendingSearchGeneration Then Exit Sub
    eventsWereEnabled = Application.EnableEvents
    screenWasUpdating = Application.ScreenUpdating
    On Error GoTo CleanExit
    CancelScheduledProductSearch
    mSearchInProgress = True
    previousEnableCancelKey = Application.EnableCancelKey
    cancelKeyCaptured = True
    Application.EnableCancelKey = xlErrorHandler
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    query = requestedQuery
    If Len(query) = 0 Then
        ResetProductSearchState False
        FocusProductSearch
        GoTo CleanExit
    End If
    If table.DataBodyRange Is Nothing Then
        ResetProductSearchState False
        SetSearchButtonCaption T("search_button") & " (0)"
        GoTo CleanExit
    End If

    If StrComp(query, mSearchQuery, vbTextCompare) <> 0 Then
        mSearchCurrentRow = 0
    End If
    Set matches = ProductSearchMatchRows(table, query)
    matchCount = matches.Count
    mSearchQuery = query

    If matchCount = 0 Then
        mSearchCurrentRow = 0
        HighlightSelectedProductRow PriceSheet().Range("A1")
        SetSearchButtonCaption T("search_button") & " (0)"
    Else
        matchIndex = NextProductSearchMatchIndex( _
            matches, mSearchCurrentRow)
        rowIndex = CLng(matches.Item(matchIndex))
        mSearchCurrentRow = rowIndex
        SetSearchButtonCaption T("search_button") & " (" & _
            CStr(matchIndex) & "/" & CStr(matchCount) & ")"
        Set anchor = table.DataBodyRange.Cells(rowIndex, 1)
        PriceSheet().Activate
        Application.EnableEvents = False
        Application.ScreenUpdating = False
        anchor.Select
        RememberSearchSelection anchor
        HighlightSelectedProductRow anchor
        ActiveWindow.ScrollColumn = ProductViewportColumn(table)
        ActiveWindow.ScrollRow = Application.Max(1, anchor.Row - 3)
    End If
CleanExit:
    On Error Resume Next
    If cancelKeyCaptured Then
        Application.EnableCancelKey = previousEnableCancelKey
    End If
    Application.EnableEvents = eventsWereEnabled
    Application.ScreenUpdating = screenWasUpdating
    mSearchInProgress = False
    On Error GoTo 0
End Sub

Public Sub ClearProductSearch()
    ResumeAfterCancelledClose
    If mCatalogCommitInProgress Or mSearchInProgress Then Exit Sub
    On Error Resume Next
    CancelScheduledProductSearch
    ResetProductSearchState True
    FocusProductSearch
    On Error GoTo 0
End Sub

Private Function ProductSearchMatchRows(ByVal table As ListObject, _
                                        ByVal query As String) As Collection
    Dim matches As New Collection
    Dim rowIndex As Long
    Dim normalizedQuery As String
    Dim filteredView As Boolean
    Dim visibleRows As Object
    Dim rowIsVisible As Boolean

    If table.DataBodyRange Is Nothing Then
        Set ProductSearchMatchRows = matches
        Exit Function
    End If
    normalizedQuery = NormalizeProductSearchText(query)
    If Len(normalizedQuery) = 0 Then
        Set ProductSearchMatchRows = matches
        Exit Function
    End If
    EnsureProductSearchIndex table
    filteredView = table.Parent.FilterMode
    If filteredView Then Set visibleRows = VisibleProductRowMap(table)
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        rowIsVisible = True
        If filteredView Then _
            rowIsVisible = visibleRows.Exists(CStr(rowIndex))
        If rowIsVisible Then
            If (Len(mSearchIndexProductCodes(rowIndex)) > 0 And _
                mSearchIndexProductCodes(rowIndex) = normalizedQuery) Or _
               (Len(mSearchIndexWooIDs(rowIndex)) > 0 And _
                mSearchIndexWooIDs(rowIndex) = normalizedQuery) Or _
               (Len(mSearchIndexNames(rowIndex)) > 0 And _
                InStr(1, mSearchIndexNames(rowIndex), normalizedQuery, _
                      vbBinaryCompare) > 0) Then
                matches.Add rowIndex
            End If
        End If
    Next rowIndex
    Set ProductSearchMatchRows = matches
End Function

Private Sub WarmProductSearchIndex()
    On Error GoTo WarmFailed
    EnsureProductSearchIndex PriceSheet().ListObjects(PRODUCTS_TABLE)
    Exit Sub
WarmFailed:
    InvalidateProductSearchIndex
    Err.Clear
End Sub

Private Sub EnsureProductSearchIndex(ByVal table As ListObject)
    Dim rowCount As Long
    Dim rowIndex As Long
    Dim revision As String
    Dim productCodes As Variant
    Dim productNames As Variant
    Dim wooIDs As Variant

    If table.DataBodyRange Is Nothing Then
        InvalidateProductSearchIndex
        Exit Sub
    End If
    rowCount = table.DataBodyRange.Rows.Count
    revision = Trim$(CStr(ConfigSheet().Range("G44").Value2))
    If mSearchIndexReady And mSearchIndexRowCount = rowCount And _
       mSearchIndexRevision = revision Then Exit Sub

    InvalidateProductSearchIndex
    productCodes = table.DataBodyRange.Columns(7).Value2
    productNames = table.DataBodyRange.Columns(8).Value2
    wooIDs = table.DataBodyRange.Columns(9).Value2
    ReDim mSearchIndexProductCodes(1 To rowCount)
    ReDim mSearchIndexNames(1 To rowCount)
    ReDim mSearchIndexWooIDs(1 To rowCount)
    For rowIndex = 1 To rowCount
        mSearchIndexProductCodes(rowIndex) = _
            SearchCellText(productCodes(rowIndex, 1))
        mSearchIndexNames(rowIndex) = _
            SearchCellText(productNames(rowIndex, 1))
        mSearchIndexWooIDs(rowIndex) = _
            SearchCellText(wooIDs(rowIndex, 1))
    Next rowIndex
    mSearchIndexRowCount = rowCount
    mSearchIndexRevision = revision
    mSearchIndexReady = True
End Sub

Private Sub InvalidateProductSearchIndex()
    mSearchIndexReady = False
    mSearchIndexRowCount = 0
    mSearchIndexRevision = vbNullString
    Erase mSearchIndexProductCodes
    Erase mSearchIndexNames
    Erase mSearchIndexWooIDs
End Sub

Private Function VisibleProductRowMap(ByVal table As ListObject) As Object
    Dim visibleRows As Object
    Dim visibleRange As Range
    Dim area As Range
    Dim firstTableRow As Long
    Dim firstVisibleIndex As Long
    Dim offset As Long

    Set visibleRows = CreateObject("Scripting.Dictionary")
    On Error GoTo CleanExit
    firstTableRow = table.DataBodyRange.Row
    Set visibleRange = table.DataBodyRange.Columns(1). _
        SpecialCells(xlCellTypeVisible)
    For Each area In visibleRange.Areas
        firstVisibleIndex = area.Row - firstTableRow + 1
        For offset = 0 To area.Rows.Count - 1
            visibleRows(CStr(firstVisibleIndex + offset)) = True
        Next offset
    Next area
CleanExit:
    Set VisibleProductRowMap = visibleRows
End Function

Private Function SearchCellText(ByVal value As Variant) As String
    If IsError(value) Or IsNull(value) Or IsEmpty(value) Then Exit Function
    SearchCellText = NormalizeProductSearchText(CStr(value))
End Function

Private Function NormalizeProductSearchText(ByVal value As String) As String
    Dim digitIndex As Long

    value = LCase$(Trim$(value))
    value = Replace(value, ChrW(&H64A), ChrW(&H6CC))
    value = Replace(value, ChrW(&H643), ChrW(&H6A9))
    value = Replace(value, ChrW(&H200C), " ")
    value = Replace(value, ChrW(&HA0), " ")
    For digitIndex = 0 To 9
        value = Replace(value, ChrW(&H6F0 + digitIndex), _
                        CStr(digitIndex))
        value = Replace(value, ChrW(&H660 + digitIndex), _
                        CStr(digitIndex))
    Next digitIndex
    Do While InStr(1, value, "  ", vbBinaryCompare) > 0
        value = Replace(value, "  ", " ")
    Loop
    NormalizeProductSearchText = Trim$(value)
End Function

Private Function IsNativeSearchEnterTransition(ByVal target As Range) As Boolean
    Dim currentCell As Range
    Dim searchInput As Range
    Dim table As ListObject
    Dim currentPriceCell As Range
    Dim currentSheetName As String

    On Error GoTo CleanExit
    If Not mSearchNativeEnterPending Then Exit Function
    If target Is Nothing Then Exit Function
    Set currentCell = target.Cells(1, 1)
    currentSheetName = currentCell.Worksheet.CodeName
    If currentSheetName <> mLastSearchSelectionSheet Then Exit Function
    If currentCell.Column <> mLastSearchSelectionColumn Or _
       currentCell.Row <> mLastSearchSelectionRow + 1 Then Exit Function
    If Len(ReadSearchLiteral()) = 0 Then Exit Function

    Set searchInput = ThisWorkbook.Names( _
        "ProductSearchQuery").RefersToRange.MergeArea
    If mLastSearchSelectionRow = searchInput.Row And _
       mLastSearchSelectionColumn = searchInput.Column Then
        IsNativeSearchEnterTransition = True
        Exit Function
    End If
    If mSearchCurrentRow <= 0 Or _
       mLastSearchSelectionSheet <> PriceSheet().CodeName Then Exit Function
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Or _
       mSearchCurrentRow > table.DataBodyRange.Rows.Count Then Exit Function
    Set currentPriceCell = table.DataBodyRange.Cells(mSearchCurrentRow, 1)
    If mLastSearchSelectionRow = currentPriceCell.Row And _
       mLastSearchSelectionColumn = currentPriceCell.Column Then _
        IsNativeSearchEnterTransition = True
CleanExit:
End Function

Private Sub RememberSearchSelection(ByVal target As Range)
    On Error GoTo CleanExit
    If target Is Nothing Then Exit Sub
    mLastSearchSelectionSheet = target.Cells(1, 1).Worksheet.CodeName
    mLastSearchSelectionRow = target.Cells(1, 1).Row
    mLastSearchSelectionColumn = target.Cells(1, 1).Column
CleanExit:
End Sub

Private Function ProductRowMatchesQuery(ByVal productRow As Range, _
                                        ByVal query As String) As Boolean
    Dim normalizedQuery As String
    Dim normalizedName As String
    Dim normalizedProductCode As String
    Dim normalizedWooID As String

    normalizedQuery = NormalizeProductSearchText(query)
    If Len(normalizedQuery) = 0 Or productRow.Columns.Count < 9 Then _
        Exit Function
    normalizedProductCode = SearchCellText(productRow.Cells(1, 7).Value2)
    normalizedName = SearchCellText(productRow.Cells(1, 8).Value2)
    normalizedWooID = SearchCellText(productRow.Cells(1, 9).Value2)
    ProductRowMatchesQuery = _
        (Len(normalizedProductCode) > 0 And _
         normalizedProductCode = normalizedQuery) Or _
        (Len(normalizedWooID) > 0 And _
         normalizedWooID = normalizedQuery) Or _
        (Len(normalizedName) > 0 And _
         InStr(1, normalizedName, normalizedQuery, vbBinaryCompare) > 0)
End Function

Private Function NextProductSearchMatchIndex(ByVal matches As Collection, _
                                             ByVal currentRow As Long) As Long
    Dim matchIndex As Long

    For matchIndex = 1 To matches.Count
        If CLng(matches.Item(matchIndex)) > currentRow Then
            NextProductSearchMatchIndex = matchIndex
            Exit Function
        End If
    Next matchIndex
    NextProductSearchMatchIndex = 1
End Function

Private Function ProductViewportColumn(ByVal table As ListObject) As Long
    ProductViewportColumn = Application.Max(1, table.Range.Column - 1)
End Function

Private Sub ResetProductSearchState( _
        Optional ByVal clearQuery As Boolean = False)
    Dim previousEvents As Boolean

    mSearchQuery = vbNullString
    mSearchCurrentRow = 0
    If clearQuery Then
        previousEvents = Application.EnableEvents
        Application.EnableEvents = False
        On Error Resume Next
        ThisWorkbook.Names("ProductSearchQuery").RefersToRange. _
            MergeArea.NumberFormat = "@"
        ThisWorkbook.Names("ProductSearchQuery").RefersToRange. _
            MergeArea.ClearContents
        On Error GoTo 0
        Application.EnableEvents = previousEvents
    End If
    SetSearchButtonCaption T("search_button")
End Sub

Private Sub SetSearchButtonCaption(ByVal caption As String)
    Dim searchButton As Shape

    On Error GoTo CleanExit
    Set searchButton = PriceSheet().Shapes(SEARCH_BUTTON_SHAPE)
    searchButton.TextFrame2.TextRange.Text = caption
    searchButton.TextFrame2.TextRange.Font.Name = NamedText( _
        "PersianFont", DEFAULT_PERSIAN_FONT)
    searchButton.TextFrame2.TextRange.Font.NameComplexScript = NamedText( _
        "PersianFont", DEFAULT_PERSIAN_FONT)
    searchButton.TextFrame2.TextRange.Font.NameFarEast = NamedText( _
        "PersianFont", DEFAULT_PERSIAN_FONT)
    searchButton.TextFrame2.TextRange.LanguageID = 1065
    searchButton.TextFrame.Characters.Font.Name = NamedText( _
        "PersianFont", DEFAULT_PERSIAN_FONT)
CleanExit:
End Sub

Public Sub HighlightSelectedProductRow(ByVal target As Range)
    Dim table As ListObject
    Dim settings As Worksheet
    Dim selectedRow As Long
    Dim relativeRow As Long
    Dim previewRow As Long
    Dim currentSelectedRow As Long
    Dim currentPreviewRow As Long
    Dim previousEvents As Boolean

    If mCatalogCommitInProgress Then Exit Sub
    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    If target Is Nothing Then Exit Sub
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set settings = ConfigSheet()
    If Not table.DataBodyRange Is Nothing Then
        If Not Intersect(target.Cells(1, 1), table.DataBodyRange) Is Nothing Then
            selectedRow = target.Row
            relativeRow = selectedRow - table.DataBodyRange.Row + 1
        End If
    End If
    currentSelectedRow = CLng(Val(CStr(settings.Range("G30").Value2)))
    currentPreviewRow = CLng(Val(CStr(settings.Range("G48").Value2)))
    If relativeRow > 0 Then
        If currentPreviewRow = selectedRow Then
            previewRow = selectedRow
        Else
            previewRow = ProductPreviewCandidateRow(table, relativeRow)
        End If
    End If
    Application.EnableEvents = False
    If currentSelectedRow <> selectedRow Then _
        settings.Range("G30").Value2 = selectedRow
    If currentPreviewRow <> previewRow Then _
        settings.Range("G48").Value2 = previewRow
CleanExit:
    Application.EnableEvents = previousEvents
End Sub

Private Function ProductPreviewCandidateRow(ByVal table As ListObject, _
        ByVal relativeRow As Long) As Long
    Dim basePrice As Variant
    Dim stockValue As Variant

    If table.DataBodyRange Is Nothing Then Exit Function
    If relativeRow < 1 Or relativeRow > table.DataBodyRange.Rows.Count Then _
        Exit Function
    basePrice = table.DataBodyRange.Cells(relativeRow, 1).Value2
    stockValue = table.DataBodyRange.Cells(relativeRow, 6).Value2
    If IsError(basePrice) Or IsError(stockValue) Then Exit Function
    If Len(CanonicalCellText(basePrice)) > 0 Or _
       Len(CanonicalCellText(stockValue)) = 0 Or _
       Not IsNumeric(stockValue) Then Exit Function
    If CDbl(stockValue) <= 0 Then _
        ProductPreviewCandidateRow = table.DataBodyRange.Row + relativeRow - 1
End Function

Public Sub HandleProductImageSettingChanged()
    CancelScheduledProductImagePreview
    RefreshProductImagePreview SelectedProductRelativeRow()
End Sub

Public Sub QueueProductImagePreviewForSelection(ByVal target As Range)
    Dim table As ListObject
    Dim relativeRow As Long

    On Error GoTo CleanExit
    If mWorkbookClosing Then Exit Sub
    If Not ProductImagesEnabled() Then
        CancelScheduledProductImagePreview
        Exit Sub
    End If
    If Not target Is Nothing Then
        Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
        If Not table.DataBodyRange Is Nothing Then
            If Not Intersect(target.Cells(1, 1), _
                             table.DataBodyRange) Is Nothing Then
                relativeRow = target.Row - table.DataBodyRange.Row + 1
            End If
        End If
    End If
    mPendingImagePreviewRow = relativeRow
    ScheduleProductImagePreview
CleanExit:
End Sub

Private Sub ScheduleProductImagePreview()
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameImagePreviewPending = True
        Exit Sub
    End If
    If mWorkbookClosing Then Exit Sub
    CancelScheduledProductImagePreview
    On Error GoTo ScheduleFailed
    mScheduledImagePreviewTime = Now + _
        (IMAGE_PREVIEW_DELAY_SECONDS / 86400#)
    Application.OnTime EarliestTime:=mScheduledImagePreviewTime, _
        Procedure:=QualifiedWorkbookMacro( _
            "RunScheduledProductImagePreview"), _
        Schedule:=True
    mImagePreviewScheduled = True
    Exit Sub

ScheduleFailed:
    mImagePreviewScheduled = False
    Err.Clear
End Sub

Public Sub RunScheduledProductImagePreview()
    Dim pendingRow As Long

    mImagePreviewScheduled = False
    If mWorkbookClosing Then Exit Sub
    pendingRow = mPendingImagePreviewRow
    If pendingRow <> SelectedProductRelativeRow() Then Exit Sub
    RefreshProductImagePreview pendingRow
End Sub

Private Sub CancelScheduledProductImagePreview()
    If Not mImagePreviewScheduled Then Exit Sub
    On Error Resume Next
    Application.OnTime EarliestTime:=mScheduledImagePreviewTime, _
        Procedure:=QualifiedWorkbookMacro( _
            "RunScheduledProductImagePreview"), _
        Schedule:=False
    mImagePreviewScheduled = False
    On Error GoTo 0
End Sub

Private Sub RefreshProductImagePreview(ByVal relativeRow As Long)
    Dim syncTable As ListObject
    Dim imageURL As String
    Dim cachedPath As String
    Dim requestValue As AsyncWinHttpRequest

    CancelProductImagePreview True
    If Not ProductImagesEnabled() Then
        ShowProductImageState _
            U("06460645062706CC06340020062A06350627064806CC06310020063A06CC06310641063906270644002006270633062A002E")
        Exit Sub
    End If
    If relativeRow < 1 Then
        ShowProductImageState _
            U("06280631062706CC002006460645062706CC06340020062A0635064806CC0631060C002006CC06A9002006A9062706440627002006310627002006270646062A062E06270628002006A9064606CC062F002E")
        Exit Sub
    End If

    On Error GoTo PreviewFailed
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    If syncTable.DataBodyRange Is Nothing Or _
       relativeRow > syncTable.DataBodyRange.Rows.Count Then GoTo NoImage
    imageURL = Trim$(CStr(syncTable.DataBodyRange.Cells( _
        relativeRow, SYNC_COLUMN_COUNT).Value2))
    If Len(imageURL) = 0 Then GoTo NoImage
    If Not IsSafeProductImageURL(imageURL) Then GoTo InvalidImageURL

    cachedPath = CachedProductImagePath(imageURL)
    If Len(cachedPath) > 0 Then
        DisplayProductImageFile cachedPath, imageURL
        Exit Sub
    End If

    ShowProductImageState _
        U("062F06310020062D062706440020062F063106CC06270641062A0020062A0635064806CC06312026")
    mImageRequestURL = imageURL
    mImageRequestRow = relativeRow
    mImageRequestGeneration = mImageGeneration
    Set requestValue = New AsyncWinHttpRequest
    Set mImageRequest = requestValue
    requestValue.OpenAsync "GET", imageURL, NextAsyncToken(), _
        "product_image", False, PRODUCT_IMAGE_MAX_BYTES, _
        5000, 5000, 5000, PRODUCT_IMAGE_TIMEOUT_MS
    requestValue.SetRequestHeader "Accept", _
        "image/avif,image/webp,image/png,image/jpeg,image/gif"
    requestValue.Send
    Exit Sub

NoImage:
    ShowProductImageState _
        U("06280631062706CC0020062706CC0646002006A90627064406270020062A0635064806CC063106CC0020062B0628062A002006460634062F0647002006270633062A002E")
    Exit Sub

InvalidImageURL:
    ShowProductImageState _
        U("064606340627064606CC0020062A0635064806CC0631002006450639062A062806310020064606CC0633062A002E")
    Exit Sub

PreviewFailed:
    Set mImageRequest = Nothing
    ShowProductImageState _
        U("062A0635064806CC063100200642062706280644002006460645062706CC06340020064606CC0633062A002E")
End Sub

Private Sub CancelProductImagePreview(ByVal clearPreview As Boolean)
    mImageGeneration = mImageGeneration + 1
    On Error Resume Next
    If Not mImageRequest Is Nothing Then mImageRequest.Abort
    Set mImageRequest = Nothing
    mImageRequestURL = vbNullString
    mImageRequestRow = 0
    mImageRequestGeneration = 0
    If clearPreview Then ClearProductImageShape
    On Error GoTo 0
End Sub

Private Sub HandleProductImageTerminal()
    Dim requestValue As AsyncWinHttpRequest
    Dim requestURL As String
    Dim requestRow As Long
    Dim requestGeneration As Long
    Dim contentType As String
    Dim imageBytes As Variant
    Dim cachePath As String

    On Error GoTo ImageFailed
    Set requestValue = mImageRequest
    If requestValue Is Nothing Then Exit Sub
    requestURL = mImageRequestURL
    requestRow = mImageRequestRow
    requestGeneration = mImageRequestGeneration
    Set mImageRequest = Nothing
    mImageRequestURL = vbNullString
    mImageRequestRow = 0
    mImageRequestGeneration = 0

    If requestGeneration <> mImageGeneration Or _
       requestRow <> SelectedProductRelativeRow() Or _
       Not ProductImagesEnabled() Then Exit Sub
    If requestValue.HasError Or requestValue.Aborted Or _
       requestValue.StatusCode < 200 Or requestValue.StatusCode >= 300 Then _
        GoTo ImageFailed
    contentType = LCase$(Trim$(Split( _
        requestValue.ContentType & ";", ";")(0)))
    If Len(ProductImageExtension(contentType)) = 0 Then GoTo ImageFailed
    imageBytes = requestValue.TakeResponseBody()
    If Not IsByteArrayVariant(imageBytes) Or _
       ByteArrayVariantLength(imageBytes) < 1 Or _
       ByteArrayVariantLength(imageBytes) > PRODUCT_IMAGE_MAX_BYTES Then _
        GoTo ImageFailed

    cachePath = ProductImageCachePath(requestURL, contentType)
    SaveProductImageBytes imageBytes, cachePath
    CacheProductImagePath requestURL, cachePath
    If requestGeneration <> mImageGeneration Or _
       requestRow <> SelectedProductRelativeRow() Or _
       Not ProductImagesEnabled() Then Exit Sub
    DisplayProductImageFile cachePath, requestURL
    Exit Sub

ImageFailed:
    On Error Resume Next
    Set mImageRequest = Nothing
    mImageRequestURL = vbNullString
    mImageRequestRow = 0
    mImageRequestGeneration = 0
    If requestGeneration = mImageGeneration And _
       ProductImagesEnabled() Then
        ShowProductImageState _
            U("062A0635064806CC063100200642062706280644002006460645062706CC06340020064606CC0633062A002E")
    End If
    On Error GoTo 0
End Sub

Private Function ProductImagesEnabled() As Boolean
    Dim valid As Boolean

    ProductImagesEnabled = NamedYesNo("ShowProductImages", valid)
    If Not valid Then ProductImagesEnabled = False
End Function

Private Function SelectedProductRelativeRow() As Long
    Dim table As ListObject
    Dim selectedRow As Long
    Dim relativeRow As Long

    On Error GoTo CleanExit
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Function
    selectedRow = CLng(Val(CStr(ConfigSheet().Range("G30").Value2)))
    relativeRow = selectedRow - table.DataBodyRange.Row + 1
    If relativeRow >= 1 And relativeRow <= table.DataBodyRange.Rows.Count Then _
        SelectedProductRelativeRow = relativeRow
CleanExit:
End Function

Private Function IsSafeProductImageURL(ByVal imageURL As String) As Boolean
    Dim authorityEnd As Long
    Dim authority As String

    imageURL = Trim$(imageURL)
    If Len(imageURL) < 12 Or Len(imageURL) > 2048 Then Exit Function
    If LCase$(Left$(imageURL, 8)) <> "https://" Then Exit Function
    authorityEnd = InStr(9, imageURL, "/", vbBinaryCompare)
    If authorityEnd = 0 Then
        authority = Mid$(imageURL, 9)
    Else
        authority = Mid$(imageURL, 9, authorityEnd - 9)
    End If
    If Len(authority) = 0 Or _
       InStr(1, authority, "@", vbBinaryCompare) > 0 Then Exit Function
    IsSafeProductImageURL = True
End Function

Private Sub EnsureProductImageCache()
    If mImageCache Is Nothing Then
        Set mImageCache = CreateObject("Scripting.Dictionary")
        mImageCache.CompareMode = vbBinaryCompare
    End If
End Sub

Private Function CachedProductImagePath(ByVal imageURL As String) As String
    Dim fileSystem As Object
    Dim cachePath As String

    EnsureProductImageCache
    If Not mImageCache.Exists(imageURL) Then Exit Function
    cachePath = CStr(mImageCache(imageURL))
    Set fileSystem = CreateObject("Scripting.FileSystemObject")
    If fileSystem.FileExists(cachePath) Then
        CachedProductImagePath = cachePath
    Else
        mImageCache.Remove imageURL
    End If
End Function

Private Sub CacheProductImagePath(ByVal imageURL As String, _
                                  ByVal cachePath As String)
    Dim cacheKey As Variant
    Dim evictedPath As String
    Dim fileSystem As Object

    EnsureProductImageCache
    If Not mImageCache.Exists(imageURL) And _
       mImageCache.Count >= PRODUCT_IMAGE_CACHE_LIMIT Then
        For Each cacheKey In mImageCache.Keys
            evictedPath = CStr(mImageCache(CStr(cacheKey)))
            mImageCache.Remove CStr(cacheKey)
            Exit For
        Next cacheKey
        If Len(evictedPath) > 0 Then
            Set fileSystem = CreateObject("Scripting.FileSystemObject")
            On Error Resume Next
            If fileSystem.FileExists(evictedPath) And _
               InStr(1, fileSystem.GetFileName(evictedPath), _
                     "digitalogic-product-image-", vbTextCompare) = 1 Then _
                fileSystem.DeleteFile evictedPath, True
            On Error GoTo 0
        End If
    End If
    mImageCache(imageURL) = cachePath
End Sub

Private Function ProductImageCachePath(ByVal imageURL As String, _
                                       ByVal contentType As String) As String
    Dim fileSystem As Object
    Dim digestText As String

    Set fileSystem = CreateObject("Scripting.FileSystemObject")
    digestText = Mid$(SHA256Revision(imageURL), 8)
    ProductImageCachePath = fileSystem.GetSpecialFolder(2).Path & _
        Application.PathSeparator & "digitalogic-product-image-" & _
        digestText & ProductImageExtension(contentType)
End Function

Private Function ProductImageExtension(ByVal contentType As String) As String
    Select Case LCase$(Trim$(contentType))
        Case "image/jpeg", "image/jpg"
            ProductImageExtension = ".jpg"
        Case "image/png"
            ProductImageExtension = ".png"
        Case "image/gif"
            ProductImageExtension = ".gif"
        Case "image/webp"
            ProductImageExtension = ".webp"
        Case "image/avif"
            ProductImageExtension = ".avif"
    End Select
End Function

Private Sub SaveProductImageBytes(ByVal imageBytes As Variant, _
                                  ByVal cachePath As String)
    Dim stream As Object

    Set stream = CreateObject("ADODB.Stream")
    stream.Type = 1
    stream.Open
    stream.Write imageBytes
    stream.SaveToFile cachePath, 2
    stream.Close
End Sub

Private Sub DisplayProductImageFile(ByVal cachePath As String, _
                                    ByVal imageURL As String)
    Dim previewArea As Range
    Dim pictureValue As Shape
    Dim maxWidth As Double
    Dim maxHeight As Double

    On Error GoTo DisplayFailed
    ClearProductImageShape
    Set previewArea = ThisWorkbook.Names( _
        "ProductImagePreviewArea").RefersToRange
    Set pictureValue = PriceSheet().Shapes.AddPicture( _
        cachePath, 0, -1, previewArea.Left + 4, previewArea.Top + 4, _
        -1, -1)
    pictureValue.Name = PRODUCT_IMAGE_SHAPE
    pictureValue.LockAspectRatio = -1
    maxWidth = previewArea.Width - 8
    maxHeight = previewArea.Height - 8
    If pictureValue.Width > maxWidth Then pictureValue.Width = maxWidth
    If pictureValue.Height > maxHeight Then pictureValue.Height = maxHeight
    pictureValue.Left = previewArea.Left + _
        (previewArea.Width - pictureValue.Width) / 2
    pictureValue.Top = previewArea.Top + _
        (previewArea.Height - pictureValue.Height) / 2
    pictureValue.Placement = 1
    pictureValue.AlternativeText = U( _
        "062A0635064806CC063100200645062D063506480644")
    ShowProductImageState _
        U("062A0635064806CC063100200645062D063506480644002006460645062706CC06340020062F0627062F064700200634062F002E"), _
        False
    Exit Sub

DisplayFailed:
    ClearProductImageShape
    ShowProductImageState _
        U("062A0635064806CC063100200642062706280644002006460645062706CC06340020064606CC0633062A002E")
End Sub

Private Sub ClearProductImageShape()
    On Error Resume Next
    PriceSheet().Shapes(PRODUCT_IMAGE_SHAPE).Delete
    On Error GoTo 0
End Sub

Private Sub ShowProductImageState(ByVal messageText As String, _
                                  Optional ByVal clearImage As Boolean = True)
    Dim statusCell As Range

    If clearImage Then ClearProductImageShape
    On Error GoTo CleanExit
    Set statusCell = ThisWorkbook.Names( _
        "ProductImagePreviewStatus").RefersToRange
    statusCell.Value2 = messageText
    statusCell.Font.Name = NamedText("PersianFont", DEFAULT_PERSIAN_FONT)
    statusCell.Font.Bold = False
    statusCell.WrapText = True
CleanExit:
End Sub

Public Function ValidateProductImagePreviewUIForValidation() As Boolean
    Dim settingCell As Range
    Dim previewArea As Range
    Dim previewShape As Shape
    Dim previousValue As Variant
    Dim previousEvents As Boolean
    Dim sampleURL As String
    Dim fileSystem As Object

    On Error GoTo ValidationFailed
    previousEvents = Application.EnableEvents
    Set settingCell = ThisWorkbook.Names( _
        "ShowProductImages").RefersToRange
    Set previewArea = ThisWorkbook.Names( _
        "ProductImagePreviewArea").RefersToRange
    previousValue = settingCell.Value2
    If settingCell.Address(False, False) <> "B31" Or _
       settingCell.Validation.Type <> 3 Then GoTo ValidationFailed
    If Not Intersect(previewArea, _
        PriceSheet().ListObjects(PRODUCTS_TABLE).Range) Is Nothing Then _
        GoTo ValidationFailed
    If Not Intersect(previewArea, PriceSheet().Range("B3:K4")) _
        Is Nothing Then GoTo ValidationFailed
    If Not IsSafeProductImageURL( _
        "https://digitalogic.ir/product-image.webp") Or _
       IsSafeProductImageURL("http://digitalogic.ir/image.jpg") Or _
       IsSafeProductImageURL("https://user:secret@digitalogic.ir/a.jpg") Then _
        GoTo ValidationFailed
    Application.EnableEvents = False
    settingCell.Value2 = U("062E06CC0631")
    HandleProductImageSettingChanged
    If Not mImageRequest Is Nothing Then GoTo ValidationFailed
    On Error Resume Next
    Err.Clear
    Set previewShape = PriceSheet().Shapes(PRODUCT_IMAGE_SHAPE)
    If Err.Number = 0 Then GoTo ValidationFailed
    Err.Clear
    On Error GoTo ValidationFailed
    EnsureProductImageCache
    sampleURL = "https://digitalogic.ir/missing-cache-entry.webp"
    Set fileSystem = CreateObject("Scripting.FileSystemObject")
    mImageCache(sampleURL) = fileSystem.BuildPath( _
        fileSystem.GetSpecialFolder(2).Path, _
        "digitalogic-product-image-missing.webp")
    If Len(CachedProductImagePath(sampleURL)) <> 0 Or _
       mImageCache.Exists(sampleURL) Then GoTo ValidationFailed
    ValidateProductImagePreviewUIForValidation = True

ValidationExit:
    On Error Resume Next
    settingCell.Value2 = previousValue
    Application.EnableEvents = previousEvents
    HandleProductImageSettingChanged
    On Error GoTo 0
    Exit Function

ValidationFailed:
    ValidateProductImagePreviewUIForValidation = False
    Resume ValidationExit
End Function

Public Sub HandlePricingProposalChanged()
    If mProposalSyncActive Then Exit Sub

    On Error GoTo CleanExit
    mProposalSyncActive = True
    InvalidatePricingPreview
    ConfigSheet().Range("B23").Value2 = _
        U("062A063A06CC06CC06310627062A0020062F0631002006350641002006270631063306270644002006270633062A061B002006280631062706CC002006270631063306270644060C0020062F06A90645064700200647064506AF062706450200C06330627063206CC0020062706A9064606480646002006310627002006280632064606CC062F002E")

CleanExit:
    mProposalSyncActive = False
End Sub

Public Sub QueuePricingInputWriteback(ByVal Target As Range)
    Dim keys As Variant
    Dim keyValue As Variant
    Dim inputCell As Range
    Dim desiredValue As String
    Dim confirmedValue As String

    If mInternalPricingRefresh Or mProposalSyncActive Or _
       mCatalogCommitInProgress Or mWorkbookClosing Then Exit Sub
    EnsurePricingDirtyState
    keys = Split("yuan_price,dollar_price,cny_effective_date," & _
                 "usd_effective_date,profit_margin_percent," & _
                 "air_express_price_per_kg,price_rounding_digits", ",")
    For Each keyValue In keys
        Set inputCell = WritebackCell(CStr(keyValue))
        If Not inputCell Is Nothing Then
            If Not Intersect(Target, inputCell.MergeArea) Is Nothing Then
                On Error GoTo InvalidInput
                If inputCell.HasFormula Then GoTo InvalidInput
                desiredValue = ProposedWritebackValue(CStr(keyValue))
                confirmedValue = ConfirmedWritebackValue(CStr(keyValue))
                mPricingEditGeneration = mPricingEditGeneration + 1
                If Len(confirmedValue) > 0 And _
                   StrComp(desiredValue, confirmedValue, _
                           vbBinaryCompare) = 0 Then
                    ClearPricingDirtyKey CStr(keyValue)
                    MarkWritebackState CStr(keyValue), "confirmed", _
                        U("062706CC0646002006450642062F06270631002006280627002006450642062F062706310020062A062306CC06CC062F0634062F0647002006480628200C0633062706CC062A00200634062F06470020064806280633062706CC062A002006CC06A906CC002006270633062A002E")
                Else
                    mPricingDirty(CStr(keyValue)) = _
                        inputCell.Address(False, False)
                    mPricingDirtyGenerations(CStr(keyValue)) = _
                        mPricingEditGeneration
                    MarkWritebackState CStr(keyValue), "pending", _
                        U("062A063A06CC06CC0631002006270646062A063806270631002006270633062A061B002006280631062706CC002006270631063306270644060C0020062F06A90645064700200647064506AF062706450200C06330627063206CC0020062706A9064606480646002006310627002006280632064606CC062F002E")
                End If
                On Error GoTo 0
            End If
        End If
ContinueKey:
        Set inputCell = Nothing
    Next keyValue
    InvalidatePricingPreview
    ConfigSheet().Range("B23").Value2 = _
        U("062A063A06CC06CC06310627062A0020062F0631002006350641002006270631063306270644002006270633062A061B002006280631062706CC002006270631063306270644060C0020062F06A90645064700200647064506AF062706450200C06330627063206CC0020062706A9064606480646002006310627002006280632064606CC062F002E")
    Exit Sub

InvalidInput:
    MarkWritebackState CStr(keyValue), "warning", _
        U("06450642062F06270631002006450639062A062806310020064606CC0633062A061B002006450642062F0627063100200631062700200627063506440627062D002006A9064606CC062F002E") & _
        IIf(Len(Trim$(Err.Description)) > 0, vbCrLf & Err.Description, vbNullString)
    SetOperationProgressSurface "writeback_validation", 0, _
        U("06450642062F06270631002006450639062A062806310020064606CC0633062A"), _
        "warning", True
    Err.Clear
    Resume ContinueKey
End Sub

Public Sub QueueVisibleCNYProposal(ByVal Target As Range)
    Dim settings As Worksheet
    Dim proposedValue As Variant

    If mInternalPricingRefresh Or mProposalSyncActive Or _
       mCatalogCommitInProgress Or mWorkbookClosing Then Exit Sub
    If Target Is Nothing Or Target.Cells.CountLarge <> 1 Then Exit Sub
    proposedValue = Target.Value2
    If Not IsNumeric(proposedValue) Or CDbl(proposedValue) <= 0 Or _
       CDbl(proposedValue) <> Fix(CDbl(proposedValue)) Then
        Target.Formula = "=IF('" & _
            U("062A0646063806CC06450627062A") & _
            "'!G18="""","""",'" & _
            U("062A0646063806CC06450627062A") & "'!G18)"
        Err.Raise vbObjectError + 791, "QueueVisibleCNYProposal", _
                  U("06460631062E002006CC0648062A0646062A0631064A002006450639062A062806310020064606CC0633062A002E")
    End If
    Set settings = ConfigSheet()
    settings.Range("B18").Value = CDbl(proposedValue)
    QueuePricingInputWriteback settings.Range("B18")
End Sub

Public Sub RecoverPendingPricingIntentOnOpen()
    Dim settings As Worksheet

    If mWorkbookClosing Then Exit Sub
    Set settings = ConfigSheet()
    ClearDurablePendingPricingIntent
    If Not IsSHA256RevisionText(CStr(settings.Range("G14").Value2)) Then _
        Exit Sub
    QueuePricingInputWriteback Union(settings.Range("B18:B22"), _
        settings.Range("E20"), settings.Range("B26"))
End Sub

Private Sub ClearQueuedPricingIntent(ByVal settingKey As String)
    EnsureWritebackQueue
    If mWritebackPending.Exists(settingKey) Then _
        mWritebackPending.Remove settingKey
    If mWritebackPendingValues.Exists(settingKey) Then _
        mWritebackPendingValues.Remove settingKey
    If mWritebackPendingGenerations.Exists(settingKey) Then _
        mWritebackPendingGenerations.Remove settingKey
    If mWritebackPending.Count = 0 And mWritebackRequest Is Nothing And _
       Len(mWritebackStage) = 0 Then UnschedulePricingWriteback
End Sub

Private Sub EnsureWritebackQueue()
    If mWritebackPending Is Nothing Then
        Set mWritebackPending = CreateObject("Scripting.Dictionary")
        mWritebackPending.CompareMode = vbBinaryCompare
    End If
    If mWritebackPendingValues Is Nothing Then
        Set mWritebackPendingValues = CreateObject("Scripting.Dictionary")
        mWritebackPendingValues.CompareMode = vbBinaryCompare
    End If
    If mWritebackPendingGenerations Is Nothing Then
        Set mWritebackPendingGenerations = CreateObject("Scripting.Dictionary")
        mWritebackPendingGenerations.CompareMode = vbBinaryCompare
    End If
End Sub

Private Sub EnsurePricingDirtyState()
    If mPricingDirty Is Nothing Then
        Set mPricingDirty = CreateObject("Scripting.Dictionary")
        mPricingDirty.CompareMode = vbBinaryCompare
    End If
    If mPricingDirtyGenerations Is Nothing Then
        Set mPricingDirtyGenerations = CreateObject("Scripting.Dictionary")
        mPricingDirtyGenerations.CompareMode = vbBinaryCompare
    End If
End Sub

Private Sub ClearPricingDirtyKey(ByVal settingKey As String)
    EnsurePricingDirtyState
    If mPricingDirty.Exists(settingKey) Then mPricingDirty.Remove settingKey
    If mPricingDirtyGenerations.Exists(settingKey) Then _
        mPricingDirtyGenerations.Remove settingKey
End Sub

Private Sub ReconcilePricingProposalStates()
    Dim keys As Variant
    Dim keyValue As Variant
    Dim desiredValue As String
    Dim confirmedValue As String
    Dim inputCell As Range

    EnsurePricingDirtyState
    keys = Split("yuan_price,dollar_price,cny_effective_date," & _
                 "usd_effective_date,profit_margin_percent," & _
                 "air_express_price_per_kg,price_rounding_digits", ",")
    For Each keyValue In keys
        On Error GoTo InvalidValue
        desiredValue = ProposedWritebackValue(CStr(keyValue))
        confirmedValue = ConfirmedWritebackValue(CStr(keyValue))
        If Len(mWritebackStage) > 0 And _
           ActiveWritebackContainsKey(CStr(keyValue)) Then
            MarkWritebackState CStr(keyValue), "sending", _
                U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006480628200C0633062706CC062A")
        ElseIf Len(confirmedValue) > 0 And _
               StrComp(desiredValue, confirmedValue, _
                       vbBinaryCompare) = 0 Then
            ClearPricingDirtyKey CStr(keyValue)
            MarkWritebackState CStr(keyValue), "confirmed", _
                U("06450642062F062706310020062806270020062E064806270646062F0646002006450648062706410642002006270633062A002E")
        Else
            Set inputCell = WritebackCell(CStr(keyValue))
            If Not mPricingDirty.Exists(CStr(keyValue)) Then
                mPricingEditGeneration = mPricingEditGeneration + 1
                mPricingDirtyGenerations(CStr(keyValue)) = _
                    mPricingEditGeneration
            End If
            mPricingDirty(CStr(keyValue)) = _
                inputCell.Address(False, False)
            MarkWritebackState CStr(keyValue), "pending", _
                U("062A063A06CC06CC0631002006270646062A063806270631002006270633062A061B002006280631062706CC002006270631063306270644060C0020062F06A90645064700200647064506AF062706450200C06330627063206CC0020062706A9064606480646002006310627002006280632064606CC062F002E")
        End If
ContinueKey:
        On Error GoTo 0
        Set inputCell = Nothing
    Next keyValue
    Exit Sub
InvalidValue:
    MarkWritebackState CStr(keyValue), "failed", _
        U("06450642062F06270631002006450639062A062806310020064606CC0633062A002E")
    Err.Clear
    Resume ContinueKey
End Sub

Private Function ActiveWritebackContainsKey(ByVal settingKey As String) As Boolean
    Dim keyValue As Variant

    If mWritebackSettingKey = settingKey Then
        ActiveWritebackContainsKey = True
        Exit Function
    End If
    If mWritebackSettingKey <> "settings_batch" Or _
       Len(mActiveWritebackKeys) = 0 Then Exit Function
    For Each keyValue In Split(mActiveWritebackKeys, ",")
        If StrComp(CStr(keyValue), settingKey, vbBinaryCompare) = 0 Then
            ActiveWritebackContainsKey = True
            Exit Function
        End If
    Next keyValue
End Function

Public Sub SyncPricingSettingsNow()
    Dim settings As Worksheet
    Dim requestID As String
    Dim requestBody As String
    Dim keysCSV As String
    Dim keyValue As Variant

    ResumeAfterCancelledClose
    If mWorkbookClosing Or Len(mWritebackStage) > 0 Or _
       Not mWritebackRequest Is Nothing Then Exit Sub
    Set settings = ConfigSheet()
    EnsurePricingDirtyState
    QueuePricingInputWriteback Union(settings.Range("B18:B22"), _
        settings.Range("E20"), settings.Range("B26"))
    If mPricingDirty.Count = 0 Then
        settings.Range("B23").Value2 = _
            U("062A063A06CC06CC063106CC002006280631062706CC00200627063106330627064400200648062C0648062F00200646062F06270631062F061B0020064706450647002006450642062F0627063106470627002006280627002006480628200C0633062706CC062A002006CC06A906CC002006270633062A002E")
        Exit Sub
    End If

    On Error GoTo ValidationFailed
    requestID = NewRequestID("settings-sync")
    keysCSV = PricingDirtyKeysCSV()
    requestBody = BuildPricingBatchWritebackRequest(requestID, keysCSV)
    EnsureWritebackQueue
    mWritebackPending("settings_batch") = keysCSV
    mWritebackPendingValues("settings_batch") = requestBody
    mWritebackPendingGenerations("settings_batch") = mPricingEditGeneration
    For Each keyValue In Split(keysCSV, ",")
        MarkWritebackState CStr(keyValue), "pending", _
            U("062F06310020063506410020062706310633062706440020062F0633062A0647002006280647002006480628200C0633062706CC062A")
    Next keyValue
    settings.Range("B23").Value2 = _
        U("062A063A06CC06CC06310627062A0020062F06310020063506410020062706310633062706440020062706450646002006280647002006480631062F067E06310633002006270633062A002E")
    SchedulePricingWriteback 1
    Exit Sub

ValidationFailed:
    For Each keyValue In mPricingDirty.Keys
        MarkWritebackState CStr(keyValue), "failed", _
            U("06450642062F06270631002006450639062A06280631002006270633062A061B002006450642062F0627063100200631062700200627063506440627062D002006A9064606CC062F002E")
    Next keyValue
    settings.Range("B23").Value2 = _
        U("06450642062F06270631002006450639062A06280631002006270633062A061B002006450642062F0627063100200631062700200627063506440627062D002006A9064606CC062F002E")
    SetOperationProgressSurface "writeback_validation", 0, _
        settings.Range("B23").Value2, "failed", True
End Sub

Private Function PricingDirtyKeysCSV() As String
    Dim keys As Variant
    Dim keyValue As Variant

    EnsurePricingDirtyState
    keys = Split("yuan_price,dollar_price,cny_effective_date," & _
                 "usd_effective_date,profit_margin_percent," & _
                 "air_express_price_per_kg,price_rounding_digits", ",")
    For Each keyValue In keys
        If mPricingDirty.Exists(CStr(keyValue)) Then
            If Len(PricingDirtyKeysCSV) > 0 Then _
                PricingDirtyKeysCSV = PricingDirtyKeysCSV & ","
            PricingDirtyKeysCSV = PricingDirtyKeysCSV & CStr(keyValue)
        End If
    Next keyValue
End Function

Private Function ProposedWritebackValue(ByVal settingKey As String) As String
    Dim inputCell As Range

    Set inputCell = WritebackCell(settingKey)
    If inputCell Is Nothing Then Err.Raise vbObjectError + 794, _
        "ProposedWritebackValue", T("invalid_workbook")
    Select Case settingKey
        Case "cny_effective_date", "usd_effective_date"
            ProposedWritebackValue = CanonicalDateText(inputCell.Value2)
        Case "profit_margin_percent"
            If Not IsNumeric(inputCell.Value2) Then Err.Raise _
                vbObjectError + 794, "ProposedWritebackValue", _
                T("invalid_workbook")
            ProposedWritebackValue = _
                CanonicalCellText(CDbl(inputCell.Value2) * 100#)
        Case Else
            ProposedWritebackValue = CanonicalCellText(inputCell.Value2)
    End Select
    If Len(ProposedWritebackValue) = 0 Then Err.Raise _
        vbObjectError + 794, "ProposedWritebackValue", _
        T("invalid_workbook")
End Function

Private Function BuildPricingBatchWritebackRequest( _
        ByVal requestID As String, ByVal keysCSV As String) As String
    Dim settings As Worksheet
    Dim profitPercent As Variant
    Dim shippingCurrency As String
    Dim shippingRevision As String
    Dim usdEffectiveDate As String
    Dim cnyEffectiveDate As String
    Dim keyValue As Variant
    Dim keysJSON As String
    Dim previousJSON As String

    Set settings = ConfigSheet()
    profitPercent = Empty
    If IsNumeric(settings.Range("B21").Value2) Then
        profitPercent = CDbl(settings.Range("B21").Value2) * 100#
    End If
    shippingCurrency = UCase$(Trim$(CStr(settings.Range("H14").Value2)))
    shippingRevision = Trim$(CStr(settings.Range("H15").Value2))
    usdEffectiveDate = CanonicalDateText(settings.Range("E20").Value2)
    cnyEffectiveDate = CanonicalDateText(settings.Range("B20").Value2)
    ValidatePricingSettings settings, profitPercent, shippingCurrency, _
        shippingRevision, usdEffectiveDate, cnyEffectiveDate
    If Not IsSHA256RevisionText(Trim$(CStr(settings.Range("G14").Value2))) _
        Then Err.Raise vbObjectError + 795, _
            "BuildPricingBatchWritebackRequest", T("invalid_workbook")

    keysJSON = "["
    previousJSON = "{"
    For Each keyValue In Split(keysCSV, ",")
        If Len(keysJSON) > 1 Then keysJSON = keysJSON & ","
        If Len(previousJSON) > 1 Then previousJSON = previousJSON & ","
        keysJSON = keysJSON & JsonString(CStr(keyValue))
        previousJSON = previousJSON & JsonString(CStr(keyValue)) & _
            ":" & JsonString(ConfirmedWritebackValue(CStr(keyValue)))
    Next keyValue
    keysJSON = keysJSON & "]"
    previousJSON = previousJSON & "}"
    BuildPricingBatchWritebackRequest = _
        "{""schema"":" & JsonString(PRICING_WRITEBACK_BATCH_REQUEST_SCHEMA) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """setting_keys"":" & keysJSON & "," & _
        """expected_state_revision"":" & _
        JsonString(Trim$(CStr(settings.Range("G14").Value2))) & "," & _
        """previous_confirmed_values"":" & previousJSON & "," & _
        """settings"":{" & _
        """dollar_price"":" & JsonNumberOrNull(settings.Range("B19").Value2) & "," & _
        """yuan_price"":" & JsonNumberOrNull(settings.Range("B18").Value2) & "," & _
        """effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """usd_effective_date"":" & JsonString(usdEffectiveDate) & "," & _
        """cny_effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """profit_margin_percent"":" & JsonNumberOrNull(profitPercent) & "," & _
        """air_express_price_per_kg"":" & JsonNumberOrNull(settings.Range("B22").Value2) & "," & _
        """air_express_currency"":" & JsonString(shippingCurrency) & "," & _
        """shipping_catalog_revision"":" & JsonString(shippingRevision) & "," & _
        """price_rounding_digits"":" & JsonNumberOrNull(settings.Range("B26").Value2) & "," & _
        """price_rounding_mode"":" & JsonString(PRICE_ROUNDING_MODE) & "}}"
End Function

Private Sub SchedulePricingWriteback(Optional ByVal delaySeconds As Long = 1)
    If mSaveRenameSchedulesSuspended Then
        mSaveRenameWritebackPending = True
        Exit Sub
    End If
    If mWorkbookClosing Or mWritebackScheduled Or _
       Not mWritebackRequest Is Nothing Then Exit Sub
    On Error GoTo ScheduleFailed
    If delaySeconds < 1 Then delaySeconds = 1
    mScheduledWritebackTime = Now + TimeSerial(0, 0, delaySeconds)
    mWritebackScheduled = True
    Application.OnTime EarliestTime:=mScheduledWritebackTime, _
        Procedure:=QualifiedWorkbookMacro("RunScheduledPricingWriteback"), _
        Schedule:=True
    Exit Sub

ScheduleFailed:
    mWritebackScheduled = False
    Err.Clear
End Sub

Private Sub UnschedulePricingWriteback()
    If mWritebackScheduled Then
        On Error Resume Next
        Application.OnTime EarliestTime:=mScheduledWritebackTime, _
            Procedure:=QualifiedWorkbookMacro("RunScheduledPricingWriteback"), _
            Schedule:=False
        On Error GoTo 0
    End If
    mWritebackScheduled = False
End Sub

Public Sub RunScheduledPricingWriteback()
    Dim pendingKey As Variant

    mWritebackScheduled = False
    If mWorkbookClosing Or Not mWritebackRequest Is Nothing Then Exit Sub
    If mWritebackStage = "poll_wait" Then
        mWritebackStage = "poll"
        SetOperationProgressSurface "writeback_wait", -1, _
            U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006480628200C0633062706CC062A"), _
            "pending", False
        RunSynchronousWritebackStep
        Exit Sub
    End If
    EnsureWritebackQueue
    If mWritebackPending.Count = 0 Then Exit Sub
    For Each pendingKey In mWritebackPending.Keys
        mWritebackSettingKey = CStr(pendingKey)
        mWritebackCellAddress = CStr(mWritebackPending(pendingKey))
        If mWritebackPendingValues.Exists(CStr(pendingKey)) Then
            If mWritebackSettingKey = "settings_batch" Then
                mActiveWritebackRequestBody = _
                    CStr(mWritebackPendingValues(CStr(pendingKey)))
            Else
                mActiveWritebackDesiredValue = _
                    CStr(mWritebackPendingValues(CStr(pendingKey)))
            End If
            mWritebackPendingValues.Remove CStr(pendingKey)
        Else
            mActiveWritebackDesiredValue = vbNullString
        End If
        If mWritebackPendingGenerations.Exists(CStr(pendingKey)) Then
            mActiveWritebackGeneration = _
                CLng(mWritebackPendingGenerations(CStr(pendingKey)))
            mWritebackPendingGenerations.Remove CStr(pendingKey)
        Else
            mActiveWritebackGeneration = 0
        End If
        mWritebackPending.Remove CStr(pendingKey)
        Exit For
    Next pendingKey
    If Len(mWritebackSettingKey) = 0 Then Exit Sub
    If mWritebackSettingKey = "settings_batch" Then _
        mActiveWritebackKeys = mWritebackCellAddress
    mWritebackRequestID = NewRequestID("writeback")
    mWritebackJobID = vbNullString
    mWritebackCSRFToken = vbNullString
    mWritebackPollCount = 0
    mWritebackStage = "session"
    SetOperationProgressSurface "writeback_send", -1, _
        U("062F06310020062D062706440020062706310633062706440020062A063A06CC06CC0631"), _
        "pending", False
    RunSynchronousWritebackStep
End Sub

Private Sub RunSynchronousWritebackStep()
    Dim requestValue As Object
    Dim methodName As String
    Dim endpoint As String
    Dim requestBody As String
    Dim responseText As String
    Dim statusCode As Long
    Dim root As JsonValue
    Dim statusText As String
    Dim messageText As String
    Dim revisionText As String
    Dim confirmedValue As String
    Dim failureNumber As Long
    Dim failureSource As String
    Dim failureDescription As String

    On Error GoTo RequestFailed
    If mWritebackStage = "poll" Then
        SetOperationProgressSurface "writeback_wait", -1, _
            U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006480628200C0633062706CC062A"), _
            "pending", False
    Else
        SetOperationProgressSurface "writeback_send", -1, _
            U("062F06310020062D062706440020062706310633062706440020062A063A06CC06CC0631"), _
            "pending", False
    End If
    Select Case mWritebackStage
        Case "session"
            methodName = "POST"
            endpoint = PricingBaseURL() & "/session"
            requestBody = "{}"
        Case "enqueue"
            methodName = "POST"
            If mWritebackSettingKey = "site_confirmation" Then
                endpoint = PricingBaseURL() & "/confirmations"
                If Len(mPendingConfirmationTransactionID) > 0 Then
                    requestBody = BuildPricingConfirmationRequest( _
                        mWritebackRequestID)
                Else
                    requestBody = "{}"
                End If
            ElseIf mWritebackSettingKey = "settings_batch" Then
                endpoint = PricingBaseURL() & "/writebacks"
                requestBody = mActiveWritebackRequestBody
            Else
                endpoint = PricingBaseURL() & "/writebacks"
                requestBody = BuildPricingWritebackRequest( _
                    mWritebackSettingKey, mWritebackRequestID)
            End If
        Case "ack"
            If Len(mWritebackJobID) <> 32 Then GoTo InvalidResponse
            methodName = "POST"
            endpoint = PricingBaseURL() & "/writebacks/" & _
                mWritebackJobID & "/ack"
            requestBody = "{}"
        Case "poll"
            If Len(mWritebackJobID) <> 32 Then GoTo InvalidResponse
            methodName = "GET"
            endpoint = PricingBaseURL() & "/writebacks/" & _
                mWritebackJobID
        Case Else
            GoTo InvalidResponse
    End Select
    If Not IsAllowedPricingAuthenticatedUrl(endpoint) Then _
        GoTo InvalidResponse

    ' Worksheet_Change only queues this macro. These requests are loopback-only
    ' and bounded; the companion performs the remote WordPress mutation,
    ' idempotency/retry and readback work in its background queue. Avoiding the
    ' WinHTTP event sink here also avoids the affected Office build's native
    ' event-module mismatch while preserving a responsive edit handler. Use
    ' ServerXMLHTTP for these pinned loopback calls so this path does not load
    ' the same WinHTTP component that reproduced the native Office fault.
    Set requestValue = CreateObject("MSXML2.Server" & "XMLHTTP.6.0")
    ' The companion origin is pinned to loopback. Never send this local session
    ' or its CSRF token through a system/environment proxy.
    requestValue.setProxy 1
    requestValue.setTimeouts 2000, 2000, 3000, 5000
    requestValue.Open methodName, endpoint, False
    requestValue.setRequestHeader "Accept", "application/json"
    requestValue.setRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    If Len(mWritebackCSRFToken) > 0 Then
        requestValue.setRequestHeader PRICING_CSRF_HEADER, _
            mWritebackCSRFToken
    End If
    If methodName = "POST" Then
        requestValue.setRequestHeader "Content-Type", _
            "application/json; charset=utf-8"
        requestValue.Send Utf8Bytes(requestBody)
    Else
        requestValue.Send
    End If
    statusCode = CLng(requestValue.Status)
    responseText = CStr(requestValue.responseText)
    Set requestValue = Nothing
    If statusCode < 200 Or statusCode >= 300 Then
        messageText = ResponseErrorMessage(responseText)
        If Len(messageText) = 0 Then _
            messageText = "HTTP " & CStr(statusCode)
        FailPricingWriteback messageText
        Exit Sub
    End If

    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Then GoTo InvalidResponse
    Select Case mWritebackStage
        Case "session"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_SESSION_SCHEMA Then GoTo InvalidResponse
            mWritebackCSRFToken = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "csrf_token"))))
            If Len(mWritebackCSRFToken) <> 43 Then GoTo InvalidResponse
            mWritebackStage = "enqueue"
            RunSynchronousWritebackStep
        Case "enqueue"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Then GoTo InvalidResponse
            mWritebackJobID = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "job_id"))))
            If Len(mWritebackJobID) <> 32 Then GoTo InvalidResponse
            messageText = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "message_fa"))))
            If Len(messageText) = 0 Then
                messageText = U("062F06310020063506410020062706310633062706440020062706450646002006280647002006480631062F067E06310633002006270633062A002E")
            End If
            MarkWritebackState mWritebackSettingKey, "pending", messageText
            SetOperationProgressSurface "writeback_wait", -1, _
                U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006480628200C0633062706CC062A"), _
                "pending", False
            mWritebackStage = "poll_wait"
            SchedulePricingWriteback 1
        Case "poll"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Or _
               Trim$(CStr(JsonRuntime.JsonText(root, "job_id"))) <> _
               mWritebackJobID Then GoTo InvalidResponse
            statusText = LCase$(Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "status")))))
            messageText = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "message_fa"))))
            Select Case statusText
                Case "pending", "sending", "pending_ack", "sending_ack"
                    MarkWritebackState mWritebackSettingKey, _
                        "sending", messageText
                    SetOperationProgressSurface "writeback_wait", -1, _
                        U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006480628200C0633062706CC062A"), _
                        "pending", False
                    mWritebackPollCount = mWritebackPollCount + 1
                    If mWritebackPollCount > 480 Then
                        FailPricingWriteback T("sync_retry")
                    Else
                        mWritebackStage = "poll_wait"
                        SchedulePricingWriteback 1
                    End If
                Case "awaiting_excel"
                    ApplyWebsiteCommittedWriteback root, messageText
                    mWritebackStage = "ack"
                    RunSynchronousWritebackStep
                Case "confirmed"
                    revisionText = Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "state_revision"))))
                    confirmedValue = Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "confirmed_value"))))
                    If mWritebackSettingKey = "settings_batch" Then
                        ConfirmPricingBatchWriteback root, messageText, _
                            revisionText, Trim$(CStr(BlankIfNull( _
                            JsonRuntime.JsonText(root, "updated_at"))))
                    Else
                        ConfirmPricingWriteback messageText, revisionText, _
                            confirmedValue, Trim$(CStr(BlankIfNull( _
                            JsonRuntime.JsonText(root, "updated_at"))))
                    End If
                    CompletePricingWriteback
                Case "superseded"
                    CompletePricingWriteback
                Case "conflict", "failed"
                    RestoreWritebackValueFromTerminal root
                    If Len(messageText) = 0 Then messageText = _
                        U("06280647200C063106480632063106330627064606CC002006480631062F067E063106330020064606270645064806410642003A") & _
                        " " & Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "code"))))
                    MarkWritebackState mWritebackSettingKey, "failed", _
                        messageText
                    SetOperationProgressSurface "writeback_conflict", 0, _
                        U("062A06390627063106360020062F06310020062A063A06CC06CC0631") & _
                        ": " & messageText, "failed", True
                    CompletePricingWriteback
                Case Else
                    GoTo InvalidResponse
            End Select
        Case "ack"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Or _
               Trim$(CStr(JsonRuntime.JsonText(root, "job_id"))) <> _
               mWritebackJobID Then GoTo InvalidResponse
            mWritebackStage = "poll_wait"
            SchedulePricingWriteback 1
    End Select
    Exit Sub

InvalidResponse:
    messageText = U("067E06270633062E00200646062706450639062A062806310020067E06440020062F0631002006450631062D064406470020") & _
                  mWritebackStage & _
                  " (HTTP " & CStr(statusCode) & ")"
    If Len(responseText) > 0 Then
        messageText = messageText & ": " & Left$(responseText, 200)
    End If
    FailPricingWriteback messageText
    Exit Sub
RequestFailed:
    failureNumber = Err.Number
    failureSource = Err.Source
    failureDescription = Err.Description
    ' A bounded loopback timeout does not prove the durable website job failed.
    ' The companion may already have committed or ACKed the idempotent request.
    ' Re-read the same job before showing a terminal red state so transient
    ' local contention cannot turn a confirmed website change into a false
    ' Excel failure.
    If (mWritebackStage = "poll" Or mWritebackStage = "ack") And _
       mWritebackPollCount < 12 Then
        mWritebackPollCount = mWritebackPollCount + 1
        MarkWritebackState mWritebackSettingKey, "sending", _
            U("062F06310020062806310631063306CC00200646062A06CC062C06470020062706320020064806280633062706CC062A")
        SetOperationProgressSurface "writeback_retry", -1, _
            U("062F06310020062806310631063306CC00200646062A06CC062C06470020062706320020064806280633062706CC062A"), _
            "pending", False
        mWritebackStage = "poll_wait"
        Set requestValue = Nothing
        SchedulePricingWriteback 1
        Exit Sub
    End If
    messageText = U("06450631062D064406470020") & mWritebackStage & _
                  ": " & failureSource & " (" & CStr(failureNumber) & _
                  "): " & failureDescription
    Set requestValue = Nothing
    FailPricingWriteback messageText
End Sub

Private Sub BeginWritebackEnqueue()
    mWritebackStage = "enqueue"
    If mWritebackSettingKey = "site_confirmation" Then
        StartWritebackRequest "POST", PricingBaseURL() & _
            "/confirmations", _
            BuildPricingConfirmationRequest(mWritebackRequestID)
    ElseIf mWritebackSettingKey = "settings_batch" Then
        StartWritebackRequest "POST", PricingBaseURL() & "/writebacks", _
            mActiveWritebackRequestBody
    Else
        StartWritebackRequest "POST", PricingBaseURL() & "/writebacks", _
            BuildPricingWritebackRequest(mWritebackSettingKey, _
                                         mWritebackRequestID)
    End If
End Sub

Private Sub BeginWritebackPoll()
    If Len(mWritebackJobID) <> 32 Then
        FailPricingWriteback T("bridge_missing")
        Exit Sub
    End If
    mWritebackStage = "poll"
    StartWritebackRequest "GET", PricingBaseURL() & "/writebacks/" & _
        mWritebackJobID, vbNullString
End Sub

Private Sub StartWritebackRequest(ByVal methodName As String, _
                                  ByVal endpoint As String, _
                                  ByVal requestBody As String)
    Dim requestValue As AsyncWinHttpRequest
    Dim requestToken As Long

    On Error GoTo StartFailed
    If Not IsAllowedPricingAuthenticatedUrl(endpoint) Or _
       Not mWritebackRequest Is Nothing Then
        Err.Raise vbObjectError + 770, "StartWritebackRequest", _
                  T("bridge_missing")
    End If
    requestToken = NextAsyncToken()
    Set requestValue = New AsyncWinHttpRequest
    Set mWritebackRequest = requestValue
    requestValue.OpenAsync UCase$(methodName), endpoint, requestToken, _
        "pricing_writeback_" & mWritebackStage, False, 262144, _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS
    requestValue.SetRequestHeader "Accept", "application/json"
    requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    If Len(mWritebackCSRFToken) > 0 Then
        requestValue.SetRequestHeader PRICING_CSRF_HEADER, _
            mWritebackCSRFToken
    End If
    If UCase$(methodName) = "POST" Then
        requestValue.SetRequestHeader "Content-Type", _
            "application/json; charset=utf-8"
        requestValue.Send Utf8Bytes(requestBody)
    Else
        requestValue.Send
    End If
    Exit Sub

StartFailed:
    Set mWritebackRequest = Nothing
    FailPricingWriteback Err.Description
End Sub

Private Sub HandleWritebackTerminal()
    Dim requestValue As AsyncWinHttpRequest
    Dim statusCode As Long
    Dim responseBody As Variant
    Dim responseText As String
    Dim root As JsonValue
    Dim statusText As String
    Dim messageText As String
    Dim revisionText As String
    Dim confirmedValue As String

    On Error GoTo TerminalFailed
    Set requestValue = mWritebackRequest
    If requestValue Is Nothing Or Not requestValue.Terminal Then Exit Sub
    statusCode = requestValue.StatusCode
    responseBody = requestValue.TakeResponseBody()
    Set mWritebackRequest = Nothing
    If requestValue.HasError Or requestValue.Aborted Then
        FailPricingWriteback requestValue.ErrorDescription
        Exit Sub
    End If
    responseText = Utf8TextFromBytes(responseBody)
    If statusCode < 200 Or statusCode >= 300 Then
        messageText = ResponseErrorMessage(responseText)
        If Len(messageText) = 0 Then messageText = "HTTP " & CStr(statusCode)
        FailPricingWriteback messageText
        Exit Sub
    End If
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Then
        FailPricingWriteback T("bridge_missing")
        Exit Sub
    End If
    Select Case mWritebackStage
        Case "session"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_SESSION_SCHEMA Then GoTo InvalidResponse
            mWritebackCSRFToken = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "csrf_token"))))
            If Len(mWritebackCSRFToken) <> 43 Then GoTo InvalidResponse
            BeginWritebackEnqueue
        Case "enqueue"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Then GoTo InvalidResponse
            mWritebackJobID = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "job_id"))))
            If Len(mWritebackJobID) <> 32 Then GoTo InvalidResponse
            messageText = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "message_fa"))))
            If Len(messageText) = 0 Then
                messageText = U("062F06310020063506410020062706310633062706440020062706450646002006280647002006480631062F067E06310633002006270633062A002E")
            End If
            MarkWritebackState mWritebackSettingKey, "pending", messageText
            mWritebackStage = "poll_wait"
            SchedulePricingWriteback 1
        Case "poll"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Or _
               Trim$(CStr(JsonRuntime.JsonText(root, "job_id"))) <> _
               mWritebackJobID Then GoTo InvalidResponse
            statusText = LCase$(Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "status")))))
            messageText = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(root, "message_fa"))))
            Select Case statusText
                Case "pending", "sending", "pending_ack", "sending_ack"
                    If Len(messageText) = 0 Then
                        messageText = U("062F06310020062D062706440020062706310633062706440020062706450646002006280647002006480631062F067E06310633002006480020062E064806270646062F064600200646062A06CC062C0647002006270633062A002E")
                    End If
                    MarkWritebackState mWritebackSettingKey, _
                        "sending", messageText
                    mWritebackPollCount = mWritebackPollCount + 1
                    If mWritebackPollCount > 480 Then
                        FailPricingWriteback T("sync_retry")
                    Else
                        mWritebackStage = "poll_wait"
                        SchedulePricingWriteback 1
                    End If
                Case "awaiting_excel"
                    ApplyWebsiteCommittedWriteback root, messageText
                    mWritebackStage = "ack"
                    StartWritebackRequest "POST", PricingBaseURL() & _
                        "/writebacks/" & mWritebackJobID & "/ack", "{}"
                Case "confirmed"
                    revisionText = Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "state_revision"))))
                    confirmedValue = Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "confirmed_value"))))
                    If mWritebackSettingKey = "settings_batch" Then
                        ConfirmPricingBatchWriteback root, messageText, _
                            revisionText, Trim$(CStr(BlankIfNull( _
                            JsonRuntime.JsonText(root, "updated_at"))))
                    Else
                        ConfirmPricingWriteback messageText, revisionText, _
                            confirmedValue, Trim$(CStr(BlankIfNull( _
                            JsonRuntime.JsonText(root, "updated_at"))))
                    End If
                    CompletePricingWriteback
                Case "superseded"
                    CompletePricingWriteback
                Case "conflict", "failed"
                    RestoreWritebackValueFromTerminal root
                    If Len(messageText) = 0 Then messageText = _
                        U("06280647200C063106480632063106330627064606CC002006480631062F067E063106330020064606270645064806410642003A") & _
                        " " & Trim$(CStr(BlankIfNull( _
                        JsonRuntime.JsonText(root, "code"))))
                    MarkWritebackState mWritebackSettingKey, "failed", _
                        messageText
                    CompletePricingWriteback
                Case Else
                    GoTo InvalidResponse
            End Select
        Case "ack"
            If CStr(JsonRuntime.JsonText(root, "schema")) <> _
               PRICING_WRITEBACK_JOB_SCHEMA Or _
               Trim$(CStr(JsonRuntime.JsonText(root, "job_id"))) <> _
               mWritebackJobID Then GoTo InvalidResponse
            mWritebackStage = "poll_wait"
            SchedulePricingWriteback 1
        Case Else
            GoTo InvalidResponse
    End Select
    Exit Sub

InvalidResponse:
    FailPricingWriteback T("bridge_missing")
    Exit Sub
TerminalFailed:
    FailPricingWriteback Err.Description
End Sub

Private Sub CompletePricingWriteback()
    Dim completedSiteConfirmation As Boolean

    completedSiteConfirmation = _
        (mWritebackSettingKey = "site_confirmation")
    If mWritebackSettingKey = "site_confirmation" Then
        mPendingConfirmationTransactionID = vbNullString
        mPendingConfirmationRevision = vbNullString
        mPendingConfirmationDigest = vbNullString
        mPendingConfirmationDeadline = 0
    End If
    mWritebackStage = vbNullString
    mWritebackSettingKey = vbNullString
    mWritebackCellAddress = vbNullString
    mWritebackRequestID = vbNullString
    mWritebackJobID = vbNullString
    mWritebackCSRFToken = vbNullString
    mWritebackPollCount = 0
    mActiveWritebackGeneration = 0
    mActiveWritebackDesiredValue = vbNullString
    mActiveWritebackKeys = vbNullString
    mActiveWritebackRequestBody = vbNullString
    EnsureWritebackQueue
    If mWritebackPending.Count > 0 Then SchedulePricingWriteback 1
    If completedSiteConfirmation Then
        ' Applying the website-confirmed setting already updates the hidden
        ' confirmed input and recalculates every workbook formula atomically.
        ' Do not start a full Patris/Woo catalog refresh on this critical path:
        ' Patris revisions are expected to move continuously and a catalog
        ' rebuild can consume the next website-first ACK window. Product data
        ' remains independently event-driven through source/catalog events.
        mRefreshAfterSiteConfirmation = False
    End If
End Sub

Private Sub ApplyWebsiteCommittedWriteback(ByVal root As JsonValue, _
                                           ByVal messageText As String)
    Dim settings As Worksheet
    Dim inputCell As Range
    Dim confirmedValue As Variant
    Dim revisionText As String
    Dim transactionID As String
    Dim digestText As String
    Dim deadlineValue As Variant
    Dim previousEvents As Boolean

    Set settings = ConfigSheet()
    If mWritebackSettingKey <> "settings_batch" Then
        Set inputCell = WritebackCell(mWritebackSettingKey)
        If inputCell Is Nothing Then Err.Raise vbObjectError + 781, _
            "ApplyWebsiteCommittedWriteback", T("bridge_missing")
    End If
    transactionID = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(root, "transaction_id"))))
    revisionText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(root, "state_revision"))))
    digestText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(root, "confirmed_settings_digest"))))
    deadlineValue = NumericOrBlank( _
        JsonRuntime.JsonText(root, "ack_deadline"))
    confirmedValue = BlankIfNull( _
        JsonRuntime.JsonText(root, "confirmed_value"))
    If Len(transactionID) <> 36 Or Left$(transactionID, 4) <> "ptx_" Then
        Err.Raise vbObjectError + 782, _
            "ApplyWebsiteCommittedWriteback", _
            U("063406460627063306470020062A0631062706A9064606340020062A062306CC06CC062F002006480628200C0633062706CC062A002006450639062A062806310020064606CC0633062A002E")
    End If
    If Not IsSHA256RevisionText(revisionText) Then
        Err.Raise vbObjectError + 782, _
            "ApplyWebsiteCommittedWriteback", _
            U("06460633062E0647002006420637063906CC0020064206CC0645062A200C06AF06300627063106CC002006480628200C0633062706CC062A0020062F06310020067E06270633062E00200647064506310627064700200648062C0648062F00200646062F06270631062F002E")
    End If
    If Not IsSHA256RevisionText(digestText) Then
        Err.Raise vbObjectError + 782, _
            "ApplyWebsiteCommittedWriteback", _
            U("0627062B06310020062A0646063806CC06450627062A0020062A062306CC06CC062F0634062F0647002006480628200C0633062706CC062A0020062F06310020067E06270633062E00200647064506310627064700200648062C0648062F00200646062F06270631062F002E")
    End If
    If IsEmpty(deadlineValue) Then
        Err.Raise vbObjectError + 782, _
            "ApplyWebsiteCommittedWriteback", _
            U("064506470644062A0020062A062306CC06CC062F002006480628200C0633062706CC062A0020062F06310020067E06270633062E00200647064506310627064700200648062C0648062F00200646062F06270631062F002E")
    End If
    If mWritebackSettingKey <> "settings_batch" And _
       Len(CanonicalCellText(confirmedValue)) = 0 Then
        Err.Raise vbObjectError + 782, _
            "ApplyWebsiteCommittedWriteback", _
            U("06460631062E0020062A062306CC06CC062F0634062F0647002006480628200C0633062706CC062A0020062F06310020067E06270633062E00200647064506310627064700200648062C0648062F00200646062F06270631062F002E")
    End If

    previousEvents = Application.EnableEvents
    On Error GoTo ApplyFailed
    mInternalPricingRefresh = True
    Application.EnableEvents = False
    settings.Range("G14").Value2 = revisionText
    Select Case mWritebackSettingKey
        Case "settings_batch"
            ApplyConfirmedSettingsForActiveBatch root, settings, True
        Case "yuan_price", "site_confirmation"
            settings.Range("B10").Value = CDbl(confirmedValue)
            settings.Range("G18").Value = CDbl(confirmedValue)
            If Not WritebackHasNewerProposal() Then
                inputCell.Value = CDbl(confirmedValue)
                RestoreVisibleCNYFormula
            End If
        Case "dollar_price"
            settings.Range("B11").Value = CDbl(confirmedValue)
            settings.Range("G19").Value = CDbl(confirmedValue)
            inputCell.Value = CDbl(confirmedValue)
        Case "cny_effective_date"
            settings.Range("H17").Value2 = CanonicalDateText(confirmedValue)
            settings.Range("G20").Value2 = CanonicalDateText(confirmedValue)
            inputCell.Value2 = CanonicalDateText(confirmedValue)
        Case "usd_effective_date"
            settings.Range("H16").Value2 = CanonicalDateText(confirmedValue)
            inputCell.Value2 = CanonicalDateText(confirmedValue)
        Case "profit_margin_percent"
            settings.Range("B13").Value = CDbl(confirmedValue) / 100#
            settings.Range("G21").Value = CDbl(confirmedValue) / 100#
            inputCell.Value = CDbl(confirmedValue) / 100#
        Case "air_express_price_per_kg"
            settings.Range("B14").Value = CDbl(confirmedValue)
            settings.Range("G22").Value = CDbl(confirmedValue)
            inputCell.Value = CDbl(confirmedValue)
        Case "price_rounding_digits"
            settings.Range("B15").Value = CLng(confirmedValue)
            settings.Range("H18").Value = CLng(confirmedValue)
            inputCell.Value = CLng(confirmedValue)
        Case Else
            Err.Raise vbObjectError + 783, _
                "ApplyWebsiteCommittedWriteback", T("bridge_missing")
    End Select
    CalculateRefreshedWorkbook
    MarkWritebackState mWritebackSettingKey, "sending", _
        IIf(Len(messageText) > 0, messageText, _
            U("06460631062E0020062A062706CC06CC062F0634062F06470020062F06310020064806280633062706CC062A0020062B0628062A00200634062F061B0020062F0631002006270646062A063806270631002006460647062706CC06CC002006270633062A002E"))
    SetOperationProgressSurface "writeback_wait", -1, _
        U("062F0631002006270646062A0638062706310020062A062306CC06CC062F002006460647062706CC06CC002006480628200C0633062706CC062A"), _
        "pending", False
ApplyExit:
    Application.EnableEvents = previousEvents
    mInternalPricingRefresh = False
    Exit Sub
ApplyFailed:
    Application.EnableEvents = previousEvents
    mInternalPricingRefresh = False
    Err.Raise Err.Number, "ApplyWebsiteCommittedWriteback", Err.Description
End Sub

Private Sub ApplyConfirmedSettingsForActiveBatch( _
        ByVal root As JsonValue, ByVal settings As Worksheet, _
        ByVal updateProposals As Boolean)
    Dim document As JsonValue
    Dim keyValue As Variant
    Dim confirmedValue As Variant

    Set document = JsonRuntime.JsonMember(root, "confirmed_settings")
    If document Is Nothing Or document.Kind <> "object" Or _
       Len(mActiveWritebackKeys) = 0 Then
        Err.Raise vbObjectError + 784, _
            "ApplyConfirmedSettingsForActiveBatch", T("bridge_missing")
    End If
    For Each keyValue In Split(mActiveWritebackKeys, ",")
        confirmedValue = BlankIfNull( _
            JsonRuntime.JsonText(document, CStr(keyValue)))
        If Len(CanonicalCellText(confirmedValue)) = 0 Then
            Err.Raise vbObjectError + 784, _
                "ApplyConfirmedSettingsForActiveBatch", T("bridge_missing")
        End If
        ApplyConfirmedWritebackValue settings, CStr(keyValue), _
            confirmedValue, updateProposals And _
            Not PricingKeyHasNewerProposal(CStr(keyValue))
    Next keyValue
End Sub

Private Sub ApplyConfirmedWritebackValue(ByVal settings As Worksheet, _
        ByVal settingKey As String, ByVal confirmedValue As Variant, _
        ByVal updateProposal As Boolean)
    Dim inputCell As Range

    Set inputCell = WritebackCell(settingKey)
    If inputCell Is Nothing Then Err.Raise vbObjectError + 785, _
        "ApplyConfirmedWritebackValue", T("bridge_missing")
    Select Case settingKey
        Case "yuan_price"
            settings.Range("B10").Value = CDbl(confirmedValue)
            settings.Range("G18").Value = CDbl(confirmedValue)
            If updateProposal Then
                inputCell.Value = CDbl(confirmedValue)
                RestoreVisibleCNYFormula
            End If
        Case "dollar_price"
            settings.Range("B11").Value = CDbl(confirmedValue)
            settings.Range("G19").Value = CDbl(confirmedValue)
            If updateProposal Then inputCell.Value = CDbl(confirmedValue)
        Case "cny_effective_date"
            settings.Range("H17").Value2 = CanonicalDateText(confirmedValue)
            settings.Range("G20").Value2 = CanonicalDateText(confirmedValue)
            If updateProposal Then inputCell.Value2 = _
                CanonicalDateText(confirmedValue)
        Case "usd_effective_date"
            settings.Range("H16").Value2 = CanonicalDateText(confirmedValue)
            If updateProposal Then inputCell.Value2 = _
                CanonicalDateText(confirmedValue)
        Case "profit_margin_percent"
            settings.Range("B13").Value = CDbl(confirmedValue) / 100#
            settings.Range("G21").Value = CDbl(confirmedValue) / 100#
            If updateProposal Then inputCell.Value = _
                CDbl(confirmedValue) / 100#
        Case "air_express_price_per_kg"
            settings.Range("B14").Value = CDbl(confirmedValue)
            settings.Range("G22").Value = CDbl(confirmedValue)
            If updateProposal Then inputCell.Value = CDbl(confirmedValue)
        Case "price_rounding_digits"
            settings.Range("B15").Value = CLng(confirmedValue)
            settings.Range("H18").Value = CLng(confirmedValue)
            If updateProposal Then inputCell.Value = CLng(confirmedValue)
        Case Else
            Err.Raise vbObjectError + 785, _
                "ApplyConfirmedWritebackValue", T("bridge_missing")
    End Select
End Sub

Private Function PricingKeyHasNewerProposal( _
        ByVal settingKey As String) As Boolean
    EnsurePricingDirtyState
    If mPricingDirtyGenerations.Exists(settingKey) Then
        PricingKeyHasNewerProposal = _
            (CLng(mPricingDirtyGenerations(settingKey)) > _
             mActiveWritebackGeneration)
    End If
End Function

Private Sub RestoreWritebackValueFromTerminal(ByVal root As JsonValue)
    Dim confirmedValue As Variant
    Dim settings As Worksheet
    Dim inputCell As Range
    Dim previousEvents As Boolean

    Set settings = ConfigSheet()
    If mWritebackSettingKey = "settings_batch" Then
        previousEvents = Application.EnableEvents
        On Error GoTo RestoreExit
        mInternalPricingRefresh = True
        Application.EnableEvents = False
        ApplyConfirmedSettingsForActiveBatch root, settings, False
        CalculateRefreshedWorkbook
        GoTo RestoreExit
    End If
    confirmedValue = BlankIfNull( _
        JsonRuntime.JsonText(root, "confirmed_value"))
    If Len(CanonicalCellText(confirmedValue)) = 0 Then Exit Sub
    Set inputCell = WritebackCell(mWritebackSettingKey)
    If inputCell Is Nothing Then Exit Sub
    If mWritebackSettingKey = "site_confirmation" Then
        ' A cold listener may replay a terminal event for another setting (for
        ' example rounding digits = 2). Site-confirmation discovery is bound to
        ' the confirmed CNY cell, so never show that unrelated event value in
        ' the B18 progress message or tooltip.
        confirmedValue = CanonicalCellText(settings.Range("G18").Value2)
    End If
    previousEvents = Application.EnableEvents
    On Error GoTo RestoreExit
    mInternalPricingRefresh = True
    Application.EnableEvents = False
    Select Case mWritebackSettingKey
        Case "yuan_price", "site_confirmation"
            settings.Range("B10").Value = CDbl(confirmedValue)
            settings.Range("G18").Value = CDbl(confirmedValue)
            If Not WritebackHasNewerProposal() Then
                inputCell.Value = CDbl(confirmedValue)
                RestoreVisibleCNYFormula
            End If
        Case "dollar_price"
            settings.Range("B11").Value = CDbl(confirmedValue)
            settings.Range("G19").Value = CDbl(confirmedValue)
            inputCell.Value = CDbl(confirmedValue)
        Case "cny_effective_date"
            settings.Range("H17").Value2 = CanonicalDateText(confirmedValue)
            settings.Range("G20").Value2 = CanonicalDateText(confirmedValue)
            inputCell.Value2 = CanonicalDateText(confirmedValue)
        Case "usd_effective_date"
            settings.Range("H16").Value2 = CanonicalDateText(confirmedValue)
            inputCell.Value2 = CanonicalDateText(confirmedValue)
        Case "profit_margin_percent"
            settings.Range("B13").Value = CDbl(confirmedValue) / 100#
            settings.Range("G21").Value = CDbl(confirmedValue) / 100#
            inputCell.Value = CDbl(confirmedValue) / 100#
        Case "air_express_price_per_kg"
            settings.Range("B14").Value = CDbl(confirmedValue)
            settings.Range("G22").Value = CDbl(confirmedValue)
            inputCell.Value = CDbl(confirmedValue)
        Case "price_rounding_digits"
            settings.Range("B15").Value = CLng(confirmedValue)
            settings.Range("H18").Value = CLng(confirmedValue)
            inputCell.Value = CLng(confirmedValue)
    End Select
    CalculateRefreshedWorkbook
RestoreExit:
    Application.EnableEvents = previousEvents
    mInternalPricingRefresh = False
End Sub

Private Sub FailPricingWriteback(ByVal reasonText As String)
    If Len(Trim$(reasonText)) = 0 Then reasonText = T("sync_retry")
    If mWritebackSettingKey = "settings_batch" Then
        reasonText = _
            U("062706310633062706440020062A0646063806CC06450627062A002006A9062706450644002006460634062F061B00200627062A0635062706440020063106270020062806310631063306CC002006A9064606CC062F002006480020062F0648062806270631064700200628062A06330627063206CC062F002E")
    End If
    If Len(mWritebackStage) > 0 And _
       mWritebackSettingKey <> "settings_batch" Then
        reasonText = "[" & mWritebackStage & "] " & reasonText
    End If
    If Len(mWritebackSettingKey) > 0 Then
        MarkWritebackState mWritebackSettingKey, "failed", _
            U("06280647200C063106480632063106330627064606CC002006480631062F067E063106330020064606270645064806410642003A") & _
            " " & reasonText
    End If
    SetOperationProgressSurface "writeback_failed", 0, _
        U("062706310633062706440020064606270645064806410642002006280648062F") & _
        ": " & reasonText, "failed", True
    Set mWritebackRequest = Nothing
    CompletePricingWriteback
End Sub

Private Sub ConfirmPricingBatchWriteback(ByVal root As JsonValue, _
        ByVal messageText As String, ByVal revisionText As String, _
        ByVal updatedAt As String)
    Dim settings As Worksheet
    Dim previousEvents As Boolean
    Dim keyValue As Variant
    Dim noteText As String

    Set settings = ConfigSheet()
    previousEvents = Application.EnableEvents
    On Error GoTo ConfirmFailed
    Application.EnableEvents = False
    mInternalPricingRefresh = True
    If Not IsSHA256RevisionText(revisionText) Then Err.Raise _
        vbObjectError + 786, "ConfirmPricingBatchWriteback", _
        T("bridge_missing")
    settings.Range("G14").Value2 = revisionText
    ApplyConfirmedSettingsForActiveBatch root, settings, True
    CalculateRefreshedWorkbook
    noteText = U("062A063A06CC06CC06310627062A0020062F0631002006480628200C0633062706CC062A0020062B0628062A00200648062806270020062E064806270646062F064600200645062C062F062F0020062E064806270646062F064700200634062F002E")
    If Len(messageText) > 0 Then noteText = noteText & vbCrLf & messageText
    If Len(updatedAt) > 0 Then noteText = noteText & vbCrLf & _
        U("0632064506270646003A") & " " & updatedAt
    For Each keyValue In Split(mActiveWritebackKeys, ",")
        If PricingKeyHasNewerProposal(CStr(keyValue)) Then
            MarkWritebackState CStr(keyValue), "pending", _
                U("062A063A06CC06CC06310020062C062F06CC062F062A063106CC0020062F0631002006270646062A063806270631002006270633062A061B002006280631062706CC002006270631063306270644060C0020062F06A90645064700200647064506AF062706450200C06330627063206CC0020062706A9064606480646002006310627002006280632064606CC062F002E")
        Else
            ClearPricingDirtyKey CStr(keyValue)
            MarkWritebackState CStr(keyValue), "confirmed", noteText
        End If
    Next keyValue
    settings.Range("B23").Value2 = noteText
    SetOperationProgressSurface "writeback_confirmed", 100, _
        U("062A063A06CC06CC06310627062A0020062F0631002006480628200C0633062706CC062A0020062A062306CC06CC062F00200634062F"), _
        "confirmed", True
ConfirmExit:
    mInternalPricingRefresh = False
    Application.EnableEvents = previousEvents
    Exit Sub
ConfirmFailed:
    mInternalPricingRefresh = False
    Application.EnableEvents = previousEvents
    Err.Raise Err.Number, "ConfirmPricingBatchWriteback", Err.Description
End Sub

Private Sub ConfirmPricingWriteback(ByVal messageText As String, _
                                    ByVal revisionText As String, _
                                    ByVal confirmedValue As String, _
                                    ByVal updatedAt As String)
    Dim settings As Worksheet
    Dim inputCell As Range
    Dim previousEvents As Boolean
    Dim noteText As String

    Set settings = ConfigSheet()
    Set inputCell = WritebackCell(mWritebackSettingKey)
    If inputCell Is Nothing Then Exit Sub
    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    Application.EnableEvents = False
    If IsSHA256RevisionText(revisionText) Then
        settings.Range("G14").Value2 = revisionText
    End If
    Select Case mWritebackSettingKey
        Case "yuan_price"
            settings.Range("B10").Value = CDbl(confirmedValue)
            settings.Range("G18").Value = CDbl(confirmedValue)
            If Not WritebackHasNewerProposal() Then
                inputCell.Value = CDbl(confirmedValue)
                RestoreVisibleCNYFormula
                ClearDurablePendingPricingIntent
            End If
        Case "dollar_price": settings.Range("G19").Value = CDbl(confirmedValue)
        Case "cny_effective_date": settings.Range("G20").Value2 = _
            CanonicalDateText(inputCell.Value2)
        Case "usd_effective_date": settings.Range("H16").Value2 = _
            CanonicalDateText(inputCell.Value2)
        Case "profit_margin_percent": settings.Range("G21").Value = _
            inputCell.Value
        Case "air_express_price_per_kg": settings.Range("G22").Value = _
            inputCell.Value
        Case "price_rounding_digits": settings.Range("H18").Value = _
            inputCell.Value
    End Select
    noteText = U("062A062306CC06CC062F002006480631062F067E06310633")
    If Len(messageText) > 0 Then noteText = noteText & vbCrLf & messageText
    If Len(updatedAt) > 0 Then noteText = noteText & vbCrLf & _
        U("0632064506270646003A") & " " & updatedAt
    If Len(revisionText) > 0 Then noteText = noteText & vbCrLf & _
        U("06460633062E0647003A") & " " & revisionText
    If Len(confirmedValue) > 0 Then noteText = noteText & vbCrLf & _
        U("06450642062F06270631003A") & " " & confirmedValue
    MarkWritebackState mWritebackSettingKey, "confirmed", noteText
    SetOperationProgressSurface "writeback_confirmed", 100, _
        U("062A063A06CC06CC06310020062A062306CC06CC062F00200634062F") & _
        IIf(Len(confirmedValue) > 0, ": " & confirmedValue, vbNullString), _
        "confirmed", True
CleanExit:
    Application.EnableEvents = previousEvents
End Sub

Private Sub MarkWritebackState(ByVal settingKey As String, _
                               ByVal stateName As String, _
                               ByVal noteText As String)
    Dim inputCell As Range
    Dim displayRange As Range
    Dim fillColor As Long
    Dim keyValue As Variant

    If settingKey = "settings_batch" Then
        If Len(mActiveWritebackKeys) = 0 Then Exit Sub
        For Each keyValue In Split(mActiveWritebackKeys, ",")
            MarkWritebackState CStr(keyValue), stateName, noteText
        Next keyValue
        Exit Sub
    End If

    Set inputCell = WritebackCell(settingKey)
    If inputCell Is Nothing Then Exit Sub
    Set displayRange = inputCell.MergeArea
    Select Case LCase$(stateName)
        Case "pending", "sending": fillColor = RGB(252, 228, 178)
        Case "confirmed": fillColor = RGB(226, 239, 218)
        Case "failed": fillColor = RGB(244, 204, 204)
        Case "warning": fillColor = RGB(255, 242, 204)
        Case Else: fillColor = RGB(255, 248, 231)
    End Select
    displayRange.Interior.Color = fillColor
    If settingKey = "yuan_price" Or settingKey = "site_confirmation" Then
        On Error Resume Next
        ThisWorkbook.Worksheets(1).Range("M7").Interior.Color = fillColor
        On Error GoTo 0
    End If
    On Error Resume Next
    inputCell.Comment.Delete
    If Len(noteText) > 0 Then
        inputCell.AddComment noteText
        inputCell.Comment.Visible = False
        inputCell.Comment.Shape.TextFrame.Characters.Font.Name = _
            DEFAULT_PERSIAN_FONT
    End If
    On Error GoTo 0
End Sub

Public Function ValidatePricingWritebackUIForValidation() As Boolean
    Dim inputCell As Range
    Dim keys As Variant
    Dim keyValue As Variant

    On Error GoTo Failed
    Set inputCell = WritebackCell("yuan_price")
    If inputCell Is Nothing Or inputCell.Address(False, False) <> "B18" Then _
        GoTo Failed
    If WritebackCell("dollar_price").Address(False, False) <> "B19" Or _
       WritebackCell("cny_effective_date").Address(False, False) <> "B20" Or _
       WritebackCell("usd_effective_date").Address(False, False) <> "E20" Or _
       WritebackCell("profit_margin_percent").Address(False, False) <> "B21" Or _
       WritebackCell("air_express_price_per_kg").Address(False, False) <> "B22" Or _
       WritebackCell("price_rounding_digits").Address(False, False) <> "B26" Then _
        GoTo Failed
    keys = Split("yuan_price,dollar_price,cny_effective_date," & _
                 "usd_effective_date,profit_margin_percent," & _
                 "air_express_price_per_kg,price_rounding_digits", ",")
    For Each keyValue In keys
        Set inputCell = WritebackCell(CStr(keyValue))
        MarkWritebackState CStr(keyValue), "pending", "pending"
        If inputCell.MergeArea.Interior.Color <> RGB(252, 228, 178) Or _
           inputCell.Comment Is Nothing Then GoTo Failed
        MarkWritebackState CStr(keyValue), "confirmed", "confirmed"
        If inputCell.MergeArea.Interior.Color <> RGB(226, 239, 218) Then _
            GoTo Failed
        MarkWritebackState CStr(keyValue), "failed", "failed"
        If inputCell.MergeArea.Interior.Color <> RGB(244, 204, 204) Then _
            GoTo Failed
        MarkWritebackState CStr(keyValue), "warning", "warning"
        If inputCell.MergeArea.Interior.Color <> RGB(255, 242, 204) Then _
            GoTo Failed
        MarkWritebackState CStr(keyValue), "neutral", vbNullString
        If inputCell.MergeArea.Interior.Color <> RGB(255, 248, 231) Or _
           Not inputCell.Comment Is Nothing Then GoTo Failed
    Next keyValue
    ValidatePricingWritebackUIForValidation = True
    Exit Function

Failed:
    On Error Resume Next
    For Each keyValue In keys
        MarkWritebackState CStr(keyValue), "neutral", vbNullString
    Next keyValue
    On Error GoTo 0
End Function

Private Function BuildPricingConfirmationRequest( _
        ByVal requestID As String) As String
    Dim settings As Worksheet
    Dim profitPercent As Variant
    Dim body As String

    Set settings = ConfigSheet()
    If Len(mPendingConfirmationTransactionID) <> 36 Or _
       Left$(mPendingConfirmationTransactionID, 4) <> "ptx_" Or _
       Not IsSHA256RevisionText(mPendingConfirmationRevision) Or _
       Not IsSHA256RevisionText(mPendingConfirmationDigest) Then
        Err.Raise vbObjectError + 784, _
            "BuildPricingConfirmationRequest", T("bridge_missing")
    End If
    profitPercent = Empty
    If IsNumeric(settings.Range("B13").Value2) Then
        profitPercent = CDbl(settings.Range("B13").Value2) * 100#
    End If
    body = "{""schema"":" & _
        JsonString(PRICING_CONFIRMATION_REQUEST_SCHEMA) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """transaction_id"":" & _
        JsonString(mPendingConfirmationTransactionID) & "," & _
        """committed_state_revision"":" & _
        JsonString(mPendingConfirmationRevision) & "," & _
        """confirmed_settings_digest"":" & _
        JsonString(mPendingConfirmationDigest) & "," & _
        """confirmed_settings"":{" & _
        """dollar_price"":" & JsonNumberOrNull(settings.Range("B11").Value2) & "," & _
        """yuan_price"":" & JsonNumberOrNull(settings.Range("B10").Value2) & "," & _
        """effective_date"":" & JsonString(CanonicalDateText(settings.Range("B12").Value2)) & "," & _
        """usd_effective_date"":" & JsonString(CanonicalDateText(settings.Range("H16").Value2)) & "," & _
        """cny_effective_date"":" & JsonString(CanonicalDateText(settings.Range("H17").Value2)) & "," & _
        """profit_margin_percent"":" & JsonNumberOrNull(profitPercent) & "," & _
        """air_express_price_per_kg"":" & JsonNumberOrNull(settings.Range("B14").Value2) & "," & _
        """air_express_currency"":" & JsonString(UCase$(Trim$(CStr(settings.Range("H14").Value2)))) & "," & _
        """shipping_catalog_revision"":" & JsonString(Trim$(CStr(settings.Range("H15").Value2))) & "," & _
        """price_rounding_digits"":" & JsonNumberOrNull(settings.Range("B15").Value2) & "," & _
        """price_rounding_mode"":" & JsonString(Trim$(CStr(settings.Range("H19").Value2))) & "}}"
    BuildPricingConfirmationRequest = body
End Function

Public Function ValidateOperationProgressUIForValidation() As Boolean
    Dim targetSheet As Worksheet
    Dim track As Shape
    Dim fill As Shape
    Dim label As Shape
    Dim sheetName As Variant

    On Error GoTo Failed
    InitializeOperationProgress
    If Not OperationProgressReadyHiddenForValidation() Then GoTo Failed
    For Each sheetName In Array(PriceSheet().Name, ConfigSheet().Name)
        Set targetSheet = ThisWorkbook.Worksheets(CStr(sheetName))
        Set track = targetSheet.Shapes(PROGRESS_TRACK_SHAPE)
        Set fill = targetSheet.Shapes(PROGRESS_FILL_SHAPE)
        Set label = targetSheet.Shapes(PROGRESS_TEXT_SHAPE)
        If track.Width <= 0 Or label.Width <= 0 Then GoTo Failed
        If StrComp(label.TextFrame2.TextRange.Font.NameComplexScript, _
                   "Yekan Bakh", vbTextCompare) <> 0 Then GoTo Failed
    Next sheetName
    SetRefreshProgress "1/4"
    If Not OperationProgressStageMatchesForValidation( _
        "refresh_request", 10, _
        U("062F0631062E064806270633062A002006280647200C063106480632063106330627064606CC0020062B0628062A00200634062F")) Then _
        GoTo Failed
    SetRefreshProgress "2/4"
    If Not OperationProgressStageMatchesForValidation( _
        "refresh_wait", -1, _
        U("062F0631002006270646062A0638062706310020062206450627062F0647200C06330627063206CC00200645064606280639")) Then _
        GoTo Failed
    SetSnapshotProgress 0, 0, 0
    If Not OperationProgressStageMatchesForValidation( _
        "refresh_download", -1, _
        U("062F06310020062D062706440020062F063106CC06270641062A0020062F0627062F0647200C0647062706CC0020062C062F06CC062F0020062F06310020067E063300200627063200200632064506CC06460647061B00200641064706310633062A002006410639064406CC00200642062706280644002006270633062A06410627062F0647002006270633062A002E002E002E")) Then _
        GoTo Failed
    SetRefreshProgress "3/4"
    If Not OperationProgressStageMatchesForValidation( _
        "refresh_validate", 70, _
        U("062F06310020062D06270644002006270639062A06280627063106330646062C06CC0020062F0627062F0647200C06470627")) Then _
        GoTo Failed
    SetRefreshProgress "4/4"
    If Not OperationProgressStageMatchesForValidation( _
        "refresh_apply", 90, _
        U("062F06310020062D0627064400200627063906450627064400200631062F06CC0641200C0647062700200648002006410631064506480644200C06470627")) Then _
        GoTo Failed
    SetOperationProgressSurface "completed", 100, _
        U("06280647200C063106480632063106330627064606CC002006A906270645064400200634062F"), _
        "confirmed", False
    If Not OperationProgressStageMatchesForValidation( _
        "completed", 100, _
        U("06280647200C063106480632063106330627064606CC002006A906270645064400200634062F")) Then _
        GoTo Failed
    For Each sheetName In Array(PriceSheet().Name, ConfigSheet().Name)
        Set targetSheet = ThisWorkbook.Worksheets(CStr(sheetName))
        Set track = targetSheet.Shapes(PROGRESS_TRACK_SHAPE)
        Set fill = targetSheet.Shapes(PROGRESS_FILL_SHAPE)
        If Abs(fill.Width - track.Width) > 1 Or _
           fill.Fill.ForeColor.RGB <> RGB(22, 163, 74) Then GoTo Failed
    Next sheetName
    InitializeOperationProgress
    If Not OperationProgressReadyHiddenForValidation() Then GoTo Failed
    ValidateOperationProgressUIForValidation = True
    Exit Function

Failed:
    On Error Resume Next
    InitializeOperationProgress
    On Error GoTo 0
End Function

Private Function OperationProgressReadyHiddenForValidation() As Boolean
    Dim targetSheet As Worksheet
    Dim track As Shape
    Dim fill As Shape
    Dim label As Shape
    Dim sheetName As Variant

    If VarType(Application.StatusBar) <> vbBoolean Then Exit Function
    If CBool(Application.StatusBar) Then Exit Function
    For Each sheetName In Array(PriceSheet().Name, ConfigSheet().Name)
        Set targetSheet = ThisWorkbook.Worksheets(CStr(sheetName))
        Set track = targetSheet.Shapes(PROGRESS_TRACK_SHAPE)
        Set fill = targetSheet.Shapes(PROGRESS_FILL_SHAPE)
        Set label = targetSheet.Shapes(PROGRESS_TEXT_SHAPE)
        If track.Visible <> msoFalse Or fill.Visible <> msoFalse Or _
           label.Visible <> msoFalse Then Exit Function
        If InStr(1, label.AlternativeText, "stage=ready", _
                 vbBinaryCompare) = 0 Then Exit Function
    Next sheetName
    OperationProgressReadyHiddenForValidation = True
End Function

Private Function OperationProgressStageMatchesForValidation( _
    ByVal stageName As String, _
    ByVal percentValue As Long, _
    ByVal messageText As String) As Boolean
    Dim expectedText As String
    Dim forbiddenText As String
    Dim targetSheet As Worksheet
    Dim track As Shape
    Dim fill As Shape
    Dim label As Shape
    Dim sheetName As Variant

    forbiddenText = U("064606270645063906CC0646")
    If percentValue < 0 Then
        expectedText = messageText
    Else
        expectedText = CStr(percentValue) & "% - " & messageText
    End If
    If StrComp(CStr(Application.StatusBar), expectedText, _
               vbBinaryCompare) <> 0 Then Exit Function
    If InStr(1, CStr(Application.StatusBar), forbiddenText, _
             vbBinaryCompare) > 0 Then Exit Function
    For Each sheetName In Array(PriceSheet().Name, ConfigSheet().Name)
        Set targetSheet = ThisWorkbook.Worksheets(CStr(sheetName))
        Set track = targetSheet.Shapes(PROGRESS_TRACK_SHAPE)
        Set fill = targetSheet.Shapes(PROGRESS_FILL_SHAPE)
        Set label = targetSheet.Shapes(PROGRESS_TEXT_SHAPE)
        If track.Visible <> msoTrue Or fill.Visible <> msoTrue Or _
           label.Visible <> msoTrue Then Exit Function
        If StrComp(label.TextFrame2.TextRange.Text, expectedText, _
                   vbBinaryCompare) <> 0 Then Exit Function
        If InStr(1, label.TextFrame2.TextRange.Text, forbiddenText, _
                 vbBinaryCompare) > 0 Then Exit Function
        If InStr(1, label.AlternativeText, "stage=" & stageName, _
                 vbBinaryCompare) = 0 Or _
           InStr(1, label.AlternativeText, "percent=" & _
                 CStr(percentValue), vbBinaryCompare) = 0 Then Exit Function
        If percentValue < 0 And _
           (fill.Visible <> msoTrue Or fill.Width <= 1 Or _
            fill.Width >= track.Width) Then Exit Function
    Next sheetName
    OperationProgressStageMatchesForValidation = True
End Function

Private Function WritebackCell(ByVal settingKey As String) As Range
    Dim addressText As String

    Select Case settingKey
        Case "yuan_price", "site_confirmation": addressText = "B18"
        Case "dollar_price": addressText = "B19"
        Case "cny_effective_date": addressText = "B20"
        Case "usd_effective_date": addressText = "E20"
        Case "profit_margin_percent": addressText = "B21"
        Case "air_express_price_per_kg": addressText = "B22"
        Case "price_rounding_digits": addressText = "B26"
        Case Else: Exit Function
    End Select
    Set WritebackCell = ConfigSheet().Range(addressText)
End Function

Private Function BuildPricingWritebackRequest(ByVal settingKey As String, _
                                              ByVal requestID As String) As String
    Dim settings As Worksheet
    Dim profitPercent As Variant
    Dim shippingCurrency As String
    Dim shippingRevision As String
    Dim usdEffectiveDate As String
    Dim cnyEffectiveDate As String
    Dim yuanProposal As Variant
    Dim body As String

    Set settings = ConfigSheet()
    profitPercent = Empty
    If IsNumeric(settings.Range("B21").Value2) Then
        profitPercent = CDbl(settings.Range("B21").Value2) * 100#
    End If
    shippingCurrency = UCase$(Trim$(CStr(settings.Range("H14").Value2)))
    shippingRevision = Trim$(CStr(settings.Range("H15").Value2))
    usdEffectiveDate = CanonicalDateText(settings.Range("E20").Value2)
    cnyEffectiveDate = CanonicalDateText(settings.Range("B20").Value2)
    yuanProposal = settings.Range("B18").Value2
    If settingKey = "yuan_price" And _
       Len(mActiveWritebackDesiredValue) > 0 Then
        yuanProposal = CDbl(mActiveWritebackDesiredValue)
    End If
    ValidatePricingSettings settings, profitPercent, shippingCurrency, _
        shippingRevision, usdEffectiveDate, cnyEffectiveDate
    body = "{""schema"":" & JsonString(PRICING_WRITEBACK_REQUEST_SCHEMA) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """setting_key"":" & JsonString(settingKey) & "," & _
        """expected_state_revision"":" & _
        JsonString(Trim$(CStr(settings.Range("G14").Value2))) & "," & _
        """previous_confirmed_value"":" & _
        JsonString(ConfirmedWritebackValue(settingKey)) & "," & _
        """settings"":{" & _
        """dollar_price"":" & JsonNumberOrNull(settings.Range("B19").Value2) & "," & _
        """yuan_price"":" & JsonNumberOrNull(yuanProposal) & "," & _
        """effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """usd_effective_date"":" & JsonString(usdEffectiveDate) & "," & _
        """cny_effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """profit_margin_percent"":" & JsonNumberOrNull(profitPercent) & "," & _
        """air_express_price_per_kg"":" & JsonNumberOrNull(settings.Range("B22").Value2) & "," & _
        """air_express_currency"":" & JsonString(shippingCurrency) & "," & _
        """shipping_catalog_revision"":" & JsonString(shippingRevision) & "," & _
        """price_rounding_digits"":" & JsonNumberOrNull(settings.Range("B26").Value2) & "," & _
        """price_rounding_mode"":" & JsonString(PRICE_ROUNDING_MODE) & "}}"
    BuildPricingWritebackRequest = body
End Function

Private Sub PersistPendingPricingIntent(ByVal settingKey As String, _
                                        ByVal desiredValue As String, _
                                        ByVal generation As Long)
    Dim settings As Worksheet

    If settingKey <> "yuan_price" Then Exit Sub
    Set settings = ConfigSheet()
    settings.Range("H48").Value2 = desiredValue
    settings.Range("H49").Value2 = settingKey
    settings.Range("H50").Value2 = generation
End Sub

Private Sub ClearDurablePendingPricingIntent()
    On Error Resume Next
    ConfigSheet().Range("H48:H50").ClearContents
    On Error GoTo 0
End Sub

Private Function WritebackHasNewerProposal() As Boolean
    WritebackHasNewerProposal = _
        (mWritebackSettingKey = "yuan_price") And _
        (mPricingEditGeneration > mActiveWritebackGeneration)
End Function

Private Sub RestoreVisibleCNYFormula()
    On Error Resume Next
    ThisWorkbook.Worksheets(1).Range("M7").Formula = _
        "=IF('" & U("062A0646063806CC06450627062A") & _
        "'!G18="""","""",'" & U("062A0646063806CC06450627062A") & _
        "'!G18)"
    On Error GoTo 0
End Sub

Private Function ConfirmedWritebackValue(ByVal settingKey As String) As String
    Dim settings As Worksheet

    Set settings = ConfigSheet()
    Select Case settingKey
        Case "yuan_price"
            ConfirmedWritebackValue = CanonicalCellText(settings.Range("G18").Value2)
        Case "dollar_price"
            ConfirmedWritebackValue = CanonicalCellText(settings.Range("G19").Value2)
        Case "cny_effective_date"
            ConfirmedWritebackValue = CanonicalDateText(settings.Range("G20").Value2)
        Case "usd_effective_date"
            ConfirmedWritebackValue = CanonicalDateText(settings.Range("H16").Value2)
        Case "profit_margin_percent"
            ConfirmedWritebackValue = CanonicalCellText(CDbl(settings.Range("G21").Value2) * 100#)
        Case "air_express_price_per_kg"
            ConfirmedWritebackValue = CanonicalCellText(settings.Range("G22").Value2)
        Case "price_rounding_digits"
            ConfirmedWritebackValue = CanonicalCellText(settings.Range("H18").Value2)
        Case Else
            Err.Raise vbObjectError + 790, "ConfirmedWritebackValue", T("invalid_workbook")
    End Select
End Function

Public Sub PreviewPricingChanges()
    ResumeAfterCancelledClose
    KickQueuedAsyncDispatch
    If mRefreshInProgress Or mPricingActionInProgress Then Exit Sub
    CancelScheduledPricingPreview True
    PreviewPricingChangesCore False
End Sub

Private Sub SchedulePricingPreview()
    If mSaveRenameSchedulesSuspended Then
        mSaveRenamePreviewPending = True
        Exit Sub
    End If
    If mWorkbookClosing Or mPricingPreviewScheduled Then Exit Sub
    On Error GoTo ScheduleFailed
    mScheduledPricingPreviewTime = Now + TimeSerial(0, 0, 1)
    mPricingPreviewScheduled = True
    Application.OnTime EarliestTime:=mScheduledPricingPreviewTime, _
        Procedure:=QualifiedWorkbookMacro("RunScheduledPricingPreview"), _
        Schedule:=True
    Exit Sub

ScheduleFailed:
    mPricingPreviewScheduled = False
    Err.Clear
End Sub

Public Sub RunScheduledPricingPreview()
    mPricingPreviewScheduled = False
    If mWorkbookClosing Or Not mPricingPreviewQueued Then Exit Sub
    If mRefreshInProgress Or mPricingActionInProgress Then Exit Sub
    mPricingPreviewQueued = False
    PreviewPricingChangesCore False
End Sub

Private Sub CancelScheduledPricingPreview(ByVal clearQueued As Boolean)
    If mPricingPreviewScheduled Then
        On Error Resume Next
        Application.OnTime EarliestTime:=mScheduledPricingPreviewTime, _
            Procedure:=QualifiedWorkbookMacro("RunScheduledPricingPreview"), _
            Schedule:=False
        On Error GoTo 0
    End If
    mPricingPreviewScheduled = False
    If clearQueued Then mPricingPreviewQueued = False
End Sub

Private Sub PreviewPricingChangesCore(ByVal showMessage As Boolean)
    On Error GoTo BeginFailed
    ResetFiniteOperationContext
    mOperationKind = "preview"
    mOperationOriginalSourceID = mSourceID
    mOperationOriginalSourceDataset = mSourceDataset
    mOperationOriginalSourceRevision = mSourceRevision
    mOperationShowMessage = showMessage
    mOperationStartedAt = PhaseTimestamp()
    mOperationContractStartedAt = mOperationStartedAt
    mPricingActionInProgress = True
    mLastOperationName = "preview"
    mLastOperationSucceeded = False
    mLastOperationError = vbNullString
    InvalidatePricingPreview
    mOperationRequestedSettings = PricingSettingsCanonical()
    mPricingCSRFToken = vbNullString
    CaptureOperationCancelKey
    RegisterRefreshCancelHotkey
    BeginContractRequest "contract"
    Exit Sub

BeginFailed:
    FailActiveOperation Err.Number, "PreviewPricingChangesCore", _
        Err.Description
End Sub

Public Sub ApplyPricingChanges()
    ResumeAfterCancelledClose
    KickQueuedAsyncDispatch
    If mRefreshInProgress Or mPricingActionInProgress Then Exit Sub
    ApplyPricingChangesCore True, False
End Sub

Private Sub ApplyPricingChangesCore(ByVal requireConfirmation As Boolean, _
                                    ByVal showSuccess As Boolean)
    Dim settings As Worksheet
    Dim answer As Long

    On Error GoTo BeginFailed
    Set settings = ConfigSheet()
    If Len(mLastPreviewDigest) = 0 Then
        settings.Range("B23").Value2 = T("preview_first")
        Exit Sub
    End If
    If mLastPreviewSettings <> PricingSettingsCanonical() Then
        InvalidatePricingPreview
        settings.Range("B23").Value2 = T("preview_first")
        Exit Sub
    End If
    If Len(mLastApplyRequestID) = 0 And _
       mLastPreviewStateRevision <> _
           Trim$(CStr(settings.Range("G14").Value2)) Then
        InvalidatePricingPreview
        settings.Range("B23").Value2 = T("preview_first")
        Exit Sub
    End If

    If requireConfirmation Then
        answer = ConfirmUnicodeMessage( _
            T("apply_confirm") & vbCrLf & vbCrLf & _
            CStr(settings.Range("B23").Value2), _
            T("apply_title"))
        If answer <> vbYes Then Exit Sub
    End If

    If Len(mLastApplyRequestID) = 0 Then
        mLastApplyRequestID = NewRequestID("apply")
        settings.Range("G28").Value2 = mLastApplyRequestID
    End If

    ResetFiniteOperationContext
    mOperationKind = "apply"
    mOperationOriginalSourceID = mSourceID
    mOperationOriginalSourceDataset = mSourceDataset
    mOperationOriginalSourceRevision = mSourceRevision
    mOperationRequireConfirmation = requireConfirmation
    mOperationShowMessage = showSuccess
    mOperationStartedAt = PhaseTimestamp()
    mOperationContractStartedAt = mOperationStartedAt
    mOperationSavedPreviewDigest = mLastPreviewDigest
    mOperationSavedPreviewExpiresAt = mLastPreviewExpiresAt
    mOperationSavedPreviewStateRevision = mLastPreviewStateRevision
    mOperationSavedPreviewSettings = mLastPreviewSettings
    mOperationSavedPreviewWarningCount = mLastPreviewWarningCount
    mOperationSavedApplyRequestID = mLastApplyRequestID
    mPricingActionInProgress = True
    mLastOperationName = "apply"
    mLastOperationSucceeded = False
    mLastOperationError = vbNullString
    mPricingCSRFToken = vbNullString
    CaptureOperationCancelKey
    RegisterRefreshCancelHotkey
    BeginContractRequest "contract"
    Exit Sub

BeginFailed:
    FailActiveOperation Err.Number, "ApplyPricingChangesCore", _
        Err.Description
End Sub

Private Sub InvalidatePricingPreview()
    mLastPreviewDigest = vbNullString
    mLastPreviewExpiresAt = vbNullString
    mLastPreviewStateRevision = vbNullString
    mLastPreviewSettings = vbNullString
    mLastPreviewWarningCount = 0
    mLastApplyRequestID = vbNullString

    On Error Resume Next
    ConfigSheet().Range("G26:G28").ClearContents
    On Error GoTo 0
End Sub

Private Function PricingStateAddresses() As Variant
    PricingStateAddresses = Split( _
        "B10|B11|B12|B13|B14|B15|B18|B19|B20|E20|B21|B22|B26|" & _
        "G14|G15|G18|G19|G20|G21|G22|G31|G32|G33|G34|G35|G36|" & _
        "G37|G38|G39|G40|G41|G42|G43|G44|G45|G46|G47|" & _
        "G56|H14|H15|H16|H17|H18|H19", _
        "|")
End Function

Private Function CapturePricingStateSnapshot( _
    ByVal settings As Worksheet) As Variant
    Dim addresses As Variant
    Dim values() As Variant
    Dim index As Long

    addresses = PricingStateAddresses()
    ReDim values(LBound(addresses) To UBound(addresses))
    For index = LBound(addresses) To UBound(addresses)
        values(index) = settings.Range(CStr(addresses(index))).Value2
    Next index
    CapturePricingStateSnapshot = values
End Function

Private Sub RestorePricingStateSnapshot(ByVal settings As Worksheet, _
                                        ByVal values As Variant)
    Dim addresses As Variant
    Dim proposalAddresses As Variant
    Dim proposalAddress As Variant
    Dim preservedProposals As Object
    Dim index As Long
    Dim previousEvents As Boolean
    Dim previousInternalRefresh As Boolean
    Dim restoreErrorNumber As Long
    Dim restoreErrorSource As String
    Dim restoreErrorDescription As String

    previousEvents = Application.EnableEvents
    previousInternalRefresh = mInternalPricingRefresh
    On Error GoTo RestoreFailed
    Application.EnableEvents = False
    mInternalPricingRefresh = True
    addresses = PricingStateAddresses()
    If LBound(values) <> LBound(addresses) Or _
       UBound(values) <> UBound(addresses) Then
        Err.Raise vbObjectError + 199, _
                  "RestorePricingStateSnapshot", _
                  T("invalid_workbook")
    End If
    If mPricingEditGeneration > mOperationPricingEditGeneration Then
        Set preservedProposals = CreateObject("Scripting.Dictionary")
        preservedProposals.CompareMode = vbBinaryCompare
        proposalAddresses = Split("B18|B19|B20|E20|B21|B22|B26", "|")
        For Each proposalAddress In proposalAddresses
            preservedProposals(CStr(proposalAddress)) = _
                settings.Range(CStr(proposalAddress)).Value2
        Next proposalAddress
    End If
    For index = LBound(addresses) To UBound(addresses)
        settings.Range(CStr(addresses(index))).Value2 = values(index)
    Next index
    If Not preservedProposals Is Nothing Then
        For Each proposalAddress In preservedProposals.Keys
            settings.Range(CStr(proposalAddress)).Value2 = _
                preservedProposals(CStr(proposalAddress))
        Next proposalAddress
    End If
RestoreExit:
    mInternalPricingRefresh = previousInternalRefresh
    Application.EnableEvents = previousEvents
    If restoreErrorNumber <> 0 Then
        Err.Raise restoreErrorNumber, restoreErrorSource, _
                  restoreErrorDescription
    End If
    Exit Sub

RestoreFailed:
    restoreErrorNumber = Err.Number
    restoreErrorSource = Err.Source
    restoreErrorDescription = Err.Description
    Resume RestoreExit
End Sub

Private Function PricingSettingsCanonical() As String
    Dim settings As Worksheet

    Set settings = ConfigSheet()
    PricingSettingsCanonical = _
        CanonicalCellText(settings.Range("B18").Value2) & "|" & _
        CanonicalCellText(settings.Range("B19").Value2) & "|" & _
        CanonicalDateText(settings.Range("B20").Value2) & "|" & _
        CanonicalDateText(settings.Range("E20").Value2) & "|" & _
        CanonicalCellText(settings.Range("B21").Value2) & "|" & _
        CanonicalCellText(settings.Range("B22").Value2) & "|" & _
        CanonicalCellText(settings.Range("B26").Value2) & "|" & _
        CanonicalCellText(settings.Range("H14").Value2) & "|" & _
        CanonicalCellText(settings.Range("H15").Value2)
End Function

Private Function WarningSummary(ByVal result As JsonValue) As String
    Dim warnings As JsonValue
    Dim warning As JsonValue
    Dim rowIndex As Long
    Dim messageText As String

    Set warnings = JsonRuntime.JsonMember(result, "warnings")
    If warnings Is Nothing Or warnings.Kind <> "array" Then Exit Function
    For rowIndex = 1 To JsonRuntime.JsonArrayCount(warnings)
        Set warning = JsonRuntime.JsonArrayItem(warnings, rowIndex)
        If warning Is Nothing Or warning.Kind <> "object" Then GoTo NextWarning
        If Not WarningHasPositiveCount(warning) Then GoTo NextWarning
        messageText = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(warning, "message_fa"))))
        If Len(messageText) = 0 Then messageText = T("sync_failed")
        If Len(messageText) > 0 Then
            WarningSummary = WarningSummary & vbCrLf & U("2022") & " " & _
                messageText
        End If
NextWarning:
    Next rowIndex
End Function

Private Function ValidatedWarningCount(ByVal result As JsonValue) As Long
    Dim warnings As JsonValue
    Dim warning As JsonValue
    Dim rowIndex As Long

    Set warnings = JsonRuntime.JsonMember(result, "warnings")
    If warnings Is Nothing Or warnings.Kind <> "array" Then
        Err.Raise vbObjectError + 165, "ValidatedWarningCount", _
                  T("sync_failed")
    End If
    For rowIndex = 1 To JsonRuntime.JsonArrayCount(warnings)
        Set warning = JsonRuntime.JsonArrayItem(warnings, rowIndex)
        If warning Is Nothing Or warning.Kind <> "object" Then
            Err.Raise vbObjectError + 166, "ValidatedWarningCount", _
                      T("sync_failed")
        End If
        If WarningHasPositiveCount(warning) Then _
            ValidatedWarningCount = ValidatedWarningCount + 1
    Next rowIndex
End Function

Private Sub ReadSourceIdentity(ByVal contract As JsonValue)
    Dim sourceValue As JsonValue

    Set sourceValue = JsonRuntime.JsonMember(contract, "source")
    If sourceValue Is Nothing Or sourceValue.Kind <> "object" Then
        Err.Raise vbObjectError + 102, "ReadSourceIdentity", T("invalid_workbook")
    End If
    mSourceID = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(sourceValue, "id"))))
    mSourceDataset = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(sourceValue, "dataset"))))
    mSourceRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(sourceValue, "revision"))))
    If Len(mSourceID) = 0 Or Len(mSourceDataset) = 0 Or _
       Len(mSourceRevision) = 0 Then
        Err.Raise vbObjectError + 103, "ReadSourceIdentity", T("invalid_workbook")
    End If
End Sub

Private Sub EnsureSourceIdentity()
    If Len(mSourceID) = 0 Or Len(mSourceDataset) = 0 Or _
       Len(mSourceRevision) = 0 Then
        Err.Raise vbObjectError + 103, "EnsureSourceIdentity", _
                  T("invalid_workbook")
    End If
End Sub

Private Function PricingSnapshotRequestJson(ByVal requestID As String) As String
    Dim maxAgeSeconds As Long
    Dim stateRevisionField As String

    EnsureSourceIdentity
    maxAgeSeconds = PRICING_SNAPSHOT_CACHE_SECONDS
    If mForceFreshSnapshot Then maxAgeSeconds = 0
    If Len(mRequiredSnapshotStateRevision) > 0 Then
        If Not IsSHA256RevisionText(mRequiredSnapshotStateRevision) Then
            Err.Raise vbObjectError + 130, "PricingSnapshotRequestJson", _
                      T("invalid_workbook")
        End If
        maxAgeSeconds = 0
        stateRevisionField = ",""expected_state_revision"":" & _
            JsonString(mRequiredSnapshotStateRevision)
    End If
    PricingSnapshotRequestJson = _
        "{""schema"":" & JsonString(PRICING_SNAPSHOT_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID) & "," & _
        """channel"":" & JsonString(PRICING_CONTRACT_CHANNEL) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """source"":{" & _
        """id"":" & JsonString(mSourceID) & "," & _
        """dataset"":" & JsonString(mSourceDataset) & "," & _
        """revision"":" & JsonString(mSourceRevision) & "}," & _
        """locale"":""fa"",""projection"":" & _
        JsonString(PRICING_SNAPSHOT_PROJECTION) & "," & _
        """max_age_seconds"":" & _
        CStr(maxAgeSeconds) & stateRevisionField & "}"
End Function

Private Function PricingRevisionRequestJson(ByVal requestID As String) As String
    EnsureSourceIdentity
    PricingRevisionRequestJson = _
        "{""schema"":" & JsonString(PRICING_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """operation"":""revision""," & _
        """client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID) & "," & _
        """channel"":" & JsonString(PRICING_CONTRACT_CHANNEL) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """source"":{" & _
        """id"":" & JsonString(mSourceID) & "," & _
        """dataset"":" & JsonString(mSourceDataset) & "," & _
        """revision"":" & JsonString(mSourceRevision) & "}}"
End Function

Private Sub ParsePricingSnapshotJob(ByVal root As JsonValue, _
                                    ByVal expectedRequestID As String, _
                                    ByRef jobID As String, _
                                    ByRef jobStatus As String, _
                                    ByRef jobCode As String, _
                                    ByRef waitURL As String, _
                                    ByRef eventsURL As String, _
                                    ByRef jobEventsURL As String, _
                                    ByRef payloadURL As String, _
                                    ByRef cancelURL As String, _
                                    ByRef expectedETag As String, _
                                    ByRef snapshotRevision As String, _
                                    ByRef expectedStateRevision As String, _
                                    ByRef pricingStateRevision As String, _
                                    ByRef datasetRevision As String, _
                                    ByRef completedPages As Long, _
                                    ByRef totalPages As Long, _
                                    ByRef rowCount As Long)
    Dim parsedJobID As String
    Dim parsedRequestID As String
    Dim progress As JsonValue
    Dim sourceValue As JsonValue
    Dim identity As JsonValue
    Dim capacity As JsonValue
    Dim previousCompletedPages As Long
    Dim previousTotalPages As Long
    Dim previousRowCount As Long
    Dim expectedJobURL As String
    Dim statusURL As String

    previousCompletedPages = completedPages
    previousTotalPages = totalPages
    previousRowCount = rowCount

    If root Is Nothing Or root.Kind <> "object" Or _
       SiteText(root, "schema") <> PRICING_SNAPSHOT_JOB_SCHEMA Or _
       SiteText(root, "projection") <> PRICING_SNAPSHOT_PROJECTION Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    parsedRequestID = SiteText(root, "request_id")
    parsedJobID = SiteText(root, "job_id")
    If parsedRequestID <> expectedRequestID Or Len(parsedJobID) = 0 Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    If Len(jobID) > 0 And parsedJobID <> jobID Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    jobID = parsedJobID
    Set sourceValue = JsonRuntime.JsonMember(root, "source")
    If sourceValue Is Nothing Or sourceValue.Kind <> "object" Or _
       SiteText(sourceValue, "id") <> mSourceID Or _
       SiteText(sourceValue, "dataset") <> mSourceDataset Or _
       SiteText(sourceValue, "revision") <> mSourceRevision Or _
       LCase$(SiteText(root, "locale")) <> "fa" Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    jobStatus = LCase$(SiteText(root, "status"))
    jobCode = LCase$(SiteText(root, "code"))
    Select Case jobStatus
        Case "running", "cancelling", "ready", "failed", "cancelled", _
             "invalidated", "expired"
        Case Else
            Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                      T("invalid_workbook")
    End Select

    Set capacity = JsonRuntime.JsonMember(root, "capacity")
    If capacity Is Nothing Or capacity.Kind <> "object" Or _
       RequiredWholeNumber(capacity, "page_size") <> SNAPSHOT_PAGE_SIZE Or _
       RequiredWholeNumber(capacity, "max_pages") <> MAX_STATE_PAGES Or _
       RequiredWholeNumber(capacity, "max_rows") <> MAX_SNAPSHOT_ROWS Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If

    expectedJobURL = PricingBaseURL() & "/snapshots/" & jobID
    statusURL = SnapshotRouteURL(SiteText(root, "status_url"))
    waitURL = SnapshotRouteURL(SiteText(root, "wait_url"))
    eventsURL = SnapshotRouteURL(SiteText(root, "events_url"))
    jobEventsURL = SnapshotRouteURL(SiteText(root, "job_events_url"))
    payloadURL = SnapshotRouteURL(SiteText(root, "payload_url"))
    cancelURL = SnapshotRouteURL(SiteText(root, "cancel_url"))
    If statusURL <> expectedJobURL Or _
       waitURL <> expectedJobURL & "?wait=terminal" Or _
       eventsURL <> PricingBaseURL() & "/events" Or _
       jobEventsURL <> expectedJobURL Or _
       payloadURL <> expectedJobURL & "/payload" Or _
       cancelURL <> expectedJobURL Or _
       LCase$(SiteText(root, "events_accept")) <> "text/event-stream" Or _
       SiteText(root, "events_cursor_header") <> "Last-Event-ID" Or _
       RequiredWholeNumber(root, "events_keepalive_seconds") <> 15 Or _
       RequiredWholeNumber(root, "events_history_capacity") <> 256 Or _
       SiteText(root, "events_lifecycle") <> _
           "session_scoped_durable" Or _
       SiteText(root, "job_events_lifecycle") <> _
           "job_scoped_progress" Or _
       SiteText(root, "event_schema") <> PRICING_SNAPSHOT_EVENT_SCHEMA Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    Set progress = JsonRuntime.JsonMember(root, "progress")
    If progress Is Nothing Or progress.Kind <> "object" Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If
    completedPages = RequiredWholeNumber(progress, "completed_pages")
    totalPages = RequiredWholeNumber(progress, "total_pages")
    rowCount = RequiredWholeNumber(progress, "rows")
    If totalPages > MAX_STATE_PAGES Or rowCount > MAX_SNAPSHOT_ROWS Or _
       completedPages > totalPages Or _
       (totalPages = 0 And completedPages <> 0) Or _
       completedPages < previousCompletedPages Or _
       rowCount < previousRowCount Or _
       (previousTotalPages > 0 And totalPages <> previousTotalPages) Then
        Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                  T("invalid_workbook")
    End If

    expectedETag = SiteText(root, "etag")
    snapshotRevision = SiteText(root, "snapshot_revision")
    expectedStateRevision = SiteText(root, "state_revision")
    pricingStateRevision = SiteText(root, "pricing_state_revision")
    Set identity = JsonRuntime.JsonMember(root, "identity")
    If Not identity Is Nothing Then _
        datasetRevision = SiteText(identity, "catalog_revision")
    If jobStatus = "ready" Then
        If Not IsSHA256RevisionText(snapshotRevision) Or _
           Not IsSHA256RevisionText(StrongETagRevision(expectedETag)) Or _
           Not IsSHA256RevisionText(expectedStateRevision) Or _
           Not IsSHA256RevisionText(pricingStateRevision) Or _
           Not IsSHA256RevisionText(datasetRevision) Then
            Err.Raise vbObjectError + 130, "ParsePricingSnapshotJob", _
                      T("invalid_workbook")
        End If
    End If
End Sub

Private Sub SetSnapshotProgress(ByVal completedPages As Long, _
                                ByVal totalPages As Long, _
                                ByVal rowCount As Long)
    Dim progressText As String
    Dim progressPercent As Long

    progressText = "2/4"
    If totalPages > 0 Then
        progressText = progressText & " (" & CStr(completedPages) & "/" & _
            CStr(totalPages) & ", " & CStr(rowCount) & ")"
        progressPercent = 20 + CLng((completedPages / totalPages) * 40#)
        SetOperationProgressSurface "refresh_download", progressPercent, _
            U("062F06310020062D062706440020062F063106CC06270641062A0020062F0627062F0647200C06470627") & _
            " (" & CStr(rowCount) & ")", "active", False
    Else
        SetOperationProgressSurface "refresh_download", -1, _
            U("062F06310020062D062706440020062F063106CC06270641062A0020062F0627062F0647200C0647062706CC0020062C062F06CC062F0020062F06310020067E063300200627063200200632064506CC06460647061B00200641064706310633062A002006410639064406CC00200642062706280644002006270633062A06410627062F0647002006270633062A002E002E002E"), _
            "active", False
    End If
    PumpExcelMessages
End Sub

Private Function SnapshotRouteURL(ByVal routeValue As String) As String
    Dim candidate As String
    Dim pricingRoot As String
    Dim pricingPrefix As String

    routeValue = Trim$(routeValue)
    pricingRoot = PricingBaseURL()
    pricingPrefix = LCase$(pricingRoot & "/")
    If Left$(routeValue, 1) = "/" Then
        candidate = Left$(pricingRoot, _
            Len(pricingRoot) - Len("/api/pricing-sync")) & routeValue
    Else
        candidate = routeValue
    End If
    If Not IsAllowedPricingBridgeUrl(candidate) Or _
       LCase$(Left$(candidate, Len(pricingPrefix))) <> pricingPrefix Then
        Err.Raise vbObjectError + 130, "SnapshotRouteURL", _
                  T("invalid_workbook")
    End If
    SnapshotRouteURL = candidate
End Function

Private Sub RaiseSnapshotHTTPError(ByVal statusCode As Long, _
                                   ByVal responseText As String)
    Dim errorCode As String

    errorCode = LCase$(ResponseErrorCode(responseText))
    If errorCode = "canonical_source_mismatch" Then
        Err.Raise vbObjectError + 121, "RaiseSnapshotHTTPError", _
                  T("invalid_workbook")
    End If
    Err.Raise vbObjectError + 235, "RaiseSnapshotHTTPError", _
              FriendlySnapshotFailure(errorCode)
End Sub

Private Sub RaiseSnapshotJobFailure(ByVal errorCode As String)
    errorCode = LCase$(Trim$(errorCode))
    Select Case errorCode
        Case "snapshot_revision_changed", "snapshot_integrity_failed", _
             "digitalogic_reconciled_snapshot_changed"
            Err.Raise vbObjectError + 130, "RaiseSnapshotJobFailure", _
                      T("invalid_workbook")
        Case "canonical_source_mismatch"
            Err.Raise vbObjectError + 121, "RaiseSnapshotJobFailure", _
                      T("invalid_workbook")
        Case Else
            Err.Raise vbObjectError + 236, "RaiseSnapshotJobFailure", _
                      FriendlySnapshotFailure(errorCode)
    End Select
End Sub

Private Function IsSnapshotDriftCode(ByVal errorCode As String) As Boolean
    Select Case LCase$(Trim$(errorCode))
        Case "snapshot_source_changed", "snapshot_catalog_changed", _
             "snapshot_pricing_state_changed", "snapshot_generation_changed", _
             "snapshot_revision_changed", _
             "digitalogic_reconciled_snapshot_changed"
            IsSnapshotDriftCode = True
        Case Else
            IsSnapshotDriftCode = False
    End Select
End Function

Private Function FriendlySnapshotFailure(ByVal errorCode As String) As String
    Select Case LCase$(Trim$(errorCode))
        Case "remote_unavailable", "remote_not_configured", _
             "canonical_source_unavailable"
            FriendlySnapshotFailure = T("pricing_service_unavailable")
        Case "snapshot_source_revision_conflict"
            FriendlySnapshotFailure = T("source_changed")
        Case "snapshot_source_changed", "snapshot_catalog_changed", _
             "snapshot_pricing_state_changed", "snapshot_generation_changed", _
             "snapshot_revision_changed", _
             "digitalogic_reconciled_snapshot_changed"
            FriendlySnapshotFailure = T("snapshot_changed")
        Case Else
            FriendlySnapshotFailure = T("sync_retry")
    End Select
End Function

Private Function SnapshotRowFieldsMatch(ByVal payload As JsonValue) As Boolean
    Dim fields As JsonValue
    Dim fieldValue As JsonValue
    Dim fieldIndex As Long
    Dim signature As String

    If payload Is Nothing Then Exit Function
    Set fields = JsonRuntime.JsonMember(payload, "row_fields")
    If fields Is Nothing Then Exit Function
    If fields.Kind <> "array" Then Exit Function
    If JsonRuntime.JsonArrayCount(fields) <> _
       PRICING_SNAPSHOT_ROW_FIELD_COUNT Then Exit Function
    For fieldIndex = 1 To PRICING_SNAPSHOT_ROW_FIELD_COUNT
        Set fieldValue = JsonRuntime.JsonArrayItem(fields, fieldIndex)
        If fieldValue Is Nothing Then Exit Function
        If fieldValue.Kind <> "string" Then Exit Function
        If fieldIndex > 1 Then signature = signature & ","
        signature = signature & CStr(JsonRuntime.JsonScalar(fieldValue))
    Next fieldIndex
    SnapshotRowFieldsMatch = _
        (signature = PRICING_SNAPSHOT_ROW_FIELDS)
End Function

Private Function ImportPricingSnapshotPayload( _
        ByVal responseText As String, ByVal expectedETag As String, _
        ByVal expectedSnapshotRevision As String, _
        ByVal expectedStateRevision As String, _
        ByVal siteRows As Object, _
        ByRef validatedState As JsonValue, _
        ByRef validatedCatalog As JsonValue, _
        ByRef validatedDatasetRevision As String, _
        ByRef validatedSourceRevision As String, _
        ByRef validatedCountSignature As String) As Long
    Dim payload As JsonValue
    Dim sourceValue As JsonValue
    Dim stateSource As JsonValue
    Dim integrity As JsonValue
    Dim mutationGuard As JsonValue
    Dim previewGuard As JsonValue
    Dim applyGuard As JsonValue
    Dim state As JsonValue
    Dim catalog As JsonValue
    Dim pagination As JsonValue
    Dim rowsValue As JsonValue
    Dim rowValue As JsonValue
    Dim warnings As JsonValue
    Dim reconciliation As JsonValue
    Dim counts As JsonValue
    Dim snapshotRevision As String
    Dim stateRevision As String
    Dim datasetRevision As String
    Dim sourceRevision As String
    Dim countSignature As String
    Dim rowIndex As Long
    Dim pageRows As Long
    Dim paginationLimit As Long
    Dim paginationTotal As Long
    Dim integrityPageCount As Long
    Dim warningCount As Long
    Dim identityKey As String
    Dim rawStateText As String
    Dim rawStateDigest As String
    Dim currentRevision As String
    Dim submittedRevision As String
    Dim matchedCount As Long
    Dim patrisOnlyCount As Long
    Dim wooOnlyCount As Long
    Dim unionCount As Long
    Dim ambiguousCount As Long

    mSnapshotValidationStage = "wire-digest"
    ' The callback hashes the exact ResponseBody bytes before UTF-8 decoding.
    ' This parser therefore receives only a body already bound to the strong
    ' response ETag; it still validates that the expected ETag is strong.
    If Not IsSHA256RevisionText(StrongETagRevision(expectedETag)) Then _
        GoTo InvalidSnapshot
    rawStateText = RawJSONObjectMember(responseText, "state")
    If Len(rawStateText) = 0 Then GoTo InvalidSnapshot
    rawStateDigest = SHA256Revision(rawStateText)
    rawStateText = vbNullString

    mSnapshotValidationStage = "parse"
    Set payload = JsonRuntime.ParseJson(responseText)
    mSnapshotValidationStage = "envelope"
    If payload Is Nothing Or payload.Kind <> "object" Or _
       SiteText(payload, "schema") <> PRICING_SNAPSHOT_PAYLOAD_SCHEMA Or _
       SiteText(payload, "projection") <> PRICING_SNAPSHOT_PROJECTION Then _
        GoTo InvalidSnapshot
    If Not SnapshotRowFieldsMatch(payload) Then GoTo InvalidSnapshot
    snapshotRevision = SiteText(payload, "snapshot_revision")
    If snapshotRevision <> expectedSnapshotRevision Or _
       Not IsSHA256RevisionText(snapshotRevision) Then GoTo InvalidSnapshot

    Set sourceValue = JsonRuntime.JsonMember(payload, "source")
    If sourceValue Is Nothing Or sourceValue.Kind <> "object" Then _
        GoTo InvalidSnapshot
    If SiteText(sourceValue, "id") <> mSourceID Or _
       SiteText(sourceValue, "dataset") <> mSourceDataset Or _
       SiteText(sourceValue, "revision") <> mSourceRevision Then _
        GoTo InvalidSnapshot

    stateRevision = SiteText(payload, "state_revision")
    If stateRevision <> expectedStateRevision Or _
       Not IsSHA256RevisionText(stateRevision) Then GoTo InvalidSnapshot
    Set integrity = JsonRuntime.JsonMember(payload, "integrity")
    If integrity Is Nothing Or integrity.Kind <> "object" Then _
        GoTo InvalidSnapshot
    If LCase$(SiteText(integrity, "algorithm")) <> "sha256" Or _
       Not IsSHA256RevisionText(SiteText(integrity, "state_digest")) Or _
       Not IsSHA256RevisionText( _
           SiteText(integrity, "catalog_metadata_digest")) Or _
       Not IsSHA256RevisionText( _
           SiteText(integrity, "page_revisions_digest")) Or _
       Not IsSHA256RevisionText( _
           SiteText(integrity, "warnings_digest")) Then _
        GoTo InvalidSnapshot
    If rawStateDigest <> SiteText(integrity, "state_digest") Then _
        GoTo InvalidSnapshot

    mSnapshotValidationStage = "mutation-guard"
    Set mutationGuard = JsonRuntime.JsonMember(payload, "mutation_guard")
    If mutationGuard Is Nothing Or mutationGuard.Kind <> "object" Or _
       SiteText(mutationGuard, "expected_state_revision") <> _
           stateRevision Then GoTo InvalidSnapshot
    Set previewGuard = JsonRuntime.JsonMember(mutationGuard, "preview")
    Set applyGuard = JsonRuntime.JsonMember(mutationGuard, "apply")
    If previewGuard Is Nothing Or applyGuard Is Nothing Then _
        GoTo InvalidSnapshot
    If UCase$(SiteText(previewGuard, "method")) <> "POST" Or _
       SiteText(previewGuard, "path") <> "/api/pricing-sync/preview" Or _
       Not BooleanValue(JsonRuntime.JsonText( _
           previewGuard, "requires_idempotency_key")) Or _
       Not BooleanValue(JsonRuntime.JsonText( _
           previewGuard, "requires_if_match")) Then GoTo InvalidSnapshot
    If UCase$(SiteText(applyGuard, "method")) <> "POST" Or _
       SiteText(applyGuard, "path") <> "/api/pricing-sync/apply" Or _
       SiteText(applyGuard, "confirmation") <> "APPLY" Or _
       Not BooleanValue(JsonRuntime.JsonText( _
           applyGuard, "requires_idempotency_key")) Or _
        Not BooleanValue(JsonRuntime.JsonText( _
            applyGuard, "requires_if_match")) Then GoTo InvalidSnapshot

    mSnapshotValidationStage = "state-contract"
    Set state = JsonRuntime.JsonMember(payload, "state")
    If state Is Nothing Or state.Kind <> "object" Or _
       SiteText(state, "schema") <> PRICING_STATE_SCHEMA Or _
       SiteText(state, "state_revision") <> stateRevision Then _
        GoTo InvalidSnapshot
    Set stateSource = JsonRuntime.JsonMember(state, "source")
    If stateSource Is Nothing Or stateSource.Kind <> "object" Then _
        GoTo InvalidSnapshot
    currentRevision = SiteText(stateSource, "current_revision")
    submittedRevision = SiteText(stateSource, "submitted_revision")
    If currentRevision <> mSourceRevision Or _
       submittedRevision <> currentRevision Or _
       Not BooleanValue(JsonRuntime.JsonText( _
           stateSource, "revision_matches_current")) Then GoTo InvalidSnapshot
    RejectProjectionIntegrityWarnings state
    Set catalog = JsonRuntime.JsonMember(state, "catalog")
    If catalog Is Nothing Or catalog.Kind <> "object" Or _
       SiteText(catalog, "dataset") <> "reconciled_products" Then _
        GoTo InvalidSnapshot
    datasetRevision = SiteText(catalog, "dataset_revision")
    If Not IsSHA256RevisionText(datasetRevision) Or _
       datasetRevision <> SiteText(integrity, "dataset_revision") Or _
       CatalogColumnSignature(catalog) <> RECONCILED_COLUMN_KEYS Then _
        GoTo InvalidSnapshot
    sourceRevision = StateSourceRevision(state, catalog)
    If sourceRevision <> mSourceRevision Then GoTo InvalidSnapshot
    countSignature = CatalogCountSignature(catalog)
    Set reconciliation = JsonRuntime.JsonMember(catalog, "reconciliation")
    If reconciliation Is Nothing Or reconciliation.Kind <> "object" Then _
        GoTo InvalidSnapshot
    Set counts = JsonRuntime.JsonMember(reconciliation, "counts")
    If counts Is Nothing Or counts.Kind <> "object" Then GoTo InvalidSnapshot
    matchedCount = RequiredWholeNumber(counts, "matched")
    patrisOnlyCount = RequiredWholeNumber(counts, "patris_only")
    wooOnlyCount = RequiredWholeNumber(counts, "woo_only")
    unionCount = RequiredWholeNumber(counts, "union_rows")
    ambiguousCount = RequiredWholeNumber(counts, "ambiguous_codes")

    Set rowsValue = JsonRuntime.JsonMember(catalog, "rows")
    Set pagination = JsonRuntime.JsonMember(catalog, "pagination")
    If rowsValue Is Nothing Or rowsValue.Kind <> "array" Or _
       pagination Is Nothing Or pagination.Kind <> "object" Then _
        GoTo InvalidSnapshot
    pageRows = JsonRuntime.JsonArrayCount(rowsValue)
    paginationLimit = RequiredWholeNumber(pagination, "limit")
    paginationTotal = RequiredWholeNumber(pagination, "total")
    If RequiredWholeNumber(pagination, "page") <> 1 Or _
       RequiredWholeNumber(pagination, "pages") <> 1 Or _
       BooleanValue(JsonRuntime.JsonText(pagination, "has_more")) Or _
       paginationTotal < 1 Or paginationTotal > MAX_SNAPSHOT_ROWS Or _
       paginationLimit <> paginationTotal Or _
       pageRows <> paginationTotal Then GoTo InvalidSnapshot
    If ambiguousCount <> 0 Or _
       matchedCount + patrisOnlyCount + wooOnlyCount <> unionCount Or _
       unionCount <> paginationTotal Then GoTo InvalidSnapshot

    integrityPageCount = RequiredWholeNumber(integrity, "page_count")
    If integrityPageCount < 1 Or integrityPageCount > MAX_STATE_PAGES Or _
       RequiredWholeNumber(integrity, "remote_total") <> paginationTotal Or _
       RequiredWholeNumber(integrity, "row_count") <> paginationTotal Or _
       RequiredWholeNumber(integrity, "distinct_sync_keys") <> _
           paginationTotal Then GoTo InvalidSnapshot
    Set warnings = JsonRuntime.JsonMember(state, "warnings")
    If Not warnings Is Nothing Then
        If warnings.Kind <> "array" Then GoTo InvalidSnapshot
        warningCount = JsonRuntime.JsonArrayCount(warnings)
    End If
    If RequiredWholeNumber(integrity, "warning_count") <> warningCount Then _
        GoTo InvalidSnapshot

    mSnapshotValidationStage = "rows"
    For rowIndex = 1 To pageRows
        Set rowValue = JsonRuntime.JsonArrayItem(rowsValue, rowIndex)
        If rowValue Is Nothing Then GoTo InvalidSnapshot
        If rowValue.Kind <> "array" Then GoTo InvalidSnapshot
        If JsonRuntime.JsonArrayCount(rowValue) <> _
           PRICING_SNAPSHOT_ROW_FIELD_COUNT Then GoTo InvalidSnapshot
        identityKey = SnapshotProjectedRowText( _
            rowValue, SNAPSHOT_FIELD_SYNC_KEY)
        If Len(identityKey) = 0 Or siteRows.Exists(identityKey) Then _
            GoTo InvalidSnapshot
        Select Case LCase$(SnapshotProjectedRowText( _
            rowValue, SNAPSHOT_FIELD_RECONCILIATION_STATUS))
            Case "matched", "patris_only", "woo_only"
            Case Else
                GoTo InvalidSnapshot
        End Select
        siteRows.Add identityKey, rowValue
        If rowIndex Mod UI_PUMP_ROW_INTERVAL = 0 Then PumpExcelMessages
    Next rowIndex
    If siteRows.Count <> paginationTotal Then GoTo InvalidSnapshot

    mSnapshotValidationStage = "validated"
    ' Return validated object references and metadata without mutating Excel.
    ' CommitRefreshSnapshot applies state and tables together under rollback.
    Set validatedState = state
    Set validatedCatalog = catalog
    validatedDatasetRevision = datasetRevision
    validatedSourceRevision = sourceRevision
    validatedCountSignature = countSignature
    mSnapshotValidationStage = "complete"
    ImportPricingSnapshotPayload = paginationTotal
    Exit Function

InvalidSnapshot:
    mStatePageTimingText = _
        "snapshot_validation=" & mSnapshotValidationStage
    Err.Raise vbObjectError + 130, "ImportPricingSnapshotPayload", _
              T("invalid_workbook")
End Function

Private Sub ApplyReconciliationCounts(ByVal catalog As JsonValue)
    Dim reconciliation As JsonValue
    Dim counts As JsonValue
    Dim settings As Worksheet
    Dim wooRaw As Variant
    Dim wooLeaves As Variant
    Dim excludedParents As Variant

    Set reconciliation = JsonRuntime.JsonMember(catalog, "reconciliation")
    If reconciliation Is Nothing Or reconciliation.Kind <> "object" Then
        Err.Raise vbObjectError + 118, "ApplyReconciliationCounts", _
                  T("invalid_workbook")
    End If
    Set counts = JsonRuntime.JsonMember(reconciliation, "counts")
    If counts Is Nothing Or counts.Kind <> "object" Then
        Err.Raise vbObjectError + 119, "ApplyReconciliationCounts", _
                  T("invalid_workbook")
    End If
    Set settings = ConfigSheet()
    wooRaw = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "woocommerce_raw"))
    wooLeaves = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "woocommerce_leaves"))
    excludedParents = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "variable_parents_excluded"))
    If IsEmpty(wooRaw) Or IsEmpty(wooLeaves) Or IsEmpty(excludedParents) Or _
       CDbl(wooRaw) < 0 Or CDbl(wooLeaves) < 0 Or _
       CDbl(excludedParents) < 0 Or _
       CLng(wooRaw) <> CDbl(wooRaw) Or _
       CLng(wooLeaves) <> CDbl(wooLeaves) Or _
       CLng(excludedParents) <> CDbl(excludedParents) Then
        Err.Raise vbObjectError + 125, "ApplyReconciliationCounts", _
                  T("invalid_workbook")
    End If
    settings.Range("G32").Value2 = CLng(wooRaw)
    settings.Range("G33").Value2 = CLng(wooLeaves)
    settings.Range("G34").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "matched"))
    settings.Range("G35").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "patris_only"))
    settings.Range("G36").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "woo_only"))
    settings.Range("G38").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "union_rows"))
    settings.Range("G41").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "ambiguous_codes"))
    settings.Range("G42").Value2 = NumericOrBlank( _
        JsonRuntime.JsonText(counts, "patris_products"))
    settings.Range("G43").Value2 = CLng(excludedParents)
End Sub

Private Function StateSourceRevision(ByVal state As JsonValue, _
                                     ByVal catalog As JsonValue) As String
    Dim sourceValue As JsonValue
    Dim reconciliation As JsonValue
    Dim reconciliationSource As JsonValue
    Dim currentRevision As String
    Dim submittedRevision As String
    Dim reconciledRevision As String
    Dim matchesCurrentValue As Variant

    Set sourceValue = JsonRuntime.JsonMember(state, "source")
    If sourceValue Is Nothing Or sourceValue.Kind <> "object" Then Exit Function
    currentRevision = SiteText(sourceValue, "current_revision")
    submittedRevision = SiteText(sourceValue, "submitted_revision")
    matchesCurrentValue = JsonRuntime.JsonText( _
        sourceValue, "revision_matches_current")

    Set reconciliation = JsonRuntime.JsonMember(catalog, "reconciliation")
    If Not reconciliation Is Nothing Then
        Set reconciliationSource = JsonRuntime.JsonMember( _
            reconciliation, "source")
    End If
    If Not reconciliationSource Is Nothing Then
        reconciledRevision = SiteText(reconciliationSource, "revision")
    End If

    If Len(currentRevision) = 0 Then currentRevision = reconciledRevision
    If Len(currentRevision) = 0 Then
        currentRevision = SiteText(sourceValue, "revision")
    End If
    If Not IsSHA256RevisionText(currentRevision) Or _
       currentRevision <> mSourceRevision Then Exit Function
    If Len(submittedRevision) > 0 And _
       submittedRevision <> currentRevision Then Exit Function
    If Len(reconciledRevision) > 0 And _
       reconciledRevision <> currentRevision Then Exit Function
    If Not IsEmpty(matchesCurrentValue) Then
        If VarType(matchesCurrentValue) <> vbBoolean Then Exit Function
        If Not CBool(matchesCurrentValue) Then Exit Function
    End If
    StateSourceRevision = currentRevision
End Function

Private Sub RejectProjectionIntegrityWarnings(ByVal state As JsonValue)
    If HasProjectionIntegrityWarning(state, 0) Then
        Err.Raise vbObjectError + 192, _
                  "RejectProjectionIntegrityWarnings", _
                  T("projection_integrity_warning")
    End If
End Sub

Private Function HasProjectionIntegrityWarning(ByVal value As JsonValue, _
                                               ByVal depth As Long) As Boolean
    Dim memberName As Variant
    Dim child As JsonValue
    Dim itemIndex As Long
    Dim warningCode As String

    If value Is Nothing Then Exit Function
    If depth > 64 Then
        Err.Raise vbObjectError + 193, _
                  "HasProjectionIntegrityWarning", _
                  T("invalid_workbook")
    End If

    Select Case value.Kind
        Case "string"
            warningCode = LCase$(Trim$(CStr(value.Scalar)))
            HasProjectionIntegrityWarning = _
                Left$(warningCode, Len("product_type_cache_drift")) = _
                    "product_type_cache_drift" Or _
                Left$(warningCode, Len("projection_integrity")) = _
                    "projection_integrity"
        Case "object"
            For Each memberName In value.ObjectItems.Keys
                ' Product rows have their own price/data warnings. Integrity
                ' warnings belong to state/catalog metadata, so skip the large
                ' row payload while scanning every metadata object and array.
                If LCase$(CStr(memberName)) <> "rows" Then
                    Set child = value.ObjectItems(CStr(memberName))
                    If HasProjectionIntegrityWarning(child, depth + 1) Then
                        HasProjectionIntegrityWarning = True
                        Exit Function
                    End If
                End If
            Next memberName
        Case "array"
            For itemIndex = 1 To value.ArrayItems.Count
                Set child = value.ArrayItems(itemIndex)
                If HasProjectionIntegrityWarning(child, depth + 1) Then
                    HasProjectionIntegrityWarning = True
                    Exit Function
                End If
            Next itemIndex
    End Select
End Function

Private Sub ValidateProjectionIntegrityGuard()
    If Not ProjectionIntegrityFixtureRejected( _
        "{""integrity"":{""warnings"":[{""code"":" & _
        """product_type_cache_drift""}]}}") Then
        Err.Raise vbObjectError + 194, _
                  "ValidateProjectionIntegrityGuard", _
                  "Exact product-type drift warning was not rejected."
    End If
    If Not ProjectionIntegrityFixtureRejected( _
        "{""metadata"":{""warnings"":[{""code"":" & _
        """product_type_cache_drift_term_changed""}]}}") Then
        Err.Raise vbObjectError + 195, _
                  "ValidateProjectionIntegrityGuard", _
                  "Prefixed product-type drift warning was not rejected."
    End If
    If Not ProjectionIntegrityFixtureRejected( _
        "{""warnings"":[{""code"":""projection_integrity""}]}") Then
        Err.Raise vbObjectError + 196, _
                  "ValidateProjectionIntegrityGuard", _
                  "Exact projection-integrity warning was not rejected."
    End If
    If Not ProjectionIntegrityFixtureRejected( _
        "{""catalog"":{""integrity"":{""warnings"":[{""code"":" & _
        """projection_integrity_product_type_readback_failed""}]}}}") Then
        Err.Raise vbObjectError + 197, _
                  "ValidateProjectionIntegrityGuard", _
                  "Prefixed projection-integrity warning was not rejected."
    End If
    If ProjectionIntegrityFixtureRejected( _
        "{""integrity"":{""warnings"":[{""code"":" & _
        """price_input_missing""}]}}") Then
        Err.Raise vbObjectError + 198, _
                  "ValidateProjectionIntegrityGuard", _
                  "A benign warning was incorrectly rejected."
    End If
End Sub

Private Function ProjectionIntegrityFixtureRejected( _
    ByVal fixtureJson As String) As Boolean
    Dim fixture As JsonValue
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo Rejected
    Set fixture = JsonRuntime.ParseJson(fixtureJson)
    RejectProjectionIntegrityWarnings fixture
    Exit Function

Rejected:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    Err.Clear
    On Error GoTo 0
    If savedErrorNumber <> vbObjectError + 192 Then
        Err.Raise savedErrorNumber, _
                  "ProjectionIntegrityFixtureRejected", _
                  savedErrorDescription
    End If
    ProjectionIntegrityFixtureRejected = True
End Function

Private Function CatalogColumnSignature(ByVal catalog As JsonValue) As String
    Dim columnsValue As JsonValue
    Dim columnValue As JsonValue
    Dim columnIndex As Long
    Dim columnKey As String

    Set columnsValue = JsonRuntime.JsonMember(catalog, "columns")
    If columnsValue Is Nothing Or columnsValue.Kind <> "array" Then
        Err.Raise vbObjectError + 132, "CatalogColumnSignature", _
                  T("invalid_workbook")
    End If
    For columnIndex = 1 To JsonRuntime.JsonArrayCount(columnsValue)
        Set columnValue = JsonRuntime.JsonArrayItem( _
            columnsValue, columnIndex)
        If columnValue Is Nothing Or columnValue.Kind <> "object" Then
            Err.Raise vbObjectError + 133, "CatalogColumnSignature", _
                      T("invalid_workbook")
        End If
        columnKey = SiteText(columnValue, "key")
        If Len(columnKey) = 0 Or InStr(1, columnKey, ",", vbBinaryCompare) > 0 Then
            Err.Raise vbObjectError + 134, "CatalogColumnSignature", _
                      T("invalid_workbook")
        End If
        If Len(CatalogColumnSignature) > 0 Then
            CatalogColumnSignature = CatalogColumnSignature & ","
        End If
        CatalogColumnSignature = CatalogColumnSignature & columnKey
    Next columnIndex
End Function

Private Function CatalogCountSignature(ByVal catalog As JsonValue) As String
    Dim reconciliation As JsonValue
    Dim counts As JsonValue
    Dim fields As Variant
    Dim fieldName As Variant
    Dim countValue As Variant

    Set reconciliation = JsonRuntime.JsonMember(catalog, "reconciliation")
    If reconciliation Is Nothing Or reconciliation.Kind <> "object" Then
        Err.Raise vbObjectError + 135, "CatalogCountSignature", _
                  T("invalid_workbook")
    End If
    Set counts = JsonRuntime.JsonMember(reconciliation, "counts")
    If counts Is Nothing Or counts.Kind <> "object" Then
        Err.Raise vbObjectError + 136, "CatalogCountSignature", _
                  T("invalid_workbook")
    End If
    fields = Array( _
        "patris_products", "woocommerce_raw", "woocommerce_leaves", _
        "union_rows", "matched", "patris_only", "woo_only", _
        "ambiguous_codes", "variable_parents_excluded")
    For Each fieldName In fields
        countValue = NumericOrBlank( _
            JsonRuntime.JsonText(counts, CStr(fieldName)))
        If IsEmpty(countValue) Or CDbl(countValue) < 0 Or _
           CLng(countValue) <> CDbl(countValue) Then
            Err.Raise vbObjectError + 137, "CatalogCountSignature", _
                      T("invalid_workbook")
        End If
        If Len(CatalogCountSignature) > 0 Then
            CatalogCountSignature = CatalogCountSignature & "|"
        End If
        CatalogCountSignature = CatalogCountSignature & CStr(fieldName) & _
            "=" & CStr(CLng(countValue))
    Next fieldName
End Function

Private Function RequiredWholeNumber(ByVal objectValue As JsonValue, _
                                     ByVal fieldName As String) As Long
    Dim numberValue As Variant

    numberValue = NumericOrBlank( _
        JsonRuntime.JsonText(objectValue, fieldName))
    If IsEmpty(numberValue) Or CDbl(numberValue) < 0 Or _
       CLng(numberValue) <> CDbl(numberValue) Then
        Err.Raise vbObjectError + 138, "RequiredWholeNumber", _
                  T("invalid_workbook")
    End If
    RequiredWholeNumber = CLng(numberValue)
End Function

Private Function IsSHA256RevisionText(ByVal value As String) As Boolean
    Dim index As Long
    Dim character As String

    If Len(value) <> 71 Or Left$(value, 7) <> "sha256:" Or _
       value <> LCase$(value) Then Exit Function
    For index = 8 To 71
        character = Mid$(value, index, 1)
        If Not ((character >= "0" And character <= "9") Or _
                (character >= "a" And character <= "f")) Then Exit Function
    Next index
    IsSHA256RevisionText = True
End Function

Private Sub ApplyGlobalState(ByVal state As JsonValue)
    Dim settings As Worksheet
    Dim primarySettings As JsonValue
    Dim freshness As JsonValue
    Dim currencyState As JsonValue
    Dim shippingState As JsonValue
    Dim profitMargin As JsonValue
    Dim markup As JsonValue
    Dim priceRounding As JsonValue
    Dim remoteCNY As Variant
    Dim remoteUSD As Variant
    Dim remoteDate As Variant
    Dim remoteProfit As Variant
    Dim remoteShipping As Variant
    Dim remoteRounding As Variant
    Dim remoteRoundingMode As String
    Dim shippingCurrency As String
    Dim shippingRevision As String
    Dim remoteUSDDate As Variant
    Dim remoteCNYDate As Variant
    Dim stale As Boolean
    Dim pricingStateRevision As String

    Set settings = ConfigSheet()
    Set primarySettings = JsonRuntime.JsonMember(state, "settings")
    Set freshness = JsonRuntime.JsonMember(state, "freshness")
    Set currencyState = JsonRuntime.JsonMember(state, "currency")
    Set shippingState = JsonRuntime.JsonMember(state, "shipping")
    Set profitMargin = JsonRuntime.JsonMember(state, "profit_margin")
    Set markup = JsonRuntime.JsonMember(state, "default_markup")
    Set priceRounding = JsonRuntime.JsonMember(state, "price_rounding")

    If Not primarySettings Is Nothing Then
        remoteCNY = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(primarySettings, "yuan_price"))
        remoteUSD = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(primarySettings, "dollar_price"))
        remoteDate = BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "effective_date"))
        remoteUSDDate = BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "usd_effective_date"))
        remoteCNYDate = BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "cny_effective_date"))
        If IsEmpty(remoteCNYDate) Then remoteCNYDate = remoteDate
        If IsEmpty(remoteUSDDate) Then remoteUSDDate = remoteDate
        remoteProfit = NumericOrBlank( _
            JsonRuntime.JsonText(primarySettings, "profit_margin_percent"))
        If Not IsEmpty(remoteProfit) Then remoteProfit = CDbl(remoteProfit) / 100#
        remoteShipping = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(primarySettings, "air_express_price_per_kg"))
        remoteRounding = NumericOrBlank( _
            JsonRuntime.JsonText(primarySettings, "price_rounding_digits"))
        remoteRoundingMode = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "price_rounding_mode"))))
        shippingCurrency = UCase$(Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "air_express_currency")))))
        shippingRevision = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(primarySettings, "shipping_catalog_revision"))))
    Else
        If currencyState Is Nothing Then
            Err.Raise vbObjectError + 112, "ApplyGlobalState", T("bridge_missing")
        End If
        remoteCNY = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(currencyState, "yuan_price"))
        remoteUSD = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(currencyState, "dollar_price"))
        remoteDate = BlankIfNull( _
            JsonRuntime.JsonText(currencyState, "effective_date"))
        If IsEmpty(remoteDate) Then
            remoteDate = BlankIfNull( _
                JsonRuntime.JsonText(currencyState, "update_date"))
        End If
        remoteCNYDate = remoteDate
        remoteUSDDate = remoteDate
        remoteProfit = Empty
        If Not profitMargin Is Nothing Then
            remoteProfit = NumericOrBlank( _
                JsonRuntime.JsonText(profitMargin, "profit_margin_percent"))
        End If
        If IsEmpty(remoteProfit) And Not markup Is Nothing Then
            If BooleanValue(JsonRuntime.JsonText(markup, "configured")) Then
                remoteProfit = NumericOrBlank( _
                    JsonRuntime.JsonText(markup, "profit_percent"))
            End If
        End If
        If Not IsEmpty(remoteProfit) Then remoteProfit = CDbl(remoteProfit) / 100#
    End If

    If IsEmpty(remoteRounding) And Not priceRounding Is Nothing Then
        remoteRounding = NumericOrBlank( _
            JsonRuntime.JsonText(priceRounding, "rounding_digits"))
    End If
    If Len(remoteRoundingMode) = 0 And Not priceRounding Is Nothing Then
        remoteRoundingMode = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(priceRounding, "rounding_mode"))))
    End If
    If IsEmpty(remoteRounding) Or CDbl(remoteRounding) < 0 Or _
       CDbl(remoteRounding) > 9 Or _
       CDbl(remoteRounding) <> Fix(CDbl(remoteRounding)) Or _
       remoteRoundingMode <> PRICE_ROUNDING_MODE Then
        Err.Raise vbObjectError + 207, "ApplyGlobalState", T("rounding_required")
    End If

    If (IsEmpty(remoteShipping) Or Len(shippingCurrency) = 0 Or _
        Len(shippingRevision) = 0) And Not shippingState Is Nothing Then
        remoteShipping = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(shippingState, "price_per_kg"))
        shippingCurrency = UCase$(Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(shippingState, "currency")))))
        shippingRevision = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(shippingState, "catalog_revision"))))
    End If

    stale = False
    If Not freshness Is Nothing Then
        stale = BooleanValue(JsonRuntime.JsonText(freshness, "stale"))
    ElseIf Not currencyState Is Nothing Then
        stale = BooleanValue(JsonRuntime.JsonText(currencyState, "stale"))
    End If
    If Not stale Then
        stale = CurrencyDateAgeDays(CStr(remoteCNYDate)) > _
            CLng(Val(CStr(settings.Range("B25").Value2))) Or _
            CurrencyDateAgeDays(CStr(remoteUSDDate)) > _
            CLng(Val(CStr(settings.Range("B25").Value2)))
    End If

    settings.Range("B10").Value = remoteCNY
    settings.Range("B11").Value = remoteUSD
    settings.Range("B12").Value2 = CanonicalDateText(remoteDate)
    settings.Range("B13").Value = remoteProfit
    settings.Range("B14").Value = remoteShipping
    settings.Range("B15").Value = CLng(remoteRounding)
    pricingStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(state, "pricing_state_revision"))))
    If Not IsSHA256RevisionText(pricingStateRevision) Then
        pricingStateRevision = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(state, "state_revision"))))
    End If
    settings.Range("G14").Value2 = pricingStateRevision
    settings.Range("G15").Value = stale
    settings.Range("H14").Value = shippingCurrency
    settings.Range("H15").Value = shippingRevision
    settings.Range("H17").Value2 = CanonicalDateText(remoteCNYDate)
    settings.Range("H18").Value = CLng(remoteRounding)
    settings.Range("H19").Value2 = remoteRoundingMode

    UpdateProposalCell settings.Range("B18"), settings.Range("G18"), remoteCNY
    UpdateProposalCell settings.Range("B19"), settings.Range("G19"), remoteUSD
    UpdateProposalDateCell settings.Range("B20"), settings.Range("G20"), _
        remoteCNYDate
    UpdateProposalDateCell settings.Range("E20"), settings.Range("H16"), _
        remoteUSDDate
    UpdateProposalCell settings.Range("B21"), settings.Range("G21"), remoteProfit
    UpdateProposalCell settings.Range("B22"), settings.Range("G22"), remoteShipping
    UpdateProposalCell settings.Range("B26"), settings.Range("H18"), _
        CLng(remoteRounding)
    UpdateProposalDriftFlags settings, remoteCNY, remoteUSD, remoteCNYDate, _
        remoteUSDDate, remoteProfit, remoteShipping, CLng(remoteRounding)
    CapturePendingPricingConfirmation state
    ReconcilePricingProposalStates
End Sub

Private Sub CapturePendingPricingConfirmation(ByVal state As JsonValue)
    Dim confirmation As JsonValue
    Dim statusText As String
    Dim transactionID As String
    Dim revisionText As String
    Dim digestText As String
    Dim ackPath As String
    Dim consumerID As String
    Dim channelText As String
    Dim deadlineValue As Variant

    mPendingConfirmationTransactionID = vbNullString
    mPendingConfirmationRevision = vbNullString
    mPendingConfirmationDigest = vbNullString
    mPendingConfirmationDeadline = 0
    Set confirmation = JsonRuntime.JsonMember(state, "confirmation")
    If confirmation Is Nothing Then Exit Sub
    statusText = LCase$(Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "status")))))
    If statusText <> "awaiting_ack" Then Exit Sub
    If Trim$(CStr(JsonRuntime.JsonText(confirmation, "schema"))) <> _
       PRICING_CONFIRMATION_SCHEMA Then Exit Sub
    transactionID = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "transaction_id"))))
    revisionText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "committed_revision"))))
    digestText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "committed_settings_digest"))))
    ackPath = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "ack_path"))))
    consumerID = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "consumer_id"))))
    channelText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(confirmation, "channel"))))
    deadlineValue = NumericOrBlank( _
        JsonRuntime.JsonText(confirmation, "ack_deadline"))
    If Len(transactionID) <> 36 Or Left$(transactionID, 4) <> "ptx_" Or _
       Not IsSHA256RevisionText(revisionText) Or _
       Not IsSHA256RevisionText(digestText) Or _
       ackPath <> "/wp-json/digitalogic/pricing/sync/ack" Or _
       consumerID <> PRICING_CONTRACT_CLIENT_ID Or _
       channelText <> PRICING_CONTRACT_CHANNEL Or _
       IsEmpty(deadlineValue) Then Exit Sub
    mPendingConfirmationTransactionID = transactionID
    mPendingConfirmationRevision = revisionText
    mPendingConfirmationDigest = digestText
    mPendingConfirmationDeadline = CDbl(deadlineValue)
End Sub

Private Sub UpdateProposalCell(ByVal proposal As Range, _
                               ByVal baseline As Range, _
                               ByVal remoteValue As Variant)
    Dim previousBaseline As String
    Dim proposalText As String

    previousBaseline = CanonicalCellText(baseline.Value2)
    proposalText = CanonicalCellText(proposal.Value2)
    If Len(proposalText) = 0 Or proposalText = previousBaseline Then
        proposal.Value = remoteValue
    End If
    baseline.Value = remoteValue
End Sub

Private Sub UpdateProposalDateCell(ByVal proposal As Range, _
                                   ByVal baseline As Range, _
                                   ByVal remoteValue As Variant)
    Dim previousBaseline As String
    Dim proposalText As String
    Dim remoteText As String

    previousBaseline = CanonicalDateText(baseline.Value2)
    proposalText = CanonicalDateText(proposal.Value2)
    remoteText = CanonicalDateText(remoteValue)
    If Len(CanonicalCellText(proposal.Value2)) = 0 Or _
       (Len(proposalText) > 0 And proposalText = previousBaseline) Then
        proposal.Value2 = remoteText
    End If
    baseline.Value2 = remoteText
End Sub

Private Sub UpdateProposalDriftFlags(ByVal settings As Worksheet, _
                                     ByVal remoteCNY As Variant, _
                                     ByVal remoteUSD As Variant, _
                                     ByVal remoteCNYDate As Variant, _
                                     ByVal remoteUSDDate As Variant, _
                                     ByVal remoteProfit As Variant, _
                                     ByVal remoteShipping As Variant, _
                                     ByVal remoteRounding As Variant)
    Dim threshold As Double
    Dim mismatch As Boolean
    Dim critical As Boolean

    threshold = CDbl(settings.Range("B24").Value2)
    CompareProposalNumber settings.Range("B18").Value2, remoteCNY, _
        threshold, mismatch, critical
    CompareProposalNumber settings.Range("B19").Value2, remoteUSD, _
        threshold, mismatch, critical
    CompareProposalNumber settings.Range("B21").Value2, remoteProfit, _
        threshold, mismatch, critical
    CompareProposalNumber settings.Range("B22").Value2, remoteShipping, _
        threshold, mismatch, critical
    CompareProposalInteger settings.Range("B26").Value2, remoteRounding, _
        mismatch, critical
    If Not CanonicalDateValuesEqual( _
        settings.Range("B20").Value2, remoteCNYDate) Then mismatch = True
    If Not CanonicalDateValuesEqual( _
        settings.Range("E20").Value2, remoteUSDDate) Then mismatch = True
    settings.Range("G39").Value2 = mismatch
    settings.Range("G40").Value2 = critical
End Sub

Private Sub CompareProposalInteger(ByVal proposalValue As Variant, _
                                   ByVal remoteValue As Variant, _
                                   ByRef mismatch As Boolean, _
                                   ByRef critical As Boolean)
    Dim proposalNumber As Variant
    Dim remoteNumber As Variant

    proposalNumber = NumericOrBlank(proposalValue)
    remoteNumber = NumericOrBlank(remoteValue)
    If IsEmpty(proposalNumber) Or IsEmpty(remoteNumber) Then
        If IsEmpty(proposalNumber) Xor IsEmpty(remoteNumber) Then
            mismatch = True
            critical = True
        End If
        Exit Sub
    End If
    If CDbl(proposalNumber) <> Fix(CDbl(proposalNumber)) Or _
       CDbl(proposalNumber) < 0 Or CDbl(proposalNumber) > 9 Then
        mismatch = True
        critical = True
        Exit Sub
    End If
    If CLng(proposalNumber) <> CLng(remoteNumber) Then mismatch = True
End Sub

Private Sub CompareProposalNumber(ByVal proposalValue As Variant, _
                                  ByVal remoteValue As Variant, _
                                  ByVal threshold As Double, _
                                  ByRef mismatch As Boolean, _
                                  ByRef critical As Boolean)
    Dim proposalNumber As Variant
    Dim remoteNumber As Variant
    Dim difference As Double

    proposalNumber = NumericOrBlank(proposalValue)
    remoteNumber = NumericOrBlank(remoteValue)
    If IsEmpty(proposalNumber) Or IsEmpty(remoteNumber) Then
        If IsEmpty(proposalNumber) Xor IsEmpty(remoteNumber) Then
            mismatch = True
            critical = True
        End If
        Exit Sub
    End If
    If Abs(CDbl(proposalNumber) - CDbl(remoteNumber)) <= 0.000000001 Then _
        Exit Sub
    mismatch = True
    If CDbl(remoteNumber) = 0 Then
        critical = True
    Else
        difference = Abs(CDbl(proposalNumber) - CDbl(remoteNumber)) / _
            Abs(CDbl(remoteNumber))
        If difference > threshold Then critical = True
    End If
End Sub

Private Function ImportReconciledCatalog(ByVal reconciledRows As Object) As Long
    Dim reconciledRow As JsonValue
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim mainOutput() As Variant
    Dim syncOutput() As Variant
    Dim shippingCounts As Object
    Dim profitCounts As Object
    Dim rowKey As Variant
    Dim syncKey As String
    Dim reconciliationStatus As String
    Dim rowKind As String
    Dim codeValue As String
    Dim patrisCodeValue As String
    Dim wooIDValue As String
    Dim weightValue As Variant
    Dim foreignPrice As Variant
    Dim locationValue As String
    Dim categoryValue As String
    Dim priceWarning As String
    Dim goodsCurrency As String
    Dim shippingCurrency As String
    Dim priceSourceCurrency As String
    Dim priceSourceKind As String
    Dim profitValue As Variant
    Dim shippingValue As Variant
    Dim priceSourceAmount As Variant
    Dim commonShipping As Variant
    Dim commonProfit As Variant
    Dim cnyValue As Variant
    Dim usdValue As Variant
    Dim rateDate As Variant
    Dim dataRows As Long
    Dim outputRow As Long
    Dim matchedRows As Long
    Dim sourceOnlyRows As Long
    Dim wooOnlyRows As Long
    Dim ambiguousRows As Long
    Dim phaseStartedAt As Double

    dataRows = reconciledRows.Count
    If dataRows < 1 Then
        Err.Raise vbObjectError + 126, "ImportReconciledCatalog", _
                  T("invalid_workbook")
    End If
    ReDim mainOutput(1 To dataRows, 1 To PRODUCT_COLUMN_COUNT)
    ReDim syncOutput(1 To dataRows, 1 To SYNC_COLUMN_COUNT)
    Set shippingCounts = CreateObject("Scripting.Dictionary")
    Set profitCounts = CreateObject("Scripting.Dictionary")
    mReconcileSeconds = 0#
    mPricingSeconds = 0#
    mTableWriteSeconds = 0#
    mFormattingSeconds = 0#
    phaseStartedAt = PhaseTimestamp()

    cnyValue = PositiveNumericOrBlank(ConfigSheet().Range("B10").Value2)
    usdValue = PositiveNumericOrBlank(ConfigSheet().Range("B11").Value2)
    rateDate = ConfigSheet().Range("B12").Value2

    For Each rowKey In reconciledRows.Keys
        outputRow = outputRow + 1
        Set reconciledRow = reconciledRows(CStr(rowKey))
        syncKey = ReconciledRowText( _
            reconciledRow, "sync_key", SNAPSHOT_FIELD_SYNC_KEY)
        If syncKey <> CStr(rowKey) Then
            Err.Raise vbObjectError + 127, "ImportReconciledCatalog", _
                      T("invalid_workbook")
        End If
        reconciliationStatus = LCase$( _
            ReconciledRowText(reconciledRow, "reconciliation_status", _
                              SNAPSHOT_FIELD_RECONCILIATION_STATUS))
        Select Case reconciliationStatus
            Case "matched"
                rowKind = T("row_kind_matched")
                matchedRows = matchedRows + 1
            Case "patris_only"
                rowKind = T("row_kind_source_only")
                sourceOnlyRows = sourceOnlyRows + 1
            Case "woo_only"
                rowKind = T("row_kind_woo_only")
                wooOnlyRows = wooOnlyRows + 1
            Case "ambiguous"
                rowKind = T("row_kind_ambiguous")
                ambiguousRows = ambiguousRows + 1
                ConfigSheet().Range("G31").Value2 = True
            Case Else
                Err.Raise vbObjectError + 128, "ImportReconciledCatalog", _
                          T("invalid_workbook")
        End Select

        patrisCodeValue = ReconciledRowText( _
            reconciledRow, "patris_code", SNAPSHOT_FIELD_PATRIS_CODE)
        codeValue = patrisCodeValue
        wooIDValue = ReconciledRowText( _
            reconciledRow, "woocommerce_id", _
            SNAPSHOT_FIELD_WOOCOMMERCE_ID)
        Select Case reconciliationStatus
            Case "matched"
                If Len(wooIDValue) = 0 Or _
                   syncKey <> "patris:" & patrisCodeValue Or _
                   Len(patrisCodeValue) = 0 Then
                    Err.Raise vbObjectError + 139, _
                              "ImportReconciledCatalog", _
                              T("invalid_workbook") & " [sync_key=" & _
                              syncKey & "; patris_code=" & _
                              patrisCodeValue & "; woo_id=" & _
                              wooIDValue & "]"
                End If
            Case "patris_only"
                If Len(patrisCodeValue) = 0 Or _
                   syncKey <> "patris:" & patrisCodeValue Then
                    Err.Raise vbObjectError + 140, _
                              "ImportReconciledCatalog", _
                              T("invalid_workbook")
                End If
            Case "woo_only"
                If Len(wooIDValue) = 0 Or _
                   syncKey <> "woo:" & wooIDValue Then
                    Err.Raise vbObjectError + 141, _
                              "ImportReconciledCatalog", _
                              T("invalid_workbook")
                End If
                If Len(codeValue) = 0 Then
                    codeValue = ReconciledRowText( _
                        reconciledRow, "sku", SNAPSHOT_FIELD_SKU)
                End If
        End Select
        weightValue = ReconciledRowNumeric( _
            reconciledRow, "weight_grams", SNAPSHOT_FIELD_WEIGHT_GRAMS)
        foreignPrice = PositiveNumericOrBlank( _
            ReconciledRowScalar(reconciledRow, "foreign_price", _
                                SNAPSHOT_FIELD_FOREIGN_PRICE))
        locationValue = ReconciledRowText( _
            reconciledRow, "patris_location", _
            SNAPSHOT_FIELD_PATRIS_LOCATION)
        categoryValue = ReconciledRowText( _
            reconciledRow, "categories", SNAPSHOT_FIELD_CATEGORIES)
        goodsCurrency = ReconciledRowText( _
            reconciledRow, "foreign_currency", _
            SNAPSHOT_FIELD_FOREIGN_CURRENCY)
        shippingValue = ReconciledRowNumeric( _
            reconciledRow, "shipping_price_per_kg", _
            SNAPSHOT_FIELD_SHIPPING_PRICE_PER_KG)
        shippingCurrency = ReconciledRowText( _
            reconciledRow, "shipping_price_per_kg_currency", _
            SNAPSHOT_FIELD_SHIPPING_CURRENCY)
        profitValue = ReconciledRowNumeric( _
            reconciledRow, "profit_margin_percent", _
            SNAPSHOT_FIELD_PROFIT_MARGIN_PERCENT)
        priceSourceAmount = ReconciledRowNumeric( _
            reconciledRow, "price_source_amount", _
            SNAPSHOT_FIELD_PRICE_SOURCE_AMOUNT)
        priceSourceCurrency = ReconciledRowText( _
            reconciledRow, "price_source_currency", _
            SNAPSHOT_FIELD_PRICE_SOURCE_CURRENCY)
        priceSourceKind = ReconciledRowText( _
            reconciledRow, "price_source_kind", _
            SNAPSHOT_FIELD_PRICE_SOURCE_KIND)

        Select Case reconciliationStatus
            Case "ambiguous"
                priceWarning = T("ambiguous_woo_match")
            Case "woo_only"
                If Not IsEmpty(ReconciledRowNumeric( _
                    reconciledRow, "effective_price", _
                    SNAPSHOT_FIELD_EFFECTIVE_PRICE)) Then
                    priceWarning = T("woo_only_preserved_price")
                Else
                    priceWarning = T("woo_only_price_unavailable")
                End If
            Case Else
                priceWarning = vbNullString
                If IsEmpty(foreignPrice) Or IsEmpty(weightValue) Or _
                   IsEmpty(shippingValue) Or IsEmpty(profitValue) Or _
                   Len(goodsCurrency) = 0 Or Len(shippingCurrency) = 0 Then
                    If reconciliationStatus = "matched" And _
                        Not IsEmpty(ReconciledRowNumeric( _
                            reconciledRow, "effective_price", _
                            SNAPSHOT_FIELD_EFFECTIVE_PRICE)) Then
                        If IsEmpty(foreignPrice) Then
                            priceWarning = T( _
                                "preserved_price_missing_purchase")
                        ElseIf IsEmpty(weightValue) Then
                            priceWarning = T( _
                                "preserved_price_missing_weight")
                        Else
                            priceWarning = T( _
                                "preserved_price_incomplete")
                        End If
                    Else
                        priceWarning = T( _
                            "price_unavailable_incomplete")
                    End If
                End If
        End Select
        CountNumericValue shippingCounts, shippingValue
        CountNumericValue profitCounts, profitValue

        mainOutput(outputRow, 1) = Empty
        mainOutput(outputRow, 2) = weightValue
        mainOutput(outputRow, 3) = BuildOtherText( _
            weightValue, locationValue)
        mainOutput(outputRow, 4) = locationValue
        mainOutput(outputRow, 5) = foreignPrice
        mainOutput(outputRow, 6) = FirstNumeric( _
            ReconciledRowNumeric(reconciledRow, "patris_total_stock", _
                                 SNAPSHOT_FIELD_PATRIS_TOTAL_STOCK), _
            ReconciledRowNumeric(reconciledRow, "stock_quantity", _
                                 SNAPSHOT_FIELD_STOCK_QUANTITY))
        mainOutput(outputRow, 7) = codeValue
        mainOutput(outputRow, 8) = NormalizeHumanProductName( _
            ReconciledRowText(reconciledRow, "name", _
                              SNAPSHOT_FIELD_NAME))
        mainOutput(outputRow, 9) = wooIDValue
        mainOutput(outputRow, 10) = categoryValue

        syncOutput(outputRow, 1) = syncKey
        syncOutput(outputRow, 2) = goodsCurrency
        syncOutput(outputRow, 3) = shippingValue
        syncOutput(outputRow, 4) = shippingCurrency
        syncOutput(outputRow, 5) = profitValue
        syncOutput(outputRow, 6) = cnyValue
        syncOutput(outputRow, 7) = usdValue
        syncOutput(outputRow, 8) = rateDate
        syncOutput(outputRow, 9) = wooIDValue
        syncOutput(outputRow, 10) = ReconciledRowNumeric( _
            reconciledRow, "effective_price", _
            SNAPSHOT_FIELD_EFFECTIVE_PRICE)
        syncOutput(outputRow, 11) = ReconciledRowText( _
            reconciledRow, "updated_at", SNAPSHOT_FIELD_UPDATED_AT)
        syncOutput(outputRow, 12) = ReconciledRowText( _
            reconciledRow, "record_revision", _
            SNAPSHOT_FIELD_RECORD_REVISION)
        syncOutput(outputRow, 13) = ReconciledRowText( _
            reconciledRow, "permalink", SNAPSHOT_FIELD_PERMALINK)
        syncOutput(outputRow, 14) = profitValue
        syncOutput(outputRow, 15) = ReconciledRowNumeric( _
            reconciledRow, "patris_final_price", _
            SNAPSHOT_FIELD_PATRIS_FINAL_PRICE)
        syncOutput(outputRow, 16) = ReconciledRowNumeric( _
            reconciledRow, "sale_price", SNAPSHOT_FIELD_SALE_PRICE)
        syncOutput(outputRow, 17) = categoryValue
        syncOutput(outputRow, 18) = ReconciledRowText( _
            reconciledRow, "publication_status", _
            SNAPSHOT_FIELD_PUBLICATION_STATUS)
        syncOutput(outputRow, 19) = priceWarning
        syncOutput(outputRow, 20) = rowKind
        syncOutput(outputRow, 21) = priceSourceAmount
        syncOutput(outputRow, 22) = priceSourceCurrency
        syncOutput(outputRow, 23) = priceSourceKind
        syncOutput(outputRow, 24) = ReconciledRowText( _
            reconciledRow, "image_url", SNAPSHOT_FIELD_IMAGE_URL)
        If outputRow Mod UI_PUMP_ROW_INTERVAL = 0 Then PumpExcelMessages
    Next rowKey
    mReconcileSeconds = PhaseElapsed(phaseStartedAt)

    If matchedRows <> CLng(Val(CStr(ConfigSheet().Range("G34").Value2))) Or _
       sourceOnlyRows <> CLng(Val(CStr(ConfigSheet().Range("G35").Value2))) Or _
       wooOnlyRows <> CLng(Val(CStr(ConfigSheet().Range("G36").Value2))) Or _
       dataRows <> CLng(Val(CStr(ConfigSheet().Range("G38").Value2))) Then
        Err.Raise vbObjectError + 129, "ImportReconciledCatalog", _
                  T("invalid_workbook")
    End If
    ConfigSheet().Range("G37").Value2 = ambiguousRows

    commonShipping = MostCommonNumeric(shippingCounts)
    commonProfit = MostCommonNumeric(profitCounts)
    If Len(CanonicalCellText(ConfigSheet().Range("B14").Value2)) = 0 And _
       Not IsEmpty(commonShipping) Then
        ConfigSheet().Range("B14").Value = commonShipping
    End If
    If Len(CanonicalCellText(ConfigSheet().Range("B22").Value2)) = 0 And _
       Not IsEmpty(commonShipping) Then
        ConfigSheet().Range("B22").Value = commonShipping
    End If
    If Len(CanonicalCellText(ConfigSheet().Range("B13").Value2)) = 0 And _
       Not IsEmpty(commonProfit) Then
        ConfigSheet().Range("B13").Value = CDbl(commonProfit) / 100#
    End If
    If Len(CanonicalCellText(ConfigSheet().Range("B21").Value2)) = 0 And _
       Not IsEmpty(commonProfit) Then
        ConfigSheet().Range("B21").Value = CDbl(commonProfit) / 100#
    End If

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    phaseStartedAt = PhaseTimestamp()
    ReplaceTableData table, mainOutput, dataRows, PRODUCT_COLUMN_COUNT
    ReplaceTableData syncTable, syncOutput, dataRows, SYNC_COLUMN_COUNT
    mTableWriteSeconds = PhaseElapsed(phaseStartedAt)
    phaseStartedAt = PhaseTimestamp()
    ApplyProductTableFormulas table
    mPricingSeconds = PhaseElapsed(phaseStartedAt)
    phaseStartedAt = PhaseTimestamp()
    ApplyProductTableFormatting table
    ApplyWooLinks table, syncTable
    mFormattingSeconds = PhaseElapsed(phaseStartedAt)
    ImportReconciledCatalog = dataRows
End Function

Private Sub CaptureCatalogTableSnapshot( _
        ByRef productSnapshot As Variant, ByRef productRows As Long, _
        ByRef syncSnapshot As Variant, ByRef syncRows As Long)
    Dim productTable As ListObject
    Dim syncTable As ListObject

    Set productTable = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    If Not productTable.DataBodyRange Is Nothing Then
        productRows = productTable.DataBodyRange.Rows.Count
        productSnapshot = productTable.DataBodyRange.FormulaR1C1
    End If
    If Not syncTable.DataBodyRange Is Nothing Then
        syncRows = syncTable.DataBodyRange.Rows.Count
        syncSnapshot = syncTable.DataBodyRange.FormulaR1C1
    End If
End Sub

Private Sub RestoreCatalogTableSnapshot( _
        ByRef productSnapshot As Variant, ByVal productRows As Long, _
        ByRef syncSnapshot As Variant, ByVal syncRows As Long)
    Dim productTable As ListObject
    Dim syncTable As ListObject

    Set productTable = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    RestoreTableFormulaSnapshot productTable, productSnapshot, productRows, _
        PRODUCT_COLUMN_COUNT
    RestoreTableFormulaSnapshot syncTable, syncSnapshot, syncRows, _
        SYNC_COLUMN_COUNT
    ApplyProductTableFormatting productTable
    ApplyWooLinks productTable, syncTable
End Sub

Private Sub RestoreTableFormulaSnapshot(ByVal table As ListObject, _
                                        ByRef snapshot As Variant, _
                                        ByVal dataRows As Long, _
                                        ByVal dataColumns As Long)
    Dim parentSheet As Worksheet
    Dim firstRow As Long
    Dim firstColumn As Long

    Set parentSheet = table.Parent
    firstRow = table.Range.Row
    firstColumn = table.Range.Column
    If Not table.DataBodyRange Is Nothing Then table.DataBodyRange.Delete
    If dataRows < 1 Then
        table.Resize parentSheet.Cells(firstRow, firstColumn).Resize( _
            1, dataColumns)
        Exit Sub
    End If
    table.Resize parentSheet.Cells(firstRow, firstColumn).Resize( _
        dataRows + 1, dataColumns)
    If table.Name = SYNC_TABLE Then _
        table.DataBodyRange.Columns(1).NumberFormat = "@"
    table.DataBodyRange.FormulaR1C1 = snapshot
End Sub

Private Sub ReplaceTableData(ByVal table As ListObject, _
                             ByRef output() As Variant, _
                             ByVal dataRows As Long, _
                             ByVal dataColumns As Long)
    Dim parentSheet As Worksheet
    Dim firstRow As Long
    Dim firstColumn As Long

    Set parentSheet = table.Parent
    firstRow = table.Range.Row
    firstColumn = table.Range.Column
    If Not table.DataBodyRange Is Nothing Then table.DataBodyRange.Delete
    table.Resize parentSheet.Range( _
        parentSheet.Cells(firstRow, firstColumn), _
        parentSheet.Cells(firstRow + dataRows, firstColumn + dataColumns - 1))
    If table.Name = SYNC_TABLE Then
        parentSheet.Cells(firstRow + 1, firstColumn).Resize( _
            dataRows, 1).NumberFormat = "@"
    End If
    parentSheet.Cells(firstRow + 1, firstColumn).Resize( _
        dataRows, dataColumns).Value = output
End Sub

Private Sub ApplyProductTableFormulas(ByVal table As ListObject)
    Dim priceFormula As String
    Dim basePriceFormula As String
    Dim projectedFormula As String
    Dim fallbackFormula As String
    Dim readyFormula As String
    Dim lookupExpression As String
    Dim eligibleKindFormula As String
    Dim sourceAmountFormula As String
    Dim sourceCurrencyFormula As String
    Dim sourceKindFormula As String
    Dim settingsReference As String
    Dim cnyRateFormula As String

    If table.DataBodyRange Is Nothing Then Exit Sub
    settingsReference = "'" & U("062A0646063806CC06450627062A") & "'!"
    lookupExpression = _
        "IF(RC[6]<>"""",""patris:""&RC[6],""woo:""&RC[8])"
    cnyRateFormula = _
        "IF(AND(ISNUMBER(ConfirmedCNYRate),ConfirmedCNYRate>0)," & _
        "ConfirmedCNYRate,VLOOKUP(" & lookupExpression & _
        ",SyncData,6,FALSE))"
    eligibleKindFormula = _
        "OR(VLOOKUP(" & lookupExpression & ",SyncData,20,FALSE)=""" & _
        T("row_kind_matched") & """,VLOOKUP(" & lookupExpression & _
        ",SyncData,20,FALSE)=""" & T("row_kind_source_only") & """)"
    fallbackFormula = _
        "IFERROR(IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,10,FALSE)>0,VLOOKUP(" & lookupExpression & _
        ",SyncData,10,FALSE),""""),"""")"
    readyFormula = _
        "AND(" & eligibleKindFormula & ",RC[4]<>"""",RC[4]>0," & _
        "RC[1]<>"""",RC[1]>=0," & _
        "VLOOKUP(" & lookupExpression & ",SyncData,3,FALSE)<>""""," & _
        "VLOOKUP(" & lookupExpression & ",SyncData,3,FALSE)>=0," & _
        "VLOOKUP(" & lookupExpression & ",SyncData,5,FALSE)<>""""," & _
        "VLOOKUP(" & lookupExpression & ",SyncData,5,FALSE)>=0," & _
        "OR(AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""CNY""," & cnyRateFormula & _
        ">0),AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE)>0),VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRR"",VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRT"")," & _
        "OR(AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""CNY""," & cnyRateFormula & _
        ">0),AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE)>0),VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""IRR"",VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""IRT""))"
    basePriceFormula = _
        "IFERROR(IF(" & readyFormula & ",ROUND((" & _
        "(RC[4]*IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""CNY""," & cnyRateFormula & _
        ",IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE),IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRR"",0.1,IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRT"",1,NA())))))+" & _
        "IF(OR(RC[1]="""",VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)=""""),0,(RC[1]/1000)*VLOOKUP(" & _
        lookupExpression & ",SyncData,3,FALSE)*IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""CNY""," & _
        cnyRateFormula & ",IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""USD"",VLOOKUP(" & _
        lookupExpression & ",SyncData,7,FALSE),IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""IRR"",0.1,IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""IRT"",1,NA()))))))" & _
        "*(1+VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)/100),-" & settingsReference & "R15C2)," & _
        fallbackFormula & ")," & fallbackFormula & ")"
    sourceAmountFormula = _
        "VLOOKUP(" & lookupExpression & ",SyncData,21,FALSE)"
    sourceCurrencyFormula = _
        "VLOOKUP(" & lookupExpression & ",SyncData,22,FALSE)"
    sourceKindFormula = _
        "VLOOKUP(" & lookupExpression & ",SyncData,23,FALSE)"
    projectedFormula = _
        "IFERROR(IF(AND(" & eligibleKindFormula & "," & _
        sourceKindFormula & "=""foreign_price""," & _
        sourceCurrencyFormula & "=""CNY""," & sourceAmountFormula & _
        ">0,RC[1]<>"""",RC[1]>=0,VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)<>"""",VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)>=0,VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)<>"""",VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)>=0," & cnyRateFormula & _
        ">0,OR(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""CNY"",VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""IRR"")),ROUND((" & sourceAmountFormula & _
        "*" & cnyRateFormula & "+" & _
        "(RC[1]/1000)*IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""CNY"",VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)*" & cnyRateFormula & _
        ",VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)/10))*(1+VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)/100),-" & settingsReference & "R15C2),"
    projectedFormula = projectedFormula & _
        "IF(AND(" & eligibleKindFormula & "," & sourceKindFormula & _
        "=""partner_price""," & sourceCurrencyFormula & "=""IRR""," & _
        sourceAmountFormula & ">0,VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)<>"""",VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)>=0),ROUND((" & sourceAmountFormula & _
        "/10)*(1+VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)/100),-" & settingsReference & "R15C2),"
    projectedFormula = projectedFormula & _
        "IF(AND(" & eligibleKindFormula & "," & sourceKindFormula & _
        "=""sale_price_direct""," & sourceCurrencyFormula & _
        "=""IRR""," & sourceAmountFormula & ">0,MOD(" & _
        sourceAmountFormula & ",10)=0)," & sourceAmountFormula & _
        "/10,""""))),"""")"
    priceFormula = _
        "=LET(basePrice," & basePriceFormula & ",projectedPrice," & _
        projectedFormula & ",IF(AND(basePrice="""",RC[5]<>""""," & _
        "RC[5]<=0,ROW()=ProjectedPricePreviewRow)," & _
        "projectedPrice,basePrice))"
    On Error GoTo LegacyFormula
    table.ListColumns(1).DataBodyRange.Formula2R1C1 = priceFormula
    Exit Sub

LegacyFormula:
    Err.Clear
    table.ListColumns(1).DataBodyRange.FormulaR1C1 = priceFormula
End Sub

Private Sub ApplyProductTableFormatting(ByVal table As ListObject)
    Dim highlightRule As FormatCondition
    Dim previewRule As FormatCondition
    Dim calculatedRule As FormatCondition
    Dim warningRule As FormatCondition
    Dim missingRule As FormatCondition
    Dim pricingKey As String
    Dim pricingWarning As String

    If table.DataBodyRange Is Nothing Then Exit Sub
    EnsureProductColumnWidths
    table.ListColumns(1).DataBodyRange.NumberFormat = "#,##0"
    table.ListColumns(2).DataBodyRange.NumberFormat = "General"
    table.ListColumns(5).DataBodyRange.NumberFormat = "General"
    table.ListColumns(6).DataBodyRange.NumberFormat = "General"
    table.ListColumns(7).DataBodyRange.NumberFormat = "@"
    table.ListColumns(9).DataBodyRange.NumberFormat = "@"
    table.ListColumns(1).DataBodyRange.ReadingOrder = xlLTR
    table.ListColumns(2).DataBodyRange.ReadingOrder = xlLTR
    table.ListColumns(5).DataBodyRange.ReadingOrder = xlLTR
    table.ListColumns(6).DataBodyRange.ReadingOrder = xlLTR
    table.ListColumns(7).DataBodyRange.ReadingOrder = xlLTR
    table.ListColumns(9).DataBodyRange.ReadingOrder = xlLTR
    ApplyLatinFont Union(table.ListColumns(1).DataBodyRange, _
                         table.ListColumns(2).DataBodyRange, _
                         table.ListColumns(5).DataBodyRange, _
                         table.ListColumns(6).DataBodyRange, _
                         table.ListColumns(7).DataBodyRange, _
                         table.ListColumns(9).DataBodyRange)
    ApplyPersianFont Union(table.ListColumns(3).DataBodyRange, _
                           table.ListColumns(4).DataBodyRange, _
                           table.ListColumns(8).DataBodyRange, _
                           table.ListColumns(10).DataBodyRange)
    table.ListColumns(8).DataBodyRange.Font.Bold = True
    table.ListColumns(10).DataBodyRange.ReadingOrder = xlRTL
    table.ListColumns(10).DataBodyRange.HorizontalAlignment = xlRight
    ApplyPriceDisplayFontSetting
    table.DataBodyRange.Rows.RowHeight = 24
    On Error GoTo ConditionalFormattingUnavailable
    table.DataBodyRange.FormatConditions.Delete
    Set highlightRule = table.DataBodyRange.FormatConditions.Add( _
        Type:=xlExpression, _
        Formula1:="=ROW()=SelectedProductRow")
    highlightRule.Interior.Color = RGB(255, 244, 204)
    Set previewRule = table.ListColumns(1).DataBodyRange. _
        FormatConditions.Add( _
            Type:=xlExpression, _
            Formula1:="=ROW()=ProjectedPricePreviewRow")
    previewRule.Font.Color = RGB(128, 128, 128)
    previewRule.Font.Italic = True

    ' The selling-price cell is the operator-facing state indicator. Keep the
    ' three states distinct after every table rebuild: green means a positive
    ' calculation with no reconciliation warning, amber means a usable price
    ' with a warning, and red means the row has no usable calculated price.
    pricingKey = "IF($H6<>"""",""patris:""&$H6,""woo:""&$J6)"
    pricingWarning = "IFERROR(VLOOKUP(" & pricingKey & _
        ",SyncData,19,FALSE),"""")"

    Set calculatedRule = table.ListColumns(1).DataBodyRange. _
        FormatConditions.Add( _
            Type:=xlExpression, _
            Formula1:="=AND($I6<>"""",$B6>0," & _
                pricingWarning & "="""")")
    calculatedRule.Interior.Color = RGB(226, 239, 218)
    calculatedRule.Font.Color = RGB(0, 97, 0)

    Set warningRule = table.ListColumns(1).DataBodyRange. _
        FormatConditions.Add( _
            Type:=xlExpression, _
            Formula1:="=AND($I6<>"""",$B6>0," & _
                pricingWarning & "<>"""")")
    warningRule.Interior.Color = RGB(255, 242, 204)
    warningRule.Font.Color = RGB(156, 101, 0)

    Set missingRule = table.ListColumns(1).DataBodyRange. _
        FormatConditions.Add( _
            Type:=xlExpression, _
            Formula1:="=AND($I6<>"""",OR($B6="""",$B6<=0))")
    missingRule.Interior.Color = RGB(244, 204, 204)
    missingRule.Font.Color = RGB(156, 0, 6)
    Exit Sub

ConditionalFormattingUnavailable:
    ' Conditional-format corruption in an older workbook must not abort sync.
    Err.Clear
    On Error GoTo 0
End Sub

Private Sub EnsureProductColumnWidths()
    With PriceSheet()
        If .Columns("B").ColumnWidth < 20# Then _
            .Columns("B").ColumnWidth = 20#
        If .Columns("K").ColumnWidth < 36# Then _
            .Columns("K").ColumnWidth = 36#
    End With
End Sub

Public Sub ApplyPriceDisplayFontSetting()
    Dim table As ListObject
    Dim latinFont As String

    On Error GoTo CleanExit
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Sub
    latinFont = NamedText("LatinFont", DEFAULT_LATIN_FONT)
    ApplyRangeFontSlots table.ListColumns(1).DataBodyRange, _
        PriceDisplayFontName(latinFont)
CleanExit:
End Sub

Private Function PriceDisplayFontName(ByVal latinFont As String) As String
    Dim valid As Boolean
    Dim useFaNum As Boolean

    useFaNum = NamedYesNo("PriceDisplayFaNum", valid)
    If Not valid Then
        Err.Raise vbObjectError + 233, "PriceDisplayFontName", _
                  T("font_policy_invalid")
    End If
    If useFaNum Then
        PriceDisplayFontName = DEFAULT_FANUM_FONT
    Else
        PriceDisplayFontName = latinFont
    End If
End Function

Private Sub ApplyPersianFont(ByVal target As Range)
    Dim fontName As String

    fontName = NamedText("PersianFont", DEFAULT_PERSIAN_FONT)
    ApplyRangeFontSlots target, fontName
End Sub

Private Sub ApplyLatinFont(ByVal target As Range)
    Dim fontName As String

    fontName = NamedText("LatinFont", DEFAULT_LATIN_FONT)
    ApplyRangeFontSlots target, fontName
End Sub

Private Sub ApplyRangeFontSlots(ByVal target As Range, _
                                ByVal fontName As String)
    Dim fontValue As Object

    Set fontValue = target.Font
    CallByName fontValue, "Name", VbLet, fontName
    On Error Resume Next
    CallByName fontValue, "NameComplexScript", VbLet, fontName
    CallByName fontValue, "NameFarEast", VbLet, fontName
    On Error GoTo 0
End Sub

Private Sub ApplyWooLinks(ByVal table As ListObject, _
                          ByVal syncTable As ListObject)
    Dim rowIndex As Long

    If table.DataBodyRange Is Nothing Or syncTable.DataBodyRange Is Nothing Then Exit Sub
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        ApplyWooLinkRow table, syncTable, rowIndex
        If rowIndex Mod UI_PUMP_ROW_INTERVAL = 0 Then PumpExcelMessages
    Next rowIndex
End Sub

Private Function HasPersianCharacter(ByVal value As String) As Boolean
    Dim characterIndex As Long
    Dim codePoint As Long

    For characterIndex = 1 To Len(value)
        codePoint = AscW(Mid$(value, characterIndex, 1))
        If codePoint < 0 Then codePoint = codePoint + 65536
        Select Case codePoint
            Case &H600 To &H6FF, &H750 To &H77F, _
                 &H8A0 To &H8FF, &HFB50 To &HFDFF, _
                 &HFE70 To &HFEFF
                HasPersianCharacter = True
                Exit Function
        End Select
    Next characterIndex
End Function

Private Sub ApplyProductNameDirection(ByVal productCell As Range)
    If HasPersianCharacter(CStr(productCell.Value2)) Then
        productCell.ReadingOrder = xlRTL
        productCell.HorizontalAlignment = xlRight
    Else
        productCell.ReadingOrder = xlLTR
        productCell.HorizontalAlignment = xlLeft
    End If
End Sub

Private Sub ApplyProductNameFont(ByVal productCell As Range)
    ApplyPersianFont productCell
    productCell.Font.Bold = True
End Sub

Private Sub ApplyWooLinkRow(ByVal table As ListObject, _
                            ByVal syncTable As ListObject, _
                            ByVal rowIndex As Long)
    Dim wooID As String
    Dim permalink As String
    Dim linkText As String
    Dim publicationStatus As String
    Dim linkCell As Range
    Dim currentAddress As String

    On Error GoTo RowFailed
    wooID = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 9).Value2))
    permalink = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 13).Value2))
    publicationStatus = LCase$(Trim$(CStr( _
        syncTable.DataBodyRange.Cells(rowIndex, 18).Value2)))
    Set linkCell = table.DataBodyRange.Cells(rowIndex, 8)
    linkText = CStr(linkCell.Value2)
    If linkCell.Hyperlinks.Count > 0 Then
        currentAddress = CStr(linkCell.Hyperlinks(1).Address)
    End If

    linkCell.Value2 = linkText
    ApplyProductNameDirection linkCell
    Select Case publicationStatus
        Case "publish"
            linkCell.Font.Color = RGB(1, 104, 205)
        Case "draft", "pending", "private", "future"
            linkCell.Font.Color = RGB(180, 111, 0)
        Case Else
            linkCell.Font.Color = RGB(164, 40, 40)
    End Select

    If Len(wooID) = 0 Then
        If linkCell.Hyperlinks.Count > 0 Then linkCell.Hyperlinks.Delete
        linkCell.Font.Color = RGB(164, 40, 40)
        Exit Sub
    End If
    If Len(linkText) = 0 Then
        If linkCell.Hyperlinks.Count > 0 Then linkCell.Hyperlinks.Delete
        Exit Sub
    End If

    If IsAllowedDigitalogicUrl(permalink) Then
        If StrComp(currentAddress, permalink, vbTextCompare) <> 0 Then
            If linkCell.Hyperlinks.Count > 0 Then linkCell.Hyperlinks.Delete
            table.Parent.Hyperlinks.Add _
                Anchor:=linkCell, Address:=permalink, _
                TextToDisplay:=linkText
        End If
        ApplyProductNameFont linkCell
        ApplyProductNameDirection linkCell
        Select Case publicationStatus
            Case "publish"
                linkCell.Font.Color = RGB(1, 104, 205)
            Case "draft", "pending", "private", "future"
                linkCell.Font.Color = RGB(180, 111, 0)
            Case Else
                linkCell.Font.Color = RGB(164, 40, 40)
        End Select
    ElseIf linkCell.Hyperlinks.Count > 0 Then
        linkCell.Hyperlinks.Delete
    End If
    Exit Sub

RowFailed:
    On Error Resume Next
    If Not linkCell Is Nothing And Len(linkText) > 0 Then
        linkCell.Value2 = linkText
        ApplyProductNameFont linkCell
        linkCell.Font.Color = RGB(164, 40, 40)
        ApplyProductNameDirection linkCell
    End If
    Err.Clear
    On Error GoTo 0
End Sub

Private Function PriceParitySummary() As Variant
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim rowIndex As Long
    Dim calculated As Variant
    Dim wooPrice As Variant
    Dim difference As Double
    Dim threshold As Double
    Dim matched As Long
    Dim overLimit As Long

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    threshold = CDbl(ConfigSheet().Range("B24").Value2)
    If table.DataBodyRange Is Nothing Then
        PriceParitySummary = Array(0, 0)
        Exit Function
    End If

    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        calculated = NumericOrBlank( _
            table.DataBodyRange.Cells(rowIndex, 1).Value2)
        wooPrice = NumericOrBlank( _
            syncTable.DataBodyRange.Cells(rowIndex, 10).Value2)
        If Not IsEmpty(calculated) And Not IsEmpty(wooPrice) And _
           CDbl(wooPrice) > 0 Then
            difference = Abs(CDbl(calculated) - CDbl(wooPrice)) / CDbl(wooPrice)
            If difference <= 0.000001 Then
                matched = matched + 1
            ElseIf difference > threshold Then
                overLimit = overLimit + 1
            End If
        End If
        If rowIndex Mod (UI_PUMP_ROW_INTERVAL * 2) = 0 Then _
            PumpExcelMessages
    Next rowIndex
    PriceParitySummary = Array(matched, overLimit)
End Function

Private Function StrictPriceParityMismatchCount() As Long
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim rowIndex As Long
    Dim calculated As Variant
    Dim wooPrice As Variant
    Dim rowKind As String
    Dim mismatchCount As Long

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    If table.DataBodyRange Is Nothing Or _
       syncTable.DataBodyRange Is Nothing Then Exit Function

    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        rowKind = CStr(syncTable.DataBodyRange.Cells(rowIndex, 20).Value2)
        If rowKind = T("row_kind_matched") Or _
           rowKind = T("row_kind_woo_only") Then
            calculated = NumericOrBlank( _
                table.DataBodyRange.Cells(rowIndex, 1).Value2)
            wooPrice = NumericOrBlank( _
                syncTable.DataBodyRange.Cells(rowIndex, 10).Value2)
            If IsEmpty(calculated) Xor IsEmpty(wooPrice) Then
                mismatchCount = mismatchCount + 1
            ElseIf Not IsEmpty(calculated) And Not IsEmpty(wooPrice) Then
                If Abs(CDbl(calculated) - CDbl(wooPrice)) > 0.01 Then
                    mismatchCount = mismatchCount + 1
                End If
            End If
        End If
        If rowIndex Mod (UI_PUMP_ROW_INTERVAL * 2) = 0 Then _
            PumpExcelMessages
    Next rowIndex
    StrictPriceParityMismatchCount = mismatchCount
End Function

Private Function BuildPricingRequest(ByVal operationName As String, _
                                     ByVal requestID As String, _
                                     ByVal previewDigest As String, _
                                     ByVal includeConfirmation As Boolean, _
                                     Optional ByVal expectedRevision As String = "") As String
    Dim settings As Worksheet
    Dim body As String
    Dim profitPercent As Variant
    Dim shippingCurrency As String
    Dim shippingRevision As String
    Dim usdEffectiveDate As String
    Dim cnyEffectiveDate As String

    Set settings = ConfigSheet()
    If BooleanValue(settings.Range("G31").Value2) Then
        Err.Raise vbObjectError + 163, "BuildPricingRequest", _
                  T("identity_warning")
    End If
    If Len(expectedRevision) = 0 Then
        expectedRevision = Trim$(CStr(settings.Range("G14").Value2))
    End If
    profitPercent = Empty
    If IsNumeric(settings.Range("B21").Value2) Then
        profitPercent = CDbl(settings.Range("B21").Value2) * 100#
    End If
    shippingCurrency = UCase$(Trim$(CStr(settings.Range("H14").Value2)))
    shippingRevision = Trim$(CStr(settings.Range("H15").Value2))
    usdEffectiveDate = CanonicalDateText(settings.Range("E20").Value2)
    cnyEffectiveDate = CanonicalDateText(settings.Range("B20").Value2)
    ValidatePricingSettings settings, profitPercent, shippingCurrency, _
        shippingRevision, usdEffectiveDate, cnyEffectiveDate

    body = "{""schema"":" & JsonString(PRICING_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """operation"":" & JsonString(operationName) & "," & _
        """client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID) & "," & _
        """channel"":" & JsonString(PRICING_CONTRACT_CHANNEL) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """idempotency_key"":" & JsonString(requestID) & "," & _
        """expected_state_revision"":" & _
        JsonString(expectedRevision) & "," & _
        """settings"":{" & _
        """dollar_price"":" & JsonNumberOrNull(settings.Range("B19").Value2) & "," & _
        """yuan_price"":" & JsonNumberOrNull(settings.Range("B18").Value2) & "," & _
        """effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """usd_effective_date"":" & JsonString(usdEffectiveDate) & "," & _
        """cny_effective_date"":" & JsonString(cnyEffectiveDate) & "," & _
        """profit_margin_percent"":" & JsonNumberOrNull(profitPercent) & "," & _
        """air_express_price_per_kg"":" & JsonNumberOrNull(settings.Range("B22").Value2) & "," & _
        """air_express_currency"":" & JsonString(shippingCurrency) & "," & _
        """shipping_catalog_revision"":" & JsonString(shippingRevision) & "," & _
        """price_rounding_digits"":" & JsonNumberOrNull(settings.Range("B26").Value2) & "," & _
        """price_rounding_mode"":" & JsonString(PRICE_ROUNDING_MODE) & "}," & _
        """product_changes"":[]"
    If Len(previewDigest) > 0 Then
        body = body & ",""preview_digest"":" & JsonString(previewDigest)
    End If
    If includeConfirmation Then
        body = body & ",""confirmation"":""APPLY"""
    End If
    BuildPricingRequest = body & "}"
End Function

Private Sub ValidatePricingSettings(ByVal settings As Worksheet, _
                                    ByVal profitPercent As Variant, _
                                    ByVal shippingCurrency As String, _
                                    ByVal shippingRevision As String, _
                                    ByVal usdEffectiveDate As String, _
                                    ByVal cnyEffectiveDate As String)
    Dim dateText As String
    Dim usdDateText As String

    dateText = CanonicalDateText(settings.Range("B20").Value2)
    usdDateText = CanonicalDateText(settings.Range("E20").Value2)
    If Not IsNumeric(settings.Range("B18").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B18").Value2) <= 0 Then GoTo InvalidSettings
    If Not IsNumeric(settings.Range("B19").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B19").Value2) <= 0 Then GoTo InvalidSettings
    If Len(dateText) <> 10 Then GoTo InvalidSettings
    If Mid$(dateText, 5, 1) <> "-" Then GoTo InvalidSettings
    If Mid$(dateText, 8, 1) <> "-" Then GoTo InvalidSettings
    If cnyEffectiveDate <> dateText Then GoTo InvalidSettings
    If Len(usdDateText) <> 10 Then GoTo InvalidSettings
    If Mid$(usdDateText, 5, 1) <> "-" Then GoTo InvalidSettings
    If Mid$(usdDateText, 8, 1) <> "-" Then GoTo InvalidSettings
    If usdEffectiveDate <> usdDateText Then GoTo InvalidSettings
    If IsEmpty(profitPercent) Or IsNull(profitPercent) Then GoTo InvalidSettings
    If Not IsNumeric(profitPercent) Then GoTo InvalidSettings
    If CDbl(profitPercent) < 0 Or CDbl(profitPercent) > 1000 Then GoTo InvalidSettings
    If Not IsNumeric(settings.Range("B22").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B22").Value2) <= 0 Then GoTo InvalidSettings
    If Not IsNumeric(settings.Range("B26").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B26").Value2) < 0 Or _
       CDbl(settings.Range("B26").Value2) > 9 Then GoTo InvalidSettings
    If CDbl(settings.Range("B26").Value2) <> _
       Fix(CDbl(settings.Range("B26").Value2)) Then GoTo InvalidSettings
    If shippingCurrency <> "CNY" And shippingCurrency <> "IRR" Then GoTo InvalidSettings
    If Len(shippingRevision) <> 71 Then GoTo InvalidSettings
    If Left$(shippingRevision, 7) <> "sha256:" Then GoTo InvalidSettings
    Exit Sub

InvalidSettings:
    Err.Raise vbObjectError + 164, "ValidatePricingSettings", _
              T("settings_required")
End Sub

Private Function PricingEndpoint(ByVal operationName As String) As String
    PricingEndpoint = PricingBaseURL() & "/" & operationName
End Function

Private Function PricingBaseURL() As String
    Dim productUrl As String
    Dim lowerUrl As String
    Dim suffix As String

    productUrl = Trim$(CStr(ConfigSheet().Range("B3").Value2))
    lowerUrl = LCase$(productUrl)
    suffix = "/api/product-sync"
    If Not IsAllowedPatrisUrl(productUrl) Or _
       Right$(lowerUrl, Len(suffix)) <> suffix Then
        Err.Raise vbObjectError + 148, "PricingBaseURL", T("bridge_missing")
    End If
    PricingBaseURL = Left$(productUrl, Len(productUrl) - Len(suffix)) & _
        "/api/pricing-sync"
End Function

Private Function UniversalRefreshURL() As String
    Dim productUrl As String
    Dim lowerUrl As String
    Dim suffix As String

    productUrl = Trim$(CStr(ConfigSheet().Range("B3").Value2))
    lowerUrl = LCase$(productUrl)
    suffix = "/api/product-sync"
    If Not IsAllowedPatrisUrl(productUrl) Or _
       Right$(lowerUrl, Len(suffix)) <> suffix Then
        Err.Raise vbObjectError + 148, "UniversalRefreshURL", _
                  T("bridge_missing")
    End If
    UniversalRefreshURL = _
        Left$(productUrl, Len(productUrl) - Len(suffix)) & "/api/refresh"
End Function

Private Function Utf8Bytes(ByVal value As String) As Variant
    Dim stream As Object

    Set stream = CreateObject("ADODB.Stream")
    stream.Type = 2
    stream.Charset = "utf-8"
    stream.Open
    stream.WriteText value
    stream.Position = 0
    stream.Type = 1
    stream.Position = 3
    Utf8Bytes = stream.Read
    stream.Close
End Function

Private Function Utf8ByteArray(ByVal value As String) As Byte()
    Dim stream As Object
    Dim bytes() As Byte

    Set stream = CreateObject("ADODB.Stream")
    stream.Type = 2
    stream.Charset = "utf-8"
    stream.Open
    stream.WriteText value
    stream.Position = 0
    stream.Type = 1
    stream.Position = 3
    bytes = stream.Read
    stream.Close
    Utf8ByteArray = bytes
End Function

Private Function RawJSONObjectMember(ByVal jsonText As String, _
                                     ByVal memberName As String) As String
    Dim marker As String
    Dim valueStart As Long
    Dim characterIndex As Long
    Dim depth As Long
    Dim character As String
    Dim inString As Boolean
    Dim escaped As Boolean

    marker = Chr$(34) & memberName & Chr$(34) & ":"
    valueStart = InStr(1, jsonText, marker, vbBinaryCompare)
    If valueStart = 0 Then Exit Function
    valueStart = valueStart + Len(marker)
    Do While valueStart <= Len(jsonText) And _
             InStr(1, " " & vbTab & vbCr & vbLf, _
                   Mid$(jsonText, valueStart, 1), vbBinaryCompare) > 0
        valueStart = valueStart + 1
    Loop
    If valueStart > Len(jsonText) Or _
       Mid$(jsonText, valueStart, 1) <> "{" Then Exit Function

    For characterIndex = valueStart To Len(jsonText)
        character = Mid$(jsonText, characterIndex, 1)
        If inString Then
            If escaped Then
                escaped = False
            ElseIf character = "\" Then
                escaped = True
            ElseIf character = Chr$(34) Then
                inString = False
            End If
        ElseIf character = Chr$(34) Then
            inString = True
        ElseIf character = "{" Or character = "[" Then
            depth = depth + 1
        ElseIf character = "}" Or character = "]" Then
            depth = depth - 1
            If depth = 0 Then
                RawJSONObjectMember = Mid$( _
                    jsonText, valueStart, characterIndex - valueStart + 1)
                Exit Function
            End If
            If depth < 0 Then Exit Function
        End If
    Next characterIndex
End Function

Private Function StrongETagRevision(ByVal etagValue As String) As String
    etagValue = Trim$(etagValue)
    If Len(etagValue) <> 73 Or Left$(etagValue, 1) <> Chr$(34) Or _
       Right$(etagValue, 1) <> Chr$(34) Then Exit Function
    StrongETagRevision = Mid$(etagValue, 2, 71)
    If Not IsSHA256RevisionText(StrongETagRevision) Then _
        StrongETagRevision = vbNullString
End Function

Private Function SHA256RevisionBytes(ByVal value As Variant) As String
    Dim algorithmName As String
    Dim propertyName As String
    Dim bytes() As Byte
    Dim hashObject() As Byte
    Dim hashValue(0 To 31) As Byte
    Dim objectLength As Long
    Dim resultSize As Long
    Dim byteCount As Long
    Dim byteIndex As Long
    Dim status As Long
    Dim digestText As String
    Dim stream As Object
#If VBA7 Then
    Dim algorithmHandle As LongPtr
    Dim hashHandle As LongPtr
#Else
    Dim algorithmHandle As Long
    Dim hashHandle As Long
#End If

    On Error GoTo HashFailed
    If Not IsByteArrayVariant(value) Then GoTo HashFailed
    byteCount = ByteArrayVariantLength(value)
    If byteCount < 1 Or byteCount > MAX_PRICING_RESPONSE_BYTES Then _
        GoTo HashFailed

    ' Copy the SAFEARRAY as binary data only. No text decoding or re-encoding
    ' occurs before the representation digest is calculated.
    Set stream = CreateObject("ADODB.Stream")
    stream.Type = 1
    stream.Open
    stream.Write value
    stream.Position = 0
    bytes = stream.Read
    stream.Close
    Set stream = Nothing
    If UBound(bytes) - LBound(bytes) + 1 <> byteCount Then GoTo HashFailed

    algorithmName = "SHA256"
    propertyName = "ObjectLength"
    status = BCryptOpenAlgorithmProvider( _
        algorithmHandle, StrPtr(algorithmName), 0, 0)
    If status <> 0 Then GoTo HashFailed
    status = BCryptGetProperty( _
        algorithmHandle, StrPtr(propertyName), objectLength, 4, _
        resultSize, 0)
    If status <> 0 Or objectLength < 1 Then GoTo HashFailed
    ReDim hashObject(0 To objectLength - 1)
    status = BCryptCreateHash( _
        algorithmHandle, hashHandle, hashObject(0), objectLength, 0, 0, 0)
    If status <> 0 Then GoTo HashFailed
    status = BCryptHashData( _
        hashHandle, bytes(LBound(bytes)), byteCount, 0)
    If status <> 0 Then GoTo HashFailed
    status = BCryptFinishHash(hashHandle, hashValue(0), 32, 0)
    If status <> 0 Then GoTo HashFailed
    For byteIndex = 0 To 31
        digestText = digestText & _
            LCase$(Right$("0" & Hex$(hashValue(byteIndex)), 2))
    Next byteIndex
    SHA256RevisionBytes = "sha256:" & digestText

HashCleanup:
    On Error Resume Next
    If Not stream Is Nothing Then stream.Close
    Set stream = Nothing
    If hashHandle <> 0 Then BCryptDestroyHash hashHandle
    If algorithmHandle <> 0 Then _
        BCryptCloseAlgorithmProvider algorithmHandle, 0
    On Error GoTo 0
    If Len(SHA256RevisionBytes) = 0 Then
        Err.Raise vbObjectError + 238, "SHA256RevisionBytes", _
                  T("invalid_workbook")
    End If
    Exit Function

HashFailed:
    SHA256RevisionBytes = vbNullString
    Resume HashCleanup
End Function

Private Function SHA256Revision(ByVal value As String) As String
    Dim algorithmName As String
    Dim propertyName As String
    Dim bytes() As Byte
    Dim hashObject() As Byte
    Dim hashValue(0 To 31) As Byte
    Dim objectLength As Long
    Dim resultSize As Long
    Dim byteCount As Long
    Dim byteIndex As Long
    Dim status As Long
    Dim digestText As String
#If VBA7 Then
    Dim algorithmHandle As LongPtr
    Dim hashHandle As LongPtr
#Else
    Dim algorithmHandle As Long
    Dim hashHandle As Long
#End If

    On Error GoTo HashFailed
    algorithmName = "SHA256"
    propertyName = "ObjectLength"
    status = BCryptOpenAlgorithmProvider( _
        algorithmHandle, StrPtr(algorithmName), 0, 0)
    If status <> 0 Then GoTo HashFailed
    status = BCryptGetProperty( _
        algorithmHandle, StrPtr(propertyName), objectLength, 4, _
        resultSize, 0)
    If status <> 0 Or objectLength < 1 Then GoTo HashFailed
    ReDim hashObject(0 To objectLength - 1)
    status = BCryptCreateHash( _
        algorithmHandle, hashHandle, hashObject(0), objectLength, 0, 0, 0)
    If status <> 0 Then GoTo HashFailed

    If Len(value) > 0 Then
        bytes = Utf8ByteArray(value)
        byteCount = UBound(bytes) - LBound(bytes) + 1
        status = BCryptHashData( _
            hashHandle, bytes(LBound(bytes)), byteCount, 0)
        If status <> 0 Then GoTo HashFailed
    End If
    status = BCryptFinishHash(hashHandle, hashValue(0), 32, 0)
    If status <> 0 Then GoTo HashFailed
    For byteIndex = 0 To 31
        digestText = digestText & _
            LCase$(Right$("0" & Hex$(hashValue(byteIndex)), 2))
    Next byteIndex
    SHA256Revision = "sha256:" & digestText

HashCleanup:
    On Error Resume Next
    If hashHandle <> 0 Then BCryptDestroyHash hashHandle
    If algorithmHandle <> 0 Then _
        BCryptCloseAlgorithmProvider algorithmHandle, 0
    On Error GoTo 0
    If Len(SHA256Revision) = 0 Then
        Err.Raise vbObjectError + 238, "SHA256Revision", _
                  T("invalid_workbook")
    End If
    Exit Function

HashFailed:
    SHA256Revision = vbNullString
    Resume HashCleanup
End Function

Private Function ResponseData(ByVal root As JsonValue) As JsonValue
    Dim data As JsonValue

    If root Is Nothing Or root.Kind <> "object" Then
        Err.Raise vbObjectError + 145, "ResponseData", T("bridge_missing")
    End If
    Set data = JsonRuntime.JsonMember(root, "data")
    If data Is Nothing Then
        Set ResponseData = root
    Else
        Set ResponseData = data
    End If
End Function

Private Function ResponseErrorCode(ByVal responseText As String) As String
    Dim root As JsonValue
    Dim data As JsonValue

    On Error GoTo NoCode
    Set root = JsonRuntime.ParseJson(responseText)
    Set data = ResponseData(root)
    ResponseErrorCode = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(data, "code"))))
    If Len(ResponseErrorCode) = 0 Then
        ResponseErrorCode = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(root, "code"))))
    End If
NoCode:
End Function

Private Function ResponseErrorMessage(ByVal responseText As String) As String
    Dim root As JsonValue
    Dim data As JsonValue
    Dim errorCode As String

    On Error GoTo NoMessage
    Set root = JsonRuntime.ParseJson(responseText)
    Set data = ResponseData(root)
    errorCode = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(data, "code"))))
    If Len(errorCode) = 0 Then
        errorCode = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(root, "code"))))
    End If
    Select Case LCase$(errorCode)
        Case "digitalogic_reconciled_projection_integrity_failed"
            ResponseErrorMessage = T("projection_integrity_blocked")
            Exit Function
        Case "remote_not_configured", "canonical_source_unavailable", _
             "remote_unavailable"
            ResponseErrorMessage = T("pricing_service_unavailable")
            Exit Function
    End Select
    ResponseErrorMessage = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(data, "message"))))
    If Len(ResponseErrorMessage) = 0 Then
        ResponseErrorMessage = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(root, "message"))))
    End If
    If Len(errorCode) > 0 Then
        If Len(ResponseErrorMessage) > 0 Then
            ResponseErrorMessage = errorCode & ": " & ResponseErrorMessage
        Else
            ResponseErrorMessage = errorCode
        End If
    End If
NoMessage:
End Function

Private Function NewRequestID(ByVal operationName As String) As String
    Randomize
    NewRequestID = "excel-" & operationName & "-" & _
        Format$(Now, "yyyymmddhhnnss") & "-" & _
        LCase$(Hex$(CLng(Rnd() * 2147483646#)))
End Function

Private Function JsonString(ByVal value As String) As String
    value = Replace$(value, "\", "\\")
    value = Replace$(value, """", Chr$(92) & Chr$(34))
    value = Replace$(value, vbCr, "\r")
    value = Replace$(value, vbLf, "\n")
    JsonString = """" & value & """"
End Function

Private Function JsonNumberOrNull(ByVal value As Variant) As String
    If IsEmpty(value) Or IsNull(value) Then
        JsonNumberOrNull = "null"
    ElseIf Len(Trim$(CStr(value))) = 0 Or Not IsNumeric(value) Then
        JsonNumberOrNull = "null"
    Else
        JsonNumberOrNull = FormatNumericForText(CDbl(value))
    End If
End Function

Private Function IsAllowedPatrisUrl(ByVal address As String) As Boolean
    IsAllowedPatrisUrl = _
        LCase$(Left$(Trim$(address), Len(LOOPBACK_PREFIX))) = LOOPBACK_PREFIX And _
        InStr(1, address, "/api/product-sync", vbTextCompare) > 0
End Function

Private Function IsAllowedPricingBridgeUrl(ByVal address As String) As Boolean
    IsAllowedPricingBridgeUrl = _
        LCase$(Left$(Trim$(address), Len(LOOPBACK_PREFIX))) = LOOPBACK_PREFIX And _
        InStr(1, address, "/api/pricing-sync/", vbTextCompare) > 0
End Function

Private Function IsAllowedPricingAuthenticatedUrl( _
        ByVal address As String) As Boolean
    If IsAllowedPricingBridgeUrl(address) Then
        IsAllowedPricingAuthenticatedUrl = True
        Exit Function
    End If
    IsAllowedPricingAuthenticatedUrl = _
        (StrComp(Trim$(address), UniversalRefreshURL(), vbTextCompare) = 0)
End Function

Private Function IsAllowedDigitalogicUrl(ByVal address As String) As Boolean
    Dim normalized As String

    normalized = LCase$(Trim$(address))
    IsAllowedDigitalogicUrl = _
        Left$(normalized, Len("https://digitalogic.ir/")) = _
        "https://digitalogic.ir/"
End Function

Private Function RTrimSlash(ByVal value As String) As String
    Do While Right$(value, 1) = "/"
        value = Left$(value, Len(value) - 1)
    Loop
    RTrimSlash = value
End Function

Private Function PositiveNumericOrBlank(ByVal value As Variant) As Variant
    Dim numberValue As Variant

    numberValue = NumericOrBlank(value)
    If IsEmpty(numberValue) Or CDbl(numberValue) <= 0 Then
        PositiveNumericOrBlank = Empty
    Else
        PositiveNumericOrBlank = numberValue
    End If
End Function

Private Function NumericOrBlank(ByVal value As Variant) As Variant
    If IsEmpty(value) Or IsNull(value) Then
        NumericOrBlank = Empty
    ElseIf Len(Trim$(CStr(value))) = 0 Then
        NumericOrBlank = Empty
    ElseIf IsNumeric(value) Then
        NumericOrBlank = CDbl(value)
    Else
        NumericOrBlank = Empty
    End If
End Function

Private Function SnapshotProjectedRowScalar(ByVal rowValue As JsonValue, _
                                            ByVal fieldIndex As Long) As Variant
    Dim fieldValue As JsonValue

    If rowValue Is Nothing Then
        SnapshotProjectedRowScalar = Empty
        Exit Function
    End If
    If rowValue.Kind <> "array" Or fieldIndex < 1 Or _
       fieldIndex > PRICING_SNAPSHOT_ROW_FIELD_COUNT Then
        SnapshotProjectedRowScalar = Empty
        Exit Function
    End If
    Set fieldValue = JsonRuntime.JsonArrayItem(rowValue, fieldIndex)
    SnapshotProjectedRowScalar = JsonRuntime.JsonScalar(fieldValue)
End Function

Private Function SnapshotProjectedRowText(ByVal rowValue As JsonValue, _
                                          ByVal fieldIndex As Long) As String
    SnapshotProjectedRowText = Trim$(CStr(BlankIfNull( _
        SnapshotProjectedRowScalar(rowValue, fieldIndex))))
End Function

Private Function ReconciledRowScalar(ByVal rowValue As JsonValue, _
                                     ByVal fieldName As String, _
                                     ByVal projectionIndex As Long) As Variant
    If rowValue Is Nothing Then
        ReconciledRowScalar = Empty
    ElseIf rowValue.Kind = "array" Then
        ReconciledRowScalar = SnapshotProjectedRowScalar( _
            rowValue, projectionIndex)
    Else
        ReconciledRowScalar = JsonRuntime.JsonText(rowValue, fieldName)
    End If
End Function

Private Function ReconciledRowNumeric(ByVal rowValue As JsonValue, _
                                      ByVal fieldName As String, _
                                      ByVal projectionIndex As Long) As Variant
    ReconciledRowNumeric = NumericOrBlank(ReconciledRowScalar( _
        rowValue, fieldName, projectionIndex))
End Function

Private Function ReconciledRowText(ByVal rowValue As JsonValue, _
                                   ByVal fieldName As String, _
                                   ByVal projectionIndex As Long) As String
    ReconciledRowText = Trim$(CStr(BlankIfNull(ReconciledRowScalar( _
        rowValue, fieldName, projectionIndex))))
End Function

Private Function SiteNumeric(ByVal siteRow As JsonValue, _
                             ByVal fieldName As String) As Variant
    If siteRow Is Nothing Then
        SiteNumeric = Empty
    Else
        SiteNumeric = NumericOrBlank( _
            JsonRuntime.JsonText(siteRow, fieldName))
    End If
End Function

Private Function SiteText(ByVal siteRow As JsonValue, _
                          ByVal fieldName As String) As String
    If Not siteRow Is Nothing Then
        SiteText = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(siteRow, fieldName))))
    End If
End Function

Private Function FirstNumeric(ByVal firstValue As Variant, _
                              ByVal secondValue As Variant) As Variant
    Dim parsed As Variant

    parsed = NumericOrBlank(firstValue)
    If Not IsEmpty(parsed) Then
        FirstNumeric = parsed
        Exit Function
    End If
    FirstNumeric = NumericOrBlank(secondValue)
End Function

Private Function FirstText(ByVal firstValue As Variant, _
                           ByVal secondValue As Variant, _
                           ByVal fallback As String) As String
    FirstText = Trim$(CStr(BlankIfNull(firstValue)))
    If Len(FirstText) = 0 Then
        FirstText = Trim$(CStr(BlankIfNull(secondValue)))
    End If
    If Len(FirstText) = 0 Then FirstText = fallback
End Function

Private Sub CountNumericValue(ByVal counts As Object, ByVal value As Variant)
    Dim key As String

    If IsEmpty(value) Or Not IsNumeric(value) Then Exit Sub
    key = FormatNumericForText(CDbl(value))
    If counts.Exists(key) Then
        counts(key) = CLng(counts(key)) + 1
    Else
        counts.Add key, 1
    End If
End Sub

Private Function MostCommonNumeric(ByVal counts As Object) As Variant
    Dim key As Variant
    Dim bestKey As String
    Dim bestCount As Long

    For Each key In counts.Keys
        If CLng(counts(key)) > bestCount Then
            bestCount = CLng(counts(key))
            bestKey = CStr(key)
        End If
    Next key
    If bestCount > 0 Then MostCommonNumeric = Val(bestKey)
End Function

Private Function CurrencyDateAgeDays(ByVal value As String) As Long
    Dim normalized As String
    Dim parsedDate As Date

    On Error GoTo InvalidDate
    normalized = Trim$(value)
    If Len(normalized) = 10 And Mid$(normalized, 5, 1) = "-" Then
        parsedDate = DateSerial( _
            CLng(Left$(normalized, 4)), _
            CLng(Mid$(normalized, 6, 2)), _
            CLng(Right$(normalized, 2)))
        If parsedDate > Date Then GoTo InvalidDate
        CurrencyDateAgeDays = DateDiff("d", parsedDate, Date)
        Exit Function
    End If
InvalidDate:
    CurrencyDateAgeDays = 99999
End Function

Private Function BooleanValue(ByVal value As Variant) As Boolean
    If VarType(value) = vbBoolean Then
        BooleanValue = CBool(value)
    Else
        BooleanValue = LCase$(Trim$(CStr(value))) = "true" Or _
            Trim$(CStr(value)) = "1"
    End If
End Function

Private Function BuildOtherText(ByVal weightValue As Variant, _
                                ByVal locationValue As String) As String
    If Not IsEmpty(weightValue) Then
        BuildOtherText = FormatNumericForText(CDbl(weightValue)) & _
            U("06AF06310645")
    End If
    If Len(locationValue) > 0 Then
        If Len(BuildOtherText) > 0 Then
            BuildOtherText = BuildOtherText & " " & U("0640") & locationValue
        Else
            BuildOtherText = locationValue
        End If
    End If
End Function

Private Function FormatNumericForText(ByVal value As Double) As String
    Dim digitIndex As Long

    ' Str$ is deliberately used instead of Format$/CStr: it always emits the
    ' period decimal separator required by JSON and does not add locale group
    ' separators. Trim the leading space used for positive values.
    FormatNumericForText = Trim$(Str$(value))
    ' Normalize defensively in case a future Office host localizes digits.
    For digitIndex = 0 To 9
        FormatNumericForText = Replace$(FormatNumericForText, _
            ChrW(&H6F0 + digitIndex), CStr(digitIndex))
        FormatNumericForText = Replace$(FormatNumericForText, _
            ChrW(&H660 + digitIndex), CStr(digitIndex))
    Next digitIndex
    FormatNumericForText = Replace$(FormatNumericForText, ChrW(&H66B), ".")
    FormatNumericForText = Replace$(FormatNumericForText, ChrW(&H66C), "")
    FormatNumericForText = Replace$(FormatNumericForText, ChrW(&H2212), "-")
End Function

Private Function CanonicalCellText(ByVal value As Variant) As String
    If IsEmpty(value) Or IsNull(value) Then Exit Function
    If IsNumeric(value) Then
        CanonicalCellText = FormatNumericForText(CDbl(value))
    Else
        CanonicalCellText = Trim$(CStr(value))
    End If
End Function

Private Function CanonicalDateText(ByVal value As Variant) As String
    Dim parsedDate As Date
    Dim normalized As String

    On Error GoTo InvalidDate
    If IsEmpty(value) Or IsNull(value) Then Exit Function
    If VarType(value) = vbDate Then
        parsedDate = CDate(value)
        CanonicalDateText = ISODateText(parsedDate)
        Exit Function
    End If
    If VarType(value) <> vbString And IsNumeric(value) Then
        parsedDate = CDate(CDbl(value))
        CanonicalDateText = ISODateText(parsedDate)
        Exit Function
    End If

    normalized = Trim$(CStr(value))
    If Len(normalized) <> 10 Or Mid$(normalized, 5, 1) <> "-" Or _
       Mid$(normalized, 8, 1) <> "-" Then GoTo InvalidDate
    parsedDate = DateSerial( _
        CLng(Left$(normalized, 4)), _
        CLng(Mid$(normalized, 6, 2)), _
        CLng(Right$(normalized, 2)))
    If ISODateText(parsedDate) <> normalized Then GoTo InvalidDate
    CanonicalDateText = normalized
    Exit Function

InvalidDate:
    CanonicalDateText = vbNullString
End Function

Private Function ISODateText(ByVal value As Date) As String
    ISODateText = Right$("0000" & CStr(Year(value)), 4) & "-" & _
        Right$("00" & CStr(Month(value)), 2) & "-" & _
        Right$("00" & CStr(Day(value)), 2)
End Function

Private Function CanonicalDateValuesEqual(ByVal leftValue As Variant, _
                                          ByVal rightValue As Variant) As Boolean
    Dim leftText As String
    Dim rightText As String

    leftText = CanonicalDateText(leftValue)
    rightText = CanonicalDateText(rightValue)
    If Len(leftText) = 0 Or Len(rightText) = 0 Then
        CanonicalDateValuesEqual = _
            Len(CanonicalCellText(leftValue)) = 0 And _
            Len(CanonicalCellText(rightValue)) = 0
    Else
        CanonicalDateValuesEqual = leftText = rightText
    End If
End Function

Private Sub ValidateProposalDateNormalization()
    Dim settings As Worksheet
    Dim settingsSnapshot As Variant
    Dim settingsSnapshotCaptured As Boolean
    Dim sampleDate As Date
    Dim expectedDate As String
    Dim savedThreshold As Variant
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    settingsSnapshot = CapturePricingStateSnapshot(settings)
    settingsSnapshotCaptured = True
    savedThreshold = settings.Range("B24").Value2
    sampleDate = DateSerial(2026, 7, 27)
    expectedDate = "2026-07-27"
    If CanonicalDateText(sampleDate) <> expectedDate Then
        Err.Raise vbObjectError + 200, _
                  "ValidateProposalDateNormalization", _
                  "VBA Date values are not normalized to ISO dates."
    End If
    If CanonicalDateText(CDbl(sampleDate)) <> expectedDate Then
        Err.Raise vbObjectError + 201, _
                  "ValidateProposalDateNormalization", _
                  "Excel date serials are not normalized to ISO dates."
    End If
    If Not CanonicalDateValuesEqual(CDbl(sampleDate), expectedDate) Then
        Err.Raise vbObjectError + 202, _
                  "ValidateProposalDateNormalization", _
                  "An Excel date serial drifted from its equivalent ISO date."
    End If
    If CanonicalDateValuesEqual(CDbl(sampleDate), "2026-07-28") Then
        Err.Raise vbObjectError + 203, _
                  "ValidateProposalDateNormalization", _
                  "Distinct pricing dates were incorrectly treated as equal."
    End If
    If Len(CanonicalDateText("2026-02-31")) > 0 Then
        Err.Raise vbObjectError + 204, _
                  "ValidateProposalDateNormalization", _
                  "An invalid ISO date was accepted."
    End If

    settings.Range("B18").Value2 = 29500#
    settings.Range("B19").Value2 = 187891#
    settings.Range("B20").Value2 = CDbl(sampleDate)
    settings.Range("E20").Value2 = CDbl(sampleDate)
    settings.Range("B21").Value2 = 0.3
    settings.Range("B22").Value2 = 120#
    settings.Range("B26").Value2 = 2#
    settings.Range("B24").Value2 = 0.07
    UpdateProposalDriftFlags settings, 29500#, 187891#, expectedDate, _
        expectedDate, 0.3, 120#, 2#
    If BooleanValue(settings.Range("G39").Value2) Or _
       BooleanValue(settings.Range("G40").Value2) Then
        Err.Raise vbObjectError + 205, _
                  "ValidateProposalDateNormalization", _
                  "An equivalent Excel serial and ISO date set proposal drift."
    End If

    settings.Range("B20").Value2 = CDbl(sampleDate) + 1#
    UpdateProposalDriftFlags settings, 29500#, 187891#, expectedDate, _
        expectedDate, 0.3, 120#, 2#
    If Not BooleanValue(settings.Range("G39").Value2) Then
        Err.Raise vbObjectError + 206, _
                  "ValidateProposalDateNormalization", _
                  "A genuinely different pricing date did not set proposal drift."
    End If

CleanExit:
    On Error Resume Next
    If settingsSnapshotCaptured Then
        RestorePricingStateSnapshot settings, settingsSnapshot
        settings.Range("B24").Value2 = savedThreshold
    End If
    On Error GoTo 0
    If savedErrorNumber <> 0 Then
        Err.Raise savedErrorNumber, _
                  "ValidateProposalDateNormalization", _
                  savedErrorDescription
    End If
    Exit Sub

Failed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    Resume CleanExit
End Sub

Private Function SafeStatusError(ByVal message As String) As String
    Dim lowered As String
    Dim sanitized As String

    message = Replace$(message, vbCr, " ")
    message = Replace$(message, vbLf, " ")
    lowered = LCase$(message)
    If InStr(lowered, "credential") > 0 Or _
       InStr(lowered, "authorization") > 0 Or _
       InStr(lowered, "secret") > 0 Or _
       InStr(lowered, "token") > 0 Then
        SafeStatusError = T("bridge_missing")
    Else
        sanitized = Replace$(message, "RefreshPricingState", vbNullString, _
                             1, -1, vbTextCompare)
        sanitized = Replace$(sanitized, "[pricing state]", vbNullString, _
                             1, -1, vbTextCompare)
        sanitized = Replace$(sanitized, "pricing state", vbNullString, _
                             1, -1, vbTextCompare)
        sanitized = TrimStatusPunctuation(sanitized)
        lowered = LCase$(sanitized)
        If Len(sanitized) = 0 Or InStr(lowered, "http") > 0 Or _
           InStr(lowered, "json") > 0 Or InStr(lowered, "runtime") > 0 Or _
           InStr(lowered, "error") > 0 Or InStr(lowered, "exception") > 0 Or _
           InStr(sanitized, "[") > 0 Or InStr(sanitized, "]") > 0 Then
            SafeStatusError = T("sync_retry")
        ElseIf Len(sanitized) > 220 Then
            SafeStatusError = Left$(sanitized, 217) & U("2026")
        Else
            SafeStatusError = sanitized
        End If
    End If
End Function

Private Function TrimStatusPunctuation(ByVal message As String) As String
    message = Trim$(message)
    Do While Len(message) > 0 And _
             (Left$(message, 1) = ":" Or Left$(message, 1) = "-" Or _
              Left$(message, 1) = "]" Or Left$(message, 1) = "[")
        message = Trim$(Mid$(message, 2))
    Loop
    TrimStatusPunctuation = message
End Function

Public Function FriendlyStatusErrorForValidation( _
        ByVal message As String) As String
    FriendlyStatusErrorForValidation = SafeStatusError(message)
End Function

Public Function AuditMessageDialogForValidation() As Boolean
    On Error GoTo AuditFailed
    Load DigitalogicMessage
    DigitalogicMessage.ConfigureUnicodeHex _
        "062A06450627064500200645062A0646200C0647062706CC0020064106270631063306CC0020064106310645002006280647200C062F06310633062A06CC002006460645062706CC06340020062F0627062F06470020064506CC200C063406480646062F002E", _
        "062806310631063306CC002006460645062706CC063400200635062D06CC062D0020064106270631063306CC", _
        CLng(vbQuestion Or vbYesNo), "0628062706340647", "062806440647", _
        "062E06CC0631", DEFAULT_PERSIAN_FONT, DEFAULT_LATIN_FONT
    AuditMessageDialogForValidation = _
        DigitalogicMessage.ValidateFonts(DEFAULT_PERSIAN_FONT, _
                                          DEFAULT_LATIN_FONT) And _
        DigitalogicMessage.ValidateUnicodeCaptions( _
            "062A06450627064500200645062A0646200C0647062706CC0020064106270631063306CC0020064106310645002006280647200C062F06310633062A06CC002006460645062706CC06340020062F0627062F06470020064506CC200C063406480646062F002E", _
            "062806310631063306CC002006460645062706CC063400200635062D06CC062D0020064106270631063306CC", _
            "062806440647", "062E06CC0631")
    Unload DigitalogicMessage
    Exit Function
AuditFailed:
    On Error Resume Next
    Unload DigitalogicMessage
    On Error GoTo 0
End Function

Public Function ValidateFontPolicyFixturesForValidation() As Boolean
    Dim valid As Boolean
    Dim ignoredMode As String

    ignoredMode = NormalizedFontAuditMode("invalid", valid)
    If valid Or Len(ignoredMode) <> 0 Then Exit Function
    If NormalizedFontAuditMode( _
           U("062A0631064506CC064500200648002006470634062F06270631"), _
           valid) <> "RepairAndWarn" Or Not valid Then Exit Function
    If Not NormalizedYesNo(U("062806440647"), valid) Or _
       Not valid Then Exit Function
    If NormalizedYesNo(U("062E06CC0631"), valid) Or _
       Not valid Then Exit Function
    If Len(Trim$(vbNullString)) <> 0 Then Exit Function
    If FontAvailable("__PatrisExportDefinitelyMissingFont__") Then _
        Exit Function
    ValidateFontPolicyFixturesForValidation = True
End Function

Private Function PhaseTimestamp() As Double
    PhaseTimestamp = Timer
End Function

Private Function PhaseElapsed(ByVal startedAt As Double) As Double
    PhaseElapsed = Timer - startedAt
    If PhaseElapsed < 0# Then PhaseElapsed = PhaseElapsed + 86400#
End Function

Private Sub AppendStatePageTiming(ByVal page As Long, _
                                  ByVal seconds As Double)
    Dim item As String

    item = "p" & CStr(page) & "=" & Format$(seconds, "0.000") & "s"
    If Len(mStatePageTimingText) = 0 Then
        mStatePageTimingText = item
    Else
        mStatePageTimingText = mStatePageTimingText & "; " & item
    End If
End Sub

Private Function NormalizeHumanProductName(ByVal value As String) As String
    Dim matcher As Object

    Set matcher = CreateObject("VBScript.RegExp")
    matcher.Global = False
    matcher.IgnoreCase = True
    matcher.Pattern = "[ " & vbTab & ChrW(160) & "]*[-" & _
        ChrW(&H2013) & ChrW(&H2014) & "][ " & vbTab & ChrW(160) & _
        "]*WooID[ " & vbTab & ChrW(160) & "]+[0-9]+[ " & vbTab & _
        ChrW(160) & "]*$"
    NormalizeHumanProductName = matcher.Replace(value, vbNullString)
End Function

Private Sub ValidateProductNameNormalizer()
    If NormalizeHumanProductName("Part - WooID 1234") <> "Part" Or _
       NormalizeHumanProductName("Part " & ChrW(&H2013) & _
                                 " WooID 1234") <> "Part" Or _
       NormalizeHumanProductName("Part" & ChrW(&H2014) & _
                                 "WooID 1234") <> "Part" Or _
       NormalizeHumanProductName("Part WooID 1234") <> _
                                 "Part WooID 1234" Or _
       NormalizeHumanProductName("Part - WooID 1234 rev2") <> _
                                 "Part - WooID 1234 rev2" Or _
       NormalizeHumanProductName("SKU-WooID-1234") <> _
                                 "SKU-WooID-1234" Then
        Err.Raise vbObjectError + 224, "ValidateProductNameNormalizer", _
                  T("invalid_workbook")
    End If
End Sub

Private Function ReadSearchLiteral() As String
    Dim inputCell As Range
    Dim displayed As String
    Dim rawValue As Variant
    Dim previousEvents As Boolean

    Set inputCell = ThisWorkbook.Names("ProductSearchQuery").RefersToRange
    displayed = CStr(inputCell.Text)
    rawValue = inputCell.Value2
    inputCell.MergeArea.NumberFormat = "@"
    If VarType(rawValue) = vbString Then
        ReadSearchLiteral = CStr(rawValue)
        Exit Function
    End If
    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    Application.EnableEvents = False
    If VarType(rawValue) = vbDate Then
        inputCell.Value2 = displayed
    ElseIf Not IsEmpty(rawValue) Then
        If Len(displayed) > 0 Then inputCell.Value2 = displayed
    End If
    ReadSearchLiteral = CStr(inputCell.Value2)
CleanExit:
    Application.EnableEvents = previousEvents
End Function

Public Sub PreserveSearchLiteral()
    Dim preserved As String

    On Error Resume Next
    preserved = ReadSearchLiteral()
    On Error GoTo 0
End Sub

Private Sub ValidateSearchLiteralRuntime()
    Dim inputCell As Range
    Dim originalValue As Variant
    Dim sample As Variant

    Set inputCell = ThisWorkbook.Names("ProductSearchQuery").RefersToRange
    originalValue = inputCell.Value2
    For Each sample In Array("2.4", "25.40", "12/3", "01.02", "001234", _
                             U("06F106F206F306F4"))
        inputCell.MergeArea.NumberFormat = "@"
        inputCell.Value2 = CStr(sample)
        If ReadSearchLiteral() <> CStr(sample) Or _
           CStr(inputCell.Text) <> CStr(sample) Then
            Err.Raise vbObjectError + 225, _
                      "ValidateSearchLiteralRuntime", T("invalid_workbook")
        End If
    Next sample
    inputCell.Value2 = originalValue
    inputCell.MergeArea.NumberFormat = "@"
End Sub

Private Function AppendNonzeroCount(ByVal summary As String, _
                                    ByVal countValue As Long, _
                                    ByVal labelText As String) As String
    If countValue <= 0 Then
        AppendNonzeroCount = summary
        Exit Function
    End If
    If Len(summary) > 0 Then summary = summary & U("061B") & " "
    AppendNonzeroCount = summary & CStr(countValue) & " " & labelText
End Function

Private Function FormatNonzeroStatusSummary( _
        ByVal patrisRows As Long, ByVal wooRows As Long, _
        ByVal matchedRows As Long, ByVal sourceOnlyRows As Long, _
        ByVal wooOnlyRows As Long, ByVal priceMatchedRows As Long, _
        ByVal overLimitRows As Long) As String
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, patrisRows, T("patris_rows"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, wooRows, T("woo_products"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, matchedRows, T("matched"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, sourceOnlyRows, T("source_only"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, wooOnlyRows, T("woo_only"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, priceMatchedRows, T("price_matched"))
    FormatNonzeroStatusSummary = AppendNonzeroCount( _
        FormatNonzeroStatusSummary, overLimitRows, T("over_limit"))
    If Len(FormatNonzeroStatusSummary) = 0 Then _
        FormatNonzeroStatusSummary = T("sync_no_issues")
End Function

Public Function FormatStatusSummaryForValidation( _
        ByVal patrisRows As Long, ByVal wooRows As Long, _
        ByVal matchedRows As Long, ByVal sourceOnlyRows As Long, _
        ByVal wooOnlyRows As Long, ByVal priceMatchedRows As Long, _
        ByVal overLimitRows As Long) As String
    FormatStatusSummaryForValidation = FormatNonzeroStatusSummary( _
        patrisRows, wooRows, matchedRows, sourceOnlyRows, wooOnlyRows, _
        priceMatchedRows, overLimitRows)
End Function

Public Function FormatFontAuditSummaryForValidation( _
        ByVal missingCount As Long, ByVal driftCount As Long) As String
    FormatFontAuditSummaryForValidation = AppendNonzeroCount( _
        vbNullString, missingCount, T("font_missing"))
    FormatFontAuditSummaryForValidation = AppendNonzeroCount( _
        FormatFontAuditSummaryForValidation, driftCount, T("font_drift"))
    If Len(FormatFontAuditSummaryForValidation) = 0 Then _
        FormatFontAuditSummaryForValidation = T("font_no_issues")
End Function

Private Sub ValidateStatusSummaryFormatter()
    Dim summary As String

    summary = FormatNonzeroStatusSummary(0, 0, 3, 0, 2, 0, 0)
    If InStr(1, summary, "3 ", vbBinaryCompare) = 0 Or _
       InStr(1, summary, "2 ", vbBinaryCompare) = 0 Or _
       InStr(1, summary, "0 ", vbBinaryCompare) > 0 Then
        Err.Raise vbObjectError + 226, _
                  "ValidateStatusSummaryFormatter", T("invalid_workbook")
    End If
    If FormatNonzeroStatusSummary(0, 0, 0, 0, 0, 0, 0) <> _
       T("sync_no_issues") Then
        Err.Raise vbObjectError + 227, _
                  "ValidateStatusSummaryFormatter", T("invalid_workbook")
    End If
    summary = FormatFontAuditSummaryForValidation(2, 0)
    If InStr(1, summary, "2 ", vbBinaryCompare) = 0 Or _
       InStr(1, summary, "0 ", vbBinaryCompare) > 0 Or _
       FormatFontAuditSummaryForValidation(0, 0) <> _
       T("font_no_issues") Then
        Err.Raise vbObjectError + 227, _
                  "ValidateStatusSummaryFormatter", T("invalid_workbook")
    End If
End Sub

Private Function WarningHasPositiveCount(ByVal warning As JsonValue) As Boolean
    Dim countValue As JsonValue
    Dim textValue As String

    Set countValue = JsonRuntime.JsonMember(warning, "count")
    If countValue Is Nothing Then
        WarningHasPositiveCount = True
        Exit Function
    End If
    textValue = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(warning, "count"))))
    WarningHasPositiveCount = IsNumeric(textValue) And CDbl(textValue) > 0#
End Function

Private Function NamedText(ByVal nameText As String, _
                           ByVal fallback As String) As String
    On Error GoTo Missing
    NamedText = Trim$(CStr(ThisWorkbook.Names(nameText). _
        RefersToRange.Value2))
    If Len(NamedText) = 0 Then GoTo Missing
    Exit Function
Missing:
    NamedText = fallback
End Function

Private Function NormalizedFontAuditMode(ByVal value As String, _
                                         ByRef valid As Boolean) As String
    Select Case LCase$(Trim$(value))
        Case "off", U("062E0627064506480634")
            NormalizedFontAuditMode = "Off"
        Case "warn", U("06470634062F06270631")
            NormalizedFontAuditMode = "Warn"
        Case "repairandwarn", _
             U("062A0631064506CC064500200648002006470634062F06270631")
            NormalizedFontAuditMode = "RepairAndWarn"
        Case "strict", U("0633062E062A06AF06CC0631062706460647")
            NormalizedFontAuditMode = "Strict"
        Case Else
            valid = False
            Exit Function
    End Select
    valid = True
End Function

Private Function NamedYesNo(ByVal nameText As String, _
                            ByRef valid As Boolean) As Boolean
    NamedYesNo = NormalizedYesNo( _
        NamedText(nameText, vbNullString), valid)
End Function

Private Function NormalizedYesNo(ByVal value As String, _
                                 ByRef valid As Boolean) As Boolean
    Select Case LCase$(Trim$(value))
        Case "yes", U("062806440647"): NormalizedYesNo = True
        Case "no", U("062E06CC0631"): NormalizedYesNo = False
        Case Else: valid = False: Exit Function
    End Select
    valid = True
End Function

Private Function FontAvailable(ByVal fontName As String) As Boolean
    Dim fontControl As Object
    Dim index As Long

    On Error GoTo Unavailable
    Set fontControl = Application.CommandBars("Formatting").Controls(1)
    For index = 1 To fontControl.ListCount
        If StrComp(CStr(fontControl.List(index)), fontName, _
                   vbTextCompare) = 0 Then
            FontAvailable = True
            Exit Function
        End If
    Next index
Unavailable:
End Function

Private Function FontSlotMatches(ByVal value As Variant, _
                                 ByVal expected As String) As Boolean
    On Error GoTo Mismatch
    If IsNull(value) Or IsEmpty(value) Then Exit Function
    FontSlotMatches = StrComp(CStr(value), expected, vbTextCompare) = 0
Mismatch:
End Function

Private Function FontObjectSlotSupported(ByVal fontValue As Object, _
                                         ByVal slotName As String) As Boolean
    Dim ignored As Variant

    On Error GoTo Unsupported
    ignored = CallByName(fontValue, slotName, VbGet)
    FontObjectSlotSupported = True
Unsupported:
End Function

Private Function FontObjectSlotMatches(ByVal fontValue As Object, _
                                       ByVal slotName As String, _
                                       ByVal expected As String) As Boolean
    Dim value As Variant

    On Error GoTo Mismatch
    value = CallByName(fontValue, slotName, VbGet)
    FontObjectSlotMatches = FontSlotMatches(value, expected)
Mismatch:
End Function

Private Sub RepairFontObjectSlot(ByVal fontValue As Object, _
                                 ByVal slotName As String, _
                                 ByVal expected As String)
    On Error Resume Next
    CallByName fontValue, slotName, VbLet, expected
    On Error GoTo 0
End Sub

Private Function AuditRangeFont(ByVal target As Range, _
                                ByVal expected As String, _
                                ByVal repair As Boolean) As Long
    Dim fontValue As Object
    Dim slotName As Variant
    Dim drifted As Boolean

    Set fontValue = target.Font
    For Each slotName In Array("Name", "NameComplexScript", "NameFarEast")
        If FontObjectSlotSupported(fontValue, CStr(slotName)) Then
            If Not FontObjectSlotMatches(fontValue, CStr(slotName), _
                                         expected) Then drifted = True
        End If
    Next slotName
    If drifted Then
        AuditRangeFont = 1
        If repair Then
            For Each slotName In Array("Name", "NameComplexScript", _
                                       "NameFarEast")
                RepairFontObjectSlot fontValue, CStr(slotName), expected
            Next slotName
        End If
    End If
End Function

Private Function AuditShapeFont(ByVal shapeValue As Shape, _
                                ByVal expected As String, _
                                ByVal repair As Boolean) As Long
    On Error GoTo NoText
    If shapeValue.TextFrame2.HasText = 0 Then Exit Function
    With shapeValue.TextFrame2.TextRange.Font
        If Not FontSlotMatches(.Name, expected) Or _
           Not FontSlotMatches(.NameComplexScript, expected) Or _
           Not FontSlotMatches(.NameFarEast, expected) Or _
           CLng(shapeValue.TextFrame2.TextRange.LanguageID) <> 1065 Then
            AuditShapeFont = 1
            If repair Then
                .Name = expected
                .NameComplexScript = expected
                .NameFarEast = expected
                shapeValue.TextFrame2.TextRange.LanguageID = 1065
                On Error Resume Next
                shapeValue.TextFrame.Characters.Font.Name = expected
                On Error GoTo 0
            End If
        End If
    End With
NoText:
End Function

Private Function AuditMappedGridFont(ByVal target As Range, _
                                     ByVal latinCells As Range, _
                                     ByVal persianFont As String, _
                                     ByVal latinFont As String, _
                                     ByVal repair As Boolean) As Long
    Dim cell As Range
    Dim overlap As Range
    Dim expected As String

    For Each cell In target.Cells
        Set overlap = Nothing
        Set overlap = Application.Intersect(cell, latinCells)
        If overlap Is Nothing Then
            expected = persianFont
        Else
            expected = latinFont
        End If
        AuditMappedGridFont = AuditMappedGridFont + _
            AuditRangeFont(cell, expected, repair)
    Next cell
End Function

Private Function AuditFixedFontMap(ByVal persianFont As String, _
                                   ByVal latinFont As String, _
                                   ByVal repair As Boolean) As Long
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim dashboard As Worksheet
    Dim settings As Worksheet
    Dim latinCells As Range
    Dim sheet As Worksheet
    Dim shapeValue As Shape
    Dim columnIndex As Variant
    Dim priceDisplayFont As String

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    Set dashboard = ThisWorkbook.Worksheets(2)
    Set settings = ConfigSheet()
    priceDisplayFont = PriceDisplayFontName(latinFont)
    AuditFixedFontMap = AuditFixedFontMap + _
        AuditRangeFont(table.HeaderRowRange, persianFont, repair)
    AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
        PriceSheet().Range("B1:K5"), persianFont, repair)
    AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
        PriceSheet().Range("C3:E3"), persianFont, repair)
    If Not table.DataBodyRange Is Nothing Then
        For Each columnIndex In Array(3, 4, 8, 10)
            AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
                table.ListColumns(CLng(columnIndex)).DataBodyRange, _
                persianFont, repair)
        Next columnIndex
        AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
            table.ListColumns(1).DataBodyRange, priceDisplayFont, repair)
        For Each columnIndex In Array(2, 5, 6, 7, 9)
            AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
                table.ListColumns(CLng(columnIndex)).DataBodyRange, _
                latinFont, repair)
        Next columnIndex
    End If
    Set latinCells = dashboard.Range( _
        "B5:C6,D5:E6,F5:G6,H5:I6,B9:C10,D9:E10," & _
        "F9:G10,H9:I10,B13:E14,F13:G14,H13:I14," & _
        "C22:C24,C27:C29,C32:C34")
    AuditFixedFontMap = AuditFixedFontMap + AuditMappedGridFont( _
        dashboard.Range("B1:I34"), latinCells, persianFont, latinFont, repair)
    Set latinCells = settings.Range( _
        "B3:F4,B7:F7,B10:F15,B18:F22,B24:F26," & _
        "B39:F40,B46:F55")
    AuditFixedFontMap = AuditFixedFontMap + AuditMappedGridFont( _
        settings.Range("A1:F55"), latinCells, persianFont, latinFont, repair)
    AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
        syncTable.HeaderRowRange, persianFont, repair)
    If Not syncTable.DataBodyRange Is Nothing Then
        For Each columnIndex In Array(1, 2, 3, 4, 5, 6, 7, 8, _
                                      9, 10, 11, 12, 13, 14, 15, 16, _
                                      21, 22, 23, 24)
            AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
                syncTable.ListColumns(CLng(columnIndex)).DataBodyRange, _
                latinFont, repair)
        Next columnIndex
        For Each columnIndex In Array(17, 18, 19, 20)
            AuditFixedFontMap = AuditFixedFontMap + AuditRangeFont( _
                syncTable.ListColumns(CLng(columnIndex)).DataBodyRange, _
                persianFont, repair)
        Next columnIndex
    End If
    For Each sheet In ThisWorkbook.Worksheets
        For Each shapeValue In sheet.Shapes
            On Error Resume Next
            If Len(Trim$(CStr(shapeValue.OnAction))) > 0 Then
                AuditFixedFontMap = AuditFixedFontMap + _
                    AuditShapeFont(shapeValue, persianFont, repair)
            End If
            On Error GoTo 0
        Next shapeValue
    Next sheet
End Function

Private Sub EnforceConfiguredFontsAfterRefresh()
    Dim persianFont As String
    Dim latinFont As String

    persianFont = NamedText("PersianFont", DEFAULT_PERSIAN_FONT)
    latinFont = NamedText("LatinFont", DEFAULT_LATIN_FONT)
    Call AuditFixedFontMap(persianFont, latinFont, True)
    If AuditFixedFontMap(persianFont, latinFont, False) <> 0 Then
        Err.Raise vbObjectError + 231, _
                  "EnforceConfiguredFontsAfterRefresh", _
                  T("font_policy_invalid")
    End If
End Sub

Public Function AuditFontsForValidation() As Boolean
    On Error GoTo AuditFailed
    AuditFontsForValidation = AuditConfiguredFonts(True, True)
    Exit Function

AuditFailed:
    ' A validator fixture must report failure through its return value. Letting
    ' an expected strict-audit error escape Excel.Run opens a hidden VBE modal
    ' and strands the out-of-process validator until its host timeout.
    AuditFontsForValidation = False
End Function

Public Function RepairFontDriftForValidation() As Boolean
    Dim persianFont As String
    Dim latinFont As String
    Dim driftCount As Long

    persianFont = NamedText("PersianFont", vbNullString)
    latinFont = NamedText("LatinFont", vbNullString)
    If Len(persianFont) = 0 Or Len(latinFont) = 0 Then Exit Function
    driftCount = AuditFixedFontMap(persianFont, latinFont, True)
    RepairFontDriftForValidation = driftCount > 0 And _
        AuditFixedFontMap(persianFont, latinFont, False) = 0
End Function

Public Function AuditFontsOnOpen() As Boolean
    On Error GoTo AuditFailed
    AuditFontsOnOpen = AuditConfiguredFonts(True)
    Exit Function

AuditFailed:
    On Error Resume Next
    ConfigSheet().Range("B6").Value2 = SafeStatusError(Err.Description)
    On Error GoTo 0
    AuditFontsOnOpen = False
End Function

Public Function AuditConfiguredFonts( _
        Optional ByVal onOpen As Boolean = False, _
        Optional ByVal forceStrict As Boolean = False) As Boolean
    Dim persianFont As String
    Dim latinFont As String
    Dim priceDisplayFont As String
    Dim auditMode As String
    Dim valid As Boolean
    Dim validateOnOpen As Boolean
    Dim allowFallback As Boolean
    Dim repair As Boolean
    Dim missingCount As Long
    Dim driftCount As Long
    Dim summary As String

    persianFont = NamedText("PersianFont", vbNullString)
    latinFont = NamedText("LatinFont", vbNullString)
    If Len(persianFont) = 0 Or Len(latinFont) = 0 Then _
        Err.Raise vbObjectError + 228, "AuditConfiguredFonts", _
                  T("font_policy_invalid")
    priceDisplayFont = PriceDisplayFontName(latinFont)
    auditMode = NormalizedFontAuditMode( _
        NamedText("FontAuditMode", vbNullString), valid)
    If Not valid Then Err.Raise vbObjectError + 228, _
        "AuditConfiguredFonts", T("font_policy_invalid")
    validateOnOpen = NamedYesNo("ValidateFontsOnOpen", valid)
    If Not valid Then Err.Raise vbObjectError + 229, _
        "AuditConfiguredFonts", T("font_policy_invalid")
    allowFallback = NamedYesNo("AllowFallback", valid)
    If Not valid Then Err.Raise vbObjectError + 230, _
        "AuditConfiguredFonts", T("font_policy_invalid")
    If onOpen And Not validateOnOpen And Not forceStrict Then
        AuditConfiguredFonts = True
        Exit Function
    End If
    If forceStrict Then auditMode = "Strict"
    If auditMode = "Off" Then
        AuditConfiguredFonts = True
        Exit Function
    End If
    If Not FontAvailable(persianFont) Then missingCount = missingCount + 1
    If Not FontAvailable(latinFont) Then missingCount = missingCount + 1
    If StrComp(priceDisplayFont, latinFont, vbTextCompare) <> 0 Then
        If Not FontAvailable(priceDisplayFont) Then _
            missingCount = missingCount + 1
    End If
    repair = auditMode = "RepairAndWarn"
    driftCount = AuditFixedFontMap(persianFont, latinFont, repair)
    If missingCount = 0 And driftCount = 0 Then
        AuditConfiguredFonts = True
        Exit Function
    End If
    summary = FormatFontAuditSummaryForValidation( _
        missingCount, driftCount)
    If auditMode = "Strict" And _
       (driftCount > 0 Or (missingCount > 0 And Not allowFallback)) Then
        Err.Raise vbObjectError + 231, "AuditConfiguredFonts", summary
    End If
    If auditMode = "Strict" Then
        AuditConfiguredFonts = True
        Exit Function
    End If
    On Error Resume Next
    ConfigSheet().Range("B6").Value2 = summary
    On Error GoTo 0
    If auditMode = "Warn" Then
        AuditConfiguredFonts = True
    Else
        AuditConfiguredFonts = (missingCount = 0 Or allowFallback) And _
                               (driftCount = 0 Or repair)
    End If
End Function

Private Sub ValidateFontPolicyRuntime()
    Dim valid As Boolean
    Dim ignored As String
    Dim value As Variant
    Dim probe As Range
    Dim originalFont As String

    For Each value In Array("Off", "Warn", "RepairAndWarn", "Strict")
        If NormalizedFontAuditMode(CStr(value), valid) <> CStr(value) Or _
           Not valid Then GoTo InvalidPolicy
    Next value
    If NormalizedFontAuditMode( _
           U("062E0627064506480634"), valid) <> "Off" Or _
       Not valid Then GoTo InvalidPolicy
    If NormalizedFontAuditMode( _
           U("06470634062F06270631"), valid) <> "Warn" Or _
       Not valid Then GoTo InvalidPolicy
    If NormalizedFontAuditMode( _
           U("062A0631064506CC064500200648002006470634062F06270631"), _
           valid) <> "RepairAndWarn" Or Not valid Then GoTo InvalidPolicy
    If NormalizedFontAuditMode( _
           U("0633062E062A06AF06CC0631062706460647"), valid) <> _
           "Strict" Or Not valid Then GoTo InvalidPolicy
    ignored = NormalizedFontAuditMode("invalid", valid)
    If valid Then GoTo InvalidPolicy
    If Not NormalizedYesNo("Yes", valid) Or Not valid Then _
        GoTo InvalidPolicy
    If NormalizedYesNo("No", valid) Or Not valid Then GoTo InvalidPolicy
    If Not NormalizedYesNo(U("062806440647"), valid) Or _
       Not valid Then GoTo InvalidPolicy
    If NormalizedYesNo(U("062E06CC0631"), valid) Or _
       Not valid Then GoTo InvalidPolicy
    Call NormalizedYesNo("invalid", valid)
    If valid Then GoTo InvalidPolicy
    If NamedText("PersianFont", vbNullString) <> DEFAULT_PERSIAN_FONT Or _
       NamedText("LatinFont", vbNullString) <> DEFAULT_LATIN_FONT Then _
        GoTo InvalidPolicy
    If Not NamedYesNo("PriceDisplayFaNum", valid) Or Not valid Or _
       PriceDisplayFontName(DEFAULT_LATIN_FONT) <> DEFAULT_FANUM_FONT Then _
        GoTo InvalidPolicy
    If FontAvailable("__PatrisExportDefinitelyMissingFont__") Then _
        GoTo InvalidPolicy

    Set probe = ConfigSheet().Range("G54")
    originalFont = CStr(probe.Font.Name)
    probe.Font.Name = "Arial"
    If AuditRangeFont(probe, DEFAULT_LATIN_FONT, True) <> 1 Or _
       StrComp(CStr(probe.Font.Name), DEFAULT_LATIN_FONT, _
               vbTextCompare) <> 0 Then GoTo InvalidPolicy
    probe.Font.Name = originalFont
    Exit Sub
InvalidPolicy:
    On Error Resume Next
    If Not probe Is Nothing Then probe.Font.Name = originalFont
    On Error GoTo 0
    Err.Raise vbObjectError + 232, "ValidateFontPolicyRuntime", _
              T("font_policy_invalid")
End Sub

Private Function PriceSheet() As Worksheet
    Set PriceSheet = ThisWorkbook.Worksheets( _
        U("0645062D0635064806440627062A"))
End Function

Private Function ConfigSheet() As Worksheet
    Set ConfigSheet = ThisWorkbook.Worksheets(U("062A0646063806CC06450627062A"))
End Function

Private Function SyncSheet() As Worksheet
    Set SyncSheet = ThisWorkbook.Worksheets( _
        U("062F0627062F0647200C0647062706CC00200647064506AF06270645200C06330627063206CC"))
End Function

Private Sub ValidateUnicodeRuntime()
    Dim expected As String

    expected = U("0647064506AF06270645")
    If Len(expected) <> 5 Or AscW(Left$(expected, 1)) <> &H647 Then
        Err.Raise vbObjectError + 190, "ValidateUnicodeRuntime", _
                  "Unicode runtime validation failed."
    End If
End Sub

Private Sub ValidateRoundingRuntime()
    If Application.WorksheetFunction.Round(123449#, -2) <> 123400# Or _
       Application.WorksheetFunction.Round(123450#, -2) <> 123500# Or _
       Application.WorksheetFunction.Round(123456#, -2) <> 123500# Then
        Err.Raise vbObjectError + 208, "ValidateRoundingRuntime", _
                  "Excel ROUND does not implement the required half-up policy."
    End If
End Sub

Private Function ConfirmUnicodeMessage(ByVal message As String, _
                                       ByVal title As String) As Long
    ConfirmUnicodeMessage = MessageBoxW( _
        Application.hWnd, StrPtr(message), StrPtr(title), _
        CLng(vbQuestion Or vbYesNo) Or MB_RIGHT Or MB_RTLREADING)
End Function

Private Function T(ByVal key As String) As String
    Select Case key
        Case "sync_done"
            T = U("0647064506AF06270645200C06330627063206CC002006A906270645064400200634062F002E")
        Case "unknown"
            T = U("0646062706450634062E0635")
        Case "enabled"
            T = U("0641063906270644")
        Case "disabled"
            T = U("063A06CC06310641063906270644")
        Case "allowed"
            T = U("0645062C06270632")
        Case "not_allowed"
            T = U("063A06CC06310645062C06270632")
        Case "published"
            T = U("06270646062A063406270631")
        Case "draft"
            T = U("067E06CC0634200C0646064806CC0633")
        Case "not_run"
            T = U("0628062F0648064600200627062C06310627")
        Case "sync_failed"
            T = U("0647064506AF06270645200C06330627063206CC002006270646062C06270645002006460634062F003A")
        Case "sync_retry"
            T = U("06270631062A062806270637002006280627002006330631064806CC06330020064206CC0645062A200C06AF06300627063106CC002006A9062706450644002006460634062F002E00200644063706410627064B002006860646062F00200644062D063806470020062F06CC06AF06310020062F064806280627063106470020062A064406270634002006A9064606CC062F002E")
        Case "button_ok"
            T = U("0628062706340647")
        Case "button_yes"
            T = U("062806440647")
        Case "button_no"
            T = U("062E06CC0631")
        Case "sync_no_issues"
            T = U("0647064506AF06270645200C06330627063206CC002006A906270645064400200634062F061B00200645063406A9064406CC002006CC06270641062A002006460634062F002E")
        Case "sync_title"
            T = U("0647064506AF06270645200C06330627063206CC0020064406CC0633062A0020064206CC0645062A")
        Case "patris_rows"
            T = U("0631062F06CC0641002006A9062706440627")
        Case "woo_products"
            T = U("06A906270644062706CC00200648064806A90627064506310633")
        Case "matched"
            T = U("0647064506270647064606AF")
        Case "price_matched"
            T = U("064206CC0645062A00200647064506270647064606AF")
        Case "source_only"
            T = U("0641064206370020063306270645062706460647002006A9062706440627")
        Case "woo_only"
            T = U("06410642063700200648064806A90627064506310633")
        Case "over_limit"
            T = U("06A906270644062700200628062700200627062E062A0644062706410020062806CC06340020062706320020062D062F00200645062C06270632")
        Case "stale_rate"
            T = U("06460631062E002006270631063200200642062F06CC064506CC002006270633062A")
        Case "preview_ready"
            T = U("067E06CC0634200C06460645062706CC06340020062206450627062F0647002006270633062A002E")
        Case "preview_failed"
            T = U("067E06CC0634200C06460645062706CC0634002006270646062C06270645002006460634062F003A")
        Case "apply_title"
            T = U("062706390645062706440020062A063A06CC06CC06310627062A0020062A062306CC06CC062F0634062F0647")
        Case "apply_confirm"
            T = U("062206CC06270020062A063A06CC06CC06310627062A0020067E06CC0634200C06460645062706CC0634200C0634062F06470020062F063100200633062706CC062A002006270639064506270644002006340648062F061F")
        Case "apply_done"
            T = U("062A063A06CC06CC06310627062A00200627063906450627064400200634062F002006480020064206CC0645062A200C0647062706CC00200648064806A9062706450631063300200628062706320645062D0627063306280647002006480020062806270632062E06480627064606CC00200634062F0646062F002E")
        Case "preview_first"
            T = U("06270628062A062F06270020067E06CC0634200C06460645062706CC06340020062A063A06CC06CC06310627062A00200631062700200627062C06310627002006A9064606CC062F002E")
        Case "bridge_missing"
            T = U("067E064400200627064506460020064206CC0645062A200C06AF06300627063106CC0020062F06310020062F0633062A063106330020064606CC0633062A002E")
        Case "invalid_workbook"
            T = U("064206270644062800200641062706CC0644002006450639062A062806310020064606CC0633062A002E")
        Case "source_sync_failed"
            T = U("0647064506AF06270645200C06330627063206CC002006450646062806390020062F0627062F0647002006270646062C06270645002006460634062F002E")
        Case "source_changed"
            T = U("062F0627062F0647200C0647062706CC0020067E0627062A063106CC063300200647064606AF0627064500200647064506AF06270645200C06330627063206CC0020062A063A06CC06CC0631002006A90631062F061B002006280627063206CC0627062806CC0020062E0648062F06A906270631002006A9062706450644002006460634062F002E0020062F064806280627063106470020062A064406270634002006A9064606CC062F002E")
        Case "snapshot_changed"
            T = U("062F0627062F0647200C0647062706CC0020064206CC0645062A0020062F06310020062D06CC064600200647064506AF06270645200C06330627063206CC0020062A063A06CC06CC0631002006A90631062F002E002006280631062706CC0020062D06410638002006330627063206AF0627063106CC060C0020062F0627062F0647002006460627064206350020064806270631062F002006460634062F061B0020062F064806280627063106470020062A064406270634002006A9064606CC062F002E")
        Case "price_missing"
            T = U("064206CC0645062A00200645062D0627063306280647002006460634062F")
        Case "woo_missing"
            T = U("064106270642062F0020064206CC0645062A00200648064806A90627064506310633")
        Case "status_stale"
            T = U("06470634062F06270631003A002006460631062E002006270631063200200642062F06CC064506CC")
        Case "status_drift"
            T = U("06470634062F06270631003A00200627062E062A0644062706410020062806CC0634002006270632002006F7066A")
        Case "status_sync"
            T = U("064606CC0627063200200628064700200647064506AF06270645200C06330627063206CC")
        Case "status_sale"
            T = U("06410631064806340020064806CC0698064700200641063906270644")
        Case "search_title"
            T = U("062C0633062A200C0648062C064806CC00200645062D063506480644")
        Case "search_missing"
            T = U("0645062D06350648064406CC00200628062700200639062806270631062A0020064806270631062F0020067E06CC062F0627002006460634062F002E")
        Case "search_button"
            T = U("067E06CC062F0627002006A90631062F0646")
        Case "preserved_price_missing_weight"
            T = U("064806320646002006A906270644062700200646062706450648062C0648062F002006270633062A061B0020064206CC0645062A00200633062706CC062A0020062D0641063800200634062F002E")
        Case "preserved_price_missing_purchase"
            T = U("064206CC0645062A0020062E063106CC062F00200646062706450648062C0648062F002006270633062A061B0020064206CC0645062A00200633062706CC062A0020062D0641063800200634062F002E")
        Case "preserved_price_incomplete"
            T = U("062706370644062706390627062A0020064206CC0645062A200C06AF06300627063106CC00200646062706420635002006270633062A061B0020064206CC0645062A00200633062706CC062A0020062D0641063800200634062F002E")
        Case "price_unavailable_incomplete"
            T = U("062706370644062706390627062A0020064206CC0645062A200C06AF06300627063106CC00200646062706420635002006270633062A061B0020064206CC0645062A002006460645062706CC06340020062F0627062F064700200646064506CC200C06340648062F002E")
        Case "woo_only_preserved_price"
            T = U("062706CC0646002006A906270644062700200641064206370020062F063100200648064806A90627064506310633002006450648062C0648062F002006270633062A061B0020064206CC0645062A00200633062706CC062A00200628062F0648064600200645062D062706330628064700200645062D064406CC002006460645062706CC06340020062F0627062F06470020064506CC200C06340648062F002E")
        Case "woo_only_price_unavailable"
            T = U("062706CC0646002006A906270644062700200641064206370020062F063100200648064806A90627064506310633002006450648062C0648062F002006270633062A002006480020064206CC0645062A00200642062706280644002006460645062706CC063400200646062F0627062F002E")
        Case "ambiguous_woo_match"
            T = U("062A0637062806CC0642002006A9062F002006A90627064406270020062F063100200648064806A9062706450631063300200645062806470645002006270633062A061B0020064206CC0645062A00200633062706CC062A002006280631062706CC0020062706CC064600200631062F06CC06410020062D0641063800200634062F0647002006480020062706390645062706440020062A063A06CC06CC06310627062A002006450633062F0648062F002006270633062A002E")
        Case "orphan_woo_variation"
            T = U("062A06460648063900200648064806A9062706450631063300200628062F0648064600200645062D063506480644002006450627062F0631002006270633062A061B0020062706390645062706440020062A063A06CC06CC06310627062A002006450633062F0648062F002006270633062A002E")
        Case "identity_warning"
            T = U("06470634062F0627063100200647064806CC062A06CC003A0020062A0637062806CC064200200648064806A9062706450631063300200645062806470645002006CC062700200646062706420635002006270633062A061B0020062706390645062706440020062A063A06CC06CC06310627062A002006450633062F0648062F00200634062F002E")
        Case "proposal_drift"
            T = U("06470634062F06270631003A0020064506420627062F06CC06310020067E06CC0634064606470627062F06CC0020062706CC064600200641062706CC064400200628062700200633062706CC062A002006CC06A90633062706460020064606CC0633062A002E")
        Case "proposal_drift_critical"
            T = U("06470634062F0627063100200628062D06310627064606CC003A00200627062E062A064406270641002006CC06A906CC002006270632002006460631062E200C06470627060C0020062D06450644002006CC06270020062D0627063406CC0647002006330648062F0020062806CC0634002006270632002006F7066A002006270633062A002E")
        Case "projection_integrity_warning"
            T = U("0647064506AF06270645200C06330627063206CC00200645062A06480642064100200634062F003A002006CC06A9067E06270631068606AF06CC002006460648063900200645062D0635064806440627062A00200648064806A906270645063106330020062A062306CC06CC062F002006460634062F002E")
        Case "projection_integrity_blocked"
            T = U("0647064506AF06270645200C06330627063206CC00200645062A06480642064100200634062F061B002006CC06A9067E06270631068606AF06CC00200641064706310633062A002006A9062706440627064706270020062A062306CC06CC062F002006460634062F002E00200644063706410627064B002006450646062806390020062F0627062F06470020062806310631063306CC002006340648062F002E")
        Case "pricing_service_unavailable"
            T = U("06330631064806CC063300200647064506AF06270645200C06330627063206CC0020064206CC0645062A0020062F06310020062F0633062A063106330020064606CC0633062A002E00200644063706410627064B0020062F064806280627063106470020062A064406270634002006A9064606CC062F002E")
        Case "row_kind_matched"
            T = U("0647064506270647064606AF")
        Case "row_kind_source_only"
            T = U("0641064206370020063306270645062706460647002006A9062706440627")
        Case "row_kind_woo_only"
            T = U("06410642063700200648064806A90627064506310633")
        Case "row_kind_ambiguous"
            T = U("0645062806470645")
        Case "settings_required"
            T = U("0647064506470020064506420627062F06CC06310020064206CC0645062A200C06AF06300627063106CC00200628062706CC062F002006450639062A0628063100200648002006A906270645064400200628062706340646062F002E")
        Case "rounding_required"
            T = U("062A0639062F0627062F0020063106420645002006AF0631062F06A90631062F06460020064206CC0645062A00200628062706CC062F00200639062F062F06CC00200635064106310020062A0627002006F90020062806270634062F002E")
        Case "xlsx_filter"
            T = U("06460633062E064700200628062F0648064600200645062706A90631064800200028002A002E0078006C007300780029")
        Case "xlsm_filter"
            T = U("06460633062E06470020062F06270631062706CC00200645062706A90631064800200028002A002E0078006C0073006D0029")
        Case "save_title"
            T = U("0630062E06CC06310647002006460633062E0647")
        Case "save_nomacro_done"
            T = U("06460633062E064700200628062F0648064600200645062706A90631064800200630062E06CC0631064700200634062F002E")
        Case "save_extension"
            T = U("067E063306480646062F00200641062706CC064400200628062706CC062F00200078006C00730078002006CC062700200078006C0073006D0020062806270634062F002E")
        Case "save_overwrite"
            T = U("0641062706CC0644002006450648062C0648062F002006270633062A002E0020062C062706CC06AF063206CC0646002006340648062F061F")
        Case "template_save_copy"
            T = U("06280631062706CC0020062D064106380020062F0627062F0647200C0647062706CC002006320646062F0647060C00200641062706CC064400200627064406AF064800200631064806CC0020062F06CC063306A900200630062E06CC0631064700200646064506CC200C06340648062F061B002006270632002000AB0630062E06CC06310647002006280647200C0639064606480627064600BB002006280631062706CC002006460633062E06470020062E06310648062C06CC002006270633062A06410627062F0647002006A9064606CC062F002E")
        Case "template_close_wait"
            T = U("064206CC0645062A0020062F06310020062D0627064400200647064506AF06270645200C06330627063206CC002006280627002006480628200C0633062706CC062A002006270633062A061B0020067E06330020062706320020067E062706CC0627064600200647064506AF06270645200C06330627063206CC0020062F0648062806270631064700200641062706CC06440020063106270020062806280646062F06CC062F002E")
        Case "font_missing"
            T = U("0642064406450020067E06CC06A9063106280646062F06CC200C0634062F0647002006CC06270641062A002006460634062F")
        Case "font_drift"
            T = U("0645063A062706CC0631062A0020064206440645")
        Case "font_no_issues"
            T = U("064706CC068600200645063406A90644002006420644064506CC002006CC06270641062A002006460634062F002E")
        Case "font_audit_title"
            T = U("0645064506CC063206CC0020064206440645")
        Case "font_policy_invalid"
            T = U("063306CC06270633062A002006420644064500200646062706450639062A06280631002006270633062A002E")
        Case Else
            T = key
    End Select
End Function

Private Function U(ByVal hexCodePoints As String) As String
    Dim position As Long
    Dim codePoint As Long

    If Len(hexCodePoints) Mod 4 <> 0 Then
        Err.Raise vbObjectError + 191, "U", "Invalid Unicode literal."
    End If
    For position = 1 To Len(hexCodePoints) Step 4
        codePoint = CLng("&H" & Mid$(hexCodePoints, position, 4))
        If codePoint > 32767 Then codePoint = codePoint - 65536
        U = U & ChrW$(codePoint)
    Next position
End Function

Private Function BlankIfNull(ByVal value As Variant) As Variant
    If IsNull(value) Or IsEmpty(value) Then
        BlankIfNull = Empty
    Else
        BlankIfNull = value
    End If
End Function
