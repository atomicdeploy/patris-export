Attribute VB_Name = "PatrisDashboard"
Option Explicit

Private Const DASHBOARD_SHEET As String = "Dashboard"
Private Const PRODUCTS_SHEET As String = "Products"
Private Const SETTINGS_SHEET As String = "Settings"
Private Const PRODUCTS_TABLE As String = "tblProducts"
Private Const PRODUCT_COLUMN_COUNT As Long = 25
Private Const MAX_DIGITALOGIC_PAGES As Long = 100
Private Const DIGITALOGIC_HOST_PREFIX As String = "https://digitalogic.ir/"

Public Sub RefreshAllData(Optional ByVal silent As Boolean = False)
    Dim previousCalculation As XlCalculation
    Dim patrisRows As Long
    Dim digitalogicStatus As String
    Dim refreshMessage As String

    On Error GoTo Failed
    previousCalculation = Application.Calculation
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual

    patrisRows = RefreshPatrisContract()
    digitalogicStatus = RefreshDigitalogicSafely()
    ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B5").Value = Now
    ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B8").Value = digitalogicStatus
    UpdateLiveConfiguration
    RefreshDashboard

    refreshMessage = CStr(patrisRows) & " Patris product row(s) refreshed." & vbCrLf & digitalogicStatus
    If Not silent Then
        MsgBox refreshMessage, vbInformation, "Patris / Digitalogic sync"
    End If

CleanExit:
    Application.Calculation = previousCalculation
    Application.EnableEvents = True
    Application.ScreenUpdating = True
    Exit Sub

Failed:
    ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B8").Value = "Refresh failed: " & SafeErrorMessage(Err.Description)
    If Not silent Then
        MsgBox "Refresh was not completed: " & SafeErrorMessage(Err.Description), _
               vbExclamation, "Patris / Digitalogic sync"
    End If
    Resume CleanExit
End Sub

Public Sub RefreshOnOpen()
    Dim autoRefresh As String
    autoRefresh = LCase$(Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B11").Value2)))
    If autoRefresh = "yes" Or autoRefresh = "true" Or autoRefresh = "1" Then
        RefreshAllData True
    End If
End Sub

Public Function RefreshPatrisContract() As Long
    Dim endpoint As String
    Dim responseText As String
    Dim root As JsonValue
    Dim productsValue As JsonValue
    Dim categoriesValue As JsonValue
    Dim product As JsonValue
    Dim categoryNames As Object
    Dim headersSeen As Object
    Dim products As Worksheet
    Dim table As ListObject
    Dim output() As Variant
    Dim manualInputs As Object
    Dim manualValues As Variant
    Dim rowIndex As Long
    Dim dataRows As Long
    Dim codeValue As String
    Dim categoryCode As String
    Dim warehouseStock As JsonValue
    Dim mode As String

    endpoint = Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B2").Value2))
    If Len(endpoint) = 0 Then
        Err.Raise vbObjectError + 100, "RefreshPatrisContract", "The Patris product-sync endpoint is empty."
    End If
    responseText = HttpGet(endpoint, "application/vnd.patris.product-sync+json, application/json", False)
    Set root = JsonRuntime.ParseJson(responseText)
    If root.Kind <> "object" Or CStr(JsonRuntime.JsonText(root, "schema")) <> "patris.product-sync" Then
        Err.Raise vbObjectError + 101, "RefreshPatrisContract", _
                  "The Patris endpoint did not return a patris.product-sync envelope."
    End If

    Set productsValue = JsonRuntime.JsonMember(root, "products")
    If productsValue Is Nothing Or productsValue.Kind <> "array" Then
        Err.Raise vbObjectError + 102, "RefreshPatrisContract", _
                  "The Patris product-sync envelope is missing its products array."
    End If
    dataRows = JsonRuntime.JsonArrayCount(productsValue)
    If dataRows < 1 Then
        Err.Raise vbObjectError + 103, "RefreshPatrisContract", _
                  "The Patris product-sync envelope contains no products."
    End If

    Set categoriesValue = JsonRuntime.JsonMember(root, "categories")
    Set categoryNames = BuildCategoryNames(categoriesValue)
    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    Set manualInputs = CaptureManualInputs(table)
    Set headersSeen = CreateObject("Scripting.Dictionary")
    headersSeen.CompareMode = vbBinaryCompare
    ReDim output(1 To dataRows, 1 To PRODUCT_COLUMN_COUNT)

    For rowIndex = 1 To dataRows
        Set product = JsonRuntime.JsonArrayItem(productsValue, rowIndex)
        If product Is Nothing Or product.Kind <> "object" Then
            Err.Raise vbObjectError + 104, "RefreshPatrisContract", _
                      "Patris product " & CStr(rowIndex) & " is not a JSON object."
        End If
        codeValue = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(product, "product_code"))))
        If Len(codeValue) = 0 Then
            Err.Raise vbObjectError + 105, "RefreshPatrisContract", _
                      "The Patris response contains a blank product_code at row " & CStr(rowIndex) & "."
        End If
        If headersSeen.Exists(codeValue) Then
            Err.Raise vbObjectError + 106, "RefreshPatrisContract", _
                      "The Patris response contains duplicate product_code " & codeValue & "."
        End If
        headersSeen.Add codeValue, True

        output(rowIndex, 1) = codeValue
        output(rowIndex, 2) = BlankIfNull(JsonRuntime.JsonText(product, "name"))
        output(rowIndex, 3) = BlankIfNull(JsonRuntime.JsonText(product, "serial"))
        categoryCode = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(product, "category_code"))))
        If categoryNames.Exists(categoryCode) Then
            output(rowIndex, 4) = categoryNames(categoryCode)
        Else
            output(rowIndex, 4) = categoryCode
        End If
        Set warehouseStock = JsonRuntime.JsonMember(product, "warehouse_stock")
        output(rowIndex, 5) = JsonObjectNumberOrBlank(warehouseStock, "1")
        output(rowIndex, 6) = JsonObjectNumberOrBlank(warehouseStock, "2")
        output(rowIndex, 7) = NumericOrBlank(JsonRuntime.JsonText(product, "total_stock"))
        output(rowIndex, 8) = NumericOrBlank(JsonRuntime.JsonText(product, "foreign_price"))
        output(rowIndex, 9) = NumericOrBlank(JsonRuntime.JsonText(product, "weight_grams"))
        output(rowIndex, 10) = BlankIfNull(JsonRuntime.JsonText(product, "shipping_method_id"))
        output(rowIndex, 11) = NumericOrBlank(JsonRuntime.JsonText(product, "shipping_price_per_kg"))
        output(rowIndex, 12) = BlankIfNull(JsonRuntime.JsonText(product, "shipping_price_per_kg_currency"))
        output(rowIndex, 13) = NumericOrBlank(JsonRuntime.JsonText(product, "markup_percent"))
        output(rowIndex, 14) = NumericOrBlank(JsonRuntime.JsonText(product, "irt_per_cny"))
        output(rowIndex, 15) = NumericOrBlank(JsonRuntime.JsonText(product, "final_price"))
        output(rowIndex, 19) = JsonArrayToText(JsonRuntime.JsonMember(product, "warnings"))
        If manualInputs.Exists(codeValue) Then
            manualValues = manualInputs(codeValue)
            output(rowIndex, 20) = manualValues(0)
            output(rowIndex, 21) = manualValues(1)
            output(rowIndex, 22) = manualValues(2)
        End If
        output(rowIndex, 24) = "Patris"
    Next rowIndex

    ReplaceProductTableData table, output, dataRows
    mode = LCase$(Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B4").Value2)))
    ApplyProductTableFormulas table, mode
    ApplyProductTableFormatting table
    RefreshPatrisContract = dataRows
End Function

Private Function RefreshDigitalogicSafely() As String
    On Error GoTo DigitalogicFailed
    RefreshDigitalogicSafely = RefreshDigitalogicCatalog()
    Exit Function

DigitalogicFailed:
    RefreshDigitalogicSafely = "Digitalogic unavailable: " & SafeErrorMessage(Err.Description)
End Function

Public Function RefreshDigitalogicCatalog() As String
    Dim settings As Worksheet
    Dim endpoint As String
    Dim keyName As String
    Dim secretName As String
    Dim consumerKey As String
    Dim consumerSecret As String
    Dim responseText As String
    Dim root As JsonValue
    Dim data As JsonValue
    Dim rowsValue As JsonValue
    Dim pagination As JsonValue
    Dim wooRow As JsonValue
    Dim wooByCode As Object
    Dim seenSyncKeys As Object
    Dim allWooRows As New Collection
    Dim matchedCodes As Object
    Dim page As Long
    Dim pages As Long
    Dim rowIndex As Long
    Dim syncKey As String
    Dim patrisCode As String
    Dim matched As Long
    Dim appended As Long

    Set settings = ThisWorkbook.Worksheets(SETTINGS_SHEET)
    endpoint = Trim$(CStr(settings.Range("B3").Value2))
    If Len(endpoint) = 0 Then
        RefreshDigitalogicCatalog = "Digitalogic skipped: endpoint is not configured."
        Exit Function
    End If
    If LCase$(Left$(endpoint, Len(DIGITALOGIC_HOST_PREFIX))) <> DIGITALOGIC_HOST_PREFIX Then
        Err.Raise vbObjectError + 120, "RefreshDigitalogicCatalog", _
                  "Digitalogic credentials may only be sent to https://digitalogic.ir/."
    End If

    keyName = Trim$(CStr(settings.Range("B9").Value2))
    secretName = Trim$(CStr(settings.Range("B10").Value2))
    If Len(keyName) = 0 Or Len(secretName) = 0 Then
        Err.Raise vbObjectError + 121, "RefreshDigitalogicCatalog", _
                  "The Digitalogic credential environment-variable names are not configured."
    End If
    consumerKey = Environ$(keyName)
    consumerSecret = Environ$(secretName)
    If Len(consumerKey) = 0 Or Len(consumerSecret) = 0 Then
        RefreshDigitalogicCatalog = "Digitalogic skipped: read-only credentials are not available in the configured environment variables."
        Exit Function
    End If

    Set wooByCode = CreateObject("Scripting.Dictionary")
    wooByCode.CompareMode = vbBinaryCompare
    Set seenSyncKeys = CreateObject("Scripting.Dictionary")
    seenSyncKeys.CompareMode = vbBinaryCompare
    page = 1
    Do
        responseText = HttpGet(BuildDigitalogicPageUrl(endpoint, page), "application/json", True, consumerKey, consumerSecret)
        Set root = JsonRuntime.ParseJson(responseText)
        If root.Kind <> "object" Or Not JsonBoolean(root, "success") Then
            Err.Raise vbObjectError + 122, "RefreshDigitalogicCatalog", _
                      "Digitalogic returned an unsuccessful catalog response."
        End If
        Set data = JsonRuntime.JsonMember(root, "data")
        If data Is Nothing Or data.Kind <> "object" Then
            Err.Raise vbObjectError + 123, "RefreshDigitalogicCatalog", _
                      "Digitalogic catalog response is missing its data object."
        End If
        If CStr(BlankIfNull(JsonRuntime.JsonText(data, "dataset"))) <> "products" Then
            Err.Raise vbObjectError + 124, "RefreshDigitalogicCatalog", _
                      "Digitalogic returned the wrong catalog dataset."
        End If
        Set rowsValue = JsonRuntime.JsonMember(data, "rows")
        If rowsValue Is Nothing Or rowsValue.Kind <> "array" Then
            Err.Raise vbObjectError + 125, "RefreshDigitalogicCatalog", _
                      "Digitalogic catalog response is missing its rows array."
        End If

        For rowIndex = 1 To JsonRuntime.JsonArrayCount(rowsValue)
            Set wooRow = JsonRuntime.JsonArrayItem(rowsValue, rowIndex)
            If wooRow Is Nothing Or wooRow.Kind <> "object" Then
                Err.Raise vbObjectError + 126, "RefreshDigitalogicCatalog", _
                          "A Digitalogic catalog row is not a JSON object."
            End If
            syncKey = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "sync_key"))))
            If Len(syncKey) = 0 Then
                Err.Raise vbObjectError + 127, "RefreshDigitalogicCatalog", _
                          "A Digitalogic catalog row has no sync_key."
            End If
            If seenSyncKeys.Exists(syncKey) Then
                Err.Raise vbObjectError + 128, "RefreshDigitalogicCatalog", _
                          "Digitalogic returned duplicate sync_key " & syncKey & "."
            End If
            seenSyncKeys.Add syncKey, True
            allWooRows.Add wooRow
            patrisCode = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "patris_code"))))
            If Len(patrisCode) > 0 Then
                If wooByCode.Exists(patrisCode) Then
                    Err.Raise vbObjectError + 129, "RefreshDigitalogicCatalog", _
                              "Digitalogic returned duplicate exact Patris Code " & patrisCode & "."
                End If
                wooByCode.Add patrisCode, wooRow
            End If
        Next rowIndex

        Set pagination = JsonRuntime.JsonMember(data, "pagination")
        pages = CLng(Val(CStr(BlankIfNull(JsonRuntime.JsonText(pagination, "pages")))))
        If pages < 1 Then pages = page
        If page >= pages Then Exit Do
        page = page + 1
        If page > MAX_DIGITALOGIC_PAGES Then
            Err.Raise vbObjectError + 130, "RefreshDigitalogicCatalog", _
                      "Digitalogic pagination exceeded the workbook safety limit."
        End If
    Loop

    consumerKey = vbNullString
    consumerSecret = vbNullString
    Set matchedCodes = CreateObject("Scripting.Dictionary")
    matchedCodes.CompareMode = vbBinaryCompare
    matched = EnrichPatrisRowsFromWoo(wooByCode, matchedCodes)
    appended = AppendUnmatchedWooRows(allWooRows, matchedCodes)
    ApplyProductTableFormatting ThisWorkbook.Worksheets(PRODUCTS_SHEET).ListObjects(PRODUCTS_TABLE)
    RefreshDigitalogicCatalog = "Digitalogic: " & CStr(allWooRows.Count) & _
        " product(s), " & CStr(matched) & " exact match(es), " & _
        CStr(appended) & " WooCommerce-only row(s)."
End Function

Private Function BuildCategoryNames(ByVal categoriesValue As JsonValue) As Object
    Dim result As Object
    Dim item As JsonValue
    Dim index As Long
    Dim codeValue As String

    Set result = CreateObject("Scripting.Dictionary")
    result.CompareMode = vbBinaryCompare
    If categoriesValue Is Nothing Or categoriesValue.Kind <> "array" Then
        Set BuildCategoryNames = result
        Exit Function
    End If
    For index = 1 To JsonRuntime.JsonArrayCount(categoriesValue)
        Set item = JsonRuntime.JsonArrayItem(categoriesValue, index)
        codeValue = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(item, "category_code"))))
        If Len(codeValue) > 0 And Not result.Exists(codeValue) Then
            result.Add codeValue, BlankIfNull(JsonRuntime.JsonText(item, "name"))
        End If
    Next index
    Set BuildCategoryNames = result
End Function

Private Sub ReplaceProductTableData(ByVal table As ListObject, ByRef output() As Variant, ByVal dataRows As Long)
    Dim products As Worksheet

    Set products = table.Parent
    If Not table.DataBodyRange Is Nothing Then table.DataBodyRange.Delete
    table.Resize products.Range("A1:Y" & CStr(dataRows + 1))
    products.Range("A2").Resize(dataRows, PRODUCT_COLUMN_COUNT).Value = output
End Sub

Private Function CaptureManualInputs(ByVal table As ListObject) As Object
    Dim result As Object
    Dim rowIndex As Long
    Dim codeColumn As Long
    Dim overrideColumn As Long
    Dim statusColumn As Long
    Dim notesColumn As Long
    Dim codeValue As String

    Set result = CreateObject("Scripting.Dictionary")
    result.CompareMode = vbBinaryCompare
    If table.DataBodyRange Is Nothing Then
        Set CaptureManualInputs = result
        Exit Function
    End If
    codeColumn = table.ListColumns("Code").Index
    overrideColumn = table.ListColumns("Manual Price Override (IRT)").Index
    statusColumn = table.ListColumns("Review Status").Index
    notesColumn = table.ListColumns("Notes").Index
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        codeValue = CStr(table.DataBodyRange.Cells(rowIndex, codeColumn).Value2)
        If Len(codeValue) > 0 Then
            result(codeValue) = Array( _
                table.DataBodyRange.Cells(rowIndex, overrideColumn).Value2, _
                table.DataBodyRange.Cells(rowIndex, statusColumn).Value2, _
                table.DataBodyRange.Cells(rowIndex, notesColumn).Value2)
        End If
    Next rowIndex
    Set CaptureManualInputs = result
End Function

Private Sub ApplyProductTableFormulas(ByVal table As ListObject, ByVal mode As String)
    Dim dataRows As Long
    Dim products As Worksheet
    Dim lastRow As Long

    If table.DataBodyRange Is Nothing Then Exit Sub
    Set products = table.Parent
    dataRows = table.DataBodyRange.Rows.Count
    lastRow = dataRows + 1
    If mode = "formula" Then
        products.Range("O2:O" & CStr(lastRow)).FormulaR1C1 = _
            "=IF(AND(COUNT(RC[-7],RC[-6],RC[-4],RC[-2],RC[-1])=5," & _
            "OR(RC[-3]=""CNY"",RC[-3]=""IRR""))," & _
            "ROUND((RC[-7]*RC[-1]+RC[-6]/1000*IF(RC[-3]=""CNY""," & _
            "RC[-4]*RC[-1],RC[-4]/10))*(1+RC[-2]/100),0),"""")"
    End If
    products.Range("W2:W" & CStr(lastRow)).FormulaR1C1 = _
        "=IF(COUNT(RC[-3])=1,RC[-3],IF(COUNT(RC[-8])=1,RC[-8]," & _
        "IF(COUNT(RC[-6])=1,RC[-6],"""")))"
    products.Range("Y2:Y" & CStr(lastRow)).FormulaR1C1 = _
        "=LOWER(RC[-24]&"" ""&RC[-23]&"" ""&RC[-22]&"" ""&RC[-9]&"" ""&RC[-6]&"" ""&RC[-4]&"" ""&RC[-3])"
End Sub

Private Sub ApplyProductTableFormatting(ByVal table As ListObject)
    Dim products As Worksheet
    Dim lastRow As Long

    If table.DataBodyRange Is Nothing Then Exit Sub
    Set products = table.Parent
    lastRow = table.DataBodyRange.Rows.Count + 1
    products.Range("A2:A" & CStr(lastRow)).NumberFormat = "@"
    products.Range("E2:N" & CStr(lastRow)).NumberFormat = "#,##0.########"
    products.Range("O2:O" & CStr(lastRow)).NumberFormat = "#,##0"
    products.Range("Q2:R" & CStr(lastRow)).NumberFormat = "#,##0.########"
    products.Range("T2:T" & CStr(lastRow)).NumberFormat = "#,##0"
    products.Range("W2:W" & CStr(lastRow)).NumberFormat = "#,##0"
    products.Range("T2:V" & CStr(lastRow)).Interior.Color = RGB(255, 247, 214)
    products.Range("U2:U" & CStr(lastRow)).Validation.Delete
    products.Range("U2:U" & CStr(lastRow)).Validation.Add _
        Type:=xlValidateList, AlertStyle:=xlValidAlertStop, _
        Operator:=xlBetween, Formula1:="Needs review,Approved,Blocked"
End Sub

Private Function EnrichPatrisRowsFromWoo(ByVal wooByCode As Object, ByVal matchedCodes As Object) As Long
    Dim products As Worksheet
    Dim table As ListObject
    Dim rowIndex As Long
    Dim sheetRow As Long
    Dim codeValue As String
    Dim wooRow As JsonValue

    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Function
    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        codeValue = CStr(table.DataBodyRange.Cells(rowIndex, 1).Value2)
        If wooByCode.Exists(codeValue) Then
            Set wooRow = wooByCode(codeValue)
            sheetRow = table.DataBodyRange.Row + rowIndex - 1
            ApplyWooRow products, sheetRow, wooRow, True
            matchedCodes(codeValue) = True
            EnrichPatrisRowsFromWoo = EnrichPatrisRowsFromWoo + 1
        Else
            table.DataBodyRange.Cells(rowIndex, 19).Value = CombineStatus( _
                CStr(table.DataBodyRange.Cells(rowIndex, 19).Value2), "No WooCommerce match")
        End If
    Next rowIndex
End Function

Private Function AppendUnmatchedWooRows(ByVal allWooRows As Collection, ByVal matchedCodes As Object) As Long
    Dim products As Worksheet
    Dim table As ListObject
    Dim wooRow As JsonValue
    Dim appendRows As New Collection
    Dim patrisCode As String
    Dim oldCount As Long
    Dim newCount As Long
    Dim index As Long
    Dim sheetRow As Long
    Dim mode As String

    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    For Each wooRow In allWooRows
        patrisCode = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "patris_code"))))
        If Len(patrisCode) = 0 Or Not matchedCodes.Exists(patrisCode) Then
            appendRows.Add wooRow
        End If
    Next wooRow
    If appendRows.Count = 0 Then Exit Function

    If table.DataBodyRange Is Nothing Then
        oldCount = 0
    Else
        oldCount = table.DataBodyRange.Rows.Count
    End If
    newCount = oldCount + appendRows.Count
    table.Resize products.Range("A1:Y" & CStr(newCount + 1))
    For index = 1 To appendRows.Count
        Set wooRow = appendRows(index)
        sheetRow = oldCount + index + 1
        products.Cells(sheetRow, 1).Value = WooDisplayKey(wooRow)
        products.Cells(sheetRow, 2).Value = BlankIfNull(JsonRuntime.JsonText(wooRow, "name"))
        products.Cells(sheetRow, 3).Value = BlankIfNull(JsonRuntime.JsonText(wooRow, "part_number"))
        products.Cells(sheetRow, 4).Value = BlankIfNull(JsonRuntime.JsonText(wooRow, "categories"))
        products.Cells(sheetRow, 7).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "patris_total_stock"))
        products.Cells(sheetRow, 8).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "foreign_price"))
        products.Cells(sheetRow, 9).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "weight_grams"))
        products.Cells(sheetRow, 10).Value = BlankIfNull(JsonRuntime.JsonText(wooRow, "shipping_method_id"))
        products.Cells(sheetRow, 11).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "shipping_price_per_kg"))
        products.Cells(sheetRow, 12).Value = BlankIfNull(JsonRuntime.JsonText(wooRow, "shipping_price_per_kg_currency"))
        products.Cells(sheetRow, 13).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "profit_percent"))
        products.Cells(sheetRow, 15).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "patris_final_price"))
        products.Cells(sheetRow, 24).Value = "WooCommerce"
        ApplyWooRow products, sheetRow, wooRow, False
    Next index
    mode = LCase$(Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B4").Value2)))
    ApplyProductTableFormulas table, mode
    AppendUnmatchedWooRows = appendRows.Count
End Function

Private Sub ApplyWooRow(ByVal products As Worksheet, ByVal sheetRow As Long, ByVal wooRow As JsonValue, ByVal matchedPatris As Boolean)
    Dim wooId As String
    Dim permalink As String
    Dim statusText As String
    Dim sourceText As String

    wooId = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "woocommerce_id"))))
    permalink = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "permalink"))))
    products.Cells(sheetRow, 16).Hyperlinks.Delete
    If Len(wooId) > 0 Then
        If IsSafePublicUrl(permalink) Then
            products.Hyperlinks.Add Anchor:=products.Cells(sheetRow, 16), _
                Address:=permalink, TextToDisplay:="WooID " & wooId
        Else
            products.Cells(sheetRow, 16).Value = "WooID " & wooId
        End If
    End If
    products.Cells(sheetRow, 17).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "effective_price"))
    products.Cells(sheetRow, 18).Value = NumericOrBlank(JsonRuntime.JsonText(wooRow, "stock_quantity"))
    statusText = CombineStatus( _
        CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "sync_status"))), _
        CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "sync_error"))))
    If Not matchedPatris Then statusText = CombineStatus(statusText, "WooCommerce only")
    products.Cells(sheetRow, 19).Value = CombineStatus(CStr(products.Cells(sheetRow, 19).Value2), statusText)
    If matchedPatris Then
        sourceText = "Patris + WooCommerce"
    Else
        sourceText = "WooCommerce"
    End If
    products.Cells(sheetRow, 24).Value = sourceText
End Sub

Private Function WooDisplayKey(ByVal wooRow As JsonValue) As String
    WooDisplayKey = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "patris_code"))))
    If Len(WooDisplayKey) = 0 Then
        WooDisplayKey = Trim$(CStr(BlankIfNull(JsonRuntime.JsonText(wooRow, "sync_key"))))
    End If
End Function

Private Sub UpdateLiveConfiguration()
    Dim table As ListObject
    Dim settings As Worksheet

    Set table = ThisWorkbook.Worksheets(PRODUCTS_SHEET).ListObjects(PRODUCTS_TABLE)
    Set settings = ThisWorkbook.Worksheets(SETTINGS_SHEET)
    If table.DataBodyRange Is Nothing Then
        settings.Range("B16:B18").ClearContents
        Exit Sub
    End If
    settings.Range("B16").Value = DistinctNumericSummary(table.ListColumns("IRT per CNY").DataBodyRange)
    settings.Range("B17").Value = DistinctNumericSummaryWhere( _
        table.ListColumns("Shipping Price/kg").DataBodyRange, _
        table.ListColumns("Shipping Currency").DataBodyRange, "CNY")
    settings.Range("B18").Value = DistinctNumericSummary(table.ListColumns("Profit Margin (%)").DataBodyRange)
    settings.Range("B16:B18").NumberFormat = "#,##0.########"
End Sub

Private Function DistinctNumericSummary(ByVal values As Range) As Variant
    Dim distinct As Object
    Dim index As Long
    Dim rawValue As Variant
    Dim numberValue As Double
    Dim key As String
    Dim firstValue As Double

    Set distinct = CreateObject("Scripting.Dictionary")
    distinct.CompareMode = vbBinaryCompare
    For index = 1 To values.Rows.Count
        rawValue = values.Cells(index, 1).Value2
        If Not IsError(rawValue) And Len(Trim$(CStr(rawValue))) > 0 And IsNumeric(rawValue) Then
            numberValue = CDbl(rawValue)
            key = CStr(numberValue)
            If Not distinct.Exists(key) Then
                distinct.Add key, True
                If distinct.Count = 1 Then firstValue = numberValue
            End If
        End If
    Next index
    If distinct.Count = 1 Then
        DistinctNumericSummary = firstValue
    ElseIf distinct.Count > 1 Then
        DistinctNumericSummary = "Mixed (" & CStr(distinct.Count) & ")"
    Else
        DistinctNumericSummary = Empty
    End If
End Function

Private Function DistinctNumericSummaryWhere(ByVal values As Range, _
                                             ByVal conditions As Range, _
                                             ByVal requiredCondition As String) As Variant
    Dim distinct As Object
    Dim index As Long
    Dim rawValue As Variant
    Dim numberValue As Double
    Dim key As String
    Dim firstValue As Double

    Set distinct = CreateObject("Scripting.Dictionary")
    distinct.CompareMode = vbBinaryCompare
    For index = 1 To values.Rows.Count
        If CStr(conditions.Cells(index, 1).Value2) = requiredCondition Then
            rawValue = values.Cells(index, 1).Value2
            If Not IsError(rawValue) And Len(Trim$(CStr(rawValue))) > 0 And IsNumeric(rawValue) Then
                numberValue = CDbl(rawValue)
                key = CStr(numberValue)
                If Not distinct.Exists(key) Then
                    distinct.Add key, True
                    If distinct.Count = 1 Then firstValue = numberValue
                End If
            End If
        End If
    Next index
    If distinct.Count = 1 Then
        DistinctNumericSummaryWhere = firstValue
    ElseIf distinct.Count > 1 Then
        DistinctNumericSummaryWhere = "Mixed (" & CStr(distinct.Count) & ")"
    Else
        DistinctNumericSummaryWhere = Empty
    End If
End Function

Public Sub SearchProducts()
    Dim term As String
    Dim products As Worksheet
    Dim table As ListObject
    Dim matched As Long

    On Error GoTo SearchFailed
    term = LCase$(Trim$(CStr(ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B10").Value2)))
    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Sub
    products.Activate
    If Len(term) > 0 Then
        term = Replace$(Replace$(Replace$(term, "~", "~~"), "*", "~*"), "?", "~?")
        table.Range.AutoFilter Field:=25, Criteria1:="=*" & term & "*"
    Else
        table.Range.AutoFilter Field:=25, Criteria1:="<>"
    End If
    matched = Application.WorksheetFunction.Subtotal(103, table.ListColumns(1).DataBodyRange)
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = matched & " matching product(s)"
    Exit Sub

SearchFailed:
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = "Search failed: " & SafeErrorMessage(Err.Description)
End Sub

Public Sub ResetSearch()
    Dim table As ListObject
    On Error GoTo ResetFailed
    Set table = ThisWorkbook.Worksheets(PRODUCTS_SHEET).ListObjects(PRODUCTS_TABLE)
    If Not table.DataBodyRange Is Nothing Then table.Range.AutoFilter Field:=25, Criteria1:="<>"
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B10").MergeArea.ClearContents
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").MergeArea.ClearContents
    Exit Sub

ResetFailed:
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = "Reset failed: " & SafeErrorMessage(Err.Description)
End Sub

Public Sub ChooseCompanyLogo()
    Dim selectedFile As Variant
    Dim dashboard As Worksheet
    Dim target As Range
    Dim logo As Shape

    selectedFile = Application.GetOpenFilename("Images (*.png;*.jpg;*.jpeg),*.png;*.jpg;*.jpeg", , "Choose company logo")
    If VarType(selectedFile) = vbBoolean Then Exit Sub
    Set dashboard = ThisWorkbook.Worksheets(DASHBOARD_SHEET)
    Set target = dashboard.Range("J1:M3")
    On Error Resume Next
    dashboard.Shapes("CompanyLogo").Delete
    On Error GoTo 0
    Set logo = dashboard.Shapes.AddPicture(CStr(selectedFile), msoFalse, msoTrue, target.Left, target.Top, -1, -1)
    logo.Name = "CompanyLogo"
    logo.LockAspectRatio = msoTrue
    If logo.Width > target.Width Then logo.Width = target.Width
    If logo.Height > target.Height Then logo.Height = target.Height
    logo.Left = target.Left + (target.Width - logo.Width) / 2
    logo.Top = target.Top + (target.Height - logo.Height) / 2
End Sub

Public Sub RefreshDashboard()
    Dim products As Worksheet
    Dim dashboard As Worksheet
    Dim table As ListObject
    Dim chartObject As ChartObject
    Dim emptyChartMessage As Shape
    Dim lastChartRow As Long

    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set dashboard = ThisWorkbook.Worksheets(DASHBOARD_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    dashboard.Calculate
    Set chartObject = dashboard.ChartObjects("PriceChart")
    Set emptyChartMessage = dashboard.Shapes("EmptyChartMessage")
    If table.DataBodyRange Is Nothing Then
        chartObject.Chart.SetSourceData Source:=products.Range("A1:A1")
        emptyChartMessage.Visible = msoTrue
        Exit Sub
    End If
    emptyChartMessage.Visible = msoFalse
    lastChartRow = WorksheetFunction.Min(table.DataBodyRange.Rows.Count + 1, 11)
    With chartObject.Chart
        .SetSourceData Source:=Union(products.Range("B1:B" & CStr(lastChartRow)), _
                                     products.Range("W1:W" & CStr(lastChartRow)))
        .HasTitle = True
        .ChartTitle.Text = "Effective price snapshot (first 10 products)"
    End With
    Application.CalculateFull
End Sub

Private Function HttpGet(ByVal endpoint As String, ByVal acceptHeader As String, _
                         ByVal useBasicAuthentication As Boolean, _
                         Optional ByVal username As String = vbNullString, _
                         Optional ByVal password As String = vbNullString) As String
    Dim http As Object
    Dim timeoutMs As Long

    timeoutMs = CLng(Val(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B6").Value2)) * 1000)
    If timeoutMs < 1000 Then timeoutMs = 15000
    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setTimeouts timeoutMs, timeoutMs, timeoutMs, timeoutMs
    http.Open "GET", endpoint, False
    http.setRequestHeader "Accept", acceptHeader
    If useBasicAuthentication Then
        http.setRequestHeader "Authorization", "Basic " & Base64Encode(username & ":" & password)
    End If
    http.Send
    If http.Status < 200 Or http.Status >= 300 Then
        Err.Raise vbObjectError + 140, "HttpGet", "HTTP " & CStr(http.Status) & " from the configured endpoint."
    End If
    If Len(CStr(http.responseText)) > 16777216 Then
        Err.Raise vbObjectError + 141, "HttpGet", "The configured endpoint returned more than 16 MiB."
    End If
    HttpGet = CStr(http.responseText)
End Function

Private Function Base64Encode(ByVal plainText As String) As String
    Dim document As Object
    Dim node As Object
    Dim bytes() As Byte

    bytes = StrConv(plainText, vbFromUnicode)
    Set document = CreateObject("MSXML2.DOMDocument.6.0")
    Set node = document.createElement("base64")
    node.DataType = "bin.base64"
    node.nodeTypedValue = bytes
    Base64Encode = Replace$(Replace$(node.Text, vbCr, vbNullString), vbLf, vbNullString)
End Function

Private Function BuildDigitalogicPageUrl(ByVal endpoint As String, ByVal page As Long) As String
    Dim separator As String
    If InStr(1, endpoint, "?", vbBinaryCompare) > 0 Then
        separator = "&"
    Else
        separator = "?"
    End If
    BuildDigitalogicPageUrl = endpoint & separator & _
        "dataset=products&locale=bilingual&page=" & CStr(page) & "&limit=100"
End Function

Private Function JsonBoolean(ByVal objectValue As JsonValue, ByVal memberName As String) As Boolean
    Dim member As JsonValue
    Set member = JsonRuntime.JsonMember(objectValue, memberName)
    If member Is Nothing Then Exit Function
    If member.Kind = "boolean" Then JsonBoolean = CBool(member.Scalar)
End Function

Private Function JsonObjectNumberOrBlank(ByVal objectValue As JsonValue, ByVal memberName As String) As Variant
    If objectValue Is Nothing Or objectValue.Kind <> "object" Then
        JsonObjectNumberOrBlank = Empty
    Else
        JsonObjectNumberOrBlank = NumericOrBlank(JsonRuntime.JsonText(objectValue, memberName))
    End If
End Function

Private Function JsonArrayToText(ByVal arrayValue As JsonValue) As String
    Dim index As Long
    Dim item As JsonValue
    Dim itemText As String

    If arrayValue Is Nothing Or arrayValue.Kind <> "array" Then Exit Function
    For index = 1 To JsonRuntime.JsonArrayCount(arrayValue)
        Set item = JsonRuntime.JsonArrayItem(arrayValue, index)
        itemText = Trim$(CStr(BlankIfNull(JsonRuntime.JsonScalar(item))))
        If Len(itemText) > 0 Then
            If Len(JsonArrayToText) > 0 Then JsonArrayToText = JsonArrayToText & "; "
            JsonArrayToText = JsonArrayToText & itemText
        End If
    Next index
End Function

Private Function NumericOrBlank(ByVal value As Variant) As Variant
    Dim text As String
    If IsError(value) Or IsNull(value) Or IsEmpty(value) Then
        NumericOrBlank = Empty
        Exit Function
    End If
    text = Trim$(CStr(value))
    If Len(text) = 0 Or LCase$(text) = "null" Then
        NumericOrBlank = Empty
    ElseIf IsNumeric(text) Then
        NumericOrBlank = CDbl(Val(text))
    Else
        NumericOrBlank = Empty
    End If
End Function

Private Function BlankIfNull(ByVal value As Variant) As Variant
    If IsError(value) Or IsNull(value) Or IsEmpty(value) Then
        BlankIfNull = Empty
    Else
        BlankIfNull = value
    End If
End Function

Private Function CombineStatus(ByVal firstValue As String, ByVal secondValue As String) As String
    firstValue = Trim$(firstValue)
    secondValue = Trim$(secondValue)
    If Len(firstValue) = 0 Then
        CombineStatus = secondValue
    ElseIf Len(secondValue) = 0 Or InStr(1, firstValue, secondValue, vbBinaryCompare) > 0 Then
        CombineStatus = firstValue
    Else
        CombineStatus = firstValue & "; " & secondValue
    End If
End Function

Private Function IsSafePublicUrl(ByVal address As String) As Boolean
    IsSafePublicUrl = LCase$(Left$(address, 8)) = "https://" Or LCase$(Left$(address, 7)) = "http://"
End Function

Private Function SafeErrorMessage(ByVal message As String) As String
    Dim settings As Worksheet
    Dim keyName As String
    Dim secretName As String
    Dim secretValue As String

    Set settings = ThisWorkbook.Worksheets(SETTINGS_SHEET)
    keyName = Trim$(CStr(settings.Range("B9").Value2))
    secretName = Trim$(CStr(settings.Range("B10").Value2))
    If Len(keyName) > 0 Then
        secretValue = Environ$(keyName)
        If Len(secretValue) > 0 Then message = Replace$(message, secretValue, "[redacted]")
    End If
    If Len(secretName) > 0 Then
        secretValue = Environ$(secretName)
        If Len(secretValue) > 0 Then message = Replace$(message, secretValue, "[redacted]")
    End If
    SafeErrorMessage = message
End Function
