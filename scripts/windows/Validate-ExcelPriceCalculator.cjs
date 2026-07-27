'use strict';

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const STANDARD_HEADERS = Object.freeze([
  'فی فروش',
  'گرم',
  'سایر',
  'فی فروش2',
  'نرخ ارزی',
  'همه انبارها',
  'کد کالا',
  'نام کالا',
]);

const ADVANCED_HEADERS = Object.freeze([
  'قیمت نهایی محاسبه‌شده (تومان)',
  'وزن کالا (گرم)',
  'وزن و محل کالا',
  'فی فروش منبع',
  'قیمت ارزی',
  'موجودی کل انبارها',
  'کد کالا',
  'نام کالا',
  'شناسه و لینک ووکامرس',
  'قیمت قابل‌مشاهده مشتری (تومان)',
  'اختلاف با قیمت مشتری',
  'وضعیت همگام‌سازی قیمت',
  'ارز کالا',
  'درصد سود',
  'نرخ حمل هر کیلو',
  'تاریخ نرخ ارز',
]);

const SYNC_DATA_HEADERS = Object.freeze([
  'کد کالا',
  'ارز کالا',
  'نرخ حمل هر کیلو',
  'ارز حمل',
  'درصد سود',
  'بهای یوآن',
  'بهای دلار',
  'تاریخ نرخ',
  'شناسه ووکامرس',
  'قیمت مشتری ووکامرس',
  'آخرین تغییر ووکامرس',
  'بازبینی رکورد',
  'نشانی محصول',
  'سود مرجع',
  'قیمت نهایی مرجع',
  'قیمت فروش ویژه',
]);

const repoRoot = path.resolve(__dirname, '..', '..');
const defaultCandidate = path.join(
  repoRoot,
  'docs',
  'examples',
  'لیست قیمت دیجیتالاجیک - استاندارد.xltm',
);
const defaultReference = path.join(
  os.homedir(),
  'Documents',
  'Excel',
  'Archive',
  'Price Calculator',
  '2026-07-25',
  'ماشین حساب قیمت - مرجع ایستا - 1405-05-03.xlsb',
);

function usage() {
  return [
    'Usage:',
    '  node scripts/windows/Validate-ExcelPriceCalculator.cjs [options]',
    '',
    'Options:',
    `  --candidate PATH       Workbook/template to validate (default: ${defaultCandidate})`,
    `  --reference PATH       Archived calculator baseline (default: ${defaultReference})`,
    '  --sync                 Run ProductCatalogSync.RefreshAllData silently before validation',
    '  --no-sync              Validate an already-synced workbook without running macros',
    '  --strict-reference     Fail on every comparable weight/rate difference from the archive',
    '  --json                 Print the complete machine-readable report',
    '  --timeout-ms NUMBER    Excel validation timeout in milliseconds (default: 240000)',
    '  --help                 Show this help',
    '',
    'For .xltm candidates, --sync is the default. The template is instantiated in',
    'memory and closed without saving, so validation never mutates the canonical file.',
  ].join('\n');
}

function parseArgs(argv) {
  const options = {
    candidate: defaultCandidate,
    reference: process.env.PATRIS_EXCEL_REFERENCE || defaultReference,
    sync: undefined,
    strictReference: false,
    json: false,
    timeoutMs: 240000,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    switch (argument) {
      case '--candidate':
        index += 1;
        if (index >= argv.length) throw new Error('--candidate requires a path');
        options.candidate = argv[index];
        break;
      case '--reference':
        index += 1;
        if (index >= argv.length) throw new Error('--reference requires a path');
        options.reference = argv[index];
        break;
      case '--sync':
        options.sync = true;
        break;
      case '--no-sync':
        options.sync = false;
        break;
      case '--strict-reference':
        options.strictReference = true;
        break;
      case '--json':
        options.json = true;
        break;
      case '--timeout-ms':
        index += 1;
        if (index >= argv.length) throw new Error('--timeout-ms requires a number');
        options.timeoutMs = Number(argv[index]);
        if (!Number.isInteger(options.timeoutMs) || options.timeoutMs < 1000) {
          throw new Error('--timeout-ms must be an integer of at least 1000');
        }
        break;
      case '--help':
      case '-h':
        options.help = true;
        break;
      default:
        throw new Error(`unknown option: ${argument}`);
    }
  }

  options.candidate = path.resolve(options.candidate);
  options.reference = path.resolve(options.reference);
  if (options.sync === undefined) {
    options.sync = path.extname(options.candidate).toLowerCase() === '.xltm';
  }
  return options;
}

function sameArray(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function normalizeFormula(formula) {
  return String(formula || '').replace(/\s+/gu, '').toUpperCase();
}

function structurallyMatchesDynamicPriceFormula(formula) {
  const normalized = normalizeFormula(formula);
  const requiredTokens = [
    'ROUND(',
    'RC[1]',
    'RC[4]',
    'VLOOKUP(RC[6],SYNCDATA,2,FALSE)',
    'VLOOKUP(RC[6],SYNCDATA,3,FALSE)',
    'VLOOKUP(RC[6],SYNCDATA,4,FALSE)',
    'VLOOKUP(RC[6],SYNCDATA,5,FALSE)',
    'VLOOKUP(RC[6],SYNCDATA,6,FALSE)',
    'VLOOKUP(RC[6],SYNCDATA,7,FALSE)',
  ];
  return requiredTokens.every((token) => normalized.includes(token));
}

function buildFailures(report, options) {
  const failures = [];
  const failUnless = (condition, message) => {
    if (!condition) failures.push(message);
  };

  failUnless(
    sameArray(report.reference.headers, STANDARD_HEADERS),
    `archived headers changed: ${JSON.stringify(report.reference.headers)}`,
  );
  const isStandard = sameArray(report.candidate.headers, STANDARD_HEADERS);
  const isAdvanced = sameArray(report.candidate.headers, ADVANCED_HEADERS);
  report.candidate.edition = isStandard ? 'Standard' : isAdvanced ? 'Advanced' : 'Unknown';
  failUnless(
    isStandard || isAdvanced,
    `candidate headers must exactly match the Standard or Advanced Persian contract: ${JSON.stringify(report.candidate.headers)}`,
  );
  failUnless(
    sameArray(report.syncData.headers, SYNC_DATA_HEADERS),
    `SyncData headers changed: ${JSON.stringify(report.syncData.headers)}`,
  );
  failUnless(
    report.syncData.sheetVisibility === 2,
    `SyncData must remain xlSheetVeryHidden (2); got ${report.syncData.sheetVisibility}`,
  );
  const expectedTableAddress = isStandard
    ? /^B5:I\d+$/u
    : isAdvanced
      ? /^B5:Q\d+$/u
      : /(?!)/u;
  failUnless(
    expectedTableAddress.test(report.candidate.tableAddress),
    `${report.candidate.edition} Products table has an unexpected address: ${report.candidate.tableAddress}`,
  );
  failUnless(report.candidate.rowsWithCode > 0, 'Sync produced no product rows');
  failUnless(
    report.candidate.duplicateCodes === 0,
    `candidate contains ${report.candidate.duplicateCodes} duplicate product codes`,
  );
  failUnless(
    typeof report.candidate.config.yuan === 'number'
      && Number.isFinite(report.candidate.config.yuan)
      && report.candidate.config.yuan > 0,
    `live Yuan_Price must be a positive number; got ${report.candidate.config.yuan}`,
  );
  failUnless(
    typeof report.candidate.config.shipping === 'number'
      && Number.isFinite(report.candidate.config.shipping)
      && report.candidate.config.shipping >= 0,
    `live Shipping must be a non-negative number; got ${report.candidate.config.shipping}`,
  );
  failUnless(
    typeof report.candidate.config.profit === 'number'
      && Number.isFinite(report.candidate.config.profit)
      && report.candidate.config.profit >= 0,
    `live Profit must be a non-negative decimal; got ${report.candidate.config.profit}`,
  );
  failUnless(
    report.syncData.rowsWithCode === report.candidate.rowsWithCode,
    `SyncData row count differs from Products (${report.syncData.rowsWithCode}/${report.candidate.rowsWithCode})`,
  );
  failUnless(
    report.syncData.duplicateCodes === 0,
    `SyncData contains ${report.syncData.duplicateCodes} duplicate product codes`,
  );
  failUnless(
    report.syncData.missingProductCodes === 0,
    `${report.syncData.missingProductCodes} Products rows have no matching SyncData row`,
  );
  failUnless(
    report.syncData.extraCodes === 0,
    `${report.syncData.extraCodes} SyncData rows have no matching Products row`,
  );

  failUnless(report.candidate.numericWeightRows > 0, 'no numeric weight values were loaded in column C');
  failUnless(report.candidate.numericRateRows > 0, 'no numeric foreign-price values were loaded in column F');
  failUnless(report.candidate.numericPriceRows > 0, 'no numeric final prices were produced in column B');
  failUnless(
    report.candidate.priceFormulaRows === report.candidate.rowsWithCode,
    `column B formula coverage is ${report.candidate.priceFormulaRows}/${report.candidate.rowsWithCode}`,
  );

  const structuralFormulaRows = report.candidate.formulaSignatures
    .filter(({ formula }) => structurallyMatchesDynamicPriceFormula(formula))
    .reduce((total, { count }) => total + count, 0);
  report.candidate.structuralFormulaRows = structuralFormulaRows;
  failUnless(
    structuralFormulaRows === report.candidate.rowsWithCode,
    `column B does not consistently use the dynamic per-row SyncData price calculation (${structuralFormulaRows}/${report.candidate.rowsWithCode})`,
  );

  failUnless(
    report.candidate.completeInputRows > 0,
    'no rows have enough per-row SyncData inputs for an independent price calculation',
  );
  failUnless(
    report.candidate.missingPriceForCompleteRows === 0,
    `${report.candidate.missingPriceForCompleteRows} complete rows have a blank/non-numeric final price`,
  );
  failUnless(
    report.candidate.priceMismatchRows === 0,
    `${report.candidate.priceMismatchRows} final prices differ from the independently rounded per-row SyncData calculation`,
  );
  failUnless(
    report.errors.naCount === 0,
    `candidate contains ${report.errors.naCount} #N/A cells`,
  );
  failUnless(
    report.errors.valueCount === 0,
    `candidate contains ${report.errors.valueCount} #VALUE! cells`,
  );

  failUnless(
    report.comparison.overlapRows > 0,
    'candidate and archive have no product-code overlap to compare',
  );
  if (report.comparison.weightComparable > 0) {
    failUnless(
      report.comparison.weightMatches > 0,
      'none of the comparable archived weights match the synced workbook',
    );
  }
  if (report.comparison.rateComparable > 0) {
    failUnless(
      report.comparison.rateMatches > 0,
      'none of the comparable archived foreign prices match the synced workbook',
    );
  }

  if (options.strictReference) {
    failUnless(
      report.comparison.weightDifferences === 0 && report.comparison.weightMissing === 0,
      `strict archive comparison found ${report.comparison.weightDifferences} changed and ${report.comparison.weightMissing} missing weights`,
    );
    failUnless(
      report.comparison.rateDifferences === 0 && report.comparison.rateMissing === 0,
      `strict archive comparison found ${report.comparison.rateDifferences} changed and ${report.comparison.rateMissing} missing foreign prices`,
    );
  }

  return failures;
}

function printHumanReport(report) {
  const status = report.passed ? 'PASS' : 'FAIL';
  console.log(`${status}: ${report.candidate.path}`);
  console.log(`  Sync: ${report.sync.requested ? 'requested' : 'skipped'}${report.sync.ran ? ', completed' : ''}`);
  console.log(`  Edition: ${report.candidate.edition}`);
  console.log(`  Headers: ${report.candidate.headers.join(' | ')}`);
  console.log(`  Product rows: ${report.candidate.rowsWithCode}`);
  console.log(
    `  Live config: yuan=${report.candidate.config.yuan}, shipping=${report.candidate.config.shipping}, profit=${report.candidate.config.profit}`,
  );
  console.log(
    `  SyncData: rows=${report.syncData.rowsWithCode}, hidden=${report.syncData.sheetVisibility === 2}, missing=${report.syncData.missingProductCodes}, extra=${report.syncData.extraCodes}`,
  );
  console.log(
    `  Inputs/prices: weight=${report.candidate.numericWeightRows}, rate=${report.candidate.numericRateRows}, final=${report.candidate.numericPriceRows}, independently comparable=${report.candidate.completeInputRows}, incomplete SyncData=${report.candidate.unverifiableForeignPriceRows}`,
  );
  console.log(
    `  Formula: present=${report.candidate.priceFormulaRows}, structural=${report.candidate.structuralFormulaRows}, mismatches=${report.candidate.priceMismatchRows}`,
  );
  console.log(`  Errors: #N/A=${report.errors.naCount}, #VALUE!=${report.errors.valueCount}`);
  console.log(
    `  Archive overlap: rows=${report.comparison.overlapRows}, weight matches=${report.comparison.weightMatches}/${report.comparison.weightComparable}, rate matches=${report.comparison.rateMatches}/${report.comparison.rateComparable}`,
  );

  if (!report.passed) {
    for (const failure of report.failures) console.error(`  - ${failure}`);
  }
}

const powershell = String.raw`
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$candidatePath = $env:PATRIS_VALIDATOR_CANDIDATE
$referencePath = $env:PATRIS_VALIDATOR_REFERENCE
$runSync = $env:PATRIS_VALIDATOR_SYNC -eq '1'
$invariant = [System.Globalization.CultureInfo]::InvariantCulture

function Release-ComObject([object]$value) {
    if ($null -ne $value -and [Runtime.InteropServices.Marshal]::IsComObject($value)) {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($value)
    }
}

function Matrix-Value([object]$matrix, [int]$rows, [int]$columns, [int]$row, [int]$column) {
    if ($rows -eq 1 -and $columns -eq 1) {
        return $matrix
    }
    return $matrix[$row, $column]
}

function Numeric-Or-Null([object]$value) {
    if ($null -eq $value -or $value -is [System.DBNull] -or $value -eq '') {
        return $null
    }
    if ($value -is [int] -and $value -le -2146826000) {
        return $null
    }
    try {
        return [Convert]::ToDouble($value, $invariant)
    } catch {
        return $null
    }
}

function Normalized-Code([object]$value) {
    if ($null -eq $value -or $value -is [System.DBNull]) {
        return ''
    }
    if ($value -is [byte] -or $value -is [int16] -or $value -is [int32] -or
        $value -is [int64] -or $value -is [single] -or $value -is [double] -or
        $value -is [decimal]) {
        $number = [Convert]::ToDouble($value, $invariant)
        if ([Math]::Abs($number - [Math]::Round($number)) -le 0.000000001) {
            return $number.ToString('0', $invariant)
        }
        return $number.ToString('G17', $invariant)
    }
    return ([Convert]::ToString($value, $invariant)).Trim()
}

function Find-Table([object]$book, [string]$name) {
    for ($sheetIndex = 1; $sheetIndex -le $book.Worksheets.Count; $sheetIndex++) {
        $sheet = $book.Worksheets.Item($sheetIndex)
        try {
            for ($tableIndex = 1; $tableIndex -le $sheet.ListObjects.Count; $tableIndex++) {
                $table = $sheet.ListObjects.Item($tableIndex)
                if ([string]$table.Name -ceq $name) {
                    return ,$table
                }
                Release-ComObject $table
            }
        } finally {
            Release-ComObject $sheet
        }
    }
    throw "Workbook '$($book.Name)' does not contain table '$name'."
}

function Table-Scalar([object]$book, [string]$name) {
    $table = Find-Table $book $name
    $data = $null
    $cell = $null
    try {
        $data = $table.DataBodyRange
        if ($null -eq $data) {
            return $null
        }
        $cell = $data.Cells.Item(1, 1)
        return $cell.Value2
    } finally {
        Release-ComObject $cell
        Release-ComObject $data
        Release-ComObject $table
    }
}

function Sheet-Scalar([object]$book, [int]$sheetIndex, [string]$address) {
    $sheet = $null
    $cell = $null
    try {
        $sheet = $book.Worksheets.Item($sheetIndex)
        $cell = $sheet.Range($address)
        return $cell.Value2
    } finally {
        Release-ComObject $cell
        Release-ComObject $sheet
    }
}

function Read-Products([object]$book) {
    $table = Find-Table $book 'Products'
    $headerRange = $null
    $dataRange = $null
    try {
        $headerRange = $table.HeaderRowRange
        $headerColumns = $headerRange.Columns.Count
        $headerValues = $headerRange.Value2
        $headers = @()
        for ($column = 1; $column -le $headerColumns; $column++) {
            $headers += [Convert]::ToString(
                (Matrix-Value $headerValues 1 $headerColumns 1 $column),
                $invariant
            )
        }

        $rows = @()
        $formulaCounts = @{}
        $dataRange = $table.DataBodyRange
        if ($null -ne $dataRange) {
            $rowCount = $dataRange.Rows.Count
            $columnCount = $dataRange.Columns.Count
            $values = $dataRange.Value2
            $formulas = $dataRange.FormulaR1C1
            for ($row = 1; $row -le $rowCount; $row++) {
                $price = Matrix-Value $values $rowCount $columnCount $row 1
                $weight = if ($columnCount -ge 2) {
                    Matrix-Value $values $rowCount $columnCount $row 2
                } else { $null }
                $rate = if ($columnCount -ge 5) {
                    Matrix-Value $values $rowCount $columnCount $row 5
                } else { $null }
                $code = if ($columnCount -ge 7) {
                    Normalized-Code (Matrix-Value $values $rowCount $columnCount $row 7)
                } else { '' }
                $formula = [Convert]::ToString(
                    (Matrix-Value $formulas $rowCount $columnCount $row 1),
                    $invariant
                )
                if ($code.Length -gt 0) {
                    if ($formula.StartsWith('=')) {
                        if (-not $formulaCounts.ContainsKey($formula)) {
                            $formulaCounts[$formula] = 0
                        }
                        $formulaCounts[$formula] += 1
                    }
                    $rows += [pscustomobject]@{
                        Row = [int]$table.Range.Row + $row
                        Code = $code
                        Price = $price
                        Weight = $weight
                        Rate = $rate
                        Formula = $formula
                    }
                }
            }
        }

        $signatures = @(
            $formulaCounts.GetEnumerator() |
                Sort-Object -Property Value -Descending |
                ForEach-Object {
                    [pscustomobject]@{ formula = [string]$_.Key; count = [int]$_.Value }
                }
        )
        return [pscustomobject]@{
            Headers = $headers
            TableAddress = [string]$table.Range.Address($false, $false)
            Rows = @($rows)
            FormulaSignatures = $signatures
        }
    } finally {
        Release-ComObject $dataRange
        Release-ComObject $headerRange
        Release-ComObject $table
    }
}

function Read-SyncData([object]$book) {
    $table = Find-Table $book 'SyncData'
    $headerRange = $null
    $dataRange = $null
    $sheet = $null
    try {
        $sheet = $table.Parent
        $headerRange = $table.HeaderRowRange
        $headerColumns = $headerRange.Columns.Count
        $headerValues = $headerRange.Value2
        $headers = @()
        for ($column = 1; $column -le $headerColumns; $column++) {
            $headers += [Convert]::ToString(
                (Matrix-Value $headerValues 1 $headerColumns 1 $column),
                $invariant
            )
        }

        $rows = @()
        $dataRange = $table.DataBodyRange
        if ($null -ne $dataRange) {
            $rowCount = $dataRange.Rows.Count
            $columnCount = $dataRange.Columns.Count
            $values = $dataRange.Value2
            for ($row = 1; $row -le $rowCount; $row++) {
                $code = Normalized-Code (
                    Matrix-Value $values $rowCount $columnCount $row 1
                )
                if ($code.Length -gt 0) {
                    $rows += [pscustomobject]@{
                        Code = $code
                        Currency = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 2),
                            $invariant
                        ).Trim()
                        Shipping = Matrix-Value $values $rowCount $columnCount $row 3
                        ShippingCurrency = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 4),
                            $invariant
                        ).Trim()
                        ProfitPercent = Matrix-Value $values $rowCount $columnCount $row 5
                        CNYRate = Matrix-Value $values $rowCount $columnCount $row 6
                        USDRate = Matrix-Value $values $rowCount $columnCount $row 7
                    }
                }
            }
        }

        return [pscustomobject]@{
            Headers = $headers
            Rows = @($rows)
            SheetVisibility = [int]$sheet.Visible
        }
    } finally {
        Release-ComObject $sheet
        Release-ComObject $dataRange
        Release-ComObject $headerRange
        Release-ComObject $table
    }
}

function Currency-Factor-Or-Null([object]$currencyValue, [object]$syncRow) {
    $currency = [Convert]::ToString($currencyValue, $invariant).Trim().ToUpperInvariant()
    switch ($currency) {
        'CNY' {
            $rate = Numeric-Or-Null $syncRow.CNYRate
            if ($null -ne $rate -and $rate -gt 0) { return $rate }
            return $null
        }
        'USD' {
            $rate = Numeric-Or-Null $syncRow.USDRate
            if ($null -ne $rate -and $rate -gt 0) { return $rate }
            return $null
        }
        'IRR' { return 0.1 }
        'IRT' { return 1.0 }
        default { return $null }
    }
}

function Workbook-Errors([object]$book) {
    $naCount = 0
    $valueCount = 0
    $samples = @()
    for ($sheetIndex = 1; $sheetIndex -le $book.Worksheets.Count; $sheetIndex++) {
        $sheet = $book.Worksheets.Item($sheetIndex)
        $used = $null
        try {
            $used = $sheet.UsedRange
            $rows = $used.Rows.Count
            $columns = $used.Columns.Count
            $values = $used.Value2
            for ($row = 1; $row -le $rows; $row++) {
                for ($column = 1; $column -le $columns; $column++) {
                    $value = Matrix-Value $values $rows $columns $row $column
                    $label = $null
                    $isExcelErrorCode = $value -is [int] -or $value -is [long]
                    if ($isExcelErrorCode -and [long]$value -eq -2146826246) {
                        $naCount += 1
                        $label = '#N/A'
                    } elseif ($isExcelErrorCode -and [long]$value -eq -2146826273) {
                        $valueCount += 1
                        $label = '#VALUE!'
                    }
                    if ($null -ne $label -and $samples.Count -lt 20) {
                        $cell = $sheet.Cells.Item(
                            [int]$used.Row + $row - 1,
                            [int]$used.Column + $column - 1
                        )
                        try {
                            $samples += "$($sheet.Name)!$($cell.Address($false, $false))=$label"
                        } finally {
                            Release-ComObject $cell
                        }
                    }
                }
            }
        } finally {
            Release-ComObject $used
            Release-ComObject $sheet
        }
    }
    return [pscustomobject]@{
        naCount = $naCount
        valueCount = $valueCount
        samples = $samples
    }
}

function Row-Dictionary([object[]]$rows) {
    $dictionary = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal
    )
    $duplicates = 0
    foreach ($row in $rows) {
        if ($dictionary.ContainsKey($row.Code)) {
            $duplicates += 1
        } else {
            $dictionary.Add($row.Code, $row)
        }
    }
    return [pscustomobject]@{ Values = $dictionary; Duplicates = $duplicates }
}

$excel = $null
$candidateBook = $null
$referenceBook = $null
$syncRan = $false
try {
    $excel = New-Object -ComObject Excel.Application
    $excel.Visible = $false
    $excel.DisplayAlerts = $false
    $excel.AskToUpdateLinks = $false
    $excel.EnableEvents = $false

    if ($runSync) {
        $excel.AutomationSecurity = 1
    } else {
        $excel.AutomationSecurity = 3
    }

    if ([IO.Path]::GetExtension($candidatePath).ToLowerInvariant() -eq '.xltm') {
        $candidateBook = $excel.Workbooks.Add($candidatePath)
    } else {
        $candidateBook = $excel.Workbooks.Open($candidatePath, 0, $true)
    }

    if ($runSync) {
        $macroBookName = ([string]$candidateBook.Name).Replace("'", "''")
        [void]$excel.Run("'$macroBookName'!ProductCatalogSync.RefreshAllData", $true)
        $syncRan = $true
    }
    $excel.CalculateFullRebuild()
    $candidateSyncStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 2 'B6'),
        $invariant
    )
    $candidatePricingStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 2 'B23'),
        $invariant
    )

    $candidateProducts = Read-Products $candidateBook
    $candidateSyncData = Read-SyncData $candidateBook
    $candidateConfig = [pscustomobject]@{
        yuan = Numeric-Or-Null (Table-Scalar $candidateBook 'Yuan_Price')
        shipping = Numeric-Or-Null (Table-Scalar $candidateBook 'Shipping')
        profit = Numeric-Or-Null (Table-Scalar $candidateBook 'Profit')
    }
    $errors = Workbook-Errors $candidateBook

    $excel.AutomationSecurity = 3
    $referenceBook = $excel.Workbooks.Open($referencePath, 0, $true)
    $referenceProducts = Read-Products $referenceBook
    $referenceConfig = [pscustomobject]@{
        yuan = Numeric-Or-Null (Table-Scalar $referenceBook 'Yuan_Price')
        shipping = Numeric-Or-Null (Table-Scalar $referenceBook 'Shipping')
        profit = Numeric-Or-Null (Table-Scalar $referenceBook 'Profit')
    }

    $candidateDictionary = Row-Dictionary $candidateProducts.Rows
    $syncDictionary = Row-Dictionary $candidateSyncData.Rows
    $referenceDictionary = Row-Dictionary $referenceProducts.Rows
    $missingProductCodes = 0
    $extraSyncCodes = 0
    foreach ($candidateCode in $candidateDictionary.Values.Keys) {
        if (-not $syncDictionary.Values.ContainsKey($candidateCode)) {
            $missingProductCodes += 1
        }
    }
    foreach ($syncCode in $syncDictionary.Values.Keys) {
        if (-not $candidateDictionary.Values.ContainsKey($syncCode)) {
            $extraSyncCodes += 1
        }
    }

    $numericWeightRows = 0
    $numericRateRows = 0
    $numericPriceRows = 0
    $priceFormulaRows = 0
    $completeInputRows = 0
    $unverifiableForeignPriceRows = 0
    $missingPriceForCompleteRows = 0
    $priceMismatchRows = 0
    $priceMismatchSamples = @()

    foreach ($row in $candidateProducts.Rows) {
        $weight = Numeric-Or-Null $row.Weight
        $rate = Numeric-Or-Null $row.Rate
        $price = Numeric-Or-Null $row.Price
        if ($null -ne $weight) { $numericWeightRows += 1 }
        if ($null -ne $rate) { $numericRateRows += 1 }
        if ($null -ne $price) { $numericPriceRows += 1 }
        if ([string]$row.Formula -like '=*') { $priceFormulaRows += 1 }

        if ($null -eq $rate -or -not $syncDictionary.Values.ContainsKey($row.Code)) {
            continue
        }

        $syncRow = $syncDictionary.Values[$row.Code]
        $productFactor = Currency-Factor-Or-Null $syncRow.Currency $syncRow
        $shipping = Numeric-Or-Null $syncRow.Shipping
        $profitPercent = Numeric-Or-Null $syncRow.ProfitPercent
        if ($null -eq $profitPercent) { $profitPercent = [decimal]0 }
        $shippingReady = $true
        $shippingComponent = [decimal]0
        if ($null -ne $weight -and $null -ne $shipping) {
            $shippingFactor = Currency-Factor-Or-Null $syncRow.ShippingCurrency $syncRow
            if ($null -eq $shippingFactor) {
                $shippingReady = $false
            } else {
                $shippingComponent = (
                    ([decimal]$weight / [decimal]1000) *
                    [decimal]$shipping *
                    [decimal]$shippingFactor
                )
            }
        }

        if ($null -eq $productFactor -or -not $shippingReady) {
            $unverifiableForeignPriceRows += 1
            continue
        }

        $completeInputRows += 1
        $expectedUnrounded = (
            ([decimal]$rate * [decimal]$productFactor) +
            [decimal]$shippingComponent
        ) * (
            [decimal]1 + ([decimal]$profitPercent / [decimal]100)
        )
        $expected = [Math]::Round(
            $expectedUnrounded,
            0,
            [System.MidpointRounding]::AwayFromZero
        )
        if ($null -eq $price) {
            $missingPriceForCompleteRows += 1
            if ($priceMismatchSamples.Count -lt 20) {
                $priceMismatchSamples += "$($row.Code): expected=$expected, got blank/error"
            }
        } elseif ([Math]::Abs($price - $expected) -gt 0.01) {
            $priceMismatchRows += 1
            if ($priceMismatchSamples.Count -lt 20) {
                $priceMismatchSamples += (
                    "$($row.Code): got=$price expected=$expected " +
                    "currency=$($syncRow.Currency) shipping_currency=$($syncRow.ShippingCurrency) " +
                    "profit_percent=$profitPercent"
                )
            }
        }
    }

    $overlapRows = 0
    $weightComparable = 0
    $weightMatches = 0
    $weightDifferences = 0
    $weightMissing = 0
    $rateComparable = 0
    $rateMatches = 0
    $rateDifferences = 0
    $rateMissing = 0
    $comparisonSamples = @()

    foreach ($entry in $referenceDictionary.Values.GetEnumerator()) {
        if (-not $candidateDictionary.Values.ContainsKey($entry.Key)) {
            continue
        }
        $overlapRows += 1
        $referenceRow = $entry.Value
        $candidateRow = $candidateDictionary.Values[$entry.Key]

        $referenceWeight = Numeric-Or-Null $referenceRow.Weight
        if ($null -ne $referenceWeight) {
            $weightComparable += 1
            $candidateWeight = Numeric-Or-Null $candidateRow.Weight
            if ($null -eq $candidateWeight) {
                $weightMissing += 1
                if ($comparisonSamples.Count -lt 200) {
                    $comparisonSamples += "$($entry.Key): archived weight=$referenceWeight, candidate blank"
                }
            } elseif ([Math]::Abs($candidateWeight - $referenceWeight) -le 0.000000001) {
                $weightMatches += 1
            } else {
                $weightDifferences += 1
                if ($comparisonSamples.Count -lt 200) {
                    $comparisonSamples += "$($entry.Key): weight archived=$referenceWeight candidate=$candidateWeight"
                }
            }
        }

        $referenceRate = Numeric-Or-Null $referenceRow.Rate
        if ($null -ne $referenceRate) {
            $rateComparable += 1
            $candidateRate = Numeric-Or-Null $candidateRow.Rate
            if ($null -eq $candidateRate) {
                $rateMissing += 1
                if ($comparisonSamples.Count -lt 200) {
                    $comparisonSamples += "$($entry.Key): archived rate=$referenceRate, candidate blank"
                }
            } elseif ([Math]::Abs($candidateRate - $referenceRate) -le 0.000000001) {
                $rateMatches += 1
            } else {
                $rateDifferences += 1
                if ($comparisonSamples.Count -lt 200) {
                    $comparisonSamples += "$($entry.Key): rate archived=$referenceRate candidate=$candidateRate"
                }
            }
        }
    }

    $report = [pscustomobject]@{
        sync = [pscustomobject]@{
            requested = $runSync
            ran = $syncRan
        }
        candidate = [pscustomobject]@{
            path = $candidatePath
            workbookName = [string]$candidateBook.Name
            syncStatus = $candidateSyncStatus
            pricingStatus = $candidatePricingStatus
            tableAddress = $candidateProducts.TableAddress
            headers = $candidateProducts.Headers
            rowsWithCode = @($candidateProducts.Rows).Count
            duplicateCodes = $candidateDictionary.Duplicates
            config = $candidateConfig
            numericWeightRows = $numericWeightRows
            numericRateRows = $numericRateRows
            numericPriceRows = $numericPriceRows
            priceFormulaRows = $priceFormulaRows
            completeInputRows = $completeInputRows
            unverifiableForeignPriceRows = $unverifiableForeignPriceRows
            missingPriceForCompleteRows = $missingPriceForCompleteRows
            priceMismatchRows = $priceMismatchRows
            priceMismatchSamples = $priceMismatchSamples
            formulaSignatures = $candidateProducts.FormulaSignatures
        }
        syncData = [pscustomobject]@{
            headers = $candidateSyncData.Headers
            sheetVisibility = $candidateSyncData.SheetVisibility
            rowsWithCode = @($candidateSyncData.Rows).Count
            duplicateCodes = $syncDictionary.Duplicates
            missingProductCodes = $missingProductCodes
            extraCodes = $extraSyncCodes
        }
        reference = [pscustomobject]@{
            path = $referencePath
            tableAddress = $referenceProducts.TableAddress
            headers = $referenceProducts.Headers
            rowsWithCode = @($referenceProducts.Rows).Count
            duplicateCodes = $referenceDictionary.Duplicates
            config = $referenceConfig
        }
        errors = $errors
        comparison = [pscustomobject]@{
            overlapRows = $overlapRows
            weightComparable = $weightComparable
            weightMatches = $weightMatches
            weightDifferences = $weightDifferences
            weightMissing = $weightMissing
            rateComparable = $rateComparable
            rateMatches = $rateMatches
            rateDifferences = $rateDifferences
            rateMissing = $rateMissing
            samples = $comparisonSamples
        }
    }
    [Console]::Out.WriteLine(($report | ConvertTo-Json -Depth 10 -Compress))
} finally {
    if ($null -ne $referenceBook) {
        try { $referenceBook.Close($false) } catch {}
    }
    if ($null -ne $candidateBook) {
        try { $candidateBook.Close($false) } catch {}
    }
    if ($null -ne $excel) {
        try { $excel.EnableEvents = $true } catch {}
        try { $excel.Quit() } catch {}
    }
    Release-ComObject $referenceBook
    Release-ComObject $candidateBook
    Release-ComObject $excel
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}
`;

function main() {
  let options;
  try {
    options = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    console.error(usage());
    process.exitCode = 2;
    return;
  }

  if (options.help) {
    console.log(usage());
    return;
  }
  if (process.platform !== 'win32') {
    console.error('This validator requires Windows, Microsoft Excel, and powershell.exe.');
    process.exitCode = 2;
    return;
  }
  for (const [label, filePath] of [
    ['candidate', options.candidate],
    ['reference', options.reference],
  ]) {
    if (!fs.existsSync(filePath)) {
      console.error(`${label} workbook does not exist: ${filePath}`);
      process.exitCode = 2;
      return;
    }
  }

  const result = spawnSync(
    'powershell.exe',
    ['-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', '-'],
    {
      cwd: repoRoot,
      encoding: 'utf8',
      env: {
        ...process.env,
        PATRIS_VALIDATOR_CANDIDATE: options.candidate,
        PATRIS_VALIDATOR_REFERENCE: options.reference,
        PATRIS_VALIDATOR_SYNC: options.sync ? '1' : '0',
      },
      input: `${powershell}\n`,
      maxBuffer: 16 * 1024 * 1024,
      timeout: options.timeoutMs,
      windowsHide: true,
    },
  );

  if (result.error) {
    const suffix = result.error.code === 'ETIMEDOUT'
      ? ` after ${options.timeoutMs} ms`
      : '';
    console.error(`Excel validation could not run${suffix}: ${result.error.message}`);
    process.exitCode = 2;
    return;
  }
  if (result.status !== 0) {
    console.error('Excel validation process failed.');
    if (result.stderr.trim()) console.error(result.stderr.trim());
    if (result.stdout.trim()) console.error(result.stdout.trim());
    process.exitCode = 2;
    return;
  }

  let report;
  try {
    const output = result.stdout.replace(/^\uFEFF/u, '').trim();
    report = JSON.parse(output);
  } catch (error) {
    console.error(`Excel validation returned invalid JSON: ${error.message}`);
    if (result.stdout.trim()) console.error(result.stdout.trim());
    if (result.stderr.trim()) console.error(result.stderr.trim());
    process.exitCode = 2;
    return;
  }

  report.failures = buildFailures(report, options);
  report.passed = report.failures.length === 0;
  report.strictReference = options.strictReference;

  if (options.json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    printHumanReport(report);
  }
  if (!report.passed) process.exitCode = 1;
}

main();
