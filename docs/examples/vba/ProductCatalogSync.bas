Attribute VB_Name = "ProductCatalogSync"
Option Explicit

Private Const PRODUCTS_TABLE As String = "Products"
Private Const YUAN_TABLE As String = "Yuan_Price"
Private Const SHIPPING_TABLE As String = "Shipping"
Private Const PROFIT_TABLE As String = "Profit"
Private Const PRODUCT_COLUMN_COUNT As Long = 8
Private Const MAX_WOO_PAGES As Long = 20
Private Const WOO_PAGE_SIZE As Long = 100
Private Const HTTP_TIMEOUT_MS As Long = 60000
Private Const PRICING_HTTP_TIMEOUT_MS As Long = 240000
Private Const MAX_PRICING_RESPONSE_CHARS As Long = 4194304
Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"
Private Const PRICING_CLIENT_ID As String = "digitalogic-price-calculator/v1"
Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"
Private Const PRICING_REQUEST_SCHEMA As String = "patris.excel-pricing-companion-request/v1"
Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"
Private Const PRICING_STATE_SCHEMA As String = "digitalogic.excel-pricing-sync-state/v1"
Private Const PRICING_PREVIEW_SCHEMA As String = "digitalogic.excel-pricing-sync-preview/v1"
Private Const PRICING_APPLY_SCHEMA As String = "digitalogic.excel-pricing-sync-apply/v1"
Private Const DIGITALOGIC_HOST_PREFIX As String = "https://digitalogic.ir/"
Private Const MB_RIGHT As Long = &H80000
Private Const MB_RTLREADING As Long = &H100000
Private Const MB_YESNO As Long = &H4
Private Const MB_ICONQUESTION As Long = &H20
Private Const IDYES As Long = 6

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
    Dim products As Worksheet
    Dim settings As Worksheet
    Dim table As ListObject
    Dim yuanTable As ListObject
    Dim shippingTable As ListObject
    Dim profitTable As ListObject
    Dim columnIndex As Long

    ValidateUnicodeRuntime
    ValidateProductServiceUrlRuntime
    Set products = PriceSheet()
    Set settings = ConfigSheet()
    Set table = products.ListObjects(PRODUCTS_TABLE)
    Set yuanTable = products.ListObjects(YUAN_TABLE)
    Set shippingTable = products.ListObjects(SHIPPING_TABLE)
    Set profitTable = products.ListObjects(PROFIT_TABLE)

    If table.Range.Row <> 5 Or table.Range.Column <> 2 Then
        Err.Raise vbObjectError + 90, "ValidateWorkbook", _
                  U("062C062F06480644002006A90627064406270647062700200628062706CC062F0020062706320020063306440648064400200042003500200622063A06270632002006340648062F002E")
    End If
    If table.ListColumns.Count <> PRODUCT_COLUMN_COUNT Then
        Err.Raise vbObjectError + 91, "ValidateWorkbook", _
                  U("062C062F06480644002006A90627064406270647062700200628062706CC062F0020062F064206CC06420627064B002006470634062A00200633062A064806460020062F06270634062A06470020062806270634062F002E")
    End If

    For columnIndex = 1 To PRODUCT_COLUMN_COUNT
        If Len(Trim$(CStr( _
            table.HeaderRowRange.Cells(1, columnIndex).Value2))) = 0 Then
            Err.Raise vbObjectError + 92, "ValidateWorkbook", _
                      U("0639064606480627064600200633062A064806460020062C062F06480644002006A9062706440627064706270020062E0627064406CC002006270633062A002E")
        End If
    Next columnIndex

    If settings Is Nothing Then
        Err.Raise vbObjectError + 93, "ValidateWorkbook", _
                  U("0628063106AF06470020062A0646063806CC06450627062A0020067E06CC062F0627002006460634062F002E")
    End If
End Sub

Public Sub RefreshAllData(Optional ByVal silent As Boolean = False)
    Dim previousCalculation As XlCalculation
    Dim productRows As Long
    Dim wooRows As Long
    Dim wooMatches As Long
    Dim duplicateSkus As Long
    Dim wooStatus As String
    Dim statusText As String
    Dim errorText As String
    Dim settings As Worksheet

    On Error GoTo Failed
    previousCalculation = Application.Calculation
    Application.ScreenUpdating = False
    Application.EnableEvents = False
    Application.Calculation = xlCalculationManual

    Set settings = ConfigSheet()
    productRows = RefreshProductContract()
    wooStatus = RefreshWooSafely(wooRows, wooMatches, duplicateSkus)
    Application.CalculateFull

    statusText = U("067E0627062A063106CC0633003A0020") & _
        CStr(productRows) & U("00200631062F06CC0641061B0020") & wooStatus
    If duplicateSkus > 0 Then
        statusText = statusText & U("061B0020") & CStr(duplicateSkus) & _
            U("002006A9062F0020062A06A906310627063106CC00200648064806A90627064506310633002006460627062F06CC062F0647002006AF06310641062A064700200634062F")
    End If
    settings.Range("B6").Value = statusText
    settings.Range("B7").Value = Now
    settings.Range("B7").NumberFormat = "yyyy-mm-dd hh:mm"

    If Not silent Then
        ShowUnicodeMessage CStr(productRows) & _
            U("002006A906270644062706CC0020067E0627062A063106CC0633002006280647200C063106480632063106330627064606CC00200634062F002E") & _
            vbCrLf & CStr(wooMatches) & _
            U("0020067E06CC06480646062F0020062F064206CC064200200648064806A9062706450631063300200627064106320648062F064700200634062F002E"), _
            vbInformation, _
            U("0647064506AF06270645200C06330627063206CC0020064406CC0633062A0020064206CC0645062A")
    End If

CleanExit:
    Application.Calculation = previousCalculation
    Application.EnableEvents = True
    Application.ScreenUpdating = True
    Exit Sub

Failed:
    errorText = SafeStatusError(Err.Description)
    On Error Resume Next
    If Not settings Is Nothing Then
        settings.Range("B6").Value = _
            U("0647064506AF06270645200C06330627063206CC0020064606270645064806410642002006280648062F003A0020") & errorText
    End If
    On Error GoTo 0
    If Not silent Then
        ShowUnicodeMessage U("0647064506AF06270645200C06330627063206CC002006270646062C06270645002006460634062F003A") & _
            vbCrLf & errorText, vbExclamation, _
            U("0647064506AF06270645200C06330627063206CC0020064406CC0633062A0020064206CC0645062A")
    End If
    Resume CleanExit
End Sub

Public Sub RefreshOnOpen()
    Dim autoRefresh As String
    Dim settings As Worksheet

    Set settings = ConfigSheet()
    autoRefresh = Trim$(CStr( _
        settings.Range("B5").Value2))
    If autoRefresh = U("062806440647") Then
        RefreshAllData True
        RefreshPricingState True
    End If
End Sub

Public Sub RefreshPricingState(Optional ByVal silent As Boolean = False)
    Dim root As JsonValue
    Dim currency As JsonValue
    Dim markup As JsonValue
    Dim settings As Worksheet
    Dim dollarValue As Variant
    Dim yuanValue As Variant
    Dim profitValue As Variant
    Dim effectiveDate As String
    Dim stateRevision As String
    Dim errorText As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    Set root = PricingOperation( _
        "state", _
        "{""schema"":""" & PRICING_REQUEST_SCHEMA & _
        """,""schema_version"":1,""operation"":""state""," & _
        """page"":1,""limit"":100,""locale"":""fa""}" _
    )
    RequirePricingSchema root, PRICING_STATE_SCHEMA
    stateRevision = Trim$(CStr(JsonRuntime.JsonText(root, "state_revision")))
    If Not IsRevision(stateRevision) Then
        Err.Raise vbObjectError + 170, "RefreshPricingState", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If

    Set currency = JsonRuntime.JsonMember(root, "currency")
    Set markup = JsonRuntime.JsonMember(root, "default_markup")
    If currency Is Nothing Or markup Is Nothing Then
        Err.Raise vbObjectError + 171, "RefreshPricingState", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If

    dollarValue = PositiveNumericOrBlank( _
        JsonRuntime.JsonText(currency, "dollar_price"))
    yuanValue = PositiveNumericOrBlank( _
        JsonRuntime.JsonText(currency, "yuan_price"))
    effectiveDate = Trim$(CStr( _
        BlankIfNull(JsonRuntime.JsonText(currency, "effective_date"))))
    profitValue = NumericOrBlank( _
        JsonRuntime.JsonText(markup, "profit_percent"))
    If IsEmpty(dollarValue) Or IsEmpty(yuanValue) Or _
       Len(effectiveDate) <> 10 Or IsEmpty(profitValue) Or _
       CDbl(profitValue) < 0 Or CDbl(profitValue) > 1000 Then
        Err.Raise vbObjectError + 172, "RefreshPricingState", _
                  U("064506420627062F06CC06310020062A0646063806CC06450627062A0020064206CC0645062A002006A9062706450644002006CC0627002006450639062A062806310020064606CC0633062A002E")
    End If

    settings.Range("B10").Value2 = CDbl(dollarValue)
    PriceSheet().Range("M7").Value2 = CDbl(yuanValue)
    settings.Range("B12").Value2 = effectiveDate
    PriceSheet().Range("O10").Value2 = CDbl(profitValue) / 100#
    settings.Range("H1").Value2 = stateRevision
    ClearPricingPreview
    settings.Range("B17").Value2 = _
        U("06480636063906CC062A0020064206CC0645062A200C064706270020062F063106CC06270641062A00200634062F002E")
    settings.Range("B18").Value2 = Now
    settings.Range("B18").NumberFormat = "yyyy-mm-dd hh:mm"
    Application.CalculateFull

    If Not silent Then
        ShowUnicodeMessage _
            U("06480636063906CC062A0020064206CC0645062A200C064706270020062F063106CC06270641062A00200634062F002E"), _
            vbInformation, _
            U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
    End If
    Exit Sub

Failed:
    errorText = SafeStatusError(Err.Description)
    On Error Resume Next
    ConfigSheet().Range("B17").Value2 = _
        U("06480636063906CC062A0020064206CC0645062A0020062F063106CC06270641062A002006460634062F003A0020") & errorText
    On Error GoTo 0
    If Not silent Then
        ShowUnicodeMessage _
            U("06480636063906CC062A0020064206CC0645062A0020062F063106CC06270641062A002006460634062F003A0020") & errorText, _
            vbExclamation, _
            U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
    End If
End Sub

Public Sub PreviewPricingSettings()
    Dim settings As Worksheet
    Dim stateRevision As String
    Dim idempotencyKey As String
    Dim settingsJson As String
    Dim fingerprint As String
    Dim body As String
    Dim root As JsonValue
    Dim warnings As JsonValue
    Dim warningCount As Long
    Dim previewDigest As String
    Dim errorText As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    stateRevision = Trim$(CStr(settings.Range("H1").Value2))
    If Not IsRevision(stateRevision) Then
        Err.Raise vbObjectError + 173, "PreviewPricingSettings", _
                  U("06270628062A062F0627002006480636063906CC062A0020064206CC0645062A200C064706270020063106270020062F063106CC06270641062A002006A9064606CC062F002E")
    End If
    settingsJson = CurrentPricingSettingsJSON(fingerprint)
    idempotencyKey = NewPricingIdempotencyKey("preview")
    body = "{""schema"":""" & PRICING_REQUEST_SCHEMA & _
        """,""schema_version"":1,""operation"":""preview""," & _
        """idempotency_key"":""" & idempotencyKey & """," & _
        """expected_state_revision"":""" & stateRevision & """," & _
        """settings"":" & settingsJson & ",""product_changes"":[]}"

    Set root = PricingOperation( _
        "preview", body, idempotencyKey, stateRevision)
    RequirePricingSchema root, PRICING_PREVIEW_SCHEMA
    previewDigest = Trim$(CStr( _
        JsonRuntime.JsonText(root, "preview_digest")))
    If Not IsRevision(previewDigest) Then
        Err.Raise vbObjectError + 174, "PreviewPricingSettings", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If
    Set warnings = JsonRuntime.JsonMember(root, "warnings")
    If Not warnings Is Nothing Then
        warningCount = JsonRuntime.JsonArrayCount(warnings)
    End If
    settings.Range("H2").Value2 = previewDigest
    settings.Range("H3").Value2 = fingerprint
    settings.Range("H4").Value2 = stateRevision
    settings.Range("B17").Value2 = _
        U("067E06CC0634200C06460645062706CC06340020062206450627062F0647002006270633062A002E002006470634062F0627063106470627003A0020") & _
        CStr(warningCount)
    ShowUnicodeMessage CStr(settings.Range("B17").Value2), _
        vbInformation, _
        U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
    Exit Sub

Failed:
    ClearPricingPreview
    errorText = SafeStatusError(Err.Description)
    ConfigSheet().Range("B17").Value2 = _
        U("067E06CC0634200C06460645062706CC0634002006270646062C06270645002006460634062F003A0020") & errorText
    ShowUnicodeMessage CStr(ConfigSheet().Range("B17").Value2), _
        vbExclamation, _
        U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
End Sub

Public Sub ApplyPricingSettings()
    Dim settings As Worksheet
    Dim stateRevision As String
    Dim previewDigest As String
    Dim expectedFingerprint As String
    Dim fingerprint As String
    Dim settingsJson As String
    Dim idempotencyKey As String
    Dim body As String
    Dim root As JsonValue
    Dim errorText As String

    On Error GoTo Failed
    Set settings = ConfigSheet()
    previewDigest = Trim$(CStr(settings.Range("H2").Value2))
    expectedFingerprint = CStr(settings.Range("H3").Value2)
    stateRevision = Trim$(CStr(settings.Range("H4").Value2))
    If Not IsRevision(previewDigest) Or Not IsRevision(stateRevision) Then
        Err.Raise vbObjectError + 175, "ApplyPricingSettings", _
                  U("06270628062A062F06270020067E06CC0634200C06460645062706CC06340020062A063A06CC06CC06310627062A00200631062700200627062C06310627002006A9064606CC062F002E")
    End If
    settingsJson = CurrentPricingSettingsJSON(fingerprint)
    If fingerprint <> expectedFingerprint Or _
       Trim$(CStr(settings.Range("H1").Value2)) <> stateRevision Then
        Err.Raise vbObjectError + 176, "ApplyPricingSettings", _
                  U("064506420627062F06CC06310020067E06330020062706320020067E06CC0634200C06460645062706CC06340020062A063A06CC06CC0631002006A90631062F0647200C06270646062F061B0020062F064806280627063106470020067E06CC0634200C06460645062706CC06340020062806AF06CC063106CC062F002E")
    End If
    If Not ShowUnicodeConfirm( _
        U("062A063A06CC06CC06310627062A0020064206CC0645062A002006270639064506270644002006340648062F061F"), _
        U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")) Then
        Exit Sub
    End If

    idempotencyKey = NewPricingIdempotencyKey("apply")
    body = "{""schema"":""" & PRICING_REQUEST_SCHEMA & _
        """,""schema_version"":1,""operation"":""apply""," & _
        """idempotency_key"":""" & idempotencyKey & """," & _
        """expected_state_revision"":""" & stateRevision & """," & _
        """settings"":" & settingsJson & ",""product_changes"":[]," & _
        """preview_digest"":""" & previewDigest & """," & _
        """confirmation"":""APPLY""}"
    Set root = PricingOperation( _
        "apply", body, idempotencyKey, stateRevision)
    RequirePricingSchema root, PRICING_APPLY_SCHEMA
    ClearPricingPreview
    settings.Range("B17").Value2 = _
        U("062A0646063806CC06450627062A0020064206CC0645062A0020062806270020064506480641064206CC062A00200627063906450627064400200634062F002E")
    RefreshPricingState True
    RefreshAllData True
    ShowUnicodeMessage _
        U("062A0646063806CC06450627062A0020064206CC0645062A0020062806270020064506480641064206CC062A00200627063906450627064400200634062F002E"), _
        vbInformation, _
        U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
    Exit Sub

Failed:
    errorText = SafeStatusError(Err.Description)
    ConfigSheet().Range("B17").Value2 = _
        U("062706390645062706440020062A063A06CC06CC06310627062A002006270646062C06270645002006460634062F003A0020") & errorText
    ShowUnicodeMessage CStr(ConfigSheet().Range("B17").Value2), _
        vbExclamation, _
        U("062706390645062706440020062A0646063806CC06450627062A0020064206CC0645062A")
End Sub

Private Function PricingOperation(ByVal operation As String, _
                                  ByVal requestBody As String, _
                                  Optional ByVal idempotencyKey As String = "", _
                                  Optional ByVal stateRevision As String = "") As JsonValue
    Dim baseUrl As String
    Dim sessionText As String
    Dim responseText As String
    Dim sessionRoot As JsonValue
    Dim csrfToken As String

    baseUrl = PricingBaseURL()
    sessionText = HttpPostJSON( _
        baseUrl & "/session", "{}", "", "", "")
    Set sessionRoot = JsonRuntime.ParseJson(sessionText)
    If sessionRoot.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(sessionRoot, "schema")) <> _
           PRICING_SESSION_SCHEMA Then
        Err.Raise vbObjectError + 177, "PricingOperation", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If
    csrfToken = Trim$(CStr( _
        JsonRuntime.JsonText(sessionRoot, "csrf_token")))
    If Len(csrfToken) <> 43 Then
        Err.Raise vbObjectError + 178, "PricingOperation", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If
    responseText = HttpPostJSON( _
        baseUrl & "/" & operation, requestBody, csrfToken, _
        idempotencyKey, stateRevision)
    Set PricingOperation = JsonRuntime.ParseJson(responseText)
End Function

Private Function HttpPostJSON(ByVal endpoint As String, _
                              ByVal requestBody As String, _
                              ByVal csrfToken As String, _
                              ByVal idempotencyKey As String, _
                              ByVal stateRevision As String) As String
    Dim http As Object

    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setTimeouts _
        PRICING_HTTP_TIMEOUT_MS, PRICING_HTTP_TIMEOUT_MS, _
        PRICING_HTTP_TIMEOUT_MS, PRICING_HTTP_TIMEOUT_MS
    http.Open "POST", endpoint, False
    http.setRequestHeader "Accept", "application/json"
    http.setRequestHeader "Content-Type", "application/json"
    http.setRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID
    If Len(csrfToken) > 0 Then
        http.setRequestHeader PRICING_CSRF_HEADER, csrfToken
    End If
    If Len(idempotencyKey) > 0 Then
        http.setRequestHeader "Idempotency-Key", idempotencyKey
        http.setRequestHeader "If-Match", Chr$(34) & stateRevision & Chr$(34)
    End If
    http.Send requestBody
    If http.Status < 200 Or http.Status >= 300 Then
        Err.Raise vbObjectError + 179, "HttpPostJSON", _
                  U("062F0631062E064806270633062A0020062A0646063806CC06450627062A0020064206CC0645062A0020064606270645064806410642002006280648062F002E")
    End If
    If Len(CStr(http.responseText)) > MAX_PRICING_RESPONSE_CHARS Then
        Err.Raise vbObjectError + 180, "HttpPostJSON", _
                  U("062F0631062E064806270633062A0020062A0646063806CC06450627062A0020064206CC0645062A0020064606270645064806410642002006280648062F002E")
    End If
    HttpPostJSON = CStr(http.responseText)
End Function

Private Function PricingBaseURL() As String
    Dim productUrl As String
    Dim lowerUrl As String
    Dim suffix As String

    productUrl = Trim$(CStr(ConfigSheet().Range("B3").Value2))
    lowerUrl = LCase$(productUrl)
    suffix = "/api/product-sync"
    If Not IsAllowedProductServiceUrl(productUrl) Or _
       Right$(lowerUrl, Len(suffix)) <> suffix Then
        Err.Raise vbObjectError + 181, "PricingBaseURL", _
                  U("064606340627064606CC002006330631064806CC063300200645062D064406CC002006450639062A062806310020064606CC0633062A002E")
    End If
    PricingBaseURL = Left$(productUrl, Len(productUrl) - Len(suffix)) & _
        "/api/excel/pricing-sync"
End Function

Private Function CurrentPricingSettingsJSON( _
    ByRef fingerprint As String) As String
    Dim dollarText As String
    Dim yuanText As String
    Dim dateText As String
    Dim profitText As String
    Dim dollarValue As Double
    Dim yuanValue As Double
    Dim profitValue As Double

    If Not IsNumeric(ConfigSheet().Range("B10").Value2) Or _
       Not IsNumeric(PriceSheet().Range("M7").Value2) Or _
       Not IsNumeric(PriceSheet().Range("O10").Value2) Then
        Err.Raise vbObjectError + 182, "CurrentPricingSettingsJSON", _
                  U("064506420627062F06CC06310020062A0646063806CC06450627062A0020064206CC0645062A002006A9062706450644002006CC0627002006450639062A062806310020064606CC0633062A002E")
    End If
    dollarValue = CDbl(ConfigSheet().Range("B10").Value2)
    yuanValue = CDbl(PriceSheet().Range("M7").Value2)
    profitValue = CDbl(PriceSheet().Range("O10").Value2) * 100#
    dollarText = FormatNumericForText(dollarValue)
    yuanText = FormatNumericForText(yuanValue)
    dateText = PricingDateText(ConfigSheet().Range("B12").Value2)
    profitText = FormatNumericForText(profitValue)
    If dollarValue <= 0 Or yuanValue <= 0 Or _
       Len(dateText) <> 10 Or profitValue < 0 Or _
       profitValue > 1000 Then
        Err.Raise vbObjectError + 183, "CurrentPricingSettingsJSON", _
                  U("064506420627062F06CC06310020062A0646063806CC06450627062A0020064206CC0645062A002006A9062706450644002006CC0627002006450639062A062806310020064606CC0633062A002E")
    End If
    fingerprint = dollarText & "|" & yuanText & "|" & _
        dateText & "|" & profitText
    CurrentPricingSettingsJSON = _
        "{""dollar_price"":" & dollarText & _
        ",""yuan_price"":" & yuanText & _
        ",""effective_date"":""" & dateText & _
        """,""default_profit_percent"":""" & profitText & """}"
End Function

Private Function PricingDateText(ByVal value As Variant) As String
    Dim text As String

    If IsDate(value) Then
        PricingDateText = Format$(CDate(value), "yyyy-mm-dd")
        Exit Function
    End If
    text = Trim$(CStr(value))
    If Len(text) = 10 And Mid$(text, 5, 1) = "-" And _
       Mid$(text, 8, 1) = "-" Then
        PricingDateText = text
    End If
End Function

Private Function NewPricingIdempotencyKey(ByVal operation As String) As String
    Randomize
    NewPricingIdempotencyKey = "excel-" & operation & "-" & _
        Format$(Now, "yyyymmddhhnnss") & "-" & _
        Format$(CLng(Rnd() * 999999#), "000000")
End Function

Private Sub RequirePricingSchema(ByVal root As JsonValue, _
                                 ByVal expectedSchema As String)
    If root Is Nothing Then
        Err.Raise vbObjectError + 184, "RequirePricingSchema", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If
    If root.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(root, "schema")) <> expectedSchema Then
        Err.Raise vbObjectError + 184, "RequirePricingSchema", _
                  U("067E06270633062E002006330631064806CC06330020062A0646063806CC06450627062A0020064206CC0645062A002006450639062A062806310020064606CC0633062A002E")
    End If
End Sub

Private Function IsRevision(ByVal value As String) As Boolean
    Dim position As Long
    Dim character As String

    If Len(value) <> 71 Or Left$(value, 7) <> "sha256:" Then Exit Function
    For position = 8 To 71
        character = Mid$(value, position, 1)
        If InStr(1, "0123456789abcdef", character, _
                 vbBinaryCompare) = 0 Then Exit Function
    Next position
    IsRevision = True
End Function

Private Sub ClearPricingPreview()
    ConfigSheet().Range("H2:H4").ClearContents
End Sub

Public Function RefreshProductContract() As Long
    Dim endpoint As String
    Dim responseText As String
    Dim root As JsonValue
    Dim productsValue As JsonValue
    Dim product As JsonValue
    Dim products As Worksheet
    Dim settings As Worksheet
    Dim table As ListObject
    Dim output() As Variant
    Dim codesSeen As Object
    Dim rowIndex As Long
    Dim dataRows As Long
    Dim codeValue As String
    Dim locationValue As String
    Dim weightValue As Variant
    Dim salePriceValue As Variant
    Dim foreignPriceValue As Variant
    Dim stockValue As Variant

    Set settings = ConfigSheet()
    endpoint = Trim$(CStr(settings.Range("B3").Value2))
    If Not IsAllowedProductServiceUrl(endpoint) Then
        Err.Raise vbObjectError + 100, "RefreshProductContract", _
                  U("064606340627064606CC0020067E0627062A063106CC063300200628062706CC062F00200645062D064406CC00200648002006280627002000480054005400500020062806270634062F002E")
    End If

    responseText = HttpGet( _
        endpoint, _
        "application/vnd.patris.product-sync+json, application/json")
    Set root = JsonRuntime.ParseJson(responseText)
    If root.Kind <> "object" Or _
       CStr(JsonRuntime.JsonText(root, "schema")) <> "patris.product-sync" Then
        Err.Raise vbObjectError + 101, "RefreshProductContract", _
                  U("067E06270633062E0020067E0627062A063106CC063300200642063106270631062F0627062F002006450639062A06280631002006A90627064406270020064606CC0633062A002E")
    End If

    Set productsValue = JsonRuntime.JsonMember(root, "products")
    If productsValue Is Nothing Or productsValue.Kind <> "array" Then
        Err.Raise vbObjectError + 102, "RefreshProductContract", _
                  U("0641064706310633062A002006A9062706440627064706270020062F06310020067E06270633062E0020067E0627062A063106CC06330020067E06CC062F0627002006460634062F002E")
    End If
    dataRows = JsonRuntime.JsonArrayCount(productsValue)
    If dataRows < 1 Then
        Err.Raise vbObjectError + 103, "RefreshProductContract", _
                  U("0641064706310633062A002006A90627064406270647062706CC0020067E0627062A063106CC06330020062E0627064406CC002006270633062A002E")
    End If

    Set codesSeen = CreateObject("Scripting.Dictionary")
    codesSeen.CompareMode = vbBinaryCompare
    ReDim output(1 To dataRows, 1 To PRODUCT_COLUMN_COUNT)

    For rowIndex = 1 To dataRows
        Set product = JsonRuntime.JsonArrayItem(productsValue, rowIndex)
        If product Is Nothing Or product.Kind <> "object" Then
            Err.Raise vbObjectError + 104, "RefreshProductContract", _
                      U("06CC06A906CC00200627063200200631062F06CC0641200C0647062706CC0020067E0627062A063106CC0633002006450639062A062806310020064606CC0633062A002E")
        End If

        codeValue = Trim$(CStr( _
            BlankIfNull(JsonRuntime.JsonText(product, "product_code"))))
        If Len(codeValue) = 0 Then
            Err.Raise vbObjectError + 105, "RefreshProductContract", _
                      U("06A9062F002006A906270644062706CC0020062E0627064406CC0020062F06310020067E06270633062E0020067E0627062A063106CC063300200648062C0648062F0020062F06270631062F002E")
        End If
        If codesSeen.Exists(codeValue) Then
            Err.Raise vbObjectError + 106, "RefreshProductContract", _
                      U("06A9062F002006A906270644062706CC0020062A06A906310627063106CC0020062F06310020067E06270633062E0020067E0627062A063106CC063300200648062C0648062F0020062F06270631062F002E")
        End If
        codesSeen.Add codeValue, True

        weightValue = NumericOrBlank( _
            JsonRuntime.JsonText(product, "weight_grams"))
        locationValue = Trim$(CStr( _
            BlankIfNull(JsonRuntime.JsonText(product, "location"))))
        salePriceValue = PositiveNumericOrBlank( _
            JsonRuntime.JsonText(product, "sale_price_source"))
        foreignPriceValue = NumericOrBlank( _
            JsonRuntime.JsonText(product, "foreign_price"))
        stockValue = NumericOrBlank( _
            JsonRuntime.JsonText(product, "total_stock"))

        output(rowIndex, 1) = Empty
        output(rowIndex, 2) = weightValue
        output(rowIndex, 3) = BuildOtherText(weightValue, locationValue)
        output(rowIndex, 4) = salePriceValue
        output(rowIndex, 5) = foreignPriceValue
        output(rowIndex, 6) = stockValue
        output(rowIndex, 7) = codeValue
        output(rowIndex, 8) = BlankIfNull( _
            JsonRuntime.JsonText(product, "name"))
    Next rowIndex

    Set products = PriceSheet()
    Set table = products.ListObjects(PRODUCTS_TABLE)
    ReplaceProductTableData table, output, dataRows
    ApplyProductTableFormulas table
    ApplyProductTableFormatting table
    RefreshProductContract = dataRows
End Function

Private Function RefreshWooSafely(ByRef totalRows As Long, _
                                  ByRef matchedRows As Long, _
                                  ByRef duplicateSkus As Long) As String
    On Error GoTo WooFailed

    matchedRows = RefreshWooCatalog(totalRows, duplicateSkus)
    RefreshWooSafely = U("0648064806A90627064506310633003A0020") & _
        CStr(totalRows) & U("002006A9062706440627002006480020") & _
        CStr(matchedRows) & U("0020062A0637062806CC06420020062F064206CC0642")
    Exit Function

WooFailed:
    totalRows = 0
    matchedRows = 0
    duplicateSkus = 0
    RefreshWooSafely = _
        U("0648064806A906270645063106330020062F06310020062F0633062A063106330020064606280648062F061B0020062706370644062706390627062A0020067E0627062A063106CC0633002006280647200C063106480632063106330627064606CC00200634062F")
End Function

Private Function RefreshWooCatalog(ByRef totalRows As Long, _
                                   ByRef duplicateSkuCount As Long) As Long
    Dim endpoint As String
    Dim settings As Worksheet
    Dim responseText As String
    Dim root As JsonValue
    Dim wooRow As JsonValue
    Dim wooBySku As Object
    Dim ambiguousSkus As Object
    Dim page As Long
    Dim pageRows As Long
    Dim rowIndex As Long
    Dim skuValue As String

    Set settings = ConfigSheet()
    endpoint = Trim$(CStr(settings.Range("B4").Value2))
    If Not IsAllowedDigitalogicUrl(endpoint) Then
        Err.Raise vbObjectError + 120, "RefreshWooCatalog", _
                  U("064606340627064606CC00200648064806A9062706450631063300200628062706CC062F0020004800540054005000530020064800200645062A0639064406420020062806470020062F06CC062C06CC062A062706440627062C06CC06A90020062806270634062F002E")
    End If

    Set wooBySku = CreateObject("Scripting.Dictionary")
    wooBySku.CompareMode = vbBinaryCompare
    Set ambiguousSkus = CreateObject("Scripting.Dictionary")
    ambiguousSkus.CompareMode = vbBinaryCompare

    For page = 1 To MAX_WOO_PAGES
        responseText = HttpGet( _
            BuildWooPageUrl(endpoint, page), "application/json")
        Set root = JsonRuntime.ParseJson(responseText)
        If root.Kind <> "array" Then
            Err.Raise vbObjectError + 121, "RefreshWooCatalog", _
                      U("067E06270633062E00200648064806A9062706450631063300200641064706310633062A002006450639062A06280631002006A90627064406270020064606CC0633062A002E")
        End If

        pageRows = JsonRuntime.JsonArrayCount(root)
        totalRows = totalRows + pageRows
        For rowIndex = 1 To pageRows
            Set wooRow = JsonRuntime.JsonArrayItem(root, rowIndex)
            If Not wooRow Is Nothing And wooRow.Kind = "object" Then
                skuValue = Trim$(CStr( _
                    BlankIfNull(JsonRuntime.JsonText(wooRow, "sku"))))
                If Len(skuValue) > 0 Then
                    If ambiguousSkus.Exists(skuValue) Then
                        ' Ambiguous SKUs never participate in a join.
                    ElseIf wooBySku.Exists(skuValue) Then
                        wooBySku.Remove skuValue
                        ambiguousSkus.Add skuValue, True
                    Else
                        wooBySku.Add skuValue, wooRow
                    End If
                End If
            End If
        Next rowIndex

        If pageRows < WOO_PAGE_SIZE Then Exit For
    Next page

    duplicateSkuCount = ambiguousSkus.Count
    RefreshWooCatalog = ApplyWooLinks(wooBySku)
End Function

Private Function ApplyWooLinks(ByVal wooBySku As Object) As Long
    Dim products As Worksheet
    Dim table As ListObject
    Dim rowIndex As Long
    Dim codeValue As String
    Dim nameValue As String
    Dim wooId As String
    Dim permalink As String
    Dim wooRow As JsonValue
    Dim nameCell As Range

    Set products = PriceSheet()
    Set table = products.ListObjects(PRODUCTS_TABLE)
    If table.DataBodyRange Is Nothing Then Exit Function

    For rowIndex = 1 To table.DataBodyRange.Rows.Count
        codeValue = CStr(table.DataBodyRange.Cells(rowIndex, 7).Value2)
        If wooBySku.Exists(codeValue) Then
            Set wooRow = wooBySku(codeValue)
            wooId = Trim$(CStr( _
                BlankIfNull(JsonRuntime.JsonText(wooRow, "id"))))
            permalink = Trim$(CStr( _
                BlankIfNull(JsonRuntime.JsonText(wooRow, "permalink"))))
            Set nameCell = table.DataBodyRange.Cells(rowIndex, 8)
            nameValue = CStr(nameCell.Value2)
            nameCell.Hyperlinks.Delete

            If Len(wooId) > 0 Then
                If Len(nameValue) > 0 Then
                    nameValue = nameValue & " " & U("2014") & _
                        " WooID " & wooId
                Else
                    nameValue = "WooID " & wooId
                End If
                If IsAllowedDigitalogicUrl(permalink) Then
                    products.Hyperlinks.Add _
                        Anchor:=nameCell, _
                        Address:=permalink, _
                        TextToDisplay:=nameValue
                Else
                    nameCell.Value = nameValue
                End If
            End If
            ApplyWooLinks = ApplyWooLinks + 1
        End If
    Next rowIndex
End Function

Private Sub ReplaceProductTableData(ByVal table As ListObject, _
                                    ByRef output() As Variant, _
                                    ByVal dataRows As Long)
    Dim products As Worksheet

    Set products = table.Parent
    If Not table.DataBodyRange Is Nothing Then table.DataBodyRange.Delete
    table.Resize products.Range("B5:I" & CStr(dataRows + 5))
    products.Range("H6:H" & CStr(dataRows + 5)).NumberFormat = "@"
    products.Range("B6").Resize(dataRows, PRODUCT_COLUMN_COUNT).Value = output
End Sub

Private Sub ApplyProductTableFormulas(ByVal table As ListObject)
    Dim formulaText As String

    If table.DataBodyRange Is Nothing Then Exit Sub
    formulaText = _
        "=IFERROR(IF(OR(RC[1]="""",RC[4]=""""," & _
        "INDEX(Shipping,1,1)="""",INDEX(Profit,1,1)=""""," & _
        "INDEX(Yuan_Price,1,1)=""""),""""," & _
        "((RC[1]*INDEX(Shipping,1,1)/1000)+RC[4])*" & _
        "(1+INDEX(Profit,1,1))*INDEX(Yuan_Price,1,1)),"""")"
    table.ListColumns(1).DataBodyRange.FormulaR1C1 = formulaText
End Sub

Private Sub ApplyProductTableFormatting(ByVal table As ListObject)
    If table.DataBodyRange Is Nothing Then Exit Sub

    table.ListColumns(1).DataBodyRange.NumberFormat = "#,##0"
    table.ListColumns(2).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(4).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(5).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(6).DataBodyRange.NumberFormat = "#,##0.########"
    table.ListColumns(7).DataBodyRange.NumberFormat = "@"
End Sub

Private Function BuildOtherText(ByVal weightValue As Variant, _
                                ByVal locationValue As String) As String
    If Not IsEmpty(weightValue) Then
        BuildOtherText = FormatNumericForText(CDbl(weightValue)) & _
            U("06AF06310645")
    End If
    If Len(locationValue) > 0 Then
        If Len(BuildOtherText) > 0 Then
            BuildOtherText = BuildOtherText & " " & U("0640") & _
                locationValue
        Else
            BuildOtherText = locationValue
        End If
    End If
End Function

Private Function FormatNumericForText(ByVal value As Double) As String
    Dim decimalSeparator As String

    FormatNumericForText = Format$(value, "0.########")
    decimalSeparator = CStr(Application.International(xlDecimalSeparator))
    If decimalSeparator <> "." Then
        FormatNumericForText = Replace$( _
            FormatNumericForText, decimalSeparator, ".")
    End If
End Function

Private Function BuildWooPageUrl(ByVal endpoint As String, _
                                 ByVal page As Long) As String
    Dim separator As String
    Dim normalizedEndpoint As String

    normalizedEndpoint = LCase$(Trim$(endpoint))
    If Right$(normalizedEndpoint, Len("page=")) = "page=" Then
        BuildWooPageUrl = endpoint & CStr(page)
        Exit Function
    End If

    If InStr(1, endpoint, "?", vbBinaryCompare) > 0 Then
        separator = "&"
    Else
        separator = "?"
    End If
    BuildWooPageUrl = endpoint & separator & _
        "per_page=" & CStr(WOO_PAGE_SIZE) & "&page=" & CStr(page)
End Function

Private Function HttpGet(ByVal endpoint As String, _
                         ByVal acceptHeader As String) As String
    Dim http As Object

    Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")
    http.setTimeouts _
        HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS, HTTP_TIMEOUT_MS
    http.Open "GET", endpoint, False
    http.setRequestHeader "Accept", acceptHeader
    http.Send
    If http.Status < 200 Or http.Status >= 300 Then
        Err.Raise vbObjectError + 140, "HttpGet", _
                  U("06330631064806CC06330020067E06270633062E0020064506480641064200200646062F0627062F002E")
    End If
    If Len(CStr(http.responseText)) > 16777216 Then
        Err.Raise vbObjectError + 141, "HttpGet", _
                  U("062D062C06450020067E06270633062E002006330631064806CC06330020062806CC06340020062706320020062D062F00200645062C06270632002006270633062A002E")
    End If
    HttpGet = CStr(http.responseText)
End Function

Private Function IsAllowedProductServiceUrl(ByVal address As String) As Boolean
    Dim lowerAddress As String
    Dim authority As String
    Dim host As String
    Dim portText As String
    Dim pathPosition As Long
    Dim colonPosition As Long

    lowerAddress = LCase$(Trim$(address))
    If Left$(lowerAddress, Len("http://")) <> "http://" Then Exit Function
    If InStr(1, lowerAddress, "\", vbBinaryCompare) > 0 Or _
       InStr(1, lowerAddress, "@", vbBinaryCompare) > 0 Or _
       InStr(1, lowerAddress, "?", vbBinaryCompare) > 0 Or _
       InStr(1, lowerAddress, "#", vbBinaryCompare) > 0 Or _
       InStr(1, lowerAddress, "%", vbBinaryCompare) > 0 Then Exit Function

    pathPosition = InStr(Len("http://") + 1, lowerAddress, "/", _
                         vbBinaryCompare)
    If pathPosition = 0 Then Exit Function
    authority = Mid$(lowerAddress, Len("http://") + 1, _
                     pathPosition - Len("http://") - 1)
    If Len(authority) = 0 Then Exit Function

    colonPosition = InStr(1, authority, ":", vbBinaryCompare)
    If colonPosition > 0 Then
        If InStr(colonPosition + 1, authority, ":", _
                 vbBinaryCompare) > 0 Then Exit Function
        host = Left$(authority, colonPosition - 1)
        portText = Mid$(authority, colonPosition + 1)
        If Not IsStrictTcpPort(portText) Then Exit Function
    Else
        host = authority
    End If
    If host <> "127.0.0.1" And host <> "localhost" Then Exit Function
    IsAllowedProductServiceUrl = True
End Function

Private Function IsStrictTcpPort(ByVal value As String) As Boolean
    Dim position As Long
    Dim character As String
    Dim portNumber As Long

    If Len(value) < 1 Or Len(value) > 5 Then Exit Function
    For position = 1 To Len(value)
        character = Mid$(value, position, 1)
        If character < "0" Or character > "9" Then Exit Function
    Next position
    portNumber = CLng(value)
    IsStrictTcpPort = (portNumber >= 1 And portNumber <= 65535)
End Function

Private Sub ValidateProductServiceUrlRuntime()
    Dim candidate As Variant

    For Each candidate In Array( _
        "http://127.0.0.1/api/product-sync", _
        "http://127.0.0.1:18080/api/product-sync", _
        "http://localhost/api/product-sync", _
        "http://localhost:18080/api/product-sync")
        If Not IsAllowedProductServiceUrl(CStr(candidate)) Then
            Err.Raise vbObjectError + 185, _
                      "ValidateProductServiceUrlRuntime", _
                      "A valid loopback product-service URL was rejected."
        End If
    Next candidate
    For Each candidate In Array( _
        "http://localhost:x@attacker.example/api/product-sync", _
        "http://127.0.0.1@attacker.example/api/product-sync", _
        "http://localhost:0/api/product-sync", _
        "http://localhost:65536/api/product-sync", _
        "http://localhost:18080/api/product-sync?target=remote", _
        "http://localhost:18080\@attacker.example/api/product-sync", _
        "https://localhost:18080/api/product-sync")
        If IsAllowedProductServiceUrl(CStr(candidate)) Then
            Err.Raise vbObjectError + 186, _
                      "ValidateProductServiceUrlRuntime", _
                      "A non-loopback product-service URL was accepted."
        End If
    Next candidate
End Sub

Private Function IsAllowedDigitalogicUrl(ByVal address As String) As Boolean
    IsAllowedDigitalogicUrl = _
        LCase$(Left$(Trim$(address), Len(DIGITALOGIC_HOST_PREFIX))) = _
        DIGITALOGIC_HOST_PREFIX
End Function

Private Function PositiveNumericOrBlank(ByVal value As Variant) As Variant
    Dim numericValue As Variant

    numericValue = NumericOrBlank(value)
    If IsEmpty(numericValue) Or CDbl(numericValue) <= 0 Then
        PositiveNumericOrBlank = Empty
    Else
        PositiveNumericOrBlank = numericValue
    End If
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

Private Function SafeStatusError(ByVal message As String) As String
    message = Replace$(message, vbCr, " ")
    message = Replace$(message, vbLf, " ")
    message = Trim$(message)
    If Len(message) = 0 Then
        message = U("062E0637062706CC00200646062706450634062E0635")
    End If
    If Len(message) > 300 Then message = Left$(message, 300)
    SafeStatusError = message
End Function

Private Function PriceSheet() As Worksheet
    Set PriceSheet = ThisWorkbook.Worksheets(1)
End Function

Private Function ConfigSheet() As Worksheet
    Set ConfigSheet = ThisWorkbook.Worksheets(2)
End Function

Private Sub ValidateUnicodeRuntime()
    Dim sample As String

    sample = U("06CC06A906AF")
    If Len(sample) <> 3 Or _
       AscW(Mid$(sample, 1, 1)) <> &H6CC Or _
       AscW(Mid$(sample, 2, 1)) <> &H6A9 Or _
       AscW(Mid$(sample, 3, 1)) <> &H6AF Then
        Err.Raise vbObjectError + 159, "ValidateUnicodeRuntime", _
                  "The VBA Unicode runtime is unavailable."
    End If
End Sub

Private Sub ShowUnicodeMessage(ByVal message As String, _
                               ByVal style As VbMsgBoxStyle, _
                               ByVal title As String)
    Dim ignoredResult As Long

#If VBA7 Then
    ignoredResult = MessageBoxW( _
        CLngPtr(Application.hwnd), StrPtr(message), StrPtr(title), _
        CLng(style) Or MB_RIGHT Or MB_RTLREADING)
#Else
    ignoredResult = MessageBoxW( _
        Application.hwnd, StrPtr(message), StrPtr(title), _
        CLng(style) Or MB_RIGHT Or MB_RTLREADING)
#End If
End Sub

Private Function ShowUnicodeConfirm(ByVal message As String, _
                                    ByVal title As String) As Boolean
    Dim result As Long

#If VBA7 Then
    result = MessageBoxW( _
        CLngPtr(Application.hwnd), StrPtr(message), StrPtr(title), _
        MB_YESNO Or MB_ICONQUESTION Or MB_RIGHT Or MB_RTLREADING)
#Else
    result = MessageBoxW( _
        Application.hwnd, StrPtr(message), StrPtr(title), _
        MB_YESNO Or MB_ICONQUESTION Or MB_RIGHT Or MB_RTLREADING)
#End If
    ShowUnicodeConfirm = (result = IDYES)
End Function

Private Function U(ByVal hexCodePoints As String) As String
    Dim position As Long
    Dim codePoint As Long

    hexCodePoints = Replace$(hexCodePoints, " ", vbNullString)
    If Len(hexCodePoints) Mod 4 <> 0 Then
        Err.Raise vbObjectError + 160, "U", _
                  "Invalid Unicode code-point sequence."
    End If
    For position = 1 To Len(hexCodePoints) Step 4
        codePoint = CLng("&H" & Mid$(hexCodePoints, position, 4))
        U = U & ChrW$(codePoint)
    Next position
End Function

Private Function BlankIfNull(ByVal value As Variant) As Variant
    If IsError(value) Or IsNull(value) Or IsEmpty(value) Then
        BlankIfNull = Empty
    Else
        BlankIfNull = value
    End If
End Function
