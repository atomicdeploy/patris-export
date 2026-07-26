param(
    [Parameter(Mandatory = $true)]
    [string]$Executable,
    [Parameter(Mandatory = $true)]
    [string]$DbPath,
    [string]$Address = ":18080",
    [string]$Debounce = "500ms",
    [string]$EnvironmentVariableNames = "",
    [string]$ExtraArgsBase64 = ""
)

$ErrorActionPreference = "Stop"

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

Push-Location $workingDirectory
try {
    & $Executable @arguments
    if ($null -ne $LASTEXITCODE) {
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}
