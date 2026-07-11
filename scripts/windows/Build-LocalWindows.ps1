param(
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$PxlibRepo = $(if ($env:PXLIB_REPO) { $env:PXLIB_REPO } else { "https://github.com/steinm/pxlib.git" }),
    [string]$PxlibRef = $env:PXLIB_REF,
    [string]$PatrisDb = "C:\Patris\data4\kala.db",
    [string]$Version = $env:VERSION,
    [switch]$SkipPxlibBuild
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
$depsRoot = Join-Path $AtomicDeployRoot "deps"
$pxlibSource = Join-Path $depsRoot "pxlib"
$pxlibBuild = Join-Path $depsRoot "pxlib-build-windows"
$pxlibInstall = Join-Path $depsRoot "pxlib-install-windows"
$deployRoot = Join-Path $AtomicDeployRoot "deploy"
$vcpkgRoot = $(if ($env:VCPKG_ROOT) { $env:VCPKG_ROOT } else { Join-Path $depsRoot "vcpkg" })
$vcpkgTriplet = $(if ($env:VCPKG_DEFAULT_TRIPLET) { $env:VCPKG_DEFAULT_TRIPLET } else { "x64-windows" })
$useVcpkg = $env:USE_VCPKG -match '^(1|true|yes|on)$'

if (-not $Version) {
    Push-Location $repoRoot
    try {
        $Version = (& git describe --tags --abbrev=0 2>$null)
        if ($LASTEXITCODE -ne 0 -or -not $Version) {
            $Version = "v1.0.0"
        }
    } finally {
        Pop-Location
    }
    $Version = $Version -replace '^v', ''
}
if ($Version -notmatch '^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9._-]+)?$') {
    Write-Warning "Invalid version '$Version'; using 1.0.0"
    $Version = "1.0.0"
}

New-Item -ItemType Directory -Force $depsRoot, $deployRoot | Out-Null

$winLibsBin = "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin"
$goBin = "C:\Program Files\Go\bin"
$env:PATH = "$winLibsBin;$goBin;$env:PATH"

$git = Resolve-Tool "git.exe"
$bash = Resolve-Tool "bash.exe" @("C:\Program Files\Git\usr\bin\bash.exe")
$go = Resolve-Tool "go.exe" @("$goBin\go.exe")
$cmake = Resolve-Tool "cmake.exe" @("$winLibsBin\cmake.exe")
$gcc = Resolve-Tool "gcc.exe" @("$winLibsBin\gcc.exe")
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

Push-Location $repoRoot
try {
    & (Join-Path $PSScriptRoot "Rebuild-Assets.ps1")

    Push-Location web
    try {
        cmd /c npm install
        if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
        cmd /c npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    } finally {
        Pop-Location
    }

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

    $buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $commit = (& $git -C $repoRoot rev-parse --short=12 HEAD).Trim()
    $versionPkg = "github.com/atomicdeploy/patris-export/pkg/version"
    $outExe = Join-Path $deployRoot "patris-export-windows-amd64.exe"
    Invoke-Checked $go @("build", "-ldflags", "-X $versionPkg.Version=$Version -X $versionPkg.BuildDate=$buildDate -X $versionPkg.Commit=$commit", "-o", $outExe, ".\cmd\patris-export")
    Copy-Item $outExe (Join-Path $deployRoot "patris-export.exe") -Force

    Copy-Item (Join-Path $pxlibInstall "bin\*.dll") $deployRoot -ErrorAction SilentlyContinue
    foreach ($runtimeDll in @("libgcc_s_seh-1.dll", "libwinpthread-1.dll", "libstdc++-6.dll")) {
        $runtimePath = Join-Path $winLibsBin $runtimeDll
        if (Test-Path $runtimePath) {
            Copy-Item $runtimePath $deployRoot -Force
        }
    }

    if (Test-Path $PatrisDb) {
        Invoke-Checked $outExe @("info", $PatrisDb)
    } else {
        Write-Warning "Patris database not found at $PatrisDb; skipping local database smoke test."
    }

    Write-Host "Built executable: $outExe"
} finally {
    Pop-Location
}
