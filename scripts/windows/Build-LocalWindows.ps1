param(
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$PxlibRepo = $(if ($env:PXLIB_REPO) { $env:PXLIB_REPO } else { "https://github.com/steinm/pxlib.git" }),
    [string]$PxlibRef = $env:PXLIB_REF,
    [string]$PatrisDb = "C:\Patris\data4\kala.db",
    [string]$Version = $env:VERSION,
    [ValidateSet("dynamic", "cgo", "cgo-static")]
    [string]$PxlibBackend = $(if ($env:PATRIS_EXPORT_PXLIB_BACKEND) { $env:PATRIS_EXPORT_PXLIB_BACKEND } else { "dynamic" }),
    [switch]$SkipPxlibBuild,
    [switch]$SkipPromote
)

$ErrorActionPreference = "Stop"

function Resolve-Tool {
    param(
        [string]$Name,
        [string[]]$Candidates = @()
    )

    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }

    $found = (Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1)
    if ($found) {
        return $found.Source
    }

    throw "Required tool not found: $Name"
}

function Invoke-Checked {
    param(
        [string]$FilePath,
        [string[]]$Arguments = @()
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed with exit code $LASTEXITCODE`: $FilePath $($Arguments -join ' ')"
    }
}

function Convert-ToMsysPath {
    param([string]$Path)

    $full = [System.IO.Path]::GetFullPath($Path)
    if ($full -match '^([A-Za-z]):\\(.*)$') {
        return "/$($matches[1].ToLower())/" + ($matches[2] -replace '\\', '/')
    }
    return ($full -replace '\\', '/')
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$pinnedPxlibRef = (Get-Content (Join-Path $repoRoot "dependencies\pxlib.ref") -Raw).Trim()
if (-not $PxlibRef) {
    $PxlibRef = $pinnedPxlibRef
}
$depsRoot = Join-Path $AtomicDeployRoot "deps"
$pxlibSource = Join-Path $depsRoot "pxlib"
$pxlibBuild = Join-Path $depsRoot "pxlib-build-windows"
$pxlibInstall = Join-Path $depsRoot "pxlib-install-windows"
$almEnabled = $env:ENABLE_ALM -match '^(1|true|yes|on)$'
$almAppID = $env:ALM_APP_ID
if ($almEnabled -and -not $almAppID) {
    throw "ENABLE_ALM=1 requires ALM_APP_ID."
}
if ($almEnabled -and $almAppID -notmatch '^[A-Za-z0-9._:@+\-]{6,128}$') {
    throw "ALM_APP_ID must be 6-128 characters from A-Z, a-z, 0-9, '.', '_', ':', '@', '+', or '-'."
}
$buildVariant = if ($almEnabled) { "alm-compat" } else { "standard" }
$licensingMode = if ($almEnabled) { "alm_compat_utf8_v1" } else { "none" }
$PxlibBackend = $PxlibBackend.ToLowerInvariant()
$baseDeployRoot = if ($almEnabled) { Join-Path $AtomicDeployRoot "deploy\alm-compat" } else { Join-Path $AtomicDeployRoot "deploy" }
$deployRoot = if ($PxlibBackend -eq "dynamic") { $baseDeployRoot } else { Join-Path $baseDeployRoot $PxlibBackend }
$stageParent = Join-Path $AtomicDeployRoot "build"
$stageName = if ($almEnabled) { "patris-export-windows-amd64-alm-compat" } else { "patris-export-windows-amd64" }
if ($PxlibBackend -ne "dynamic") {
    $stageName += "-$PxlibBackend"
}
$stageRoot = Join-Path $stageParent $stageName
$vcpkgRoot = $(if ($env:VCPKG_ROOT) { $env:VCPKG_ROOT } else { Join-Path $depsRoot "vcpkg" })
$vcpkgTriplet = $(if ($env:VCPKG_DEFAULT_TRIPLET) { $env:VCPKG_DEFAULT_TRIPLET } else { "x64-windows" })
$useVcpkg = $env:USE_VCPKG -match '^(1|true|yes|on)$'

if (-not $Version) {
    $versionSource = Get-Content (Join-Path $repoRoot "pkg\version\version.go") -Raw
    $versionMatch = [regex]::Match($versionSource, 'Version\s*=\s*"([^"]+)"')
    if (-not $versionMatch.Success) {
        throw "Unable to read the source version from pkg\version\version.go."
    }
    $Version = $versionMatch.Groups[1].Value
}
if ($Version -notmatch '^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9._-]+)?$') {
    throw "Invalid version '$Version'."
}

New-Item -ItemType Directory -Force $depsRoot, $deployRoot, $stageParent | Out-Null
$resolvedStageParent = [System.IO.Path]::GetFullPath($stageParent).TrimEnd('\') + '\'
$resolvedStageRoot = [System.IO.Path]::GetFullPath($stageRoot)
if (-not $resolvedStageRoot.StartsWith($resolvedStageParent, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to clean an unsafe Windows staging path: $resolvedStageRoot"
}
if (Test-Path -LiteralPath $resolvedStageRoot) {
    Remove-Item -LiteralPath $resolvedStageRoot -Recurse -Force
}
New-Item -ItemType Directory -Force $resolvedStageRoot | Out-Null

$winLibsBin = "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin"
$goBin = "C:\Program Files\Go\bin"
$env:PATH = "$winLibsBin;$goBin;$env:PATH"

$git = Resolve-Tool "git.exe"
$bash = Resolve-Tool "bash.exe" @("C:\Program Files\Git\usr\bin\bash.exe")
$go = Resolve-Tool "go.exe" @("$goBin\go.exe")
$cmake = Resolve-Tool "cmake.exe" @("$winLibsBin\cmake.exe")
$gcc = Resolve-Tool "gcc.exe" @("$winLibsBin\gcc.exe")
$ar = Resolve-Tool "ar.exe" @("$winLibsBin\ar.exe")
$windres = Resolve-Tool "windres.exe" @("$winLibsBin\windres.exe")

Write-Host "Using Go: $(& $go version)"
Write-Host "Using GCC: $(& $gcc --version | Select-Object -First 1)"
Write-Host "Using CMake: $(& $cmake --version | Select-Object -First 1)"

if (-not $SkipPxlibBuild) {
    if (-not (Test-Path $pxlibSource)) {
        Invoke-Checked $git @("clone", $PxlibRepo, $pxlibSource)
    }

    Invoke-Checked $git @("-C", $pxlibSource, "fetch", "--tags", "origin")
    if ($PxlibRef) {
        Invoke-Checked $git @("-C", $pxlibSource, "checkout", $PxlibRef)
    } else {
        $defaultRef = (& $git -C $pxlibSource symbolic-ref --short refs/remotes/origin/HEAD).Trim()
        $defaultBranch = $defaultRef -replace '^origin/', ''
        Invoke-Checked $git @("-C", $pxlibSource, "checkout", "-B", "atomicdeploy-build", $defaultRef)
        Invoke-Checked $git @("-C", $pxlibSource, "pull", "--ff-only", "origin", $defaultBranch)
    }
    Invoke-Checked $git @("-C", $pxlibSource, "reset", "--hard", "HEAD")

    $paradoxC = Join-Path $pxlibSource "src\paradox.c"
    if (Test-Path $paradoxC) {
        $content = Get-Content $paradoxC -Raw
        $content = $content -replace '#include <Windows\.h>', '#include <windows.h>'
        $content = $content -replace '#include <Winbase\.h>', '#include <winbase.h>'
        Set-Content -Path $paradoxC -Value $content -NoNewline
    }

    $cmakeLists = Join-Path $pxlibSource "CMakeLists.txt"
    $cmakeContent = Get-Content $cmakeLists -Raw
    $oldDefinitions = 'add_definitions\(\s*-DHAVE_CONFIG_H\s*-Wall -Wpointer-arith -W\s*\$\{PXLIB_EXTRA_GCC_FLAGS\}\s*\)'
    $cmakeContent = [regex]::Replace(
        $cmakeContent,
        $oldDefinitions,
        "add_definitions(-DHAVE_CONFIG_H)`n    set(PXLIB_WARNING_FLAGS -Wall -Wpointer-arith -W `${PXLIB_EXTRA_GCC_FLAGS})"
    )
    if ($cmakeContent -notmatch "PATCHED: Apply warning flags only to C/C\+\+ files") {
        $cmakeContent = $cmakeContent -replace "add_library\(pxlib SHARED \`\$\{SOURCES\}\)", @"
add_library(pxlib SHARED `${SOURCES})

# PATCHED: Apply warning flags only to C/C++ files, not RC files.
if(CMAKE_COMPILER_IS_GNUCC)
    target_compile_options(pxlib PRIVATE `$<`$<COMPILE_LANGUAGE:C>:`${PXLIB_WARNING_FLAGS}>)
endif()
"@
    }
    Set-Content -Path $cmakeLists -Value $cmakeContent -NoNewline

    if (Test-Path $pxlibBuild) {
        Remove-Item -Recurse -Force $pxlibBuild
    }
    New-Item -ItemType Directory -Force $pxlibBuild, $pxlibInstall | Out-Null

    Invoke-Checked $cmake @(
        "-S", $pxlibSource,
        "-B", $pxlibBuild,
        "-G", "MinGW Makefiles",
        "-DCMAKE_BUILD_TYPE=Release",
        "-DENABLE_GSF=OFF",
        "-DCMAKE_INSTALL_PREFIX=$pxlibInstall"
    )
    Invoke-Checked $cmake @("--build", $pxlibBuild, "--config", "Release", "--parallel")
    Invoke-Checked $cmake @("--install", $pxlibBuild, "--prefix", $pxlibInstall)

    New-Item -ItemType Directory -Force (Join-Path $pxlibInstall "include"), (Join-Path $pxlibInstall "lib"), (Join-Path $pxlibInstall "bin") | Out-Null
    if (-not (Test-Path (Join-Path $pxlibInstall "include\paradox.h"))) {
        Copy-Item (Join-Path $pxlibSource "include\*.h") (Join-Path $pxlibInstall "include") -ErrorAction SilentlyContinue
        Copy-Item (Join-Path $pxlibBuild "include\*.h") (Join-Path $pxlibInstall "include") -ErrorAction SilentlyContinue
    }
    Copy-Item (Join-Path $pxlibBuild "*.a") (Join-Path $pxlibInstall "lib") -ErrorAction SilentlyContinue
    Copy-Item (Join-Path $pxlibBuild "*.dll.a") (Join-Path $pxlibInstall "lib") -ErrorAction SilentlyContinue
    Copy-Item (Join-Path $pxlibBuild "*.dll") (Join-Path $pxlibInstall "bin") -ErrorAction SilentlyContinue
    $pxlibObjectFiles = @(
        Get-ChildItem (Join-Path $pxlibBuild "CMakeFiles\pxlib.dir") -Recurse -File |
            Where-Object { $_.Extension -in @(".o", ".obj") } |
            Sort-Object FullName |
            ForEach-Object { $_.FullName }
    )
    if ($pxlibObjectFiles.Count -eq 0) {
        throw "pxlib build did not produce object files for the static backend."
    }
    $staticArchive = Join-Path $pxlibInstall "lib\libpxlib_static.a"
    Remove-Item $staticArchive -Force -ErrorAction SilentlyContinue
    Invoke-Checked $ar (@("rcsD", $staticArchive) + $pxlibObjectFiles)
}

if (-not (Test-Path (Join-Path $pxlibInstall "include\paradox.h"))) {
    if ($env:PXLIB_ROOT -and (Test-Path (Join-Path $env:PXLIB_ROOT "include\paradox.h"))) {
        $pxlibInstall = $env:PXLIB_ROOT
        Write-Warning "Using system pxlib from PXLIB_ROOT=$pxlibInstall"
    } elseif ($useVcpkg -and (Test-Path (Join-Path $vcpkgRoot "installed\$vcpkgTriplet\include\paradox.h"))) {
        $pxlibInstall = Join-Path $vcpkgRoot "installed\$vcpkgTriplet"
        Write-Warning "Using pxlib from vcpkg: $pxlibInstall"
    } else {
        throw "pxlib build did not produce include\paradox.h and PXLIB_ROOT is not set."
    }
}
if ($PxlibBackend -eq "cgo-static" -and -not (Test-Path (Join-Path $pxlibInstall "lib\libpxlib_static.a"))) {
    throw "The cgo-static backend requires lib\libpxlib_static.a. Re-run without -SkipPxlibBuild or provide a complete PXLIB_ROOT."
}

Push-Location $repoRoot
try {
    & (Join-Path $PSScriptRoot "Rebuild-Assets.ps1")

    Push-Location web
    try {
        cmd /c npm ci
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
        cmd /c npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    } finally {
        Pop-Location
    }

    $buildDate = (& $git -C $repoRoot show -s --format=%cI HEAD).Trim()
    $commit = (& $git -C $repoRoot rev-parse --short=12 HEAD).Trim()
    $versionPkg = "github.com/atomicdeploy/patris-export/pkg/version"
    $licensingPkg = "github.com/atomicdeploy/patris-export/pkg/licensing"
    $env:VERSION = $Version
    $env:BUILD_DATE = $buildDate
    $env:COMMIT = $commit

    Invoke-Checked $bash @("-lc", "cd '$(Convert-ToMsysPath $repoRoot)' && ./scripts/generate-version-rc.sh cmd/patris-export/patris-export.rc")
    Invoke-Checked $windres @("--target=pe-x86-64", "-i", "cmd/patris-export/patris-export.rc", "-o", "cmd/patris-export/patris-export_windows_amd64.syso", "-O", "coff")

    $env:CGO_ENABLED = "1"
    $env:CC = $gcc
    $env:CGO_CFLAGS = "-I$pxlibInstall\include"
    $env:CGO_LDFLAGS = "-L$pxlibInstall\lib -L$pxlibInstall\bin"
    if ($useVcpkg -and (Test-Path (Join-Path $vcpkgRoot "installed\$vcpkgTriplet"))) {
        $vcpkgInstalled = Join-Path $vcpkgRoot "installed\$vcpkgTriplet"
        $env:CGO_CFLAGS = "$($env:CGO_CFLAGS) -I$vcpkgInstalled\include"
        $env:CGO_LDFLAGS = "$($env:CGO_LDFLAGS) -L$vcpkgInstalled\lib -L$vcpkgInstalled\bin"
        $env:PATH = "$vcpkgInstalled\bin;$env:PATH"
        Write-Host "Using optional vcpkg C dependency paths: $vcpkgInstalled"
    }

    $linkerFlags = "-X $versionPkg.Version=$Version -X $versionPkg.BuildDate=$buildDate -X $versionPkg.Commit=$commit"
    $buildTags = @()
    switch ($PxlibBackend) {
        "cgo" { $buildTags += "pxlib_cgo" }
        "cgo-static" { $buildTags += @("pxlib_cgo", "pxlib_cgo_static") }
    }
    if ($almEnabled) {
        $buildTags += "alm_compat"
        $linkerFlags += " -X $licensingPkg.almAppID=$almAppID"
    }
    $tagArguments = @()
    if ($buildTags.Count -gt 0) {
        $tagArguments = @("-tags", ($buildTags -join ","))
    }

    $outExe = Join-Path $resolvedStageRoot "patris-export-windows-amd64.exe"
    $exeBuildArguments = @("build") + $tagArguments + @("-ldflags", $linkerFlags, "-o", $outExe, ".\cmd\patris-export")
    Invoke-Checked $go $exeBuildArguments
    Copy-Item $outExe (Join-Path $resolvedStageRoot "patris-export.exe") -Force

    $outDll = Join-Path $resolvedStageRoot "patris-export.dll"
    $dllBuildArguments = @("build") + $tagArguments + @("-buildmode=c-shared", "-ldflags", $linkerFlags, "-o", $outDll, ".\cmd\patris-export-lib")
    Invoke-Checked $go $dllBuildArguments
    if (-not (Test-Path (Join-Path $resolvedStageRoot "patris-export.h"))) {
        throw "The Windows shared-library build did not produce patris-export.h."
    }

    if ($PxlibBackend -ne "cgo-static") {
        Copy-Item (Join-Path $pxlibInstall "bin\*.dll") $resolvedStageRoot -ErrorAction SilentlyContinue
    }
    $gccBin = Split-Path -Parent $gcc
    foreach ($runtimeDll in @("libgcc_s_seh-1.dll", "libwinpthread-1.dll", "libstdc++-6.dll")) {
        $runtimePath = (& $gcc "-print-file-name=$runtimeDll" | Select-Object -First 1).Trim()
        if (-not (Test-Path -LiteralPath $runtimePath)) {
            $runtimePath = Join-Path $gccBin $runtimeDll
        }
        if (Test-Path -LiteralPath $runtimePath) {
            Copy-Item -LiteralPath $runtimePath -Destination $resolvedStageRoot -Force
        }
    }

    [ordered]@{
        variant = $buildVariant
        licensing_mode = $licensingMode
        license_required = [bool]$almEnabled
        native_backend = $PxlibBackend
    } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $resolvedStageRoot "BUILD-VARIANT.json") -Encoding utf8

    $documentation = [ordered]@{
        "README.md" = Join-Path $repoRoot "README.md"
        "INSTALL.md" = Join-Path $repoRoot "docs\INSTALL-BINARIES.md"
        "LICENSE" = Join-Path $repoRoot "LICENSE"
        "NOTICE" = Join-Path $repoRoot "NOTICE"
        "LICENSING.md" = Join-Path $repoRoot "docs\LICENSING.md"
        "CHANGELOG.md" = Join-Path $repoRoot "CHANGELOG.md"
    }
    foreach ($destinationName in $documentation.Keys) {
        Copy-Item -LiteralPath $documentation[$destinationName] -Destination (Join-Path $resolvedStageRoot $destinationName) -Force
    }
    $pxlibCommit = (Get-Content (Join-Path $repoRoot "dependencies\pxlib.ref") -Raw).Trim()
    @(
        "Patris Export $Version",
        "Build variant: $buildVariant",
        "Native pxlib backend: $PxlibBackend",
        "Licensing mode: $licensingMode",
        "Source commit: $commit",
        "pxlib source commit: $pxlibCommit",
        "Build date: $buildDate",
        "Built by: scripts/windows/Build-LocalWindows.ps1"
    ) | Set-Content -LiteralPath (Join-Path $resolvedStageRoot "BUILD-MANIFEST.txt") -Encoding utf8

    $requiredFiles = @(
        "patris-export-windows-amd64.exe",
        "patris-export.exe",
        "patris-export.dll",
        "patris-export.h",
        "libgcc_s_seh-1.dll",
        "libstdc++-6.dll",
        "libwinpthread-1.dll",
        "BUILD-VARIANT.json",
        "README.md",
        "INSTALL.md",
        "LICENSE",
        "NOTICE",
        "LICENSING.md",
        "CHANGELOG.md",
        "BUILD-MANIFEST.txt"
    )
    if ($PxlibBackend -ne "cgo-static") {
        $requiredFiles += "libpxlib.dll"
    }
    foreach ($requiredFile in $requiredFiles) {
        if (-not (Test-Path -LiteralPath (Join-Path $resolvedStageRoot $requiredFile))) {
            throw "Clean Windows staging build is missing required file: $requiredFile"
        }
    }

    if ($almEnabled) {
        Invoke-Checked $outExe @("license", "status")
    } elseif (Test-Path $PatrisDb) {
        Invoke-Checked $outExe @("info", $PatrisDb)
    } else {
        Write-Warning "Patris database not found at $PatrisDb; skipping local database smoke test."
    }

    Invoke-Checked $outExe @("--version")

    if (-not $SkipPromote) {
        foreach ($requiredFile in $requiredFiles) {
            Copy-Item -LiteralPath (Join-Path $resolvedStageRoot $requiredFile) -Destination (Join-Path $deployRoot $requiredFile) -Force
        }
    }

    Write-Host "Verified clean Windows staging build: $resolvedStageRoot"
    Write-Host "Build variant: $buildVariant ($licensingMode)"
    if ($SkipPromote) {
        Write-Host "Promotion skipped; the existing live deployment was left untouched."
    } else {
        Write-Host "Promoted executable: $(Join-Path $deployRoot 'patris-export.exe')"
    }
} finally {
    Pop-Location
}
