[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$InputPath,
    [Parameter(Mandatory)]
    [string]$OutputPath,
    [string]$ModulePath = (Join-Path $PSScriptRoot '..\..\docs\examples\vba\ProductCatalogSync.bas'),
    [string]$WorkbookClassPath = (Join-Path $PSScriptRoot '..\..\docs\examples\vba\ThisWorkbook.cls')
)

$ErrorActionPreference = 'Stop'

function Get-VbaCodeBody([string]$Path) {
    $text = Get-Content -LiteralPath $Path -Raw
    $match = [regex]::Match($text, '(?ms)^Option Explicit\s*.*$')
    if (-not $match.Success) {
        throw "Unable to locate VBA code body in $Path"
    }
    return $match.Value
}

$resolvedInput = [IO.Path]::GetFullPath($InputPath)
$resolvedOutput = [IO.Path]::GetFullPath($OutputPath)
$templateDataAuditPath = Join-Path $PSScriptRoot 'Test-ExcelTemplateDataFree.ps1'
if (-not (Test-Path -LiteralPath $resolvedInput -PathType Leaf)) {
    throw "Input workbook not found: $resolvedInput"
}
if ([IO.Path]::GetExtension($resolvedInput) -ieq '.xltm') {
    if (-not (Test-Path -LiteralPath $templateDataAuditPath -PathType Leaf)) {
        throw "Required empty-template release gate is missing: $templateDataAuditPath"
    }
    [void](& $templateDataAuditPath -Path $resolvedInput)
}
New-Item -ItemType Directory -Path (Split-Path -Parent $resolvedOutput) -Force | Out-Null
Copy-Item -LiteralPath $resolvedInput -Destination $resolvedOutput -Force

$excelSecurityPath = 'HKCU:\Software\Microsoft\Office\16.0\Excel\Security'
$previousAccessVBOM = Get-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -ErrorAction SilentlyContinue
New-Item -Path $excelSecurityPath -Force | Out-Null
Set-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -Type DWord -Value 1

$excel = $null
$workbook = $null
try {
    $excel = New-Object -ComObject Excel.Application
    $excel.Visible = $false
    $excel.DisplayAlerts = $false
    $excel.EnableEvents = $false
    $excel.AutomationSecurity = 3
    # Workbooks.Open treats an Excel template as a source for a new unsaved
    # workbook unless Editable is explicitly true. In that default mode the
    # module replacement succeeds in memory but Save does not update the .xltm
    # artifact. Pass every argument through Editable so the exact copied
    # template is opened and mutated in place.
    $missing = [Type]::Missing
    $workbook = $excel.Workbooks.Open(
        $resolvedOutput,
        0,
        $false,
        $missing,
        $missing,
        $missing,
        $false,
        $missing,
        $missing,
        $true
    )
    if ([IO.Path]::GetFullPath([string]$workbook.FullName) -ne $resolvedOutput) {
        throw "Excel opened a derived workbook instead of the target template: $($workbook.FullName)"
    }

    foreach ($replacement in @(
        @{ Name = 'ProductCatalogSync'; Path = $ModulePath },
        @{ Name = 'ThisWorkbook'; Path = $WorkbookClassPath }
    )) {
        $component = $workbook.VBProject.VBComponents.Item($replacement.Name)
        if ($null -eq $component) {
            throw "Workbook component not found: $($replacement.Name)"
        }
        $codeModule = $component.CodeModule
        if ($codeModule.CountOfLines -gt 0) {
            $codeModule.DeleteLines(1, $codeModule.CountOfLines)
        }
        $codeModule.AddFromString((Get-VbaCodeBody $replacement.Path))
    }

    # Older data-free templates predate the confirmed website-rate name used
    # by the live formulas. Repair that structural binding while the copied
    # template is already open. Runtime VBA independently verifies the same
    # binding before applying formulas so damaged copies still fail closed.
    $settings = $null
    $confirmedName = $null
    $confirmedRange = $null
    try {
        $settings = $workbook.Worksheets.Item(3)
        try { $workbook.Names.Item('ConfirmedCNYRate').Delete() } catch {}
        [void]$workbook.Names.Add('ConfirmedCNYRate', $settings.Range('G18'))
        $confirmedName = $workbook.Names.Item('ConfirmedCNYRate')
        $confirmedRange = $confirmedName.RefersToRange
        if (
            [string]$confirmedRange.Parent.CodeName -cne [string]$settings.CodeName -or
            [string]$confirmedRange.Address($false, $false) -cne 'G18'
        ) {
            throw 'ConfirmedCNYRate was not bound exactly to Settings!G18.'
        }
    }
    finally {
        if ($confirmedRange) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($confirmedRange) }
        if ($confirmedName) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($confirmedName) }
        if ($settings) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($settings) }
    }

    $workbook.Save()
} finally {
    if ($workbook) {
        try { $workbook.Close($true) } catch {}
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($workbook)
    }
    if ($excel) {
        try { $excel.Quit() } catch {}
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($excel)
    }
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
    if ($null -ne $previousAccessVBOM) {
        Set-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -Type DWord -Value ([int]$previousAccessVBOM.AccessVBOM)
    } else {
        Remove-ItemProperty -Path $excelSecurityPath -Name AccessVBOM -ErrorAction SilentlyContinue
    }
}

if ([IO.Path]::GetExtension($resolvedOutput) -ieq '.xltm') {
    [void](& $templateDataAuditPath -Path $resolvedOutput)
}

Get-Item -LiteralPath $resolvedOutput | Select-Object FullName, Length, LastWriteTime
