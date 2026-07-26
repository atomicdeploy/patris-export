param(
    [ValidateSet("Install", "Preview", "PrepareUpgrade", "Start", "Stop", "Restart", "Status", "Remove")]
    [string]$Action = "Install",
    [string]$AtomicDeployRoot = "",
    [string]$DeploymentDirectory = "",
    [string]$TaskName = "PatrisExport",
    [string]$TaskPath = "\PatrisExport\",
    [string]$DbPath = "C:\Patris\data4\kala.db",
    [string]$Address = ":18080",
    [string]$Debounce = "500ms",
    [ValidatePattern('^[A-Za-z_][A-Za-z0-9_]*$')]
    [string[]]$ImportEnvironmentVariable = @(),
    [string[]]$ExtraArgs = @(),
    [switch]$Start,
    [switch]$RequireOwnedAction
)

$ErrorActionPreference = "Stop"

if ($AtomicDeployRoot -and $DeploymentDirectory) {
    throw "Specify either AtomicDeployRoot or DeploymentDirectory, not both."
}
if ($DeploymentDirectory) {
    $deployRoot = [IO.Path]::GetFullPath($DeploymentDirectory)
} elseif ($AtomicDeployRoot) {
    $deployRoot = Join-Path ([IO.Path]::GetFullPath($AtomicDeployRoot)) "deploy"
} elseif (Test-Path -LiteralPath (Join-Path $PSScriptRoot "patris-export.exe") -PathType Leaf) {
    $deployRoot = $PSScriptRoot
} else {
    $deployRoot = Join-Path "$env:USERPROFILE\Desktop\AtomicDeploy" "deploy"
}
$exe = Join-Path $deployRoot "patris-export.exe"
$sourceLauncher = Join-Path $PSScriptRoot "Run-PatrisExportScheduledTask.ps1"
$launcher = Join-Path $deployRoot "Run-PatrisExportScheduledTask.ps1"

function ConvertTo-TaskArgument {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    if ($Value.IndexOf([char]0) -ge 0 -or $Value -match "[`r`n]") {
        throw "Scheduled-task arguments cannot contain nulls or newlines."
    }
    if ($Value -notmatch '[\s"]') {
        return $Value
    }

    $result = New-Object System.Text.StringBuilder
    [void]$result.Append('"')
    $backslashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $backslashes++
            continue
        }
        if ($character -eq '"') {
            [void]$result.Append((('\' * (($backslashes * 2) + 1)) -join ""))
            [void]$result.Append('"')
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            [void]$result.Append((('\' * $backslashes) -join ""))
            $backslashes = 0
        }
        [void]$result.Append($character)
    }
    if ($backslashes -gt 0) {
        [void]$result.Append((('\' * ($backslashes * 2)) -join ""))
    }
    [void]$result.Append('"')
    return $result.ToString()
}

function Get-DefaultProcessStatePath {
    param([Parameter(Mandatory = $true)][string]$ExecutablePath)

    $localAppData = [Environment]::GetFolderPath(
        [Environment+SpecialFolder]::LocalApplicationData
    )
    if ([string]::IsNullOrWhiteSpace($localAppData)) {
        throw "The current user's local application data directory is unavailable."
    }
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
        Join-Path $localAppData "AtomicDeploy\PatrisExport\process-state"
    ) ($digest.Substring(0, 32) + ".json")
}

$processStatePath = Get-DefaultProcessStatePath -ExecutablePath $exe

function Get-PatrisTask {
    Get-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
}

function New-PatrisProcessIdentity {
    param([Parameter(Mandatory = $true)]$Process)

    [pscustomobject]@{
        Pid          = [int]$Process.Id
        StartTimeUtc = $Process.StartTime.ToUniversalTime()
        Executable   = [IO.Path]::GetFullPath([string]$Process.Path)
    }
}

function Get-PatrisProcessIdentityById {
    param([Parameter(Mandatory = $true)][int]$ProcessId)

    for ($attempt = 0; $attempt -lt 2; $attempt++) {
        $process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
        if (-not $process) {
            return $null
        }
        try {
            return New-PatrisProcessIdentity -Process $process
        } catch {
            if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
                return $null
            }
            if ($attempt -eq 1) {
                throw "Unable to verify the identity of live process $ProcessId."
            }
        }
    }
    return $null
}

function Test-PatrisOwnedTaskAction {
    param([Parameter(Mandatory = $true)]$Task)

    $action = @($Task.Actions) | Select-Object -First 1
    if (-not $action) {
        return $false
    }

    $arguments = [string]$action.Arguments
    if ($arguments.IndexOf($launcher, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        return $true
    }

    try {
        $legacyExecutableMatches = [IO.Path]::GetFullPath([string]$action.Execute).Equals(
            [IO.Path]::GetFullPath($exe),
            [StringComparison]::OrdinalIgnoreCase
        )
        $legacyWorkingDirectoryMatches = [IO.Path]::GetFullPath([string]$action.WorkingDirectory).Equals(
            [IO.Path]::GetFullPath($deployRoot),
            [StringComparison]::OrdinalIgnoreCase
        )
        return $legacyExecutableMatches -and $legacyWorkingDirectoryMatches
    } catch {
        return $false
    }
}

function Get-PatrisTrackedDeploymentProcess {
    if (-not (Test-Path -LiteralPath $processStatePath -PathType Leaf)) {
        return @()
    }
    try {
        $state = Get-Content -LiteralPath $processStatePath -Raw | ConvertFrom-Json
        $schemaVersion = [int]$state.schema_version
        if ([string]$state.schema -ne "patris.scheduled-task-process" -or
            $schemaVersion -notin @(1, 2) -or
            -not [string]$state.executable) {
            throw "invalid state"
        }
        if (-not [IO.Path]::GetFullPath([string]$state.executable).Equals(
            [IO.Path]::GetFullPath($exe),
            [StringComparison]::OrdinalIgnoreCase
        )) {
            throw "wrong deployment"
        }

        if ($schemaVersion -eq 2 -and [string]$state.status -eq "launching") {
            if (-not [int]$state.launcher_pid -or
                -not [string]$state.launcher_start_time_utc) {
                throw "invalid launch reservation"
            }
            $launcherStart = [DateTime]::Parse(
                [string]$state.launcher_start_time_utc,
                [Globalization.CultureInfo]::InvariantCulture,
                [Globalization.DateTimeStyles]::RoundtripKind
            ).ToUniversalTime()
            $launcherIdentity = Get-PatrisProcessIdentityById -ProcessId ([int]$state.launcher_pid)
            $launcherMatches = $launcherIdentity -and
                $launcherIdentity.StartTimeUtc.Ticks -eq $launcherStart.Ticks
            $reservedChildren = @()
            foreach ($candidate in @(
                Get-CimInstance Win32_Process -Filter (
                    "ParentProcessId = {0}" -f [int]$state.launcher_pid
                ) -ErrorAction Stop
            )) {
                try {
                    $candidatePath = [IO.Path]::GetFullPath([string]$candidate.ExecutablePath)
                } catch {
                    if (-not (Get-Process -Id ([int]$candidate.ProcessId) -ErrorAction SilentlyContinue)) {
                        continue
                    }
                    throw "unable to verify reserved child path"
                }
                if (-not $candidatePath.Equals(
                    [IO.Path]::GetFullPath($exe),
                    [StringComparison]::OrdinalIgnoreCase
                )) {
                    continue
                }
                $candidateCommandLine = [string]$candidate.CommandLine
                if ($candidateCommandLine -notmatch '(^|\s)serve(\s|$)' -or
                    $candidateCommandLine.IndexOf(
                        $DbPath,
                        [StringComparison]::OrdinalIgnoreCase
                    ) -lt 0 -or
                    $candidateCommandLine.IndexOf(
                        $Address,
                        [StringComparison]::OrdinalIgnoreCase
                    ) -lt 0) {
                    throw "reserved child command does not match the managed server"
                }
                $identity = Get-PatrisProcessIdentityById -ProcessId ([int]$candidate.ProcessId)
                if (-not $identity -or $identity.StartTimeUtc -lt $launcherStart) {
                    continue
                }
                if (-not $launcherMatches -and $launcherIdentity -and
                    $identity.StartTimeUtc -ge $launcherIdentity.StartTimeUtc) {
                    continue
                }
                $reservedChildren += $identity
            }
            if ($reservedChildren.Count -gt 1) {
                throw "launch reservation has multiple matching child processes"
            }
            if ($reservedChildren.Count -eq 1) {
                return @($reservedChildren[0])
            }
            if ($launcherMatches) {
                return @()
            }
            Remove-Item -LiteralPath $processStatePath -Force -ErrorAction Stop
            return @()
        }

        if (-not [int]$state.pid -or -not [string]$state.start_time_utc -or
            ($schemaVersion -eq 2 -and [string]$state.status -ne "running")) {
            throw "invalid running state"
        }
        $identity = Get-PatrisProcessIdentityById -ProcessId ([int]$state.pid)
        if ($identity) {
            $recordedStart = [DateTime]::Parse(
                [string]$state.start_time_utc,
                [Globalization.CultureInfo]::InvariantCulture,
                [Globalization.DateTimeStyles]::RoundtripKind
            ).ToUniversalTime()
            if (-not $identity.Executable.Equals(
                [IO.Path]::GetFullPath($exe),
                [StringComparison]::OrdinalIgnoreCase
            ) -or $identity.StartTimeUtc.Ticks -ne $recordedStart.Ticks) {
                throw "stale or reused process identity"
            }
            return @($identity)
        }
        Remove-Item -LiteralPath $processStatePath -Force -ErrorAction Stop
        return @()
    } catch {
        throw "Scheduled-task process state is invalid; refusing to terminate a process: $processStatePath"
    }
}

function Get-PatrisLegacyTaskChildProcess {
    param([Parameter(Mandatory = $true)]$Task)

    if ([string]$Task.State -ne "Running" -or -not (Test-PatrisOwnedTaskAction -Task $Task)) {
        return @()
    }
    $processes = @(Get-CimInstance Win32_Process -ErrorAction Stop)
    $identities = @()
    foreach ($candidate in @($processes | Where-Object { $_.Name -ieq "patris-export.exe" })) {
        try {
            if (-not [IO.Path]::GetFullPath([string]$candidate.ExecutablePath).Equals(
                [IO.Path]::GetFullPath($exe),
                [StringComparison]::OrdinalIgnoreCase
            )) {
                continue
            }
            $parent = $processes |
                Where-Object { [int]$_.ProcessId -eq [int]$candidate.ParentProcessId } |
                Select-Object -First 1
            $parentCommandLine = [string]$parent.CommandLine
            $candidateCommandLine = [string]$candidate.CommandLine
            if (-not $parent -or
                $parentCommandLine.IndexOf(
                    $launcher,
                    [StringComparison]::OrdinalIgnoreCase
                ) -lt 0 -or
                $candidateCommandLine -notmatch '(^|\s)serve(\s|$)' -or
                $candidateCommandLine.IndexOf(
                    $DbPath,
                    [StringComparison]::OrdinalIgnoreCase
                ) -lt 0 -or
                $candidateCommandLine.IndexOf(
                    $Address,
                    [StringComparison]::OrdinalIgnoreCase
                ) -lt 0) {
                continue
            }
            $identity = Get-PatrisProcessIdentityById -ProcessId ([int]$candidate.ProcessId)
            if ($identity) {
                $identities += $identity
            }
        } catch {
        }
    }
    return @($identities)
}

function Get-PatrisManagedProcessSnapshot {
    param($Task)

    $tracked = @(Get-PatrisTrackedDeploymentProcess)
    if ($tracked.Count -gt 0) {
        return $tracked
    }
    if ($Task) {
        return @(Get-PatrisLegacyTaskChildProcess -Task $Task)
    }
    return @()
}

function Merge-PatrisProcessIdentity {
    param([Parameter(Mandatory = $true)][object[]]$Identity)

    $merged = [ordered]@{}
    foreach ($candidate in $Identity) {
        if (-not $candidate) {
            continue
        }
        $key = "{0}|{1}|{2}" -f @(
            [int]$candidate.Pid,
            $candidate.StartTimeUtc.Ticks,
            ([string]$candidate.Executable).ToUpperInvariant()
        )
        $merged[$key] = $candidate
    }
    return @($merged.Values)
}

function Wait-PatrisTaskStopped {
    param($Task)

    if (-not $Task) {
        return
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    while ([DateTime]::UtcNow -lt $deadline) {
        $currentTask = Get-PatrisTask
        if (-not $currentTask -or [string]$currentTask.State -ne "Running") {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    throw "The Patris Export scheduled task did not stop."
}

function Wait-PatrisDeploymentProcessExit {
    param([Parameter(Mandatory = $true)][object[]]$Identity)

    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    foreach ($expected in $Identity) {
        while ([DateTime]::UtcNow -lt $deadline) {
            $current = Get-PatrisProcessIdentityById -ProcessId $expected.Pid
            if (-not $current) {
                break
            }
            if ($current.StartTimeUtc.Ticks -ne $expected.StartTimeUtc.Ticks -or
                -not $current.Executable.Equals(
                    $expected.Executable,
                    [StringComparison]::OrdinalIgnoreCase
                )) {
                break
            }
            Start-Sleep -Milliseconds 200
        }
        $current = Get-PatrisProcessIdentityById -ProcessId $expected.Pid
        if ($current) {
            if ($current.StartTimeUtc.Ticks -eq $expected.StartTimeUtc.Ticks -and
                $current.Executable.Equals(
                    $expected.Executable,
                    [StringComparison]::OrdinalIgnoreCase
                )) {
                throw "The tracked Patris Export process is still running."
            }
        }
    }
}

function Stop-PatrisDeploymentProcess {
    param([Parameter(Mandatory = $true)][object[]]$Identity)

    foreach ($expected in $Identity) {
        $current = Get-PatrisProcessIdentityById -ProcessId $expected.Pid
        if (-not $current) {
            continue
        }
        if ($current.StartTimeUtc.Ticks -ne $expected.StartTimeUtc.Ticks -or
            -not $current.Executable.Equals(
                $expected.Executable,
                [StringComparison]::OrdinalIgnoreCase
            )) {
            throw "A tracked Patris Export process identity changed before termination."
        }
        $process = Get-Process -Id $expected.Pid -ErrorAction SilentlyContinue
        if (-not $process) {
            continue
        }
        try {
            Stop-Process -InputObject $process -Force -ErrorAction Stop
        } catch {
            $remaining = Get-PatrisProcessIdentityById -ProcessId $expected.Pid
            if ($remaining) {
                if ($remaining.StartTimeUtc.Ticks -eq $expected.StartTimeUtc.Ticks -and
                    $remaining.Executable.Equals(
                        $expected.Executable,
                        [StringComparison]::OrdinalIgnoreCase
                    )) {
                    throw
                }
            }
        }
    }
    Wait-PatrisDeploymentProcessExit -Identity $Identity
    $remainingTracked = @(Get-PatrisTrackedDeploymentProcess)
    if ($remainingTracked.Count -gt 0) {
        throw "A tracked Patris Export child process appeared or remained during termination."
    }
}

function Stop-PatrisTaskAndDeploymentProcess {
    param($Task)

    $beforeStop = @(Get-PatrisManagedProcessSnapshot -Task $Task)
    if ($Task) {
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
        Wait-PatrisTaskStopped -Task $Task
    }
    $afterStop = @(Get-PatrisTrackedDeploymentProcess)
    $identityCandidates = @($beforeStop + $afterStop)
    if ($identityCandidates.Count -eq 0) {
        return @()
    }
    $managedProcesses = @(
        Merge-PatrisProcessIdentity -Identity $identityCandidates
    )
    Stop-PatrisDeploymentProcess -Identity $managedProcesses
    return $managedProcesses
}

function New-PatrisTaskAction {
    $environmentNames = @($ImportEnvironmentVariable | Select-Object -Unique)
    if ($environmentNames.Count -ne $ImportEnvironmentVariable.Count) {
        throw "Imported environment variable names must be unique."
    }
    $powershell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    if (-not (Test-Path -LiteralPath $powershell -PathType Leaf)) {
        $powershell = (Get-Command "powershell.exe" -ErrorAction Stop).Source
    }
    $argumentParts = @(
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-WindowStyle",
        "Hidden",
        "-File",
        $launcher,
        "-Executable",
        $exe,
        "-DbPath",
        $DbPath,
        "-Address",
        $Address,
        "-Debounce",
        $Debounce,
        "-ProcessStatePath",
        $processStatePath
    )
    if ($environmentNames.Count -gt 0) {
        $argumentParts += @("-EnvironmentVariableNames", ($environmentNames -join ","))
    }
    if ($ExtraArgs.Count -gt 0) {
        $extraArgsJson = ConvertTo-Json -Compress -InputObject @($ExtraArgs)
        $extraArgsBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($extraArgsJson))
        $argumentParts += @("-ExtraArgsBase64", $extraArgsBase64)
    }
    $taskArguments = ($argumentParts | ForEach-Object { ConvertTo-TaskArgument $_ }) -join " "
    New-ScheduledTaskAction -Execute $powershell -Argument $taskArguments -WorkingDirectory $deployRoot
}

function Show-PatrisTaskStatus {
    $task = Get-PatrisTask
    if (-not $task) {
        Write-Host "Scheduled task not installed: $TaskPath$TaskName"
        return
    }
    $info = Get-ScheduledTaskInfo -TaskName $TaskName -TaskPath $TaskPath
    [pscustomobject]@{
        TaskName       = "$TaskPath$TaskName"
        State          = $task.State
        LastRunTime    = $info.LastRunTime
        LastTaskResult = $info.LastTaskResult
        NextRunTime    = $info.NextRunTime
        Executable     = $exe
        Launcher       = $task.Actions.Execute
        WorkingDir     = $deployRoot
    } | Format-List
}

switch ($Action) {
    { $_ -in @("Install", "Preview") } {
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
            $fallback = Join-Path $deployRoot "patris-export-windows-amd64.exe"
            if (Test-Path -LiteralPath $fallback -PathType Leaf) {
                if ($Action -eq "Install") {
                    Copy-Item -LiteralPath $fallback -Destination $exe -Force
                } else {
                    $exe = $fallback
                }
            }
        }
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
            throw "Executable not found: $exe. Run scripts\windows\Build-LocalWindows.ps1 first."
        }
        if (-not (Test-Path -LiteralPath $sourceLauncher -PathType Leaf)) {
            throw "Scheduled-task launcher not found: $sourceLauncher"
        }

        if ($Action -eq "Preview") {
            New-PatrisTaskAction
            break
        }

        New-Item -ItemType Directory -Force -Path $deployRoot | Out-Null
        if (-not [IO.Path]::GetFullPath($sourceLauncher).Equals(
            [IO.Path]::GetFullPath($launcher),
            [StringComparison]::OrdinalIgnoreCase
        )) {
            Copy-Item -LiteralPath $sourceLauncher -Destination $launcher -Force
        }
        $existingTask = Get-PatrisTask
        [void](Stop-PatrisTaskAndDeploymentProcess -Task $existingTask)
        $taskAction = New-PatrisTaskAction
        $trigger = New-ScheduledTaskTrigger -AtLogOn
        $settings = New-ScheduledTaskSettingsSet `
            -AllowStartIfOnBatteries `
            -DontStopIfGoingOnBatteries `
            -ExecutionTimeLimit ([TimeSpan]::Zero) `
            -MultipleInstances IgnoreNew `
            -RestartCount 999 `
            -RestartInterval (New-TimeSpan -Minutes 1) `
            -StartWhenAvailable
        $principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
        $task = New-ScheduledTask `
            -Action $taskAction `
            -Trigger $trigger `
            -Settings $settings `
            -Principal $principal `
            -Description "Patris Export managed local server task."
        Register-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -InputObject $task -Force | Out-Null
        Write-Host "Installed scheduled task: $TaskPath$TaskName"
        if ($Start) {
            Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
            Start-Sleep -Seconds 2
        }
        Show-PatrisTaskStatus
        break
    }
    "Start" {
        $task = Get-PatrisTask
        if (-not $task) {
            throw "Scheduled task not installed: $TaskPath$TaskName"
        }
        $managedProcesses = @(Get-PatrisManagedProcessSnapshot -Task $task)
        if ([string]$task.State -eq "Running") {
            if ($managedProcesses.Count -gt 0) {
                Show-PatrisTaskStatus
                break
            }
            throw "The scheduled task is running without a verified Patris Export child process."
        }
        if ($managedProcesses.Count -gt 0) {
            throw "A verified Patris Export child process exists outside the task state."
        }
        Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
        Start-Sleep -Seconds 2
        $startedTask = Get-PatrisTask
        if (@(Get-PatrisManagedProcessSnapshot -Task $startedTask).Count -eq 0) {
            throw "The scheduled task did not start a verified Patris Export child process."
        }
        Show-PatrisTaskStatus
    }
    "Stop" {
        $task = Get-PatrisTask
        [void](Stop-PatrisTaskAndDeploymentProcess -Task $task)
        Show-PatrisTaskStatus
    }
    "Restart" {
        $task = Get-PatrisTask
        if (-not $task) {
            throw "Scheduled task not installed: $TaskPath$TaskName"
        }
        [void](Stop-PatrisTaskAndDeploymentProcess -Task $task)
        Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
        Start-Sleep -Seconds 2
        $startedTask = Get-PatrisTask
        if (@(Get-PatrisManagedProcessSnapshot -Task $startedTask).Count -eq 0) {
            throw "The scheduled task did not restart a verified Patris Export child process."
        }
        Show-PatrisTaskStatus
    }
    "Status" {
        Show-PatrisTaskStatus
    }
    "PrepareUpgrade" {
        $task = Get-PatrisTask
        if (-not $task) {
            if ($RequireOwnedAction -and @(Get-PatrisTrackedDeploymentProcess).Count -gt 0) {
                throw "A tracked Patris Export child process exists without the expected task."
            }
            Write-Host "Scheduled task not installed: $TaskPath$TaskName"
            break
        }
        if ($RequireOwnedAction -and -not (Test-PatrisOwnedTaskAction -Task $task)) {
            if (@(Get-PatrisTrackedDeploymentProcess).Count -gt 0) {
                throw "An unrecognized task owns the tracked Patris Export child process."
            }
            Write-Host "Scheduled task is not owned by this deployment; leaving it unchanged: $TaskPath$TaskName"
            break
        }
        $managedProcesses = @(Get-PatrisManagedProcessSnapshot -Task $task)
        $wasRunning = ([string]$task.State -eq "Running") -or $managedProcesses.Count -gt 0
        [void](Stop-PatrisTaskAndDeploymentProcess -Task $task)
        if ($wasRunning) {
            exit 10
        }
        break
    }
    "Remove" {
        $task = Get-PatrisTask
        if (-not $task) {
            if ($RequireOwnedAction -and @(Get-PatrisTrackedDeploymentProcess).Count -gt 0) {
                throw "A tracked Patris Export child process exists without the expected task."
            }
            Write-Host "Scheduled task not installed: $TaskPath$TaskName"
            break
        }
        if ($RequireOwnedAction -and -not (Test-PatrisOwnedTaskAction -Task $task)) {
            if (@(Get-PatrisTrackedDeploymentProcess).Count -gt 0) {
                throw "An unrecognized task owns the tracked Patris Export child process."
            }
            Write-Host "Scheduled task is not owned by this deployment; leaving it unchanged: $TaskPath$TaskName"
            break
        }
        [void](Stop-PatrisTaskAndDeploymentProcess -Task $task)
        Unregister-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -Confirm:$false
        Write-Host "Removed scheduled task: $TaskPath$TaskName"
    }
}
