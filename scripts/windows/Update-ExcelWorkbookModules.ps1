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
if (-not (Test-Path -LiteralPath $resolvedInput -PathType Leaf)) {
    throw "Input workbook not found: $resolvedInput"
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
    $workbook = $excel.Workbooks.Open($resolvedOutput, 0, $false)

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

Get-Item -LiteralPath $resolvedOutput | Select-Object FullName, Length, LastWriteTime
