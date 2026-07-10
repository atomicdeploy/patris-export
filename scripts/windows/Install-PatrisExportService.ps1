param(
    [string]$AtomicDeployRoot = "$env:USERPROFILE\Desktop\AtomicDeploy",
    [string]$ServiceName = "PatrisExport",
    [string]$DisplayName = "Patris Export API",
    [string]$DbPath = "C:\Patris\data4\kala.db",
    [string]$Address = ":8080",
    [string]$Debounce = "500ms",
    [switch]$Start
)

$ErrorActionPreference = "Stop"

$deployRoot = Join-Path $AtomicDeployRoot "deploy"
$appExe = Join-Path $deployRoot "patris-export.exe"
$fallbackExe = Join-Path $deployRoot "patris-export-windows-amd64.exe"
$winswExe = Join-Path $deployRoot "$ServiceName.exe"
$winswXml = Join-Path $deployRoot "$ServiceName.xml"

if (-not (Test-Path $appExe)) {
    if (Test-Path $fallbackExe) {
        Copy-Item $fallbackExe $appExe -Force
    } else {
        throw "Executable not found: $appExe. Run Build-LocalWindows.ps1 first."
    }
}

New-Item -ItemType Directory -Force $deployRoot | Out-Null

if (-not (Test-Path $winswExe)) {
    $url = "https://github.com/winsw/winsw/releases/latest/download/WinSW-x64.exe"
    Invoke-WebRequest -Uri $url -OutFile $winswExe
}

$xml = @"
<service>
  <id>$ServiceName</id>
  <name>$DisplayName</name>
  <description>Serves the Patris Export web UI and API.</description>
  <executable>$appExe</executable>
  <arguments>serve "$DbPath" --addr $Address --debounce $Debounce</arguments>
  <workingdirectory>$deployRoot</workingdirectory>
  <log mode="roll-by-size">
    <sizeThreshold>10485760</sizeThreshold>
    <keepFiles>5</keepFiles>
  </log>
  <onfailure action="restart" delay="10 sec"/>
</service>
"@
Set-Content -Path $winswXml -Value $xml -Encoding UTF8

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ';') -notcontains $deployRoot) {
    [Environment]::SetEnvironmentVariable("Path", ($userPath.TrimEnd(';') + ";$deployRoot").TrimStart(';'), "User")
    Write-Host "Added to user PATH: $deployRoot"
}

& $winswExe install
if ($Start) {
    & $winswExe start
}

Write-Host "Installed service $ServiceName using $winswExe"
