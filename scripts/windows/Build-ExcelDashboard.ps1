[CmdletBinding()]
param(
    [string]$OutputPath,
    [string]$DistributionCopyPath,
    [string]$PreviewDirectory
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path $repoRoot 'docs\examples\Patris-Digitalogic-Price-Calculator.xltm'
}
if ([string]::IsNullOrWhiteSpace($DistributionCopyPath)) {
    $DistributionCopyPath = Join-Path $repoRoot 'outputs\patris-excel-15\Patris-Digitalogic-Price-Calculator.xltm'
}
if ([string]::IsNullOrWhiteSpace($PreviewDirectory)) {
    $PreviewDirectory = Join-Path $repoRoot 'outputs\patris-excel-15\preview'
}
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
$DistributionCopyPath = [IO.Path]::GetFullPath($DistributionCopyPath)
$PreviewDirectory = [IO.Path]::GetFullPath($PreviewDirectory)
$vbaModulePath = Join-Path $repoRoot 'docs\examples\vba\PatrisDashboard.bas'
$jsonRuntimePath = Join-Path $repoRoot 'docs\examples\vba\JsonRuntime.bas'
$jsonValuePath = Join-Path $repoRoot 'docs\examples\vba\JsonValue.cls'
$thisWorkbookPath = Join-Path $repoRoot 'docs\examples\vba\ThisWorkbook.cls'
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
    $Range.Font.Name = 'Yekan Bakh FaNum'
    $Range.Font.Size = $FontSize
    $Range.Font.Bold = $true
    $Range.VerticalAlignment = -4108
}

function Add-ActionButton($Sheet, [string]$Text, [string]$Macro, $Anchor, [double]$Width = 112, [double]$Height = 30) {
    $shape = $Sheet.Shapes.AddShape(5, $Anchor.Left, $Anchor.Top, $Width, $Height)
    $shape.Fill.ForeColor.RGB = ConvertTo-OleColor '1C61E7'
    $shape.Line.ForeColor.RGB = ConvertTo-OleColor '174FC0'
    $shape.TextFrame2.TextRange.Text = $Text
    $shape.TextFrame2.TextRange.Font.Name = 'Yekan Bakh FaNum'
    $shape.TextFrame2.TextRange.Font.Size = 11
    $shape.TextFrame2.TextRange.Font.Bold = $true
    $shape.TextFrame2.TextRange.Font.Fill.ForeColor.RGB = ConvertTo-OleColor 'FFFFFF'
    $shape.TextFrame2.VerticalAnchor = 3
    $shape.TextFrame2.TextRange.ParagraphFormat.Alignment = 2
    $shape.OnAction = $Macro
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

New-Item -ItemType Directory -Force (Split-Path -Parent $OutputPath) | Out-Null
New-Item -ItemType Directory -Force (Split-Path -Parent $DistributionCopyPath) | Out-Null
New-Item -ItemType Directory -Force $PreviewDirectory | Out-Null

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

    # The canonical workbook intentionally keeps the familiar Persian price-list
    # layout. Product rows are empty in the template and are created only by Sync.
    $priceList = $workbook.Worksheets.Item(1)
    $priceList.Name = 'لیست قیمت'
    $settings = $workbook.Worksheets.Add([Type]::Missing, $priceList)
    $settings.Name = 'تنظیمات'

    foreach ($sheet in @($priceList, $settings)) {
        $sheet.Cells.Font.Name = 'Yekan Bakh FaNum'
        $sheet.Cells.Font.Size = 11
        $sheet.Cells.Font.Color = ConvertTo-OleColor '282828'
        $sheet.Cells.ReadingOrder = -5004
        $sheet.Activate()
        $excel.ActiveWindow.DisplayRightToLeft = $true
    }

    # Main sheet: the original eight visible columns, in the original order.
    $priceList.Activate()
    $excel.ActiveWindow.DisplayGridlines = $true
    $priceList.Range('B3').Value2 = 'لیست کالاها'
    $priceList.Range('B3').Font.Size = 16
    $priceList.Range('B3').Font.Bold = $true
    $priceList.Range('B3').Font.Color = ConvertTo-OleColor '242424'

    $headers = @(
        'فی فروش',
        'گرم',
        'سایر',
        'فی فروش2',
        'نرخ ارزی',
        'همه انبارها',
        'کد کالا',
        'نام کالا'
    )
    for ($column = 0; $column -lt $headers.Count; $column++) {
        $priceList.Cells.Item(5, $column + 2).Value2 = $headers[$column]
    }
    $productTable = $priceList.ListObjects.Add(1, $priceList.Range('B5:I6'), $null, 1)
    $productTable.Name = 'Products'
    $productTable.TableStyle = 'TableStyleMedium2'
    [void]$productTable.DataBodyRange.Delete()
    $priceList.Range('B5:I5').Interior.Color = ConvertTo-OleColor '1C61E7'
    $priceList.Range('B5:I5').Font.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range('B5:I5').Font.Bold = $true
    $priceList.Range('B5:I5').HorizontalAlignment = -4108
    $priceList.Range('B5:I5').VerticalAlignment = -4108
    $priceList.Range('B5:I5').Borders.Color = ConvertTo-OleColor '174FC0'
    $priceList.Range('B5:I5').Borders.Weight = 2
    $priceList.Rows.Item(5).RowHeight = 31

    $priceList.Columns('B').ColumnWidth = 18
    $priceList.Columns('C').ColumnWidth = 13.57
    $priceList.Columns('D').ColumnWidth = 19.14
    $priceList.Columns('E').ColumnWidth = 17.43
    $priceList.Columns('F').ColumnWidth = 14.57
    $priceList.Columns('G').ColumnWidth = 17.71
    $priceList.Columns('H').ColumnWidth = 17.43
    $priceList.Columns('I').ColumnWidth = 59.86
    $priceList.Columns('J').ColumnWidth = 2.29
    $priceList.Columns('K').ColumnWidth = 1.71
    $priceList.Columns('L').ColumnWidth = 2.43
    $priceList.Columns('M').ColumnWidth = 12.71
    $priceList.Columns('N').ColumnWidth = 2.86
    $priceList.Columns('O').ColumnWidth = 12.71
    $priceList.Columns('B').NumberFormat = '#,##0'
    $priceList.Columns('C').NumberFormat = '#,##0.##'
    $priceList.Columns('E').NumberFormat = '#,##0'
    $priceList.Columns('F').NumberFormat = '#,##0.####'
    $priceList.Columns('G').NumberFormat = '#,##0.##'
    $priceList.Columns('B').Font.Bold = $true
    $priceList.Columns('B:H').HorizontalAlignment = -4152
    $priceList.Columns('I').HorizontalAlignment = -4131

    # Keep the three original configuration tables and their familiar cells.
    $priceList.Range('M6').Value2 = 'بهای یوآن'
    $priceList.Range('M7').Value2 = 29500
    $yuanTable = $priceList.ListObjects.Add(1, $priceList.Range('M6:M7'), $null, 1)
    $yuanTable.Name = 'Yuan_Price'
    $yuanTable.TableStyle = 'TableStyleMedium2'

    $priceList.Range('O6').Value2 = 'نرخ حمل'
    $priceList.Range('O7').Value2 = 120
    $shippingTable = $priceList.ListObjects.Add(1, $priceList.Range('O6:O7'), $null, 1)
    $shippingTable.Name = 'Shipping'
    $shippingTable.TableStyle = 'TableStyleMedium2'

    $priceList.Range('O9').Value2 = 'درصد سود'
    $priceList.Range('O10').Value2 = 0.3
    $profitTable = $priceList.ListObjects.Add(1, $priceList.Range('O9:O10'), $null, 1)
    $profitTable.Name = 'Profit'
    $profitTable.TableStyle = 'TableStyleMedium2'

    foreach ($configHeader in @($priceList.Range('M6'), $priceList.Range('O6'), $priceList.Range('O9'))) {
        $configHeader.Interior.Color = ConvertTo-OleColor '1C61E7'
        $configHeader.Font.Color = ConvertTo-OleColor 'FFFFFF'
        $configHeader.Font.Bold = $true
        $configHeader.HorizontalAlignment = -4108
    }
    foreach ($configValue in @($priceList.Range('M7'), $priceList.Range('O7'), $priceList.Range('O10'))) {
        $configValue.Interior.Color = ConvertTo-OleColor 'DDE8FC'
        $configValue.Font.Color = ConvertTo-OleColor '242424'
        $configValue.Font.Bold = $true
        $configValue.HorizontalAlignment = -4108
        $configValue.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    }
    $priceList.Range('M7').NumberFormat = '#,##0'
    $priceList.Range('O7').NumberFormat = '#,##0.##'
    $priceList.Range('O10').NumberFormat = '0%'

    [void](Add-ActionButton $priceList 'همگام‌سازی اکنون' 'PatrisDashboard.RefreshAllData' $priceList.Range('M3') $priceList.Range('M3:O4').Width $priceList.Range('M3:O4').Height)
    $priceList.Range('M12:O12').Merge()
    $priceList.Range('M12').Value2 = 'وضعیت همگام‌سازی'
    Set-SectionStyle $priceList.Range('M12:O12') 'F6F6F6' '242424' 10
    $priceList.Range('M13:O14').Merge()
    $priceList.Range('M13').Formula = "='تنظیمات'!B6"
    $priceList.Range('M13:O14').WrapText = $true
    $priceList.Range('M13:O14').HorizontalAlignment = -4108
    $priceList.Range('M13:O14').VerticalAlignment = -4108
    $priceList.Range('M13:O14').Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $priceList.Range('M13:O14').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $priceList.Range('M16:O16').Merge()
    $priceList.Range('M16').Value2 = 'آخرین به‌روزرسانی'
    Set-SectionStyle $priceList.Range('M16:O16') 'F6F6F6' '242424' 10
    $priceList.Range('M17:O17').Merge()
    $priceList.Range('M17').Formula = "=IF('تنظیمات'!B7="""","""",'تنظیمات'!B7)"
    $priceList.Range('M17:O17').NumberFormat = 'yyyy/mm/dd hh:mm'
    $priceList.Range('M17:O17').HorizontalAlignment = -4108
    $priceList.Range('M17:O17').Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $priceList.Range('M17:O17').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $priceList.Range('B5:I1000').Borders.Color = ConvertTo-OleColor 'D9D9D9'

    $excel.ActiveWindow.SplitRow = 5
    $excel.ActiveWindow.FreezePanes = $true
    $excel.ActiveWindow.Zoom = 90

    # Settings stays because it is useful, but every visible label is Persian.
    $settings.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $settings.Columns('A').ColumnWidth = 28
    $settings.Columns('B:F').ColumnWidth = 18
    $settings.Range('A1:F2').Merge()
    $settings.Range('A1').Value2 = 'تنظیمات همگام‌سازی و محاسبه قیمت'
    Set-SectionStyle $settings.Range('A1:F2') '1C61E7' 'FFFFFF' 17
    $settings.Range('A1:F2').HorizontalAlignment = -4108

    $settings.Range('A3').Value2 = 'نشانی سرویس پاتریس'
    $settings.Range('B3:F3').Merge()
    $settings.Range('B3').Value2 = 'http://127.0.0.1:18080/api/product-sync'
    $settings.Range('A4').Value2 = 'نشانی عمومی ووکامرس'
    $settings.Range('B4:F4').Merge()
    $settings.Range('B4').Value2 = 'https://digitalogic.ir/wp-json/wc/store/v1/products'
    $settings.Range('A5').Value2 = 'همگام‌سازی خودکار هنگام بازشدن'
    $settings.Range('B5').Value2 = 'بله'
    $settings.Range('B5').Validation.Delete()
    $settings.Range('B5').Validation.Add(3, 1, 1, 'بله,خیر')
    $settings.Range('A6').Value2 = 'وضعیت'
    $settings.Range('B6:F6').Merge()
    $settings.Range('B6').Value2 = 'هنوز همگام‌سازی نشده است.'
    $settings.Range('A7').Value2 = 'آخرین به‌روزرسانی موفق'
    $settings.Range('B7:F7').Merge()
    $settings.Range('B7:F7').ClearContents()
    $settings.Range('B7:F7').NumberFormat = 'yyyy/mm/dd hh:mm'
    $settings.Range('A3:A7').Font.Bold = $true
    $settings.Range('A3:F7').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Range('B3:F7').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B3:F4').ReadingOrder = -5003

    $settings.Range('A9:F9').Merge()
    $settings.Range('A9').Value2 = 'مقادیر محاسبه قیمت'
    Set-SectionStyle $settings.Range('A9:F9') 'DDE8FC' '242424' 12
    $settings.Range('A10').Value2 = 'بهای یوآن'
    $settings.Range('B10:F10').Merge()
    $settings.Range('B10').Formula = "='لیست قیمت'!M7"
    $settings.Range('A11').Value2 = 'نرخ حمل CNY'
    $settings.Range('B11:F11').Merge()
    $settings.Range('B11').Formula = "='لیست قیمت'!O7"
    $settings.Range('A12').Value2 = 'درصد سود'
    $settings.Range('B12:F12').Merge()
    $settings.Range('B12').Formula = "='لیست قیمت'!O10"
    $settings.Range('B10:F11').NumberFormat = '#,##0'
    $settings.Range('B12:F12').NumberFormat = '0%'
    $settings.Range('A10:A12').Font.Bold = $true
    $settings.Range('A10:F12').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B10:F12').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B10:F12').ReadingOrder = -5003
    $settings.Range('B10:F12').HorizontalAlignment = -4108

    $settings.Range('A14:F17').Merge()
    $settings.Range('A14').Value2 = "این سه مقدار همان تنظیمات فایل قبلی هستند و از صفحه «لیست قیمت» ویرایش می‌شوند.`nردیف‌های کالا در قالب ذخیره نمی‌شوند؛ دکمه همگام‌سازی آن‌ها را از پاتریس می‌گیرد و لینک ووکامرس را با شناسه WooID اضافه می‌کند.`nاگر وزن یا نرخ ارزی موجود نباشد، قیمت نهایی خالی می‌ماند و هیچ خطای #N/A نمایش داده نمی‌شود."
    $settings.Range('A14:F17').WrapText = $true
    $settings.Range('A14:F17').VerticalAlignment = -4160
    $settings.Range('A14:F17').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('A14:F17').Font.Color = ConvertTo-OleColor '282828'
    $settings.Range('A14:F17').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Rows('14:17').RowHeight = 28

    $priceList.PageSetup.PrintArea = '$B$3:$O$30'
    $priceList.PageSetup.Orientation = 2
    $priceList.PageSetup.Zoom = $false
    $priceList.PageSetup.FitToPagesWide = 1
    $priceList.PageSetup.FitToPagesTall = 1
    $settings.PageSetup.PrintArea = '$A$1:$F$17'
    $settings.PageSetup.Zoom = $false
    $settings.PageSetup.FitToPagesWide = 1
    $settings.PageSetup.FitToPagesTall = 1
    $priceList.Activate()
    $excel.ActiveWindow.DisplayGridlines = $true
    $excel.ActiveWindow.DisplayRightToLeft = $true
    $excel.ActiveWindow.Zoom = 90

    # Import the auditable checked-in parser/runtime, dashboard module, and open event.
    [void]$workbook.VBProject.VBComponents.Import($jsonValuePath)
    [void]$workbook.VBProject.VBComponents.Import($jsonRuntimePath)
    [void]$workbook.VBProject.VBComponents.Import($vbaModulePath)
    $thisWorkbookComponent = $workbook.VBProject.VBComponents.Item('ThisWorkbook')
    $thisWorkbookCode = Get-Content -Raw -Encoding UTF8 $thisWorkbookPath
    $thisWorkbookComponent.CodeModule.AddFromString($thisWorkbookCode)

    # Run a non-networking macro to force VBA parsing and validate the expected
    # sheet, table, and configuration contracts before packaging.
    try {
        $excel.Run("'$($workbook.Name)'!PatrisDashboard.ValidateWorkbook")
    }
    catch {
        # Preserve the failed package at the requested diagnostic path so the
        # native VBA editor can identify the exact compile location.
        $workbook.SaveAs($OutputPath, 53)
        $workbook.Save()
        throw
    }
    # 53 = xlOpenXMLTemplateMacroEnabled (.xltm). Opening the canonical file
    # creates a separate workbook instance for any later Save As operation.
    $workbook.SaveAs($OutputPath, 53)
    $workbook.Save()

    $priceList.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'price-list.pdf'))
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

    $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $OutputPath
    $checksumText = "$($hash.Hash.ToLowerInvariant())  $([IO.Path]::GetFileName($OutputPath))`n"
    $utf8NoBom = [Text.UTF8Encoding]::new($false)
    [IO.File]::WriteAllText(($OutputPath + '.sha256'), $checksumText, $utf8NoBom)
    Copy-Item -LiteralPath $OutputPath -Destination $DistributionCopyPath -Force
    Copy-Item -LiteralPath ($OutputPath + '.sha256') -Destination ($DistributionCopyPath + '.sha256') -Force
    [pscustomobject]@{
        output = $OutputPath
        distribution_copy = $DistributionCopyPath
        sha256 = $hash.Hash.ToLowerInvariant()
        excel_version = $excelVersion
        vba_components = $vbaComponents
        preview_directory = $PreviewDirectory
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
