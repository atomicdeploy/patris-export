[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$CarrierPath,
    [Parameter(Mandatory)]
    [string]$OutputPath,
    [string]$SourceRoot = (Join-Path $PSScriptRoot '..\..\docs\examples\vba'),
    [string]$ManifestPath
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

$carrier = [IO.Path]::GetFullPath($CarrierPath)
$output = [IO.Path]::GetFullPath($OutputPath)
$sourceDirectory = [IO.Path]::GetFullPath($SourceRoot)
if ([IO.Path]::GetExtension($carrier) -ine '.xltm' -or
    [IO.Path]::GetExtension($output) -ine '.xltm') {
    throw 'Headless template assembly accepts only .xltm carriers and outputs.'
}
if (-not (Test-Path -LiteralPath $carrier -PathType Leaf)) {
    throw "Compiled carrier not found: $carrier"
}
if (Test-Path -LiteralPath $output) {
    throw "Refusing to overwrite an existing template: $output"
}
if ([string]::IsNullOrWhiteSpace($ManifestPath)) {
    $ManifestPath = $output + '.headless-manifest.json'
}
$manifest = [IO.Path]::GetFullPath($ManifestPath)
if (Test-Path -LiteralPath $manifest) {
    throw "Refusing to overwrite an existing manifest: $manifest"
}

$audit = Join-Path $PSScriptRoot 'Test-ExcelTemplateDataFree.ps1'
if (-not (Test-Path -LiteralPath $audit -PathType Leaf)) {
    throw "Required data-free OOXML audit is missing: $audit"
}
[void](& $audit -Path $carrier)

$requiredSources = @(
    'ProductCatalogSync.bas',
    'JsonRuntime.bas',
    'JsonValue.cls',
    'AsyncWinHttpRequest.cls',
    'PricingSseParser.cls',
    'ThisWorkbook.cls'
)
$sourceHashes = [ordered]@{}
foreach ($name in $requiredSources) {
    $path = Join-Path $sourceDirectory $name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required VBA source is missing: $path"
    }
    $sourceHashes[$name] = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
}

$archive = [IO.Compression.ZipFile]::OpenRead($carrier)
try {
    $vbaEntry = $archive.GetEntry('xl/vbaProject.bin')
    if ($null -eq $vbaEntry -or $vbaEntry.Length -le 0) {
        throw 'Compiled carrier is missing xl/vbaProject.bin.'
    }
    $contentTypesEntry = $archive.GetEntry('[Content_Types].xml')
    $relationshipsEntry = $archive.GetEntry('xl/_rels/workbook.xml.rels')
    if ($null -eq $contentTypesEntry -or $null -eq $relationshipsEntry) {
        throw 'Compiled carrier is missing required OOXML relationship metadata.'
    }
    $contentTypesReader = [IO.StreamReader]::new($contentTypesEntry.Open())
    $relationshipsReader = [IO.StreamReader]::new($relationshipsEntry.Open())
    try {
        $contentTypes = $contentTypesReader.ReadToEnd()
        $relationships = $relationshipsReader.ReadToEnd()
    }
    finally {
        $contentTypesReader.Dispose()
        $relationshipsReader.Dispose()
    }
    if ($contentTypes -notmatch 'application/vnd\.ms-excel\.template\.macroEnabled\.main\+xml' -or
        $contentTypes -notmatch 'application/vnd\.ms-office\.vbaProject') {
        throw 'Compiled carrier content types are not a macro-enabled Excel template.'
    }
    if ($relationships -notmatch '/vbaProject' -or
        $relationships -notmatch 'vbaProject\.bin') {
        throw 'Compiled carrier workbook has no VBA project relationship.'
    }
    $vbaStream = $vbaEntry.Open()
    $vbaSha = [Security.Cryptography.SHA256]::Create()
    try {
        $vbaHash = [BitConverter]::ToString(
            $vbaSha.ComputeHash($vbaStream)
        ).Replace('-', '').ToLowerInvariant()
    }
    finally {
        $vbaSha.Dispose()
        $vbaStream.Dispose()
    }
}
finally {
    $archive.Dispose()
}

$outputDirectory = Split-Path -Parent $output
if (-not (Test-Path -LiteralPath $outputDirectory -PathType Container)) {
    [void][IO.Directory]::CreateDirectory($outputDirectory)
}
$temporary = Join-Path $outputDirectory ('.headless-' + [Guid]::NewGuid().ToString('N') + '.xltm')
try {
    [IO.File]::Copy($carrier, $temporary, $false)
    [void](& $audit -Path $temporary)
    [IO.File]::Move($temporary, $output)
}
finally {
    if (Test-Path -LiteralPath $temporary -PathType Leaf) {
        [IO.File]::Delete($temporary)
    }
}

$carrierHash = (Get-FileHash -LiteralPath $carrier -Algorithm SHA256).Hash.ToLowerInvariant()
$outputHash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
if ($carrierHash -cne $outputHash) {
    throw 'Headless assembly changed the compiled carrier unexpectedly.'
}
$manifestDirectory = Split-Path -Parent $manifest
if (-not (Test-Path -LiteralPath $manifestDirectory -PathType Container)) {
    [void][IO.Directory]::CreateDirectory($manifestDirectory)
}
$record = [ordered]@{
    schema = 'patris.excel-headless-carrier/v1'
    created_at_utc = [DateTime]::UtcNow.ToString('o')
    carrier_sha256 = $carrierHash
    output_sha256 = $outputHash
    vba_project_sha256 = $vbaHash
    data_empty = $true
    source_compiled_headlessly = $false
    provenance = 'compiled-carrier-preserved-byte-for-byte'
    source_sha256 = $sourceHashes
    native_excel_required_for = @(
        'compile changed VBA source into vbaProject.bin',
        'resolve VBA references and compile errors',
        'native calculation and event behavior',
        'save/reopen and physical interaction acceptance'
    )
}
[IO.File]::WriteAllText(
    $manifest,
    ($record | ConvertTo-Json -Depth 6),
    [Text.UTF8Encoding]::new($false)
)

[pscustomobject]@{
    OutputPath = $output
    ManifestPath = $manifest
    SHA256 = $outputHash
    VBAProjectSHA256 = $vbaHash
    SourceCompiledHeadlessly = $false
}
