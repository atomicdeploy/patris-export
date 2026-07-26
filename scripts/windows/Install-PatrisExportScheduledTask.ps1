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

function Get-PatrisTask {
    Get-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
}

function Get-PatrisDeploymentProcess {
    Get-Process -Name "patris-export" -ErrorAction SilentlyContinue |
        Where-Object {
            try {
                $_.Path -and [IO.Path]::GetFullPath($_.Path).Equals(
                    [IO.Path]::GetFullPath($exe),
                    [StringComparison]::OrdinalIgnoreCase
                )
            } catch {
                $false
            }
        }
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

function Wait-PatrisDeploymentProcessExit {
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    while (@(Get-PatrisDeploymentProcess).Count -gt 0 -and [DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Milliseconds 200
    }
    if (@(Get-PatrisDeploymentProcess).Count -gt 0) {
        throw "Patris Export is still running from the deployment directory."
    }
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
        $Debounce
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
        Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
        Start-Sleep -Seconds 2
        Show-PatrisTaskStatus
    }
    "Stop" {
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
        Show-PatrisTaskStatus
    }
    "Restart" {
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 1
        Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
        Start-Sleep -Seconds 2
        Show-PatrisTaskStatus
    }
    "Status" {
        Show-PatrisTaskStatus
    }
    "PrepareUpgrade" {
        $task = Get-PatrisTask
        if (-not $task) {
            if ($RequireOwnedAction -and @(Get-PatrisDeploymentProcess).Count -gt 0) {
                throw "Patris Export is running from this deployment without the expected task."
            }
            Write-Host "Scheduled task not installed: $TaskPath$TaskName"
            break
        }
        if ($RequireOwnedAction -and -not (Test-PatrisOwnedTaskAction -Task $task)) {
            if (@(Get-PatrisDeploymentProcess).Count -gt 0) {
                throw "An unrecognized task left Patris Export running from this deployment."
            }
            Write-Host "Scheduled task is not owned by this deployment; leaving it unchanged: $TaskPath$TaskName"
            break
        }
        $wasRunning = ([string]$task.State -eq "Running") -or @(Get-PatrisDeploymentProcess).Count -gt 0
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
        Wait-PatrisDeploymentProcessExit
        if ($wasRunning) {
            exit 10
        }
        break
    }
    "Remove" {
        $task = Get-PatrisTask
        if (-not $task) {
            if ($RequireOwnedAction -and @(Get-PatrisDeploymentProcess).Count -gt 0) {
                throw "Patris Export is running from this deployment without the expected task."
            }
            Write-Host "Scheduled task not installed: $TaskPath$TaskName"
            break
        }
        if ($RequireOwnedAction -and -not (Test-PatrisOwnedTaskAction -Task $task)) {
            if (@(Get-PatrisDeploymentProcess).Count -gt 0) {
                throw "An unrecognized task left Patris Export running from this deployment."
            }
            Write-Host "Scheduled task is not owned by this deployment; leaving it unchanged: $TaskPath$TaskName"
            break
        }
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -Confirm:$false
        Wait-PatrisDeploymentProcessExit
        Write-Host "Removed scheduled task: $TaskPath$TaskName"
    }
}
