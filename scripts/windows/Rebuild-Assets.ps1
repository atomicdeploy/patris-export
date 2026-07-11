param(
    [string]$LogoSource = $(if ($env:PATRIS_EXPORT_LOGO_SOURCE) { $env:PATRIS_EXPORT_LOGO_SOURCE } else { "" }),
    [string]$IconPng,
    [string]$IconIco,
    [string]$IconSizes = $(if ($env:PATRIS_EXPORT_ICON_SIZES) { $env:PATRIS_EXPORT_ICON_SIZES } else { "256,128,64,48,32,24,16" }),
    [string]$WebIconPng,
    [string]$WebFaviconIco,
    [string]$NotificationAudio,
    [string]$CropAlphaThreshold = $(if ($env:PATRIS_EXPORT_CROP_ALPHA_THRESHOLD) { $env:PATRIS_EXPORT_CROP_ALPHA_THRESHOLD } else { "2%" })
)

$ErrorActionPreference = "Stop"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if (-not $LogoSource) {
    $downloadLogo = Join-Path $env:USERPROFILE "Downloads\Patris_API_Logo.png"
    if (Test-Path $downloadLogo) {
        $LogoSource = $downloadLogo
    } else {
        $LogoSource = Join-Path $repoRoot "assets\patris-api-icon.png"
    }
}
if (-not $IconPng) {
    $IconPng = Join-Path $repoRoot "assets\patris-api-icon.png"
}
if (-not $IconIco) {
    $IconIco = Join-Path $repoRoot "assets\windows\patris-api.ico"
}
if (-not $WebFaviconIco) {
    $WebFaviconIco = Join-Path $repoRoot "web\assets\favicon.ico"
}
if (-not $NotificationAudio) {
    $NotificationAudio = Join-Path $repoRoot "web\assets\notification.ogg"
}

$magick = (Get-Command magick.exe -ErrorAction SilentlyContinue | Select-Object -First 1)
if (-not $magick -and (Test-Path "C:\Program Files\ImageMagick\magick.exe")) {
    $magick = Get-Item "C:\Program Files\ImageMagick\magick.exe"
}
if (-not $magick) {
    throw "ImageMagick magick.exe is required to rebuild Windows icon assets."
}
if (-not (Test-Path $LogoSource)) {
    throw "Logo source not found: $LogoSource"
}

New-Item -ItemType Directory -Force (Split-Path -Parent $IconPng), (Split-Path -Parent $IconIco) | Out-Null
$geometry = (& $magick.Source $LogoSource -alpha extract -threshold $CropAlphaThreshold -format "%@" "info:").Trim()
if (-not $geometry) {
    throw "Could not determine visible logo bounds with alpha threshold $CropAlphaThreshold."
}
& $magick.Source $LogoSource -alpha set -crop $geometry +repage -background none -strip $IconPng
if ($LASTEXITCODE -ne 0) { throw "Failed to create $IconPng" }

$croppedGeometry = (& $magick.Source $IconPng -alpha extract -threshold $CropAlphaThreshold -format "%@" "info:").Trim()
$croppedExpected = (& $magick.Source identify -format "%wx%h+0+0" $IconPng).Trim()
if ($croppedGeometry -ne $croppedExpected) {
    throw "Generated icon PNG still has transparent edge padding at alpha threshold ${CropAlphaThreshold}: bounds $croppedGeometry, expected $croppedExpected."
}

& $magick.Source $IconPng -define "icon:auto-resize=$IconSizes" $IconIco
if ($LASTEXITCODE -ne 0) { throw "Failed to create $IconIco" }

New-Item -ItemType Directory -Force (Split-Path -Parent $WebFaviconIco) | Out-Null
if ($WebIconPng) {
    New-Item -ItemType Directory -Force (Split-Path -Parent $WebIconPng) | Out-Null
    Copy-Item $IconPng $WebIconPng -Force
}
Copy-Item $IconIco $WebFaviconIco -Force

if (-not (Test-Path $NotificationAudio)) {
    throw "Notification audio missing: $NotificationAudio"
}

Write-Host "Rebuilt assets:"
Write-Host "  Crop alpha threshold: $CropAlphaThreshold"
Write-Host "  $IconPng"
Write-Host "  $IconIco"
if ($WebIconPng) {
    Write-Host "  $WebIconPng"
}
Write-Host "  $WebFaviconIco"
Write-Host "  $NotificationAudio"
