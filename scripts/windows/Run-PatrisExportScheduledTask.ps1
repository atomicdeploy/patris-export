param(
    [Parameter(Mandatory = $true)]
    [string]$Executable,
    [Parameter(Mandatory = $true)]
    [string]$DbPath,
    [string]$Address = ":18080",
    [string]$Debounce = "500ms",
    [string]$EnvironmentVariableNames = "",
    [string]$ExtraArgsBase64 = "",
    [string]$ProcessStatePath = ""
)

$ErrorActionPreference = "Stop"

function ConvertTo-ProcessArgument {
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

function ConvertTo-PatrisUtcInstant {
    param([Parameter(Mandatory = $true)][object]$Value)

    # PowerShell 7 can materialize an ISO-8601 JSON string as DateTime. Casting
    # that value back to string drops its Kind and fractional seconds, so a
    # subsequent DateTime.Parse(...).ToUniversalTime() can both shift the
    # instant by the local offset and lose the exact process-start ticks.
    if ($Value -is [DateTimeOffset]) {
        return ([DateTimeOffset]$Value).ToUniversalTime()
    }
    if ($Value -is [DateTime]) {
        return ([DateTimeOffset]::new([DateTime]$Value)).ToUniversalTime()
    }
    return [DateTimeOffset]::Parse(
        [string]$Value,
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::RoundtripKind
    ).ToUniversalTime()
}

if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
    throw "Patris Export executable not found: $Executable"
}

$names = @(
    $EnvironmentVariableNames.Split(
        [char[]]@(","),
        [System.StringSplitOptions]::RemoveEmptyEntries
    ) |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ }
)

if ($names.Count -ne (@($names | Select-Object -Unique)).Count) {
    throw "Environment variable names must be unique."
}

foreach ($name in $names) {
    if ($name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
        throw "Invalid environment variable name: $name"
    }

    $value = [Environment]::GetEnvironmentVariable($name, "User")
    if ([string]::IsNullOrEmpty($value)) {
        $value = [Environment]::GetEnvironmentVariable($name, "Machine")
    }
    if ([string]::IsNullOrEmpty($value)) {
        throw "Required scheduled-task environment variable is unavailable: $name"
    }

    [Environment]::SetEnvironmentVariable($name, $value, "Process")
}

$extraArguments = @()
if ($ExtraArgsBase64) {
    try {
        $extraArgsJson = [Text.Encoding]::UTF8.GetString(
            [Convert]::FromBase64String($ExtraArgsBase64)
        )
        $decodedArguments = $extraArgsJson | ConvertFrom-Json
        $extraArguments = @($decodedArguments)
    } catch {
        throw "Scheduled-task extra arguments are not valid encoded JSON."
    }
    foreach ($argument in $extraArguments) {
        if ($argument -isnot [string] -or $argument.IndexOf([char]0) -ge 0 -or $argument -match "[`r`n]") {
            throw "Scheduled-task extra arguments must be strings without nulls or newlines."
        }
    }
}

$arguments = @("serve", $DbPath, "--addr", $Address, "--debounce", $Debounce) + $extraArguments
$workingDirectory = Split-Path -Parent $Executable
if (-not $ProcessStatePath) {
    $ProcessStatePath = Get-DefaultProcessStatePath -ExecutablePath $Executable
}
$ProcessStatePath = [IO.Path]::GetFullPath($ProcessStatePath)
$processStateDirectory = [IO.Path]::GetDirectoryName($ProcessStatePath)
New-Item -ItemType Directory -Path $processStateDirectory -Force | Out-Null
$stateLockPath = "$ProcessStatePath.lock"
try {
    $stateLock = [IO.File]::Open(
        $stateLockPath,
        [IO.FileMode]::OpenOrCreate,
        [IO.FileAccess]::ReadWrite,
        [IO.FileShare]::None
    )
} catch [IO.IOException] {
    $win32Code = $_.Exception.HResult -band 0xFFFF
    if ($win32Code -in @(32, 33)) {
        throw "Another scheduled-task launcher owns the deployment process state."
    }
    throw "Scheduled-task process-state storage access is unavailable."
} catch {
    throw "Scheduled-task process-state storage access is unavailable."
}
try {
    if (Test-Path -LiteralPath $ProcessStatePath -PathType Leaf) {
        try {
            $existingState = Get-Content -LiteralPath $ProcessStatePath -Raw | ConvertFrom-Json
            if ([string]$existingState.schema -ne "patris.scheduled-task-process" -or
                [int]$existingState.schema_version -notin @(1, 2) -or
                -not [string]$existingState.executable -or
                -not [IO.Path]::GetFullPath([string]$existingState.executable).Equals(
                    [IO.Path]::GetFullPath($Executable),
                    [StringComparison]::OrdinalIgnoreCase
                )) {
                throw "invalid state"
            }
            if ([int]$existingState.schema_version -eq 2 -and
                [string]$existingState.status -eq "launching") {
                throw "launch reservation requires recovery"
            }
            if (-not [int]$existingState.pid -or -not [string]$existingState.start_time_utc) {
                throw "invalid running state"
            }
            $existingProcess = Get-Process -Id ([int]$existingState.pid) -ErrorAction SilentlyContinue
            if ($existingProcess) {
                $existingStart = ConvertTo-PatrisUtcInstant -Value (
                    $existingState.start_time_utc
                )
                try {
                    if ($existingProcess.StartTime.ToUniversalTime().Ticks -eq $existingStart.UtcTicks -and
                        [IO.Path]::GetFullPath([string]$existingProcess.Path).Equals(
                            [IO.Path]::GetFullPath($Executable),
                            [StringComparison]::OrdinalIgnoreCase
                        )) {
                        throw "tracked child is already running"
                    }
                } catch {
                    if (Get-Process -Id ([int]$existingState.pid) -ErrorAction SilentlyContinue) {
                        throw
                    }
                }
            }
            Remove-Item -LiteralPath $ProcessStatePath -Force -ErrorAction Stop
        } catch {
            throw "Existing scheduled-task process state requires the installer helper's stop or restart action: $ProcessStatePath"
        }
    }
} catch {
    $stateLock.Dispose()
    throw
}
$process = $null
$launcherProcess = Get-Process -Id $PID -ErrorAction Stop
$launcherStartedUtc = $launcherProcess.StartTime.ToUniversalTime().ToString("O")
$startedPid = 0
$startedUtc = ""
$stateTempPath = "$ProcessStatePath.$PID.tmp"
$stateOwned = $false

try {
    $launchingState = [ordered]@{
        schema         = "patris.scheduled-task-process"
        schema_version = 2
        status         = "launching"
        launcher_pid   = $PID
        launcher_start_time_utc = $launcherStartedUtc
        executable     = [IO.Path]::GetFullPath($Executable)
    }
    $launchingState | ConvertTo-Json -Compress |
        Set-Content -LiteralPath $stateTempPath -Encoding utf8
    Move-Item -LiteralPath $stateTempPath -Destination $ProcessStatePath -Force
    $stateOwned = $true

    Push-Location $workingDirectory
    try {
        $startInfo = New-Object System.Diagnostics.ProcessStartInfo
        $startInfo.FileName = $Executable
        $startInfo.WorkingDirectory = $workingDirectory
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.Arguments = ($arguments | ForEach-Object {
            ConvertTo-ProcessArgument ([string]$_)
        }) -join " "
        $process = [Diagnostics.Process]::Start($startInfo)
        if (-not $process) {
            throw "Unable to start Patris Export."
        }
        $startedPid = $process.Id
        $startedUtc = $process.StartTime.ToUniversalTime().ToString("O")
        $runningState = [ordered]@{
            schema         = "patris.scheduled-task-process"
            schema_version = 2
            status         = "running"
            launcher_pid   = $PID
            launcher_start_time_utc = $launcherStartedUtc
            pid            = $startedPid
            start_time_utc = $startedUtc
            executable     = [IO.Path]::GetFullPath($Executable)
        }
        $runningState | ConvertTo-Json -Compress |
            Set-Content -LiteralPath $stateTempPath -Encoding utf8
        Move-Item -LiteralPath $stateTempPath -Destination $ProcessStatePath -Force
        $process.WaitForExit()
        exit $process.ExitCode
    } finally {
        Pop-Location
    }
} finally {
    if ($process) {
        try {
            try {
                if (-not $process.HasExited) {
                    $process.Kill()
                    $process.WaitForExit()
                }
            } catch {
                if (-not $process.HasExited) {
                    throw
                }
            }
        } finally {
            $process.Dispose()
        }
    }
    if ($stateOwned -and
        (Test-Path -LiteralPath $ProcessStatePath -PathType Leaf)) {
        try {
            $currentState = Get-Content -LiteralPath $ProcessStatePath -Raw | ConvertFrom-Json
            if ([int]$currentState.launcher_pid -eq $PID -and
                [string]$currentState.launcher_start_time_utc -eq $launcherStartedUtc) {
                Remove-Item -LiteralPath $ProcessStatePath -Force
            }
        } catch {
        }
    }
    if (Test-Path -LiteralPath $stateTempPath -PathType Leaf) {
        Remove-Item -LiteralPath $stateTempPath -Force -ErrorAction SilentlyContinue
    }
    $stateLock.Dispose()
}
