[CmdletBinding()]
param(
    [ValidateSet('Standard', 'Advanced')]
    [string]$Edition = 'Standard',
    [string]$OutputPath,
    [string]$DistributionCopyPath,
    [string]$PreviewDirectory,
    [string]$ChecksumManifestPath
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$editionFa = if ($Edition -eq 'Advanced') { 'پیشرفته' } else { 'استاندارد' }
$canonicalFileName = "لیست قیمت دیجیتالاجیک - $editionFa.xltm"
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path $repoRoot (Join-Path 'docs\examples' $canonicalFileName)
}
if ([string]::IsNullOrWhiteSpace($DistributionCopyPath)) {
    $DistributionCopyPath = Join-Path $repoRoot (Join-Path 'outputs\patris-excel-15' $canonicalFileName)
}
if ([string]::IsNullOrWhiteSpace($PreviewDirectory)) {
    $PreviewDirectory = Join-Path $repoRoot (Join-Path 'outputs\patris-excel-15\preview' $Edition.ToLowerInvariant())
}
if ([string]::IsNullOrWhiteSpace($ChecksumManifestPath)) {
    $ChecksumManifestPath = Join-Path $repoRoot 'outputs\patris-excel-15\checksums\SHA256SUMS-price-calculators.txt'
}
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
$DistributionCopyPath = [IO.Path]::GetFullPath($DistributionCopyPath)
$PreviewDirectory = [IO.Path]::GetFullPath($PreviewDirectory)
$ChecksumManifestPath = [IO.Path]::GetFullPath($ChecksumManifestPath)
$vbaModulePath = Join-Path $repoRoot 'docs\examples\vba\ProductCatalogSync.bas'
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

    # The canonical workbook intentionally keeps the familiar Persian price-list
    # layout. Product rows are empty in the template and are created only by Sync.
    $priceList = $workbook.Worksheets.Item(1)
    $priceList.Name = 'لیست قیمت'
    $settings = $workbook.Worksheets.Add([Type]::Missing, $priceList)
    $settings.Name = 'تنظیمات'
    $syncData = $workbook.Worksheets.Add([Type]::Missing, $settings)
    $syncData.Name = 'داده‌های همگام‌سازی'

    foreach ($sheet in @($priceList, $settings, $syncData)) {
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
    $priceList.Range('B3').Value2 = "لیست کالاها - $editionFa"
    $priceList.Range('B3').Font.Size = 16
    $priceList.Range('B3').Font.Bold = $true
    $priceList.Range('B3').Font.Color = ConvertTo-OleColor '242424'

    $headers = if ($Edition -eq 'Advanced') {
        @(
            'قیمت نهایی محاسبه‌شده (تومان)',
            'وزن کالا (گرم)',
            'وزن و محل کالا',
            'فی فروش منبع',
            'قیمت ارزی',
            'موجودی کل انبارها',
            'کد کالا',
            'نام کالا',
            'شناسه و لینک ووکامرس',
            'قیمت قابل‌مشاهده مشتری (تومان)',
            'اختلاف با قیمت مشتری',
            'وضعیت همگام‌سازی قیمت',
            'ارز کالا',
            'درصد سود',
            'نرخ حمل هر کیلو',
            'تاریخ نرخ ارز'
        )
    }
    else {
        @(
            'فی فروش',
            'گرم',
            'سایر',
            'فی فروش2',
            'نرخ ارزی',
            'همه انبارها',
            'کد کالا',
            'نام کالا'
        )
    }
    for ($column = 0; $column -lt $headers.Count; $column++) {
        $priceList.Cells.Item(5, $column + 2).Value2 = $headers[$column]
    }
    $productLastColumn = if ($Edition -eq 'Advanced') { 'Q' } else { 'I' }
    $productTable = $priceList.ListObjects.Add(1, $priceList.Range("B5:${productLastColumn}6"), $null, 1)
    $productTable.Name = 'Products'
    $productTable.TableStyle = 'TableStyleMedium2'
    [void]$productTable.DataBodyRange.Delete()
    $priceList.Range("B5:${productLastColumn}5").Interior.Color = ConvertTo-OleColor '1C61E7'
    $priceList.Range("B5:${productLastColumn}5").Font.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range("B5:${productLastColumn}5").Font.Bold = $true
    $priceList.Range("B5:${productLastColumn}5").HorizontalAlignment = -4108
    $priceList.Range("B5:${productLastColumn}5").VerticalAlignment = -4108
    $priceList.Range("B5:${productLastColumn}5").Borders.Color = ConvertTo-OleColor '174FC0'
    $priceList.Range("B5:${productLastColumn}5").Borders.Weight = 2
    $priceList.Rows.Item(5).RowHeight = 31

    $priceList.Columns('B').ColumnWidth = 18
    $priceList.Columns('C').ColumnWidth = 13.57
    $priceList.Columns('D').ColumnWidth = 19.14
    $priceList.Columns('E').ColumnWidth = 17.43
    $priceList.Columns('F').ColumnWidth = 14.57
    $priceList.Columns('G').ColumnWidth = 17.71
    $priceList.Columns('H').ColumnWidth = 17.43
    $priceList.Columns('I').ColumnWidth = 59.86
    if ($Edition -eq 'Advanced') {
        $priceList.Columns('J').ColumnWidth = 20
        $priceList.Columns('K').ColumnWidth = 23
        $priceList.Columns('L').ColumnWidth = 18
        $priceList.Columns('M').ColumnWidth = 28
        $priceList.Columns('N').ColumnWidth = 11
        $priceList.Columns('O').ColumnWidth = 12
        $priceList.Columns('P').ColumnWidth = 15
        $priceList.Columns('Q').ColumnWidth = 14
        $configFirstColumn = 'T'
        $configSecondColumn = 'V'
        $configLastColumn = 'V'
    }
    else {
        $priceList.Columns('J').ColumnWidth = 2.29
        $priceList.Columns('K').ColumnWidth = 1.71
        $priceList.Columns('L').ColumnWidth = 2.43
        $priceList.Columns('M').ColumnWidth = 12.71
        $priceList.Columns('N').ColumnWidth = 2.86
        $priceList.Columns('O').ColumnWidth = 12.71
        $configFirstColumn = 'M'
        $configSecondColumn = 'O'
        $configLastColumn = 'O'
    }
    $priceList.Columns('B').NumberFormat = '#,##0'
    $priceList.Columns('C').NumberFormat = '#,##0.##'
    $priceList.Columns('E').NumberFormat = '#,##0'
    $priceList.Columns('F').NumberFormat = '#,##0.####'
    $priceList.Columns('G').NumberFormat = '#,##0.##'
    if ($Edition -eq 'Advanced') {
        $priceList.Columns('J').NumberFormat = '@'
        $priceList.Columns('J').HorizontalAlignment = -4131
        $priceList.Columns('J').ReadingOrder = -5003
        $priceList.Columns('K').NumberFormat = '#,##0'
        $priceList.Columns('L').NumberFormat = '0.0%'
        $priceList.Columns('O').NumberFormat = '0.00"%"'
        $priceList.Columns('P').NumberFormat = '0.######'
        $priceList.Columns('Q').NumberFormat = 'yyyy/mm/dd'
        # Technical pricing inputs remain in the table for auditability and
        # formulas, but stay collapsed by default. The user can unhide them.
        $priceList.Columns('N:Q').Hidden = $true
    }
    $priceList.Columns('B').Font.Bold = $true
    $priceList.Columns('B:H').HorizontalAlignment = -4152
    $priceList.Columns('I').HorizontalAlignment = -4131

    # Keep the three familiar configuration cards, but leave their values empty
    # in the template. Sync fills them from the living Digitalogic state.
    $yuanHeader = $priceList.Range("${configFirstColumn}6")
    $yuanValue = $priceList.Range("${configFirstColumn}7")
    $shippingHeader = $priceList.Range("${configSecondColumn}6")
    $shippingValue = $priceList.Range("${configSecondColumn}7")
    $profitHeader = $priceList.Range("${configSecondColumn}9")
    $profitValue = $priceList.Range("${configSecondColumn}10")

    $yuanHeader.Value2 = 'بهای یوآن'
    $yuanValue.Formula = "=IF('تنظیمات'!B10="""","""",'تنظیمات'!B10)"
    $yuanTable = $priceList.ListObjects.Add(1, $priceList.Range("${configFirstColumn}6:${configFirstColumn}7"), $null, 1)
    $yuanTable.Name = 'Yuan_Price'
    $yuanTable.TableStyle = 'TableStyleMedium2'

    $shippingHeader.Value2 = 'نرخ حمل CNY'
    $shippingValue.Formula = "=IF('تنظیمات'!B22="""","""",'تنظیمات'!B22)"
    $shippingTable = $priceList.ListObjects.Add(1, $priceList.Range("${configSecondColumn}6:${configSecondColumn}7"), $null, 1)
    $shippingTable.Name = 'Shipping'
    $shippingTable.TableStyle = 'TableStyleMedium2'

    $profitHeader.Value2 = 'درصد سود'
    $profitValue.Formula = "=IF('تنظیمات'!B13="""","""",'تنظیمات'!B13)"
    $profitTable = $priceList.ListObjects.Add(1, $priceList.Range("${configSecondColumn}9:${configSecondColumn}10"), $null, 1)
    $profitTable.Name = 'Profit'
    $profitTable.TableStyle = 'TableStyleMedium2'

    foreach ($configHeader in @($yuanHeader, $shippingHeader, $profitHeader)) {
        $configHeader.Interior.Color = ConvertTo-OleColor '1C61E7'
        $configHeader.Font.Color = ConvertTo-OleColor 'FFFFFF'
        $configHeader.Font.Bold = $true
        $configHeader.HorizontalAlignment = -4108
    }
    foreach ($configValue in @($yuanValue, $shippingValue, $profitValue)) {
        $configValue.Interior.Color = ConvertTo-OleColor 'DDE8FC'
        $configValue.Font.Color = ConvertTo-OleColor '242424'
        $configValue.Font.Bold = $true
        $configValue.HorizontalAlignment = -4108
        $configValue.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    }
    $yuanValue.NumberFormat = '#,##0'
    $shippingValue.NumberFormat = '#,##0.##'
    $profitValue.NumberFormat = '0%'

    $buttonRange = $priceList.Range("${configFirstColumn}3:${configLastColumn}4")
    [void](Add-ActionButton $priceList 'همگام‌سازی اکنون' 'ProductCatalogSync.RefreshAllData' $buttonRange.Cells.Item(1, 1) $buttonRange.Width $buttonRange.Height)
    $statusHeaderRange = $priceList.Range("${configFirstColumn}12:${configLastColumn}12")
    $statusBodyRange = $priceList.Range("${configFirstColumn}13:${configLastColumn}14")
    $updatedHeaderRange = $priceList.Range("${configFirstColumn}16:${configLastColumn}16")
    $updatedBodyRange = $priceList.Range("${configFirstColumn}17:${configLastColumn}17")
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
    $updatedHeaderRange.Merge()
    $updatedHeaderRange.Cells.Item(1, 1).Value2 = 'آخرین به‌روزرسانی'
    Set-SectionStyle $updatedHeaderRange 'F6F6F6' '242424' 10
    $updatedBodyRange.Merge()
    $updatedBodyRange.Cells.Item(1, 1).Formula = "=IF('تنظیمات'!B7="""","""",'تنظیمات'!B7)"
    $updatedBodyRange.NumberFormat = 'yyyy/mm/dd hh:mm'
    $updatedBodyRange.HorizontalAlignment = -4108
    $updatedBodyRange.Interior.Color = ConvertTo-OleColor 'DDE8FC'
    $updatedBodyRange.Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $priceList.Range("B5:${productLastColumn}1000").Borders.Color = ConvertTo-OleColor 'D9D9D9'

    $excel.ActiveWindow.SplitRow = 5
    $excel.ActiveWindow.FreezePanes = $true
    $excel.ActiveWindow.Zoom = 90

    # Settings stays because it is useful, but every visible label is Persian.
    $settings.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
    $settings.Columns('A').ColumnWidth = 28
    $settings.Columns('B:F').ColumnWidth = 18
    $settings.Range('A1:F2').Merge()
    $settings.Range('A1').Value2 = "تنظیمات همگام‌سازی و محاسبه قیمت - $editionFa"
    Set-SectionStyle $settings.Range('A1:F2') '1C61E7' 'FFFFFF' 17
    $settings.Range('A1:F2').HorizontalAlignment = -4108

    $settings.Range('A3').Value2 = 'نشانی سرویس محصولات'
    $settings.Range('B3:F3').Merge()
    $settings.Range('B3').Value2 = 'http://127.0.0.1:18080/api/product-sync'
    $settings.Range('A4').Value2 = 'نشانی پل امن قیمت‌گذاری'
    $settings.Range('B4:F4').Merge()
    $settings.Range('B4').Value2 = 'http://127.0.0.1:18080/api/excel/pricing-sync/state'
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
    $settings.Range('A8').Value2 = 'نسخه'
    $settings.Range('B8').Value2 = $editionFa
    $settings.Range('G8').Value2 = $Edition.ToLowerInvariant()
    $settings.Rows.Item(8).Hidden = $true
    $settings.Range('A3:A8').Font.Bold = $true
    $settings.Range('A3:F8').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Range('B3:F8').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B3:F4').ReadingOrder = -5003

    $settings.Range('A9:F9').Merge()
    $settings.Range('A9').Value2 = 'مقادیر زنده سایت'
    Set-SectionStyle $settings.Range('A9:F9') 'DDE8FC' '242424' 12
    $liveLabels = @('بهای یوآن سایت', 'بهای دلار سایت', 'تاریخ مؤثر نرخ‌ها', 'سود پیش‌فرض سایت', 'بازبینی وضعیت سایت', 'زمان دریافت وضعیت')
    for ($rowOffset = 0; $rowOffset -lt $liveLabels.Count; $rowOffset++) {
        $row = 10 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $liveLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    $settings.Range('B10:B11').NumberFormat = '#,##0'
    $settings.Range('B13').NumberFormat = '0%'
    $settings.Range('A10:A15').Font.Bold = $true
    $settings.Range('A10:F15').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B10:F15').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B10:F15').HorizontalAlignment = -4108

    $settings.Range('A17:F17').Merge()
    $settings.Range('A17').Value2 = 'مقادیر پیشنهادی این فایل'
    Set-SectionStyle $settings.Range('A17:F17') 'DDE8FC' '242424' 12
    $proposalLabels = @('بهای یوآن', 'بهای دلار', 'تاریخ مؤثر', 'درصد سود', 'نرخ حمل CNY', 'وضعیت پیش‌نمایش')
    for ($rowOffset = 0; $rowOffset -lt $proposalLabels.Count; $rowOffset++) {
        $row = 18 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $proposalLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    $settings.Range('B18:B19').NumberFormat = '#,##0'
    $settings.Range('B21').NumberFormat = '0%'
    $settings.Range('B22').NumberFormat = '#,##0.##'
    $settings.Range('A18:A23').Font.Bold = $true
    $settings.Range('A18:F23').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B18:F23').Interior.Color = ConvertTo-OleColor 'FFF8E7'
    $settings.Range('B18:F23').HorizontalAlignment = -4108

    $settings.Range('A24').Value2 = 'حد هشدار اختلاف قیمت'
    $settings.Range('B24').Value2 = 0.07
    $settings.Range('B24').NumberFormat = '0%'
    $settings.Range('A25').Value2 = 'حد هشدار قدمت نرخ (روز)'
    $settings.Range('B25').Value2 = 7
    $settings.Range('A24:A25').Font.Bold = $true
    $settings.Range('A24:F25').Borders.Color = ConvertTo-OleColor 'D9D9D9'
    $settings.Range('B24:F25').Interior.Color = ConvertTo-OleColor 'F6F6F6'

    [void](Add-ActionButton $settings 'پیش‌نمایش تغییرات' 'ProductCatalogSync.PreviewPricingChanges' $settings.Range('A27') $settings.Range('A27:C28').Width $settings.Range('A27:C28').Height)
    [void](Add-ActionButton $settings 'اعمال تغییرات تأییدشده' 'ProductCatalogSync.ApplyPricingChanges' $settings.Range('D27') $settings.Range('D27:F28').Width $settings.Range('D27:F28').Height)

    # Hidden base values and preview metadata are runtime-only conflict guards.
    $settings.Range('G18:G22').ClearContents()
    $settings.Range('G26:G28').ClearContents()
    $settings.Columns('G').Hidden = $true

    # Technical join data is never user-facing and is empty in the template.
    $syncHeaders = @(
        'کد کالا',
        'ارز کالا',
        'نرخ حمل هر کیلو',
        'ارز حمل',
        'درصد سود',
        'بهای یوآن',
        'بهای دلار',
        'تاریخ نرخ',
        'شناسه ووکامرس',
        'قیمت مشتری ووکامرس',
        'آخرین تغییر ووکامرس',
        'بازبینی رکورد',
        'نشانی محصول',
        'سود مرجع',
        'قیمت نهایی مرجع',
        'قیمت فروش ویژه'
    )
    for ($column = 0; $column -lt $syncHeaders.Count; $column++) {
        $syncData.Cells.Item(1, $column + 1).Value2 = $syncHeaders[$column]
    }
    $syncTable = $syncData.ListObjects.Add(1, $syncData.Range('A1:P2'), $null, 1)
    $syncTable.Name = 'SyncData'
    $syncTable.TableStyle = 'TableStyleMedium2'
    [void]$syncTable.DataBodyRange.Delete()
    $syncData.Visible = 2

    $priceList.PageSetup.PrintArea = if ($Edition -eq 'Advanced') { '$B$3:$V$30' } else { '$B$3:$O$30' }
    $priceList.PageSetup.Orientation = 2
    $priceList.PageSetup.Zoom = $false
    $priceList.PageSetup.FitToPagesWide = 1
    $priceList.PageSetup.FitToPagesTall = 1
    $settings.PageSetup.PrintArea = '$A$1:$F$28'
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
        $excel.Run("'$($workbook.Name)'!ProductCatalogSync.ValidateWorkbook")
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
    $utf8NoBom = [Text.UTF8Encoding]::new($false)
    Copy-Item -LiteralPath $OutputPath -Destination $DistributionCopyPath -Force
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
        edition = $Edition
        output = $OutputPath
        distribution_copy = $DistributionCopyPath
        sha256 = $hash.Hash.ToLowerInvariant()
        checksum_manifest = $ChecksumManifestPath
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
