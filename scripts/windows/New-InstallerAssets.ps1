[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory,

    [string]$SourceImage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if (-not $SourceImage) {
    $SourceImage = Join-Path $repoRoot "assets\patris-api-icon.png"
}
$SourceImage = (Resolve-Path -LiteralPath $SourceImage).Path
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path

Add-Type -AssemblyName System.Drawing

function New-Canvas {
    param(
        [int]$Width,
        [int]$Height,
        [string]$Path,
        [bool]$Uninstaller = $false
    )

    $bitmap = [System.Drawing.Bitmap]::new(
        $Width,
        $Height,
        [System.Drawing.Imaging.PixelFormat]::Format24bppRgb
    )
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $logo = $null
    $gradient = $null
    $linePen = $null
    $dotBrush = $null
    $titleFont = $null
    $smallFont = $null
    $whiteBrush = $null
    $mutedBrush = $null
    $format = $null
    try {
        $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality

        $rect = [System.Drawing.Rectangle]::new(0, 0, $Width, $Height)
        $start = if ($Uninstaller) {
            [System.Drawing.Color]::FromArgb(255, 17, 42, 78)
        } else {
            [System.Drawing.Color]::FromArgb(255, 10, 69, 134)
        }
        $end = [System.Drawing.Color]::FromArgb(255, 15, 126, 164)
        $gradient = [System.Drawing.Drawing2D.LinearGradientBrush]::new($rect, $start, $end, 35.0)
        $graphics.FillRectangle($gradient, $rect)

        $linePen = [System.Drawing.Pen]::new([System.Drawing.Color]::FromArgb(72, 186, 230, 253), 1.0)
        $dotBrush = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::FromArgb(150, 224, 242, 254))
        $step = if ($Width -gt 150) { 34 } else { 28 }
        for ($x = -20; $x -lt $Width + 30; $x += $step) {
            $graphics.DrawLine($linePen, $x, 0, $x + 70, $Height)
            $graphics.FillEllipse($dotBrush, $x + 24, [Math]::Min($Height - 5, 20 + (($x + 20) % 47)), 3, 3)
        }

        $logo = [System.Drawing.Image]::FromFile($SourceImage)
        $whiteBrush = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::White)
        $mutedBrush = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::FromArgb(225, 224, 242, 254))
        $format = [System.Drawing.StringFormat]::new()
        $format.Alignment = [System.Drawing.StringAlignment]::Center

        if ($Width -le 150) {
            $graphics.DrawImage($logo, 101, 6, 44, 44)
            $titleFont = [System.Drawing.Font]::new("Segoe UI", 10.5, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
            $smallFont = [System.Drawing.Font]::new("Segoe UI", 8.0, [System.Drawing.FontStyle]::Regular, [System.Drawing.GraphicsUnit]::Pixel)
            $graphics.DrawString("PATRIS", $titleFont, $whiteBrush, 10, 15)
            $graphics.DrawString("EXPORT", $titleFont, $whiteBrush, 10, 29)
            $graphics.DrawString("by Atomic Deploy", $smallFont, $mutedBrush, 10, 43)
        } else {
            $graphics.DrawImage($logo, 39, 25, 86, 86)
            $titleFont = [System.Drawing.Font]::new("Segoe UI", 15.0, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
            $smallFont = [System.Drawing.Font]::new("Segoe UI", 10.0, [System.Drawing.FontStyle]::Regular, [System.Drawing.GraphicsUnit]::Pixel)
            $titleRect = [System.Drawing.RectangleF]::new(8, 130, $Width - 16, 32)
            $subRect = [System.Drawing.RectangleF]::new(12, 168, $Width - 24, 70)
            $graphics.DrawString("PATRIS EXPORT", $titleFont, $whiteBrush, $titleRect, $format)
            $message = if ($Uninstaller) { "A clean uninstall with your configuration protected by default" } else { "Reliable Patris data export, integration, and automation" }
            $graphics.DrawString($message, $smallFont, $mutedBrush, $subRect, $format)
        }

        $bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Bmp)
    } finally {
        foreach ($resource in @($format, $mutedBrush, $whiteBrush, $smallFont, $titleFont, $dotBrush, $linePen, $gradient, $logo, $graphics, $bitmap)) {
            if ($null -ne $resource) {
                $resource.Dispose()
            }
        }
    }
}

New-Canvas -Width 150 -Height 57 -Path (Join-Path $OutputDirectory "installer-header.bmp")
New-Canvas -Width 164 -Height 314 -Path (Join-Path $OutputDirectory "installer-sidebar.bmp")
New-Canvas -Width 164 -Height 314 -Path (Join-Path $OutputDirectory "uninstaller-sidebar.bmp") -Uninstaller $true

Get-ChildItem -LiteralPath $OutputDirectory -Filter "*.bmp" |
    Select-Object Name, Length, FullName
