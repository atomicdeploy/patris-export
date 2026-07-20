[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Command,
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$PxlibRoot = $env:PXLIB_ROOT,
    [string]$WinLibsBin,
    [ValidateSet("dynamic", "cgo", "cgo-static")]
    [string]$PxlibBackend = $(if ($env:PATRIS_EXPORT_PXLIB_BACKEND) { $env:PATRIS_EXPORT_PXLIB_BACKEND } else { "dynamic" })
)

$ErrorActionPreference = "Stop"

function Resolve-ExistingPath {
    param(
        [string]$Description,
        [string[]]$Candidates
    )

    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }
    throw "$Description not found. Checked: $($Candidates -join '; ')"
}

function Resolve-ToolPath {
    param(
        [string]$Name,
        [string[]]$Candidates = @()
    )

    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }

    $tool = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($tool) {
        return $tool.Source
    }

    throw "Required tool not found: $Name"
}

if (-not $PxlibRoot) {
    $PxlibRoot = Join-Path $AtomicDeployRoot "deps\pxlib-install-windows"
}
$PxlibRoot = Resolve-ExistingPath "pxlib root" @($PxlibRoot)
$PxlibBackend = $PxlibBackend.ToLowerInvariant()

if (-not (Test-Path (Join-Path $PxlibRoot "include\paradox.h"))) {
    throw "pxlib header not found: $(Join-Path $PxlibRoot "include\paradox.h")"
}
if ($PxlibBackend -eq "cgo" -and -not (Test-Path (Join-Path $PxlibRoot "lib\libpxlib.dll.a"))) {
    throw "pxlib import library not found: $(Join-Path $PxlibRoot "lib\libpxlib.dll.a")"
}
if ($PxlibBackend -ne "cgo-static" -and -not (Test-Path (Join-Path $PxlibRoot "bin\libpxlib.dll"))) {
    throw "pxlib runtime DLL not found: $(Join-Path $PxlibRoot "bin\libpxlib.dll")"
}
if ($PxlibBackend -eq "cgo-static" -and -not (Test-Path (Join-Path $PxlibRoot "lib\libpxlib_static.a"))) {
    throw "The cgo-static backend requires $(Join-Path $PxlibRoot 'lib\libpxlib_static.a')."
}

if (-not $WinLibsBin) {
    $WinLibsBin = Resolve-ExistingPath "WinLibs MinGW bin" @(
        "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin",
        "C:\msys64\mingw64\bin"
    )
} else {
    $WinLibsBin = Resolve-ExistingPath "WinLibs MinGW bin" @($WinLibsBin)
}

$go = Resolve-ToolPath "go.exe" @("C:\Program Files\Go\bin\go.exe")
$gcc = Resolve-ToolPath "gcc.exe" @((Join-Path $WinLibsBin "gcc.exe"))

$env:CGO_ENABLED = "1"
$env:CC = $gcc
$env:PXLIB_ROOT = $PxlibRoot
$env:CGO_CFLAGS = "-I$PxlibRoot\include"
$env:CGO_LDFLAGS = "-L$PxlibRoot\lib -L$PxlibRoot\bin"
$env:PATH = "$(Join-Path $PxlibRoot "bin");$WinLibsBin;$(Split-Path -Parent $go);$env:PATH"
switch ($PxlibBackend) {
    "cgo" { $env:GOFLAGS = "$($env:GOFLAGS) -tags=pxlib_cgo".Trim() }
    "cgo-static" { $env:GOFLAGS = "$($env:GOFLAGS) -tags=pxlib_cgo,pxlib_cgo_static".Trim() }
}

if (-not $Command -or $Command.Count -eq 0) {
    $Command = @("go", "test", "./...")
}

if ($Command[0] -ieq "go" -or $Command[0] -ieq "go.exe") {
    $exe = $go
    $args = @($Command | Select-Object -Skip 1)
} else {
    $resolved = Get-Command $Command[0] -ErrorAction SilentlyContinue | Select-Object -First 1
    $exe = if ($resolved) { $resolved.Source } else { $Command[0] }
    $args = @($Command | Select-Object -Skip 1)
}

Write-Host "CGO_ENABLED=$env:CGO_ENABLED"
Write-Host "CC=$env:CC"
Write-Host "PXLIB_ROOT=$env:PXLIB_ROOT"
Write-Host "CGO_CFLAGS=$env:CGO_CFLAGS"
Write-Host "CGO_LDFLAGS=$env:CGO_LDFLAGS"
Write-Host "PXLIB_BACKEND=$PxlibBackend"
Write-Host "GOFLAGS=$env:GOFLAGS"
Write-Host "PATH prepended: $(Join-Path $PxlibRoot "bin");$WinLibsBin;$(Split-Path -Parent $go)"
Write-Host "Running: $exe $($args -join ' ')"

& $exe @args
exit $LASTEXITCODE
