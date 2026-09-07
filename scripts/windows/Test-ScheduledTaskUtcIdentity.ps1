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

# Execute only the real cleanup ownership condition, never the launcher or its
# process/file operations. PS7 JSON may materialize the stored string as DateTime.
$launcherPath = Join-Path $PSScriptRoot "Run-PatrisExportScheduledTask.ps1"
$tokens = $null
$parseErrors = $null
$launcherAst = [Management.Automation.Language.Parser]::ParseFile($launcherPath, [ref]$tokens, [ref]$parseErrors)
$cleanupIf = $launcherAst.Find({
    param($node)
    $node -is [Management.Automation.Language.IfStatementAst] -and
        $node.Clauses[0].Item1.Extent.Text.Contains('[int]$currentState.launcher_pid -eq $PID')
}, $true)
if (-not $cleanupIf) { throw "Missing launcher cleanup ownership condition." }
$cleanupCondition = [scriptblock]::Create($cleanupIf.Clauses[0].Item1.Extent.Text)
$convertBody = Get-UtcInstantFunctionBody -ScriptPath $launcherPath
function ConvertTo-PatrisUtcInstant {
    param($Value)
    & $convertBody -Value $Value
}
$launcherStartedUtc = $utcText
foreach ($fixture in @($utcText, $tehranOffsetText, $typedUtc, $typedUtc.ToLocalTime())) {
    $currentState = [pscustomobject]@{ launcher_pid = $PID; launcher_start_time_utc = $fixture }
    if (-not (& $cleanupCondition)) { throw "Cleanup rejected the exact owning launcher instant." }
}
$currentState.launcher_start_time_utc = $typedUtc.AddTicks(1)
if (& $cleanupCondition) { throw "Cleanup accepted another launcher instant." }
$currentState.launcher_start_time_utc = $utcText
$currentState.launcher_pid = $PID + 1
if (& $cleanupCondition) { throw "Cleanup accepted another launcher PID." }

Write-Host "Scheduled-task UTC identity and cleanup ownership preserved exact ticks across UTC and Tehran-offset inputs."
