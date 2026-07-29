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
    $Range.Font.Name = 'Yekan Bakh'
    $Range.Font.Size = $FontSize
    $Range.Font.Bold = $true
    $Range.VerticalAlignment = -4108
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
    try { $TextRange.Font.NameComplexScript = 'Yekan Bakh' } catch {}
    try { $TextRange.Font.NameFarEast = 'Yekan Bakh' } catch {}
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
    try { $shape.TextFrame.Characters().Font.Name = 'Yekan Bakh' } catch {}
    $shape.TextFrame2.VerticalAnchor = 3
    $shape.TextFrame2.TextRange.ParagraphFormat.Alignment = 2
    $shape.OnAction = $Macro
    return $shape
}

function Add-BrandLogo($Sheet, [string]$Path, $Anchor, [double]$Width = 208, [double]$Height = 48) {
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
    $priceList.Range('B1:K2').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range('E1:K2').Merge()
    $priceList.Range('E1').Value2 = 'لیست قیمت دیجیتالاجیک'
    $priceList.Range('E1').Font.Name = 'Yekan Bakh'
    $priceList.Range('E1').Font.Size = 20
    $priceList.Range('E1').Font.Bold = $true
    $priceList.Range('E1').Font.Color = ConvertTo-OleColor '2F414B'
    $priceList.Range('E1').HorizontalAlignment = -4152
    $priceList.Range('E1').VerticalAlignment = -4108
    [void](Add-BrandLogo $priceList $LogoPath $priceList.Range('B1') 190 43)

    $priceList.Rows(3).RowHeight = 32
    $priceList.Range('B3').Value2 = 'جست‌وجوی کالا (F2)'
    $priceList.Range('B3').Font.Bold = $true
    $priceList.Range('B3').Font.Color = ConvertTo-OleColor '0168CD'
    $priceList.Range('B3').VerticalAlignment = -4108
    $priceList.Range('C3:E3').Merge()
    $priceList.Range('C3').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $priceList.Range('C3:E3').Borders.Color = ConvertTo-OleColor '0168CD'
    $priceList.Range('C3:E3').Borders.Weight = 2
    $priceList.Range('C3').Font.Name = 'Yekan Bakh'
    $priceList.Range('C3').Font.Size = 12
    $priceList.Range('C3').Font.Bold = $true
    $priceList.Range('C3').Font.Color = ConvertTo-OleColor '2F414B'
    $priceList.Range('C3').HorizontalAlignment = -4152
    $priceList.Range('C3').VerticalAlignment = -4108
    $priceList.Range('C3').ReadingOrder = -5004
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
    [void](Add-ActionButton $priceList 'همگام‌سازی اکنون' 'ProductCatalogSync.RefreshAllData' $priceList.Range('I3') $priceList.Range('I3:K3').Width 27)

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
    $priceList.Columns('K').ColumnWidth = 30
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
    $priceList.Columns('H').Font.Name = 'Yekan Bakh'
    $priceList.Columns('J').Font.Name = 'Yekan Bakh'
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

    $yuanHeader.Value2 = 'بهای یوآن'
    $yuanValue.Formula = "=IF('تنظیمات'!B10="""","""",'تنظیمات'!B10)"
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
        'نوع ردیف'
    )
    for ($column = 0; $column -lt $syncHeaders.Count; $column++) {
        $syncData.Cells.Item(1, $column + 1).Value2 = $syncHeaders[$column]
    }
    $syncTable = $syncData.ListObjects.Add(1, $syncData.Range('A1:T2'), $null, 1)
    $syncTable.Name = 'SyncData'
    $syncTable.TableStyle = 'TableStyleMedium2'
    [void]$syncTable.DataBodyRange.Delete()
    $syncData.Visible = 2

    # Dashboard: compact, formula-backed operating view of the live table.
    $dashboard.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
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
    [void](Add-BrandLogo $dashboard $LogoPath $dashboard.Range('B1') 190 43)

    $dashboardCards = @(
        @{ Header = 'B4:C4'; Value = 'B5:C6'; Label = 'تعداد کالاها'; Formula = '=COUNTA(Products[نام کالا])'; Format = '#,##0' },
        @{ Header = 'D4:E4'; Value = 'D5:E6'; Label = 'منتشرشده در سایت'; Formula = '=COUNTIF(SyncData[وضعیت انتشار],"publish")'; Format = '#,##0' },
        @{ Header = 'F4:G4'; Value = 'F5:G6'; Label = 'پیش‌نویس سایت'; Formula = '=COUNTIF(SyncData[وضعیت انتشار],"draft")'; Format = '#,##0' },
        @{ Header = 'H4:I4'; Value = 'H5:I6'; Label = 'بدون صفحه ووکامرس'; Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[شناسه ووکامرس],"")'; Format = '#,##0' },
        @{ Header = 'B8:C8'; Value = 'B9:C10'; Label = 'موجودی کل'; Formula = '=IFERROR(SUM(Products[موجودی کل]),0)'; Format = 'General' },
        @{ Header = 'D8:E8'; Value = 'D9:E10'; Label = 'کالاهای موجود'; Formula = '=COUNTIF(Products[موجودی کل],">0")'; Format = '#,##0' },
        @{ Header = 'F8:G8'; Value = 'F9:G10'; Label = 'کالاهای ناموجود'; Formula = '=COUNTIFS(Products[نام کالا],"<>",Products[موجودی کل],0)'; Format = '#,##0' },
        @{ Header = 'H8:I8'; Value = 'H9:I10'; Label = 'دسته‌بندی‌های فعال'; Formula = '=IFERROR(SUMPRODUCT((Products[دسته‌بندی]<>"")/COUNTIF(Products[دسته‌بندی],Products[دسته‌بندی]&"")),0)'; Format = '#,##0' },
        @{ Header = 'B12:E12'; Value = 'B13:E14'; Label = 'ارزش فروش موجودی (تومان)'; Formula = '=IFERROR(SUMPRODUCT(Products[قیمت فروش (تومان)],Products[موجودی کل]),0)'; Format = '#,##0' },
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
    try {
        Set-OfficeTextFont $chart.ChartTitle.Format.TextFrame2.TextRange 14 $true '2F414B'
    } catch {}
    try { $chart.Legend.Font.Name = 'Yekan Bakh' } catch {}
    try {
        Set-OfficeTextFont $chart.Legend.Format.TextFrame2.TextRange 10 $false '2F414B'
    } catch {}
    $excel.ActiveWindow.Zoom = 90

    # Settings stays because it is useful, but every visible label is Persian.
    $settings.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
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
    [void](Add-BrandLogo $settings $LogoPath $settings.Range('A1') 160 36)

    $settings.Range('A3').Value2 = 'نشانی سرویس محصولات'
    $settings.Range('B3:F3').Merge()
    $settings.Range('B3').Value2 = 'http://127.0.0.1:18080/api/product-sync'
    $settings.Range('A4').Value2 = 'نشانی پل امن قیمت‌گذاری'
    $settings.Range('B4:F4').Merge()
    $settings.Range('B4').Value2 = 'http://127.0.0.1:18080/api/excel/pricing-sync/state'
    $settings.Range('A5').Value2 = 'همگام‌سازی خودکار هنگام بازشدن'
    $settings.Range('B5:F5').Merge()
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
    $liveLabels = @('بهای یوآن سایت', 'بهای دلار سایت', 'تاریخ مؤثر یوآن', 'حاشیه سود سایت', 'نرخ حمل هوایی سایت (یوآن/کیلوگرم)', 'تعداد رقم گردکردن قیمت')
    for ($rowOffset = 0; $rowOffset -lt $liveLabels.Count; $rowOffset++) {
        $row = 10 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $liveLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    foreach ($row in @(10, 11)) {
        $settings.Range("B${row}:F${row}").NumberFormat = '#,##0'
    }
    $settings.Range('B13').NumberFormat = '0%'
    $settings.Range('B14').NumberFormat = 'General'
    $settings.Range('B15').NumberFormat = '0'
    $settings.Range('A10:A15').Font.Bold = $true
    $settings.Range('A10:F15').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B10:F15').Interior.Color = ConvertTo-OleColor 'F6F6F6'
    $settings.Range('B10:F15').HorizontalAlignment = -4108

    $settings.Range('A17:F17').Merge()
    $settings.Range('A17').Value2 = 'مقادیر پیشنهادی این فایل'
    Set-SectionStyle $settings.Range('A17:F17') 'DDE8FC' '242424' 12
    $proposalLabels = @('بهای یوآن', 'بهای دلار', 'تاریخ مؤثر یوآن', 'حاشیه سود', 'نرخ حمل هوایی (یوآن/کیلوگرم)', 'وضعیت پیش‌نمایش')
    for ($rowOffset = 0; $rowOffset -lt $proposalLabels.Count; $rowOffset++) {
        $row = 18 + $rowOffset
        $settings.Cells.Item($row, 1).Value2 = $proposalLabels[$rowOffset]
        $settings.Range("B${row}:F${row}").Merge()
    }
    foreach ($row in @(18, 19)) {
        $settings.Range("B${row}:F${row}").NumberFormat = '#,##0'
    }
    $settings.Range('B21').NumberFormat = '0%'
    $settings.Range('B22').NumberFormat = 'General'
    $settings.Range('A18:A23').Font.Bold = $true
    $settings.Range('A18:F23').Borders.Color = ConvertTo-OleColor 'B9CCF4'
    $settings.Range('B18:F23').Interior.Color = ConvertTo-OleColor 'FFF8E7'
    $settings.Range('B18:F23').HorizontalAlignment = -4108
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
    $settings.Range('A3:A26').WrapText = $true
    $settings.Rows.Item(14).RowHeight = 30
    $settings.Rows.Item(22).RowHeight = 30

    [void](Add-ActionButton $settings 'پیش‌نمایش تغییرات' 'ProductCatalogSync.PreviewPricingChanges' $settings.Range('A28') $settings.Range('A28:C29').Width $settings.Range('A28:C29').Height)
    [void](Add-ActionButton $settings 'اعمال تغییرات تأییدشده' 'ProductCatalogSync.ApplyPricingChanges' $settings.Range('D28') $settings.Range('D28:F29').Width $settings.Range('D28:F29').Height)

    # Hidden base values, state/shipping revisions, and preview metadata are
    # runtime-only conflict guards. The template itself persists no live values.
    $settings.Range('G18:G22').ClearContents()
    $settings.Range('G14:G15').ClearContents()
    $settings.Range('G26:G28').ClearContents()
    $settings.Range('H14:H17').ClearContents()
    $settings.Range('G30:G47').ClearContents()
    $settings.Range('G30').Value2 = 0
    [void]$workbook.Names.Add('SelectedProductRow', $settings.Range('G30'))
    $settings.Columns('G').Hidden = $true
    $settings.Columns('H').Hidden = $true

    # Never format only the first column of several adjacent merged rows.
    # Excel silently coalesces those rows into one large MergeArea, which
    # makes every setting except the first one inaccessible to VBA.
    foreach ($row in @((10..15) + (18..23) + 26)) {
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

    $priceList.PageSetup.PrintArea = '$B$1:$O$30'
    $priceList.PageSetup.Orientation = 2
    $priceList.PageSetup.Zoom = $false
    $priceList.PageSetup.FitToPagesWide = 1
    $priceList.PageSetup.FitToPagesTall = 1
    $settings.PageSetup.PrintArea = '$A$1:$F$29'
    $settings.PageSetup.Zoom = $false
    $settings.PageSetup.FitToPagesWide = 1
    $settings.PageSetup.FitToPagesTall = 1
    $dashboard.PageSetup.PrintArea = '$B$1:$I$32'
    $dashboard.PageSetup.Zoom = $false
    $dashboard.PageSetup.FitToPagesWide = 1
    $dashboard.PageSetup.FitToPagesTall = 1
    $priceList.Activate()
    $excel.ActiveWindow.DisplayGridlines = $false
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
