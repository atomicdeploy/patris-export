param(
    [ValidateSet("Install", "Start", "Stop", "Restart", "Status", "Remove")]
    [string]$Action = "Install",
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$TaskName = "PatrisExport",
    [string]$TaskPath = "\PatrisExport\",
    [string]$DbPath = "C:\Patris\data4\kala.db",
    [string]$Address = ":18080",
    [string]$Debounce = "500ms",
    [string[]]$ExtraArgs = @(),
    [switch]$Start
)

$ErrorActionPreference = "Stop"

$deployRoot = Join-Path $AtomicDeployRoot "deploy"
$exe = Join-Path $deployRoot "patris-export.exe"
if (-not (Test-Path -LiteralPath $exe)) {
    $fallback = Join-Path $deployRoot "patris-export-windows-amd64.exe"
    if (Test-Path -LiteralPath $fallback) {
        Copy-Item -LiteralPath $fallback -Destination $exe -Force
    }
}
if (-not (Test-Path -LiteralPath $exe)) {
    throw "Executable not found: $exe. Run scripts\windows\Build-LocalWindows.ps1 first."
}

function Get-PatrisTask {
    Get-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
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
        WorkingDir     = $deployRoot
    } | Format-List
}

switch ($Action) {
    "Install" {
        New-Item -ItemType Directory -Force -Path $deployRoot | Out-Null
        $argumentParts = @("serve", "`"$DbPath`"", "--addr", $Address, "--debounce", $Debounce) + $ExtraArgs
        $taskAction = New-ScheduledTaskAction -Execute $exe -Argument ($argumentParts -join " ") -WorkingDirectory $deployRoot
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
        $task = New-ScheduledTask -Action $taskAction -Trigger $trigger -Settings $settings -Principal $principal
        Register-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -InputObject $task -Force | Out-Null
        Write-Host "Installed scheduled task: $TaskPath$TaskName"
        if ($Start) {
            Start-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath
            Start-Sleep -Seconds 2
        }
        Show-PatrisTaskStatus
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
    "Remove" {
        Stop-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $TaskName -TaskPath $TaskPath -Confirm:$false
        Write-Host "Removed scheduled task: $TaskPath$TaskName"
    }
}
