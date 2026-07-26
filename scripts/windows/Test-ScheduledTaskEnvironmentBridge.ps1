param()

$ErrorActionPreference = "Stop"

$launcher = Join-Path $PSScriptRoot "Run-PatrisExportScheduledTask.ps1"
$testId = [guid]::NewGuid().ToString("N")
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("patris-scheduled-env-" + $testId)
$probeSource = Join-Path $testRoot "EnvironmentProbe.cs"
$probeExecutable = Join-Path $testRoot "EnvironmentProbe.exe"
$capturePath = Join-Path $testRoot "capture.txt"
$sleeperSource = Join-Path $testRoot "Sleeper.cs"
$variableName = "PATRIS_EXPORT_SCHEDULED_TASK_TEST_SECRET_$testId"
$captureVariableName = "PATRIS_EXPORT_SCHEDULED_TASK_TEST_CAPTURE_PATH_$testId"
$secret = "synthetic-" + [guid]::NewGuid().ToString("N")
$previousUserValue = [Environment]::GetEnvironmentVariable($variableName, "User")
$previousProcessValue = [Environment]::GetEnvironmentVariable($variableName, "Process")
$previousCaptureUserValue = [Environment]::GetEnvironmentVariable($captureVariableName, "User")
$previousCaptureProcessValue = [Environment]::GetEnvironmentVariable($captureVariableName, "Process")

function Get-TestProcessStatePath {
    param([Parameter(Mandatory = $true)][string]$ExecutablePath)

    $normalizedPath = [IO.Path]::GetFullPath($ExecutablePath).ToUpperInvariant()
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $digest = [BitConverter]::ToString(
            $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($normalizedPath))
        ).Replace("-", "").ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
    Join-Path (
        Join-Path ([Environment]::GetFolderPath(
            [Environment+SpecialFolder]::LocalApplicationData
        )) "AtomicDeploy\PatrisExport\process-state"
    ) ($digest.Substring(0, 32) + ".json")
}

try {
    New-Item -ItemType Directory -Path $testRoot | Out-Null
    $probeProgram = @'
using System;
using System.IO;
using System.Security.Cryptography;
using System.Text;

public static class EnvironmentProbe
{
    public static int Main(string[] args)
    {
        var output = Environment.GetEnvironmentVariable("__CAPTURE_VARIABLE__");
        var value = Environment.GetEnvironmentVariable("__SECRET_VARIABLE__");
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
'@
    $probeProgram = $probeProgram.Replace("__CAPTURE_VARIABLE__", $captureVariableName)
    $probeProgram = $probeProgram.Replace("__SECRET_VARIABLE__", $variableName)
    $probeProgram |
        Set-Content -LiteralPath $probeSource -Encoding utf8

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

    @'
using System.Threading;

public static class Sleeper
{
    public static int Main()
    {
        Thread.Sleep(60000);
        return 0;
    }
}
'@ | Set-Content -LiteralPath $sleeperSource -Encoding utf8

    $targetRoot = Join-Path $testRoot "target-deployment"
    New-Item -ItemType Directory -Path $targetRoot | Out-Null
    $targetExecutable = Join-Path $targetRoot "patris-export.exe"
    Add-Type -Path $sleeperSource -OutputAssembly $targetExecutable -OutputType ConsoleApplication
    $targetProcess = Start-Process -FilePath $targetExecutable -PassThru -WindowStyle Hidden
    $controlProcess = Start-Process -FilePath $targetExecutable -PassThru -WindowStyle Hidden
    try {
        $targetStatePath = Get-TestProcessStatePath -ExecutablePath $targetExecutable
        New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($targetStatePath)) -Force |
            Out-Null
        [ordered]@{
            schema         = "patris.scheduled-task-process"
            schema_version = 1
            pid            = $targetProcess.Id
            start_time_utc = $targetProcess.StartTime.ToUniversalTime().ToString("O")
            executable     = [IO.Path]::GetFullPath($targetExecutable)
            database       = "ignored.db"
            address        = "127.0.0.1:0"
        } | ConvertTo-Json -Compress |
            Set-Content -LiteralPath $targetStatePath -Encoding utf8
        & (Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1") `
            -Action Stop `
            -DeploymentDirectory $targetRoot `
            -TaskName ("PatrisExportMissing-" + [guid]::NewGuid().ToString("N")) `
            -TaskPath "\"
        $targetProcess.Refresh()
        $controlProcess.Refresh()
        if (-not $targetProcess.HasExited) {
            throw "The exact deployment child process survived the stop action."
        }
        if ($controlProcess.HasExited) {
            throw "The stop action terminated an untracked same-path process."
        }
    } finally {
        foreach ($process in @($targetProcess, $controlProcess)) {
            try {
                if (-not $process.HasExited) {
                    Stop-Process -Id $process.Id -Force
                }
            } catch {
            }
            $process.Dispose()
        }
    }

    $concurrentRoot = Join-Path $testRoot "concurrent-launcher"
    New-Item -ItemType Directory -Path $concurrentRoot | Out-Null
    $concurrentExecutable = Join-Path $concurrentRoot "patris-export.exe"
    Copy-Item -LiteralPath $targetExecutable -Destination $concurrentExecutable
    $concurrentStatePath = Join-Path $testRoot "concurrent-process-state.json"
    $powershell = (Get-Command powershell.exe -ErrorAction Stop).Source
    $firstLauncher = Start-Process -FilePath $powershell -ArgumentList @(
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        $launcher,
        "-Executable",
        $concurrentExecutable,
        "-DbPath",
        "ignored.db",
        "-Address",
        "127.0.0.1:0",
        "-ProcessStatePath",
        $concurrentStatePath
    ) -PassThru -WindowStyle Hidden
    try {
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        $runningState = $null
        while ([DateTime]::UtcNow -lt $deadline) {
            if (Test-Path -LiteralPath $concurrentStatePath -PathType Leaf) {
                try {
                    $candidateState = Get-Content -LiteralPath $concurrentStatePath -Raw |
                        ConvertFrom-Json
                    if ([string]$candidateState.status -eq "running") {
                        $runningState = $candidateState
                        break
                    }
                } catch {
                }
            }
            Start-Sleep -Milliseconds 100
        }
        if (-not $runningState) {
            throw "The first launcher did not commit a running process state."
        }
        $stateBeforeSecondLaunch = Get-Content -LiteralPath $concurrentStatePath -Raw
        $secondLaunchMessage = ""
        try {
            & $launcher `
                -Executable $concurrentExecutable `
                -DbPath "ignored.db" `
                -Address "127.0.0.1:0" `
                -ProcessStatePath $concurrentStatePath
            throw "A concurrent launcher was accepted."
        } catch {
            $secondLaunchMessage = $_.Exception.Message
        }
        if ($secondLaunchMessage -notlike "*owns the deployment process state*") {
            throw "The concurrent launcher did not fail on the deployment-state lock."
        }
        if ((Get-Content -LiteralPath $concurrentStatePath -Raw) -ne $stateBeforeSecondLaunch) {
            throw "A concurrent launcher replaced the original process identity."
        }
        $concurrentChildren = @(
            Get-Process -Name "patris-export" -ErrorAction SilentlyContinue |
                Where-Object {
                    try {
                        $_.Path -and [IO.Path]::GetFullPath($_.Path).Equals(
                            [IO.Path]::GetFullPath($concurrentExecutable),
                            [StringComparison]::OrdinalIgnoreCase
                        )
                    } catch {
                        $false
                    }
                }
        )
        if ($concurrentChildren.Count -ne 1 -or
            $concurrentChildren[0].Id -ne [int]$runningState.pid) {
            throw "Concurrent launch protection did not preserve exactly one tracked child."
        }
    } finally {
        if ($runningState) {
            Stop-Process -Id ([int]$runningState.pid) -Force -ErrorAction SilentlyContinue
        }
        if (-not $firstLauncher.WaitForExit(10000)) {
            Stop-Process -Id $firstLauncher.Id -Force -ErrorAction SilentlyContinue
        }
        $firstLauncher.Dispose()
        Remove-Item -LiteralPath $concurrentStatePath -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath "$concurrentStatePath.lock" -Force -ErrorAction SilentlyContinue
    }

    $blockedRoot = Join-Path $testRoot "blocked-process-state"
    New-Item -ItemType Directory -Path $blockedRoot | Out-Null
    $blockedAcl = Get-Acl -LiteralPath $blockedRoot
    $originalBlockedSddl = $blockedAcl.GetSecurityDescriptorSddlForm("All")
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $deniedRights = (
        [Security.AccessControl.FileSystemRights]::CreateFiles -bor
        [Security.AccessControl.FileSystemRights]::WriteData -bor
        [Security.AccessControl.FileSystemRights]::AppendData -bor
        [Security.AccessControl.FileSystemRights]::Delete
    )
    $deniedInheritance = (
        [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [Security.AccessControl.InheritanceFlags]::ObjectInherit
    )
    $denyWrite = New-Object Security.AccessControl.FileSystemAccessRule -ArgumentList @(
        $currentSid,
        $deniedRights,
        $deniedInheritance,
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Deny
    )
    $blockedAcl.AddAccessRule($denyWrite)
    Set-Acl -LiteralPath $blockedRoot -AclObject $blockedAcl
    try {
        $failureMessage = ""
        try {
            & $launcher `
                -Executable $targetExecutable `
                -DbPath "ignored.db" `
                -ProcessStatePath (Join-Path $blockedRoot "state.json")
            throw "An unwritable process-state path was accepted."
        } catch {
            $failureMessage = $_.Exception.Message
        }
        if ($failureMessage -notlike "*denied*" -and
            $failureMessage -notlike "*access*") {
            throw "The process-state write did not fail for the expected reason."
        }
        $survivors = @(
            Get-Process -Name "patris-export" -ErrorAction SilentlyContinue |
                Where-Object {
                    try {
                        $_.Path -and [IO.Path]::GetFullPath($_.Path).Equals(
                            [IO.Path]::GetFullPath($targetExecutable),
                            [StringComparison]::OrdinalIgnoreCase
                        )
                    } catch {
                        $false
                    }
                }
        )
        if ($survivors.Count -gt 0) {
            throw "A process-state write failure left an untracked child process."
        }
    } finally {
        $restoreAcl = New-Object Security.AccessControl.DirectorySecurity
        $restoreAcl.SetSecurityDescriptorSddlForm($originalBlockedSddl)
        Set-Acl -LiteralPath $blockedRoot -AclObject $restoreAcl
    }

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
