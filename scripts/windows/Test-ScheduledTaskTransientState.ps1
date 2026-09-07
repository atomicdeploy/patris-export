param()

$ErrorActionPreference = "Stop"

$installerPath = Join-Path $PSScriptRoot "Install-PatrisExportScheduledTask.ps1"
$tokens = $null
$parseErrors = $null
$installerAst = [Management.Automation.Language.Parser]::ParseFile(
    $installerPath,
    [ref]$tokens,
    [ref]$parseErrors
)
if ($parseErrors.Count -gt 0) {
    throw "Unable to parse scheduled-task installer: $installerPath"
}

function Get-InstallerFunctionBody {
    param([Parameter(Mandatory = $true)][string]$Name)

    $functionAst = $installerAst.Find(
        {
            param($node)
            $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
                $node.Name -eq $Name
        },
        $true
    )
    if (-not $functionAst) {
        throw "Scheduled-task installer omitted function: $Name"
    }
    return $functionAst.Body.GetScriptBlock()
}

$newTransientBody = Get-InstallerFunctionBody -Name "New-PatrisTransientProcessStateException"
$testTransientBody = Get-InstallerFunctionBody -Name "Test-PatrisTransientProcessStateError"
$readStateBody = Get-InstallerFunctionBody -Name "Read-PatrisProcessState"
$identityBody = Get-InstallerFunctionBody -Name "Get-PatrisProcessIdentityById"
$trackedBody = Get-InstallerFunctionBody -Name "Get-PatrisTrackedDeploymentProcess"

$transientReadResult = & {
    param($NewTransientBody, $TestTransientBody, $ReadStateBody)

    $processStatePath = "C:\synthetic\process-state.json"
    function New-PatrisTransientProcessStateException {
        param($Message, $InnerException)
        & $NewTransientBody -Message $Message -InnerException $InnerException
    }
    function Get-Content {
        throw [IO.IOException]::new("Synthetic sharing violation.")
    }

    try {
        & $ReadStateBody -AllowTransientObservation
        throw "A transient process-state read was accepted."
    } catch {
        [pscustomobject]@{
            Tagged  = & $TestTransientBody -ErrorRecord $_
            Message = $_.Exception.Message
        }
    }
} $newTransientBody $testTransientBody $readStateBody

if (-not $transientReadResult.Tagged -or
    $transientReadResult.Message -notlike "*temporarily unreadable*") {
    throw "A transient process-state read was not classified for bounded startup retry."
}

$malformedReadResult = & {
    param($NewTransientBody, $TestTransientBody, $ReadStateBody)

    $processStatePath = "C:\synthetic\process-state.json"
    function New-PatrisTransientProcessStateException {
        param($Message, $InnerException)
        & $NewTransientBody -Message $Message -InnerException $InnerException
    }
    function Get-Content {
        return '{"schema":'
    }

    try {
        & $ReadStateBody -AllowTransientObservation
        throw "Malformed process-state JSON was accepted."
    } catch {
        [pscustomobject]@{
            Tagged  = & $TestTransientBody -ErrorRecord $_
            Message = $_.Exception.Message
        }
    }
} $newTransientBody $testTransientBody $readStateBody

if ($malformedReadResult.Tagged) {
    throw "Malformed process-state JSON was incorrectly classified as transient."
}

$identityUnavailableResult = & {
    param($NewTransientBody, $TestTransientBody, $IdentityBody)

    function New-PatrisTransientProcessStateException {
        param($Message, $InnerException)
        & $NewTransientBody -Message $Message -InnerException $InnerException
    }
    function Get-Process {
        [pscustomobject]@{ Id = 4321 }
    }
    function New-PatrisProcessIdentity {
        throw [ComponentModel.Win32Exception]::new(
            "Synthetic process identity access failure."
        )
    }

    try {
        & $IdentityBody -ProcessId 4321 -AllowTransientObservation
        throw "Transient live-process identity unavailability was accepted."
    } catch {
        [pscustomobject]@{
            Tagged  = & $TestTransientBody -ErrorRecord $_
            Message = $_.Exception.Message
        }
    }
} $newTransientBody $testTransientBody $identityBody

if (-not $identityUnavailableResult.Tagged -or
    $identityUnavailableResult.Message -notlike "*temporarily unavailable*") {
    throw "Live-process identity unavailability was not classified for bounded startup retry."
}

$transientPassThroughResult = & {
    param($NewTransientBody, $TestTransientBody, $TrackedBody)

    $processStatePath = "C:\synthetic\process-state.json"
    function Test-Path { return $true }
    function Read-PatrisProcessState {
        $exception = & $NewTransientBody `
            -Message "Synthetic transient state transition."
        throw $exception
    }
    function Test-PatrisTransientProcessStateError {
        param($ErrorRecord)
        & $TestTransientBody -ErrorRecord $ErrorRecord
    }

    try {
        & $TrackedBody -AllowTransientObservation
        throw "A transient tracked-state failure was accepted."
    } catch {
        [pscustomobject]@{
            Tagged  = & $TestTransientBody -ErrorRecord $_
            Message = $_.Exception.Message
        }
    }
} $newTransientBody $testTransientBody $trackedBody

if (-not $transientPassThroughResult.Tagged -or
    $transientPassThroughResult.Message -notlike "*transient state transition*") {
    throw "Tracked-state validation collapsed a retryable observation into corruption."
}

$staleIdentityResult = & {
    param($TestTransientBody, $TrackedBody)

    $processStatePath = "C:\synthetic\process-state.json"
    $exe = "C:\synthetic\patris-export.exe"
    $recordedStart = [DateTimeOffset]::Parse("2026-08-30T12:00:00.0000000Z")
    function Test-Path { return $true }
    function Read-PatrisProcessState {
        [pscustomobject]@{
            schema          = "patris.scheduled-task-process"
            schema_version  = 2
            status          = "running"
            pid             = 4321
            start_time_utc  = "2026-08-30T12:00:00.0000000Z"
            executable      = $exe
        }
    }
    function Get-PatrisProcessIdentityById {
        [pscustomobject]@{
            Pid          = 4321
            StartTimeUtc = $recordedStart.UtcDateTime.AddSeconds(1)
            Executable   = $exe
        }
    }
    function ConvertTo-PatrisUtcInstant {
        return $recordedStart
    }
    function Test-PatrisTransientProcessStateError {
        param($ErrorRecord)
        & $TestTransientBody -ErrorRecord $ErrorRecord
    }

    try {
        & $TrackedBody -AllowTransientObservation
        throw "A stale or reused process identity was accepted."
    } catch {
        [pscustomobject]@{
            Tagged  = & $TestTransientBody -ErrorRecord $_
            Message = $_.Exception.Message
        }
    }
} $testTransientBody $trackedBody

if ($staleIdentityResult.Tagged -or
    $staleIdentityResult.Message -notlike "*process state is invalid*") {
    throw "A stale or reused process identity was not kept terminal and fail-closed."
}

$convertBody = Get-InstallerFunctionBody -Name "ConvertTo-PatrisUtcInstant"
# Exercise the actual reservation path without enumerating, starting or stopping
# any real process. DateTime versus DateTimeOffset ordering throws in Windows PS5.1.
foreach ($childTickDelta in @(-1, 0, 1)) {
    foreach ($recordedLauncher in @(
        "2026-08-30T12:00:00.1234567Z",
        "2026-08-30T15:30:00.1234567+03:30",
        [DateTime]::Parse("2026-08-30T12:00:00.1234567Z").ToUniversalTime()
    )) {
        $children = @(& {
            param($TrackedBody, $ConvertBody, $TestTransientBody, $RecordedLauncher, $ChildTickDelta)
            $processStatePath = "C:\synthetic\process-state.json"
            $exe = "C:\synthetic\patris-export.exe"
            $DbPath = "C:\synthetic\kala.db"
            $Address = "127.0.0.1:18080"
            $start = [DateTimeOffset]::Parse("2026-08-30T12:00:00.1234567Z").UtcDateTime
            function Test-Path { return $true }
            function Read-PatrisProcessState {
                [pscustomobject]@{
                    schema = "patris.scheduled-task-process"
                    schema_version = 2
                    status = "launching"
                    launcher_pid = 4321
                    launcher_start_time_utc = $RecordedLauncher
                    executable = $exe
                }
            }
            function ConvertTo-PatrisUtcInstant {
                param($Value)
                & $ConvertBody -Value $Value
            }
            function Get-PatrisProcessIdentityById {
                param($ProcessId)
                $ticks = if ($ProcessId -eq 4321) { 0 } else { $ChildTickDelta }
                [pscustomobject]@{ Pid = $ProcessId; StartTimeUtc = $start.AddTicks($ticks); Executable = $exe }
            }
            function Get-CimInstance {
                [pscustomobject]@{ ProcessId = 4322; ExecutablePath = $exe; CommandLine = "$exe serve $DbPath --addr $Address" }
            }
            function Test-PatrisTransientProcessStateError {
                param($ErrorRecord)
                & $TestTransientBody -ErrorRecord $ErrorRecord
            }
            function Remove-Item { throw "A live launcher reservation must not be removed." }
            & $TrackedBody -AllowTransientObservation
        } $trackedBody $convertBody $testTransientBody $recordedLauncher $childTickDelta)
        $expectedCount = if ($childTickDelta -lt 0) { 0 } else { 1 }
        if ($children.Count -ne $expectedCount -or ($expectedCount -eq 1 -and $children[0].Pid -ne 4322)) {
            throw "Launch reservation accepted an older child or rejected an equal/newer exact UTC instant."
        }
    }
}

Write-Host "Scheduled-task transient state tests passed read, identity, retry, stale-identity and launch-reservation ordering boundaries."
