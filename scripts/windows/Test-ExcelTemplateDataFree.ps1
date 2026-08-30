[CmdletBinding(DefaultParameterSetName = 'Audit')]
param(
    [Parameter(Mandatory = $true, ParameterSetName = 'Audit')]
    [string]$Path,
    [Parameter(Mandatory = $true, ParameterSetName = 'SelfTest')]
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Read-ZipText([IO.Compression.ZipArchive]$Archive, [string]$EntryName) {
    $entry = $Archive.GetEntry($EntryName)
    if ($null -eq $entry) {
        throw "Required Excel package entry is missing: $EntryName"
    }
    $reader = [IO.StreamReader]::new(
        $entry.Open(),
        [Text.Encoding]::UTF8,
        $true
    )
    try {
        return $reader.ReadToEnd()
    }
    finally {
        $reader.Dispose()
    }
}

function Resolve-PackagePart([string]$BasePart, [string]$Target) {
    if ([string]::IsNullOrWhiteSpace($Target)) {
        throw "Office package relationship from $BasePart has no target."
    }
    $baseUri = [Uri]::new(
        'http://package.invalid/' + $BasePart.Replace('\', '/').TrimStart('/')
    )
    $resolved = [Uri]::new($baseUri, $Target.Replace('\', '/'))
    return $resolved.AbsolutePath.TrimStart('/')
}

function Get-RelationshipMap(
    [IO.Compression.ZipArchive]$Archive,
    [string]$RelationshipPart
) {
    [xml]$document = Read-ZipText $Archive $RelationshipPart
    $relationships = @{}
    foreach ($node in $document.SelectNodes("/*[local-name()='Relationships']/*[local-name()='Relationship']")) {
        $identifier = [string]$node.GetAttribute('Id')
        if ([string]::IsNullOrWhiteSpace($identifier)) {
            throw "Relationship in $RelationshipPart has no Id."
        }
        if ($relationships.ContainsKey($identifier)) {
            throw "Duplicate relationship Id '$identifier' in $RelationshipPart."
        }
        $relationships[$identifier] = [pscustomobject]@{
            Target = [string]$node.GetAttribute('Target')
            Type = [string]$node.GetAttribute('Type')
            TargetMode = [string]$node.GetAttribute('TargetMode')
        }
    }
    return $relationships
}

function Get-RelationshipIdentifier([Xml.XmlElement]$Node) {
    foreach ($attribute in $Node.Attributes) {
        if ($attribute.LocalName -eq 'id') {
            return [string]$attribute.Value
        }
    }
    return ''
}

function Convert-ColumnNameToNumber([string]$Name) {
    $number = 0
    foreach ($character in $Name.ToUpperInvariant().ToCharArray()) {
        if ($character -lt 'A' -or $character -gt 'Z') {
            throw "Invalid Excel column name: $Name"
        }
        $number = ($number * 26) + ([int]$character - [int][char]'A') + 1
    }
    return $number
}

function Convert-CellAddress([string]$Address) {
    $match = [regex]::Match($Address, '\A\$?([A-Za-z]+)\$?([0-9]+)\z')
    if (-not $match.Success) {
        throw "Invalid Excel cell address: $Address"
    }
    return [pscustomobject]@{
        Column = Convert-ColumnNameToNumber $match.Groups[1].Value
        Row = [int]$match.Groups[2].Value
    }
}

function Convert-RangeAddress([string]$Address) {
    $parts = $Address.Split(':')
    if ($parts.Count -eq 1) {
        $parts = @($parts[0], $parts[0])
    }
    if ($parts.Count -ne 2) {
        throw "Invalid Excel range address: $Address"
    }
    $start = Convert-CellAddress $parts[0]
    $end = Convert-CellAddress $parts[1]
    if ($start.Row -gt $end.Row -or $start.Column -gt $end.Column) {
        throw "Reversed Excel range address: $Address"
    }
    return [pscustomobject]@{
        StartColumn = $start.Column
        StartRow = $start.Row
        EndColumn = $end.Column
        EndRow = $end.Row
    }
}

function Test-CellHasPayload([Xml.XmlElement]$Cell) {
    if ($null -ne $Cell.SelectSingleNode("*[local-name()='f']")) {
        return $true
    }
    $value = $Cell.SelectSingleNode("*[local-name()='v']")
    if ($null -ne $value -and -not [string]::IsNullOrWhiteSpace($value.InnerText)) {
        return $true
    }
    $inline = $Cell.SelectSingleNode("*[local-name()='is']")
    if ($null -ne $inline -and -not [string]::IsNullOrWhiteSpace($inline.InnerText)) {
        return $true
    }
    return $false
}

function Test-RangeIntersectsDataArea(
    [string]$Address,
    [object]$TableRange
) {
    $candidate = Convert-RangeAddress $Address
    return (
        $candidate.EndRow -gt $TableRange.StartRow -and
        $candidate.StartColumn -le $TableRange.EndColumn -and
        $candidate.EndColumn -ge $TableRange.StartColumn
    )
}

function Get-WorksheetRelationshipPart([string]$WorksheetPart) {
    $directory = [IO.Path]::GetDirectoryName($WorksheetPart).Replace('\', '/')
    $fileName = [IO.Path]::GetFileName($WorksheetPart)
    return "$directory/_rels/$fileName.rels"
}

function Test-ExcelTemplateDataFree([string]$WorkbookPath) {
    $resolvedPath = [IO.Path]::GetFullPath($WorkbookPath)
    if (-not (Test-Path -LiteralPath $resolvedPath -PathType Leaf)) {
        throw "Excel template does not exist: $resolvedPath"
    }
    if ([IO.Path]::GetExtension($resolvedPath) -ine '.xltm') {
        throw "The data-free release gate accepts only .xltm templates: $resolvedPath"
    }

    $archive = [IO.Compression.ZipFile]::OpenRead($resolvedPath)
    try {
        [xml]$workbook = Read-ZipText $archive 'xl/workbook.xml'
        $workbookRelationships = Get-RelationshipMap $archive 'xl/_rels/workbook.xml.rels'
        $tableInventory = @()

        foreach ($sheetNode in $workbook.SelectNodes("/*[local-name()='workbook']/*[local-name()='sheets']/*[local-name()='sheet']")) {
            $sheetName = [string]$sheetNode.GetAttribute('name')
            $relationshipIdentifier = Get-RelationshipIdentifier $sheetNode
            if ([string]::IsNullOrWhiteSpace($relationshipIdentifier) -or
                -not $workbookRelationships.ContainsKey($relationshipIdentifier)) {
                throw "Worksheet '$sheetName' has an unresolved package relationship."
            }
            $worksheetPart = Resolve-PackagePart `
                'xl/workbook.xml' `
                $workbookRelationships[$relationshipIdentifier].Target
            [xml]$worksheet = Read-ZipText $archive $worksheetPart
            $tablePartNodes = @($worksheet.SelectNodes("/*[local-name()='worksheet']/*[local-name()='tableParts']/*[local-name()='tablePart']"))
            if ($tablePartNodes.Count -eq 0) {
                continue
            }
            $worksheetRelationshipPart = Get-WorksheetRelationshipPart $worksheetPart
            $worksheetRelationships = Get-RelationshipMap $archive $worksheetRelationshipPart

            foreach ($tablePartNode in $tablePartNodes) {
                $tableRelationshipIdentifier = Get-RelationshipIdentifier $tablePartNode
                if ([string]::IsNullOrWhiteSpace($tableRelationshipIdentifier) -or
                    -not $worksheetRelationships.ContainsKey($tableRelationshipIdentifier)) {
                    throw "Worksheet '$sheetName' has an unresolved table relationship."
                }
                $tablePart = Resolve-PackagePart `
                    $worksheetPart `
                    $worksheetRelationships[$tableRelationshipIdentifier].Target
                [xml]$tableDocument = Read-ZipText $archive $tablePart
                $tableNode = $tableDocument.DocumentElement
                $tableInventory += [pscustomobject]@{
                    Name = [string]$tableNode.GetAttribute('name')
                    DisplayName = [string]$tableNode.GetAttribute('displayName')
                    Reference = [string]$tableNode.GetAttribute('ref')
                    SheetName = $sheetName
                    SheetPart = $worksheetPart
                    TablePart = $tablePart
                    Worksheet = $worksheet
                }
            }
        }

        $reports = @()
        foreach ($requiredName in @('Products', 'SyncData')) {
            $matches = @($tableInventory | Where-Object {
                $_.Name -ieq $requiredName -or $_.DisplayName -ieq $requiredName
            })
            if ($matches.Count -ne 1) {
                throw "Expected exactly one '$requiredName' table; found $($matches.Count)."
            }
            $table = $matches[0]
            $tableRange = Convert-RangeAddress $table.Reference
            if ($tableRange.EndRow -gt ($tableRange.StartRow + 1)) {
                throw "Template table '$requiredName' persists more than one blank placeholder row: $($table.Reference)"
            }

            foreach ($cell in $table.Worksheet.SelectNodes("/*[local-name()='worksheet']/*[local-name()='sheetData']/*[local-name()='row']/*[local-name()='c']")) {
                $cellAddressText = [string]$cell.GetAttribute('r')
                if ([string]::IsNullOrWhiteSpace($cellAddressText)) {
                    throw "Worksheet '$($table.SheetName)' contains a cell without an address."
                }
                $cellAddress = Convert-CellAddress $cellAddressText
                if ($cellAddress.Row -gt $tableRange.StartRow -and
                    $cellAddress.Column -ge $tableRange.StartColumn -and
                    $cellAddress.Column -le $tableRange.EndColumn -and
                    (Test-CellHasPayload $cell)) {
                    throw "Template table '$requiredName' persists product/runtime data at $($table.SheetName)!$cellAddressText."
                }
            }

            foreach ($hyperlink in $table.Worksheet.SelectNodes("/*[local-name()='worksheet']/*[local-name()='hyperlinks']/*[local-name()='hyperlink']")) {
                $hyperlinkReference = [string]$hyperlink.GetAttribute('ref')
                if (-not [string]::IsNullOrWhiteSpace($hyperlinkReference) -and
                    (Test-RangeIntersectsDataArea $hyperlinkReference $tableRange)) {
                    throw "Template table '$requiredName' persists a runtime hyperlink at $($table.SheetName)!$hyperlinkReference."
                }
            }

            $reports += [pscustomobject]@{
                name = $requiredName
                sheet = $table.SheetName
                reference = $table.Reference
                persisted_records = 0
            }
        }

        return [pscustomobject]@{
            passed = $true
            path = $resolvedPath
            sha256 = (Get-FileHash -LiteralPath $resolvedPath -Algorithm SHA256).Hash.ToLowerInvariant()
            product_records = 0
            runtime_records = 0
            tables = $reports
        }
    }
    finally {
        $archive.Dispose()
    }
}

function Write-ZipEntry(
    [IO.Compression.ZipArchive]$Archive,
    [string]$EntryName,
    [string]$Text
) {
    $entry = $Archive.CreateEntry($EntryName)
    $writer = [IO.StreamWriter]::new(
        $entry.Open(),
        [Text.UTF8Encoding]::new($false)
    )
    try {
        $writer.Write($Text)
    }
    finally {
        $writer.Dispose()
    }
}

function New-SelfTestTemplate(
    [string]$OutputPath,
    [string]$ProductPayload,
    [string]$SyncPayload
) {
    $archive = [IO.Compression.ZipFile]::Open(
        $OutputPath,
        [IO.Compression.ZipArchiveMode]::Create
    )
    try {
        Write-ZipEntry $archive 'xl/workbook.xml' @'
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="محصولات" sheetId="1" r:id="rId1"/><sheet name="داده‌های همگام‌سازی" sheetId="2" r:id="rId2"/></sheets></workbook>
'@
        Write-ZipEntry $archive 'xl/_rels/workbook.xml.rels' @'
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/></Relationships>
'@
        Write-ZipEntry $archive 'xl/worksheets/sheet1.xml' (
            '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetData><row r="5"><c r="B5" t="inlineStr"><is><t>Price</t></is></c></row><row r="6"><c r="B6" s="1"/>' +
            $ProductPayload +
            '</row></sheetData><tableParts count="1"><tablePart r:id="rId1"/></tableParts></worksheet>'
        )
        Write-ZipEntry $archive 'xl/worksheets/_rels/sheet1.xml.rels' @'
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/table" Target="../tables/table1.xml"/></Relationships>
'@
        Write-ZipEntry $archive 'xl/tables/table1.xml' @'
<table xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" id="1" name="Products" displayName="Products" ref="B5:K6"><autoFilter ref="B5:K6"/><tableColumns count="10"/></table>
'@
        Write-ZipEntry $archive 'xl/worksheets/sheet2.xml' (
            '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Key</t></is></c></row><row r="2">' +
            $SyncPayload +
            '</row></sheetData><tableParts count="1"><tablePart r:id="rId1"/></tableParts></worksheet>'
        )
        Write-ZipEntry $archive 'xl/worksheets/_rels/sheet2.xml.rels' @'
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/table" Target="../tables/table2.xml"/></Relationships>
'@
        Write-ZipEntry $archive 'xl/tables/table2.xml' @'
<table xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" id="2" name="SyncData" displayName="SyncData" ref="A1:X2"><autoFilter ref="A1:X2"/><tableColumns count="24"/></table>
'@
    }
    finally {
        $archive.Dispose()
    }
}

function Invoke-SelfTest {
    $temporaryDirectory = Join-Path (
        [IO.Path]::GetTempPath()
    ) ('patris-empty-xltm-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    try {
        $clean = Join-Path $temporaryDirectory 'clean.xltm'
        New-SelfTestTemplate $clean '' ''
        $cleanReport = Test-ExcelTemplateDataFree $clean
        if (-not $cleanReport.passed -or $cleanReport.product_records -ne 0) {
            throw 'Clean template self-test did not pass with zero records.'
        }

        foreach ($fixture in @(
            @{
                Name = 'product-body'
                Product = '<c r="I6" t="inlineStr"><is><t>STATIC PRODUCT</t></is></c>'
                Sync = ''
            },
            @{
                Name = 'stale-product-below-table'
                Product = '</row><row r="7"><c r="H7"><v>113006024</v></c>'
                Sync = ''
            },
            @{
                Name = 'sync-body'
                Product = ''
                Sync = '<c r="A2" t="inlineStr"><is><t>patris:113006024</t></is></c>'
            }
        )) {
            $fixturePath = Join-Path $temporaryDirectory ($fixture.Name + '.xltm')
            New-SelfTestTemplate $fixturePath $fixture.Product $fixture.Sync
            $rejected = $false
            try {
                [void](Test-ExcelTemplateDataFree $fixturePath)
            }
            catch {
                $rejected = $true
            }
            if (-not $rejected) {
                throw "Contaminated template self-test was accepted: $($fixture.Name)"
            }
        }

        [pscustomobject]@{
            passed = $true
            clean_records = 0
            rejected_contaminated_fixtures = 3
        }
    }
    finally {
        if (Test-Path -LiteralPath $temporaryDirectory) {
            Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
        }
    }
}

if ($SelfTest) {
    Invoke-SelfTest
}
else {
    Test-ExcelTemplateDataFree $Path
}
