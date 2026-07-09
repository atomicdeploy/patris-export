param(
    [string]$GitMsysRoot = "C:\Program Files\Git",
    [switch]$LaunchElevated
)

$ErrorActionPreference = "Stop"

$pacman = Join-Path $GitMsysRoot "usr\bin\pacman.exe"
$shell = Join-Path $GitMsysRoot "msys2_shell.cmd"

if (-not (Test-Path $pacman)) {
    throw "pacman was not found at $pacman"
}

& $pacman --version | Select-Object -First 3

$writeTest = & (Join-Path $GitMsysRoot "usr\bin\bash.exe") -lc "touch /var/lib/pacman/codex-write-test 2>/dev/null && rm /var/lib/pacman/codex-write-test && echo writable || echo not-writable"
if ($writeTest -contains "writable") {
    & $pacman -Syy --noconfirm
    & $pacman -S --needed --noconfirm mingw-w64-x86_64-gcc mingw-w64-x86_64-cmake mingw-w64-x86_64-make mingw-w64-x86_64-pkgconf mingw-w64-x86_64-python make git
    exit $LASTEXITCODE
}

Write-Warning "$GitMsysRoot is not writable from this normal user session. Pacman needs an elevated MSYS2 shell or a writable MSYS2 root."

if ($LaunchElevated) {
    $command = "pacman -Syyu --noconfirm; pacman -S --needed --noconfirm mingw-w64-x86_64-gcc mingw-w64-x86_64-cmake mingw-w64-x86_64-make mingw-w64-x86_64-pkgconf mingw-w64-x86_64-python make git"
    Start-Process -FilePath $shell -ArgumentList "-mingw64", "-defterm", "-no-start", "-here", "-c", $command -Verb RunAs
    Write-Host "Launched elevated MSYS2 shell for pacman repair."
}
