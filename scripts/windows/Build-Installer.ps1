[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PayloadDirectory,

    [string]$OutputDirectory,
    [string]$Version,
    [string]$ArtifactLabel,
    [string]$SourceCommit,
    [string]$MakensisPath,

    [switch]$CurrentUserOnly,

    [string]$PurgeDataRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-Makensis {
    param([string]$ExplicitPath)

    $candidates = [System.Collections.Generic.List[string]]::new()
    if ($ExplicitPath) { $candidates.Add($ExplicitPath) }
    if ($env:MAKENSIS_PATH) { $candidates.Add($env:MAKENSIS_PATH) }

    $command = Get-Command "makensis.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { $candidates.Add($command.Source) }

    if ($env:LOCALAPPDATA) {
        $cacheRoot = Join-Path $env:LOCALAPPDATA "electron-builder\Cache\nsis"
        if (Test-Path -LiteralPath $cacheRoot) {
            Get-ChildItem -LiteralPath $cacheRoot -Recurse -Filter "makensis.exe" -ErrorAction SilentlyContinue |
                Sort-Object LastWriteTimeUtc -Descending |
                ForEach-Object { $candidates.Add($_.FullName) }
        }
    }

    foreach ($programRoot in @(${env:ProgramFiles(x86)}, $env:ProgramFiles)) {
        if ($programRoot) { $candidates.Add((Join-Path $programRoot "NSIS\makensis.exe")) }
    }

    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    throw "makensis.exe was not found. Install NSIS, put it on PATH, set MAKENSIS_PATH, or build once with electron-builder so its cached NSIS runtime is available."
}

function Resolve-ProductVersion {
    param([string]$RequestedVersion, [string]$RepositoryRoot)

    if ($RequestedVersion) { return $RequestedVersion }
    $source = Get-Content -LiteralPath (Join-Path $RepositoryRoot "pkg\version\version.go") -Raw
    $match = [regex]::Match($source, 'Version\s*=\s*"([^"]+)"')
    if (-not $match.Success) {
        throw "Unable to read the source version from pkg\version\version.go."
    }
    return $match.Groups[1].Value
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$PayloadDirectory = (Resolve-Path -LiteralPath $PayloadDirectory).Path
if (-not $OutputDirectory) {
    $OutputDirectory = Join-Path $repoRoot "build\installer"
}
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path

if ($PurgeDataRoot) {
    if (-not $CurrentUserOnly) {
        throw "PurgeDataRoot is a smoke-test option and requires CurrentUserOnly."
    }
    $PurgeDataRoot = [System.IO.Path]::GetFullPath($PurgeDataRoot)
    $allowedRoots = @((Join-Path $repoRoot "build"), $env:RUNNER_TEMP, $env:TEMP) |
        Where-Object { $_ } |
        ForEach-Object { [System.IO.Path]::GetFullPath($_).TrimEnd('\') + '\' }
    $allowed = $false
    foreach ($root in $allowedRoots) {
        if ($PurgeDataRoot.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
            $allowed = $true
            break
        }
    }
    if (-not $allowed) {
        throw "PurgeDataRoot must stay beneath the repository build directory or TEMP: $PurgeDataRoot"
    }
}

$Version = Resolve-ProductVersion -RequestedVersion $Version -RepositoryRoot $repoRoot
if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$') {
    throw "Installer versions must begin with semantic version X.Y.Z: $Version"
}
$versionParts = @([int]$matches[1], [int]$matches[2], [int]$matches[3], 0)
if ($versionParts | Where-Object { $_ -lt 0 -or $_ -gt 65535 }) {
    throw "Each Windows version component must be between 0 and 65535: $Version"
}
$versionQuad = $versionParts -join "."

if (-not $ArtifactLabel) { $ArtifactLabel = "v$Version" }
if ($ArtifactLabel -notmatch '^[0-9A-Za-z][0-9A-Za-z._-]*$') {
    throw "ArtifactLabel contains unsupported filename characters: $ArtifactLabel"
}
if (-not $SourceCommit) {
    $SourceCommit = (& git -C $repoRoot rev-parse --short=12 HEAD).Trim()
}
if (-not $SourceCommit) { $SourceCommit = "unknown" }

$requiredPayload = @(
    "patris-export.exe",
    "patris-export.dll",
    "patris-export.h",
    "libpxlib.dll",
    "libgcc_s_seh-1.dll",
    "libstdc++-6.dll",
    "libwinpthread-1.dll",
    "README.md",
    "INSTALL.md",
    "BUILD-MANIFEST.txt"
)
foreach ($file in $requiredPayload) {
    if (-not (Test-Path -LiteralPath (Join-Path $PayloadDirectory $file) -PathType Leaf)) {
        throw "Windows installer payload is missing required file: $file"
    }
}

$buildVariant = "standard"
$variantManifestPath = Join-Path $PayloadDirectory "BUILD-VARIANT.json"
if (Test-Path -LiteralPath $variantManifestPath -PathType Leaf) {
    try {
        $variantManifest = Get-Content -LiteralPath $variantManifestPath -Raw | ConvertFrom-Json
    } catch {
        throw "BUILD-VARIANT.json is not valid JSON: $($_.Exception.Message)"
    }
    if (-not $variantManifest.variant -or $variantManifest.variant -notmatch '^[0-9A-Za-z][0-9A-Za-z._-]*$') {
        throw "BUILD-VARIANT.json contains an invalid or missing variant."
    }
    $buildVariant = [string]$variantManifest.variant
    if ($buildVariant -ne "standard" -and $ArtifactLabel -notmatch "-$([regex]::Escape($buildVariant))$") {
        $ArtifactLabel = "$ArtifactLabel-$buildVariant"
    }
}
if ($CurrentUserOnly -and $ArtifactLabel -notmatch '-current-user-smoke$') {
    $ArtifactLabel = "$ArtifactLabel-current-user-smoke"
}

$makensis = Resolve-Makensis -ExplicitPath $MakensisPath
$assetDirectory = Join-Path $repoRoot "build\installer-assets\$ArtifactLabel"
& (Join-Path $PSScriptRoot "New-InstallerAssets.ps1") -OutputDirectory $assetDirectory | Out-Host
if (-not $?) {
    throw "Installer artwork generation failed."
}

$makensisDirectory = Split-Path -Parent $makensis
$nsisRoots = @($makensisDirectory, (Split-Path -Parent $makensisDirectory)) | Select-Object -Unique
$nsisRoot = $nsisRoots |
    Where-Object { Test-Path -LiteralPath (Join-Path $_ "Contrib\Language files\Farsi.nlf") -PathType Leaf } |
    Select-Object -First 1
if (-not $nsisRoot) {
    throw "The selected NSIS installation does not contain the Farsi language resources."
}
$languageDirectory = Join-Path $assetDirectory "languages"
New-Item -ItemType Directory -Force $languageDirectory | Out-Null
Copy-Item -LiteralPath (Join-Path $nsisRoot "Contrib\Language files\Farsi.nlf") -Destination $languageDirectory -Force
Copy-Item -LiteralPath (Join-Path $repoRoot "installer\windows\languages\Farsi.nsh") -Destination $languageDirectory -Force

$installerFilename = "patris-export-$ArtifactLabel-windows-amd64-setup.exe"
$scriptPath = Join-Path $repoRoot "installer\windows\patris-export.nsi"
$arguments = @(
    "/V3",
    "/NOCD",
    "/INPUTCHARSET",
    "UTF8",
    "/DPRODUCT_VERSION=$Version",
    "/DPRODUCT_VERSION_QUAD=$versionQuad",
    "/DARTIFACT_LABEL=$ArtifactLabel",
    "/DINSTALLER_FILENAME=$installerFilename",
    "/DSOURCE_COMMIT=$SourceCommit",
    "/DPAYLOAD_DIR=$PayloadDirectory",
    "/DOUTPUT_DIR=$OutputDirectory",
    "/DASSET_DIR=$assetDirectory",
    "/DLICENSE_FILE=$(Join-Path $repoRoot 'LICENSE')",
    "/DNOTICE_FILE=$(Join-Path $repoRoot 'NOTICE')",
    "/DCHANGELOG_FILE=$(Join-Path $repoRoot 'CHANGELOG.md')",
    "/DLICENSING_GUIDE=$(Join-Path $repoRoot 'docs\LICENSING.md')",
    "/DINSTALLER_GUIDE=$(Join-Path $repoRoot 'docs\WINDOWS_INSTALLER.md')",
    "/DCONFIG_EXAMPLE=$(Join-Path $repoRoot 'installer\windows\config.example.toml')",
    "/DLANGUAGE_DIR=$languageDirectory",
    "/DICON_FILE=$(Join-Path $repoRoot 'assets\windows\patris-api.ico')"
)
if ($CurrentUserOnly) {
    $arguments += "/DCURRENT_USER_ONLY=1"
}
if ($PurgeDataRoot) {
    $arguments += "/DPURGE_DATA_ROOT=$PurgeDataRoot"
}
$arguments += $scriptPath

Write-Host "Using makensis: $makensis"
Write-Host "Building installer: $installerFilename"
& $makensis @arguments
if ($LASTEXITCODE -ne 0) {
    throw "makensis failed with exit code $LASTEXITCODE."
}

$installerPath = Join-Path $OutputDirectory $installerFilename
if (-not (Test-Path -LiteralPath $installerPath -PathType Leaf)) {
    throw "makensis completed without producing $installerPath"
}
$hash = Get-FileHash -LiteralPath $installerPath -Algorithm SHA256
[pscustomobject]@{
    Installer = $installerPath
    Version = $Version
    ArtifactLabel = $ArtifactLabel
    BuildVariant = $buildVariant
    ExecutionScope = $(if ($CurrentUserOnly) { "current-user smoke" } else { "assisted current/all users" })
    SHA256 = $hash.Hash.ToLowerInvariant()
    Size = (Get-Item -LiteralPath $installerPath).Length
}
