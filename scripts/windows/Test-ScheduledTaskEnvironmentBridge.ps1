param()

$ErrorActionPreference = "Stop"

$launcher = Join-Path $PSScriptRoot "Run-PatrisExportScheduledTask.ps1"
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("patris-scheduled-env-" + [guid]::NewGuid().ToString("N"))
$probeSource = Join-Path $testRoot "EnvironmentProbe.cs"
$probeExecutable = Join-Path $testRoot "EnvironmentProbe.exe"
$capturePath = Join-Path $testRoot "capture.txt"
$variableName = "PATRIS_EXPORT_SCHEDULED_TASK_TEST_SECRET"
$captureVariableName = "PATRIS_EXPORT_SCHEDULED_TASK_TEST_CAPTURE_PATH"
$secret = "synthetic-" + [guid]::NewGuid().ToString("N")
$previousUserValue = [Environment]::GetEnvironmentVariable($variableName, "User")
$previousProcessValue = [Environment]::GetEnvironmentVariable($variableName, "Process")
$previousCaptureUserValue = [Environment]::GetEnvironmentVariable($captureVariableName, "User")
$previousCaptureProcessValue = [Environment]::GetEnvironmentVariable($captureVariableName, "Process")

try {
    New-Item -ItemType Directory -Path $testRoot | Out-Null
    @'
using System;
using System.IO;
using System.Security.Cryptography;
using System.Text;

public static class EnvironmentProbe
{
    public static int Main(string[] args)
    {
        var output = Environment.GetEnvironmentVariable("PATRIS_EXPORT_SCHEDULED_TASK_TEST_CAPTURE_PATH");
        var value = Environment.GetEnvironmentVariable("PATRIS_EXPORT_SCHEDULED_TASK_TEST_SECRET");
        var hasProbeArgument = Array.IndexOf(args, "--bridge-probe") >= 0;
        if (string.IsNullOrEmpty(output) || string.IsNullOrEmpty(value) || !hasProbeArgument)
        {
            return 2;
        }
        using (var sha = SHA256.Create())
        {
            var digest = sha.ComputeHash(Encoding.UTF8.GetBytes(value));
            File.WriteAllText(output, BitConverter.ToString(digest).Replace("-", "").ToLowerInvariant());
        }
        return 0;
    }
}
'@ | Set-Content -LiteralPath $probeSource -Encoding utf8

    Add-Type -Path $probeSource -OutputAssembly $probeExecutable -OutputType ConsoleApplication
    [Environment]::SetEnvironmentVariable($variableName, $secret, "User")
    [Environment]::SetEnvironmentVariable($variableName, "stale-process-value", "Process")
    [Environment]::SetEnvironmentVariable($captureVariableName, $capturePath, "User")
    [Environment]::SetEnvironmentVariable($captureVariableName, "stale-capture-path", "Process")
    $extraArgsJson = ConvertTo-Json -Compress -InputObject @("--bridge-probe")
    $extraArgsBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($extraArgsJson))

    & $launcher `
        -Executable $probeExecutable `
        -DbPath "ignored.db" `
        -EnvironmentVariableNames "$variableName,$captureVariableName" `
        -ExtraArgsBase64 $extraArgsBase64
    if ($LASTEXITCODE -ne 0) {
        throw "Scheduled-task environment bridge probe failed with exit code $LASTEXITCODE."
    }

    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $expectedHash = [BitConverter]::ToString(
            $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($secret))
        ).Replace("-", "").ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
    $actualHash = (Get-Content -LiteralPath $capturePath -Raw).Trim()
    if ($actualHash -ne $expectedHash) {
        throw "The child process did not receive the current user-scoped value."
    }

    $missingName = "PATRIS_EXPORT_SCHEDULED_TASK_TEST_MISSING_" + [guid]::NewGuid().ToString("N")
    $message = ""
    try {
        & $launcher -Executable $probeExecutable -DbPath "ignored.db" -EnvironmentVariableNames $missingName
        throw "A missing required environment variable was accepted."
    } catch {
        $message = $_.Exception.Message
    }
    if ($message -notlike "*$missingName*" -or $message -like "*$secret*") {
        throw "Missing-variable diagnostics were not name-only."
    }

    $packageRoot = Join-Path $testRoot "package"
    New-Item -ItemType Directory -Path $packageRoot | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1") -Destination $packageRoot
    Copy-Item -LiteralPath $launcher -Destination $packageRoot
    Copy-Item -LiteralPath $probeExecutable -Destination (Join-Path $packageRoot "patris-export.exe")
    $packagedInstaller = Join-Path $packageRoot "Install-PatrisExportScheduledTask.ps1"
    $action = & $packagedInstaller `
        -Action Preview `
        -DbPath "C:\Patris\data4\kala.db" `
        -Address "127.0.0.1:18080" `
        -ImportEnvironmentVariable $variableName `
        -ExtraArgs @("--raw=false", "--watch=false")
    if (-not $action -or
        -not [IO.Path]::GetFullPath([string]$action.WorkingDirectory).Equals(
            [IO.Path]::GetFullPath($packageRoot),
            [StringComparison]::OrdinalIgnoreCase
        )) {
        throw "The packaged helper did not resolve its co-located deployment."
    }
    $taskArguments = [string]$action.Arguments
    if (-not $taskArguments.Contains($variableName) -or $taskArguments.Contains($secret)) {
        throw "The packaged task preview did not preserve the name-only credential boundary."
    }
    $encodedMatch = [regex]::Match($taskArguments, '-ExtraArgsBase64\s+([^\s"]+)')
    if (-not $encodedMatch.Success) {
        throw "The packaged task preview omitted encoded extra arguments."
    }
    $decodedPreviewArguments = [Text.Encoding]::UTF8.GetString(
        [Convert]::FromBase64String($encodedMatch.Groups[1].Value)
    )
    if ($decodedPreviewArguments -ne '["--raw=false","--watch=false"]') {
        throw "The packaged task preview changed the requested extra arguments."
    }

    $missingPayloadRoot = Join-Path $testRoot "missing-payload"
    New-Item -ItemType Directory -Path $missingPayloadRoot | Out-Null
    $removalHelper = Join-Path $missingPayloadRoot "Install-PatrisExportScheduledTask.ps1"
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1") -Destination $removalHelper
    & $removalHelper `
        -Action Remove `
        -DeploymentDirectory $missingPayloadRoot `
        -TaskName ("PatrisExportMissing-" + [guid]::NewGuid().ToString("N")) `
        -TaskPath "\"

    Write-Host "Scheduled-task environment bridge passed name-only import and fail-closed tests."
} finally {
    [Environment]::SetEnvironmentVariable($variableName, $previousUserValue, "User")
    [Environment]::SetEnvironmentVariable($variableName, $previousProcessValue, "Process")
    [Environment]::SetEnvironmentVariable($captureVariableName, $previousCaptureUserValue, "User")
    [Environment]::SetEnvironmentVariable($captureVariableName, $previousCaptureProcessValue, "Process")
    if (Test-Path -LiteralPath $testRoot) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
