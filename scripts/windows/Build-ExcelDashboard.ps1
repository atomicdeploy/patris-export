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
    $Range.Font.Name = 'Aptos'
    $Range.Font.Size = $FontSize
    $Range.Font.Bold = $true
    $Range.VerticalAlignment = -4108
}

function Add-ActionButton($Sheet, [string]$Text, [string]$Macro, $Anchor, [double]$Width = 112, [double]$Height = 30) {
    $shape = $Sheet.Shapes.AddShape(5, $Anchor.Left, $Anchor.Top, $Width, $Height)
    $shape.Fill.ForeColor.RGB = ConvertTo-OleColor '0F766E'
    $shape.Line.ForeColor.RGB = ConvertTo-OleColor '0B5F59'
    $shape.TextFrame2.TextRange.Text = $Text
    $shape.TextFrame2.TextRange.Font.Name = 'Aptos'
    $shape.TextFrame2.TextRange.Font.Size = 10
    $shape.TextFrame2.TextRange.Font.Bold = $true
    $shape.TextFrame2.TextRange.Font.Fill.ForeColor.RGB = ConvertTo-OleColor 'FFFFFF'
    $shape.TextFrame2.VerticalAnchor = 3
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

    $instructions = $workbook.Worksheets.Item(1)
    $instructions.Name = 'Instructions'
    $settings = $workbook.Worksheets.Add()
    $settings.Name = 'Settings'
    $products = $workbook.Worksheets.Add()
    $products.Name = 'Products'
    $dashboard = $workbook.Worksheets.Add()
    $dashboard.Name = 'Dashboard'

    foreach ($sheet in @($dashboard, $products, $settings, $instructions)) {
        $sheet.Cells.Font.Name = 'Aptos'
        $sheet.Cells.Font.Size = 10
        $sheet.Activate()
        $excel.ActiveWindow.DisplayGridlines = $false
    }

    # Products: an intentionally empty runtime table. Opening the .xltm creates
    # a new workbook instance; the checked-in template never contains catalog
    # rows or live configuration values.
    $headers = @(
        'Code', 'Name', 'Part Number', 'Category', 'Warehouse 1', 'Warehouse 2',
        'Total Stock', 'Foreign Price (CNY)', 'Weight (g)', 'Shipping Method',
        'Shipping Price/kg', 'Shipping Currency', 'Profit Margin (%)', 'IRT per CNY',
        'Final Price (IRT)', 'WooCommerce', 'Woo Price (IRT)', 'Woo Stock', 'Sync Status',
        'Manual Price Override (IRT)', 'Review Status', 'Notes', 'Effective Price (IRT)',
        'Source', 'Search Index'
    )
    for ($column = 0; $column -lt $headers.Count; $column++) {
        $products.Cells.Item(1, $column + 1).Value2 = $headers[$column]
    }
    $productTable = $products.ListObjects.Add(1, $products.Range('A1:Y2'), $null, 1)
    $productTable.Name = 'tblProducts'
    $productTable.TableStyle = 'TableStyleMedium2'
    [void]$productTable.DataBodyRange.Delete()
    $widths = @(
        15, 31, 19, 23, 13, 13, 13, 20, 13, 18, 18, 16, 18, 15, 20,
        16, 18, 14, 31, 24, 16, 28, 21, 19, 18
    )
    for ($column = 0; $column -lt $widths.Count; $column++) {
        $products.Columns.Item($column + 1).ColumnWidth = $widths[$column]
    }
    $products.Columns.Item(25).Hidden = $true
    $products.Rows.Item(1).RowHeight = 30
    $products.Range('AA1').Value2 = 'Yellow cells are manual. They survive refresh only when the exact, case-sensitive Code still exists.'
    $products.Range('AA1').WrapText = $true
    $products.Columns.Item(27).ColumnWidth = 30
    $products.Range('AA1').Font.Color = ConvertTo-OleColor '64748B'
    $products.Range('AA1').Interior.Color = ConvertTo-OleColor 'F1F5F9'
    $products.Activate()
    $excel.ActiveWindow.SplitRow = 1
    $excel.ActiveWindow.FreezePanes = $true

    # Dashboard: compact KPI cards, search surface, chart, and logo slot.
    $dashboard.Columns('A:M').ColumnWidth = 12
    $dashboard.Rows('1:30').RowHeight = 22
    $dashboard.Range('A1:I2').Merge()
    $dashboard.Range('A1').Value2 = 'DIGITALOGIC  |  PRODUCT PRICING DASHBOARD'
    Set-SectionStyle $dashboard.Range('A1:I2') '0F172A' 'FFFFFF' 20
    $dashboard.Range('A1').HorizontalAlignment = -4131
    $dashboard.Range('A3:I3').Merge()
    $dashboard.Range('A3').Value2 = 'Patris Export + Digitalogic WordPress  |  فهرست زنده محصولات و قیمت‌گذاری'
    $dashboard.Range('A3').Font.Color = ConvertTo-OleColor '475569'
    $dashboard.Range('A3').Font.Size = 10

    $logoSlot = $dashboard.Shapes.AddShape(5, $dashboard.Range('J1').Left, $dashboard.Range('J1').Top, $dashboard.Range('J1:M3').Width, $dashboard.Range('J1:M3').Height)
    $logoSlot.Name = 'LogoPlaceholder'
    $logoSlot.Fill.ForeColor.RGB = ConvertTo-OleColor 'F8FAFC'
    $logoSlot.Line.ForeColor.RGB = ConvertTo-OleColor 'CBD5E1'
    $logoSlot.Line.DashStyle = 4
    $logoSlot.TextFrame2.TextRange.Text = 'YOUR LOGO'
    $logoSlot.TextFrame2.TextRange.Font.Name = 'Aptos'
    $logoSlot.TextFrame2.TextRange.Font.Size = 11
    $logoSlot.TextFrame2.TextRange.Font.Bold = $true
    $logoSlot.TextFrame2.TextRange.Font.Fill.ForeColor.RGB = ConvertTo-OleColor '64748B'
    $logoSlot.TextFrame2.VerticalAnchor = 3

    $cards = @(
        @{ LabelRange = 'A5:C5'; ValueRange = 'A6:C7'; Label = 'TOTAL PRODUCTS'; Formula = '=COUNTA(tblProducts[Code])'; Format = '#,##0' },
        @{ LabelRange = 'D5:F5'; ValueRange = 'D6:F7'; Label = 'TOTAL STOCK'; Formula = '=SUM(tblProducts[Total Stock])'; Format = '#,##0' },
        @{ LabelRange = 'G5:I5'; ValueRange = 'G6:I7'; Label = 'AVG. EFFECTIVE PRICE'; Formula = '=IF(COUNT(tblProducts[Effective Price (IRT)])=0,"",AVERAGE(tblProducts[Effective Price (IRT)]))'; Format = '#,##0 "IRT"' },
        @{ LabelRange = 'J5:M5'; ValueRange = 'J6:M7'; Label = 'LAST REFRESH'; Formula = '=IF(Settings!B5="","",Settings!B5)'; Format = 'yyyy-mm-dd hh:mm' }
    )
    foreach ($card in $cards) {
        $labelRange = $dashboard.Range($card.LabelRange)
        $valueRange = $dashboard.Range($card.ValueRange)
        $labelRange.Merge()
        $valueRange.Merge()
        $labelRange.Value2 = $card.Label
        $labelRange.Interior.Color = ConvertTo-OleColor 'E2E8F0'
        $labelRange.Font.Color = ConvertTo-OleColor '475569'
        $labelRange.Font.Size = 9
        $labelRange.Font.Bold = $true
        $labelRange.HorizontalAlignment = -4108
        $valueRange.Interior.Color = ConvertTo-OleColor 'F8FAFC'
        $valueRange.Borders.Color = ConvertTo-OleColor 'CBD5E1'
        $valueRange.Borders.Weight = 2
        $valueRange.HorizontalAlignment = -4108
        $valueRange.VerticalAlignment = -4108
        $valueRange.Formula = $card.Formula
        $valueRange.NumberFormat = $card.Format
        $valueRange.Font.Size = 16
        $valueRange.Font.Bold = $true
        $valueRange.Font.Color = ConvertTo-OleColor '0F172A'
    }

    $dashboard.Range('A9:M9').Merge()
    $dashboard.Range('A9').Value2 = 'SEARCH AND ACTIONS'
    Set-SectionStyle $dashboard.Range('A9:M9') 'E2E8F0' '334155' 10
    $dashboard.Range('A10').Value2 = 'Search:'
    $dashboard.Range('B10:F10').Merge()
    $dashboard.Range('B10:F10').Interior.Color = ConvertTo-OleColor 'FFFFFF'
    $dashboard.Range('B10:F10').Borders.Color = ConvertTo-OleColor '94A3B8'
    $dashboard.Range('B11:F11').Merge()
    $dashboard.Range('B11').Font.Color = ConvertTo-OleColor '64748B'
    [void](Add-ActionButton $dashboard 'Search products' 'PatrisDashboard.SearchProducts' $dashboard.Range('G10') 92 28)
    [void](Add-ActionButton $dashboard 'Reset' 'PatrisDashboard.ResetSearch' $dashboard.Range('I10') 54 28)
    [void](Add-ActionButton $dashboard 'Sync now' 'PatrisDashboard.RefreshAllData' $dashboard.Range('J10') 74 28)
    [void](Add-ActionButton $dashboard 'Choose logo' 'PatrisDashboard.ChooseCompanyLogo' $dashboard.Range('L10') 70 28)

    $chartObject = $dashboard.ChartObjects().Add($dashboard.Range('A13').Left, $dashboard.Range('A13').Top, $dashboard.Range('A13:H29').Width, $dashboard.Range('A13:H29').Height)
    $chartObject.Name = 'PriceChart'
    $chartObject.Chart.ChartType = 57
    $priceSeries = $chartObject.Chart.SeriesCollection().NewSeries()
    $priceSeries.Name = "='Products'!`$W`$1"
    $priceSeries.XValues = "='Products'!`$B`$2"
    $priceSeries.Values = "='Products'!`$W`$2"
    $chartObject.Chart.HasTitle = $true
    $chartObject.Chart.ChartTitle.Text = 'Effective price snapshot (first 10 products)'
    $chartObject.Chart.HasLegend = $false
    $chartObject.Chart.Axes(2).TickLabels.NumberFormat = '#,##0'
    $chartObject.Chart.ChartArea.Format.Line.ForeColor.RGB = ConvertTo-OleColor 'CBD5E1'
    $emptyChartMessage = $dashboard.Shapes.AddShape(
        1,
        $chartObject.Left + 2,
        $chartObject.Top + 2,
        $chartObject.Width - 4,
        $chartObject.Height - 4
    )
    $emptyChartMessage.Name = 'EmptyChartMessage'
    $emptyChartMessage.Fill.ForeColor.RGB = ConvertTo-OleColor 'F8FAFC'
    $emptyChartMessage.Line.ForeColor.RGB = ConvertTo-OleColor 'CBD5E1'
    $emptyChartMessage.TextFrame2.TextRange.Text = "No catalog rows are stored in this template.`nUse Sync now to load live prices."
    $emptyChartMessage.TextFrame2.TextRange.Font.Name = 'Aptos'
    $emptyChartMessage.TextFrame2.TextRange.Font.Size = 12
    $emptyChartMessage.TextFrame2.TextRange.Font.Bold = $true
    $emptyChartMessage.TextFrame2.TextRange.Font.Fill.ForeColor.RGB = ConvertTo-OleColor '475569'
    $emptyChartMessage.TextFrame2.TextRange.ParagraphFormat.Alignment = 2
    $emptyChartMessage.TextFrame2.VerticalAnchor = 3

    $dashboard.Range('I13:M13').Merge()
    $dashboard.Range('I13').Value2 = 'HOW TO USE'
    Set-SectionStyle $dashboard.Range('I13:M13') '0F766E' 'FFFFFF' 11
    $dashboard.Range('I14:M25').Merge()
    $dashboard.Range('I14').Value2 = "1. This is an empty macro template.`n`n2. Opening it creates a new workbook and syncs live rows.`n`n3. Sync now joins exact Patris Code only.`n`n4. WooID text links to the matching product page.`n`n5. Missing inputs stay blank with warnings—never #N/A."
    $dashboard.Range('I14').WrapText = $true
    $dashboard.Range('I14').VerticalAlignment = -4160
    $dashboard.Range('I14').Interior.Color = ConvertTo-OleColor 'F0FDFA'
    $dashboard.Range('I14').Font.Color = ConvertTo-OleColor '134E4A'
    $dashboard.Range('I27:M29').Merge()
    $dashboard.Range('I27').Value2 = 'No credentials or catalog rows are stored in the template. Woo read-only credentials come from Windows environment variables.'
    $dashboard.Range('I27').WrapText = $true
    $dashboard.Range('I27').Font.Color = ConvertTo-OleColor '9A3412'
    $dashboard.Range('I27').Interior.Color = ConvertTo-OleColor 'FFF7ED'

    # Settings and endpoint policy.
    $settings.Columns('A').ColumnWidth = 25
    $settings.Columns('B:F').ColumnWidth = 18
    $settings.Range('A1:F1').Merge()
    $settings.Range('A1').Value2 = 'CONNECTION AND CALCULATION SETTINGS'
    Set-SectionStyle $settings.Range('A1:F1') '0F172A' 'FFFFFF' 16
    $settings.Range('A2').Value2 = 'Patris product-sync endpoint'
    $settings.Range('B2:F2').Merge()
    $settings.Range('B2').Value2 = 'http://127.0.0.1:18080/api/product-sync'
    $settings.Range('A3').Value2 = 'Digitalogic catalog endpoint'
    $settings.Range('B3:F3').Merge()
    $settings.Range('B3').Value2 = 'https://digitalogic.ir/wp-json/digitalogic/v1/google-sheets/catalog'
    $settings.Range('A4').Value2 = 'Price output mode'
    $settings.Range('B4').Value2 = 'formula'
    $settings.Range('A5').Value2 = 'Last successful refresh'
    $settings.Range('B5').ClearContents()
    $settings.Range('B5').NumberFormat = 'yyyy-mm-dd hh:mm'
    $settings.Range('A6').Value2 = 'HTTP timeout (seconds)'
    $settings.Range('B6').Value2 = 10
    $settings.Range('A7').Value2 = 'Workbook language'
    $settings.Range('B7').Value2 = 'en'
    $settings.Range('A8').Value2 = 'Digitalogic status'
    $settings.Range('B8:F8').Merge()
    $settings.Range('B8').Value2 = 'Not refreshed yet.'
    $settings.Range('A9').Value2 = 'Read key environment name'
    $settings.Range('B9:F9').Merge()
    $settings.Range('B9').Value2 = 'DIGITALOGIC_CONSUMER_KEY'
    $settings.Range('A10').Value2 = 'Read secret environment name'
    $settings.Range('B10:F10').Merge()
    $settings.Range('B10').Value2 = 'DIGITALOGIC_CONSUMER_SECRET'
    $settings.Range('A11').Value2 = 'Sync automatically on open'
    $settings.Range('B11').Value2 = 'yes'
    $settings.Range('B11').Validation.Delete()
    $settings.Range('B11').Validation.Add(3, 1, 1, 'yes,no')
    $settings.Range('A2:A11').Font.Bold = $true
    $settings.Range('A2:F11').Borders.Color = ConvertTo-OleColor 'CBD5E1'
    $settings.Range('B2:F11').Interior.Color = ConvertTo-OleColor 'F8FAFC'
    $settings.Range('B4').Validation.Delete()
    $settings.Range('B4').Validation.Add(3, 1, 1, 'formula,precalculated')
    $settings.Range('A13:F13').Merge()
    $settings.Range('A13').Value2 = 'LIVE CONFIGURATION — refreshed from the current product contract'
    Set-SectionStyle $settings.Range('A13:F13') '0F766E' 'FFFFFF' 11
    $settings.Range('A16').Value2 = 'بهای یوآن (IRT/CNY)'
    $settings.Range('A17').Value2 = 'نرخ حمل CNY'
    $settings.Range('A18').Value2 = 'درصد سود'
    $settings.Range('B16:F16').Merge()
    $settings.Range('B17:F17').Merge()
    $settings.Range('B18:F18').Merge()
    $settings.Range('A16:A18').Font.Bold = $true
    $settings.Range('A16:F18').Borders.Color = ConvertTo-OleColor 'CBD5E1'
    $settings.Range('B16:F18').Interior.Color = ConvertTo-OleColor 'F0FDFA'
    $settings.Range('A16:F18').ReadingOrder = -5004
    $settings.Range('A20:F25').Merge()
    $settings.Range('A20').Value2 = "Security note`nThe template contains no product rows and no credential values. It reads the two credential environment-variable names above, then sends Basic authentication only to https://digitalogic.ir/. Keep the WooCommerce key Read-only."
    $settings.Range('A20').WrapText = $true
    $settings.Range('A20').VerticalAlignment = -4160
    $settings.Range('A20').Interior.Color = ConvertTo-OleColor 'FEF2F2'
    $settings.Range('A20').Font.Color = ConvertTo-OleColor '991B1B'

    # Instructions and audit sheet.
    $instructions.Columns('A').ColumnWidth = 4
    $instructions.Columns('B').ColumnWidth = 24
    $instructions.Columns('C:H').ColumnWidth = 16
    $instructions.Range('A1:H2').Merge()
    $instructions.Range('A1').Value2 = 'PATRIS / DIGITALOGIC EXCEL DASHBOARD — QUICK START'
    Set-SectionStyle $instructions.Range('A1:H2') '0F172A' 'FFFFFF' 17
    $instructionRows = @(
        @('1', 'Open the template', 'Double-click the .xltm. Excel creates a new macro-enabled workbook instance; the canonical template remains empty.'),
        @('2', 'Enable reviewed macros', 'Trust only this reviewed file. Refresh-on-open calls the local Patris service and the protected Digitalogic catalog.'),
        @('3', 'Keep credentials outside', 'Set the Read-only WooCommerce key and secret in the two Windows environment variables named on Settings.'),
        @('4', 'Sync live data', 'Use Sync now. Patris and WooCommerce rows join only on exact, case-sensitive Patris Code—never SKU or name.'),
        @('5', 'Review warnings', 'Missing or invalid source inputs stay blank and appear in Sync Status; formulas never emit #N/A.'),
        @('6', 'Open Woo pages', 'When a matching WooCommerce page exists, the WooCommerce cell displays WooID <id> as a clickable link.'),
        @('7', 'Save a working copy', 'Use Save As for the refreshed workbook. Do not overwrite the canonical .xltm template.'),
        @('8', 'Manual fields', 'Yellow override/status/notes cells survive a refresh only while the exact Code remains present.')
    )
    $instructionRow = 4
    foreach ($item in $instructionRows) {
        $instructions.Cells.Item($instructionRow, 1).Value2 = $item[0]
        $instructions.Cells.Item($instructionRow, 2).Value2 = $item[1]
        $instructions.Range("C${instructionRow}:H${instructionRow}").Merge()
        $instructions.Cells.Item($instructionRow, 3).Value2 = $item[2]
        $instructions.Range("A${instructionRow}:H${instructionRow}").Borders.Color = ConvertTo-OleColor 'E2E8F0'
        $instructions.Cells.Item($instructionRow, 1).Interior.Color = ConvertTo-OleColor '0F766E'
        $instructions.Cells.Item($instructionRow, 1).Font.Color = ConvertTo-OleColor 'FFFFFF'
        $instructions.Cells.Item($instructionRow, 1).Font.Bold = $true
        $instructions.Cells.Item($instructionRow, 2).Font.Bold = $true
        $instructions.Range("C${instructionRow}:H${instructionRow}").WrapText = $true
        $instructions.Rows.Item($instructionRow).RowHeight = 50
        $instructionRow++
    }
    $instructions.Range('A14:H16').Merge()
    $instructions.Range('A14').Value2 = 'راهنما: ابتدا کد ماکروها و نشانی سرویس‌ها را بررسی کنید؛ سپس فایل را فقط از مسیر مورد اعتماد اجرا کنید.'
    $instructions.Range('A14').HorizontalAlignment = -4152
    $instructions.Range('A14').ReadingOrder = -5004
    $instructions.Range('A14').WrapText = $true
    $instructions.Range('A14').Interior.Color = ConvertTo-OleColor 'F0FDFA'
    $instructions.Range('A14').Font.Name = 'Segoe UI'

    # Print areas are also the visual-QA surfaces emitted as PDFs below.
    $dashboard.PageSetup.PrintArea = '$A$1:$M$30'
    $dashboard.PageSetup.Orientation = 2
    $dashboard.PageSetup.Zoom = $false
    $dashboard.PageSetup.FitToPagesWide = 1
    $dashboard.PageSetup.FitToPagesTall = 1
    $products.PageSetup.PrintArea = '$A$1:$Y$2'
    $products.PageSetup.Orientation = 2
    $products.PageSetup.Zoom = $false
    $products.PageSetup.FitToPagesWide = 1
    $products.PageSetup.FitToPagesTall = 1
    $settings.PageSetup.PrintArea = '$A$1:$F$25'
    $settings.PageSetup.Zoom = $false
    $settings.PageSetup.FitToPagesWide = 1
    $settings.PageSetup.FitToPagesTall = 1
    $instructions.PageSetup.PrintArea = '$A$1:$H$16'
    $instructions.PageSetup.Zoom = $false
    $instructions.PageSetup.FitToPagesWide = 1
    $instructions.PageSetup.FitToPagesTall = 1
    $dashboard.Activate()
    $excel.ActiveWindow.Zoom = 90

    # Import the auditable checked-in parser/runtime, dashboard module, and open event.
    [void]$workbook.VBProject.VBComponents.Import($jsonValuePath)
    [void]$workbook.VBProject.VBComponents.Import($jsonRuntimePath)
    [void]$workbook.VBProject.VBComponents.Import($vbaModulePath)
    $thisWorkbookComponent = $workbook.VBProject.VBComponents.Item('ThisWorkbook')
    $thisWorkbookCode = Get-Content -Raw -Encoding UTF8 $thisWorkbookPath
    $thisWorkbookComponent.CodeModule.AddFromString($thisWorkbookCode)

    # Run a non-networking macro to force VBA parsing and update formulas/chart.
    try {
        $excel.Run("'$($workbook.Name)'!PatrisDashboard.RefreshDashboard")
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

    $dashboard.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'dashboard.pdf'))
    $products.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'products.pdf'))
    $settings.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'settings.pdf'))
    $instructions.ExportAsFixedFormat(0, (Join-Path $PreviewDirectory 'instructions.pdf'))

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
