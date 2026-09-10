param(
    [string]$TaskName = 'Patris Export Viewer',
    [string]$TaskPath = '\AtomicDeploy\',
    [switch]$Apply
)

$ErrorActionPreference = 'Stop'
$task = Get-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName
if (@($task.Actions).Count -ne 1) { throw 'Expected exactly one existing task action.' }
$action = $task.Actions[0]
$arguments = $action.Arguments
if ($arguments -notmatch 'Run-PatrisExportScheduledTask\.ps1') {
    throw 'Task does not use the supported Patris environment launcher.'
}
$pattern = '-EnvironmentVariableNames\s+(?:"(?<quoted>[A-Za-z0-9_,]+)"|(?<bare>[A-Za-z0-9_,]+))'
$matchesFound = [regex]::Matches($arguments, $pattern)
if ($matchesFound.Count -ne 1) { throw 'Cannot safely preserve the existing environment import list.' }
$match = $matchesFound[0]
$existing = ($match.Groups['quoted'].Value + $match.Groups['bare'].Value) -split ','
$required = @('DIGITALOGIC_PRICING_WRITE_KEY', 'DIGITALOGIC_PRICING_WRITE_SECRET')
$names = @($existing + $required | Select-Object -Unique)
$replacement = '-EnvironmentVariableNames "' + ($names -join ',') + '"'
$updated = $arguments.Substring(0, $match.Index) + $replacement + $arguments.Substring($match.Index + $match.Length)

# Print names and readiness only. Never serialize task arguments or secrets.
foreach ($name in $required) {
    $available = -not [string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($name, 'User'))
    if (-not $available) {
        $available = -not [string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($name, 'Machine'))
    }
    [pscustomobject]@{ Name = $name; AlreadyImported = $existing -contains $name; AvailableToCurrentUser = $available }
    if ($Apply -and -not $available) { throw "Required environment variable unavailable: $name" }
}
if (-not $Apply) {
    Write-Output 'Preview only. Apply under the task user with Administrator rights; no restart is performed.'
    return
}
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Administrator rights are required to update this task.'
}
$taskUser = [Security.Principal.NTAccount]::new($task.Principal.UserId)
if ($taskUser.Translate([Security.Principal.SecurityIdentifier]).Value -ne $identity.User.Value) {
    throw 'Run as the scheduled task user so credential readiness is checked for the correct account.'
}
if ($arguments -cne $updated) {
    $replacementAction = New-ScheduledTaskAction -Execute $action.Execute -WorkingDirectory $action.WorkingDirectory -Argument $updated
    Set-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Action $replacementAction | Out-Null
}
$readback = Get-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName
if ($readback.Actions[0].Arguments -cne $updated -or $readback.Actions[0].Execute -cne $action.Execute -or $readback.Actions[0].WorkingDirectory -cne $action.WorkingDirectory) {
    throw 'Task action readback mismatch.'
}
Write-Output 'Verified environment import configuration. Running service unchanged; credentials apply on its next managed start.'
