Attribute VB_Name = "ProductCatalogSync"
Option Explicit

Private Const PRODUCTS_TABLE As String = "Products"
Private Const SYNC_TABLE As String = "SyncData"
Private Const YUAN_TABLE As String = "Yuan_Price"
Private Const SHIPPING_TABLE As String = "Shipping"
Private Const PROFIT_TABLE As String = "Profit"
Private Const PRODUCT_COLUMN_COUNT As Long = 10
Private Const SYNC_COLUMN_COUNT As Long = 20
Private Const STATE_PAGE_SIZE As Long = 250
Private Const MAX_STATE_PAGES As Long = 8
Private Const STATE_SNAPSHOT_RETRIES As Long = 3
Private Const HTTP_TIMEOUT_MS As Long = 150000
Private Const PRICING_HTTP_TIMEOUT_MS As Long = 600000
Private Const MAX_PRICING_RESPONSE_CHARS As Long = 4194304
Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"
Private Const PRICING_CLIENT_ID As String = "digitalogic-price-calculator/v1"
Private Const PRICING_CONTRACT_CLIENT_ID As String = "digitalogic-price-calculator"
Private Const PRICING_CONTRACT_CHANNEL As String = "excel-workbook"
Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"
Private Const PRICING_REQUEST_SCHEMA As String = "patris.excel-pricing-companion-request/v1"
Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"
Private Const PRICE_ROUNDING_MODE As String = "nearest_half_up"
Private Const LOOPBACK_PREFIX As String = "http://127.0.0.1:18080/"
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

Private mSourceID As String
Private mSourceDataset As String
Private mSourceRevision As String
Private mLastPreviewDigest As String
Private mLastPreviewExpiresAt As String
Private mLastPreviewStateRevision As String
Private mLastPreviewSettings As String
Private mLastApplyRequestID As String
Private mProposalSyncActive As Boolean
Private mLastRefreshSucceeded As Boolean
Private mSaveFlowActive As Boolean

#If VBA7 Then
Private Declare PtrSafe Function MessageBoxW Lib "user32" ( _
    ByVal windowHandle As LongPtr, _
    ByVal messagePointer As LongPtr, _
    ByVal titlePointer As LongPtr, _
    ByVal messageType As Long) As Long
#Else
Private Declare Function MessageBoxW Lib "user32" ( _
    ByVal windowHandle As Long, _
    ByVal messagePointer As Long, _
    ByVal titlePointer As Long, _
    ByVal messageType As Long) As Long
#End If

Public Sub ValidateWorkbook()
    Dim table As ListObject
    Dim syncTable As ListObject

    ValidateUnicodeRuntime
    ValidateProposalDateNormalization
    ValidateProjectionIntegrityGuard
    ValidateRoundingRuntime
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
End Sub

Public Function RefreshAllDataForValidation() As Boolean
    RefreshAllData True
    RefreshAllDataForValidation = mLastRefreshSucceeded
End Function

Public Sub HandleWorkbookBeforeSave(ByVal saveAsUI As Boolean, _
                                    ByRef cancel As Boolean)
    Dim selectedPath As Variant
    Dim outputPath As String
    Dim extension As String
    Dim fileSystem As Object
    Dim answer As Long

    If mSaveFlowActive Or Not saveAsUI Then Exit Sub
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
        ShowUnicodeMessage T("save_extension"), vbExclamation, T("save_title")
        Exit Sub
    End If

    Set fileSystem = CreateObject("Scripting.FileSystemObject")
    If fileSystem.FileExists(outputPath) Then
        answer = ShowUnicodeMessage( _
            T("save_overwrite"), vbQuestion Or vbYesNo, T("save_title"))
        If answer <> vbYes Then Exit Sub
    End If

    If extension = ".xlsx" Then
        ExportMacroFreeCopy outputPath
        ShowUnicodeMessage T("save_nomacro_done"), vbInformation, T("save_title")
    Else
        mSaveFlowActive = True
        On Error GoTo SaveFailed
        ThisWorkbook.SaveAs Filename:=outputPath, _
            FileFormat:=xlOpenXMLWorkbookMacroEnabled
        mSaveFlowActive = False
    End If
    Exit Sub

SaveFailed:
    mSaveFlowActive = False
    Err.Raise Err.Number, "HandleWorkbookBeforeSave", Err.Description
End Sub

Private Sub ExportMacroFreeCopy(ByVal outputPath As String)
    Dim copyBook As Workbook
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

    ThisWorkbook.Worksheets.Copy
    Set copyBook = ActiveWorkbook
    RemoveMacroOnlyUI copyBook
    copyBook.SaveAs Filename:=outputPath, FileFormat:=xlOpenXMLWorkbook
    copyBook.Close SaveChanges:=False
    Set copyBook = Nothing

CleanExit:
    Application.DisplayAlerts = previousAlerts
    Application.EnableEvents = previousEvents
    Application.ScreenUpdating = previousScreenUpdating
    If savedErrorNumber <> 0 Then
        Err.Raise savedErrorNumber, "ExportMacroFreeCopy", _
                  savedErrorDescription
    End If
    Exit Sub

Failed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    On Error Resume Next
    If Not copyBook Is Nothing Then copyBook.Close SaveChanges:=False
    Set copyBook = Nothing
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
    book.Worksheets(1).Range("B3:K3").ClearContents
    book.Worksheets(1).ListObjects(PRODUCTS_TABLE). _
        DataBodyRange.FormatConditions.Delete
    book.Names("ProductSearchQuery").Delete
    book.Names("SelectedProductRow").Delete
    On Error GoTo 0
End Sub

Public Sub RefreshAllData(Optional ByVal silent As Boolean = False)
    Dim previousCalculation As XlCalculation
    Dim contract As JsonValue
    Dim reconciledRows As Object
    Dim productRows As Long
    Dim reconciledRowsFetched As Long
    Dim wooRows As Long
    Dim parity As Variant
    Dim statusText As String
    Dim settings As Worksheet
    Dim pricingStateSnapshot As Variant
    Dim pricingStateSnapshotCaptured As Boolean
    Dim savedErrorDescription As String

    On Error GoTo Failed
    mLastRefreshSucceeded = False
    previousCalculation = Application.Calculation
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual

    Set settings = ConfigSheet()
    pricingStateSnapshot = CapturePricingStateSnapshot(settings)
    pricingStateSnapshotCaptured = True
    InvalidatePricingPreview
    settings.Range("G31:G47").ClearContents
    settings.Range("G31").Value2 = False
    Set contract = LoadPatrisContract()
    ReadSourceIdentity contract
    Set reconciledRows = CreateObject("Scripting.Dictionary")
    reconciledRows.CompareMode = vbBinaryCompare
    reconciledRowsFetched = RefreshPricingState(reconciledRows)
    productRows = ImportReconciledCatalog(reconciledRows)
    If productRows <> reconciledRowsFetched Then
        Err.Raise vbObjectError + 115, "RefreshAllData", T("invalid_workbook")
    End If
    wooRows = CLng(Val(CStr(settings.Range("G32").Value2)))
    Application.CalculateFullRebuild
    parity = PriceParitySummary()

    statusText = CStr(productRows) & " " & T("patris_rows") & U("061B") & " " & _
        CStr(wooRows) & " " & T("woo_products") & U("061B") & " " & _
        CStr(settings.Range("G34").Value2) & " " & T("matched") & U("061B") & " " & _
        CStr(settings.Range("G35").Value2) & " " & T("source_only") & U("061B") & " " & _
        CStr(settings.Range("G36").Value2) & " " & T("woo_only") & U("061B") & " " & _
        CStr(parity(0)) & " " & T("price_matched") & U("061B") & " " & _
        CStr(parity(1)) & " " & T("over_limit")
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

    If Not silent Then
        ShowUnicodeMessage T("sync_done") & vbCrLf & statusText, _
            vbInformation, T("sync_title")
    End If

CleanExit:
    Application.Calculation = previousCalculation
    Application.EnableEvents = True
    Application.ScreenUpdating = True
    Exit Sub

Failed:
    mLastRefreshSucceeded = False
    savedErrorDescription = Err.Description
    statusText = T("sync_failed") & " " & _
        SafeStatusError(savedErrorDescription)
    On Error Resume Next
    If pricingStateSnapshotCaptured Then
        RestorePricingStateSnapshot settings, pricingStateSnapshot
    End If
    If Not settings Is Nothing Then settings.Range("B6").Value = statusText
    On Error GoTo 0
    If Not silent Then
        ShowUnicodeMessage statusText, vbExclamation, T("sync_title")
    End If
    Resume CleanExit
End Sub

Public Sub RefreshOnOpen()
    If Trim$(CStr(ConfigSheet().Range("B5").Value2)) = U("062806440647") Then
        RefreshAllData True
    End If
End Sub

Public Sub RegisterSearchHotkey()
    Dim workbookMacro As String

    workbookMacro = "'" & Replace(ThisWorkbook.Name, "'", "''") & _
        "'!ProductCatalogSync.FocusProductSearch"
    Application.OnKey "{F2}", workbookMacro
End Sub

Public Sub UnregisterSearchHotkey()
    Application.OnKey "{F2}"
End Sub

Public Sub FocusProductSearch()
    Dim searchInput As Range
    Dim table As ListObject

    On Error GoTo CleanExit
    PriceSheet().Activate
    Set searchInput = ThisWorkbook.Names("ProductSearchQuery").RefersToRange
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    searchInput.Select
    ActiveWindow.ScrollColumn = table.Range.Column
CleanExit:
End Sub

Public Sub SearchProducts()
    Dim table As ListObject
    Dim query As String
    Dim found As Range
    Dim anchor As Range
    Dim rowIndex As Long

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    query = Trim$(CStr( _
        ThisWorkbook.Names("ProductSearchQuery").RefersToRange.Value2))
    If Len(query) = 0 Then
        FocusProductSearch
        Exit Sub
    End If
    If table.DataBodyRange Is Nothing Then Exit Sub

    Set found = table.DataBodyRange.Find( _
        What:=query, _
        After:=table.DataBodyRange.Cells(table.DataBodyRange.Cells.Count), _
        LookIn:=xlValues, _
        LookAt:=xlPart, _
        SearchOrder:=xlByRows, _
        SearchDirection:=xlNext, _
        MatchCase:=False)
    If found Is Nothing Then
        ShowUnicodeMessage T("search_missing"), vbInformation, T("search_title")
    Else
        rowIndex = found.Row - table.DataBodyRange.Row + 1
        Set anchor = table.DataBodyRange.Cells(rowIndex, 1)
        PriceSheet().Activate
        Application.Goto anchor, False
        HighlightSelectedProductRow anchor
        ActiveWindow.ScrollColumn = table.Range.Column
        ActiveWindow.ScrollRow = Application.Max(1, anchor.Row - 3)
    End If
End Sub

Public Sub ClearProductSearch()
    Dim table As ListObject

    On Error Resume Next
    ThisWorkbook.Names("ProductSearchQuery").RefersToRange.ClearContents
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If table.AutoFilter.FilterMode Then table.AutoFilter.ShowAllData
    FocusProductSearch
    On Error GoTo 0
End Sub

Public Sub HighlightSelectedProductRow(ByVal target As Range)
    Dim table As ListObject
    Dim selectedRow As Long

    On Error GoTo CleanExit
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    If Not table.DataBodyRange Is Nothing Then
        If Not Intersect(target.Cells(1, 1), table.DataBodyRange) Is Nothing Then
            selectedRow = target.Row
        End If
    End If
    Application.EnableEvents = False
    ConfigSheet().Range("G30").Value2 = selectedRow
CleanExit:
    Application.EnableEvents = True
End Sub

Public Sub HandlePricingProposalChanged()
    If mProposalSyncActive Then Exit Sub

    On Error GoTo CleanExit
    mProposalSyncActive = True
    InvalidatePricingPreview
    PreviewPricingChangesCore False
    If Len(mLastPreviewDigest) > 0 Then ApplyPricingChanges

CleanExit:
    mProposalSyncActive = False
End Sub

Public Sub PreviewPricingChanges()
    PreviewPricingChangesCore True
End Sub

Private Sub PreviewPricingChangesCore(ByVal showMessage As Boolean)
    Dim settings As Worksheet
    Dim requestID As String
    Dim requestBody As String
    Dim responseText As String
    Dim root As JsonValue
    Dim result As JsonValue
    Dim statusText As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    InvalidatePricingPreview
    EnsureSourceIdentity
    requestID = NewRequestID("preview")
    requestBody = BuildPricingRequest("preview", requestID, vbNullString, False)
    responseText = HttpJson( _
        PricingEndpoint("preview"), requestBody, requestID, _
        Trim$(CStr(settings.Range("G14").Value2)))
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)

    mLastPreviewDigest = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "preview_digest"))))
    mLastPreviewExpiresAt = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "expires_at"))))
    mLastPreviewStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "state_revision"))))
    mLastPreviewSettings = PricingSettingsCanonical()
    statusText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "status"))))
    If Len(mLastPreviewDigest) = 0 Then
        Err.Raise vbObjectError + 160, "PreviewPricingChanges", _
                  T("preview_failed")
    End If

    settings.Range("G26").Value2 = mLastPreviewDigest
    settings.Range("G27").Value2 = mLastPreviewExpiresAt
    statusText = T("preview_ready") & " " & statusText & _
        WarningSummary(result)
    settings.Range("B23").Value2 = statusText
    If showMessage Then
        ShowUnicodeMessage CStr(settings.Range("B23").Value2), _
            vbInformation, T("apply_title")
    End If
    Exit Sub

Failed:
    InvalidatePricingPreview
    statusText = T("preview_failed") & " " & SafeStatusError(Err.Description)
    On Error Resume Next
    ConfigSheet().Range("B23").Value2 = statusText
    On Error GoTo 0
    ShowUnicodeMessage statusText, vbExclamation, T("apply_title")
End Sub

Public Sub ApplyPricingChanges()
    Dim settings As Worksheet
    Dim requestID As String
    Dim requestBody As String
    Dim responseText As String
    Dim root As JsonValue
    Dim result As JsonValue
    Dim statusText As String
    Dim answer As Long
    Dim savedPreviewDigest As String
    Dim savedPreviewExpiresAt As String
    Dim savedPreviewStateRevision As String
    Dim savedPreviewSettings As String
    Dim savedApplyRequestID As String
    Dim appliedStateRevision As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    If Len(mLastPreviewDigest) = 0 Then
        ShowUnicodeMessage T("preview_first"), vbExclamation, T("apply_title")
        Exit Sub
    End If
    If mLastPreviewSettings <> PricingSettingsCanonical() Then
        InvalidatePricingPreview
        ShowUnicodeMessage T("preview_first"), vbExclamation, T("apply_title")
        Exit Sub
    End If
    If Len(mLastApplyRequestID) = 0 And _
       mLastPreviewStateRevision <> _
           Trim$(CStr(settings.Range("G14").Value2)) Then
        InvalidatePricingPreview
        ShowUnicodeMessage T("preview_first"), vbExclamation, T("apply_title")
        Exit Sub
    End If

    answer = ShowUnicodeMessage( _
        T("apply_confirm") & vbCrLf & vbCrLf & _
        CStr(settings.Range("B23").Value2), _
        vbQuestion Or vbYesNo, T("apply_title"))
    If answer <> vbYes Then Exit Sub

    EnsureSourceIdentity
    If Len(mLastApplyRequestID) = 0 Then
        mLastApplyRequestID = NewRequestID("apply")
        settings.Range("G28").Value2 = mLastApplyRequestID
    End If
    requestID = mLastApplyRequestID
    savedPreviewDigest = mLastPreviewDigest
    savedPreviewExpiresAt = mLastPreviewExpiresAt
    savedPreviewStateRevision = mLastPreviewStateRevision
    savedPreviewSettings = mLastPreviewSettings
    savedApplyRequestID = mLastApplyRequestID
    requestBody = BuildPricingRequest( _
        "apply", requestID, mLastPreviewDigest, True, _
        mLastPreviewStateRevision)
    responseText = HttpJson( _
        PricingEndpoint("apply"), requestBody, requestID, _
        mLastPreviewStateRevision)
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)
    appliedStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "state_revision"))))
    If Len(appliedStateRevision) = 0 Then
        Err.Raise vbObjectError + 161, "ApplyPricingChanges", _
                  T("sync_failed")
    End If
    statusText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "status"))))
    statusText = T("apply_done") & " " & statusText & _
        WarningSummary(result)

    RefreshAllData True
    If Not mLastRefreshSucceeded Or _
       Trim$(CStr(ConfigSheet().Range("G14").Value2)) <> _
           appliedStateRevision Then
        Err.Raise vbObjectError + 162, "ApplyPricingChanges", _
                  T("sync_failed")
    End If
    settings.Range("B23").Value2 = statusText
    ShowUnicodeMessage CStr(settings.Range("B23").Value2), _
        vbInformation, T("apply_title")
    Exit Sub

Failed:
    statusText = SafeStatusError(Err.Description)
    On Error Resume Next
    If Len(savedPreviewDigest) > 0 And _
       PricingSettingsCanonical() = savedPreviewSettings Then
        mLastPreviewDigest = savedPreviewDigest
        mLastPreviewExpiresAt = savedPreviewExpiresAt
        mLastPreviewStateRevision = savedPreviewStateRevision
        mLastPreviewSettings = savedPreviewSettings
        mLastApplyRequestID = savedApplyRequestID
        ConfigSheet().Range("G26").Value2 = savedPreviewDigest
        ConfigSheet().Range("G27").Value2 = savedPreviewExpiresAt
        ConfigSheet().Range("G28").Value2 = savedApplyRequestID
    End If
    ConfigSheet().Range("B23").Value2 = statusText
    On Error GoTo 0
    ShowUnicodeMessage statusText, vbExclamation, T("apply_title")
End Sub

Private Sub InvalidatePricingPreview()
    mLastPreviewDigest = vbNullString
    mLastPreviewExpiresAt = vbNullString
    mLastPreviewStateRevision = vbNullString
    mLastPreviewSettings = vbNullString
    mLastApplyRequestID = vbNullString

    On Error Resume Next
    ConfigSheet().Range("G26:G28").ClearContents
    On Error GoTo 0
End Sub

Private Function PricingStateAddresses() As Variant
    PricingStateAddresses = Split( _
        "B10|B11|B12|B13|B14|B15|B18|B19|B20|B21|B22|B26|" & _
        "G14|G15|G18|G19|G20|G21|G22|G31|G32|G33|G34|G35|G36|" & _
        "G37|G38|G39|G40|G41|G42|G43|G44|G45|G46|G47|" & _
        "H14|H15|H16|H17|H18|H19", _
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
    Dim index As Long

    addresses = PricingStateAddresses()
    If LBound(values) <> LBound(addresses) Or _
       UBound(values) <> UBound(addresses) Then
        Err.Raise vbObjectError + 199, _
                  "RestorePricingStateSnapshot", _
                  T("invalid_workbook")
    End If
    For index = LBound(addresses) To UBound(addresses)
        settings.Range(CStr(addresses(index))).Value2 = values(index)
    Next index
End Sub

Private Function PricingSettingsCanonical() As String
    Dim settings As Worksheet

    Set settings = ConfigSheet()
    PricingSettingsCanonical = _
        CanonicalCellText(settings.Range("B18").Value2) & "|" & _
        CanonicalCellText(settings.Range("B19").Value2) & "|" & _
        CanonicalDateText(settings.Range("B20").Value2) & "|" & _
        CanonicalCellText(settings.Range("B21").Value2) & "|" & _
        CanonicalCellText(settings.Range("B22").Value2) & "|" & _
        CanonicalCellText(settings.Range("B26").Value2) & "|" & _
        CanonicalCellText(settings.Range("H14").Value2) & "|" & _
        CanonicalCellText(settings.Range("H15").Value2) & "|" & _
        CanonicalDateText(settings.Range("H16").Value2)
End Function

Private Function LoadPatrisContract() As JsonValue
    Dim endpoint As String
    Dim responseText As String
    Dim root As JsonValue

    endpoint = Trim$(CStr(ConfigSheet().Range("B3").Value2))
    If Not IsAllowedPatrisUrl(endpoint) Then
        Err.Raise vbObjectError + 100, "LoadPatrisContract", T("bridge_missing")
    End If
    responseText = HttpGet( _
        endpoint, _
        "application/vnd.patris.product-sync+json, application/json")
    Set root = JsonRuntime.ParseJson(responseText)
    If root.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(root, "schema")) <> "patris.product-sync" Then
        Err.Raise vbObjectError + 101, "LoadPatrisContract", T("invalid_workbook")
    End If
    Set LoadPatrisContract = root
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
        messageText = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(warning, "message_fa"))))
        If Len(messageText) > 0 Then
            WarningSummary = WarningSummary & vbCrLf & U("2022") & " " & _
                messageText
        End If
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
    Dim contract As JsonValue

    If Len(mSourceID) > 0 And Len(mSourceDataset) > 0 And _
       Len(mSourceRevision) > 0 Then Exit Sub
    Set contract = LoadPatrisContract()
    ReadSourceIdentity contract
End Sub

Private Function RefreshPricingState(ByVal siteRows As Object) As Long
    Dim attempt As Long
    Dim fetchedRows As Long
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String
    Dim settings As Worksheet
    Dim retryStateSnapshot As Variant
    Dim sourceRepairAttempted As Boolean

    Set settings = ConfigSheet()
    retryStateSnapshot = CapturePricingStateSnapshot(settings)
    For attempt = 1 To STATE_SNAPSHOT_RETRIES + 1
        If attempt > STATE_SNAPSHOT_RETRIES And _
           Not sourceRepairAttempted Then Exit For
        siteRows.RemoveAll
        settings.Range("G34:G47").ClearContents
        Err.Clear
        On Error Resume Next
        fetchedRows = RefreshPricingStateOnce(siteRows)
        savedErrorNumber = Err.Number
        savedErrorDescription = Err.Description
        Err.Clear
        On Error GoTo 0
        If savedErrorNumber = 0 Then
            RefreshPricingState = fetchedRows
            Exit Function
        End If
        RestorePricingStateSnapshot settings, retryStateSnapshot
        If savedErrorNumber = vbObjectError + 121 And _
           Not sourceRepairAttempted Then
            sourceRepairAttempted = True
            RepairCanonicalDelivery
        End If
    Next attempt

    If savedErrorNumber = 0 Then savedErrorNumber = vbObjectError + 130
    Err.Raise savedErrorNumber, "RefreshPricingState", savedErrorDescription
End Function

Private Sub RepairCanonicalDelivery()
    Dim responseText As String
    Dim root As JsonValue
    Dim contract As JsonValue
    Dim deliveredRevision As String
    Dim csrfToken As String

    On Error GoTo RepairFailed
    csrfToken = PricingSessionToken()
    responseText = HttpPostJsonRaw( _
        UniversalRefreshURL(), "{""delivery"":""wait""}", csrfToken, "", "")
    Set root = JsonRuntime.ParseJson(responseText)
    If root Is Nothing Or root.Kind <> "object" Then GoTo RepairFailed
    If Not BooleanValue(JsonRuntime.JsonText(root, "refreshed")) Then _
        GoTo RepairFailed
    If Not BooleanValue(JsonRuntime.JsonText(root, "delivered")) Then _
        GoTo RepairFailed
    deliveredRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(root, "source_revision"))))
    If Not IsSHA256RevisionText(deliveredRevision) Then GoTo RepairFailed

    mSourceID = vbNullString
    mSourceDataset = vbNullString
    mSourceRevision = vbNullString
    Set contract = LoadPatrisContract()
    ReadSourceIdentity contract
    If mSourceRevision <> deliveredRevision Then GoTo RepairFailed
    Exit Sub

RepairFailed:
    On Error GoTo 0
    Err.Raise vbObjectError + 149, "RepairCanonicalDelivery", _
              T("source_sync_failed")
End Sub

Private Function RefreshPricingStateOnce(ByVal siteRows As Object) As Long
    Dim page As Long
    Dim requestBody As String
    Dim responseText As String
    Dim root As JsonValue
    Dim state As JsonValue
    Dim catalog As JsonValue
    Dim pagination As JsonValue
    Dim rowsValue As JsonValue
    Dim rowValue As JsonValue
    Dim rowIndex As Long
    Dim pageRows As Long
    Dim identityKey As String
    Dim datasetName As String
    Dim datasetRevision As String
    Dim sourceRevision As String
    Dim pageRevision As String
    Dim columnSignature As String
    Dim countSignature As String
    Dim firstDatasetRevision As String
    Dim firstSourceRevision As String
    Dim firstColumnSignature As String
    Dim firstCountSignature As String
    Dim paginationPage As Long
    Dim paginationLimit As Long
    Dim paginationTotal As Long
    Dim paginationPages As Long
    Dim firstPaginationLimit As Long
    Dim firstPaginationTotal As Long
    Dim firstPaginationPages As Long
    Dim hasMore As Boolean
    Dim completed As Boolean

    For page = 1 To MAX_STATE_PAGES
        requestBody = StateRequestJson(page)
        responseText = HttpJson(PricingEndpoint("state"), requestBody)
        Set root = JsonRuntime.ParseJson(responseText)
        Set state = ResponseData(root)
        RejectProjectionIntegrityWarnings state
        If page = 1 Then ApplyGlobalState state

        Set catalog = JsonRuntime.JsonMember(state, "catalog")
        If catalog Is Nothing Or catalog.Kind <> "object" Then
            Err.Raise vbObjectError + 110, "RefreshPricingState", _
                      T("bridge_missing")
        End If
        datasetName = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(catalog, "dataset"))))
        If datasetName <> "reconciled_products" Then
            Err.Raise vbObjectError + 116, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        datasetRevision = SiteText(catalog, "dataset_revision")
        pageRevision = SiteText(catalog, "page_revision")
        If Not IsSHA256RevisionText(datasetRevision) Or _
           Not IsSHA256RevisionText(pageRevision) Then
            Err.Raise vbObjectError + 120, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        sourceRevision = StateSourceRevision(state, catalog)
        If Not IsSHA256RevisionText(sourceRevision) Then
            Err.Raise vbObjectError + 121, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        columnSignature = CatalogColumnSignature(catalog)
        If columnSignature <> RECONCILED_COLUMN_KEYS Then
            Err.Raise vbObjectError + 122, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        countSignature = CatalogCountSignature(catalog)

        Set rowsValue = JsonRuntime.JsonMember(catalog, "rows")
        If rowsValue Is Nothing Or rowsValue.Kind <> "array" Then
            Err.Raise vbObjectError + 111, "RefreshPricingState", _
                      T("bridge_missing")
        End If

        pageRows = JsonRuntime.JsonArrayCount(rowsValue)
        For rowIndex = 1 To pageRows
            Set rowValue = JsonRuntime.JsonArrayItem(rowsValue, rowIndex)
            identityKey = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(rowValue, "sync_key"))))
            If Len(identityKey) = 0 Then
                Err.Raise vbObjectError + 113, "RefreshPricingState", _
                          T("invalid_workbook")
            End If
            If siteRows.Exists(identityKey) Then
                Err.Raise vbObjectError + 114, "RefreshPricingState", _
                          T("invalid_workbook")
            End If
            siteRows.Add identityKey, rowValue
        Next rowIndex
        Set pagination = JsonRuntime.JsonMember(catalog, "pagination")
        If pagination Is Nothing Or pagination.Kind <> "object" Then
            Err.Raise vbObjectError + 123, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        paginationPage = RequiredWholeNumber(pagination, "page")
        paginationLimit = RequiredWholeNumber(pagination, "limit")
        paginationTotal = RequiredWholeNumber(pagination, "total")
        paginationPages = RequiredWholeNumber(pagination, "pages")
        hasMore = BooleanValue( _
            JsonRuntime.JsonText(pagination, "has_more"))

        If paginationPage <> page Or paginationLimit <> STATE_PAGE_SIZE Or _
           paginationTotal < 1 Or paginationPages < 1 Or _
           paginationPages > MAX_STATE_PAGES Or pageRows > paginationLimit Or _
           hasMore <> (paginationPage < paginationPages) Then
            Err.Raise vbObjectError + 124, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        If page = 1 Then
            firstDatasetRevision = datasetRevision
            firstSourceRevision = sourceRevision
            firstColumnSignature = columnSignature
            firstCountSignature = countSignature
            firstPaginationLimit = paginationLimit
            firstPaginationTotal = paginationTotal
            firstPaginationPages = paginationPages
            ApplyReconciliationCounts catalog
        ElseIf datasetRevision <> firstDatasetRevision Or _
               sourceRevision <> firstSourceRevision Or _
               columnSignature <> firstColumnSignature Or _
               countSignature <> firstCountSignature Or _
               paginationLimit <> firstPaginationLimit Or _
               paginationTotal <> firstPaginationTotal Or _
               paginationPages <> firstPaginationPages Then
            Err.Raise vbObjectError + 130, "RefreshPricingState", _
                      T("invalid_workbook")
        End If
        If paginationTotal <> CLng(Val(CStr( _
           ConfigSheet().Range("G38").Value2))) Then
            Err.Raise vbObjectError + 131, "RefreshPricingState", _
                      T("invalid_workbook")
        End If

        RefreshPricingStateOnce = RefreshPricingStateOnce + pageRows
        If Not hasMore Then
            completed = True
            Exit For
        End If
    Next page
    If Not completed Or _
       RefreshPricingStateOnce <> firstPaginationTotal Or _
       siteRows.Count <> firstPaginationTotal Or _
       RefreshPricingStateOnce <> CLng(Val(CStr( _
           ConfigSheet().Range("G38").Value2))) Then
        Err.Raise vbObjectError + 117, "RefreshPricingState", _
                  T("invalid_workbook")
    End If
    ConfigSheet().Range("G44").Value2 = firstDatasetRevision
    ConfigSheet().Range("G45").Value2 = firstSourceRevision
    ConfigSheet().Range("G46").Value2 = firstPaginationTotal
    ConfigSheet().Range("G47").Value2 = firstCountSignature
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
    settings.Range("G14").Value = BlankIfNull( _
        JsonRuntime.JsonText(state, "state_revision"))
    settings.Range("G15").Value = stale
    settings.Range("H14").Value = shippingCurrency
    settings.Range("H15").Value = shippingRevision
    settings.Range("H16").Value2 = CanonicalDateText(remoteUSDDate)
    settings.Range("H17").Value2 = CanonicalDateText(remoteCNYDate)
    settings.Range("H18").Value = CLng(remoteRounding)
    settings.Range("H19").Value2 = remoteRoundingMode

    UpdateProposalCell settings.Range("B18"), settings.Range("G18"), remoteCNY
    UpdateProposalCell settings.Range("B19"), settings.Range("G19"), remoteUSD
    UpdateProposalDateCell settings.Range("B20"), settings.Range("G20"), _
        remoteDate
    UpdateProposalCell settings.Range("B21"), settings.Range("G21"), remoteProfit
    UpdateProposalCell settings.Range("B22"), settings.Range("G22"), remoteShipping
    UpdateProposalCell settings.Range("B26"), settings.Range("H18"), _
        CLng(remoteRounding)
    UpdateProposalDriftFlags settings, remoteCNY, remoteUSD, remoteDate, _
        remoteProfit, remoteShipping, CLng(remoteRounding)
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
                                     ByVal remoteDate As Variant, _
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
        settings.Range("B20").Value2, remoteDate) Then mismatch = True
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
    Dim profitValue As Variant
    Dim shippingValue As Variant
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

    dataRows = reconciledRows.Count
    If dataRows < 1 Then
        Err.Raise vbObjectError + 126, "ImportReconciledCatalog", _
                  T("invalid_workbook")
    End If
    ReDim mainOutput(1 To dataRows, 1 To PRODUCT_COLUMN_COUNT)
    ReDim syncOutput(1 To dataRows, 1 To SYNC_COLUMN_COUNT)
    Set shippingCounts = CreateObject("Scripting.Dictionary")
    Set profitCounts = CreateObject("Scripting.Dictionary")

    cnyValue = PositiveNumericOrBlank(ConfigSheet().Range("B10").Value2)
    usdValue = PositiveNumericOrBlank(ConfigSheet().Range("B11").Value2)
    rateDate = ConfigSheet().Range("B12").Value2

    For Each rowKey In reconciledRows.Keys
        outputRow = outputRow + 1
        Set reconciledRow = reconciledRows(CStr(rowKey))
        syncKey = SiteText(reconciledRow, "sync_key")
        If syncKey <> CStr(rowKey) Then
            Err.Raise vbObjectError + 127, "ImportReconciledCatalog", _
                      T("invalid_workbook")
        End If
        reconciliationStatus = LCase$( _
            SiteText(reconciledRow, "reconciliation_status"))
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

        patrisCodeValue = SiteText(reconciledRow, "patris_code")
        codeValue = patrisCodeValue
        wooIDValue = SiteText(reconciledRow, "woocommerce_id")
        Select Case reconciliationStatus
            Case "matched"
                If Len(wooIDValue) = 0 Or _
                   syncKey <> "woo:" & wooIDValue Or _
                   Len(patrisCodeValue) = 0 Then
                    Err.Raise vbObjectError + 139, _
                              "ImportReconciledCatalog", _
                              T("invalid_workbook")
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
                    codeValue = SiteText(reconciledRow, "sku")
                End If
        End Select
        weightValue = SiteNumeric(reconciledRow, "weight_grams")
        foreignPrice = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(reconciledRow, "foreign_price"))
        locationValue = SiteText(reconciledRow, "patris_location")
        categoryValue = SiteText(reconciledRow, "categories")
        goodsCurrency = SiteText(reconciledRow, "foreign_currency")
        shippingValue = SiteNumeric( _
            reconciledRow, "shipping_price_per_kg")
        shippingCurrency = SiteText( _
            reconciledRow, "shipping_price_per_kg_currency")
        profitValue = SiteNumeric( _
            reconciledRow, "profit_margin_percent")

        Select Case reconciliationStatus
            Case "ambiguous"
                priceWarning = T("ambiguous_woo_match")
            Case "woo_only"
                If Not IsEmpty(SiteNumeric( _
                    reconciledRow, "effective_price")) Then
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
                       Not IsEmpty(SiteNumeric( _
                           reconciledRow, "effective_price")) Then
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
            SiteNumeric(reconciledRow, "patris_total_stock"), _
            SiteNumeric(reconciledRow, "stock_quantity"))
        mainOutput(outputRow, 7) = codeValue
        mainOutput(outputRow, 8) = SiteText(reconciledRow, "name")
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
        syncOutput(outputRow, 10) = SiteNumeric( _
            reconciledRow, "effective_price")
        syncOutput(outputRow, 11) = SiteText(reconciledRow, "updated_at")
        syncOutput(outputRow, 12) = SiteText( _
            reconciledRow, "record_revision")
        syncOutput(outputRow, 13) = SiteText(reconciledRow, "permalink")
        syncOutput(outputRow, 14) = profitValue
        syncOutput(outputRow, 15) = SiteNumeric( _
            reconciledRow, "patris_final_price")
        syncOutput(outputRow, 16) = SiteNumeric( _
            reconciledRow, "sale_price")
        syncOutput(outputRow, 17) = categoryValue
        syncOutput(outputRow, 18) = SiteText( _
            reconciledRow, "publication_status")
        syncOutput(outputRow, 19) = priceWarning
        syncOutput(outputRow, 20) = rowKind
    Next rowKey

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
    ReplaceTableData table, mainOutput, dataRows, PRODUCT_COLUMN_COUNT
    ReplaceTableData syncTable, syncOutput, dataRows, SYNC_COLUMN_COUNT
    ApplyProductTableFormulas table
    ApplyProductTableFormatting table
    ApplyWooLinks table, syncTable
    ImportReconciledCatalog = dataRows
End Function

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
    Dim fallbackFormula As String
    Dim readyFormula As String
    Dim lookupExpression As String
    Dim eligibleKindFormula As String

    If table.DataBodyRange Is Nothing Then Exit Sub
    lookupExpression = _
        "IF(RC[8]<>"""",""woo:""&RC[8],""patris:""&RC[6])"
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
        ",SyncData,2,FALSE)=""CNY"",VLOOKUP(" & lookupExpression & _
        ",SyncData,6,FALSE)>0),AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE)>0),VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRR"",VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRT"")," & _
        "OR(AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""CNY"",VLOOKUP(" & lookupExpression & _
        ",SyncData,6,FALSE)>0),AND(VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE)>0),VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""IRR"",VLOOKUP(" & lookupExpression & _
        ",SyncData,4,FALSE)=""IRT""))"
    priceFormula = _
        "=IFERROR(IF(" & readyFormula & ",ROUND((" & _
        "(RC[4]*IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""CNY"",VLOOKUP(" & lookupExpression & _
        ",SyncData,6,FALSE),IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""USD"",VLOOKUP(" & lookupExpression & _
        ",SyncData,7,FALSE),IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRR"",0.1,IF(VLOOKUP(" & lookupExpression & _
        ",SyncData,2,FALSE)=""IRT"",1,NA())))))+" & _
        "IF(OR(RC[1]="""",VLOOKUP(" & lookupExpression & _
        ",SyncData,3,FALSE)=""""),0,(RC[1]/1000)*VLOOKUP(" & _
        lookupExpression & ",SyncData,3,FALSE)*IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""CNY"",VLOOKUP(" & _
        lookupExpression & ",SyncData,6,FALSE),IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""USD"",VLOOKUP(" & _
        lookupExpression & ",SyncData,7,FALSE),IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""IRR"",0.1,IF(VLOOKUP(" & _
        lookupExpression & ",SyncData,4,FALSE)=""IRT"",1,NA()))))))" & _
        "*(1+VLOOKUP(" & lookupExpression & _
        ",SyncData,5,FALSE)/100),-'" & _
        U("062A0646063806CC06450627062A") & "'!R15C2)," & _
        fallbackFormula & ")," & fallbackFormula & ")"
    table.ListColumns(1).DataBodyRange.FormulaR1C1 = priceFormula
End Sub

Private Sub ApplyProductTableFormatting(ByVal table As ListObject)
    Dim highlightRule As FormatCondition

    If table.DataBodyRange Is Nothing Then Exit Sub
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
    table.ListColumns(7).DataBodyRange.Font.Name = "Yekan Bakh"
    table.ListColumns(9).DataBodyRange.Font.Name = "Yekan Bakh"
    table.ListColumns(8).DataBodyRange.Font.Name = "Yekan Bakh"
    table.ListColumns(8).DataBodyRange.Font.Bold = True
    table.DataBodyRange.Rows.RowHeight = 24
    table.DataBodyRange.FormatConditions.Delete
    Set highlightRule = table.DataBodyRange.FormatConditions.Add( _
        Type:=xlExpression, _
        Formula1:="=ROW()=SelectedProductRow")
    highlightRule.Interior.Color = RGB(255, 244, 204)
End Sub

Private Sub ApplyWooLinks(ByVal table As ListObject, _
                          ByVal syncTable As ListObject)
    Dim rowIndex As Long

    If table.DataBodyRange Is Nothing Or syncTable.DataBodyRange Is Nothing Then Exit Sub
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        ApplyWooLinkRow table, syncTable, rowIndex
    Next rowIndex
End Sub

Private Sub ApplyWooLinkRow(ByVal table As ListObject, _
                            ByVal syncTable As ListObject, _
                            ByVal rowIndex As Long)
    Dim wooID As String
    Dim permalink As String
    Dim linkText As String
    Dim publicationStatus As String
    Dim linkCell As Range
    Dim wooCell As Range

    On Error GoTo RowFailed
    wooID = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 9).Value2))
    permalink = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 13).Value2))
    publicationStatus = LCase$(Trim$(CStr( _
        syncTable.DataBodyRange.Cells(rowIndex, 18).Value2)))
    Set linkCell = table.DataBodyRange.Cells(rowIndex, 8)
    Set wooCell = table.DataBodyRange.Cells(rowIndex, 9)
    linkText = CStr(linkCell.Value2)

    wooCell.NumberFormat = "@"
    wooCell.ReadingOrder = xlLTR
    wooCell.Font.Name = "Yekan Bakh"
    wooCell.Value2 = wooID
    linkCell.Hyperlinks.Delete
    linkCell.Value2 = linkText
    linkCell.Font.Name = "Yekan Bakh"
    linkCell.Font.Bold = True
    Select Case publicationStatus
        Case "publish"
            linkCell.Font.Color = RGB(1, 104, 205)
        Case "draft", "pending", "private", "future"
            linkCell.Font.Color = RGB(180, 111, 0)
        Case Else
            linkCell.Font.Color = RGB(164, 40, 40)
    End Select

    If Len(wooID) = 0 Then
        linkCell.Font.Color = RGB(164, 40, 40)
        Exit Sub
    End If
    If Len(linkText) = 0 Then
        linkText = wooID
        linkCell.Value2 = linkText
    End If

    If IsAllowedDigitalogicUrl(permalink) Then
        table.Parent.Hyperlinks.Add _
            Anchor:=linkCell, Address:=permalink, _
            TextToDisplay:=linkText
        linkCell.Font.Name = "Yekan Bakh"
        linkCell.Font.Bold = True
        Select Case publicationStatus
            Case "publish"
                linkCell.Font.Color = RGB(1, 104, 205)
            Case "draft", "pending", "private", "future"
                linkCell.Font.Color = RGB(180, 111, 0)
            Case Else
                linkCell.Font.Color = RGB(164, 40, 40)
        End Select
    End If
    Exit Sub

RowFailed:
    On Error Resume Next
    If Not wooCell Is Nothing Then
        wooCell.NumberFormat = "@"
        wooCell.Value2 = wooID
    End If
    If Not linkCell Is Nothing And Len(linkText) > 0 Then
        linkCell.Value2 = linkText
        linkCell.Font.Name = "Yekan Bakh"
        linkCell.Font.Bold = True
        linkCell.Font.Color = RGB(164, 40, 40)
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
    Next rowIndex
    PriceParitySummary = Array(matched, overLimit)
End Function

Private Function StateRequestJson(ByVal page As Long) As String
    Dim requestID As String

    EnsureSourceIdentity
    requestID = NewRequestID("state")
    StateRequestJson = _
        "{""schema"":" & JsonString(PRICING_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """operation"":""state"",""source"":{" & _
        """id"":" & JsonString(mSourceID) & "," & _
        """dataset"":" & JsonString(mSourceDataset) & "," & _
        """revision"":" & JsonString(mSourceRevision) & "}," & _
        """client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID) & "," & _
        """channel"":" & JsonString(PRICING_CONTRACT_CHANNEL) & "," & _
        """request_id"":" & JsonString(requestID) & "," & _
        """page"":" & CStr(page) & "," & _
        """limit"":" & CStr(STATE_PAGE_SIZE) & ",""locale"":""fa""}"
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
    usdEffectiveDate = CanonicalDateText(settings.Range("H16").Value2)
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

    dateText = CanonicalDateText(settings.Range("B20").Value2)
    If Not IsNumeric(settings.Range("B18").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B18").Value2) <= 0 Then GoTo InvalidSettings
    If Not IsNumeric(settings.Range("B19").Value2) Then GoTo InvalidSettings
    If CDbl(settings.Range("B19").Value2) <= 0 Then GoTo InvalidSettings
    If Len(dateText) <> 10 Then GoTo InvalidSettings
    If Mid$(dateText, 5, 1) <> "-" Then GoTo InvalidSettings
    If Mid$(dateText, 8, 1) <> "-" Then GoTo InvalidSettings
    If cnyEffectiveDate <> dateText Then GoTo InvalidSettings
    If Len(usdEffectiveDate) <> 10 Then GoTo InvalidSettings
    If Mid$(usdEffectiveDate, 5, 1) <> "-" Then GoTo InvalidSettings
    If Mid$(usdEffectiveDate, 8, 1) <> "-" Then GoTo InvalidSettings
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

Private Function HttpGet(ByVal endpoint As String, _
                         ByVal acceptHeader As String) As String
    Dim http As Object

    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setProxy 1
    http.setTimeouts _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS
    http.Open "GET", endpoint, False
    http.setRequestHeader "Accept", acceptHeader
    http.Send
    If http.Status < 200 Or http.Status >= 300 Then
        Err.Raise vbObjectError + 141, "HttpGet", _
                  "HTTP " & CStr(http.Status)
    End If
    If Len(CStr(http.responseText)) = 0 Then
        Err.Raise vbObjectError + 142, "HttpGet", T("bridge_missing")
    End If
    HttpGet = CStr(http.responseText)
End Function

Private Function HttpJson(ByVal endpoint As String, _
                          ByVal requestBody As String, _
                          Optional ByVal idempotencyKey As String = "", _
                          Optional ByVal expectedRevision As String = "") As String
    Dim csrfToken As String

    If Not IsAllowedPricingBridgeUrl(endpoint) Then
        Err.Raise vbObjectError + 143, "HttpJson", T("bridge_missing")
    End If
    csrfToken = PricingSessionToken()
    HttpJson = HttpPostJsonRaw( _
        endpoint, requestBody, csrfToken, idempotencyKey, expectedRevision)
End Function

Private Function PricingSessionToken() As String
    Dim sessionText As String
    Dim sessionRoot As JsonValue
    Dim csrfToken As String

    sessionText = HttpPostJsonRaw( _
        PricingBaseURL() & "/session", "{}", "", "", "")
    Set sessionRoot = JsonRuntime.ParseJson(sessionText)
    If sessionRoot Is Nothing Then
        Err.Raise vbObjectError + 144, "PricingSessionToken", _
                  T("bridge_missing")
    End If
    If sessionRoot.Kind <> "object" Then
        Err.Raise vbObjectError + 144, "PricingSessionToken", _
                  T("bridge_missing")
    End If
    If CStr(JsonRuntime.JsonText( _
           sessionRoot, "schema")) <> PRICING_SESSION_SCHEMA Then
        Err.Raise vbObjectError + 144, "PricingSessionToken", _
                  T("bridge_missing")
    End If
    csrfToken = Trim$(CStr( _
        JsonRuntime.JsonText(sessionRoot, "csrf_token")))
    If Len(csrfToken) <> 43 Then
        Err.Raise vbObjectError + 145, "PricingSessionToken", _
                  T("bridge_missing")
    End If
    PricingSessionToken = csrfToken
End Function

Private Function HttpPostJsonRaw(ByVal endpoint As String, _
                                 ByVal requestBody As String, _
                                 ByVal csrfToken As String, _
                                 ByVal idempotencyKey As String, _
                                 ByVal expectedRevision As String) As String
    Dim http As Object
    Dim errorMessage As String

    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setProxy 1
    http.setTimeouts _
        PRICING_HTTP_TIMEOUT_MS, PRICING_HTTP_TIMEOUT_MS, _
        PRICING_HTTP_TIMEOUT_MS, PRICING_HTTP_TIMEOUT_MS
    http.Open "POST", endpoint, False
    http.setRequestHeader "Accept", "application/json"
    http.setRequestHeader "Content-Type", "application/json; charset=utf-8"
    http.setRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    If Len(csrfToken) > 0 Then
        http.setRequestHeader PRICING_CSRF_HEADER, csrfToken
    End If
    If Len(idempotencyKey) > 0 Then
        http.setRequestHeader "Idempotency-Key", idempotencyKey
    End If
    If Len(expectedRevision) > 0 Then
        http.setRequestHeader "If-Match", _
            Chr$(34) & expectedRevision & Chr$(34)
    End If
    http.Send Utf8Bytes(requestBody)
    If http.Status < 200 Or http.Status >= 300 Then
        errorMessage = ResponseErrorMessage(CStr(http.responseText))
        If Len(errorMessage) = 0 Then errorMessage = "HTTP " & CStr(http.Status)
        Err.Raise vbObjectError + 146, "HttpPostJsonRaw", errorMessage
    End If
    If Len(CStr(http.responseText)) > MAX_PRICING_RESPONSE_CHARS Then
        Err.Raise vbObjectError + 147, "HttpPostJsonRaw", T("bridge_missing")
    End If
    HttpPostJsonRaw = CStr(http.responseText)
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
        "/api/excel/pricing-sync"
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

Private Function ResponseErrorMessage(ByVal responseText As String) As String
    Dim root As JsonValue
    Dim data As JsonValue

    On Error GoTo NoMessage
    Set root = JsonRuntime.ParseJson(responseText)
    Set data = ResponseData(root)
    ResponseErrorMessage = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(data, "message"))))
    If Len(ResponseErrorMessage) = 0 Then
        ResponseErrorMessage = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(root, "message"))))
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
        InStr(1, address, "/api/excel/pricing-sync/", vbTextCompare) > 0
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
    Dim decimalSeparator As String

    FormatNumericForText = Format$(value, "0.############")
    decimalSeparator = CStr(Application.International(xlDecimalSeparator))
    If decimalSeparator <> "." Then
        FormatNumericForText = Replace$( _
            FormatNumericForText, decimalSeparator, ".")
    End If
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
    settings.Range("B21").Value2 = 0.3
    settings.Range("B22").Value2 = 120#
    settings.Range("B26").Value2 = 2#
    settings.Range("B24").Value2 = 0.07
    UpdateProposalDriftFlags settings, 29500#, 187891#, expectedDate, _
        0.3, 120#, 2#
    If BooleanValue(settings.Range("G39").Value2) Or _
       BooleanValue(settings.Range("G40").Value2) Then
        Err.Raise vbObjectError + 205, _
                  "ValidateProposalDateNormalization", _
                  "An equivalent Excel serial and ISO date set proposal drift."
    End If

    settings.Range("B20").Value2 = CDbl(sampleDate) + 1#
    UpdateProposalDriftFlags settings, 29500#, 187891#, expectedDate, _
        0.3, 120#, 2#
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

    message = Replace$(message, vbCr, " ")
    message = Replace$(message, vbLf, " ")
    lowered = LCase$(message)
    If InStr(lowered, "credential") > 0 Or _
       InStr(lowered, "authorization") > 0 Or _
       InStr(lowered, "secret") > 0 Or _
       InStr(lowered, "token") > 0 Then
        SafeStatusError = T("bridge_missing")
    ElseIf Len(message) > 300 Then
        SafeStatusError = Left$(message, 300)
    Else
        SafeStatusError = message
    End If
End Function

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

Private Function ShowUnicodeMessage(ByVal message As String, _
                                    ByVal messageType As VbMsgBoxStyle, _
                                    ByVal title As String) As Long
    ShowUnicodeMessage = MessageBoxW( _
        Application.hWnd, StrPtr(message), StrPtr(title), _
        CLng(messageType) Or MB_RIGHT Or MB_RTLREADING)
End Function

Private Function T(ByVal key As String) As String
    Select Case key
        Case "sync_done"
            T = U("0647064506AF06270645200C06330627063206CC002006A906270645064400200634062F002E")
        Case "sync_failed"
            T = U("0647064506AF06270645200C06330627063206CC002006270646062C06270645002006460634062F003A")
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
