'use strict';

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const REFERENCE_HEADERS = Object.freeze([
  'فی فروش',
  'گرم',
  'سایر',
  'فی فروش2',
  'نرخ ارزی',
  'همه انبارها',
  'کد کالا',
  'نام کالا',
]);

const CANONICAL_HEADERS = Object.freeze([
  'قیمت فروش (تومان)',
  'وزن کالا (گرم)',
  'سایر',
  'محل کالا',
  'قیمت خرید (یوآن)',
  'موجودی کل',
  'کد کالا',
  'نام کالا',
  'شناسه ووکامرس',
  'دسته‌بندی',
]);

const SYNC_DATA_HEADERS = Object.freeze([
  'کلید همگام‌سازی',
  'ارز کالا',
  'نرخ حمل هر کیلو',
  'ارز حمل',
  'حاشیه سود (درصد)',
  'بهای یوآن',
  'بهای دلار',
  'تاریخ نرخ',
  'شناسه ووکامرس',
  'قیمت مشتری ووکامرس',
  'آخرین تغییر ووکامرس',
  'بازبینی رکورد',
  'نشانی محصول',
  'حاشیه سود کالا',
  'قیمت محاسباتی کالا',
  'قیمت ویژه ووکامرس (ممیزی)',
  'دسته‌بندی',
  'وضعیت انتشار',
  'هشدار قیمت',
  'نوع ردیف',
]);

const REGRESSION_ACCEPTANCE = Object.freeze({
  relayProductCode: '109032',
  relayPrice: 554500,
  relayCategory: 'رله‌ها',
  wooFallbackProductCode: '109001',
  wooFallbackPrice: 1150000,
});

const repoRoot = path.resolve(__dirname, '..', '..');
const defaultCandidate = path.join(
  repoRoot,
  'docs',
  'examples',
  'لیست قیمت دیجیتالاجیک.xltm',
);
const defaultReference = path.join(
  os.homedir(),
  'Documents',
  'Excel',
  'Archive',
  'Price Calculator',
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
    'IF(RC[8]<>"","WOO:"&RC[8],"PATRIS:"&RC[6])',
    'SYNCDATA,2,FALSE)',
    'SYNCDATA,3,FALSE)',
    'SYNCDATA,4,FALSE)',
    'SYNCDATA,5,FALSE)',
    'SYNCDATA,6,FALSE)',
    'SYNCDATA,7,FALSE)',
    'SYNCDATA,10,FALSE)',
    'SYNCDATA,20,FALSE)',
  ];
  const settingsSheet = '\u062A\u0646\u0638\u06CC\u0645\u0627\u062A';
  const hasRoundingReference = normalized.includes(`${settingsSheet}!R15C2`)
    || normalized.includes(`'${settingsSheet}'!R15C2`);
  return hasRoundingReference
    && requiredTokens.every((token) => normalized.includes(token));
}

function isSHA256Revision(value) {
  return /^sha256:[0-9a-f]{64}$/u.test(String(value || ''));
}

function sameFiniteNumber(left, right, tolerance = 1e-9) {
  return typeof left === 'number'
    && Number.isFinite(left)
    && typeof right === 'number'
    && Number.isFinite(right)
    && Math.abs(left - right) <= tolerance;
}

function buildFailures(report, options) {
  const failures = [];
  const failUnless = (condition, message) => {
    if (!condition) failures.push(message);
  };

  if (options.sync) {
    failUnless(
      report.sync.requested && report.sync.ran && report.sync.succeeded,
      `the requested native Excel synchronization did not succeed: ${report.candidate.syncStatus}`,
    );
  }
  failUnless(
    sameArray(report.reference.headers, REFERENCE_HEADERS),
    `archived headers changed: ${JSON.stringify(report.reference.headers)}`,
  );
  const isCanonical = sameArray(report.candidate.headers, CANONICAL_HEADERS);
  report.candidate.edition = isCanonical ? 'Canonical' : 'Unknown';
  failUnless(
    isCanonical,
    `candidate headers must exactly match the canonical Persian contract: ${JSON.stringify(report.candidate.headers)}`,
  );
  failUnless(
    sameArray(report.syncData.headers, SYNC_DATA_HEADERS),
    `SyncData headers changed: ${JSON.stringify(report.syncData.headers)}`,
  );
  failUnless(
    report.syncData.sheetVisibility === 2,
    `SyncData must remain xlSheetVeryHidden (2); got ${report.syncData.sheetVisibility}`,
  );
  const expectedTableAddress = isCanonical ? /^B5:K\d+$/u : /(?!)/u;
  failUnless(
    expectedTableAddress.test(report.candidate.tableAddress),
    `${report.candidate.edition} Products table has an unexpected address: ${report.candidate.tableAddress}`,
  );
  failUnless(report.candidate.rowsWithCode > 0, 'Sync produced no product rows');
  failUnless(
    report.candidate.wooOnlyRows > 0,
    'the reconciled table contains no WooCommerce-only rows',
  );
  failUnless(
    report.candidate.ambiguousRows === 0,
    `${report.candidate.ambiguousRows} reconciled rows have an ambiguous WooCommerce identity`,
  );
  failUnless(
    typeof report.candidate.fullWooRows === 'number'
      && typeof report.candidate.wooLeafRows === 'number'
      && report.candidate.fullWooRows >= report.candidate.wooLeafRows
      && report.candidate.wooLeafRows > 0,
    `invalid WooCommerce raw/leaf coverage (${report.candidate.fullWooRows}/${report.candidate.wooLeafRows})`,
  );
  failUnless(
    report.candidate.matchedRows
      + report.candidate.sourceOnlyRows
      + report.candidate.wooOnlyRows
      + report.candidate.ambiguousRows === report.candidate.rowsWithCode,
    'reconciled row-kind counts do not equal the Products row count',
  );
  failUnless(
    isSHA256Revision(report.candidate.datasetRevision),
    `catalog dataset_revision is missing or invalid: ${report.candidate.datasetRevision}`,
  );
  failUnless(
    isSHA256Revision(report.candidate.sourceRevision),
    `catalog source revision is missing or invalid: ${report.candidate.sourceRevision}`,
  );
  failUnless(
    report.candidate.paginationTotal === report.candidate.rowsWithCode,
    `validated pagination total differs from Products (${report.candidate.paginationTotal}/${report.candidate.rowsWithCode})`,
  );
  failUnless(
    typeof report.candidate.countSignature === 'string'
      && report.candidate.countSignature.length > 0,
    'the repeated reconciliation-count signature was not persisted after a complete snapshot read',
  );
  failUnless(
    report.candidate.patrisRows
      === report.candidate.matchedRows + report.candidate.sourceOnlyRows,
    `Patris count does not equal matched + Patris-only (${report.candidate.patrisRows} != ${report.candidate.matchedRows} + ${report.candidate.sourceOnlyRows})`,
  );
  failUnless(
    report.candidate.wooLeafRows
      === report.candidate.matchedRows + report.candidate.wooOnlyRows,
    `Woo leaf count does not equal matched + Woo-only (${report.candidate.wooLeafRows} != ${report.candidate.matchedRows} + ${report.candidate.wooOnlyRows})`,
  );
  failUnless(
    report.candidate.fullWooRows
      === report.candidate.wooLeafRows + report.candidate.excludedWooParentRows,
    `raw Woo count does not equal leaves + excluded variable parents (${report.candidate.fullWooRows} != ${report.candidate.wooLeafRows} + ${report.candidate.excludedWooParentRows})`,
  );
  const expectedCountSignature = [
    `patris_products=${report.candidate.patrisRows}`,
    `woocommerce_raw=${report.candidate.fullWooRows}`,
    `woocommerce_leaves=${report.candidate.wooLeafRows}`,
    `union_rows=${report.candidate.rowsWithCode}`,
    `matched=${report.candidate.matchedRows}`,
    `patris_only=${report.candidate.sourceOnlyRows}`,
    `woo_only=${report.candidate.wooOnlyRows}`,
    `ambiguous_codes=${report.candidate.ambiguousRows}`,
    `variable_parents_excluded=${report.candidate.excludedWooParentRows}`,
  ].join('|');
  failUnless(
    report.candidate.countSignature === expectedCountSignature,
    `persisted reconciliation signature differs from the imported rows: ${report.candidate.countSignature}`,
  );
  failUnless(
    report.candidate.duplicateCodes === 0,
    `candidate contains ${report.candidate.duplicateCodes} duplicate product codes`,
  );
  failUnless(
    report.candidate.invalidSyncIdentityRows === 0,
    `${report.candidate.invalidSyncIdentityRows} visible rows do not preserve the authoritative sync_key identity`,
  );
  failUnless(
    report.candidate.wooOnlySKUFallbackRows > 0,
    'no WooCommerce-only row exposes its available SKU in کد کالا',
  );
  failUnless(
    typeof report.candidate.config.yuan === 'number'
      && Number.isFinite(report.candidate.config.yuan)
      && report.candidate.config.yuan > 0,
    `live Yuan_Price must be a positive number; got ${report.candidate.config.yuan}`,
  );
  failUnless(
    typeof report.candidate.config.usd === 'number'
      && Number.isFinite(report.candidate.config.usd)
      && report.candidate.config.usd > 0,
    `live USD price must be a positive number; got ${report.candidate.config.usd}`,
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
    Number.isInteger(report.candidate.config.roundingDigits)
      && report.candidate.config.roundingDigits >= 0
      && report.candidate.config.roundingDigits <= 9,
    `live rounding digits must be an integer from 0 through 9; got ${report.candidate.config.roundingDigits}`,
  );
  failUnless(
    sameFiniteNumber(
      report.candidate.config.yuan,
      report.candidate.config.cardYuan,
    ),
    `the visible Yuan card differs from Settings (${report.candidate.config.cardYuan}/${report.candidate.config.yuan})`,
  );
  failUnless(
    sameFiniteNumber(
      report.candidate.config.shipping,
      report.candidate.config.cardShipping,
    ),
    `the visible shipping card differs from Settings (${report.candidate.config.cardShipping}/${report.candidate.config.shipping})`,
  );
  failUnless(
    sameFiniteNumber(
      report.candidate.config.profit,
      report.candidate.config.cardProfit,
    ),
    `the visible profit card differs from Settings (${report.candidate.config.cardProfit}/${report.candidate.config.profit})`,
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
    report.candidate.fallbackPriceRows > 0,
    'no incomplete rows preserve an existing WooCommerce effective price',
  );
  failUnless(
    report.candidate.sourceOnlyUnsafeRows > 0,
    'validation data contains no incomplete source-only row for the fail-closed regression check',
  );
  failUnless(
    report.candidate.sourceOnlyUnsafePricedRows === 0,
    `${report.candidate.sourceOnlyUnsafePricedRows} incomplete source-only rows incorrectly produced a price`,
  );
  failUnless(
    report.candidate.missingExpectedPriceRows === 0,
    `${report.candidate.missingExpectedPriceRows} locally calculable or Woo-fallback rows have a blank/non-numeric final price`,
  );
  failUnless(
    report.candidate.priceMismatchRows === 0,
    `${report.candidate.priceMismatchRows} final prices differ from the independently rounded per-row SyncData calculation`,
  );
  failUnless(
    report.candidate.wooComparableRows > 0,
    'no workbook prices can be compared with WooCommerce effective prices',
  );
  failUnless(
    report.candidate.wooParityMismatchRows === 0,
    `${report.candidate.wooParityMismatchRows} workbook prices differ from WooCommerce effective customer prices`,
  );
  failUnless(
    report.search.query.length > 0
      && report.search.total > 1
      && report.search.total === report.search.expectedTotal
      && report.search.firstOrdinal === 1
      && report.search.secondOrdinal === 2,
    `product search did not expose and advance through multiple results: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.firstRow > 0
      && report.search.secondRow > 0
      && report.search.firstRow !== report.search.secondRow
      && report.search.firstRow === report.search.expectedFirstRow
      && report.search.secondRow === report.search.expectedSecondRow,
    `repeated product search did not advance to a different record: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.firstScrollColumn === report.search.expectedScrollColumn
      && report.search.secondScrollColumn === report.search.expectedScrollColumn,
    `product search did not preserve the padding column in view: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.wrapOrdinal === 1
      && report.search.wrapRow === report.search.firstRow,
    `product search did not wrap to the first result: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.clearedCaption === report.search.baseCaption,
    `clearing product search did not reset the button caption: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.regressions.relay109032.present
      && report.regressions.relay109032.price === REGRESSION_ACCEPTANCE.relayPrice
      && report.regressions.relay109032.category === REGRESSION_ACCEPTANCE.relayCategory,
    `${REGRESSION_ACCEPTANCE.relayProductCode} regression failed: ${JSON.stringify(report.regressions.relay109032)}`,
  );
  failUnless(
    report.regressions.wooFallback109001.present
      && report.regressions.wooFallback109001.price === REGRESSION_ACCEPTANCE.wooFallbackPrice
      && report.regressions.wooFallback109001.wooEffectivePrice === REGRESSION_ACCEPTANCE.wooFallbackPrice
      && report.regressions.wooFallback109001.warning.length > 0,
    `${REGRESSION_ACCEPTANCE.wooFallbackProductCode} Woo fallback regression failed: ${JSON.stringify(report.regressions.wooFallback109001)}`,
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
  const syncStatus = report.sync.requested
    ? (report.sync.ran
      ? (report.sync.succeeded ? 'requested, succeeded' : 'requested, failed')
      : 'requested, not run')
    : 'skipped';
  console.log(`  Sync: ${syncStatus}`);
  console.log(`  Edition: ${report.candidate.edition}`);
  console.log(`  Headers: ${report.candidate.headers.join(' | ')}`);
  console.log(`  Product rows: ${report.candidate.rowsWithCode}`);
  console.log(
    `  Reconciled rows: matched=${report.candidate.matchedRows}, Patris-only=${report.candidate.sourceOnlyRows}, Woo-only=${report.candidate.wooOnlyRows}, ambiguous=${report.candidate.ambiguousRows}, Woo fetched=${report.candidate.fullWooRows}, Woo leaves=${report.candidate.wooLeafRows}`,
  );
  console.log(
    `  Identity: invalid=${report.candidate.invalidSyncIdentityRows}, Woo-only SKU fallbacks=${report.candidate.wooOnlySKUFallbackRows}`,
  );
  console.log(
    `  Snapshot: dataset=${report.candidate.datasetRevision}, source=${report.candidate.sourceRevision}, total=${report.candidate.paginationTotal}`,
  );
  console.log(
    `  Live config: yuan=${report.candidate.config.yuan}, usd=${report.candidate.config.usd}, shipping=${report.candidate.config.shipping}, profit=${report.candidate.config.profit}, roundingDigits=${report.candidate.config.roundingDigits}; cards=${report.candidate.config.cardYuan}/${report.candidate.config.cardShipping}/${report.candidate.config.cardProfit}`,
  );
  console.log(
    `  SyncData: rows=${report.syncData.rowsWithCode}, hidden=${report.syncData.sheetVisibility === 2}, missing=${report.syncData.missingProductCodes}, extra=${report.syncData.extraCodes}`,
  );
  console.log(
    `  Inputs/prices: weight=${report.candidate.numericWeightRows}, rate=${report.candidate.numericRateRows}, final=${report.candidate.numericPriceRows}, independently comparable=${report.candidate.completeInputRows}, Woo fallback=${report.candidate.fallbackPriceRows}, intentionally blank=${report.candidate.intentionallyBlankRows}`,
  );
  console.log(
    `  Formula: present=${report.candidate.priceFormulaRows}, structural=${report.candidate.structuralFormulaRows}, mismatches=${report.candidate.priceMismatchRows}, Woo parity=${report.candidate.wooComparableRows - report.candidate.wooParityMismatchRows}/${report.candidate.wooComparableRows}`,
  );
  console.log(
    `  Search: query=${report.search.query}, results=${report.search.total}, rows=${report.search.firstRow}->${report.search.secondRow}->${report.search.wrapRow}, scroll=${report.search.firstScrollColumn}/${report.search.secondScrollColumn}`,
  );
  console.log(
    `  Fail-closed: unsafe source-only=${report.candidate.sourceOnlyUnsafeRows}, incorrectly priced=${report.candidate.sourceOnlyUnsafePricedRows}, missing expected=${report.candidate.missingExpectedPriceRows}`,
  );
  console.log(`  Errors: #N/A=${report.errors.naCount}, #VALUE!=${report.errors.valueCount}`);
  console.log(
    `  Archive overlap: rows=${report.comparison.overlapRows}, weight matches=${report.comparison.weightMatches}/${report.comparison.weightComparable}, rate matches=${report.comparison.rateMatches}/${report.comparison.rateComparable}`,
  );
  console.log(
    `  Regressions: 109032=${report.regressions.relay109032.price}/${report.regressions.relay109032.category}, 109001 fallback=${report.regressions.wooFallback109001.price}`,
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
                $productCode = if ($columnCount -ge 7) {
                    Normalized-Code (Matrix-Value $values $rowCount $columnCount $row 7)
                } else { '' }
                $wooID = if ($columnCount -ge 9) {
                    Normalized-Code (Matrix-Value $values $rowCount $columnCount $row 9)
                } else { '' }
                $productName = if ($columnCount -ge 8) {
                    [Convert]::ToString(
                        (Matrix-Value $values $rowCount $columnCount $row 8),
                        $invariant
                    ).Trim()
                } else { '' }
                $categories = if ($columnCount -ge 10) {
                    [Convert]::ToString(
                        (Matrix-Value $values $rowCount $columnCount $row 10),
                        $invariant
                    ).Trim()
                } else { '' }
                $identityKey = ''
                if ($wooID.Length -gt 0) {
                    $identityKey = "woo:$wooID"
                } elseif ($productCode.Length -gt 0) {
                    $identityKey = "patris:$productCode"
                }
                $formula = [Convert]::ToString(
                    (Matrix-Value $formulas $rowCount $columnCount $row 1),
                    $invariant
                )
                if ($identityKey.Length -gt 0) {
                    $searchValues = @()
                    for ($searchColumn = 1; $searchColumn -le $columnCount; $searchColumn++) {
                        $searchValues += [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row $searchColumn),
                            $invariant
                        )
                    }
                    if ($formula.StartsWith('=')) {
                        if (-not $formulaCounts.ContainsKey($formula)) {
                            $formulaCounts[$formula] = 0
                        }
                        $formulaCounts[$formula] += 1
                    }
                    $rows += [pscustomobject]@{
                        Row = [int]$table.Range.Row + $row
                        Code = $identityKey
                        ProductCode = $productCode
                        ProductName = $productName
                        WooID = $wooID
                        Categories = $categories
                        SearchText = ($searchValues -join [Environment]::NewLine)
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

function Search-MatchingRows(
    [object[]]$productRows,
    [string]$query
) {
    if ([string]::IsNullOrWhiteSpace($query)) {
        return @()
    }
    return @(
        $productRows |
            Where-Object {
                ([string]$_.SearchText).IndexOf(
                    $query,
                    [StringComparison]::OrdinalIgnoreCase
                ) -ge 0
            }
    )
}

function Select-SearchQuery([object[]]$productRows) {
    for ($prefixLength = 12; $prefixLength -ge 2; $prefixLength--) {
        $nameGroups = @(
            $productRows |
                Where-Object {
                    ([string]$_.ProductName).Length -ge $prefixLength
                } |
                Group-Object -Property {
                    ([string]$_.ProductName).Substring(
                        0,
                        $prefixLength
                    ).ToUpperInvariant()
                } |
                Where-Object { $_.Count -ge 2 -and $_.Count -le 20 } |
                Sort-Object -Property Count, Name
        )
        foreach ($group in $nameGroups) {
            $query = [string]$group.Name
            $matchingRows = @(Search-MatchingRows $productRows $query)
            if ($matchingRows.Count -ge 2 -and $matchingRows.Count -le 20) {
                return [pscustomobject]@{
                    Query = $query
                    Rows = $matchingRows
                }
            }
        }
    }
    for ($prefixLength = 8; $prefixLength -ge 2; $prefixLength--) {
        $groups = @(
            $productRows |
                Where-Object {
                    ([string]$_.ProductCode).Length -ge $prefixLength
                } |
                Group-Object -Property {
                    ([string]$_.ProductCode).Substring(
                        0,
                        $prefixLength
                    ).ToUpperInvariant()
                } |
                Where-Object { $_.Count -ge 2 -and $_.Count -le 20 } |
                Sort-Object -Property Count, Name
        )
        foreach ($group in $groups) {
            $query = [string]$group.Name
            $matchingRows = @(Search-MatchingRows $productRows $query)
            if ($matchingRows.Count -ge 2 -and $matchingRows.Count -le 20) {
                return [pscustomobject]@{
                    Query = $query
                    Rows = $matchingRows
                }
            }
        }
    }
    return $null
}

function Read-SearchButtonState(
    [object]$excel,
    [object]$searchButton,
    [int]$expectedScrollColumn
) {
    $caption = [Convert]::ToString(
        $searchButton.TextFrame2.TextRange.Text,
        $invariant
    )
    $captionMatch = [regex]::Match($caption, '\((\d+)/(\d+)\)$')
    return [pscustomobject]@{
        Caption = $caption
        Ordinal = if ($captionMatch.Success) {
            [int]$captionMatch.Groups[1].Value
        } else { 0 }
        Total = if ($captionMatch.Success) {
            [int]$captionMatch.Groups[2].Value
        } else { 0 }
        Row = [int]$excel.ActiveCell.Row
        ScrollColumn = [int]$excel.ActiveWindow.ScrollColumn
        ExpectedScrollColumn = $expectedScrollColumn
    }
}

function Test-ProductSearch(
    [object]$excel,
    [object]$book,
    [object[]]$productRows
) {
    $table = Find-Table $book 'Products'
    $sheet = $null
    $queryRange = $null
    $searchButton = $null
    try {
        $selection = Select-SearchQuery $productRows
        if ($null -eq $selection) {
            return [pscustomobject]@{
                query = ''
                total = 0
                expectedTotal = 0
                firstOrdinal = 0
                secondOrdinal = 0
                firstRow = 0
                secondRow = 0
                expectedFirstRow = 0
                expectedSecondRow = 0
                firstScrollColumn = 0
                secondScrollColumn = 0
                expectedScrollColumn = [Math]::Max(
                    1,
                    [int]$table.Range.Column - 1
                )
                wrapOrdinal = 0
                wrapRow = 0
                baseCaption = ''
                clearedCaption = ''
            }
        }
        $query = [string]$selection.Query
        $expectedRows = @($selection.Rows)
        $sheet = $table.Parent
        $queryRange = $book.Names.Item('ProductSearchQuery').RefersToRange
        $searchButton = $sheet.Shapes.Item('ProductSearchButton')
        $expectedScrollColumn = [Math]::Max(1, [int]$table.Range.Column - 1)
        $macroBookName = ([string]$book.Name).Replace("'", "''")
        $searchMacro = "'$macroBookName'!ProductCatalogSync.SearchProducts"
        $clearMacro = "'$macroBookName'!ProductCatalogSync.ClearProductSearch"
        $baseCaption = [Convert]::ToString(
            $searchButton.TextFrame2.TextRange.Text,
            $invariant
        )

        $eventsWereEnabled = [bool]$excel.EnableEvents
        try {
            $excel.EnableEvents = $false
            $queryRange.Value2 = $query
        } finally {
            $excel.EnableEvents = $eventsWereEnabled
        }
        [void]$excel.Run($searchMacro)
        $first = Read-SearchButtonState $excel $searchButton $expectedScrollColumn
        [void]$excel.Run($searchMacro)
        $second = Read-SearchButtonState $excel $searchButton $expectedScrollColumn

        $wrap = $null
        if ($first.Total -ge 2 -and $first.Total -le 50) {
            for ($index = 3; $index -le $first.Total; $index++) {
                [void]$excel.Run($searchMacro)
            }
            [void]$excel.Run($searchMacro)
            $wrap = Read-SearchButtonState $excel $searchButton $expectedScrollColumn
        }

        [void]$excel.Run($clearMacro)
        $clearedCaption = [Convert]::ToString(
            $searchButton.TextFrame2.TextRange.Text,
            $invariant
        )
        return [pscustomobject]@{
            query = $query
            total = $first.Total
            expectedTotal = $expectedRows.Count
            firstOrdinal = $first.Ordinal
            secondOrdinal = $second.Ordinal
            firstRow = $first.Row
            secondRow = $second.Row
            expectedFirstRow = [int]$expectedRows[0].Row
            expectedSecondRow = [int]$expectedRows[1].Row
            firstScrollColumn = $first.ScrollColumn
            secondScrollColumn = $second.ScrollColumn
            expectedScrollColumn = $expectedScrollColumn
            wrapOrdinal = if ($null -ne $wrap) { $wrap.Ordinal } else { 0 }
            wrapRow = if ($null -ne $wrap) { $wrap.Row } else { 0 }
            baseCaption = $baseCaption
            clearedCaption = $clearedCaption
        }
    } finally {
        Release-ComObject $searchButton
        Release-ComObject $queryRange
        Release-ComObject $sheet
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
                        WooID = Matrix-Value $values $rowCount $columnCount $row 9
                        WooEffectivePrice = Matrix-Value $values $rowCount $columnCount $row 10
                        Categories = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 17),
                            $invariant
                        ).Trim()
                        PublicationStatus = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 18),
                            $invariant
                        ).Trim()
                        Warning = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 19),
                            $invariant
                        ).Trim()
                        RowKind = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 20),
                            $invariant
                        ).Trim()
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

function ProductCode-Dictionary([object[]]$rows) {
    $dictionary = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal
    )
    $duplicates = 0
    foreach ($row in $rows) {
        $productCode = [Convert]::ToString($row.ProductCode, $invariant).Trim()
        if ($productCode.Length -eq 0) { continue }
        if ($dictionary.ContainsKey($productCode)) {
            $duplicates += 1
        } else {
            $dictionary.Add($productCode, $row)
        }
    }
    return [pscustomobject]@{ Values = $dictionary; Duplicates = $duplicates }
}

$excel = $null
$candidateBook = $null
$referenceBook = $null
$syncRan = $false
$syncSucceeded = $false
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
        $syncSucceeded = [bool]$excel.Run(
            "'$macroBookName'!ProductCatalogSync.RefreshAllDataForValidation"
        )
        $syncRan = $true
    }
    $excel.CalculateFullRebuild()
    $candidateSyncStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 3 'B6'),
        $invariant
    )
    $candidatePricingStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 3 'B23'),
        $invariant
    )

    $candidateProducts = Read-Products $candidateBook
    $candidateSearch = Test-ProductSearch $excel $candidateBook $candidateProducts.Rows
    $candidateSyncData = Read-SyncData $candidateBook
    $candidateConfig = [pscustomobject]@{
        yuan = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B10')
        usd = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B11')
        shipping = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B14')
        profit = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B13')
        roundingDigits = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B15')
        cardYuan = Numeric-Or-Null (Table-Scalar $candidateBook 'Yuan_Price')
        cardShipping = Numeric-Or-Null (Table-Scalar $candidateBook 'Shipping')
        cardProfit = Numeric-Or-Null (Table-Scalar $candidateBook 'Profit')
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
    $candidateProductDictionary = ProductCode-Dictionary $candidateProducts.Rows
    $syncDictionary = Row-Dictionary $candidateSyncData.Rows
    $referenceDictionary = ProductCode-Dictionary $referenceProducts.Rows
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
    $wooOnlyRows = @(
        $candidateSyncData.Rows |
            Where-Object { $_.RowKind -eq 'فقط ووکامرس' }
    ).Count
    $matchedRows = @(
        $candidateSyncData.Rows |
            Where-Object { $_.RowKind -eq 'هماهنگ' }
    ).Count
    $sourceOnlyRows = @(
        $candidateSyncData.Rows |
            Where-Object { $_.RowKind -eq 'فقط سامانه کالا' }
    ).Count
    $ambiguousRows = @(
        $candidateSyncData.Rows |
            Where-Object { $_.RowKind -eq 'مبهم' }
    ).Count
    $completeInputRows = 0
    $fallbackPriceRows = 0
    $intentionallyBlankRows = 0
    $sourceOnlyUnsafeRows = 0
    $sourceOnlyUnsafePricedRows = 0
    $missingExpectedPriceRows = 0
    $priceMismatchRows = 0
    $wooComparableRows = 0
    $wooParityMismatchRows = 0
    $invalidSyncIdentityRows = 0
    $wooOnlySKUFallbackRows = 0
    $priceMismatchSamples = @()

    foreach ($row in $candidateProducts.Rows) {
        $weight = Numeric-Or-Null $row.Weight
        $rate = Numeric-Or-Null $row.Rate
        $price = Numeric-Or-Null $row.Price
        if ($null -ne $weight) { $numericWeightRows += 1 }
        if ($null -ne $rate) { $numericRateRows += 1 }
        if ($null -ne $price) { $numericPriceRows += 1 }
        if ([string]$row.Formula -like '=*') { $priceFormulaRows += 1 }

        if (-not $syncDictionary.Values.ContainsKey($row.Code)) {
            continue
        }

        $syncRow = $syncDictionary.Values[$row.Code]
        $syncWooID = Normalized-Code $syncRow.WooID
        switch ($syncRow.RowKind) {
            'هماهنگ' {
                if ($row.WooID.Length -eq 0 -or
                    $syncWooID -ne $row.WooID -or
                    $row.Code -cne "woo:$($row.WooID)" -or
                    $row.ProductCode.Length -eq 0) {
                    $invalidSyncIdentityRows += 1
                }
            }
            'فقط سامانه کالا' {
                if ($row.ProductCode.Length -eq 0 -or
                    $row.Code -cne "patris:$($row.ProductCode)") {
                    $invalidSyncIdentityRows += 1
                }
            }
            'فقط ووکامرس' {
                if ($row.WooID.Length -eq 0 -or
                    $syncWooID -ne $row.WooID -or
                    $row.Code -cne "woo:$($row.WooID)") {
                    $invalidSyncIdentityRows += 1
                }
                if ($row.ProductCode.Length -gt 0) {
                    $wooOnlySKUFallbackRows += 1
                }
            }
        }
        $productFactor = Currency-Factor-Or-Null $syncRow.Currency $syncRow
        $shipping = Numeric-Or-Null $syncRow.Shipping
        $profitPercent = Numeric-Or-Null $syncRow.ProfitPercent
        $shippingFactor = Currency-Factor-Or-Null $syncRow.ShippingCurrency $syncRow
        $sitePrice = Numeric-Or-Null $syncRow.WooEffectivePrice
        $rowEligible = (
            $syncRow.RowKind -eq 'هماهنگ' -or
            $syncRow.RowKind -eq 'فقط سامانه کالا'
        )
        $localReady = (
            $rowEligible -and
            $null -ne $rate -and $rate -gt 0 -and
            $null -ne $weight -and $weight -ge 0 -and
            $null -ne $shipping -and $shipping -ge 0 -and
            $null -ne $profitPercent -and $profitPercent -ge 0 -and
            $null -ne $productFactor -and $productFactor -gt 0 -and
            $null -ne $shippingFactor -and $shippingFactor -gt 0
        )

        $unsafeSourceOnly = (
            $syncRow.RowKind -eq 'فقط سامانه کالا' -and
            $null -ne $rate -and $rate -gt 0 -and
            -not $localReady -and
            ($null -eq $sitePrice -or $sitePrice -le 0)
        )
        if ($unsafeSourceOnly) {
            $sourceOnlyUnsafeRows += 1
            if ($null -ne $price) {
                $sourceOnlyUnsafePricedRows += 1
                if ($priceMismatchSamples.Count -lt 20) {
                    $priceMismatchSamples += "$($row.Code): unsafe incomplete source-only row produced price=$price"
                }
            }
        }

        $expected = $null
        if ($localReady) {
            $completeInputRows += 1
            $shippingComponent = (
                ([decimal]$weight / [decimal]1000) *
                [decimal]$shipping *
                [decimal]$shippingFactor
            )
            $expectedUnrounded = (
                ([decimal]$rate * [decimal]$productFactor) +
                [decimal]$shippingComponent
            ) * (
                [decimal]1 + ([decimal]$profitPercent / [decimal]100)
            )
            $roundingDigits = [int]$candidateConfig.roundingDigits
            $quantum = [decimal]1
            for ($roundingIndex = 0; $roundingIndex -lt $roundingDigits; $roundingIndex++) {
                $quantum *= [decimal]10
            }
            $expected = [Math]::Round(
                $expectedUnrounded / $quantum,
                0,
                [System.MidpointRounding]::AwayFromZero
            ) * $quantum
        } elseif ($null -ne $sitePrice -and $sitePrice -gt 0) {
            $fallbackPriceRows += 1
            $expected = $sitePrice
        } else {
            $intentionallyBlankRows += 1
            continue
        }

        if ($null -ne $sitePrice -and $sitePrice -gt 0 -and $null -ne $price) {
            $wooComparableRows += 1
            if ([Math]::Abs($price - $sitePrice) -gt 0.01) {
                $wooParityMismatchRows += 1
                if ($priceMismatchSamples.Count -lt 20) {
                    $priceMismatchSamples += "$($row.Code): workbook=$price WooCommerce=$sitePrice"
                }
            }
        }

        if ($null -eq $price) {
            $missingExpectedPriceRows += 1
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

    $relayRegression = [pscustomobject]@{
        present = $false
        price = $null
        category = ''
        wooEffectivePrice = $null
    }
    if ($candidateProductDictionary.Values.ContainsKey('109032')) {
        $relayRow = $candidateProductDictionary.Values['109032']
        $relayRegression.present = $true
        $relayRegression.price = Numeric-Or-Null $relayRow.Price
        if ($syncDictionary.Values.ContainsKey($relayRow.Code)) {
            $relaySyncRow = $syncDictionary.Values[$relayRow.Code]
            $relayRegression.category = [string]$relaySyncRow.Categories
            $relayRegression.wooEffectivePrice = Numeric-Or-Null $relaySyncRow.WooEffectivePrice
        }
    }

    $wooFallbackRegression = [pscustomobject]@{
        present = $false
        price = $null
        wooEffectivePrice = $null
        warning = ''
        rowKind = ''
    }
    if ($candidateProductDictionary.Values.ContainsKey('109001')) {
        $fallbackRow = $candidateProductDictionary.Values['109001']
        $wooFallbackRegression.present = $true
        $wooFallbackRegression.price = Numeric-Or-Null $fallbackRow.Price
        if ($syncDictionary.Values.ContainsKey($fallbackRow.Code)) {
            $fallbackSyncRow = $syncDictionary.Values[$fallbackRow.Code]
            $wooFallbackRegression.wooEffectivePrice = Numeric-Or-Null $fallbackSyncRow.WooEffectivePrice
            $wooFallbackRegression.warning = [string]$fallbackSyncRow.Warning
            $wooFallbackRegression.rowKind = [string]$fallbackSyncRow.RowKind
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
        if (-not $candidateProductDictionary.Values.ContainsKey($entry.Key)) {
            continue
        }
        $overlapRows += 1
        $referenceRow = $entry.Value
        $candidateRow = $candidateProductDictionary.Values[$entry.Key]

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
            succeeded = $syncSucceeded
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
            wooOnlyRows = $wooOnlyRows
            matchedRows = $matchedRows
            sourceOnlyRows = $sourceOnlyRows
            ambiguousRows = $ambiguousRows
            fullWooRows = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'G32')
            wooLeafRows = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'G33')
            patrisRows = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'G42')
            excludedWooParentRows = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'G43')
            datasetRevision = [Convert]::ToString(
                (Sheet-Scalar $candidateBook 3 'G44'),
                $invariant
            ).Trim()
            sourceRevision = [Convert]::ToString(
                (Sheet-Scalar $candidateBook 3 'G45'),
                $invariant
            ).Trim()
            paginationTotal = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'G46')
            countSignature = [Convert]::ToString(
                (Sheet-Scalar $candidateBook 3 'G47'),
                $invariant
            ).Trim()
            completeInputRows = $completeInputRows
            fallbackPriceRows = $fallbackPriceRows
            intentionallyBlankRows = $intentionallyBlankRows
            sourceOnlyUnsafeRows = $sourceOnlyUnsafeRows
            sourceOnlyUnsafePricedRows = $sourceOnlyUnsafePricedRows
            missingExpectedPriceRows = $missingExpectedPriceRows
            priceMismatchRows = $priceMismatchRows
            wooComparableRows = $wooComparableRows
            wooParityMismatchRows = $wooParityMismatchRows
            invalidSyncIdentityRows = $invalidSyncIdentityRows
            wooOnlySKUFallbackRows = $wooOnlySKUFallbackRows
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
        search = $candidateSearch
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
        regressions = [pscustomobject]@{
            relay109032 = $relayRegression
            wooFallback109001 = $wooFallbackRegression
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

  const tempDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-excel-validator-'));
  const powershellPath = path.join(tempDirectory, 'validate.ps1');
  let result;
  try {
    // Windows PowerShell 5.1 treats a BOM-less script as the active ANSI code
    // page. A UTF-8 BOM keeps the Persian literals used by the validator exact.
    fs.writeFileSync(powershellPath, `\uFEFF${powershell}\n`, 'utf8');
    result = spawnSync(
      'powershell.exe',
      [
        '-NoLogo',
        '-NoProfile',
        '-NonInteractive',
        '-ExecutionPolicy',
        'Bypass',
        '-File',
        powershellPath,
      ],
      {
        cwd: repoRoot,
        encoding: 'utf8',
        env: {
          ...process.env,
          PATRIS_VALIDATOR_CANDIDATE: options.candidate,
          PATRIS_VALIDATOR_REFERENCE: options.reference,
          PATRIS_VALIDATOR_SYNC: options.sync ? '1' : '0',
        },
        maxBuffer: 16 * 1024 * 1024,
        timeout: options.timeoutMs,
        windowsHide: true,
      },
    );
  } finally {
    try {
      fs.unlinkSync(powershellPath);
    } catch {}
    try {
      fs.rmdirSync(tempDirectory);
    } catch {}
  }

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
