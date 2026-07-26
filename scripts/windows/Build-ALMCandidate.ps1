[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BuildDirectory,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory,

    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$ArtifactLabel,

    [Parameter(Mandatory = $true)]
    [string]$SourceCommit,

    [Parameter(Mandatory = $true)]
    [string]$SourceBuildDate,

    [Parameter(Mandatory = $true)]
    [string]$ALMAppID,

    [string]$DatabasePath,
    [string]$PythonPath = "python",
    [string]$MakensisPath,

    # Local validation can avoid a UAC prompt; CI deliberately leaves this off
    # so the uploaded full assisted installer is also installed and tested.
    [switch]$SkipAssistedInstallSmoke
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$BuildDirectory = (Resolve-Path -LiteralPath $BuildDirectory).Path
if (-not $DatabasePath) {
    $DatabasePath = Join-Path $repoRoot "testdata\kala.db"
}
$DatabasePath = (Resolve-Path -LiteralPath $DatabasePath).Path

if ($Version -notmatch '^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$') {
    throw "Version must begin with semantic version X.Y.Z: $Version"
}
if ($ArtifactLabel -notmatch '^[0-9A-Za-z][0-9A-Za-z._-]*-alm-compat$') {
    throw "ArtifactLabel must be filename-safe and end in -alm-compat: $ArtifactLabel"
}
if ($SourceCommit -notmatch '^[0-9a-fA-F]{7,64}$') {
    throw "SourceCommit is not a hexadecimal Git commit ID: $SourceCommit"
}
if ($ALMAppID -notmatch '^[A-Za-z0-9._:@+\-]{6,128}$') {
    throw "ALMAppID contains unsupported characters."
}

function Test-PathBeneath {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Parent
    )

    $candidatePath = [IO.Path]::GetFullPath($Candidate).TrimEnd('\')
    $parentPath = [IO.Path]::GetFullPath($Parent).TrimEnd('\')
    return $candidatePath.StartsWith($parentPath + '\', [StringComparison]::OrdinalIgnoreCase)
}

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$allowedOutputRoots = @((Join-Path $repoRoot "build"), $env:RUNNER_TEMP) | Where-Object { $_ }
if (-not ($allowedOutputRoots | Where-Object { Test-PathBeneath -Candidate $outputPath -Parent $_ })) {
    throw "OutputDirectory must stay beneath the repository build directory or RUNNER_TEMP: $outputPath"
}
if (Test-Path -LiteralPath $outputPath) {
    $existing = @(Get-ChildItem -LiteralPath $outputPath -Force -ErrorAction SilentlyContinue)
    if ($existing.Count -gt 0) {
        throw "OutputDirectory must be empty so stale candidates cannot be uploaded: $outputPath"
    }
} else {
    New-Item -ItemType Directory -Force $outputPath | Out-Null
}
$OutputDirectory = (Resolve-Path -LiteralPath $outputPath).Path

$temporaryParent = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$workRoot = Join-Path $temporaryParent ("patris-alm-candidate-" + [guid]::NewGuid().ToString("N"))
if (-not (Test-PathBeneath -Candidate $workRoot -Parent $temporaryParent)) {
    throw "Refusing unsafe ALM candidate work directory: $workRoot"
}
New-Item -ItemType Directory -Force $workRoot | Out-Null

function Invoke-TextCommand {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    $output = & $FilePath @Arguments 2>&1
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "Command failed with exit code ${exitCode}: $FilePath $($Arguments -join ' ')"
    }
    return ($output | Out-String).Trim()
}

function Get-LicenseStatus {
    param([Parameter(Mandatory = $true)][string]$Executable)

    $raw = Invoke-TextCommand -FilePath $Executable -Arguments @("license", "status")
    try {
        return $raw | ConvertFrom-Json
    } catch {
        throw "Patris Export returned invalid license status JSON."
    }
}

function Assert-ExactDatabaseCount {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$Database
    )

    $information = Invoke-TextCommand -FilePath $Executable -Arguments @("info", $Database)
    if ($information -notmatch 'Records:\s*354(?:\D|$)') {
        throw "Patris Export database smoke did not report exactly 354 records."
    }
}

function Assert-DLLInterop {
    param(
        [Parameter(Mandatory = $true)][string]$DLL,
        [Parameter(Mandatory = $true)][string]$Database
    )

    $smokeScript = Join-Path $repoRoot "scripts\windows\smoke_dll.py"
    $raw = Invoke-TextCommand -FilePath $PythonPath -Arguments @($smokeScript, $DLL, $Database)
    try {
        $result = $raw | ConvertFrom-Json
    } catch {
        throw "The Patris Export DLL smoke returned invalid JSON."
    }
    if ([int]$result.abi -ne 1 -or [int]$result.records -ne 354) {
        throw "The Patris Export DLL smoke must report ABI 1 and exactly 354 records."
    }
}

function Assert-LicensedRuntime {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$DLL,
        [Parameter(Mandatory = $true)][string]$Database
    )

    $status = Get-LicenseStatus -Executable $Executable
    if (-not $status.enabled -or -not $status.required -or -not $status.licensed -or
        $status.mode -ne "alm_compat_utf8_v1" -or $status.state -ne "licensed") {
        throw "The ALM runtime did not report a valid required license."
    }
    Assert-ExactDatabaseCount -Executable $Executable -Database $Database
    Assert-DLLInterop -DLL $DLL -Database $Database
}

function Install-EphemeralLicenseAndTest {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$DLL,
        [Parameter(Mandatory = $true)][string]$Database,
        [switch]$RemoveAfterTest
    )

    $licensePath = $env:PATRIS_EXPORT_LICENSE_FILE
    if (-not $licensePath) {
        throw "PATRIS_EXPORT_LICENSE_FILE must point into the isolated smoke directory."
    }
    New-Item -ItemType Directory -Force (Split-Path -Parent $licensePath) | Out-Null
    Remove-Item -LiteralPath $licensePath -Force -ErrorAction SilentlyContinue

    $initial = Get-LicenseStatus -Executable $Executable
    if (-not $initial.enabled -or -not $initial.required -or $initial.licensed -or
        $initial.mode -ne "alm_compat_utf8_v1" -or $initial.state -ne "missing") {
        throw "The unlicensed ALM runtime did not fail closed in the expected missing state."
    }

    $previousErrorPreference = $ErrorActionPreference
    try {
        # Windows PowerShell 5 promotes native stderr to NativeCommandError when
        # ErrorActionPreference is Stop. This command is expected to fail.
        $ErrorActionPreference = "Continue"
        $blockedOutput = & $Executable info $Database 2>&1
        $blockedExitCode = $LASTEXITCODE
        $null = $blockedOutput
    } finally {
        $ErrorActionPreference = $previousErrorPreference
    }
    if ($blockedExitCode -eq 0) {
        throw "The ALM runtime allowed database access before activation."
    }

    $challenge = Invoke-TextCommand -FilePath $Executable -Arguments @("license", "challenge")
    if ($challenge -notmatch '^[0-9A-F]{64}$') {
        throw "The ALM machine challenge was not a 64-character uppercase SHA-256 value."
    }

    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $licenseKey = ([BitConverter]::ToString(
            $hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($challenge + $ALMAppID))
        )).Replace("-", "")
    } finally {
        $hasher.Dispose()
    }
    $activationFile = Join-Path $workRoot ("activation-" + [guid]::NewGuid().ToString("N") + ".tmp")
    try {
        [IO.File]::WriteAllText($activationFile, $licenseKey, [Text.UTF8Encoding]::new($false))
        $null = Invoke-TextCommand -FilePath $Executable -Arguments @("license", "install", "--file", $activationFile)
    } finally {
        Remove-Item -LiteralPath $activationFile -Force -ErrorAction SilentlyContinue
        $licenseKey = $null
        $challenge = $null
    }

    Assert-LicensedRuntime -Executable $Executable -DLL $DLL -Database $Database
    if ($RemoveAfterTest) {
        $null = Invoke-TextCommand -FilePath $Executable -Arguments @("license", "remove")
        $removed = Get-LicenseStatus -Executable $Executable
        if ($removed.licensed -or $removed.state -ne "missing" -or (Test-Path -LiteralPath $licensePath)) {
            throw "The ALM license remove command did not restore the missing state."
        }
    }
}

function Invoke-InstallerProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "$Description failed with exit code $($process.ExitCode)."
    }
}

function Assert-InstalledPayload {
    param([Parameter(Mandatory = $true)][string]$InstallRoot)

    foreach ($required in @(
        "patris-export.exe",
        "patris-export.dll",
        "patris-export.h",
        "libpxlib.dll",
        "libgcc_s_seh-1.dll",
        "libstdc++-6.dll",
        "libwinpthread-1.dll",
        "README.md",
        "INSTALL.md",
        "Install-PatrisExportScheduledTask.ps1",
        "Run-PatrisExportScheduledTask.ps1",
        "BUILD-MANIFEST.txt",
        "BUILD-VARIANT.json",
        "LICENSE.txt",
        "NOTICE.txt",
        "CHANGELOG.md",
        "LICENSING.md",
        "INSTALLER.md",
        "config.example.toml",
        "Uninstall.exe"
    )) {
        if (-not (Test-Path -LiteralPath (Join-Path $InstallRoot $required) -PathType Leaf)) {
            throw "The installed ALM candidate is missing $required."
        }
    }

    $variant = Get-Content -LiteralPath (Join-Path $InstallRoot "BUILD-VARIANT.json") -Raw | ConvertFrom-Json
    if ($variant.variant -ne "alm-compat" -or $variant.licensing_mode -ne "alm_compat_utf8_v1" -or
        -not [bool]$variant.license_required) {
        throw "The installed ALM candidate has incorrect build-variant metadata."
    }
}

function Assert-CleanInstallRoot {
    param([Parameter(Mandatory = $true)][string]$InstallRoot)

    if (Test-Path -LiteralPath $InstallRoot) {
        $remaining = @(Get-ChildItem -LiteralPath $InstallRoot -Force -ErrorAction SilentlyContinue)
        if ($remaining.Count -gt 0) {
            throw "Uninstall left owned files in ${InstallRoot}: $($remaining.Name -join ', ')"
        }
    }
}

$previousLicensePath = $env:PATRIS_EXPORT_LICENSE_FILE
try {
    $payloadDirectory = Join-Path $workRoot "payload"
    New-Item -ItemType Directory -Force $payloadDirectory | Out-Null

    $payloadSources = [ordered]@{
        "patris-export.exe" = Join-Path $BuildDirectory "patris-export-windows-amd64.exe"
        "patris-export.dll" = Join-Path $BuildDirectory "patris-export.dll"
        "patris-export.h" = Join-Path $BuildDirectory "patris-export.h"
        "libpxlib.dll" = Join-Path $BuildDirectory "libpxlib.dll"
        "libgcc_s_seh-1.dll" = Join-Path $BuildDirectory "libgcc_s_seh-1.dll"
        "libstdc++-6.dll" = Join-Path $BuildDirectory "libstdc++-6.dll"
        "libwinpthread-1.dll" = Join-Path $BuildDirectory "libwinpthread-1.dll"
        "BUILD-VARIANT.json" = Join-Path $BuildDirectory "BUILD-VARIANT.json"
        "README.md" = Join-Path $repoRoot "README.md"
        "INSTALL.md" = Join-Path $repoRoot "docs\INSTALL-BINARIES.md"
        "LICENSE" = Join-Path $repoRoot "LICENSE"
        "NOTICE" = Join-Path $repoRoot "NOTICE"
        "LICENSING.md" = Join-Path $repoRoot "docs\LICENSING.md"
        "CHANGELOG.md" = Join-Path $repoRoot "CHANGELOG.md"
        "Install-PatrisExportScheduledTask.ps1" = Join-Path $repoRoot "scripts\windows\Install-PatrisExportScheduledTask.ps1"
        "Run-PatrisExportScheduledTask.ps1" = Join-Path $repoRoot "scripts\windows\Run-PatrisExportScheduledTask.ps1"
    }
    foreach ($destination in $payloadSources.Keys) {
        $source = $payloadSources[$destination]
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "The ALM source build is missing required file: $source"
        }
        Copy-Item -LiteralPath $source -Destination (Join-Path $payloadDirectory $destination) -Force
    }

    $variant = Get-Content -LiteralPath (Join-Path $payloadDirectory "BUILD-VARIANT.json") -Raw | ConvertFrom-Json
    if ($variant.variant -ne "alm-compat" -or $variant.licensing_mode -ne "alm_compat_utf8_v1" -or
        -not [bool]$variant.license_required) {
        throw "The source build is not the required alm-compat variant."
    }

    $pxlibCommit = (Get-Content -LiteralPath (Join-Path $repoRoot "dependencies\pxlib.ref") -Raw).Trim()
    @(
        "Patris Export $Version",
        "Build variant: alm-compat",
        "Licensing mode: alm_compat_utf8_v1",
        "ALM application identifier: $ALMAppID (public, not a secret)",
        "Source commit: $SourceCommit",
        "pxlib source commit: $pxlibCommit",
        "Build date: $SourceBuildDate",
        "Built by: .github/workflows/alm-build.yml and scripts/windows/Build-ALMCandidate.ps1"
    ) | Set-Content -LiteralPath (Join-Path $payloadDirectory "BUILD-MANIFEST.txt") -Encoding utf8

    $payloadLicenseRoot = Join-Path $workRoot "payload-license"
    $env:PATRIS_EXPORT_LICENSE_FILE = Join-Path $payloadLicenseRoot "license.key"
    Install-EphemeralLicenseAndTest `
        -Executable (Join-Path $payloadDirectory "patris-export.exe") `
        -DLL (Join-Path $payloadDirectory "patris-export.dll") `
        -Database $DatabasePath `
        -RemoveAfterTest

    $installerScript = Join-Path $repoRoot "scripts\windows\Build-Installer.ps1"
    $installerParameters = @{
        PayloadDirectory = $payloadDirectory
        OutputDirectory = $OutputDirectory
        Version = $Version
        ArtifactLabel = $ArtifactLabel
        SourceCommit = $SourceCommit
    }
    if ($MakensisPath) { $installerParameters.MakensisPath = $MakensisPath }
    & $installerScript @installerParameters | Out-Host

    $candidateName = "patris-export-$ArtifactLabel-windows-amd64-setup.exe"
    $candidateInstaller = Join-Path $OutputDirectory $candidateName
    if (-not (Test-Path -LiteralPath $candidateInstaller -PathType Leaf)) {
        throw "The assisted ALM candidate installer was not produced: $candidateName"
    }
    $candidateSignature = Get-AuthenticodeSignature -LiteralPath $candidateInstaller
    if ($candidateSignature.Status -ne "NotSigned") {
        throw "The optional ALM workflow accepts only an explicitly unsigned candidate; status was $($candidateSignature.Status). Add a reviewed signing stage before changing this policy."
    }

    $installerSandbox = Join-Path $workRoot "installer-lifecycle"
    $installRoot = Join-Path $installerSandbox "installed"
    $configRoot = Join-Path $installerSandbox "config"
    $configSentinel = Join-Path $configRoot "preserve-on-normal-uninstall.txt"
    $parentSentinel = Join-Path $installerSandbox "preserve-parent.txt"
    New-Item -ItemType Directory -Force $configRoot | Out-Null
    Set-Content -LiteralPath $configSentinel -Value "preserve on normal uninstall"
    Set-Content -LiteralPath $parentSentinel -Value "never purge unrelated parent data"
    $env:PATRIS_EXPORT_LICENSE_FILE = Join-Path $configRoot "license.key"

    if (-not $SkipAssistedInstallSmoke) {
        Invoke-InstallerProcess -FilePath $candidateInstaller `
            -Arguments @("/S", "/CurrentUser", "/D=$installRoot") `
            -Description "Full assisted ALM candidate installation"
        Assert-InstalledPayload -InstallRoot $installRoot
        Install-EphemeralLicenseAndTest `
            -Executable (Join-Path $installRoot "patris-export.exe") `
            -DLL (Join-Path $installRoot "patris-export.dll") `
            -Database $DatabasePath

        Invoke-InstallerProcess -FilePath (Join-Path $installRoot "Uninstall.exe") `
            -Arguments @("/S", "/CurrentUser") `
            -Description "Normal ALM candidate uninstall"
        Assert-CleanInstallRoot -InstallRoot $installRoot
        if (-not (Test-Path -LiteralPath $configSentinel) -or
            -not (Test-Path -LiteralPath $env:PATRIS_EXPORT_LICENSE_FILE)) {
            throw "Normal uninstall removed settings or license data that must be preserved."
        }
    }

    $smokeParameters = $installerParameters.Clone()
    $smokeParameters.CurrentUserOnly = $true
    $smokeParameters.PurgeDataRoot = $configRoot
    & $installerScript @smokeParameters | Out-Host
    $smokeName = "patris-export-$ArtifactLabel-current-user-smoke-windows-amd64-setup.exe"
    $smokeInstaller = Join-Path $OutputDirectory $smokeName
    if (-not (Test-Path -LiteralPath $smokeInstaller -PathType Leaf)) {
        throw "The isolated current-user purge smoke installer was not produced."
    }

    Invoke-InstallerProcess -FilePath $smokeInstaller `
        -Arguments @("/S", "/D=$installRoot") `
        -Description "Isolated current-user ALM smoke installation"
    Assert-InstalledPayload -InstallRoot $installRoot
    if ($SkipAssistedInstallSmoke) {
        Install-EphemeralLicenseAndTest `
            -Executable (Join-Path $installRoot "patris-export.exe") `
            -DLL (Join-Path $installRoot "patris-export.dll") `
            -Database $DatabasePath
        Invoke-InstallerProcess -FilePath (Join-Path $installRoot "Uninstall.exe") `
            -Arguments @("/S") `
            -Description "Normal isolated ALM smoke uninstall"
        Assert-CleanInstallRoot -InstallRoot $installRoot
        if (-not (Test-Path -LiteralPath $configSentinel) -or
            -not (Test-Path -LiteralPath $env:PATRIS_EXPORT_LICENSE_FILE)) {
            throw "Normal uninstall removed settings or license data that must be preserved."
        }
        Invoke-InstallerProcess -FilePath $smokeInstaller `
            -Arguments @("/S", "/D=$installRoot") `
            -Description "Second isolated current-user ALM smoke installation"
        Assert-InstalledPayload -InstallRoot $installRoot
    }
    Assert-LicensedRuntime `
            -Executable (Join-Path $installRoot "patris-export.exe") `
            -DLL (Join-Path $installRoot "patris-export.dll") `
            -Database $DatabasePath
    Invoke-InstallerProcess -FilePath (Join-Path $installRoot "Uninstall.exe") `
        -Arguments @("/S", "/PURGEDATA") `
        -Description "Explicit isolated ALM purge uninstall"
    Assert-CleanInstallRoot -InstallRoot $installRoot
    if (Test-Path -LiteralPath $configRoot) {
        throw "Explicit /PURGEDATA uninstall did not remove the isolated configuration root."
    }
    if (-not (Test-Path -LiteralPath $parentSentinel)) {
        throw "Explicit /PURGEDATA uninstall removed unrelated parent data."
    }
    Remove-Item -LiteralPath $smokeInstaller -Force

    Copy-Item -LiteralPath (Join-Path $payloadDirectory "BUILD-VARIANT.json") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-BUILD-VARIANT.json") -Force
    Copy-Item -LiteralPath (Join-Path $payloadDirectory "BUILD-MANIFEST.txt") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-PAYLOAD-MANIFEST.txt") -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot "docs\LICENSING.md") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-LICENSING.md") -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot "CHANGELOG.md") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-CHANGELOG.md") -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot "LICENSE") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-LICENSE.txt") -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot "NOTICE") `
        -Destination (Join-Path $OutputDirectory "PATRIS-ALM-NOTICE.txt") -Force

    @(
        "Patris Export optional ALM compatibility candidate",
        "Version: $Version",
        "Source commit: $SourceCommit",
        "Source build date: $SourceBuildDate",
        "Variant: alm-compat",
        "Licensing mode: alm_compat_utf8_v1",
        "ALM application identifier: $ALMAppID (public, not a secret)",
        "Installer: $candidateName",
        "Authenticode status: NotSigned (required for this candidate-only workflow)",
        "Verified: missing-license fail-closed, challenge, ephemeral activation, exact 354-record executable query, DLL ABI 1 exact 354-record query, full assisted current-user installation, configuration-preserving normal uninstall, isolated /PURGEDATA uninstall",
        "Ephemeral license keys, machine challenges, hardware identifiers, and smoke directories are neither retained nor uploaded."
    ) | Set-Content -LiteralPath (Join-Path $OutputDirectory "PATRIS-ALM-BUILD.txt") -Encoding utf8

    $checksumPath = Join-Path $OutputDirectory "PATRIS-ALM-SHA256SUMS"
    Get-ChildItem -LiteralPath $OutputDirectory -File |
        Where-Object { $_.Name -ne "PATRIS-ALM-SHA256SUMS" } |
        Sort-Object Name |
        ForEach-Object {
            $digest = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            "$digest  $($_.Name)"
        } | Set-Content -LiteralPath $checksumPath -Encoding ascii

    foreach ($line in Get-Content -LiteralPath $checksumPath) {
        if ($line -notmatch '^([0-9a-f]{64})  (.+)$') {
            throw "Malformed ALM checksum line: $line"
        }
        $asset = Join-Path $OutputDirectory $matches[2]
        $actual = (Get-FileHash -LiteralPath $asset -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $matches[1]) {
            throw "ALM checksum mismatch for $($matches[2])."
        }
    }

    $unexpected = @(Get-ChildItem -LiteralPath $OutputDirectory -File |
        Where-Object { $_.Name -match '(?i)(^|[-_.])(?:key|challenge|license\.key)(?:$|[-_.])' })
    if ($unexpected.Count -gt 0) {
        throw "Sensitive smoke material must not be present in candidate output: $($unexpected.Name -join ', ')"
    }

    Write-Host "Verified optional ALM candidate: $candidateInstaller"
    Write-Host "Authenticode status: NotSigned"
    Write-Host "Candidate metadata and checksums: $OutputDirectory"
} finally {
    if ($null -eq $previousLicensePath) {
        Remove-Item Env:PATRIS_EXPORT_LICENSE_FILE -ErrorAction SilentlyContinue
    } else {
        $env:PATRIS_EXPORT_LICENSE_FILE = $previousLicensePath
    }
    if (Test-Path -LiteralPath $workRoot) {
        if (-not (Test-PathBeneath -Candidate $workRoot -Parent $temporaryParent)) {
            throw "Refusing unsafe ALM smoke cleanup: $workRoot"
        }
        Remove-Item -LiteralPath $workRoot -Recurse -Force
    }
}
