param()

$ErrorActionPreference = "Stop"

function Get-StartupWaitFunctionBody {
    param([Parameter(Mandatory = $true)][string]$ScriptPath)

    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile(
        $ScriptPath,
        [ref]$tokens,
        [ref]$parseErrors
    )
    if ($parseErrors.Count -gt 0) {
        throw "Unable to parse scheduled-task installer: $ScriptPath"
    }
    $functionAst = $ast.Find(
        {
            param($node)
            $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
                $node.Name -eq "Wait-PatrisTaskStarted"
        },
        $true
    )
    if (-not $functionAst) {
        throw "Scheduled-task installer omitted Wait-PatrisTaskStarted: $ScriptPath"
    }
    return [pscustomobject]@{
        Ast  = $ast
        Body = $functionAst.Body.GetScriptBlock()
    }
}

$installerPath = Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1"
$parsedInstaller = Get-StartupWaitFunctionBody -ScriptPath $installerPath
$waitForStart = $parsedInstaller.Body
$baselineRunTime = [DateTime]::SpecifyKind(
    [DateTime]::new(2026, 8, 30, 7, 0, 0),
    [DateTimeKind]::Local
)

$verifiedResult = & {
    param($WaitForStart, $PreviousLastRunTime)

    $TaskName = "PatrisExportTest"
    $TaskPath = "\Test\"
    $script:startupPoll = 0
    $script:startupStopCalls = 0
    function Get-PatrisTask {
        $script:startupPoll++
        [pscustomobject]@{ State = "Running" }
    }
    function Get-PatrisManagedProcessSnapshot {
        param($Task)
        if ($script:startupPoll -ge 3) {
            return [pscustomobject]@{
                Pid          = 4242
                StartTimeUtc = [DateTime]::UtcNow
                Executable   = "C:\test\patris-export.exe"
            }
        }
        return @()
    }
    function Get-ScheduledTaskInfo {
        [pscustomobject]@{
            LastRunTime    = $PreviousLastRunTime
            LastTaskResult = 0
        }
    }
    function Stop-PatrisTaskAndDeploymentProcess {
        param($Task)
        $script:startupStopCalls++
    }

    $task = & $WaitForStart `
        -Operation "test start" `
        -PreviousLastRunTime $PreviousLastRunTime `
        -Timeout ([TimeSpan]::FromSeconds(1)) `
        -PollMilliseconds 1
    [pscustomobject]@{
        Task      = $task
        Polls     = $script:startupPoll
        StopCalls = $script:startupStopCalls
    }
} $waitForStart $baselineRunTime

if (-not $verifiedResult.Task -or
    [string]$verifiedResult.Task.State -ne "Running" -or
    $verifiedResult.Polls -lt 3 -or
    $verifiedResult.StopCalls -ne 0) {
    throw "The startup wait did not accept exactly one delayed verified child."
}

$exitResult = & {
    param($WaitForStart, $PreviousLastRunTime)

    $TaskName = "PatrisExportTest"
    $TaskPath = "\Test\"
    $script:startupPoll = 0
    $script:startupStopCalls = 0
    function Get-PatrisTask {
        $script:startupPoll++
        if ($script:startupPoll -eq 1) {
            return [pscustomobject]@{ State = "Running" }
        }
        return [pscustomobject]@{ State = "Ready" }
    }
    function Get-PatrisManagedProcessSnapshot {
        param($Task)
        return @()
    }
    function Get-ScheduledTaskInfo {
        [pscustomobject]@{
            LastRunTime    = $PreviousLastRunTime.AddSeconds(1)
            LastTaskResult = 7
        }
    }
    function Stop-PatrisTaskAndDeploymentProcess {
        param($Task)
        $script:startupStopCalls++
    }

    $message = ""
    try {
        & $WaitForStart `
            -Operation "test start" `
            -PreviousLastRunTime $PreviousLastRunTime `
            -Timeout ([TimeSpan]::FromSeconds(1)) `
            -PollMilliseconds 1
        throw "An exited startup was accepted."
    } catch {
        $message = $_.Exception.Message
    }
    [pscustomobject]@{
        Message   = $message
        StopCalls = $script:startupStopCalls
    }
} $waitForStart $baselineRunTime

if ($exitResult.Message -notlike "*exited during test start*state=Ready*LastTaskResult=7*" -or
    $exitResult.StopCalls -ne 0) {
    throw "The startup wait did not report a task that exited before creating a child."
}

$timeoutResult = & {
    param($WaitForStart, $PreviousLastRunTime)

    $TaskName = "PatrisExportTest"
    $TaskPath = "\Test\"
    $script:startupStopCalls = 0
    function Get-PatrisTask {
        [pscustomobject]@{ State = "Running" }
    }
    function Get-PatrisManagedProcessSnapshot {
        param($Task)
        return @()
    }
    function Get-ScheduledTaskInfo {
        [pscustomobject]@{
            LastRunTime    = $PreviousLastRunTime
            LastTaskResult = 267009
        }
    }
    function Stop-PatrisTaskAndDeploymentProcess {
        param($Task)
        $script:startupStopCalls++
    }

    $message = ""
    try {
        & $WaitForStart `
            -Operation "test restart" `
            -PreviousLastRunTime $PreviousLastRunTime `
            -Timeout ([TimeSpan]::FromMilliseconds(30)) `
            -PollMilliseconds 1
        throw "A permanently starting task was accepted."
    } catch {
        $message = $_.Exception.Message
    }
    [pscustomobject]@{
        Message   = $message
        StopCalls = $script:startupStopCalls
    }
} $waitForStart $baselineRunTime

if ($timeoutResult.Message -notlike "*still starting*test restart*stopped safely*" -or
    $timeoutResult.StopCalls -ne 1) {
    throw "The startup timeout did not safely stop and distinguish a still-starting task."
}

$notStartedResult = & {
    param($WaitForStart, $PreviousLastRunTime)

    $TaskName = "PatrisExportTest"
    $TaskPath = "\Test\"
    $script:startupStopCalls = 0
    function Get-PatrisTask {
        [pscustomobject]@{ State = "Ready" }
    }
    function Get-PatrisManagedProcessSnapshot {
        param($Task)
        return @()
    }
    function Get-ScheduledTaskInfo {
        [pscustomobject]@{
            LastRunTime    = $PreviousLastRunTime
            LastTaskResult = 0
        }
    }
    function Stop-PatrisTaskAndDeploymentProcess {
        param($Task)
        $script:startupStopCalls++
    }

    $message = ""
    try {
        & $WaitForStart `
            -Operation "test start" `
            -PreviousLastRunTime $PreviousLastRunTime `
            -Timeout ([TimeSpan]::FromMilliseconds(30)) `
            -PollMilliseconds 1
        throw "A task that never entered startup was accepted."
    } catch {
        $message = $_.Exception.Message
    }
    [pscustomobject]@{
        Message   = $message
        StopCalls = $script:startupStopCalls
    }
} $waitForStart $baselineRunTime

if ($notStartedResult.Message -notlike "*did not enter a starting state*test start*" -or
    $notStartedResult.StopCalls -ne 0) {
    throw "The startup wait did not distinguish a task that never entered startup."
}

$commands = @($parsedInstaller.Ast.FindAll(
    { param($node) $node -is [Management.Automation.Language.CommandAst] },
    $true
))
$startCommands = @($commands | Where-Object { $_.GetCommandName() -eq "Start-ScheduledTask" })
$waitCommands = @($commands | Where-Object { $_.GetCommandName() -eq "Wait-PatrisTaskStarted" })
$fixedSleeps = @($commands | Where-Object {
    $_.GetCommandName() -eq "Start-Sleep" -and $_.Extent.Text -match '-Seconds\s+2(?:\D|$)'
})
if ($startCommands.Count -ne 3 -or $waitCommands.Count -ne 4 -or $fixedSleeps.Count -ne 0) {
    throw "Start, restart, or install-start still bypasses the bounded verified-child wait."
}

Write-Host "Scheduled-task startup wait passed delayed-child, exited, bounded-cleanup, and never-started tests."
