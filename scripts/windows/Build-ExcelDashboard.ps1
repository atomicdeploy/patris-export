[CmdletBinding()]
param(
    [string]$OutputPath,
    [string]$DistributionCopyPath,
    [string]$PreviewDirectory,
    [string]$ChecksumManifestPath,
    [string]$LogoPath
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$canonicalFileName = 'لیست قیمت دیجیتالاجیک.xltm'
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path $repoRoot (Join-Path 'docs\examples' $canonicalFileName)
}
if ([string]::IsNullOrWhiteSpace($DistributionCopyPath)) {
    $DistributionCopyPath = Join-Path $repoRoot (Join-Path 'outputs\patris-excel-15' $canonicalFileName)
}
if ([string]::IsNullOrWhiteSpace($PreviewDirectory)) {
    $PreviewDirectory = Join-Path $repoRoot 'outputs\patris-excel-15\preview\canonical'
}
if ([string]::IsNullOrWhiteSpace($ChecksumManifestPath)) {
    $ChecksumManifestPath = Join-Path $repoRoot 'outputs\patris-excel-15\checksums\SHA256SUMS-price-calculators.txt'
}
if ([string]::IsNullOrWhiteSpace($LogoPath)) {
    $LogoPath = Join-Path $repoRoot 'docs\examples\assets\digitalogic-logo.svg'
}
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
$DistributionCopyPath = [IO.Path]::GetFullPath($DistributionCopyPath)
$PreviewDirectory = [IO.Path]::GetFullPath($PreviewDirectory)
$ChecksumManifestPath = [IO.Path]::GetFullPath($ChecksumManifestPath)
$LogoPath = [IO.Path]::GetFullPath($LogoPath)
if (-not (Test-Path -LiteralPath $LogoPath -PathType Leaf)) {
    throw "The official Digitalogic logo is missing: $LogoPath"
}
$vbaModulePath = Join-Path $repoRoot 'docs\examples\vba\ProductCatalogSync.bas'
$jsonRuntimePath = Join-Path $repoRoot 'docs\examples\vba\JsonRuntime.bas'
$jsonValuePath = Join-Path $repoRoot 'docs\examples\vba\JsonValue.cls'
$asyncWinHttpRequestPath = Join-Path $repoRoot 'docs\examples\vba\AsyncWinHttpRequest.cls'
$pricingSseParserPath = Join-Path $repoRoot 'docs\examples\vba\PricingSseParser.cls'
$thisWorkbookPath = Join-Path $repoRoot 'docs\examples\vba\ThisWorkbook.cls'
$templateDataAuditPath = Join-Path $PSScriptRoot 'Test-ExcelTemplateDataFree.ps1'
foreach ($requiredVbaSource in @(
    $vbaModulePath,
    $jsonRuntimePath,
    $jsonValuePath,
    $asyncWinHttpRequestPath,
    $pricingSseParserPath,
    $thisWorkbookPath
)) {
    if (-not (Test-Path -LiteralPath $requiredVbaSource -PathType Leaf)) {
        throw "Required VBA source is missing: $requiredVbaSource"
    }
}
if (-not (Test-Path -LiteralPath $templateDataAuditPath -PathType Leaf)) {
    throw "Required empty-template release gate is missing: $templateDataAuditPath"
}
$excelSecurityPath = 'HKCU:\Software\Microsoft\Office\16.0\Excel\Security'
$excelSecurityPathExisted = Test-Path $excelSecurityPath
if (-not $excelSecurityPathExisted) {
    New-Item -Path $excelSecurityPath -Force | Out-Null
}
$previousAccessVBOM = Get-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -ErrorAction SilentlyContinue

function ConvertTo-OleColor([string]$Hex) {
    $clean = $Hex.TrimStart('#')
    $red = [Convert]::ToInt32($clean.Substring(0, 2), 16)
    $green = [Convert]::ToInt32($clean.Substring(2, 2), 16)
    $blue = [Convert]::ToInt32($clean.Substring(4, 2), 16)
    return $red + (256 * $green) + (65536 * $blue)
}

function Set-SectionStyle($Range, [string]$Fill, [string]$FontColor = 'FFFFFF', [int]$FontSize = 11) {
    $Range.Interior.Color = ConvertTo-OleColor $Fill
    $Range.Font.Color = ConvertTo-OleColor $FontColor
    $Range.Font.Name = 'Yekan Bakh'
    $Range.Font.Size = $FontSize
    $Range.Font.Bold = $true
    $Range.VerticalAlignment = -4108
}

function Set-RangeFontSlots($Range, [string]$FontName) {
    $Range.Font.Name = $FontName
    # Excel's cell Font COM model exposes only Name on some Office builds.
    # Attempt the per-script slots when the host implements them; drawing
    # Font2 objects below always receive and validate all three slots.
    try { $Range.Font.NameComplexScript = $FontName } catch {}
    try { $Range.Font.NameFarEast = $FontName } catch {}
}

function Assert-RangeFontSlots($Range, [string]$FontName, [string]$Role) {
    foreach ($slot in @('Name', 'NameComplexScript', 'NameFarEast')) {
        try {
            $actual = [string]$Range.Font.$slot
        }
        catch {
            if ($slot -eq 'Name') { throw }
            continue
        }
        # This Office build returns an empty value for unsupported cell
        # complex/Far-East slots instead of throwing. Do not claim those slots
        # passed here; the saved package is audited through styles.xml.
        if ($slot -ne 'Name' -and [string]::IsNullOrWhiteSpace($actual)) {
            continue
        }
        if ([string]::IsNullOrWhiteSpace($actual) -or $actual -cne $FontName) {
            throw "Font policy failed for $Role ($slot='$actual', expected '$FontName')."
        }
        if ($actual -match '^(?i:Aptos|Calibri|Arial)$') {
            throw "Font policy found forbidden font '$actual' in $Role/$slot."
        }
    }
}

function Assert-ShapeFontSlots($Shape, [string]$FontName, [string]$Role) {
    foreach ($slot in @('Name', 'NameComplexScript', 'NameFarEast')) {
        $actual = [string]$Shape.TextFrame2.TextRange.Font.$slot
        if ([string]::IsNullOrWhiteSpace($actual) -or $actual -cne $FontName) {
            throw "Font policy failed for $Role ($slot='$actual', expected '$FontName')."
        }
        if ($actual -match '^(?i:Aptos|Calibri|Arial)$') {
            throw "Font policy found forbidden font '$actual' in $Role/$slot."
        }
    }
    $languageID = [int]$Shape.TextFrame2.TextRange.LanguageID
    if ($languageID -ne 1065) {
        throw "Font policy failed for $Role (LanguageID='$languageID', expected '1065')."
    }
    $legacyName = [string]$Shape.TextFrame.Characters().Font.Name
    if ([string]::IsNullOrWhiteSpace($legacyName) -or $legacyName -cne $FontName) {
        throw "Font policy failed for $Role legacy TextFrame (Name='$legacyName', expected '$FontName')."
    }
}

function Assert-FontFamilyAvailable([string]$FontName, [string]$Role) {
    Add-Type -AssemblyName System.Drawing
    $installedFonts = [Drawing.Text.InstalledFontCollection]::new()
    try {
        $available = @($installedFonts.Families | Where-Object {
            $_.Name -ceq $FontName
        }).Count -gt 0
    }
    finally {
        $installedFonts.Dispose()
    }
    if (-not $available) {
        throw "Configured $Role font '$FontName' is not installed; fallback is disabled."
    }
}

function Set-OfficeTextFont(
    $TextRange,
    [double]$Size,
    [bool]$Bold = $false,
    [string]$Color = ''
) {
    $TextRange.Font.Name = 'Yekan Bakh'
    # Office keeps separate Latin, Far East, and complex-script font slots.
    # Persian shapes and chart labels use the complex-script slot, so setting
    # only Font.Name can silently fall back to Calibri/Aptos.
    $TextRange.LanguageID = 1065
    $TextRange.Font.NameComplexScript = 'Yekan Bakh'
    $TextRange.Font.NameFarEast = 'Yekan Bakh'
    $TextRange.Font.Size = $Size
    $TextRange.Font.Bold = $Bold
    if (-not [string]::IsNullOrWhiteSpace($Color)) {
        $TextRange.Font.Fill.ForeColor.RGB = ConvertTo-OleColor $Color
    }
}

function Add-ActionButton($Sheet, [string]$Text, [string]$Macro, $Anchor, [double]$Width = 112, [double]$Height = 30) {
    $shape = $Sheet.Shapes.AddShape(5, $Anchor.Left, $Anchor.Top, $Width, $Height)
    $shape.Fill.ForeColor.RGB = ConvertTo-OleColor '0168CD'
    $shape.Line.ForeColor.RGB = ConvertTo-OleColor '0059B0'
    $shape.TextFrame2.TextRange.Text = $Text
    Set-OfficeTextFont $shape.TextFrame2.TextRange 11 $true 'FFFFFF'
    # Older Excel rendering paths still consult the legacy TextFrame slot.
    try {
        $shape.TextFrame.Characters().Font.Name = 'Yekan Bakh'
        $shape.TextFrame.Characters().Font.NameComplexScript = 'Yekan Bakh'
        $shape.TextFrame.Characters().Font.NameFarEast = 'Yekan Bakh'
    } catch {}
    $shape.TextFrame2.VerticalAnchor = 3
    $shape.TextFrame2.TextRange.ParagraphFormat.Alignment = 2
    $shape.OnAction = $Macro
    return $shape
}

function Add-OperationProgressSurface(
    $Sheet,
    $Anchor,
    [double]$Width,
    [double]$Height
) {
    $track = $Sheet.Shapes.AddShape(1, $Anchor.Left, $Anchor.Top, $Width, $Height)
    $track.Name = 'OperationProgressTrack'
    $track.Fill.ForeColor.RGB = ConvertTo-OleColor 'E8EEF4'
    $track.Line.ForeColor.RGB = ConvertTo-OleColor 'B9CCF4'
    $track.Line.Weight = 1
    $track.Visible = $false
    $track.AlternativeText = 'نوار وضعیت عملیات؛ زمینه ثابت.'

    $fill = $Sheet.Shapes.AddShape(1, $Anchor.Left, $Anchor.Top, 1, $Height)
    $fill.Name = 'OperationProgressFill'
    $fill.Fill.ForeColor.RGB = ConvertTo-OleColor '0168CD'
    $fill.Line.Visible = $false
    $fill.Visible = $false
    $fill.AlternativeText = 'پیشرفت عملیات؛ مقدار آماده.'

    $text = $Sheet.Shapes.AddShape(1, $Anchor.Left, $Anchor.Top, $Width, $Height)
    $text.Name = 'OperationProgressText'
    $text.Fill.Visible = $false
    $text.Line.Visible = $false
    $text.Visible = $false
    $text.TextFrame2.TextRange.Text = 'آماده'
    Set-OfficeTextFont $text.TextFrame2.TextRange 10 $true '2F414B'
    $text.TextFrame2.VerticalAnchor = 3
    $text.TextFrame2.TextRange.ParagraphFormat.Alignment = 2
    try {
        $text.TextFrame.Characters().Font.Name = 'Yekan Bakh'
        $text.TextFrame.Characters().Font.NameComplexScript = 'Yekan Bakh'
        $text.TextFrame.Characters().Font.NameFarEast = 'Yekan Bakh'
    } catch {}
    $text.AlternativeText = 'پیام و درصد عملیات؛ آماده.'

    return [pscustomobject]@{
        Track = $track
        Fill = $fill
        Text = $text
    }
}

function Add-DigitalogicMessageForm($Workbook) {
    $component = $Workbook.VBProject.VBComponents.Add(3)
    $component.Name = 'DigitalogicMessage'
    $designer = $component.Designer
    $component.Properties.Item('Caption').Value = 'Digitalogic'
    $component.Properties.Item('Width').Value = 430
    $component.Properties.Item('Height').Value = 230
    $component.Properties.Item('BackColor').Value = ConvertTo-OleColor 'F7F9FC'
    $component.Properties.Item('BorderStyle').Value = 1
    $component.Properties.Item('StartUpPosition').Value = 1

    $header = $designer.Controls.Add('Forms.Label.1', 'lblHeader', $true)
    $header.Left = 0
    $header.Top = 0
    $header.Width = 424
    $header.Height = 48
    $header.BackStyle = 1
    $header.BackColor = ConvertTo-OleColor '14324A'
    $header.Caption = ''
    $header.SpecialEffect = 0

    $brand = $designer.Controls.Add('Forms.Label.1', 'lblBrand', $true)
    $brand.Left = 18
    $brand.Top = 15
    $brand.Width = 104
    $brand.Height = 18
    $brand.BackStyle = 0
    $brand.Caption = 'DIGITALOGIC'
    $brand.ForeColor = ConvertTo-OleColor 'D8E4EE'

    $title = $designer.Controls.Add('Forms.Label.1', 'lblTitle', $true)
    $title.Left = 126
    $title.Top = 10
    $title.Width = 274
    $title.Height = 28
    $title.BackStyle = 0
    $title.Caption = ''
    $title.ForeColor = ConvertTo-OleColor 'FFFFFF'
    $title.TextAlign = 3

    $accent = $designer.Controls.Add('Forms.Label.1', 'lblAccent', $true)
    $accent.Left = 398
    $accent.Top = 68
    $accent.Width = 4
    $accent.Height = 82
    $accent.BackStyle = 1
    $accent.BackColor = ConvertTo-OleColor 'F2A900'
    $accent.Caption = ''
    $accent.SpecialEffect = 0

    $message = $designer.Controls.Add('Forms.Label.1', 'lblMessage', $true)
    $message.Left = 28
    $message.Top = 66
    $message.Width = 356
    $message.Height = 88
    $message.BackStyle = 0
    $message.Caption = ''
    $message.ForeColor = ConvertTo-OleColor '213547'
    $message.TextAlign = 3
    $message.WordWrap = $true

    $footer = $designer.Controls.Add('Forms.Label.1', 'lblFooter', $true)
    $footer.Left = 28
    $footer.Top = 182
    $footer.Width = 138
    $footer.Height = 16
    $footer.BackStyle = 0
    $footer.Caption = 'DIGITALOGIC'
    $footer.ForeColor = ConvertTo-OleColor '7A8793'

    $secondary = $designer.Controls.Add('Forms.CommandButton.1', 'cmdSecondary', $true)
    $secondary.Left = 180
    $secondary.Top = 170
    $secondary.Width = 96
    $secondary.Height = 32
    $secondary.Caption = ''
    $secondary.BackColor = ConvertTo-OleColor 'E8EDF2'
    $secondary.ForeColor = ConvertTo-OleColor '294154'

    $primary = $designer.Controls.Add('Forms.CommandButton.1', 'cmdPrimary', $true)
    $primary.Left = 288
    $primary.Top = 170
    $primary.Width = 110
    $primary.Height = 32
    $primary.Caption = ''
    $primary.BackColor = ConvertTo-OleColor '0168CD'
    $primary.ForeColor = ConvertTo-OleColor 'FFFFFF'

    $formCode = @'
Option Explicit

Private mDialogResult As Long
Private Const ARABIC_CHARSET As Long = 178

Public Property Get DialogResult() As Long
    DialogResult = mDialogResult
End Property

Public Sub ConfigureUnicodeHex(ByVal messageHex As String, _
                               ByVal titleHex As String, _
                               ByVal messageType As Long, _
                               ByVal okTextHex As String, _
                               ByVal yesTextHex As String, _
                               ByVal noTextHex As String, _
                               ByVal persianFont As String, _
                               ByVal latinFont As String)
    Configure DecodeUtf16Hex(messageHex), DecodeUtf16Hex(titleHex), _
              messageType, DecodeUtf16Hex(okTextHex), _
              DecodeUtf16Hex(yesTextHex), DecodeUtf16Hex(noTextHex), _
              persianFont, latinFont
End Sub

Public Sub Configure(ByVal message As String, ByVal title As String, _
                     ByVal messageType As Long, ByVal okText As String, _
                     ByVal yesText As String, ByVal noText As String, _
                     ByVal persianFont As String, ByVal latinFont As String)
    ' MSForms stores the native window caption through an ANSI path on some
    ' Windows installations. Keep that chrome ASCII-only and show the Persian
    ' title in the Unicode-safe in-form header below.
    Me.Caption = "DIGITALOGIC - Price Sync"
    AssertUnicodeText title, "title"
    AssertUnicodeText message, "message"
    AssertUnicodeText okText, "okText"
    AssertUnicodeText yesText, "yesText"
    AssertUnicodeText noText, "noText"
    lblTitle.Caption = title
    lblMessage.Caption = message
    lblBrand.Caption = "DIGITALOGIC"
    lblFooter.Caption = "DIGITALOGIC"
    mDialogResult = 0
    cmdPrimary.Default = True
    cmdSecondary.Cancel = True

    ApplyFonts persianFont, latinFont
    If (messageType And 4) = 4 Then
        cmdPrimary.Caption = yesText
        cmdSecondary.Caption = noText
        cmdSecondary.Visible = True
        cmdPrimary.Left = 288
    Else
        cmdPrimary.Caption = okText
        cmdSecondary.Visible = False
        cmdPrimary.Left = 288
    End If

    If (messageType And 16) = 16 Then
        lblAccent.BackColor = RGB(190, 45, 55)
    ElseIf (messageType And 48) = 48 Then
        lblAccent.BackColor = RGB(242, 169, 0)
    Else
        lblAccent.BackColor = RGB(1, 104, 205)
    End If
End Sub

Private Sub ApplyFonts(ByVal persianFont As String, _
                       ByVal latinFont As String)
    lblTitle.Font.Name = persianFont
    ApplyPersianControlFont lblTitle, persianFont, latinFont
    lblTitle.Font.Size = 12
    lblTitle.Font.Bold = True
    lblMessage.Font.Name = persianFont
    ApplyPersianControlFont lblMessage, persianFont, latinFont
    lblMessage.Font.Size = 10.5
    cmdPrimary.Font.Name = persianFont
    ApplyPersianControlFont cmdPrimary, persianFont, latinFont
    cmdPrimary.Font.Size = 10
    cmdPrimary.Font.Bold = True
    cmdSecondary.Font.Name = persianFont
    ApplyPersianControlFont cmdSecondary, persianFont, latinFont
    cmdSecondary.Font.Size = 10
    lblBrand.Font.Name = latinFont
    lblBrand.Font.Size = 8.5
    lblBrand.Font.Bold = True
    lblFooter.Font.Name = latinFont
    lblFooter.Font.Size = 8
End Sub

Private Sub ApplyPersianControlFont(ByVal target As Object, _
                                    ByVal requestedFont As String, _
                                    ByVal fallbackFont As String)
    On Error GoTo UseFallback
    If Len(Trim$(requestedFont)) = 0 Then GoTo UseFallback
    target.Font.Charset = ARABIC_CHARSET
    If StrComp(CStr(target.Font.Name), requestedFont, _
               vbTextCompare) = 0 Then Exit Sub
UseFallback:
    Err.Clear
    target.Font.Name = fallbackFont
    target.Font.Charset = ARABIC_CHARSET
End Sub

Private Sub AssertUnicodeText(ByVal value As String, ByVal fieldName As String)
    If InStr(1, value, "??", vbBinaryCompare) > 0 Then
        Err.Raise vbObjectError + 241, "DigitalogicMessage.Configure", _
                  "Damaged Unicode text: " & fieldName
    End If
End Sub

Private Function DecodeUtf16Hex(ByVal hexCodeUnits As String) As String
    Dim position As Long
    Dim codeUnit As Long

    If Len(hexCodeUnits) Mod 4 <> 0 Then
        Err.Raise vbObjectError + 242, "DecodeUtf16Hex", _
                  "Invalid UTF-16 hexadecimal text."
    End If
    For position = 1 To Len(hexCodeUnits) Step 4
        codeUnit = CLng("&H" & Mid$(hexCodeUnits, position, 4))
        If codeUnit > 32767 Then codeUnit = codeUnit - 65536
        DecodeUtf16Hex = DecodeUtf16Hex & ChrW$(codeUnit)
    Next position
End Function

Public Function ValidateUnicodeCaptions(ByVal messageHex As String, _
                                        ByVal titleHex As String, _
                                        ByVal primaryHex As String, _
                                        ByVal secondaryHex As String) As Boolean
    ValidateUnicodeCaptions = _
        StrComp(CStr(lblMessage.Caption), DecodeUtf16Hex(messageHex), _
                vbBinaryCompare) = 0 And _
        StrComp(CStr(lblTitle.Caption), DecodeUtf16Hex(titleHex), _
                vbBinaryCompare) = 0 And _
        StrComp(CStr(cmdPrimary.Caption), DecodeUtf16Hex(primaryHex), _
                vbBinaryCompare) = 0 And _
        StrComp(CStr(cmdSecondary.Caption), DecodeUtf16Hex(secondaryHex), _
                vbBinaryCompare) = 0 And _
        InStr(1, CStr(lblMessage.Caption) & CStr(lblTitle.Caption) & _
                CStr(cmdPrimary.Caption) & CStr(cmdSecondary.Caption), _
                "??", vbBinaryCompare) = 0
End Function

Public Function ValidateFonts(ByVal persianFont As String, _
                              ByVal latinFont As String) As Boolean
    ValidateFonts = _
        StrComp(lblTitle.Font.Name, persianFont, vbTextCompare) = 0 And _
        lblTitle.Font.Charset = ARABIC_CHARSET And _
        StrComp(lblMessage.Font.Name, persianFont, vbTextCompare) = 0 And _
        lblMessage.Font.Charset = ARABIC_CHARSET And _
        StrComp(cmdPrimary.Font.Name, persianFont, vbTextCompare) = 0 And _
        cmdPrimary.Font.Charset = ARABIC_CHARSET And _
        StrComp(cmdSecondary.Font.Name, persianFont, vbTextCompare) = 0 And _
        cmdSecondary.Font.Charset = ARABIC_CHARSET And _
        StrComp(lblBrand.Font.Name, latinFont, vbTextCompare) = 0 And _
        StrComp(lblFooter.Font.Name, latinFont, vbTextCompare) = 0
End Function

Private Sub cmdPrimary_Click()
    If cmdSecondary.Visible Then
        mDialogResult = 6
    Else
        mDialogResult = 1
    End If
    Unload Me
End Sub

Private Sub cmdSecondary_Click()
    mDialogResult = 7
    Unload Me
End Sub

Private Sub UserForm_QueryClose(Cancel As Integer, CloseMode As Integer)
    If CloseMode = 0 Then
        If cmdSecondary.Visible Then
            mDialogResult = 7
        Else
            mDialogResult = 1
        End If
        Cancel = False
    End If
End Sub
'@
    $component.CodeModule.AddFromString($formCode)

    foreach ($control in @($header, $brand, $title, $accent, $message, $footer, $secondary, $primary)) {
        Release-ComObject $control
    }
    Release-ComObject $designer
    return $component
}

function Add-BrandLogo(
    $Sheet,
    [string]$Path,
    $Anchor,
    [double]$Width = 208,
    [double]$Height = 48,
    [bool]$CenterVertically = $false
) {
    $shape = $Sheet.Shapes.AddPicture(
        $Path,
        $false,
        $true,
        $Anchor.Left,
        $Anchor.Top,
        $Width,
        $Height
    )
    $shape.AlternativeText = 'نشان رسمی دیجیتالاجیک'
    $shape.LockAspectRatio = -1
    if ($CenterVertically) {
        $shape.Top = $Anchor.Top + (($Anchor.Height - $shape.Height) / 2)
    }
    return $shape
}

function Release-ComObject($Object) {
    if ($null -ne $Object -and [Runtime.InteropServices.Marshal]::IsComObject($Object)) {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($Object)
    }
}

function Get-ZipEntryText($Archive, [string]$EntryName) {
    $entry = $Archive.GetEntry($EntryName)
    if ($null -eq $entry) {
        throw "Required Office package entry is missing: $EntryName"
    }
    $reader = [IO.StreamReader]::new($entry.Open(), [Text.Encoding]::UTF8, $true)
    try {
        return $reader.ReadToEnd()
    }
    finally {
        $reader.Dispose()
    }
}

function Set-ZipEntryText($Archive, [string]$EntryName, [string]$Text) {
    $existing = $Archive.GetEntry($EntryName)
    if ($null -ne $existing) {
        $existing.Delete()
    }
    $entry = $Archive.CreateEntry($EntryName, [IO.Compression.CompressionLevel]::Optimal)
    $entry.LastWriteTime = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
    $writer = [IO.StreamWriter]::new($entry.Open(), [Text.UTF8Encoding]::new($false))
    try {
        $writer.Write($Text)
    }
    finally {
        $writer.Dispose()
    }
}

function Set-ZipEntryBytes($Archive, [string]$EntryName, [byte[]]$Bytes) {
    $existing = $Archive.GetEntry($EntryName)
    if ($null -ne $existing) {
        $existing.Delete()
    }
    $entry = $Archive.CreateEntry($EntryName, [IO.Compression.CompressionLevel]::Optimal)
    $entry.LastWriteTime = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
    $stream = $entry.Open()
    try {
        $stream.Write($Bytes, 0, $Bytes.Length)
    }
    finally {
        $stream.Dispose()
    }
}

function Replace-ByteSequence(
    [byte[]]$Bytes,
    [byte[]]$Needle,
    [byte[]]$Replacement
) {
    if ($Needle.Length -ne $Replacement.Length) {
        throw 'Binary metadata replacement must preserve byte length.'
    }
    $replacements = 0
    for ($index = 0; $index -le $Bytes.Length - $Needle.Length; $index++) {
        $matched = $true
        for ($offset = 0; $offset -lt $Needle.Length; $offset++) {
            if ($Bytes[$index + $offset] -ne $Needle[$offset]) {
                $matched = $false
                break
            }
        }
        if ($matched) {
            [Array]::Copy($Replacement, 0, $Bytes, $index, $Replacement.Length)
            $replacements++
            $index += $Needle.Length - 1
        }
    }
    return $replacements
}

function Remove-ExcelPrivatePackageMetadata([string]$Path) {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::Open($Path, [IO.Compression.ZipArchiveMode]::Update)
    try {
        $workbookXml = [Xml.XmlDocument]::new()
        $workbookXml.PreserveWhitespace = $true
        $workbookXml.LoadXml((Get-ZipEntryText $archive 'xl/workbook.xml'))
        $absolutePathNodes = @($workbookXml.SelectNodes("//*[local-name()='absPath' and namespace-uri()='http://schemas.microsoft.com/office/spreadsheetml/2010/11/ac']"))
        foreach ($absolutePathNode in $absolutePathNodes) {
            $removalTarget = $absolutePathNode
            while ($null -ne $removalTarget.ParentNode -and $removalTarget.LocalName -ne 'AlternateContent') {
                $removalTarget = $removalTarget.ParentNode
            }
            if ($removalTarget.LocalName -eq 'AlternateContent') {
                [void]$removalTarget.ParentNode.RemoveChild($removalTarget)
            }
            else {
                [void]$absolutePathNode.ParentNode.RemoveChild($absolutePathNode)
            }
        }
        Set-ZipEntryText $archive 'xl/workbook.xml' $workbookXml.OuterXml

        $coreXml = [Xml.XmlDocument]::new()
        $coreXml.PreserveWhitespace = $true
        $coreXml.LoadXml((Get-ZipEntryText $archive 'docProps/core.xml'))
        $coreNamespaces = [Xml.XmlNamespaceManager]::new($coreXml.NameTable)
        $coreNamespaces.AddNamespace('cp', 'http://schemas.openxmlformats.org/package/2006/metadata/core-properties')
        $coreNamespaces.AddNamespace('dc', 'http://purl.org/dc/elements/1.1/')
        $coreNamespaces.AddNamespace('dcterms', 'http://purl.org/dc/terms/')
        $creator = $coreXml.SelectSingleNode('/cp:coreProperties/dc:creator', $coreNamespaces)
        $lastModifiedBy = $coreXml.SelectSingleNode('/cp:coreProperties/cp:lastModifiedBy', $coreNamespaces)
        if ($null -eq $creator -or $null -eq $lastModifiedBy) {
            throw 'The Excel core-properties package is missing its author fields.'
        }
        $creator.InnerText = 'AtomicDeploy'
        $lastModifiedBy.InnerText = 'AtomicDeploy'
        foreach ($timestampNode in @($coreXml.SelectNodes('/cp:coreProperties/dcterms:created | /cp:coreProperties/dcterms:modified', $coreNamespaces))) {
            [void]$timestampNode.ParentNode.RemoveChild($timestampNode)
        }
        Set-ZipEntryText $archive 'docProps/core.xml' $coreXml.OuterXml

        $applicationXml = [Xml.XmlDocument]::new()
        $applicationXml.PreserveWhitespace = $true
        $applicationXml.LoadXml((Get-ZipEntryText $archive 'docProps/app.xml'))
        $company = $applicationXml.SelectSingleNode("//*[local-name()='Company']")
        if ($null -ne $company) {
            $company.InnerText = 'AtomicDeploy'
        }
        Set-ZipEntryText $archive 'docProps/app.xml' $applicationXml.OuterXml

        # UserForms add an MSForms cache reference containing the Windows user
        # profile. Replace only that fixed-length profile prefix in both ANSI
        # and UTF-16 records so the compiled VBA stream remains structurally
        # unchanged while the distributed package stays workstation-neutral.
        $vbaEntry = $archive.GetEntry('xl/vbaProject.bin')
        if ($null -ne $vbaEntry) {
            $vbaStream = $vbaEntry.Open()
            $vbaBytesStream = [IO.MemoryStream]::new()
            try {
                $vbaStream.CopyTo($vbaBytesStream)
            }
            finally {
                $vbaStream.Dispose()
            }
            $vbaBytes = $vbaBytesStream.ToArray()
            $vbaBytesStream.Dispose()
            $userProfile = [Environment]::GetFolderPath('UserProfile')
            $neutralProfile = 'C:\ProgramData'
            if ($neutralProfile.Length -lt $userProfile.Length) {
                $neutralProfile = $neutralProfile.PadRight($userProfile.Length, '_')
            }
            elseif ($neutralProfile.Length -gt $userProfile.Length) {
                $neutralProfile = $neutralProfile.Substring(0, $userProfile.Length)
            }
            $ascii = [Text.Encoding]::ASCII
            $unicode = [Text.Encoding]::Unicode
            $vbaReplacements = Replace-ByteSequence $vbaBytes `
                ($ascii.GetBytes($userProfile)) ($ascii.GetBytes($neutralProfile))
            $vbaReplacements += Replace-ByteSequence $vbaBytes `
                ($unicode.GetBytes($userProfile)) ($unicode.GetBytes($neutralProfile))
            $profileLeaf = [IO.Path]::GetFileName($userProfile.TrimEnd('\'))
            if (-not [string]::IsNullOrWhiteSpace($profileLeaf)) {
                $neutralUser = 'User'
                if ($neutralUser.Length -lt $profileLeaf.Length) {
                    $neutralUser = $neutralUser.PadRight($profileLeaf.Length, '_')
                }
                elseif ($neutralUser.Length -gt $profileLeaf.Length) {
                    $neutralUser = $neutralUser.Substring(0, $profileLeaf.Length)
                }
                $profileAppDataFragment = "Users\$profileLeaf\AppData"
                $neutralAppDataFragment = "Users\$neutralUser\AppData"
                $vbaReplacements += Replace-ByteSequence $vbaBytes `
                    ($ascii.GetBytes($profileAppDataFragment)) `
                    ($ascii.GetBytes($neutralAppDataFragment))
                $vbaReplacements += Replace-ByteSequence $vbaBytes `
                    ($unicode.GetBytes($profileAppDataFragment)) `
                    ($unicode.GetBytes($neutralAppDataFragment))
            }
            if ($vbaReplacements -gt 0) {
                Set-ZipEntryBytes $archive 'xl/vbaProject.bin' $vbaBytes
            }
        }

        # Excel can omit the DrawingML language slot even after applying the
        # correct typeface through COM. Normalize chart/drawing text metadata
        # so Persian rendering never falls back to an Office default font.
        $drawingTextEntries = @($archive.Entries | Where-Object {
            $_.FullName -match '^xl/(charts|drawings)/.+\.xml$'
        } | ForEach-Object { $_.FullName })
        foreach ($drawingTextEntry in $drawingTextEntries) {
            $drawingXml = [Xml.XmlDocument]::new()
            $drawingXml.PreserveWhitespace = $true
            $drawingXml.LoadXml((Get-ZipEntryText $archive $drawingTextEntry))
            $changed = $false
            foreach ($fontNode in @($drawingXml.SelectNodes(
                "//*[local-name()='defRPr' or local-name()='rPr']"
            ))) {
                $fontChildren = @($fontNode.SelectNodes(
                    "./*[local-name()='latin' or local-name()='ea' or local-name()='cs']"
                ))
                if ($fontChildren.Count -eq 0) { continue }
                if ($fontNode.GetAttribute('lang') -cne 'fa-IR') {
                    $fontNode.SetAttribute('lang', 'fa-IR')
                    $changed = $true
                }
                foreach ($fontChild in $fontChildren) {
                    if ($fontChild.GetAttribute('typeface') -cne 'Yekan Bakh') {
                        $fontChild.SetAttribute('typeface', 'Yekan Bakh')
                        $changed = $true
                    }
                }
            }
            if ($changed) {
                Set-ZipEntryText $archive $drawingTextEntry $drawingXml.OuterXml
            }
        }

        $fixedTimestamp = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
        foreach ($entry in $archive.Entries) {
            $entry.LastWriteTime = $fixedTimestamp
        }
    }
    finally {
        $archive.Dispose()
    }

    $archive = [IO.Compression.ZipFile]::OpenRead($Path)
    try {
        $forbidden = '(?i)(x15ac:absPath|[A-Z]:\\Users\\|/Users/|mahdi shokri|mahdielector@)'
        foreach ($entry in $archive.Entries) {
            if ($entry.FullName -like 'xl/externalLinks/*' -or $entry.FullName -eq 'xl/connections.xml') {
                throw "The dashboard package contains an external Office connection: $($entry.FullName)"
            }
            if ($entry.FullName -notmatch '\.(xml|rels)$') {
                continue
            }
            $reader = [IO.StreamReader]::new($entry.Open(), [Text.Encoding]::UTF8, $true)
            try {
                $entryText = $reader.ReadToEnd()
            }
            finally {
                $reader.Dispose()
            }
            if ($entryText -match $forbidden) {
                throw "Private workstation metadata remains in Office package entry $($entry.FullName)."
            }
        }
    }
    finally {
        $archive.Dispose()
    }
}

Assert-FontFamilyAvailable 'Yekan Bakh' 'Persian'
Assert-FontFamilyAvailable 'Yekan Bakh FaNum' 'Persian price digits'
Assert-FontFamilyAvailable 'Segoe UI' 'Latin'

New-Item -ItemType Directory -Force (Split-Path -Parent $OutputPath) | Out-Null
New-Item -ItemType Directory -Force (Split-Path -Parent $DistributionCopyPath) | Out-Null
New-Item -ItemType Directory -Force $PreviewDirectory | Out-Null
New-Item -ItemType Directory -Force (Split-Path -Parent $ChecksumManifestPath) | Out-Null

$excel = $null
$workbook = $null
try {
    # Excel requires this switch for programmatic VBA source import. It is
    # restored in finally; ordinary macro warnings are never changed.
    New-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -PropertyType DWord -Value 1 -Force | Out-Null
    $excel = New-Object -ComObject Excel.Application
    $excel.Visible = $false
    $excel.DisplayAlerts = $false
    $excel.ScreenUpdating = $false
    $excel.EnableEvents = $false

    $workbook = $excel.Workbooks.Add()
    while ($workbook.Worksheets.Count -gt 1) {
        $workbook.Worksheets.Item($workbook.Worksheets.Count).Delete()
    }

    # Product rows are empty in the template and are created only by Sync.
    $priceList = $workbook.Worksheets.Item(1)
    $priceList.Name = 'محصولات'
    $dashboard = $workbook.Worksheets.Add([Type]::Missing, $priceList)
    $dashboard.Name = 'داشبورد'
    $settings = $workbook.Worksheets.Add([Type]::Missing, $dashboard)
    $settings.Name = 'تنظیمات'
    $syncData = $workbook.Worksheets.Add([Type]::Missing, $settings)
    $syncData.Name = 'داده‌های همگام‌سازی'

    foreach ($sheet in @($priceList, $dashboard, $settings, $syncData)) {
        $sheet.Cells.Font.Name = 'Yekan Bakh'
        $sheet.Cells.Font.Size = 11
        $sheet.Cells.Font.Color = ConvertTo-OleColor '2F414B'
        $sheet.Cells.ReadingOrder = -5004
        $sheet.Activate()
        $excel.ActiveWindow.DisplayRightToLeft = $true
    }

    # Main sheet: one canonical Persian table. The raw "other" projection remains
    # available for audit, but is collapsed by default.
    $priceList.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $priceList.Rows.Item(1).RowHeight = 60
    $priceList.Range('B1:K2').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range('E1:K2').Merge()
    $priceList.Range('E1').Value2 = 'لیست قیمت دیجیتالاجیک'
    $priceList.Range('E1').Font.Name = 'Yekan Bakh'
    $priceList.Range('E1').Font.Size = 20
    $priceList.Range('E1').Font.Bold = $true
    $priceList.Range('E1').Font.Color = ConvertTo-OleColor '2F414B'
    $priceList.Range('E1').HorizontalAlignment = -4152
    $priceList.Range('E1').VerticalAlignment = -4108
    [void](Add-BrandLogo $priceList $LogoPath $priceList.Range('B1:B2') 190 43 $true)

    $priceList.Rows(3).RowHeight = 34
    $priceList.Range('B3').Value2 = 'جست‌وجوی کالا (F2/F3)'
    $priceList.Range('B3').Font.Bold = $true
    $priceList.Range('B3').Font.Color = ConvertTo-OleColor '0168CD'
    $priceList.Range('B3').VerticalAlignment = -4108
    # The search field is one deliberate presentation/input surface, not three
    # independent cells. VBA always resolves edits and selection changes back
    # to the C3 anchor so native Enter remains top-level and recursion-safe.
    $priceList.Range('C3:E3').Merge()
    $priceList.Range('C3:E3').NumberFormat = '@'
    $priceList.Range('C3:E3').Interior.Color = ConvertTo-OleColor 'F7FBFF'
    $priceList.Range('C3:E3').Borders.Color = ConvertTo-OleColor '0168CD'
    $priceList.Range('C3:E3').Borders.Weight = 2
    $priceList.Range('C3').Font.Name = 'Yekan Bakh'
    $priceList.Range('C3').Font.Size = 12
    $priceList.Range('C3').Font.Bold = $false
    $priceList.Range('C3').Font.Color = ConvertTo-OleColor '2F414B'
    $priceList.Range('C3').HorizontalAlignment = -4152
    $priceList.Range('C3').VerticalAlignment = -4108
    $priceList.Range('C3').ReadingOrder = -5004
    $priceList.Range('C3').IndentLevel = 1
    $priceList.Range('C3').ShrinkToFit = $false
    $priceList.Range('C3').WrapText = $false
    $priceList.Range('C3').Validation.Delete()
    $priceList.Range('C3').Validation.Add(0)
    $priceList.Range('C3').Validation.InputTitle = 'جست‌وجوی کالا'
    $priceList.Range('C3').Validation.InputMessage = 'نام، کد کالا یا شناسه ووکامرس را وارد کنید و Enter یا دکمه پیدا کردن را بزنید.'
    $priceList.Range('C3').Validation.ShowInput = $true
    [void]$workbook.Names.Add('ProductSearchQuery', $priceList.Range('C3'))
    $searchButton = Add-ActionButton $priceList 'پیدا کردن' 'ProductCatalogSync.SearchProducts' $priceList.Range('F3') $priceList.Range('F3').Width 27
    $searchButton.Name = 'ProductSearchButton'
    $searchButton.AlternativeText = 'جست‌وجوی کالا؛ تعداد نتیجه و جایگاه نتیجه جاری روی همین دکمه نمایش داده می‌شود.'
    [void]$searchButton
    [void](Add-ActionButton $priceList 'پاک کردن' 'ProductCatalogSync.ClearProductSearch' $priceList.Range('G3') $priceList.Range('G3').Width 27)
    [void](Add-ActionButton $priceList 'به‌روزرسانی' 'ProductCatalogSync.RefreshAllData' $priceList.Range('I3') $priceList.Range('I3:K3').Width 27)

    $priceList.Rows.Item(4).RowHeight = 24
    [void](Add-OperationProgressSurface $priceList $priceList.Range('B4') $priceList.Range('B4:K4').Width 20)

    $headers = @(
        'قیمت فروش (تومان)',
        'وزن کالا (گرم)',
        'سایر',
        'محل کالا',
        'قیمت خرید (یوآن)',
        'موجودی کل',
        'کد کالا',
        'نام کالا',
        'شناسه ووکامرس',
        'دسته‌بندی'
    )
    for ($column = 0; $column -lt $headers.Count; $column++) {
        $priceList.Cells.Item(5, $column + 2).Value2 = $headers[$column]
    }
    $productLastColumn = 'K'
    $productTable = $priceList.ListObjects.Add(1, $priceList.Range("B5:${productLastColumn}6"), $null, 1)
    $productTable.Name = 'Products'
    $productTable.TableStyle = 'TableStyleMedium2'
    [void]$productTable.DataBodyRange.Delete()
    $priceList.Range("B5:${productLastColumn}5").Interior.Color = ConvertTo-OleColor '0168CD'
    $priceList.Range("B5:${productLastColumn}5").Font.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range("B5:${productLastColumn}5").Font.Name = 'Yekan Bakh'
    $priceList.Range("B5:${productLastColumn}5").Font.Bold = $true
    $priceList.Range("B5:${productLastColumn}5").HorizontalAlignment = -4108
    $priceList.Range("B5:${productLastColumn}5").VerticalAlignment = -4108
    $priceList.Range("B5:${productLastColumn}5").Borders.Color = ConvertTo-OleColor '0059B0'
    $priceList.Range("B5:${productLastColumn}5").Borders.Weight = 2
    $priceList.Rows.Item(5).RowHeight = 31

    $priceList.Columns('B').ColumnWidth = 20
    $priceList.Columns('C').ColumnWidth = 16
    $priceList.Columns('D').ColumnWidth = 18
    $priceList.Columns('E').ColumnWidth = 18
    $priceList.Columns('F').ColumnWidth = 18
    $priceList.Columns('G').ColumnWidth = 14
    $priceList.Columns('H').ColumnWidth = 17
    $priceList.Columns('I').ColumnWidth = 48
    $priceList.Columns('J').ColumnWidth = 17
    $priceList.Columns('K').ColumnWidth = 36
    $priceList.Columns('L').ColumnWidth = 2.5
    $priceList.Columns('M:O').ColumnWidth = 15
    $priceList.Columns('O').ColumnWidth = 24
    $configFirstColumn = 'M'
    $configSecondColumn = 'O'
    $configLastColumn = 'O'
    $priceList.Columns('B').NumberFormat = '#,##0'
    $priceList.Columns('C').NumberFormat = 'General'
    $priceList.Columns('F').NumberFormat = 'General'
    $priceList.Columns('G').NumberFormat = 'General'
    $priceList.Columns('H').NumberFormat = '@'
    $priceList.Columns('J').NumberFormat = '@'
    $priceList.Columns('B:C').ReadingOrder = -5003
    $priceList.Columns('F:H').ReadingOrder = -5003
    $priceList.Columns('J').ReadingOrder = -5003
    $priceList.Columns('D').Hidden = $true
    $priceList.Columns('B').Font.Bold = $true
    $priceList.Columns('B:C').HorizontalAlignment = -4152
    $priceList.Columns('F:H').HorizontalAlignment = -4152
    $priceList.Columns('J').HorizontalAlignment = -4131
    $priceList.Columns('I').HorizontalAlignment = -4152
    $priceList.Columns('I').Font.Bold = $true
    $priceList.Columns('K').HorizontalAlignment = -4152

    # Keep the three familiar configuration cards, but leave their values empty
    # in the template. Sync fills them from the living Digitalogic state.
    $yuanHeader = $priceList.Range("${configFirstColumn}6")
    $yuanValue = $priceList.Range("${configFirstColumn}7")
    $shippingHeader = $priceList.Range("${configSecondColumn}6")
    $shippingValue = $priceList.Range("${configSecondColumn}7")
    $profitHeader = $priceList.Range("${configSecondColumn}9")
    $profitValue = $priceList.Range("${configSecondColumn}10")

    $yuanHeader.Value2 = 'بهای یوآن (قابل ویرایش)'
    $yuanValue.Formula = "=IF('تنظیمات'!G18="""","""",'تنظیمات'!G18)"
    $yuanValue.AddComment('برای تغییر نرخ، عدد جدید را همین‌جا وارد کنید. تا تأیید وب‌سایت، فرمول‌های قیمت از نرخ تأییدشده استفاده می‌کنند.')
    $yuanTable = $priceList.ListObjects.Add(1, $priceList.Range("${configFirstColumn}6:${configFirstColumn}7"), $null, 1)
    $yuanTable.Name = 'Yuan_Price'
    $yuanTable.TableStyle = 'TableStyleMedium2'

    $shippingHeader.Value2 = 'نرخ حمل هوایی (یوآن/کیلوگرم)'
    $shippingValue.Formula = "=IF('تنظیمات'!B14="""","""",'تنظیمات'!B14)"
    $shippingTable = $priceList.ListObjects.Add(1, $priceList.Range("${configSecondColumn}6:${configSecondColumn}7"), $null, 1)
    $shippingTable.Name = 'Shipping'
    $shippingTable.TableStyle = 'TableStyleMedium2'

    $profitHeader.Value2 = 'حاشیه سود'
    $profitValue.Formula = "=IF('تنظیمات'!B13="""","""",'تنظیمات'!B13)"
    $profitTable = $priceList.ListObjects.Add(1, $priceList.Range("${configSecondColumn}9:${configSecondColumn}10"), $null, 1)
    $profitTable.Name = 'Profit'
    $profitTable.TableStyle = 'TableStyleMedium2'

    foreach ($configHeader in @($yuanHeader, $shippingHeader, $profitHeader)) {
        $configHeader.Interior.Color = ConvertTo-OleColor '0168CD'
        $configHeader.Font.Color = ConvertTo-OleColor 'FFFFFF'
        $configHeader.Font.Bold = $true
        $configHeader.HorizontalAlignment = -4108
        $configHeader.WrapText = $true
    }
    $priceList.Rows.Item(6).RowHeight = 34
    foreach ($configValue in @($yuanValue, $shippingValue, $profitValue)) {
        $configValue.Interior.Color = ConvertTo-OleColor 'DDE8FC'
        $configValue.Font.Color = ConvertTo-OleColor '242424'
        $configValue.Font.Bold = $true
        $configValue.HorizontalAlignment = -4108
        $configValue.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    }
    $yuanValue.NumberFormat = '#,##0'
    $shippingValue.NumberFormat = 'General'
    $profitValue.NumberFormat = '0%'
    foreach ($configValue in @($yuanValue, $shippingValue, $profitValue)) {
        $configValue.ReadingOrder = -5003
        $configValue.Font.Name = 'Yekan Bakh'
    }

    $statusHeaderRange = $priceList.Range("${configFirstColumn}12:${configLastColumn}12")
    $statusBodyRange = $priceList.Range("${configFirstColumn}13:${configLastColumn}16")
    $updatedHeaderRange = $priceList.Range("${configFirstColumn}18:${configLastColumn}18")
    $updatedBodyRange = $priceList.Range("${configFirstColumn}19:${configLastColumn}19")
    $statusHeaderRange.Merge()
    $statusHeaderRange.Cells.Item(1, 1).Value2 = 'وضعیت همگام‌سازی'
    Set-SectionStyle $statusHeaderRange 'F6F6F6' '242424' 10
    $statusBodyRange.Merge()
    $statusBodyRange.Cells.Item(1, 1).Formula = "='تنظیمات'!B6"
    $statusBodyRange.WrapText = $true
    $statusBodyRange.HorizontalAlignment = -4108
    $statusBodyRange.VerticalAlignment = -4108
    $statusBodyRange.Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $statusBodyRange.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $priceList.Rows('13:16').RowHeight = 24
    $updatedHeaderRange.Merge()
    $updatedHeaderRange.Cells.Item(1, 1).Value2 = 'آخرین به‌روزرسانی'
    Set-SectionStyle $updatedHeaderRange 'F6F6F6' '242424' 10
    $updatedBodyRange.Merge()
    $updatedBodyRange.Cells.Item(1, 1).Formula = "=IF('تنظیمات'!B7="""","""",'تنظیمات'!B7)"
    $updatedBodyRange.NumberFormat = 'yyyy/mm/dd hh:mm'
    $updatedBodyRange.HorizontalAlignment = -4108
    $updatedBodyRange.Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $updatedBodyRange.Borders.Color = ConvertTo-OleColor 'B9CCF4'

    # A single deferred image preview lives beside, never over, the product
    # table. The workbook starts with previews disabled so normal 1,103-row
    # refresh and search remain just as fast as before.
    $imageHeaderRange = $priceList.Range('M21:O21')
    $imageStatusRange = $priceList.Range('M22:O22')
    $imageAreaRange = $priceList.Range('M23:O32')
    $imageHeaderRange.Merge()
    $imageHeaderRange.Cells.Item(1, 1).Value2 = 'تصویر محصول'
    Set-SectionStyle $imageHeaderRange 'F6F6F6' '242424' 10
    $imageStatusRange.Merge()
    $imageStatusRange.Cells.Item(1, 1).Value2 = 'نمایش تصاویر غیرفعال است.'
    $imageStatusRange.WrapText = $true
    $imageStatusRange.HorizontalAlignment = -4108
    $imageStatusRange.VerticalAlignment = -4108
    $imageStatusRange.Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $imageStatusRange.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $imageAreaRange.Merge()
    $imageAreaRange.Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $imageAreaRange.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $priceList.Rows.Item(22).RowHeight = 34
    $priceList.Rows('23:32').RowHeight = 21
    [void]$workbook.Names.Add('ProductImagePreviewStatus', $priceList.Range('M22'))
    [void]$workbook.Names.Add('ProductImagePreviewArea', $imageAreaRange)
    $priceList.Range("B5:${productLastColumn}1000").Borders.Color = ConvertTo-OleColor 'D9E5EC'

    $excel.ActiveWindow.SplitRow = 5
    $excel.ActiveWindow.FreezePanes = $true
    $excel.ActiveWindow.Zoom = 90

    # Technical join data is never user-facing and is empty in the template.
    # It is created before Dashboard formulas so structured references resolve.
    $syncHeaders = @(
        'کلید همگام‌سازی',
        'ارز کالا',
        'نرخ حمل هر کیلو',
        'ارز حمل',
        'حاشیه سود (درصد)',
        'بهای یوآن',
        'بهای دلار',
        'تاریخ نرخ',
        'شناسه ووکامرس',
        'قیمت مشتری ووکامرس',
        'آخرین تغییر ووکامرس',
        'بازبینی رکورد',
        'نشانی محصول',
        'حاشیه سود کالا',
        'قیمت محاسباتی کالا',
        'قیمت ویژه ووکامرس (ممیزی)',
        'دسته‌بندی',
        'وضعیت انتشار',
        'هشدار قیمت',
        'نوع ردیف',
        'مبلغ منبع قیمت',
        'ارز منبع قیمت',
        'نوع منبع قیمت',
        'نشانی تصویر'
    )
    for ($column = 0; $column -lt $syncHeaders.Count; $column++) {
        $syncData.Cells.Item(1, $column + 1).Value2 = $syncHeaders[$column]
    }
    $syncTable = $syncData.ListObjects.Add(1, $syncData.Range('A1:X2'), $null, 1)
    $syncTable.Name = 'SyncData'
    $syncTable.TableStyle = 'TableStyleMedium2'
    [void]$syncTable.DataBodyRange.Delete()
    $syncData.Visible = 2

    # Dashboard: compact, formula-backed operating view of the live table.
    $dashboard.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $dashboard.Rows.Item(1).RowHeight = 60
    $dashboard.Columns('A').ColumnWidth = 2.5
    $dashboard.Columns('B').ColumnWidth = 22
    $dashboard.Columns('C').ColumnWidth = 8
    $dashboard.Columns('D:I').ColumnWidth = 15
    $dashboard.Range('B1:I2').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $dashboard.Range('E1:I2').Merge()
    $dashboard.Range('E1').Value2 = 'داشبورد قیمت و موجودی'
    $dashboard.Range('E1').Font.Name = 'Yekan Bakh'
    $dashboard.Range('E1').Font.Size = 20
    $dashboard.Range('E1').Font.Bold = $true
    $dashboard.Range('E1').Font.Color = ConvertTo-OleColor '2F414B'
    $dashboard.Range('E1').HorizontalAlignment = -4152
    $dashboard.Range('E1').VerticalAlignment = -4108
    [void](Add-BrandLogo $dashboard $LogoPath $dashboard.Range('B1:B2') 190 43 $true)

    $dashboardCards = @(
        @{ Header = 'B4:C4'; Value = 'B5:C6'; Label = 'تعداد کالاها'; Formula = '=COUNTA(Products[نام کالا])'; Format = '#,##0' },
        @{ Header = 'D4:E4'; Value = 'D5:E6'; Label = 'منتشرشده در سایت'; Formula = '=COUNTIF(SyncData[وضعیت انتشار],"publish")'; Format = '#,##0' },
        @{ Header = 'F4:G4'; Value = 'F5:G6'; Label = 'پیش‌نویس سایت'; Formula = '=COUNTIF(SyncData[وضعیت انتشار],"draft")'; Format = '#,##0' },
        @{ Header = 'H4:I4'; Value = 'H5:I6'; Label = 'بدون صفحه ووکامرس'; Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[شناسه ووکامرس],"")'; Format = '#,##0' },
        @{ Header = 'B8:C8'; Value = 'B9:C10'; Label = 'موجودی کل'; Formula = '=IFERROR(SUM(Products[موجودی کل]),0)'; Format = 'General' },
        @{ Header = 'D8:E8'; Value = 'D9:E10'; Label = 'کالاهای موجود'; Formula = '=COUNTIF(Products[موجودی کل],">0")'; Format = '#,##0' },
        @{ Header = 'F8:G8'; Value = 'F9:G10'; Label = 'کالاهای ناموجود'; Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[موجودی کل],0)'; Format = '#,##0' },
        @{ Header = 'H8:I8'; Value = 'H9:I10'; Label = 'دسته‌بندی‌های فعال'; Formula = '=IFERROR(SUMPRODUCT((Products[دسته‌بندی]<>"")/COUNTIF(Products[دسته‌بندی],Products[دسته‌بندی]&"")),0)'; Format = '#,##0' },
        @{ Header = 'B12:E12'; Value = 'B13:E14'; Label = 'ارزش فروش موجودی (تومان)'; Formula = '=IFERROR(SUMPRODUCT((Products[موجودی کل]>0)*Products[قیمت فروش (تومان)]*Products[موجودی کل]),0)'; Format = '#,##0' },
        @{ Header = 'F12:G12'; Value = 'F13:G14'; Label = 'بهای یوآن'; Formula = "=IF('تنظیمات'!B10="""","""",'تنظیمات'!B10)"; Format = '#,##0' },
        @{ Header = 'H12:I12'; Value = 'H13:I14'; Label = 'حاشیه سود'; Formula = "=IF('تنظیمات'!B13="""","""",'تنظیمات'!B13)"; Format = '0%' }
    )
    foreach ($card in $dashboardCards) {
        $header = $dashboard.Range($card.Header)
        $value = $dashboard.Range($card.Value)
        $header.Merge()
        $value.Merge()
        $header.Cells.Item(1, 1).Value2 = $card.Label
        try {
            $value.Cells.Item(1, 1).Formula = $card.Formula
        }
        catch {
            throw "Dashboard formula failed for '$($card.Label)': $($card.Formula)"
        }
        Set-SectionStyle $header 'EAF5FB' '2F414B' 10
        $header.HorizontalAlignment = -4108
        $value.Interior.Color = ConvertTo-OleColor 'FFFFFF'
        $value.Font.Name = 'Yekan Bakh'
        $value.Font.Size = 17
        $value.Font.Bold = $true
        $value.Font.Color = ConvertTo-OleColor '0168CD'
        $value.HorizontalAlignment = -4108
        $value.VerticalAlignment = -4108
        $value.NumberFormat = $card.Format
        $value.ReadingOrder = -5003
        $header.Borders.Color = ConvertTo-OleColor 'A9CFE4'
        $value.Borders.Color = ConvertTo-OleColor 'A9CFE4'
    }
    $dashboard.Range('B16:I16').Merge()
    $dashboard.Range('B16').Value2 = 'وضعیت همگام‌سازی'
    Set-SectionStyle $dashboard.Range('B16:I16') '2F414B' 'FFFFFF' 11
    $dashboard.Range('B17:I19').Merge()
    $dashboard.Range('B17').Formula = "='تنظیمات'!B6"
    $dashboard.Range('B17:I19').WrapText = $true
    $dashboard.Range('B17:I19').HorizontalAlignment = -4108
    $dashboard.Range('B17:I19').VerticalAlignment = -4108
    $dashboard.Range('B17:I19').Interior.Color = ConvertTo-OleColor 'F4F8FB'
    $dashboard.Range('B17:I19').Borders.Color = ConvertTo-OleColor 'A9CFE4'
    $dashboard.Range('B21').Value2 = 'وضعیت'
    $dashboard.Range('C21').Value2 = 'تعداد'
    $dashboard.Range('B22').Value2 = 'منتشرشده'
    $dashboard.Range('B23').Value2 = 'پیش‌نویس'
    $dashboard.Range('B24').Value2 = 'بدون صفحه'
    $dashboard.Range('C22').Formula = '=COUNTIF(SyncData[وضعیت انتشار],"publish")'
    $dashboard.Range('C23').Formula = '=COUNTIF(SyncData[وضعیت انتشار],"draft")'
    $dashboard.Range('C24').Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[شناسه ووکامرس],"")'
    $dashboard.Range('B21:C24').Borders.Color = ConvertTo-OleColor 'A9CFE4'
    $dashboard.Range('B21:C21').Interior.Color = ConvertTo-OleColor '0168CD'
    $dashboard.Range('B21:C21').Font.Color = ConvertTo-OleColor 'FFFFFF'
    $dashboard.Range('B21:C21').Font.Bold = $true
    $dashboard.Range('C22:C24').NumberFormat = '#,##0'
    $dashboard.Range('C22:C24').ReadingOrder = -5003
    $dashboard.Range('B26').Value2 = 'هشدار'
    $dashboard.Range('C26').Value2 = 'تعداد'
    $dashboard.Range('B27').Value2 = 'دسته‌بندی نامشخص'
    $dashboard.Range('B28').Value2 = 'هشدارهای قیمت'
    $dashboard.Range('B29').Value2 = 'فقط در ووکامرس'
    $dashboard.Range('C27').Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[دسته‌بندی],"")'
    $dashboard.Range('C28').Formula = '=COUNTIF(SyncData[هشدار قیمت],"<>")'
    $dashboard.Range('C29').Formula = '=COUNTIF(SyncData[نوع ردیف],"فقط ووکامرس")'
    $dashboard.Range('B26:C29').Borders.Color = ConvertTo-OleColor 'F0C36D'
    $dashboard.Range('B26:C26').Interior.Color = ConvertTo-OleColor 'FFF3D6'
    $dashboard.Range('B26:C26').Font.Bold = $true
    $dashboard.Range('C27:C29').NumberFormat = '#,##0'
    $dashboard.Range('C27:C29').ReadingOrder = -5003

    # Operator-facing legend for the conditional state colors applied to the
    # selling-price column after every live synchronization.
    $dashboard.Range('B31:C31').Merge()
    $dashboard.Range('B31').Value2 = 'وضعیت محاسبه قیمت'
    Set-SectionStyle $dashboard.Range('B31:C31') '2F414B' 'FFFFFF' 10
    $dashboard.Range('B32').Value2 = 'قیمت آماده'
    $dashboard.Range('B33').Value2 = 'دارای هشدار'
    $dashboard.Range('B34').Value2 = 'قیمت محاسبه‌نشده'
    # Keep the three counts mutually exclusive and aligned with the three
    # conditional-format rules on the selling-price cells.
    $dashboard.Range('C33').Formula = '=COUNTIFS(SyncData[هشدار قیمت],"<>",SyncData[قیمت محاسباتی کالا],">0")'
    $dashboard.Range('C32').Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[قیمت فروش (تومان)],">0")-C33'
    $dashboard.Range('C34').Formula = '=COUNTA(Products[نام کالا])-C32-C33'
    $dashboard.Range('B32:C32').Interior.Color = ConvertTo-OleColor 'E2EFDA'
    $dashboard.Range('B32:C32').Font.Color = ConvertTo-OleColor '006100'
    $dashboard.Range('B33:C33').Interior.Color = ConvertTo-OleColor 'FFF2CC'
    $dashboard.Range('B33:C33').Font.Color = ConvertTo-OleColor '9C6500'
    $dashboard.Range('B34:C34').Interior.Color = ConvertTo-OleColor 'F4CCCC'
    $dashboard.Range('B34:C34').Font.Color = ConvertTo-OleColor '9C0006'
    $dashboard.Range('B31:C34').Borders.Color = ConvertTo-OleColor 'A9CFE4'
    $dashboard.Range('B32:B34').Font.Bold = $true
    $dashboard.Range('C32:C34').NumberFormat = '#,##0'
    $dashboard.Range('C32:C34').ReadingOrder = -5003
    $chartObject = $dashboard.ChartObjects().Add(
        $dashboard.Range('E21').Left,
        $dashboard.Range('E21').Top,
        $dashboard.Range('E21:I32').Width,
        $dashboard.Range('E21:I32').Height
    )
    $chart = $chartObject.Chart
    $chart.SetSourceData($dashboard.Range('B21:C24'))
    $chart.ChartType = -4120
    $chart.HasTitle = $true
    $chart.ChartTitle.Text = 'وضعیت انتشار ووکامرس'
    $chart.HasLegend = $true
    $chart.Legend.Position = -4107
    try { $chart.ChartArea.Font.Name = 'Yekan Bakh' } catch {}
    try { $chart.ChartTitle.Font.Name = 'Yekan Bakh' } catch {}
    Set-OfficeTextFont $chart.ChartTitle.Format.TextFrame2.TextRange 14 $true '2F414B'
    try { $chart.Legend.Font.Name = 'Yekan Bakh' } catch {}
    Set-OfficeTextFont $chart.Legend.Format.TextFrame2.TextRange 10 $false '2F414B'
    $excel.ActiveWindow.Zoom = 90

    # Settings stays because it is useful, but every visible label is Persian.
    $settings.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $settings.Rows.Item(1).RowHeight = 60
    $settings.Columns('A').ColumnWidth = 40
    $settings.Columns('B:F').ColumnWidth = 16
    $settings.Range('A1:F2').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $settings.Range('C1:F2').Merge()
    $settings.Range('C1').Value2 = 'تنظیمات همگام‌سازی و محاسبه قیمت'
    $settings.Range('C1').Font.Name = 'Yekan Bakh'
    $settings.Range('C1').Font.Size = 17
    $settings.Range('C1').Font.Bold = $true
    $settings.Range('C1').Font.Color = ConvertTo-OleColor '2F414B'
    $settings.Range('C1').HorizontalAlignment = -4152
    $settings.Range('C1').VerticalAlignment = -4108
    [void](Add-BrandLogo $settings $LogoPath $settings.Range('A1:A2') 160 36 $true)

    $settings.Range('A3').Value2 = 'نشانی سرویس محصولات'
    $settings.Range('B3:F3').Merge()
    $settings.Range('B3').Value2 = 'http://127.0.0.1:18080/api/product-sync'
    $settings.Range('A4').Value2 = 'نشانی پل امن قیمت‌گذاری'
    $settings.Range('B4:F4').Merge()
    $settings.Range('B4').Value2 = 'http://127.0.0.1:18080/api/pricing-sync/state'
    $settings.Range('A5').Value2 = 'همگام‌سازی خودکار هنگام بازشدن'
    $settings.Range('B5:F5').Merge()
    # The network phase keeps Excel interactive and commits only after the
    # snapshot is validated, so normal opens can populate themselves safely.
    $settings.Range('B5').Value2 = 'بله'
    $settings.Range('B5').Validation.Delete()
    $settings.Range('B5').Validation.Add(3, 1, 1, 'بله,خیر')
    $settings.Range('A6').Value2 = 'وضعیت'
    $settings.Range('B6:F6').Merge()
    $settings.Range('B6').Value2 = 'هنوز همگام‌سازی نشده است.'
    $settings.Range('B6:F6').WrapText = $true
    $settings.Rows.Item(6).RowHeight = 45
    $settings.Range('A7').Value2 = 'آخرین به‌روزرسانی موفق'
    $settings.Range('B7:F7').Merge()
    $settings.Range('B7:F7').ClearContents()
    $settings.Range('B7:F7').NumberFormat = 'yyyy/mm/dd hh:mm'
    $settings.Range('A8:F8').ClearContents()
    $settings.Range('G8').Value2 = 'canonical'
    $settings.Rows.Item(8).Hidden = $true
    $settings.Range('A3:A7').Font.Bold = $true
    $settings.Range('A3:F7').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Range('B3:F7').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B3:F4').ReadingOrder = -5003

    $settings.Range('A9:F9').Merge()
    $settings.Range('A9').Value2 = 'مقادیر زنده سایت'
    Set-SectionStyle $settings.Range('A9:F9') 'DDE8FC' '242424' 12
    $liveLabels = @('? بهای یوآن سایت', '? بهای دلار سایت', 'تاریخ مؤثر یوآن', 'حاشیه سود سایت', 'نرخ حمل هوایی سایت (یوآن/کیلوگرم)', 'تعداد رقم گردکردن قیمت')
    for ($rowOffset = 0; $rowOffset -lt $liveLabels.Count; $rowOffset++) {
        $row = 10 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $liveLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    foreach ($row in @(10, 11)) {
        $settings.Range("B${row}:F${row}").NumberFormat = '#,##0'
    }
    $settings.Range('B13').NumberFormat = '0%'
    $settings.Range('B14').NumberFormat = '0.############'
    $settings.Range('B15').NumberFormat = '0'
    $settings.Range('A10:A15').Font.Bold = $true
    $settings.Range('A10:F15').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B10:F15').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B10:F11').Interior.Color = ConvertTo-OleColor 'FFF2CC'
    $settings.Range('B10:F15').HorizontalAlignment = -4108

    $settings.Rows.Item(16).RowHeight = 24
    [void](Add-OperationProgressSurface $settings $settings.Range('A16') $settings.Range('A16:F16').Width 20)

    $settings.Range('A17:F17').Merge()
    $settings.Range('A17').Value2 = 'مقادیر پیشنهادی این فایل'
    Set-SectionStyle $settings.Range('A17:F17') 'DDE8FC' '242424' 12
    $proposalLabels = @('بهای یوآن', 'بهای دلار', 'تاریخ مؤثر یوآن', 'حاشیه سود', 'نرخ حمل هوایی (یوآن/کیلوگرم)', 'وضعیت پیش‌نمایش')
    for ($rowOffset = 0; $rowOffset -lt $proposalLabels.Count; $rowOffset++) {
        $row = 18 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $proposalLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    $settings.Range('B20:F20').UnMerge()
    $settings.Range('B20:C20').Merge()
    $settings.Range('D20').Value2 = 'تاریخ مؤثر دلار'
    $settings.Range('E20:F20').Merge()
    $settings.Range('D20').Font.Bold = $true
    foreach ($row in @(18, 19)) {
        $settings.Range("B${row}:F${row}").NumberFormat = '#,##0'
    }
    $settings.Range('B21').NumberFormat = '0%'
    $settings.Range('B22').NumberFormat = '0.############'
    $settings.Range('A18:A23').Font.Bold = $true
    $settings.Range('A18:F23').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B18:F23').Interior.Color = ConvertTo-OleColor 'FFF8E7'
    $settings.Range('B18:F23').HorizontalAlignment = -4108
    [void]$workbook.Names.Add('ConfirmedCNYRate', $settings.Range('G18'))
    $settings.Range('B22').Validation.Delete()
    $settings.Range('B22').Validation.Add(2, 1, 5, '0')
    $settings.Range('B22').Validation.ErrorTitle = 'نرخ حمل نامعتبر'
    $settings.Range('B22').Validation.ErrorMessage = 'نرخ حمل باید عددی بزرگ‌تر از صفر باشد.'
    $settings.Range('B22').Validation.ShowError = $true
    foreach ($row in (10..14)) {
        $settings.Range("B${row}:F${row}").ReadingOrder = -5003
        $settings.Range("B${row}:F${row}").Font.Name = 'Yekan Bakh'
    }
    foreach ($row in (18..22)) {
        $settings.Range("B${row}:F${row}").ReadingOrder = -5003
        $settings.Range("B${row}:F${row}").Font.Name = 'Yekan Bakh'
    }

    $settings.Range('A24').Value2 = 'حد هشدار اختلاف قیمت'
    $settings.Range('B24').Value2 = 0.07
    $settings.Range('B24').NumberFormat = '0%'
    $settings.Range('A25').Value2 = 'حد هشدار قدمت نرخ (روز)'
    $settings.Range('B25').Value2 = 7
    $settings.Range('A24:A25').Font.Bold = $true
    $settings.Range('A24:F25').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Range('B24:F25').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('A26').Value2 = 'تعداد رقم گردکردن قیمت پیشنهادی'
    $settings.Range('B26:F26').Merge()
    $settings.Range('B26').NumberFormat = '0'
    $settings.Range('B26').Interior.Color = ConvertTo-OleColor 'FFF8E7'
    $settings.Range('B26').HorizontalAlignment = -4108
    $settings.Range('A26').Font.Bold = $true
    $settings.Range('A26:F26').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B26').Validation.Delete()
    $settings.Range('B26').Validation.Add(1, 1, 1, '0', '9')
    $settings.Range('B26').Validation.ErrorTitle = 'تعداد رقم نامعتبر'
    $settings.Range('B26').Validation.ErrorMessage = 'تعداد رقم گردکردن باید عددی صحیح از صفر تا ۹ باشد.'
    $settings.Range('B26').Validation.ShowError = $true
    $settings.Range('A27:F27').ClearContents()
    $settings.Range('A3:A32').WrapText = $true
    $settings.Rows.Item(14).RowHeight = 30
    $settings.Rows.Item(22).RowHeight = 30

    # Settings edits stay local and amber until this one explicit batch action.
    # The companion performs preview/apply/readback in its background worker;
    # Excel turns fields green only after the website ACK is complete.
    [void](Add-ActionButton $settings 'همگام‌سازی اکنون' 'ProductCatalogSync.SyncPricingSettingsNow' $settings.Range('A28') $settings.Range('A28:F29').Width $settings.Range('A28:F29').Height)

    $settings.Range('A31:F37').ClearContents()
    $settings.Range('A31').Value2 = 'نمایش تصاویر محصولات'
    $settings.Range('B31:F31').Merge()
    $settings.Range('B31').Value2 = 'خیر'
    $settings.Range('B31:F31').NumberFormat = '@'
    $settings.Range('B31:F31').Interior.Color = ConvertTo-OleColor 'FFF8E7'
    $settings.Range('A31:F31').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('A31').Font.Bold = $true
    $settings.Range('B31').Validation.Delete()
    $settings.Range('B31').Validation.Add(3, 1, 1, 'خیر,بله')
    [void]$workbook.Names.Add('ShowProductImages', $settings.Range('B31'))
    $settings.Range('A37:F37').Merge()
    $settings.Range('A37').Value2 = 'برای حفظ سرعت، فقط تصویر کالای انتخاب‌شده دریافت می‌شود.'
    $settings.Range('A37:F37').WrapText = $true
    $settings.Range('A37:F37').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('A37:F37').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Rows.Item(32).RowHeight = 32

    $settings.Range('A38:F38').Merge()
    $settings.Range('A38').Value2 = 'سیاست قلم'
    Set-SectionStyle $settings.Range('A38:F38') 'DDE8FC' '242424' 12
    $fontPolicy = @(
        @{ Row = 39; Label = 'قلم فارسی و متن‌های ترکیبی'; Value = 'Yekan Bakh'; Name = 'PersianFont' },
        @{ Row = 40; Label = 'قلم لاتین و شناسه‌های فنی'; Value = 'Segoe UI'; Name = 'LatinFont' },
        @{ Row = 41; Label = 'حالت ممیزی قلم'; Value = 'ترمیم و هشدار'; Name = 'FontAuditMode' },
        @{ Row = 42; Label = 'اعتبارسنجی قلم هنگام بازشدن'; Value = 'بله'; Name = 'ValidateFontsOnOpen' },
        @{ Row = 43; Label = 'اجازه قلم جایگزین'; Value = 'خیر'; Name = 'AllowFallback' },
        @{ Row = 44; Label = 'نمایش رقم‌های فارسی در قیمت فروش'; Value = 'بله'; Name = 'PriceDisplayFaNum' }
    )
    foreach ($setting in $fontPolicy) {
        $row = $setting.Row
        $settings.Cells.Item($row, 1).Value2 = $setting.Label
        $settings.Range("B${row}:F${row}").Merge()
        $settings.Range("B${row}").Value2 = $setting.Value
        $settings.Range("B${row}:F${row}").NumberFormat = '@'
        $settings.Range("B${row}:F${row}").Interior.Color = ConvertTo-OleColor 'FFF8E7'
        $settings.Range("A${row}:F${row}").Borders.Color = ConvertTo-OleColor 'B9CCF4'
        [void]$workbook.Names.Add($setting.Name, $settings.Range("B${row}"))
    }
    $settings.Range('B41').Validation.Add(3, 1, 1, 'خاموش,هشدار,ترمیم و هشدار,سختگیرانه')
    $settings.Range('B42').Validation.Add(3, 1, 1, 'بله,خیر')
    $settings.Range('B43').Validation.Add(3, 1, 1, 'بله,خیر')
    $settings.Range('B44').Validation.Add(3, 1, 1, 'بله,خیر')

    $settings.Range('A45:F45').Merge()
    $settings.Range('A45').Value2 = 'زمان‌بندی مرحله‌های همگام‌سازی (ثانیه)'
    Set-SectionStyle $settings.Range('A45:F45') 'DDE8FC' '242424' 12
    $phaseLabels = @(
        'دریافت نشست محلی',
        'دریافت قرارداد',
        'دریافت وضعیت محصول و سایت',
        'جزئیات دریافت صفحه‌ها',
        'تطبیق رکوردها',
        'محاسبه قیمت',
        'نوشتن گروهی جدول',
        'پیوندها و قالب‌بندی',
        'محاسبه اکسل',
        'ذخیره فایل'
    )
    for ($rowOffset = 0; $rowOffset -lt $phaseLabels.Count; $rowOffset++) {
        $row = 46 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $phaseLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
        $settings.Range("B${row}:F${row}").ClearContents()
        $settings.Range("B${row}:F${row}").NumberFormat = if ($row -eq 49) { '@' } else { '0.000' }
        $settings.Range("B${row}:F${row}").Interior.Color = ConvertTo-OleColor 'F6F6F6'
        $settings.Range("A${row}:F${row}").Borders.Color = ConvertTo-OleColor 'B9CCF4'
    }
    $settings.Range('A39:A55').Font.Bold = $true
    $settings.Range('A38:A55').WrapText = $true

    # Hidden base values, state/shipping revisions, and preview metadata are
    # runtime-only conflict guards. The template itself persists no live values.
    $settings.Range('G18:G22').ClearContents()
    $settings.Range('G14:G15').ClearContents()
    $settings.Range('G26:G28').ClearContents()
    $settings.Range('H14:H17').ClearContents()
    $settings.Range('G30:G55').ClearContents()
    $settings.Range('G30').Value2 = 0
    $settings.Range('G48').Value2 = 0
    [void]$workbook.Names.Add('SelectedProductRow', $settings.Range('G30'))
    [void]$workbook.Names.Add('ProjectedPricePreviewRow', $settings.Range('G48'))
    $settings.Columns('G').Hidden = $true
    $settings.Columns('H').Hidden = $true

    # Never format only the first column of several adjacent merged rows.
    # Excel silently coalesces those rows into one large MergeArea, which
    # makes every setting except the first one inaccessible to VBA.
    foreach ($row in @((10..15) + 18 + 19 + (21..23) + 26 + 31 + (39..44) + (46..55))) {
        $mergeCell = $settings.Range("B${row}")
        $mergeArea = $mergeCell.MergeArea
        try {
            $expectedMergeAddress = "B${row}:F${row}"
            $actualMergeAddress = $mergeArea.Address($false, $false)
            if ($actualMergeAddress -cne $expectedMergeAddress) {
                throw "Settings row $row has MergeArea $actualMergeAddress; expected $expectedMergeAddress."
            }
        }
        finally {
            Release-ComObject $mergeArea
            Release-ComObject $mergeCell
        }
    }
    $noteMerge = $settings.Range('A37').MergeArea.Address($false, $false)
    if ($noteMerge -cne 'A37:F37') {
        throw "Settings image-preview note has MergeArea $noteMerge; expected A37:F37."
    }
    foreach ($dateMergeAddress in @('B20:C20', 'E20:F20')) {
        $mergeCell = $settings.Range($dateMergeAddress.Split(':')[0])
        $mergeArea = $mergeCell.MergeArea
        try {
            $actualMergeAddress = $mergeArea.Address($false, $false)
            if ($actualMergeAddress -cne $dateMergeAddress) {
                throw "Settings date field has MergeArea $actualMergeAddress; expected $dateMergeAddress."
            }
        }
        finally {
            Release-ComObject $mergeArea
            Release-ComObject $mergeCell
        }
    }

    # Apply the fixed role map after all cells/shapes exist. Font selection is
    # role-based only; the sole numeric variant is the explicit selling-price
    # FaNum toggle. Technical numerics and identifiers always remain Latin.
    Set-RangeFontSlots $priceList.Range('B1:K6') 'Yekan Bakh'
    Set-RangeFontSlots $priceList.Range('B6') 'Yekan Bakh FaNum'
    Set-RangeFontSlots $priceList.Range('C6,F6:H6,J6') 'Segoe UI'
    Set-RangeFontSlots $priceList.Range('I6,K6') 'Yekan Bakh'
    Set-RangeFontSlots $dashboard.Range('B1:I34') 'Yekan Bakh'
    Set-RangeFontSlots $dashboard.Range('B5:C6,D5:E6,F5:G6,H5:I6,B9:C10,D9:E10,F9:G10,H9:I10,B13:E14,F13:G14,H13:I14,C22:C24,C27:C29,C32:C34') 'Segoe UI'
    Set-RangeFontSlots $settings.Range('A1:F55') 'Yekan Bakh'
    Set-RangeFontSlots $settings.Range('B3:F4,B7:F7,B10:F15,B18:F22,B24:F26,B39:F40,B46:F55') 'Segoe UI'
    Set-RangeFontSlots $syncData.Range('A1:X1') 'Yekan Bakh'
    Set-RangeFontSlots $syncData.Range('A2:P2,U2:X2') 'Segoe UI'
    Set-RangeFontSlots $syncData.Range('Q2:T2') 'Yekan Bakh'
    foreach ($sheet in @($priceList, $dashboard, $settings)) {
        for ($shapeIndex = 1; $shapeIndex -le $sheet.Shapes.Count; $shapeIndex++) {
            $shape = $sheet.Shapes.Item($shapeIndex)
            try {
                $shapeAction = ''
                try { $shapeAction = [string]$shape.OnAction } catch {}
                if (-not [string]::IsNullOrWhiteSpace($shapeAction)) {
                    Set-OfficeTextFont $shape.TextFrame2.TextRange 11 $true 'FFFFFF'
                    try {
                        $shape.TextFrame.Characters().Font.Name = 'Yekan Bakh'
                        $shape.TextFrame.Characters().Font.NameComplexScript = 'Yekan Bakh'
                        $shape.TextFrame.Characters().Font.NameFarEast = 'Yekan Bakh'
                    } catch {}
                    Assert-ShapeFontSlots $shape 'Yekan Bakh' "action shape $($shape.Name)"
                }
            }
            finally { Release-ComObject $shape }
        }
    }
    foreach ($sheet in @($priceList, $settings)) {
        foreach ($shapeName in @('OperationProgressTrack', 'OperationProgressFill', 'OperationProgressText')) {
            $shape = $sheet.Shapes.Item($shapeName)
            try {
                if ($shape.Width -le 0 -or $shape.Height -le 0) {
                    throw "$($sheet.Name) progress shape $shapeName has invalid dimensions."
                }
                if ($shapeName -eq 'OperationProgressText') {
                    Assert-ShapeFontSlots $shape 'Yekan Bakh' "$($sheet.Name) progress label"
                }
            }
            finally { Release-ComObject $shape }
        }
    }

    if ([double]$priceList.Columns('B').ColumnWidth -lt 20) {
        throw "Products column B is narrower than the required 20-character minimum."
    }
    if ([double]$priceList.Columns('K').ColumnWidth -lt 34) {
        throw "Products column K is narrower than the required 34-character minimum."
    }
    Assert-RangeFontSlots $priceList.Range('B5:K5') 'Yekan Bakh' 'Products headers'
    Assert-RangeFontSlots $priceList.Range('C3:E3') 'Yekan Bakh' 'search input'
    Assert-RangeFontSlots $priceList.Range('B6') 'Yekan Bakh FaNum' 'selling-price FaNum role'
    Assert-RangeFontSlots $priceList.Range('C6') 'Segoe UI' 'weight role'
    Assert-RangeFontSlots $priceList.Range('H6,J6') 'Segoe UI' 'SKU and Woo ID roles'
    Assert-RangeFontSlots $priceList.Range('I6,K6') 'Yekan Bakh' 'name and category roles'
    Assert-RangeFontSlots $dashboard.Range('B1:I4') 'Yekan Bakh' 'dashboard text'
    Assert-RangeFontSlots $dashboard.Range('C22:C24') 'Segoe UI' 'dashboard counts'
    Assert-RangeFontSlots $settings.Range('A1:F2') 'Yekan Bakh' 'settings text'
    Assert-RangeFontSlots $settings.Range('B39:F40') 'Segoe UI' 'font family values'
    Assert-RangeFontSlots $settings.Range('B41:F44') 'Yekan Bakh' 'localized font policy values'
    Assert-RangeFontSlots $syncData.Range('A1:X1') 'Yekan Bakh' 'SyncData headers'

    $priceList.PageSetup.PrintArea = '$B$1:$O$30'
    $priceList.PageSetup.Orientation = 2
    $priceList.PageSetup.Zoom = $false
    $priceList.PageSetup.FitToPagesWide = 1
    $priceList.PageSetup.FitToPagesTall = 1
    $settings.PageSetup.PrintArea = '$A$1:$F$55'
    $settings.PageSetup.Zoom = $false
    $settings.PageSetup.FitToPagesWide = 1
    $settings.PageSetup.FitToPagesTall = 1
    $dashboard.PageSetup.PrintArea = '$B$1:$I$34'
    $dashboard.PageSetup.Zoom = $false
    $dashboard.PageSetup.FitToPagesWide = 1
    $dashboard.PageSetup.FitToPagesTall = 1
    $priceList.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $excel.ActiveWindow.DisplayRightToLeft = $true
    $excel.ActiveWindow.Zoom = 90

    # Early binding is required for WinHttp.WinHttpRequest WithEvents callbacks.
    # Fail closed if the registered 5.1 type library cannot be resolved exactly.
    $winHttpReference = $null
    try {
        $winHttpReference = $workbook.VBProject.References.AddFromGuid(
            '{662901FC-6951-4854-9EB2-D9A2570F2B2E}',
            5,
            1
        )
        if (
            [string]$winHttpReference.Guid -ine '{662901FC-6951-4854-9EB2-D9A2570F2B2E}' -or
            [int]$winHttpReference.Major -ne 5 -or
            [int]$winHttpReference.Minor -ne 1 -or
            [bool]$winHttpReference.IsBroken
        ) {
            throw 'The Microsoft WinHTTP Services 5.1 reference is missing or broken.'
        }
    }
    catch {
        throw "Could not bind Microsoft WinHTTP Services 5.1: $($_.Exception.Message)"
    }
    finally {
        Release-ComObject $winHttpReference
    }

    # Import the auditable checked-in parser/runtime, callback classes,
    # dashboard module, and workbook events. Import errors are fatal.
    [void]$workbook.VBProject.VBComponents.Import($jsonValuePath)
    [void]$workbook.VBProject.VBComponents.Import($jsonRuntimePath)
    $asyncComponent = $workbook.VBProject.VBComponents.Import($asyncWinHttpRequestPath)
    $sseParserComponent = $workbook.VBProject.VBComponents.Import($pricingSseParserPath)
    foreach ($classContract in @(
        @{
            Component = $asyncComponent
            Name = 'AsyncWinHttpRequest'
            Required = 'Private WithEvents mHttp As WinHttp.WinHttpRequest'
        },
        @{
            Component = $sseParserComponent
            Name = 'PricingSseParser'
            Required = 'Public Function Feed(ByVal Chunk As Variant) As Collection'
        }
    )) {
        $component = $classContract.Component
        if ($null -eq $component -or
            [string]$component.Name -cne $classContract.Name -or
            [int]$component.Type -ne 2) {
            throw "Imported VBA class '$($classContract.Name)' has the wrong identity or type."
        }
        $codeModule = $component.CodeModule
        try {
            $lineCount = [int]$codeModule.CountOfLines
            if ($lineCount -lt 1) {
                throw "Imported VBA class '$($classContract.Name)' is empty."
            }
            $classSource = [string]$codeModule.Lines(1, $lineCount)
            if ($classSource.IndexOf(
                    [string]$classContract.Required,
                    [StringComparison]::Ordinal
                ) -lt 0) {
                throw "Imported VBA class '$($classContract.Name)' failed its source contract."
            }
        }
        finally {
            Release-ComObject $codeModule
        }
    }
    Release-ComObject $asyncComponent
    Release-ComObject $sseParserComponent
    [void]$workbook.VBProject.VBComponents.Import($vbaModulePath)
    $messageFormComponent = Add-DigitalogicMessageForm $workbook
    Release-ComObject $messageFormComponent
    $thisWorkbookComponent = $workbook.VBProject.VBComponents.Item('ThisWorkbook')
    $thisWorkbookCode = Get-Content -Raw -Encoding UTF8 $thisWorkbookPath
    $thisWorkbookComponent.CodeModule.AddFromString($thisWorkbookCode)

    # Run a non-networking macro to force VBA parsing and validate the expected
    # sheet, table, and configuration contracts before packaging.
    try {
        $excel.Run("'$($workbook.Name)'!ProductCatalogSync.ValidateWorkbook")
    }
    catch {
        $validationError = $_
        # Preserve the failed package at the requested diagnostic path so the
        # native VBA editor can identify the exact compile location.
        try {
            $workbook.SaveAs($OutputPath, 53)
            $workbook.Save()
        }
        catch {
            Write-Warning ("Could not preserve the failed diagnostic workbook: " + $_.Exception.Message)
        }
        throw "ValidateWorkbook failed before packaging: $($validationError.Exception.Message)"
    }
    # 53 = xlOpenXMLTemplateMacroEnabled (.xltm). Opening the canonical file
    # creates a separate workbook instance for any later Save As operation.
    try {
        $workbook.SaveAs($OutputPath, 53)
    }
    catch {
        throw "Saving the verified macro-enabled template failed: $($_.Exception.Message)"
    }
    $workbook.Save()

    $priceList.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'price-list.pdf'))
    $dashboard.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'dashboard.pdf'))
    $settings.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'settings.pdf'))

    $excelVersion = [string]$excel.Version
    $vbaComponents = [int]$workbook.VBProject.VBComponents.Count
    $workbook.Close($true)
    Release-ComObject $workbook
    $workbook = $null
    $excel.Quit()
    Release-ComObject $excel
    $excel = $null

    Remove-ExcelPrivatePackageMetadata $OutputPath

    # A runtime-instantiated workbook may contain a complete live catalog. Never
    # let such a workbook become the packaged .xltm: the release artifact must
    # retain the table/layout contract but ship zero product or SyncData rows.
    $templateDataAudit = & $templateDataAuditPath -Path $OutputPath

    $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $OutputPath
    $utf8NoBom = [Text.UTF8Encoding]::new($false)
    Copy-Item -LiteralPath $OutputPath -Destination $DistributionCopyPath -Force
    $distributionDataAudit = & $templateDataAuditPath -Path $DistributionCopyPath
    $manifestEntries = @{}
    if (Test-Path -LiteralPath $ChecksumManifestPath) {
        foreach ($line in [IO.File]::ReadAllLines($ChecksumManifestPath, [Text.Encoding]::UTF8)) {
            if ($line -match '\A([0-9a-fA-F]{64})  (.+)\z') {
                $manifestEntries[$matches[2]] = $matches[1].ToLowerInvariant()
            }
        }
    }
    foreach ($obsoleteName in @(
        'Digitalogic-Price-Calculator.xltm',
        'لیست قیمت دیجیتالاجیک - استاندارد.xltm',
        'لیست قیمت دیجیتالاجیک - پیشرفته.xltm',
        'لیست قیمت پاتریس و دیجیتالاجیک - استاندارد.xltm',
        'لیست قیمت پاتریس و دیجیتالاجیک - پیشرفته.xltm'
    )) {
        [void]$manifestEntries.Remove($obsoleteName)
    }
    $manifestEntries[[IO.Path]::GetFileName($OutputPath)] = $hash.Hash.ToLowerInvariant()
    $manifestText = (
        $manifestEntries.GetEnumerator() |
            Sort-Object -Property Name |
            ForEach-Object { "$($_.Value)  $($_.Name)" }
    ) -join "`n"
    [IO.File]::WriteAllText($ChecksumManifestPath, ($manifestText + "`n"), $utf8NoBom)
    [pscustomobject]@{
        edition = 'canonical'
        output = $OutputPath
        distribution_copy = $DistributionCopyPath
        sha256 = $hash.Hash.ToLowerInvariant()
        checksum_manifest = $ChecksumManifestPath
        excel_version = $excelVersion
        vba_components = $vbaComponents
        preview_directory = $PreviewDirectory
        template_product_records = [int]$templateDataAudit.product_records
        distribution_product_records = [int]$distributionDataAudit.product_records
    } | ConvertTo-Json -Compress
}
finally {
    if ($null -ne $workbook) {
        try { $workbook.Close($false) } catch {}
    }
    if ($null -ne $excel) {
        try { $excel.Quit() } catch {}
    }
    Release-ComObject $workbook
    Release-ComObject $excel
    if ($null -ne $previousAccessVBOM) {
        New-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -PropertyType DWord -Value $previousAccessVBOM.AccessVBOM -Force | Out-Null
    }
    else {
        Remove-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -ErrorAction SilentlyContinue
        if (-not $excelSecurityPathExisted) {
            Remove-Item -LiteralPath $excelSecurityPath -ErrorAction SilentlyContinue
        }
    }
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}
