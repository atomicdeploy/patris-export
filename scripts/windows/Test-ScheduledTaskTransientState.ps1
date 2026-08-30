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

Write-Host "Scheduled-task transient state tests passed read, identity, retry, and stale-identity boundaries."
