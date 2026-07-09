param(
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$DbPath = "C:\Patris\data4\kala.db",
    [string]$Address = ":8080",
    [string]$Debounce = "500ms"
)

$ErrorActionPreference = "Stop"

$deployRoot = Join-Path $AtomicDeployRoot "deploy"
$exe = Join-Path $deployRoot "patris-export-windows-amd64.exe"
if (-not (Test-Path $exe)) {
    throw "Executable not found: $exe. Run Build-LocalWindows.ps1 first."
}

Push-Location $deployRoot
try {
    & $exe serve $DbPath --addr $Address --debounce $Debounce
} finally {
    Pop-Location
}
