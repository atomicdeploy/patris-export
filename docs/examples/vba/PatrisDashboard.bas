Attribute VB_Name = "PatrisDashboard"
Option Explicit

Private Const DASHBOARD_SHEET As String = "Dashboard"
Private Const PRODUCTS_SHEET As String = "Products"
Private Const SETTINGS_SHEET As String = "Settings"
Private Const DIGITALOGIC_RAW_SHEET As String = "Digitalogic Raw"
Private Const PRODUCTS_TABLE As String = "tblProducts"

Public Sub RefreshAllData()
    Dim previousCalculation As XlCalculation
    Dim patrisRows As Long
    Dim digitalogicStatus As String

    On Error GoTo Failed
    previousCalculation = Application.Calculation
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual

    patrisRows = RefreshPatrisCsv()
    digitalogicStatus = RefreshDigitalogicEndpoint()
    ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B5").Value = Now
    ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B8").Value = digitalogicStatus
    RefreshDashboard

    MsgBox CStr(patrisRows) & " product row(s) refreshed." & vbCrLf & digitalogicStatus, _
           vbInformation, "Patris / Digitalogic refresh"

CleanExit:
    Application.Calculation = previousCalculation
    Application.EnableEvents = True
    Application.ScreenUpdating = True
    Exit Sub

Failed:
    MsgBox "Refresh was not completed: " & Err.Description, vbExclamation, "Patris / Digitalogic refresh"
    Resume CleanExit
End Sub

Public Function RefreshPatrisCsv() As Long
    Dim endpoint As String
    Dim csvText As String
    Dim parsed As Collection
    Dim headers As Object
    Dim products As Worksheet
    Dim table As ListObject
    Dim output() As Variant
    Dim rowIndex As Long
    Dim sourceRow As Collection
    Dim mode As String
    Dim dataRows As Long
    Dim manualInputs As Object
    Dim seenCodes As Object
    Dim manualValues As Variant
    Dim codeValue As String

    endpoint = Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B2").Value2))
    If Len(endpoint) = 0 Then Err.Raise vbObjectError + 100, , "The Patris CSV endpoint is empty."
    csvText = HttpGet(endpoint, "text/csv, application/json;q=0.5")
    Set parsed = ParseCsv(csvText)
    If parsed.Count < 2 Then Err.Raise vbObjectError + 101, , "The Patris endpoint returned no product rows."

    Set headers = HeaderIndex(parsed(1))
    If Not HasAnyHeader(headers, "product_code", "Code", "code") Then _
        Err.Raise vbObjectError + 103, , "The Patris CSV response is missing the required Code/product_code header."
    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    Set manualInputs = CaptureManualInputs(table)
    Set seenCodes = CreateObject("Scripting.Dictionary")
    seenCodes.CompareMode = vbBinaryCompare
    dataRows = parsed.Count - 1
    ReDim output(1 To dataRows, 1 To 20)

    For rowIndex = 1 To dataRows
        Set sourceRow = parsed(rowIndex + 1)
        output(rowIndex, 1) = CsvValue(sourceRow, headers, "product_code", "Code", "code")
        output(rowIndex, 2) = CsvValue(sourceRow, headers, "name", "Name")
        output(rowIndex, 3) = CsvValue(sourceRow, headers, "part_number", "Part Number", "serial")
        output(rowIndex, 4) = CsvValue(sourceRow, headers, "category_name", "category_code", "Category")
        output(rowIndex, 5) = WarehouseValue(CsvValue(sourceRow, headers, "warehouse_stock", "ANBAR"), 1)
        output(rowIndex, 6) = WarehouseValue(CsvValue(sourceRow, headers, "warehouse_stock", "ANBAR"), 2)
        output(rowIndex, 7) = NumericOrBlank(CsvValue(sourceRow, headers, "total_stock", "ALLANBAR"))
        output(rowIndex, 8) = NumericOrBlank(CsvValue(sourceRow, headers, "foreign_price"))
        output(rowIndex, 9) = NumericOrBlank(CsvValue(sourceRow, headers, "weight_grams"))
        output(rowIndex, 10) = CsvValue(sourceRow, headers, "shipping_method_id")
        output(rowIndex, 11) = NumericOrBlank(CsvValue(sourceRow, headers, "shipping_price_per_kg"))
        output(rowIndex, 12) = CsvValue(sourceRow, headers, "shipping_price_per_kg_currency")
        output(rowIndex, 13) = NumericOrBlank(CsvValue(sourceRow, headers, "markup_percent"))
        output(rowIndex, 14) = NumericOrBlank(CsvValue(sourceRow, headers, "irt_per_cny"))
        output(rowIndex, 15) = NumericOrBlank(CsvValue(sourceRow, headers, "final_price"))
        codeValue = Trim$(CStr(output(rowIndex, 1)))
        If Len(codeValue) = 0 Then _
            Err.Raise vbObjectError + 104, , "The Patris CSV response contains a blank Code at data row " & CStr(rowIndex) & "."
        If seenCodes.Exists(codeValue) Then _
            Err.Raise vbObjectError + 105, , "The Patris CSV response contains duplicate Code " & codeValue & "."
        seenCodes.Add codeValue, True
        output(rowIndex, 1) = codeValue
        If manualInputs.Exists(codeValue) Then
            manualValues = manualInputs(codeValue)
            output(rowIndex, 16) = manualValues(0)
            output(rowIndex, 17) = manualValues(1)
            output(rowIndex, 18) = manualValues(2)
        End If
        output(rowIndex, 20) = "Patris CSV"
    Next rowIndex

    If Not table.DataBodyRange Is Nothing Then table.DataBodyRange.Delete
    table.Resize products.Range("A1:U" & CStr(dataRows + 1))
    products.Range("A2:A" & CStr(dataRows + 1)).NumberFormat = "@"
    products.Range("A2").Resize(dataRows, 20).Value = output
    products.Range("P2:R" & CStr(dataRows + 1)).Interior.Color = RGB(255, 247, 214)
    products.Range("Q2:Q" & CStr(dataRows + 1)).Validation.Delete
    products.Range("Q2:Q" & CStr(dataRows + 1)).Validation.Add _
        Type:=xlValidateList, AlertStyle:=xlValidAlertStop, _
        Operator:=xlBetween, Formula1:="Needs review,Approved,Blocked"
    products.Range("S2:S" & CStr(dataRows + 1)).FormulaR1C1 = _
        "=IF(ISNUMBER(RC[-3]),RC[-3],RC[-4])"
    products.Range("U2:U" & CStr(dataRows + 1)).FormulaR1C1 = _
        "=LOWER(RC[-20]&"" ""&RC[-19]&"" ""&RC[-18]&"" ""&RC[-4]&"" ""&RC[-3])"

    mode = LCase$(Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B4").Value2)))
    If mode = "formula" Then
        products.Range("O2:O" & CStr(dataRows + 1)).FormulaR1C1 = _
            "=IF(AND(COUNT(RC[-7],RC[-6],RC[-4],RC[-2],RC[-1])=5," & _
            "OR(RC[-3]=""CNY"",RC[-3]=""IRR""))," & _
            "ROUND((RC[-7]*RC[-1]+RC[-6]/1000*IF(RC[-3]=""CNY""," & _
            "RC[-4]*RC[-1],RC[-4]/10))*(1+RC[-2]/100),0),"""")"
    End If
    products.Range("E2:K" & CStr(dataRows + 1)).NumberFormat = "#,##0.########"
    products.Range("M2:N" & CStr(dataRows + 1)).NumberFormat = "#,##0.########"
    products.Range("O2:P" & CStr(dataRows + 1)).NumberFormat = "#,##0"
    products.Range("S2:S" & CStr(dataRows + 1)).NumberFormat = "#,##0"
    RefreshPatrisCsv = dataRows
End Function

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

Public Function RefreshDigitalogicEndpoint() As String
    Dim endpoint As String
    Dim responseText As String
    Dim rawSheet As Worksheet

    endpoint = Trim$(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B3").Value2))
    If Len(endpoint) = 0 Then
        RefreshDigitalogicEndpoint = "Digitalogic endpoint skipped (not configured)."
        Exit Function
    End If

    responseText = Trim$(Replace$(HttpGet(endpoint, "application/json"), ChrW(&HFEFF), vbNullString))
    If Len(responseText) = 0 Then _
        Err.Raise vbObjectError + 106, , "The Digitalogic endpoint returned an empty response."
    If Not IsValidJsonResponse(responseText) Or _
            (Left$(responseText, 1) <> "{" And Left$(responseText, 1) <> "[") Then _
        Err.Raise vbObjectError + 107, , "The Digitalogic endpoint did not return a JSON object or array."
    Set rawSheet = ThisWorkbook.Worksheets(DIGITALOGIC_RAW_SHEET)
    rawSheet.Range("A2").Value = endpoint
    rawSheet.Range("A3").Value = Now
    rawSheet.Range("A4").Value = Left$(responseText, 32767)
    RefreshDigitalogicEndpoint = "Digitalogic endpoint responded (" & Format$(Len(responseText), "#,##0") & " characters)."
End Function

Public Function IsValidJsonResponse(ByVal jsonText As String) As Boolean
    Dim position As Long

    On Error GoTo InvalidJson
    jsonText = Trim$(Replace$(jsonText, ChrW(&HFEFF), vbNullString))
    If Len(jsonText) = 0 Then Exit Function
    position = 1
    If Not ParseJsonValue(jsonText, position, 0) Then Exit Function
    SkipJsonWhitespace jsonText, position
    IsValidJsonResponse = position > Len(jsonText)
    Exit Function

InvalidJson:
    IsValidJsonResponse = False
End Function

Private Function ParseJsonValue(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As Boolean
    Dim character As String

    If depth > 64 Then Exit Function
    SkipJsonWhitespace jsonText, position
    If position > Len(jsonText) Then Exit Function
    character = Mid$(jsonText, position, 1)
    Select Case character
        Case "{"
            ParseJsonValue = ParseJsonObject(jsonText, position, depth)
        Case "["
            ParseJsonValue = ParseJsonArray(jsonText, position, depth)
        Case """"
            ParseJsonValue = ParseJsonString(jsonText, position)
        Case "t"
            ParseJsonValue = ParseJsonLiteral(jsonText, position, "true")
        Case "f"
            ParseJsonValue = ParseJsonLiteral(jsonText, position, "false")
        Case "n"
            ParseJsonValue = ParseJsonLiteral(jsonText, position, "null")
        Case Else
            If character = "-" Or (character >= "0" And character <= "9") Then _
                ParseJsonValue = ParseJsonNumber(jsonText, position)
    End Select
End Function

Private Function ParseJsonObject(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As Boolean
    position = position + 1
    SkipJsonWhitespace jsonText, position
    If JsonCharacterAt(jsonText, position) = "}" Then
        position = position + 1
        ParseJsonObject = True
        Exit Function
    End If

    Do
        If Not ParseJsonString(jsonText, position) Then Exit Function
        SkipJsonWhitespace jsonText, position
        If JsonCharacterAt(jsonText, position) <> ":" Then Exit Function
        position = position + 1
        If Not ParseJsonValue(jsonText, position, depth + 1) Then Exit Function
        SkipJsonWhitespace jsonText, position
        Select Case JsonCharacterAt(jsonText, position)
            Case "}"
                position = position + 1
                ParseJsonObject = True
                Exit Function
            Case ","
                position = position + 1
                SkipJsonWhitespace jsonText, position
            Case Else
                Exit Function
        End Select
    Loop
End Function

Private Function ParseJsonArray(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As Boolean
    position = position + 1
    SkipJsonWhitespace jsonText, position
    If JsonCharacterAt(jsonText, position) = "]" Then
        position = position + 1
        ParseJsonArray = True
        Exit Function
    End If

    Do
        If Not ParseJsonValue(jsonText, position, depth + 1) Then Exit Function
        SkipJsonWhitespace jsonText, position
        Select Case JsonCharacterAt(jsonText, position)
            Case "]"
                position = position + 1
                ParseJsonArray = True
                Exit Function
            Case ","
                position = position + 1
                SkipJsonWhitespace jsonText, position
            Case Else
                Exit Function
        End Select
    Loop
End Function

Private Function ParseJsonString(ByRef jsonText As String, ByRef position As Long) As Boolean
    Dim character As String
    Dim escaped As String
    Dim index As Long

    If JsonCharacterAt(jsonText, position) <> """" Then Exit Function
    position = position + 1
    Do While position <= Len(jsonText)
        character = Mid$(jsonText, position, 1)
        If character = """" Then
            position = position + 1
            ParseJsonString = True
            Exit Function
        End If
        If (CLng(AscW(character)) And &HFFFF&) < 32 Then Exit Function
        If character = "\" Then
            position = position + 1
            If position > Len(jsonText) Then Exit Function
            escaped = Mid$(jsonText, position, 1)
            If escaped = "u" Then
                If position + 4 > Len(jsonText) Then Exit Function
                For index = position + 1 To position + 4
                    If Not IsJsonHexCharacter(Mid$(jsonText, index, 1)) Then Exit Function
                Next index
                position = position + 5
            ElseIf InStr(1, """\/bfnrt", escaped, vbBinaryCompare) > 0 Then
                position = position + 1
            Else
                Exit Function
            End If
        Else
            position = position + 1
        End If
    Loop
End Function

Private Function ParseJsonNumber(ByRef jsonText As String, ByRef position As Long) As Boolean
    If JsonCharacterAt(jsonText, position) = "-" Then position = position + 1
    If JsonCharacterAt(jsonText, position) = "0" Then
        position = position + 1
        If IsJsonDigit(JsonCharacterAt(jsonText, position)) Then Exit Function
    ElseIf IsJsonDigitOneToNine(JsonCharacterAt(jsonText, position)) Then
        Do While IsJsonDigit(JsonCharacterAt(jsonText, position))
            position = position + 1
        Loop
    Else
        Exit Function
    End If

    If JsonCharacterAt(jsonText, position) = "." Then
        position = position + 1
        If Not IsJsonDigit(JsonCharacterAt(jsonText, position)) Then Exit Function
        Do While IsJsonDigit(JsonCharacterAt(jsonText, position))
            position = position + 1
        Loop
    End If

    If LCase$(JsonCharacterAt(jsonText, position)) = "e" Then
        position = position + 1
        If JsonCharacterAt(jsonText, position) = "+" Or JsonCharacterAt(jsonText, position) = "-" Then position = position + 1
        If Not IsJsonDigit(JsonCharacterAt(jsonText, position)) Then Exit Function
        Do While IsJsonDigit(JsonCharacterAt(jsonText, position))
            position = position + 1
        Loop
    End If
    ParseJsonNumber = True
End Function

Private Function ParseJsonLiteral(ByRef jsonText As String, ByRef position As Long, ByVal literal As String) As Boolean
    If Mid$(jsonText, position, Len(literal)) <> literal Then Exit Function
    position = position + Len(literal)
    ParseJsonLiteral = True
End Function

Private Sub SkipJsonWhitespace(ByRef jsonText As String, ByRef position As Long)
    Do While position <= Len(jsonText)
        Select Case Mid$(jsonText, position, 1)
            Case " ", vbTab, vbCr, vbLf
                position = position + 1
            Case Else
                Exit Sub
        End Select
    Loop
End Sub

Private Function JsonCharacterAt(ByRef jsonText As String, ByVal position As Long) As String
    If position >= 1 And position <= Len(jsonText) Then JsonCharacterAt = Mid$(jsonText, position, 1)
End Function

Private Function IsJsonDigit(ByVal character As String) As Boolean
    IsJsonDigit = Len(character) = 1 And character >= "0" And character <= "9"
End Function

Private Function IsJsonDigitOneToNine(ByVal character As String) As Boolean
    IsJsonDigitOneToNine = Len(character) = 1 And character >= "1" And character <= "9"
End Function

Private Function IsJsonHexCharacter(ByVal character As String) As Boolean
    character = LCase$(character)
    IsJsonHexCharacter = (character >= "0" And character <= "9") Or (character >= "a" And character <= "f")
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
        table.Range.AutoFilter Field:=21, Criteria1:="=*" & term & "*"
    Else
        table.Range.AutoFilter Field:=21, Criteria1:="<>"
    End If
    matched = Application.WorksheetFunction.Subtotal(103, table.ListColumns(1).DataBodyRange)
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = matched & " matching product(s)"
    Exit Sub

SearchFailed:
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = "Search failed: " & Err.Description
End Sub

Public Sub ResetSearch()
    Dim products As Worksheet
    Dim table As ListObject
    On Error GoTo ResetFailed
    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    table.Range.AutoFilter Field:=21, Criteria1:="<>"
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B10").MergeArea.ClearContents
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").MergeArea.ClearContents
    Exit Sub

ResetFailed:
    ThisWorkbook.Worksheets(DASHBOARD_SHEET).Range("B11").Value = "Reset failed: " & Err.Description
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
    Dim lastChartRow As Long

    Set products = ThisWorkbook.Worksheets(PRODUCTS_SHEET)
    Set dashboard = ThisWorkbook.Worksheets(DASHBOARD_SHEET)
    Set table = products.ListObjects(PRODUCTS_TABLE)
    dashboard.Calculate

    If table.DataBodyRange Is Nothing Then Exit Sub
    lastChartRow = WorksheetFunction.Min(table.DataBodyRange.Rows.Count + 1, 11)
    Set chartObject = dashboard.ChartObjects("PriceChart")
    With chartObject.Chart
        .SetSourceData Source:=Union(products.Range("B1:B" & CStr(lastChartRow)), products.Range("S1:S" & CStr(lastChartRow)))
        .HasTitle = True
        .ChartTitle.Text = "Effective price snapshot (first 10 products)"
    End With
    Application.CalculateFull
End Sub

Private Function HttpGet(ByVal endpoint As String, ByVal acceptHeader As String) As String
    Dim http As Object
    Dim timeoutMs As Long

    timeoutMs = CLng(Val(CStr(ThisWorkbook.Worksheets(SETTINGS_SHEET).Range("B6").Value2)) * 1000)
    If timeoutMs < 1000 Then timeoutMs = 10000
    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setTimeouts timeoutMs, timeoutMs, timeoutMs, timeoutMs
    http.Open "GET", endpoint, False
    http.setRequestHeader "Accept", acceptHeader
    http.Send
    If http.Status < 200 Or http.Status >= 300 Then
        Err.Raise vbObjectError + 102, , "HTTP " & CStr(http.Status) & " from " & endpoint
    End If
    HttpGet = CStr(http.responseText)
End Function

Private Function ParseCsv(ByVal text As String) As Collection
    Dim rows As New Collection
    Dim currentRow As New Collection
    Dim fieldValue As String
    Dim inQuotes As Boolean
    Dim character As String
    Dim index As Long

    For index = 1 To Len(text)
        character = Mid$(text, index, 1)
        If inQuotes Then
            If character = """" Then
                If index < Len(text) And Mid$(text, index + 1, 1) = """" Then
                    fieldValue = fieldValue & """"
                    index = index + 1
                Else
                    inQuotes = False
                End If
            Else
                fieldValue = fieldValue & character
            End If
        Else
            Select Case character
                Case """"
                    inQuotes = True
                Case ","
                    currentRow.Add fieldValue
                    fieldValue = vbNullString
                Case vbCr, vbLf
                    If character = vbCr And index < Len(text) And Mid$(text, index + 1, 1) = vbLf Then index = index + 1
                    currentRow.Add fieldValue
                    fieldValue = vbNullString
                    If currentRow.Count > 1 Or Len(CStr(currentRow(1))) > 0 Then rows.Add currentRow
                    Set currentRow = New Collection
                Case Else
                    fieldValue = fieldValue & character
            End Select
        End If
    Next index
    If Len(fieldValue) > 0 Or currentRow.Count > 0 Then
        currentRow.Add fieldValue
        rows.Add currentRow
    End If
    Set ParseCsv = rows
End Function

Private Function HeaderIndex(ByVal headerRow As Collection) As Object
    Dim result As Object
    Dim index As Long
    Set result = CreateObject("Scripting.Dictionary")
    result.CompareMode = vbTextCompare
    For index = 1 To headerRow.Count
        result(NormalizeHeader(CStr(headerRow(index)))) = index
    Next index
    Set HeaderIndex = result
End Function

Private Function CsvValue(ByVal sourceRow As Collection, ByVal headers As Object, ParamArray names() As Variant) As Variant
    Dim candidate As Variant
    Dim normalized As String
    Dim index As Long
    For Each candidate In names
        normalized = NormalizeHeader(CStr(candidate))
        If headers.Exists(normalized) Then
            index = CLng(headers(normalized))
            If index <= sourceRow.Count Then
                CsvValue = sourceRow(index)
                Exit Function
            End If
        End If
    Next candidate
    CsvValue = Empty
End Function

Private Function HasAnyHeader(ByVal headers As Object, ParamArray names() As Variant) As Boolean
    Dim candidate As Variant
    For Each candidate In names
        If headers.Exists(NormalizeHeader(CStr(candidate))) Then
            HasAnyHeader = True
            Exit Function
        End If
    Next candidate
End Function

Private Function NormalizeHeader(ByVal value As String) As String
    NormalizeHeader = LCase$(Trim$(Replace$(value, ChrW(&HFEFF), vbNullString)))
End Function

Private Function NumericOrBlank(ByVal value As Variant) As Variant
    Dim text As String
    text = Trim$(CStr(value))
    If Len(text) = 0 Or LCase$(text) = "null" Then
        NumericOrBlank = Empty
    ElseIf IsNumeric(text) Then
        NumericOrBlank = Val(text)
    Else
        NumericOrBlank = Empty
    End If
End Function

Private Function WarehouseValue(ByVal value As Variant, ByVal ordinal As Long) As Variant
    Dim expression As Object
    Dim matches As Object
    Dim text As String
    text = CStr(value)
    Set expression = CreateObject("VBScript.RegExp")
    expression.Global = True
    expression.IgnoreCase = True
    expression.Pattern = """[^""]+""\s*:\s*(-?[0-9]+(\.[0-9]+)?)"
    Set matches = expression.Execute(text)
    If ordinal >= 1 And ordinal <= matches.Count Then
        WarehouseValue = Val(matches(ordinal - 1).SubMatches(0))
    Else
        WarehouseValue = Empty
    End If
End Function
