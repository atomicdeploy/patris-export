Attribute VB_Name = "ProductCatalogSync"
Option Explicit

Private Const PRODUCTS_TABLE As String = "Products"
Private Const SYNC_TABLE As String = "SyncData"
Private Const YUAN_TABLE As String = "Yuan_Price"
Private Const SHIPPING_TABLE As String = "Shipping"
Private Const PROFIT_TABLE As String = "Profit"
Private Const STANDARD_COLUMN_COUNT As Long = 8
Private Const ADVANCED_COLUMN_COUNT As Long = 16
Private Const SYNC_COLUMN_COUNT As Long = 16
Private Const STATE_PAGE_SIZE As Long = 100
Private Const MAX_STATE_PAGES As Long = 20
Private Const HTTP_TIMEOUT_MS As Long = 90000
Private Const PRICING_HTTP_TIMEOUT_MS As Long = 240000
Private Const MAX_PRICING_RESPONSE_CHARS As Long = 4194304
Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"
Private Const PRICING_CLIENT_ID As String = "digitalogic-price-calculator/v1"
Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"
Private Const PRICING_REQUEST_SCHEMA As String = "patris.excel-pricing-companion-request/v1"
Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"
Private Const LOOPBACK_PREFIX As String = "http://127.0.0.1:18080/"
Private Const MB_RIGHT As Long = &H80000
Private Const MB_RTLREADING As Long = &H100000

Private mSourceID As String
Private mSourceDataset As String
Private mSourceRevision As String
Private mLastPreviewDigest As String
Private mLastPreviewExpiresAt As String
Private mLastPreviewStateRevision As String

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
    Dim expectedColumns As Long

    ValidateUnicodeRuntime
    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    expectedColumns = IIf(IsAdvanced(), ADVANCED_COLUMN_COUNT, STANDARD_COLUMN_COUNT)

    If table.Range.Row <> 5 Or table.Range.Column <> 2 Then
        Err.Raise vbObjectError + 90, "ValidateWorkbook", T("invalid_workbook")
    End If
    If table.ListColumns.Count <> expectedColumns Then
        Err.Raise vbObjectError + 91, "ValidateWorkbook", T("invalid_workbook")
    End If
    If syncTable.ListColumns.Count <> SYNC_COLUMN_COUNT Then
        Err.Raise vbObjectError + 92, "ValidateWorkbook", T("invalid_workbook")
    End If
End Sub

Public Sub RefreshAllData(Optional ByVal silent As Boolean = False)
    Dim previousCalculation As XlCalculation
    Dim contract As JsonValue
    Dim siteRows As Object
    Dim patrisRows As Long
    Dim wooRows As Long
    Dim parity As Variant
    Dim statusText As String
    Dim settings As Worksheet

    On Error GoTo Failed
    previousCalculation = Application.Calculation
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual

    Set settings = ConfigSheet()
    Set contract = LoadPatrisContract()
    ReadSourceIdentity contract
    Set siteRows = CreateObject("Scripting.Dictionary")
    siteRows.CompareMode = vbBinaryCompare
    wooRows = RefreshPricingState(siteRows)
    patrisRows = ImportPatrisContract(contract, siteRows)
    Application.CalculateFullRebuild
    parity = PriceParitySummary()

    statusText = CStr(patrisRows) & " " & T("patris_rows") & U("061B") & " " & _
        CStr(wooRows) & " " & T("woo_products") & U("061B") & " " & _
        CStr(parity(0)) & " " & T("matched") & U("061B") & " " & _
        CStr(parity(1)) & " " & T("over_limit")
    If CBool(settings.Range("G15").Value2) Then
        statusText = statusText & U("061B") & " " & T("stale_rate")
    End If
    settings.Range("B6").Value = statusText
    settings.Range("B7").Value = Now
    settings.Range("B7").NumberFormat = "yyyy-mm-dd hh:mm"

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
    statusText = T("sync_failed") & " " & SafeStatusError(Err.Description)
    On Error Resume Next
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

Public Sub PreviewPricingChanges()
    Dim settings As Worksheet
    Dim requestID As String
    Dim requestBody As String
    Dim responseText As String
    Dim root As JsonValue
    Dim result As JsonValue
    Dim statusText As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    EnsureSourceIdentity
    requestID = NewRequestID("preview")
    requestBody = BuildPricingRequest("preview", requestID, vbNullString, False)
    responseText = HttpJson( _
        PricingEndpoint("preview"), requestBody, requestID, _
        Trim$(CStr(settings.Range("B14").Value2)))
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)

    mLastPreviewDigest = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "preview_digest"))))
    mLastPreviewExpiresAt = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "expires_at"))))
    mLastPreviewStateRevision = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "state_revision"))))
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
    ShowUnicodeMessage CStr(settings.Range("B23").Value2), _
        vbInformation, T("apply_title")
    Exit Sub

Failed:
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

    On Error GoTo Failed
    Set settings = ConfigSheet()
    If Len(mLastPreviewDigest) = 0 Then
        mLastPreviewDigest = Trim$(CStr(settings.Range("G26").Value2))
    End If
    If Len(mLastPreviewDigest) = 0 Then
        ShowUnicodeMessage T("preview_first"), vbExclamation, T("apply_title")
        Exit Sub
    End If

    answer = ShowUnicodeMessage( _
        T("apply_confirm"), vbQuestion Or vbYesNo, T("apply_title"))
    If answer <> vbYes Then Exit Sub

    EnsureSourceIdentity
    requestID = NewRequestID("apply")
    requestBody = BuildPricingRequest( _
        "apply", requestID, mLastPreviewDigest, True)
    responseText = HttpJson( _
        PricingEndpoint("apply"), requestBody, requestID, _
        Trim$(CStr(settings.Range("B14").Value2)))
    Set root = JsonRuntime.ParseJson(responseText)
    Set result = ResponseData(root)
    statusText = Trim$(CStr(BlankIfNull( _
        JsonRuntime.JsonText(result, "status"))))
    statusText = T("apply_done") & " " & statusText & _
        WarningSummary(result)

    mLastPreviewDigest = vbNullString
    mLastPreviewExpiresAt = vbNullString
    mLastPreviewStateRevision = vbNullString
    settings.Range("G26:G27").ClearContents
    settings.Range("B23").Value2 = statusText
    ShowUnicodeMessage CStr(settings.Range("B23").Value2), _
        vbInformation, T("apply_title")
    RefreshAllData True
    Exit Sub

Failed:
    statusText = SafeStatusError(Err.Description)
    On Error Resume Next
    ConfigSheet().Range("B23").Value2 = statusText
    On Error GoTo 0
    ShowUnicodeMessage statusText, vbExclamation, T("apply_title")
End Sub

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
    Dim codeValue As String
    Dim hasMore As Boolean

    For page = 1 To MAX_STATE_PAGES
        requestBody = StateRequestJson(page)
        responseText = HttpJson(PricingEndpoint("state"), requestBody)
        Set root = JsonRuntime.ParseJson(responseText)
        Set state = ResponseData(root)
        If page = 1 Then ApplyGlobalState state

        Set catalog = JsonRuntime.JsonMember(state, "catalog")
        If catalog Is Nothing Or catalog.Kind <> "object" Then
            Err.Raise vbObjectError + 110, "RefreshPricingState", _
                      T("bridge_missing")
        End If
        Set rowsValue = JsonRuntime.JsonMember(catalog, "rows")
        If rowsValue Is Nothing Or rowsValue.Kind <> "array" Then
            Err.Raise vbObjectError + 111, "RefreshPricingState", _
                      T("bridge_missing")
        End If

        pageRows = JsonRuntime.JsonArrayCount(rowsValue)
        For rowIndex = 1 To pageRows
            Set rowValue = JsonRuntime.JsonArrayItem(rowsValue, rowIndex)
            codeValue = Trim$(CStr(BlankIfNull( _
                JsonRuntime.JsonText(rowValue, "patris_code"))))
            If Len(codeValue) > 0 And Not siteRows.Exists(codeValue) Then
                siteRows.Add codeValue, rowValue
            End If
        Next rowIndex
        RefreshPricingState = RefreshPricingState + pageRows

        Set pagination = JsonRuntime.JsonMember(catalog, "pagination")
        hasMore = False
        If Not pagination Is Nothing Then
            hasMore = BooleanValue( _
                JsonRuntime.JsonText(pagination, "has_more"))
        End If
        If Not hasMore Then Exit For
    Next page
End Function

Private Sub ApplyGlobalState(ByVal state As JsonValue)
    Dim settings As Worksheet
    Dim currencyState As JsonValue
    Dim markup As JsonValue
    Dim remoteCNY As Variant
    Dim remoteUSD As Variant
    Dim remoteDate As Variant
    Dim remoteProfit As Variant
    Dim stale As Boolean

    Set settings = ConfigSheet()
    Set currencyState = JsonRuntime.JsonMember(state, "currency")
    Set markup = JsonRuntime.JsonMember(state, "default_markup")
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
    remoteProfit = Empty
    If Not markup Is Nothing Then
        If BooleanValue(JsonRuntime.JsonText(markup, "configured")) Then
            remoteProfit = NumericOrBlank( _
                JsonRuntime.JsonText(markup, "profit_percent"))
            If Not IsEmpty(remoteProfit) Then remoteProfit = CDbl(remoteProfit) / 100#
        End If
    End If
    stale = BooleanValue(JsonRuntime.JsonText(currencyState, "stale"))
    If Not stale Then
        stale = CurrencyDateAgeDays(CStr(remoteDate)) > _
            CLng(Val(CStr(settings.Range("B25").Value2)))
    End If

    settings.Range("B10").Value = remoteCNY
    settings.Range("B11").Value = remoteUSD
    settings.Range("B12").Value = remoteDate
    settings.Range("B13").Value = remoteProfit
    settings.Range("B14").Value = BlankIfNull( _
        JsonRuntime.JsonText(state, "state_revision"))
    settings.Range("B15").Value = BlankIfNull( _
        JsonRuntime.JsonText(state, "generated_at"))
    settings.Range("G15").Value = stale

    UpdateProposalCell settings.Range("B18"), settings.Range("G18"), remoteCNY
    UpdateProposalCell settings.Range("B19"), settings.Range("G19"), remoteUSD
    UpdateProposalCell settings.Range("B20"), settings.Range("G20"), remoteDate
    UpdateProposalCell settings.Range("B21"), settings.Range("G21"), remoteProfit
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

Private Function ImportPatrisContract(ByVal contract As JsonValue, _
                                      ByVal siteRows As Object) As Long
    Dim productsValue As JsonValue
    Dim product As JsonValue
    Dim siteRow As JsonValue
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim mainOutput() As Variant
    Dim syncOutput() As Variant
    Dim codesSeen As Object
    Dim shippingCounts As Object
    Dim profitCounts As Object
    Dim dataRows As Long
    Dim mainColumns As Long
    Dim rowIndex As Long
    Dim codeValue As String
    Dim weightValue As Variant
    Dim foreignPrice As Variant
    Dim locationValue As String
    Dim profitValue As Variant
    Dim shippingValue As Variant
    Dim commonShipping As Variant
    Dim commonProfit As Variant
    Dim cnyValue As Variant
    Dim usdValue As Variant
    Dim rateDate As Variant

    Set productsValue = JsonRuntime.JsonMember(contract, "products")
    If productsValue Is Nothing Or productsValue.Kind <> "array" Then
        Err.Raise vbObjectError + 120, "ImportPatrisContract", _
                  T("invalid_workbook")
    End If
    dataRows = JsonRuntime.JsonArrayCount(productsValue)
    If dataRows < 1 Then
        Err.Raise vbObjectError + 121, "ImportPatrisContract", _
                  T("invalid_workbook")
    End If

    mainColumns = IIf(IsAdvanced(), ADVANCED_COLUMN_COUNT, STANDARD_COLUMN_COUNT)
    ReDim mainOutput(1 To dataRows, 1 To mainColumns)
    ReDim syncOutput(1 To dataRows, 1 To SYNC_COLUMN_COUNT)
    Set codesSeen = CreateObject("Scripting.Dictionary")
    codesSeen.CompareMode = vbBinaryCompare
    Set shippingCounts = CreateObject("Scripting.Dictionary")
    Set profitCounts = CreateObject("Scripting.Dictionary")

    cnyValue = PositiveNumericOrBlank(ConfigSheet().Range("B18").Value2)
    usdValue = PositiveNumericOrBlank(ConfigSheet().Range("B19").Value2)
    rateDate = ConfigSheet().Range("B20").Value2

    For rowIndex = 1 To dataRows
        Set product = JsonRuntime.JsonArrayItem(productsValue, rowIndex)
        codeValue = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(product, "product_code"))))
        If Len(codeValue) = 0 Or codesSeen.Exists(codeValue) Then
            Err.Raise vbObjectError + 122, "ImportPatrisContract", _
                      T("invalid_workbook")
        End If
        codesSeen.Add codeValue, True

        Set siteRow = Nothing
        If siteRows.Exists(codeValue) Then Set siteRow = siteRows(codeValue)
        weightValue = NumericOrBlank(JsonRuntime.JsonText(product, "weight_grams"))
        foreignPrice = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(product, "foreign_price"))
        locationValue = Trim$(CStr(BlankIfNull( _
            JsonRuntime.JsonText(product, "location"))))
        profitValue = NumericOrBlank( _
            JsonRuntime.JsonText(product, "markup_percent"))
        If IsEmpty(profitValue) And Not siteRow Is Nothing Then
            profitValue = NumericOrBlank( _
                JsonRuntime.JsonText(siteRow, "profit_percent"))
        End If
        shippingValue = NumericOrBlank( _
            JsonRuntime.JsonText(product, "shipping_price_per_kg"))
        If IsEmpty(shippingValue) And Not siteRow Is Nothing Then
            shippingValue = NumericOrBlank( _
                JsonRuntime.JsonText(siteRow, "shipping_price_per_kg"))
        End If
        CountNumericValue shippingCounts, shippingValue
        CountNumericValue profitCounts, profitValue

        mainOutput(rowIndex, 1) = Empty
        mainOutput(rowIndex, 2) = weightValue
        mainOutput(rowIndex, 3) = BuildOtherText(weightValue, locationValue)
        mainOutput(rowIndex, 4) = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(product, "sale_price_source"))
        mainOutput(rowIndex, 5) = foreignPrice
        mainOutput(rowIndex, 6) = NumericOrBlank( _
            JsonRuntime.JsonText(product, "total_stock"))
        mainOutput(rowIndex, 7) = codeValue
        mainOutput(rowIndex, 8) = BlankIfNull( _
            JsonRuntime.JsonText(product, "name"))

        syncOutput(rowIndex, 1) = codeValue
        syncOutput(rowIndex, 2) = FirstText( _
            JsonRuntime.JsonText(product, "foreign_currency"), _
            SiteText(siteRow, "foreign_currency"), vbNullString)
        syncOutput(rowIndex, 3) = shippingValue
        syncOutput(rowIndex, 4) = FirstText( _
            JsonRuntime.JsonText(product, "shipping_price_per_kg_currency"), _
            SiteText(siteRow, "shipping_price_per_kg_currency"), vbNullString)
        syncOutput(rowIndex, 5) = profitValue
        syncOutput(rowIndex, 6) = FirstNumeric( _
            cnyValue, JsonRuntime.JsonText(product, "irt_per_cny"))
        syncOutput(rowIndex, 7) = usdValue
        syncOutput(rowIndex, 8) = FirstText( _
            rateDate, JsonRuntime.JsonText(product, "currency_effective_date"), vbNullString)
        syncOutput(rowIndex, 9) = SiteNumeric(siteRow, "woocommerce_id")
        syncOutput(rowIndex, 10) = SiteNumeric(siteRow, "effective_price")
        syncOutput(rowIndex, 11) = SiteText(siteRow, "updated_at")
        syncOutput(rowIndex, 12) = SiteText(siteRow, "record_revision")
        syncOutput(rowIndex, 13) = SiteText(siteRow, "permalink")
        syncOutput(rowIndex, 14) = profitValue
        syncOutput(rowIndex, 15) = FirstNumeric( _
            JsonRuntime.JsonText(product, "final_price"), _
            SiteNumeric(siteRow, "patris_final_price"))
        syncOutput(rowIndex, 16) = SiteNumeric(siteRow, "sale_price")

        If mainColumns = ADVANCED_COLUMN_COUNT Then
            mainOutput(rowIndex, 9) = syncOutput(rowIndex, 9)
            mainOutput(rowIndex, 10) = syncOutput(rowIndex, 10)
            mainOutput(rowIndex, 11) = Empty
            mainOutput(rowIndex, 12) = Empty
            mainOutput(rowIndex, 13) = syncOutput(rowIndex, 2)
            mainOutput(rowIndex, 14) = profitValue
            mainOutput(rowIndex, 15) = shippingValue
            mainOutput(rowIndex, 16) = syncOutput(rowIndex, 8)
        End If
    Next rowIndex

    commonShipping = MostCommonNumeric(shippingCounts)
    commonProfit = MostCommonNumeric(profitCounts)
    If Len(CanonicalCellText(ConfigSheet().Range("B22").Value2)) = 0 Then
        ConfigSheet().Range("B22").Value = commonShipping
    End If
    If Len(CanonicalCellText(ConfigSheet().Range("B21").Value2)) = 0 And _
       Not IsEmpty(commonProfit) Then
        ConfigSheet().Range("B21").Value = CDbl(commonProfit) / 100#
    End If
    For rowIndex = 1 To dataRows
        If IsEmpty(syncOutput(rowIndex, 3)) Then
            syncOutput(rowIndex, 3) = ConfigSheet().Range("B22").Value2
            If Len(CanonicalCellText(syncOutput(rowIndex, 3))) > 0 And _
               Len(CanonicalCellText(syncOutput(rowIndex, 4))) = 0 Then
                syncOutput(rowIndex, 4) = "CNY"
            End If
            If mainColumns = ADVANCED_COLUMN_COUNT Then
                mainOutput(rowIndex, 15) = syncOutput(rowIndex, 3)
            End If
        End If
        If IsEmpty(syncOutput(rowIndex, 5)) Then
            If IsNumeric(ConfigSheet().Range("B21").Value2) Then
                syncOutput(rowIndex, 5) = _
                    CDbl(ConfigSheet().Range("B21").Value2) * 100#
                syncOutput(rowIndex, 14) = syncOutput(rowIndex, 5)
                If mainColumns = ADVANCED_COLUMN_COUNT Then
                    mainOutput(rowIndex, 14) = syncOutput(rowIndex, 5)
                End If
            End If
        End If
    Next rowIndex

    Set table = PriceSheet().ListObjects(PRODUCTS_TABLE)
    Set syncTable = SyncSheet().ListObjects(SYNC_TABLE)
    ReplaceTableData table, mainOutput, dataRows, mainColumns
    ReplaceTableData syncTable, syncOutput, dataRows, SYNC_COLUMN_COUNT
    ApplyProductTableFormulas table
    ApplyProductTableFormatting table
    ApplyWooLinks table, syncTable
    ImportPatrisContract = dataRows
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
    parentSheet.Cells(firstRow + 1, firstColumn).Resize( _
        dataRows, dataColumns).Value = output
End Sub

Private Sub ApplyProductTableFormulas(ByVal table As ListObject)
    Dim priceFormula As String
    Dim differenceFormula As String
    Dim statusFormula As String

    If table.DataBodyRange Is Nothing Then Exit Sub
    priceFormula = _
        "=IFERROR(IF(RC[4]="""","""",ROUND((" & _
        "(RC[4]*IF(VLOOKUP(RC[6],SyncData,2,FALSE)=""CNY""," & _
        "VLOOKUP(RC[6],SyncData,6,FALSE),IF(VLOOKUP(RC[6],SyncData,2,FALSE)=""USD""," & _
        "VLOOKUP(RC[6],SyncData,7,FALSE),IF(VLOOKUP(RC[6],SyncData,2,FALSE)=""IRR"",0.1," & _
        "IF(VLOOKUP(RC[6],SyncData,2,FALSE)=""IRT"",1,NA())))))+" & _
        "IF(OR(RC[1]="""",VLOOKUP(RC[6],SyncData,3,FALSE)=""""),0," & _
        "(RC[1]/1000)*VLOOKUP(RC[6],SyncData,3,FALSE)*" & _
        "IF(VLOOKUP(RC[6],SyncData,4,FALSE)=""CNY"",VLOOKUP(RC[6],SyncData,6,FALSE)," & _
        "IF(VLOOKUP(RC[6],SyncData,4,FALSE)=""USD"",VLOOKUP(RC[6],SyncData,7,FALSE)," & _
        "IF(VLOOKUP(RC[6],SyncData,4,FALSE)=""IRR"",0.1," & _
        "IF(VLOOKUP(RC[6],SyncData,4,FALSE)=""IRT"",1,NA()))))))" & _
        "*(1+VLOOKUP(RC[6],SyncData,5,FALSE)/100),0)),"""")"
    table.ListColumns(1).DataBodyRange.FormulaR1C1 = priceFormula

    If table.ListColumns.Count = ADVANCED_COLUMN_COUNT Then
        differenceFormula = _
            "=IFERROR(IF(OR(RC[-10]="""",RC[-1]="""",RC[-1]=0),""""," & _
            "(RC[-10]-RC[-1])/RC[-1]),"""")"
        table.ListColumns(11).DataBodyRange.FormulaR1C1 = differenceFormula
        statusFormula = _
            "=IFERROR(IF(RC[-11]="""",""" & T("price_missing") & """," & _
            "IF(RC[-2]="""",""" & T("woo_missing") & """," & _
            "IF('" & ConfigSheet().Name & "'!R15C7=TRUE,""" & T("status_stale") & """," & _
            "IF(VLOOKUP(RC[-5],SyncData,16,FALSE)<>"""",""" & T("status_sale") & """," & _
            "IF(ABS(RC[-1])>'" & ConfigSheet().Name & "'!R24C2,""" & T("status_drift") & """," & _
            "IF(ABS(RC[-1])>0.000001,""" & T("status_sync") & """,""" & _
            T("matched") & """)))))),"""")"
        table.ListColumns(12).DataBodyRange.FormulaR1C1 = statusFormula
    End If
End Sub

Private Sub ApplyProductTableFormatting(ByVal table As ListObject)
    If table.DataBodyRange Is Nothing Then Exit Sub
    table.ListColumns(1).DataBodyRange.NumberFormat = "#,##0"
    table.ListColumns(2).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(4).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(5).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(6).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(7).DataBodyRange.NumberFormat = "@"
    If table.ListColumns.Count = ADVANCED_COLUMN_COUNT Then
        table.ListColumns(9).DataBodyRange.NumberFormat = "0"
        table.ListColumns(10).DataBodyRange.NumberFormat = "#,##0"
        table.ListColumns(11).DataBodyRange.NumberFormat = "0.0%"
        table.ListColumns(14).DataBodyRange.NumberFormat = "0.00""%"""
        table.ListColumns(15).DataBodyRange.NumberFormat = "#,##0.######"
        table.ListColumns(16).DataBodyRange.NumberFormat = "yyyy/mm/dd"
    End If
End Sub

Private Sub ApplyWooLinks(ByVal table As ListObject, _
                          ByVal syncTable As ListObject)
    Dim rowIndex As Long
    Dim wooID As String
    Dim permalink As String
    Dim linkText As String
    Dim linkCell As Range

    If table.DataBodyRange Is Nothing Or syncTable.DataBodyRange Is Nothing Then Exit Sub
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        wooID = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 9).Value2))
        permalink = Trim$(CStr(syncTable.DataBodyRange.Cells(rowIndex, 13).Value2))
        If table.ListColumns.Count = ADVANCED_COLUMN_COUNT Then
            Set linkCell = table.DataBodyRange.Cells(rowIndex, 9)
            linkText = vbNullString
        Else
            Set linkCell = table.DataBodyRange.Cells(rowIndex, 8)
            linkText = CStr(linkCell.Value2)
        End If
        linkCell.Hyperlinks.Delete
        If Len(wooID) > 0 Then
            If Len(linkText) > 0 Then linkText = linkText & " - "
            linkText = linkText & "WooID " & wooID
            If IsAllowedDigitalogicUrl(permalink) Then
                table.Parent.Hyperlinks.Add _
                    Anchor:=linkCell, Address:=permalink, _
                    TextToDisplay:=linkText
            Else
                linkCell.Value = linkText
            End If
        ElseIf table.ListColumns.Count = ADVANCED_COLUMN_COUNT Then
            linkCell.ClearContents
        End If
    Next rowIndex
End Sub

Private Function PriceParitySummary() As Variant
    Dim table As ListObject
    Dim syncTable As ListObject
    Dim rowIndex As Long
    Dim calculated As Variant
    Dim wooPrice As Variant
    Dim salePrice As Variant
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
        salePrice = NumericOrBlank( _
            syncTable.DataBodyRange.Cells(rowIndex, 16).Value2)
        If IsEmpty(salePrice) And Not IsEmpty(calculated) And _
           Not IsEmpty(wooPrice) And CDbl(wooPrice) > 0 Then
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
    StateRequestJson = _
        "{""schema"":" & JsonString(PRICING_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """operation"":""state"",""page"":" & CStr(page) & "," & _
        """limit"":" & CStr(STATE_PAGE_SIZE) & ",""locale"":""fa""}"
End Function

Private Function BuildPricingRequest(ByVal operationName As String, _
                                     ByVal requestID As String, _
                                     ByVal previewDigest As String, _
                                     ByVal includeConfirmation As Boolean) As String
    Dim settings As Worksheet
    Dim body As String
    Dim profitPercent As Variant

    Set settings = ConfigSheet()
    profitPercent = Empty
    If IsNumeric(settings.Range("B21").Value2) Then
        profitPercent = CDbl(settings.Range("B21").Value2) * 100#
    End If

    body = "{""schema"":" & JsonString(PRICING_REQUEST_SCHEMA) & "," & _
        """schema_version"":1," & _
        """operation"":" & JsonString(operationName) & "," & _
        """idempotency_key"":" & JsonString(requestID) & "," & _
        """expected_state_revision"":" & _
        JsonString(Trim$(CStr(settings.Range("B14").Value2))) & "," & _
        """settings"":{" & _
        """dollar_price"":" & JsonNumberOrNull(settings.Range("B19").Value2) & "," & _
        """yuan_price"":" & JsonNumberOrNull(settings.Range("B18").Value2) & "," & _
        """effective_date"":" & JsonString(Trim$(CStr(settings.Range("B20").Value2))) & "," & _
        """default_profit_percent"":" & JsonNumberOrNull(profitPercent) & "}," & _
        """product_changes"":[]"
    If Len(previewDigest) > 0 Then
        body = body & ",""preview_digest"":" & JsonString(previewDigest)
    End If
    If includeConfirmation Then
        body = body & ",""confirmation"":""APPLY"""
    End If
    BuildPricingRequest = body & "}"
End Function

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
    Dim sessionText As String
    Dim sessionRoot As JsonValue
    Dim csrfToken As String

    If Not IsAllowedPricingBridgeUrl(endpoint) Then
        Err.Raise vbObjectError + 143, "HttpJson", T("bridge_missing")
    End If
    sessionText = HttpPostJsonRaw( _
        PricingBaseURL() & "/session", "{}", "", "", "")
    Set sessionRoot = JsonRuntime.ParseJson(sessionText)
    If sessionRoot.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(sessionRoot, "schema")) <> PRICING_SESSION_SCHEMA Then
        Err.Raise vbObjectError + 144, "HttpJson", T("bridge_missing")
    End If
    csrfToken = Trim$(CStr( _
        JsonRuntime.JsonText(sessionRoot, "csrf_token")))
    If Len(csrfToken) <> 43 Then
        Err.Raise vbObjectError + 145, "HttpJson", T("bridge_missing")
    End If
    HttpJson = HttpPostJsonRaw( _
        endpoint, requestBody, csrfToken, idempotencyKey, expectedRevision)
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
    Set PriceSheet = ThisWorkbook.Worksheets(U("064406CC0633062A0020064206CC0645062A"))
End Function

Private Function ConfigSheet() As Worksheet
    Set ConfigSheet = ThisWorkbook.Worksheets(U("062A0646063806CC06450627062A"))
End Function

Private Function SyncSheet() As Worksheet
    Set SyncSheet = ThisWorkbook.Worksheets( _
        U("062F0627062F0647200C0647062706CC00200647064506AF06270645200C06330627063206CC"))
End Function

Private Function IsAdvanced() As Boolean
    IsAdvanced = LCase$(Trim$(CStr( _
        ConfigSheet().Range("G8").Value2))) = "advanced"
End Function

Private Sub ValidateUnicodeRuntime()
    Dim expected As String

    expected = U("0647064506AF06270645")
    If Len(expected) <> 5 Or AscW(Left$(expected, 1)) <> &H647 Then
        Err.Raise vbObjectError + 190, "ValidateUnicodeRuntime", _
                  "Unicode runtime validation failed."
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
