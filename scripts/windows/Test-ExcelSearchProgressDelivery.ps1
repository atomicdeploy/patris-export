[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CandidatePath,
    [int]$SearchCycles = 100
)

$ErrorActionPreference = 'Stop'
$CandidatePath = [IO.Path]::GetFullPath($CandidatePath)
if (-not (Test-Path -LiteralPath $CandidatePath -PathType Leaf)) {
    throw "Candidate workbook does not exist: $CandidatePath"
}
if ($SearchCycles -lt 3 -or $SearchCycles -gt 500) {
    throw 'SearchCycles must be between 3 and 500.'
}

function Release-ComObject($Value) {
    if ($null -ne $Value -and [Runtime.InteropServices.Marshal]::IsComObject($Value)) {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($Value)
    }
}

function Set-SearchQuery($Excel, $QueryRange, [string]$Value) {
    $previousEvents = [bool]$Excel.EnableEvents
    try {
        $Excel.EnableEvents = $false
        $QueryRange.NumberFormat = '@'
        $QueryRange.Value2 = $Value
    }
    finally {
        $Excel.EnableEvents = $previousEvents
    }
}

function Excel-CrashEvents([datetime]$StartTime) {
    return @(
        Get-WinEvent -FilterHashtable @{
            LogName = 'Application'
            Id = 1000, 1001
            StartTime = $StartTime
        } -ErrorAction SilentlyContinue |
        Where-Object { $_.Message -match '(?i)EXCEL\.EXE' }
    ).Count
}

function Excel-CrashDumps([datetime]$StartTime) {
    $dumpRoot = Join-Path $env:LOCALAPPDATA 'CrashDumps'
    if (-not (Test-Path -LiteralPath $dumpRoot -PathType Container)) {
        return 0
    }
    return @(
        Get-ChildItem -LiteralPath $dumpRoot -Filter 'EXCEL.EXE*.dmp' -File -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -ge $StartTime }
    ).Count
}

$startedAt = Get-Date
$excel = $null
$book = $null
$table = $null
$queryRange = $null
$searchButton = $null
$sheet = $null
$filterApplied = $false
$result = $null

try {
    Write-Host 'stage=create_excel'
    $excel = New-Object -ComObject Excel.Application
    $excel.Visible = $false
    $excel.DisplayAlerts = $false
    $excel.EnableEvents = $true
    $excel.AutomationSecurity = 1
    Write-Host 'stage=open_workbook'
    $book = $excel.Workbooks.Open($CandidatePath, 0, $false)
    Write-Host 'stage=workbook_opened'
    $bookName = ([string]$book.Name).Replace("'", "''")
    $sheet = $book.Worksheets.Item(1)
    $table = $sheet.ListObjects.Item('Products')
    $queryRange = $book.Names.Item('ProductSearchQuery').RefersToRange
    $searchButton = $sheet.Shapes.Item('ProductSearchButton')
    $searchMacro = "'$bookName'!ProductCatalogSync.SearchProducts"
    $clearMacro = "'$bookName'!ProductCatalogSync.ClearProductSearch"
    $progressMacro = "'$bookName'!ProductCatalogSync.ValidateOperationProgressUIForValidation"
    $progressInitializeMacro = "'$bookName'!ProductCatalogSync.InitializeOperationProgress"
    $writebackMacro = "'$bookName'!ProductCatalogSync.ValidatePricingWritebackUIForValidation"

    $initialRows = [int]$table.ListRows.Count
    if ($initialRows -eq 0) {
        Write-Host 'stage=populate_live_data'
        [void]$excel.Run("'$bookName'!ProductCatalogSync.RefreshAllDataForValidation")
        $syncDeadline = [DateTime]::UtcNow.AddMinutes(5)
        $idle = $false
        while (-not $idle -and [DateTime]::UtcNow -lt $syncDeadline) {
            try {
                $idle = [bool]$excel.Run("'$bookName'!ProductCatalogSync.AsyncPricingIdleForValidation")
            }
            catch {
                $idle = $false
            }
            if (-not $idle) {
                Start-Sleep -Milliseconds 100
            }
        }
        if (-not $idle) {
            try { [void]$excel.Run("'$bookName'!ProductCatalogSync.CancelActivePricingOperations") } catch {}
            throw 'Live population did not finish within five minutes.'
        }
        if (-not [bool]$excel.Run("'$bookName'!ProductCatalogSync.LastPricingOperationSucceededForValidation")) {
            $syncError = [string]$excel.Run("'$bookName'!ProductCatalogSync.LastPricingOperationErrorForValidation")
            $syncDiagnostic = [string]$excel.Run("'$bookName'!ProductCatalogSync.LastPricingOperationDiagnosticForValidation")
            throw "Live population failed: $syncError; diagnostic=$syncDiagnostic"
        }
        $initialRows = [int]$table.ListRows.Count
    }
    $initialAddress = [string]$table.Range.Address($false, $false)
    if ($initialRows -lt 1 -or $initialRows -gt 2000) {
        throw "Expected a populated bounded table (1..2000 rows); found $initialRows."
    }
    Write-Host 'stage=initialize_progress'
    [void]$excel.Run($progressInitializeMacro)
    Write-Host 'stage=validate_progress'
    if (-not [bool]$excel.Run($progressMacro)) {
        throw 'Visible progress lifecycle validation returned false.'
    }
    Write-Host 'stage=validate_writeback_ui'
    if (-not [bool]$excel.Run($writebackMacro)) {
        throw 'Writeback color/comment lifecycle validation returned false.'
    }

    Write-Host 'stage=repeated_search'
    Set-SearchQuery $excel $queryRange '109032'
    for ($index = 1; $index -le $SearchCycles; $index++) {
        [void]$excel.Run($searchMacro)
        $caption = [string]$searchButton.TextFrame2.TextRange.Text
        if ($caption -notmatch '\(1/1\)$') {
            throw "Cycle $index returned unexpected caption: $caption"
        }
        if (-not [bool]$excel.Ready) {
            throw "Excel was not ready after search cycle $index."
        }
    }
    [void]$excel.Run($clearMacro)

    Write-Host 'stage=no_match_search'
    Set-SearchQuery $excel $queryRange '__DIGITALOGIC_NO_MATCH__'
    [void]$excel.Run($searchMacro)
    $noMatchCaption = [string]$searchButton.TextFrame2.TextRange.Text
    if ($noMatchCaption -notmatch '\(0\)$') {
        throw "No-match search returned unexpected caption: $noMatchCaption"
    }
    [void]$excel.Run($clearMacro)

    Write-Host 'stage=wildcard_search'
    Set-SearchQuery $excel $queryRange '*?~'
    [void]$excel.Run($searchMacro)
    $wildcardCaption = [string]$searchButton.TextFrame2.TextRange.Text
    [void]$excel.Run($clearMacro)

    Write-Host 'stage=filter_preservation'
    [void]$table.Range.AutoFilter(7, '109032')
    $filterApplied = $true
    if (-not [bool]$table.AutoFilter.FilterMode) {
        throw 'Filter fixture could not be applied.'
    }
    Set-SearchQuery $excel $queryRange '109032'
    [void]$excel.Run($searchMacro)
    [void]$excel.Run($clearMacro)
    if (-not [bool]$table.AutoFilter.FilterMode) {
        throw 'Clearing search removed the user filter.'
    }
    if ([int]$table.ListRows.Count -ne $initialRows -or
        [string]$table.Range.Address($false, $false) -ne $initialAddress) {
        throw 'Search changed the product table rows or address.'
    }

    $result = [pscustomobject]@{
        passed = $true
        rows = $initialRows
        table_address = $initialAddress
        repeated_cycles = $SearchCycles
        repeated_caption = [string]$searchButton.TextFrame2.TextRange.Text
        no_match_caption = $noMatchCaption
        literal_wildcard_caption = $wildcardCaption
        filter_preserved = $true
        progress_lifecycle = $true
        writeback_ui_lifecycle = $true
        excel_ready = [bool]$excel.Ready
        crash_event_delta = $null
        crash_dump_delta = $null
    }
    Write-Host 'stage=complete'
}
finally {
    if ($filterApplied -and $null -ne $sheet) {
        try { if ($sheet.FilterMode) { $sheet.ShowAllData() } } catch {}
    }
    if ($null -ne $book) {
        try { $book.Close($false) } catch {}
    }
    if ($null -ne $excel) {
        try { $excel.Quit() } catch {}
    }
    Release-ComObject $searchButton
    Release-ComObject $queryRange
    Release-ComObject $table
    Release-ComObject $sheet
    Release-ComObject $book
    Release-ComObject $excel
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}

$crashDelta = Excel-CrashEvents $startedAt
$crashDumpDelta = Excel-CrashDumps $startedAt
$result.crash_event_delta = $crashDelta
$result.crash_dump_delta = $crashDumpDelta
if ($crashDelta -gt 0) {
    throw "Excel crash events increased by $crashDelta during the focused test."
}
if ($crashDumpDelta -gt 0) {
    throw "Excel crash dumps increased by $crashDumpDelta during the focused test."
}
$result | ConvertTo-Json -Depth 6 -Compress
