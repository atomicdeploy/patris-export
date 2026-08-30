$ErrorActionPreference = "Stop"

function Get-UtcInstantFunctionBody {
    param([Parameter(Mandatory = $true)][string]$ScriptPath)

    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile(
        $ScriptPath,
        [ref]$tokens,
        [ref]$parseErrors
    )
    if ($parseErrors.Count -gt 0) {
        throw "Unable to parse scheduled-task helper: $ScriptPath"
    }
    $functionAst = $ast.Find(
        {
            param($node)
            $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
                $node.Name -eq "ConvertTo-PatrisUtcInstant"
        },
        $true
    )
    if (-not $functionAst) {
        throw "Scheduled-task helper omitted ConvertTo-PatrisUtcInstant: $ScriptPath"
    }
    return $functionAst.Body.GetScriptBlock()
}

$utcText = "2026-08-30T07:13:47.7719478Z"
$tehranOffsetText = "2026-08-30T10:43:47.7719478+03:30"
$expectedUtcTicks = [long]639236708277719478
$typedUtc = [DateTime]::SpecifyKind(
    [DateTime]::new($expectedUtcTicks),
    [DateTimeKind]::Utc
)
$helperPaths = @(
    (Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1"),
    (Join-Path $PSScriptRoot "Run-PatrisExportScheduledTask.ps1")
)

foreach ($helperPath in $helperPaths) {
    $convertToUtc = Get-UtcInstantFunctionBody -ScriptPath $helperPath
    foreach ($fixture in @(
        $typedUtc,
        $typedUtc.ToLocalTime(),
        $utcText,
        $tehranOffsetText
    )) {
        $actual = & $convertToUtc -Value $fixture
        if ($actual -isnot [DateTimeOffset] -or
            $actual.Offset -ne [TimeSpan]::Zero -or
            $actual.UtcTicks -ne $expectedUtcTicks) {
            throw (
                "Scheduled-task UTC identity changed exact ticks in {0}: " +
                "type={1}, offset={2}, ticks={3}, local_offset={4}"
            ) -f @(
                $helperPath,
                $actual.GetType().FullName,
                $actual.Offset,
                $actual.UtcTicks,
                [TimeZoneInfo]::Local.GetUtcOffset($typedUtc)
            )
        }
    }
}

Write-Host "Scheduled-task UTC identity preserved exact ticks across UTC and Tehran-offset inputs."
