'use strict';

const { spawnSync } = require('node:child_process');
const { randomUUID } = require('node:crypto');
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
  'مبلغ منبع قیمت',
  'ارز منبع قیمت',
  'نوع منبع قیمت',
]);

const REGRESSION_ACCEPTANCE = Object.freeze({
  relayProductCode: '109032',
  relayPrice: 554500,
  relayCategory: 'رله‌ها',
  wooFallbackProductCode: '109001',
  wooFallbackPrice: 1150000,
});

const repoRoot = path.resolve(__dirname, '..', '..');
const VALIDATOR_HOST_STARTUP_CLEANUP_GRACE_MS = 30000;
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
    '  --no-sync              Validate without running the live synchronization macro',
    '  --strict-reference     Fail on every comparable weight/rate difference from the archive',
    '  --json                 Print the complete machine-readable report',
    '  --timeout-ms NUMBER    Excel validation timeout in milliseconds (default: 240000)',
    '  --self-test-native-excel-timeout  Force a hidden native Excel timeout safety test',
    '  --help                 Show this help',
    '',
    'For .xltm candidates, --sync is the default. The template is instantiated in',
    'memory and closed without saving, so validation never mutates the canonical file.',
    'The host reserves a separate bounded 30-second COM startup/cleanup grace.',
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
    selfTestProcessSafety: false,
    selfTestNativeExcelTimeout: false,
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
      case '--self-test-process-safety':
        options.selfTestProcessSafety = true;
        options.sync = false;
        break;
      case '--self-test-native-excel-timeout':
        options.selfTestNativeExcelTimeout = true;
        options.sync = false;
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
    'SYNCDATA,21,FALSE)',
    'SYNCDATA,22,FALSE)',
    'SYNCDATA,23,FALSE)',
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
    report.candidate.config.autoSyncOnOpen === 'بله',
    `canonical template must default automatic synchronization to بله; got ${JSON.stringify(report.candidate.config.autoSyncOnOpen)}`,
  );
  failUnless(
    sameArray(report.syncData.headers, SYNC_DATA_HEADERS),
    `SyncData headers changed: ${JSON.stringify(report.syncData.headers)}`,
  );
  failUnless(
    Array.isArray(report.branding)
      && report.branding.length === 3
      && report.branding.every((sheet) => (
        sameFiniteNumber(sheet.row1Height, 60, 0.01)
        && sheet.logoFound === true
        && typeof sheet.centerDeltaPoints === 'number'
        && sheet.centerDeltaPoints <= 0.75
      )),
    `branding rows/logos are not 60pt and vertically centered: ${JSON.stringify(report.branding)}`,
  );
  failUnless(
    report.searchLiteral.numberFormat === '@'
      && Array.isArray(report.searchLiteral.samples)
      && report.searchLiteral.samples.length === 6
      && report.searchLiteral.samples.every((sample) => (
        sample.input === sample.value
        && sample.input === sample.text
        && sample.valueType === 'String'
        && sample.input === sample.reopenValue
        && sample.input === sample.reopenText
        && sample.reopenValueType === 'String'
        && sample.reopenNumberFormat === '@'
      )),
    `the product search input did not preserve all literal-text fixtures: ${JSON.stringify(report.searchLiteral)}`,
  );
  failUnless(
    report.statusSummary.mixed.includes('3 ')
      && report.statusSummary.mixed.includes('2 ')
      && !report.statusSummary.mixed.includes('0 ')
      && report.statusSummary.allZero.length > 0
      && report.statusSummary.auditMixed.includes('2 ')
      && !report.statusSummary.auditMixed.includes('0 ')
      && report.statusSummary.auditAllZero.length > 0
      && !report.statusSummary.auditAllZero.includes('0 '),
    `zero-count status or audit categories were not omitted: ${JSON.stringify(report.statusSummary)}`,
  );
  failUnless(
    report.fontAudit.passed === true
      && report.fontAudit.persianFont === 'Yekan Bakh'
      && report.fontAudit.latinFont === 'Segoe UI'
      && report.fontAudit.auditMode === 'ترمیم و هشدار'
      && report.fontAudit.validateOnOpen === 'بله'
      && report.fontAudit.allowFallback === 'خیر'
      && report.fontAudit.priceDisplayFaNum === 'بله'
      && report.fontAudit.faNumToggle.enabled === true
      && report.fontAudit.faNumToggle.disabled === true
      && report.fontAudit.invalidModeRejected === true
      && report.fontAudit.missingConfigRejected === true
      && report.fontAudit.missingFontRejected === true
      && report.fontAudit.driftRepaired === true
      && report.fontAudit.dialogPassed === true
      && report.fontAudit.friendlyError.length > 0
      && !report.fontAudit.friendlyError.includes('RefreshPricingState')
      && !report.fontAudit.friendlyError.includes('[pricing state]')
      && !report.fontAudit.friendlyError.toLowerCase().includes('http')
      && report.fontAudit.priceSlots.some((slot) => (
        slot.name === 'Name' && slot.supported === true && slot.value === 'Yekan Bakh FaNum'
      ))
      && report.fontAudit.priceSlots.every((slot) => (
        slot.supported === false || slot.value === 'Yekan Bakh FaNum'
      ))
      && report.fontAudit.technicalSlots.length > 0
      && report.fontAudit.technicalSlots.every((slot) => (
        slot.supported === false || slot.value === 'Segoe UI'
      )),
    `the fixed font policy or hard font audit failed: ${JSON.stringify(report.fontAudit)}`,
  );
  failUnless(
    report.pricingWritebackUI === true,
    'the native pricing-writeback cell color/comment and routing fixture failed',
  );
  failUnless(
    report.pricingProgressUI === true,
    'the visible Persian progress surface lifecycle fixture failed',
  );
  failUnless(
    report.searchRegression.passed === true,
    `the bounded non-reentrant search regression failed: ${JSON.stringify(report.searchRegression)}`,
  );
  failUnless(
    report.titleDirection.latinRows > 0
      && report.titleDirection.persianRows > 0
      && report.titleDirection.mismatches === 0,
    `product-name reading order does not follow title script: ${JSON.stringify(report.titleDirection)}`,
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
    report.search.firstColumn === report.search.expectedColumn
      && report.search.secondColumn === report.search.expectedColumn,
    `Enter/F3 search did not leave the price cell selected: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.expectedYellowCellCount > 0
      && report.search.firstYellowCellCount === report.search.expectedYellowCellCount
      && report.search.secondYellowCellCount === report.search.expectedYellowCellCount
      && report.search.wrapYellowCellCount === report.search.expectedYellowCellCount,
    `the selected search result is not highlighted across the full row under manual calculation: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.search.clearedCaption === report.search.baseCaption,
    `clearing product search did not reset the button caption: ${JSON.stringify(report.search)}`,
  );
  failUnless(
    report.transientPricePreview.found === true
      && report.transientPricePreview.row > 0
      && report.transientPricePreview.markerRow === report.transientPricePreview.row
      && sameFiniteNumber(
        report.transientPricePreview.selectedValue,
        report.transientPricePreview.expected,
        0.01,
      )
      && report.transientPricePreview.grayFont === true
      && report.transientPricePreview.yellowCellCount
        === report.transientPricePreview.expectedYellowCellCount
      && report.transientPricePreview.resetValue === null,
    `zero-stock projected price preview is not transient, gray, and independently correct: ${JSON.stringify(report.transientPricePreview)}`,
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
    `  Search: query=${report.search.query}, results=${report.search.total}, rows=${report.search.firstRow}->${report.search.secondRow}->${report.search.wrapRow}, yellow=${report.search.firstYellowCellCount}/${report.search.expectedYellowCellCount}, scroll=${report.search.firstScrollColumn}/${report.search.secondScrollColumn}`,
  );
  console.log(
    `  Presentation: branding=${report.branding.length} sheets, title direction mismatches=${report.titleDirection.mismatches}, fonts=${report.fontAudit.persianFont}/${report.fontAudit.latinFont}`,
  );
  console.log(
    `  Zero-stock preview: code=${report.transientPricePreview.code}, value=${report.transientPricePreview.selectedValue}, reset=${report.transientPricePreview.resetValue}`,
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

const processSafetyPowerShell = String.raw`
if (-not ('PatrisExcelValidatorNativeMethods' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class PatrisExcelValidatorNativeMethods {
    public const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;
    public const uint MOVEFILE_REPLACE_EXISTING = 0x00000001;
    public const uint MOVEFILE_WRITE_THROUGH = 0x00000008;

    [StructLayout(LayoutKind.Sequential)]
    public struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct IO_COUNTERS {
        public ulong ReadOperationCount;
        public ulong WriteOperationCount;
        public ulong OtherOperationCount;
        public ulong ReadTransferCount;
        public ulong WriteTransferCount;
        public ulong OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
        public JOBOBJECT_BASIC_LIMIT_INFORMATION BasicLimitInformation;
        public IO_COUNTERS IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    [DllImport("user32.dll")]
    public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint processId);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr CreateJobObject(IntPtr securityAttributes, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool SetInformationJobObject(
        IntPtr job,
        int informationClass,
        IntPtr information,
        uint informationLength
    );

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool IsProcessInJob(IntPtr process, IntPtr job, out bool result);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool MoveFileEx(string existingPath, string newPath, uint flags);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool CloseHandle(IntPtr handle);
}
'@
}

function New-ValidatorKillOnCloseJob {
    $jobHandle = [PatrisExcelValidatorNativeMethods]::CreateJobObject(
        [IntPtr]::Zero,
        $null
    )
    if ($jobHandle -eq [IntPtr]::Zero) {
        throw [ComponentModel.Win32Exception]::new(
            [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
            'Unable to create the validator kill-on-close job.'
        )
    }

    $informationPointer = [IntPtr]::Zero
    try {
        $basic = [PatrisExcelValidatorNativeMethods+JOBOBJECT_BASIC_LIMIT_INFORMATION]::new()
        $basic.LimitFlags = [PatrisExcelValidatorNativeMethods]::JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        $information = [PatrisExcelValidatorNativeMethods+JOBOBJECT_EXTENDED_LIMIT_INFORMATION]::new()
        $information.BasicLimitInformation = $basic
        $informationLength = [Runtime.InteropServices.Marshal]::SizeOf($information)
        $informationPointer = [Runtime.InteropServices.Marshal]::AllocHGlobal($informationLength)
        [Runtime.InteropServices.Marshal]::StructureToPtr(
            $information,
            $informationPointer,
            $false
        )
        if (-not [PatrisExcelValidatorNativeMethods]::SetInformationJobObject(
            $jobHandle,
            9,
            $informationPointer,
            [uint32]$informationLength
        )) {
            throw [ComponentModel.Win32Exception]::new(
                [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
                'Unable to set JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.'
            )
        }

        $currentProcess = [Diagnostics.Process]::GetCurrentProcess()
        try {
            if (-not [PatrisExcelValidatorNativeMethods]::AssignProcessToJobObject(
                $jobHandle,
                $currentProcess.Handle
            )) {
                throw [ComponentModel.Win32Exception]::new(
                    [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
                    'Unable to assign the validator host to its kill-on-close job.'
                )
            }
        }
        finally {
            $currentProcess.Dispose()
        }
    }
    catch {
        [void][PatrisExcelValidatorNativeMethods]::CloseHandle($jobHandle)
        throw
    }
    finally {
        if ($informationPointer -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::FreeHGlobal($informationPointer)
        }
    }

    # Intentionally retain this handle for the lifetime of powershell.exe. If
    # Node times out and terminates this host, Windows closes the last handle
    # and kills the validator's entire contained process tree, including Excel.
    return $jobHandle
}

function Get-ValidatorProcessIdentityById(
    [int]$processId,
    [string]$expectedExecutableName = ''
) {
    $process = [Diagnostics.Process]::GetProcessById($processId)
    try {
        [void]$process.Handle
        $pathDeadline = [DateTime]::UtcNow.AddSeconds(5)
        $path = ''
        $pathError = $null
        while ([string]::IsNullOrWhiteSpace($path)) {
            if ($process.HasExited) {
                throw "Process $processId exited before its executable identity was readable."
            }
            try {
                $process.Refresh()
                $path = [string]$process.MainModule.FileName
            }
            catch {
                $pathError = $_.Exception
            }
            if (-not [string]::IsNullOrWhiteSpace($path)) { break }
            if ([DateTime]::UtcNow -ge $pathDeadline) {
                throw [InvalidOperationException]::new(
                    "Process $processId executable identity was not readable within 5 seconds.",
                    $pathError
                )
            }
            Start-Sleep -Milliseconds 25
        }
        $startTimeUtc = $process.StartTime.ToUniversalTime()
        if (-not [string]::IsNullOrWhiteSpace($expectedExecutableName) -and
            -not [IO.Path]::GetFileName($path).Equals(
                $expectedExecutableName,
                [StringComparison]::OrdinalIgnoreCase
            )) {
            throw "Process $processId is not the expected $expectedExecutableName executable: $path"
        }
        return [pscustomobject]@{
            Process = $process
            Id = $processId
            StartTimeUtc = $startTimeUtc
            StartTimeUtcTicks = $startTimeUtc.Ticks
            ExecutablePath = $path
        }
    }
    catch {
        $process.Dispose()
        throw
    }
}

function Get-ExcelProcessIdentity([object]$application) {
    [uint32]$processIdValue = 0
    [void][PatrisExcelValidatorNativeMethods]::GetWindowThreadProcessId(
        [IntPtr]([long]$application.Hwnd),
        [ref]$processIdValue
    )
    if ($processIdValue -eq 0) {
        throw 'Unable to resolve the hidden validator Excel process ID.'
    }
    return Get-ValidatorProcessIdentityById ([int]$processIdValue) 'EXCEL.EXE'
}

function Add-ValidatorProcessToJob([IntPtr]$jobHandle, [object]$identity) {
    if ($jobHandle -eq [IntPtr]::Zero) {
        throw 'The validator job handle is unavailable.'
    }
    $process = [Diagnostics.Process]$identity.Process
    $isAssigned = $false
    if (-not [PatrisExcelValidatorNativeMethods]::IsProcessInJob(
        $process.Handle,
        $jobHandle,
        [ref]$isAssigned
    )) {
        throw [ComponentModel.Win32Exception]::new(
            [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
            "Unable to query validator job membership for process $($identity.Id)."
        )
    }
    if (-not $isAssigned -and
        -not [PatrisExcelValidatorNativeMethods]::AssignProcessToJobObject(
            $jobHandle,
            $process.Handle
        )) {
        throw [ComponentModel.Win32Exception]::new(
            [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
            "Unable to assign process $($identity.Id) to the validator kill-on-close job."
        )
    }
    $isAssigned = $false
    if (-not [PatrisExcelValidatorNativeMethods]::IsProcessInJob(
        $process.Handle,
        $jobHandle,
        [ref]$isAssigned
    ) -or -not $isAssigned) {
        throw "Process $($identity.Id) is not contained by the validator kill-on-close job."
    }
    return $true
}

function Write-ValidatorProcessIdentity(
    [object]$identity,
    [string]$outputPath,
    [bool]$assignedToValidatorJob = $false
) {
    if ([string]::IsNullOrWhiteSpace($outputPath)) { return }
    $payload = [ordered]@{
        pid = [int]$identity.Id
        start_time_utc_ticks = [long]$identity.StartTimeUtcTicks
        executable_path = [string]$identity.ExecutablePath
        assigned_to_validator_job = $assignedToValidatorJob
    } | ConvertTo-Json -Compress
    $temporaryPath = $outputPath + '.tmp-' + [Guid]::NewGuid().ToString('N')
    [IO.File]::WriteAllText(
        $temporaryPath,
        $payload,
        [Text.UTF8Encoding]::new($false)
    )
    if (-not [PatrisExcelValidatorNativeMethods]::MoveFileEx(
        $temporaryPath,
        $outputPath,
        [PatrisExcelValidatorNativeMethods]::MOVEFILE_REPLACE_EXISTING -bor
            [PatrisExcelValidatorNativeMethods]::MOVEFILE_WRITE_THROUGH
    )) {
        throw [ComponentModel.Win32Exception]::new(
            [Runtime.InteropServices.Marshal]::GetLastWin32Error(),
            'Unable to atomically publish the validator process identity.'
        )
    }
}

function Wait-ValidatorProcessExit(
    [object]$identity,
    [int]$timeoutMilliseconds = 15000,
    [int[]]$acceptedExitCodes = @(0)
) {
    $process = [Diagnostics.Process]$identity.Process
    if (-not $process.WaitForExit($timeoutMilliseconds)) {
        throw "Validator process $($identity.Id) did not exit within $timeoutMilliseconds ms."
    }
    $process.WaitForExit()
    $exitCode = [int]$process.ExitCode
    if ($acceptedExitCodes -notcontains $exitCode) {
        throw "Validator process $($identity.Id) ($($identity.ExecutablePath), started $($identity.StartTimeUtc.ToString('o'))) exited with unexpected code $exitCode."
    }
    return [pscustomobject]@{
        Exited = $true
        ExitCode = $exitCode
        Id = [int]$identity.Id
        StartTimeUtc = $identity.StartTimeUtc
        ExecutablePath = [string]$identity.ExecutablePath
    }
}

function New-ValidatorCombinedException(
    [Exception]$primaryException,
    [Collections.Generic.List[Exception]]$cleanupExceptions
) {
    $exceptions = [Collections.Generic.List[Exception]]::new()
    if ($null -ne $primaryException) {
        $exceptions.Add($primaryException)
    }
    foreach ($cleanupException in $cleanupExceptions) {
        $exceptions.Add($cleanupException)
    }
    if ($exceptions.Count -eq 0) { return $null }
    if ($exceptions.Count -eq 1) { return $exceptions[0] }
    return [AggregateException]::new(
        'Excel validation and cleanup both failed.',
        [Exception[]]$exceptions.ToArray()
    )
}
`;

const powershell = String.raw`
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

${processSafetyPowerShell}

$candidatePath = $env:PATRIS_VALIDATOR_CANDIDATE
$referencePath = $env:PATRIS_VALIDATOR_REFERENCE
$stagePath = $env:PATRIS_VALIDATOR_STAGE_PATH
$runSync = $env:PATRIS_VALIDATOR_SYNC -eq '1'
$validationTimeoutMilliseconds = [int]$env:PATRIS_VALIDATOR_TIMEOUT_MS
$invariant = [System.Globalization.CultureInfo]::InvariantCulture

function Set-ValidatorStage([string]$stage, [string]$detail = '') {
    if ([string]::IsNullOrWhiteSpace($stagePath)) { return }
    $payload = [ordered]@{
        stage = $stage
        detail = $detail
        updated_at_utc = [DateTime]::UtcNow.ToString('o')
    } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($stagePath, $payload, [Text.UTF8Encoding]::new($false))
}

function Release-ComObject([object]$value) {
    try {
        if ($null -ne $value -and [Runtime.InteropServices.Marshal]::IsComObject($value)) {
            [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($value)
        }
    } catch {}
}

function Invoke-ComFinalizerBarrier {
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}

function Test-RetryableExcelComRejection([object]$errorRecord) {
    $exception = if ($null -ne $errorRecord) { $errorRecord.Exception } else { $null }
    while ($null -ne $exception) {
        # Excel temporarily rejects out-of-process automation while a native
        # callback is committing workbook state. Retrying these two COM
        # statuses is the documented automation behavior; every other error
        # remains terminal and is surfaced unchanged.
        if ($exception.HResult -eq -2147418111 -or # RPC_E_CALL_REJECTED
            $exception.HResult -eq -2147417846) {  # RPC_E_SERVERCALL_RETRYLATER
            return $true
        }
        $exception = $exception.InnerException
    }
    return $false
}

function Invoke-ExcelBusyRetry(
    [scriptblock]$operation,
    [DateTime]$deadline,
    [string]$purpose
) {
    while ($true) {
        try {
            return & $operation
        } catch {
            if (-not (Test-RetryableExcelComRejection $_)) {
                throw
            }
            if ([DateTime]::UtcNow -ge $deadline) {
                throw "Excel remained busy beyond the validator deadline while $purpose."
            }
            Start-Sleep -Milliseconds 100
        }
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

function Reset-SelectedProductRow([object]$excel, [object]$book) {
    $eventsWereEnabled = [bool]$excel.EnableEvents
    $table = $null
    $dataRange = $null
    try {
        $excel.EnableEvents = $false
        foreach ($nameText in @('SelectedProductRow', 'ProjectedPricePreviewRow')) {
            $definedName = $null
            $marker = $null
            try {
                $definedName = $book.Names.Item($nameText)
                $marker = $definedName.RefersToRange
                $marker.Value2 = 0
            } finally {
                Release-ComObject $marker
                Release-ComObject $definedName
            }
        }
        $table = Find-Table $book 'Products'
        $dataRange = $table.DataBodyRange
        if ($null -ne $dataRange) {
            [void]$dataRange.Calculate()
        }
    } finally {
        $excel.EnableEvents = $eventsWereEnabled
        Release-ComObject $dataRange
        Release-ComObject $table
    }
}

function Test-BrandingLayout([object]$book) {
    $contracts = @(
        [pscustomobject]@{ SheetIndex = 1; Anchor = 'B1:B2' },
        [pscustomobject]@{ SheetIndex = 2; Anchor = 'B1:B2' },
        [pscustomobject]@{ SheetIndex = 3; Anchor = 'A1:A2' }
    )
    $results = @()
    foreach ($contract in $contracts) {
        $sheet = $null
        $anchor = $null
        $logo = $null
        try {
            $sheet = $book.Worksheets.Item([int]$contract.SheetIndex)
            $anchor = $sheet.Range([string]$contract.Anchor)
            for ($shapeIndex = 1; $shapeIndex -le $sheet.Shapes.Count; $shapeIndex++) {
                $candidate = $sheet.Shapes.Item($shapeIndex)
                # Excel exposes SVG logos as msoGraphic (28) and raster logos
                # as msoPicture (13). Both are valid branded picture shapes.
                if ([int]$candidate.Type -eq 13 -or [int]$candidate.Type -eq 28) {
                    $logo = $candidate
                    break
                }
                Release-ComObject $candidate
            }
            $rowHeight = [double]$sheet.Rows.Item(1).RowHeight
            $logoFound = $null -ne $logo
            $centerDelta = $null
            if ($logoFound) {
                $anchorCenter = [double]$anchor.Top + ([double]$anchor.Height / 2)
                $logoCenter = [double]$logo.Top + ([double]$logo.Height / 2)
                $centerDelta = [Math]::Abs($anchorCenter - $logoCenter)
            }
            $results += [pscustomobject]@{
                sheet = [string]$sheet.Name
                row1Height = $rowHeight
                logoFound = $logoFound
                centerDeltaPoints = $centerDelta
            }
        } finally {
            Release-ComObject $logo
            Release-ComObject $anchor
            Release-ComObject $sheet
        }
    }
    return @($results)
}

function Test-SearchLiteralText([object]$excel, [object]$book) {
    $definedName = $null
    $input = $null
    $eventsWereEnabled = [bool]$excel.EnableEvents
    try {
        $definedName = $book.Names.Item('ProductSearchQuery')
        $input = $definedName.RefersToRange
        $savedValue = $input.Value2
        $samples = @()
        $macro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.PreserveSearchLiteral"
        $copyExtension = switch ([int]$book.FileFormat) {
            51 { '.xlsx' }
            52 { '.xlsm' }
            53 { '.xltm' }
            default { '.xlsm' }
        }
        $excel.EnableEvents = $false
        foreach ($fixture in @('2.4', '25.40', '12/3', '01.02', '001234', '۱۲۳۴')) {
            $reopenPath = Join-Path ([System.IO.Path]::GetTempPath()) (
                'patris-search-literal-' + [Guid]::NewGuid().ToString('N') +
                $copyExtension
            )
            $reopenBook = $null
            $reopenWorkbooks = $null
            $reopenName = $null
            $reopenInput = $null
            $input.NumberFormat = '@'
            $input.Value2 = $fixture
            [void]$excel.Run($macro)
            try {
                $book.SaveCopyAs($reopenPath)
                $reopenWorkbooks = $excel.Workbooks
                $reopenBook = $reopenWorkbooks.Open($reopenPath, 0, $true)
                Release-ComObject $reopenWorkbooks
                $reopenWorkbooks = $null
                $reopenName = $reopenBook.Names.Item('ProductSearchQuery')
                $reopenInput = $reopenName.RefersToRange
                $samples += [pscustomobject]@{
                    input = $fixture
                    value = [Convert]::ToString($input.Value2, $invariant)
                    text = [Convert]::ToString($input.Text, $invariant)
                    valueType = if ($null -eq $input.Value2) {
                        ''
                    } else {
                        [string]$input.Value2.GetType().Name
                    }
                    reopenValue = [Convert]::ToString($reopenInput.Value2, $invariant)
                    reopenText = [Convert]::ToString($reopenInput.Text, $invariant)
                    reopenValueType = if ($null -eq $reopenInput.Value2) {
                        ''
                    } else {
                        [string]$reopenInput.Value2.GetType().Name
                    }
                    reopenNumberFormat = [Convert]::ToString(
                        $reopenInput.NumberFormat,
                        $invariant
                    )
                }
            } finally {
                Release-ComObject $reopenInput
                Release-ComObject $reopenName
                if ($null -ne $reopenBook) {
                    $reopenBook.Close($false)
                }
                Release-ComObject $reopenBook
                Release-ComObject $reopenWorkbooks
                Remove-Item -LiteralPath $reopenPath -Force -ErrorAction SilentlyContinue
            }
        }
        return [pscustomobject]@{
            numberFormat = [Convert]::ToString($input.NumberFormat, $invariant)
            samples = @($samples)
        }
    } finally {
        if ($null -ne $input) {
            $input.Value2 = $savedValue
        }
        $excel.EnableEvents = $eventsWereEnabled
        Release-ComObject $input
        Release-ComObject $definedName
    }
}

function Test-FontAudit([object]$excel, [object]$book) {
    $settings = $null
    $table = $null
    $tableDataRange = $null
    $priceDataRange = $null
    $priceColumn = $null
    $savedPersianFont = $null
    $savedAuditMode = $null
    $savedPriceDisplayFaNum = $null
    $savedPriceFont = $null
    try {
        $settings = $book.Worksheets.Item(3)
        $table = Find-Table $book 'Products'
        $tableDataRange = $table.DataBodyRange
        $priceDataRange = $table.ListColumns.Item(1).DataBodyRange
        if ($null -ne $priceDataRange) {
            $priceColumn = $priceDataRange.Cells.Item(1, 1)
        }
        $macro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.AuditFontsForValidation"
        Set-ValidatorStage 'font_audit_macro'
        $passed = [bool]$excel.Run($macro)
        $dialogMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.AuditMessageDialogForValidation"
        $friendlyMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.FriendlyStatusErrorForValidation"
        Set-ValidatorStage 'font_dialog_fixture'
        $dialogPassed = [bool]$excel.Run($dialogMacro)
        Set-ValidatorStage 'font_friendly_error_fixture'
        $friendlyError = [Convert]::ToString(
            $excel.Run(
                $friendlyMacro,
                'RefreshPricingState [pricing state]: HTTP 500 internal error'
            ),
            $invariant
        )
        $policyFixtureMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.ValidateFontPolicyFixturesForValidation"
        Set-ValidatorStage 'font_policy_fixtures'
        $policyFixturesPassed = [bool]$excel.Run($policyFixtureMacro)
        $savedPersianFont = $settings.Range('B39').Value2
        $savedAuditMode = $settings.Range('B41').Value2
        $savedPriceDisplayFaNum = $settings.Range('B44').Value2
        $driftRepaired = $false
        $faNumEnabled = $false
        $faNumDisabled = $false
        $priceSlots = @()
        $technicalSlots = @()
        if ($null -ne $priceColumn) {
            $savedPriceFont = $priceColumn.Font.Name
            $applyPriceFontMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.ApplyPriceDisplayFontSetting"
            $settings.Range('B44').Value2 = 'بله'
            Set-ValidatorStage 'font_apply_fanum'
            [void]$excel.Run($applyPriceFontMacro)
            $faNumEnabled = [Convert]::ToString(
                $priceColumn.Font.Name,
                $invariant
            ) -eq 'Yekan Bakh FaNum'
            $priceSlots = @(
                foreach ($slot in @('Name', 'NameComplexScript', 'NameFarEast')) {
                    try {
                        $slotValue = [Convert]::ToString($priceColumn.Font.$slot, $invariant)
                        [pscustomobject]@{
                            name = $slot
                            supported = $slot -eq 'Name' -or -not [string]::IsNullOrWhiteSpace($slotValue)
                            value = $slotValue
                        }
                    } catch {
                        [pscustomobject]@{
                            name = $slot
                            supported = $false
                            value = ''
                        }
                    }
                }
            )
            foreach ($columnIndex in @(2, 5, 6, 7, 9)) {
                $technicalCell = $null
                try {
                    $technicalCell = $tableDataRange.Cells.Item(1, $columnIndex)
                    foreach ($slot in @('Name', 'NameComplexScript', 'NameFarEast')) {
                        try {
                            $slotValue = [Convert]::ToString($technicalCell.Font.$slot, $invariant)
                            $technicalSlots += [pscustomobject]@{
                                column = $columnIndex
                                name = $slot
                                supported = $slot -eq 'Name' -or -not [string]::IsNullOrWhiteSpace($slotValue)
                                value = $slotValue
                            }
                        } catch {
                            $technicalSlots += [pscustomobject]@{
                                column = $columnIndex
                                name = $slot
                                supported = $false
                                value = ''
                            }
                        }
                    }
                } finally {
                    Release-ComObject $technicalCell
                }
            }
            $settings.Range('B44').Value2 = 'خیر'
            Set-ValidatorStage 'font_apply_segoe'
            [void]$excel.Run($applyPriceFontMacro)
            $faNumDisabled = [Convert]::ToString(
                $priceColumn.Font.Name,
                $invariant
            ) -eq 'Segoe UI'
            $settings.Range('B44').Value2 = 'بله'
            Set-ValidatorStage 'font_reapply_fanum'
            [void]$excel.Run($applyPriceFontMacro)
            $priceColumn.Font.Name = 'Arial'
            $repairMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.RepairFontDriftForValidation"
            Set-ValidatorStage 'font_repair_drift'
            $driftRepaired = [bool]$excel.Run($repairMacro) -and
                [Convert]::ToString($priceColumn.Font.Name, $invariant) -eq 'Yekan Bakh FaNum'
        }

        return [pscustomobject]@{
            passed = $passed
            persianFont = [Convert]::ToString($settings.Range('B39').Value2, $invariant)
            latinFont = [Convert]::ToString($settings.Range('B40').Value2, $invariant)
            auditMode = [Convert]::ToString($settings.Range('B41').Value2, $invariant)
            validateOnOpen = [Convert]::ToString($settings.Range('B42').Value2, $invariant)
            allowFallback = [Convert]::ToString($settings.Range('B43').Value2, $invariant)
            priceDisplayFaNum = [Convert]::ToString($savedPriceDisplayFaNum, $invariant)
            faNumToggle = [pscustomobject]@{
                enabled = $faNumEnabled
                disabled = $faNumDisabled
            }
            invalidModeRejected = $policyFixturesPassed
            missingConfigRejected = $policyFixturesPassed
            missingFontRejected = $policyFixturesPassed
            driftRepaired = $driftRepaired
            dialogPassed = $dialogPassed
            friendlyError = $friendlyError
            priceSlots = $priceSlots
            technicalSlots = $technicalSlots
        }
    } finally {
        if ($null -ne $settings) {
            if ($null -ne $savedPersianFont) {
                $settings.Range('B39').Value2 = $savedPersianFont
            }
            if ($null -ne $savedAuditMode) {
                $settings.Range('B41').Value2 = $savedAuditMode
            }
            if ($null -ne $savedPriceDisplayFaNum) {
                $settings.Range('B44').Value2 = $savedPriceDisplayFaNum
                if ($null -ne $priceColumn) {
                    $restorePriceFontMacro = "'$($book.Name.Replace("'", "''"))'!ProductCatalogSync.ApplyPriceDisplayFontSetting"
                    Set-ValidatorStage 'font_restore_original'
                    [void]$excel.Run($restorePriceFontMacro)
                }
            }
        }
        if ($null -ne $priceColumn -and $null -ne $savedPriceFont) {
            $priceColumn.Font.Name = $savedPriceFont
        }
        Release-ComObject $priceColumn
        Release-ComObject $priceDataRange
        Release-ComObject $tableDataRange
        Release-ComObject $table
        Release-ComObject $settings
    }
}

function Test-PricingWritebackUI([object]$excel, [object]$book) {
    $bookName = ([string]$book.Name).Replace("'", "''")
    return [bool]$excel.Run(
        "'$bookName'!ProductCatalogSync.ValidatePricingWritebackUIForValidation"
    )
}

function Test-PricingProgressUI([object]$excel, [object]$book) {
    $bookName = ([string]$book.Name).Replace("'", "''")
    return [bool]$excel.Run(
        "'$bookName'!ProductCatalogSync.ValidateOperationProgressUIForValidation"
    )
}

function Test-StatusSummaryFormatter([object]$excel, [object]$book) {
    $bookName = $book.Name.Replace("'", "''")
    $statusMacro = "'$bookName'!ProductCatalogSync.FormatStatusSummaryForValidation"
    $auditMacro = "'$bookName'!ProductCatalogSync.FormatFontAuditSummaryForValidation"
    return [pscustomobject]@{
        mixed = [Convert]::ToString($excel.Run($statusMacro, 0, 0, 3, 0, 2, 0, 0), $invariant)
        allZero = [Convert]::ToString($excel.Run($statusMacro, 0, 0, 0, 0, 0, 0, 0), $invariant)
        auditMixed = [Convert]::ToString($excel.Run($auditMacro, 2, 0), $invariant)
        auditAllZero = [Convert]::ToString($excel.Run($auditMacro, 0, 0), $invariant)
    }
}

function Test-ProductTitleDirection([object]$book) {
    $table = Find-Table $book 'Products'
    $titleColumn = $null
    $cell = $null
    $latinRows = 0
    $persianRows = 0
    $mismatches = 0
    $samples = @()
    try {
        $titleColumn = $table.ListColumns.Item(8).DataBodyRange
        if ($null -eq $titleColumn) {
            return [pscustomobject]@{
                latinRows = 0
                persianRows = 0
                mismatches = 0
                samples = @()
            }
        }
        for ($row = 1; $row -le $titleColumn.Rows.Count; $row++) {
            $cell = $titleColumn.Cells.Item($row, 1)
            $title = [Convert]::ToString($cell.Value2, $invariant).Trim()
            if ($title.Length -gt 0) {
                $containsPersian = [regex]::IsMatch(
                    $title,
                    '[\u0600-\u06FF\u0750-\u077F\u08A0-\u08FF\uFB50-\uFDFF\uFE70-\uFEFF]'
                )
                $expected = if ($containsPersian) { -5004 } else { -5003 }
                if ($containsPersian) {
                    $persianRows += 1
                } else {
                    $latinRows += 1
                }
                $actual = [int]$cell.ReadingOrder
                if ($actual -ne $expected) {
                    $mismatches += 1
                    if ($samples.Count -lt 20) {
                        $samples += "row=$([int]$cell.Row), expected=$expected, actual=$actual, title=$title"
                    }
                }
            }
            Release-ComObject $cell
            $cell = $null
        }
        return [pscustomobject]@{
            latinRows = $latinRows
            persianRows = $persianRows
            mismatches = $mismatches
            samples = $samples
        }
    } finally {
        Release-ComObject $cell
        Release-ComObject $titleColumn
        Release-ComObject $table
    }
}

function Read-Products([object]$book) {
    $table = Find-Table $book 'Products'
    $tableRange = $null
    $headerRange = $null
    $dataRange = $null
    try {
        $tableRange = $table.Range
        $tableFirstColumn = [int]$tableRange.Column
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
                $stock = if ($columnCount -ge 6) {
                    Matrix-Value $values $rowCount $columnCount $row 6
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
                        Stock = $stock
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
            TableAddress = [string]$tableRange.Address($false, $false)
            TableFirstColumn = $tableFirstColumn
            TableColumnCount = if ($null -ne $dataRange) {
                [int]$dataRange.Columns.Count
            } else { 0 }
            Rows = @($rows)
            FormulaSignatures = $signatures
        }
    } finally {
        Release-ComObject $dataRange
        Release-ComObject $headerRange
        Release-ComObject $tableRange
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
    [object]$table,
    [int]$expectedScrollColumn
) {
    $caption = [Convert]::ToString(
        $searchButton.TextFrame2.TextRange.Text,
        $invariant
    )
    $captionMatch = [regex]::Match($caption, '\((\d+)/(\d+)\)$')
    $dataRange = $null
    $selectedTableRow = $null
    $selectedCell = $null
    $displayFormat = $null
    $interior = $null
    $activeRow = [int]$excel.ActiveCell.Row
    $activeColumn = [int]$excel.ActiveCell.Column
    $yellowCellCount = 0
    $tableColumnCount = 0
    try {
        $dataRange = $table.DataBodyRange
        if ($null -ne $dataRange) {
            $tableColumnCount = [int]$dataRange.Columns.Count
            $rowIndex = $activeRow - [int]$dataRange.Row + 1
            if ($rowIndex -ge 1 -and $rowIndex -le [int]$dataRange.Rows.Count) {
                $selectedTableRow = $dataRange.Rows.Item($rowIndex)
                for ($column = 1; $column -le $tableColumnCount; $column++) {
                    $selectedCell = $selectedTableRow.Cells.Item(1, $column)
                    $displayFormat = $selectedCell.DisplayFormat
                    $interior = $displayFormat.Interior
                    if ([int]$interior.Color -eq 13432063) {
                        $yellowCellCount += 1
                    }
                    Release-ComObject $interior
                    Release-ComObject $displayFormat
                    Release-ComObject $selectedCell
                    $interior = $null
                    $displayFormat = $null
                    $selectedCell = $null
                }
            }
        }
    } finally {
        Release-ComObject $interior
        Release-ComObject $displayFormat
        Release-ComObject $selectedCell
        Release-ComObject $selectedTableRow
        Release-ComObject $dataRange
    }
    return [pscustomobject]@{
        Caption = $caption
        Ordinal = if ($captionMatch.Success) {
            [int]$captionMatch.Groups[1].Value
        } else { 0 }
        Total = if ($captionMatch.Success) {
            [int]$captionMatch.Groups[2].Value
        } else { 0 }
        Row = $activeRow
        Column = $activeColumn
        ExpectedColumn = [int]$table.Range.Column
        YellowCellCount = $yellowCellCount
        ExpectedYellowCellCount = $tableColumnCount
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
                firstColumn = 0
                secondColumn = 0
                expectedColumn = [int]$table.Range.Column
                firstYellowCellCount = 0
                secondYellowCellCount = 0
                wrapYellowCellCount = 0
                expectedYellowCellCount = [int]$table.ListColumns.Count
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
        $focusMacro = "'$macroBookName'!ProductCatalogSync.FocusProductSearch"
        $registerMacro = "'$macroBookName'!ProductCatalogSync.RegisterSearchHotkey"
        $unregisterMacro = "'$macroBookName'!ProductCatalogSync.UnregisterSearchHotkey"
        $refreshEnterMacro = "'$macroBookName'!ProductCatalogSync.RefreshSearchEnterHotkey"
        $enterMacro = "'$macroBookName'!ProductCatalogSync.HandleProductSearchEnter"
        $clearMacro = "'$macroBookName'!ProductCatalogSync.ClearProductSearch"
        $baseCaption = [Convert]::ToString(
            $searchButton.TextFrame2.TextRange.Text,
            $invariant
        )

        $eventsWereEnabled = [bool]$excel.EnableEvents
        $calculationWas = [int]$excel.Calculation
        try {
            $excel.Calculation = -4135
            [void]$book.Activate()
            [void]$sheet.Activate()
            [void]$excel.Run($registerMacro)
            [void]$excel.Run($focusMacro)
            $excel.EnableEvents = $false
            $queryRange.Value2 = $query
        } finally {
            $excel.EnableEvents = $eventsWereEnabled
        }
        [void]$excel.Run($refreshEnterMacro)
        [void]$excel.Run($enterMacro)
        $first = Read-SearchButtonState $excel $searchButton $table $expectedScrollColumn
        [void]$excel.Run($refreshEnterMacro)
        [void]$excel.Run($enterMacro)
        $second = Read-SearchButtonState $excel $searchButton $table $expectedScrollColumn

        $wrap = $null
        if ($first.Total -ge 2 -and $first.Total -le 50) {
            for ($index = 3; $index -le $first.Total; $index++) {
                [void]$excel.Run($searchMacro)
            }
            [void]$excel.Run($searchMacro)
            $wrap = Read-SearchButtonState $excel $searchButton $table $expectedScrollColumn
        }

        [void]$excel.Run($clearMacro)
        [void]$excel.Run($unregisterMacro)
        $clearedCaption = [Convert]::ToString(
            $searchButton.TextFrame2.TextRange.Text,
            $invariant
        )
        $excel.Calculation = $calculationWas
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
            firstColumn = $first.Column
            secondColumn = $second.Column
            expectedColumn = $first.ExpectedColumn
            firstYellowCellCount = $first.YellowCellCount
            secondYellowCellCount = $second.YellowCellCount
            wrapYellowCellCount = if ($null -ne $wrap) {
                $wrap.YellowCellCount
            } else { 0 }
            expectedYellowCellCount = $first.ExpectedYellowCellCount
            expectedScrollColumn = $expectedScrollColumn
            wrapOrdinal = if ($null -ne $wrap) { $wrap.Ordinal } else { 0 }
            wrapRow = if ($null -ne $wrap) { $wrap.Row } else { 0 }
            baseCaption = $baseCaption
            clearedCaption = $clearedCaption
        }
    } finally {
        if ($null -ne $excel -and $null -ne $calculationWas) {
            try { $excel.Calculation = $calculationWas } catch {}
        }
        Release-ComObject $searchButton
        Release-ComObject $queryRange
        Release-ComObject $sheet
        Release-ComObject $table
    }
}

function Test-SearchCrashRegression([object]$excel, [object]$book) {
    $table = Find-Table $book 'Products'
    $queryRange = $null
    $button = $null
    $sheet = $null
    $eventsWereEnabled = [bool]$excel.EnableEvents
    try {
        $sheet = $table.Parent
        $queryRange = $book.Names.Item('ProductSearchQuery').RefersToRange
        $button = $sheet.Shapes.Item('ProductSearchButton')
        $macroBookName = ([string]$book.Name).Replace("'", "''")
        $searchMacro = "'$macroBookName'!ProductCatalogSync.SearchProducts"
        $clearMacro = "'$macroBookName'!ProductCatalogSync.ClearProductSearch"
        $initialAddress = [string]$table.Range.Address($false, $false)
        $initialRows = [int]$table.ListRows.Count
        $checks = @()
        foreach ($query in @('109032', '109032', '109032', '__DIGITALOGIC_NO_MATCH__', '*?~')) {
            $excel.EnableEvents = $false
            $queryRange.NumberFormat = '@'
            $queryRange.Value2 = $query
            $excel.EnableEvents = $eventsWereEnabled
            [void]$excel.Run($searchMacro)
            $caption = [Convert]::ToString(
                $button.TextFrame2.TextRange.Text,
                $invariant
            )
            $checks += [pscustomobject]@{
                query = $query
                caption = $caption
                ready = [bool]$excel.Ready
                rows = [int]$table.ListRows.Count
                address = [string]$table.Range.Address($false, $false)
            }
            [void]$excel.Run($clearMacro)
        }
        $passed = (
            @($checks).Count -eq 5 -and
            @($checks | Where-Object {
                -not $_.ready -or
                $_.rows -ne $initialRows -or
                $_.address -ne $initialAddress
            }).Count -eq 0 -and
            @($checks | Select-Object -First 3 | Where-Object {
                $_.caption -notmatch '\(1/1\)$'
            }).Count -eq 0 -and
            $checks[3].caption -match '\(0\)$'
        )
        return [pscustomobject]@{
            passed = $passed
            initialRows = $initialRows
            initialAddress = $initialAddress
            checks = $checks
        }
    } finally {
        $excel.EnableEvents = $eventsWereEnabled
        Release-ComObject $button
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
            $dataFirstRow = [int]$dataRange.Row
            $dataFirstColumn = [int]$dataRange.Column
            $values = $dataRange.Value2
            for ($row = 1; $row -le $rowCount; $row++) {
                $code = Normalized-Code (
                    Matrix-Value $values $rowCount $columnCount $row 1
                )
                if ($code.Length -gt 0) {
                    $rows += [pscustomobject]@{
                        Row = $dataFirstRow + $row - 1
                        FirstColumn = $dataFirstColumn
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
                        PriceSourceAmount = Matrix-Value $values $rowCount $columnCount $row 21
                        PriceSourceCurrency = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 22),
                            $invariant
                        ).Trim()
                        PriceSourceKind = [Convert]::ToString(
                            (Matrix-Value $values $rowCount $columnCount $row 23),
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

function Round-ProjectedPrice([decimal]$value, [int]$digits) {
    $quantum = [decimal]1
    for ($index = 0; $index -lt $digits; $index++) {
        $quantum *= [decimal]10
    }
    return [Math]::Round(
        $value / $quantum,
        0,
        [System.MidpointRounding]::AwayFromZero
    ) * $quantum
}

function Expected-ProjectedPrice(
    [object]$productRow,
    [object]$syncRow,
    [int]$roundingDigits
) {
    $amount = Numeric-Or-Null $syncRow.PriceSourceAmount
    if ($null -eq $amount -or $amount -le 0) { return $null }
    $currency = [Convert]::ToString(
        $syncRow.PriceSourceCurrency,
        $invariant
    ).Trim().ToUpperInvariant()
    $kind = [Convert]::ToString(
        $syncRow.PriceSourceKind,
        $invariant
    ).Trim().ToLowerInvariant()
    $profitPercent = Numeric-Or-Null $syncRow.ProfitPercent

    switch ($kind) {
        'sale_price_direct' {
            if ($currency -ne 'IRR' -or ([decimal]$amount % [decimal]10) -ne 0) {
                return $null
            }
            return [decimal]$amount / [decimal]10
        }
        'partner_price' {
            if ($currency -ne 'IRR' -or $null -eq $profitPercent -or $profitPercent -lt 0) {
                return $null
            }
            $unrounded = (
                ([decimal]$amount / [decimal]10) *
                ([decimal]1 + ([decimal]$profitPercent / [decimal]100))
            )
            return Round-ProjectedPrice $unrounded $roundingDigits
        }
        'foreign_price' {
            $factor = Currency-Factor-Or-Null $currency $syncRow
            $weight = Numeric-Or-Null $productRow.Weight
            $shipping = Numeric-Or-Null $syncRow.Shipping
            $shippingFactor = Currency-Factor-Or-Null $syncRow.ShippingCurrency $syncRow
            if ($null -eq $factor -or $factor -le 0 -or
                $null -eq $weight -or $weight -lt 0 -or
                $null -eq $shipping -or $shipping -lt 0 -or
                $null -eq $shippingFactor -or $shippingFactor -le 0 -or
                $null -eq $profitPercent -or $profitPercent -lt 0) {
                return $null
            }
            $unrounded = (
                ([decimal]$amount * [decimal]$factor) +
                (([decimal]$weight / [decimal]1000) *
                    [decimal]$shipping * [decimal]$shippingFactor)
            ) * (
                [decimal]1 + ([decimal]$profitPercent / [decimal]100)
            )
            return Round-ProjectedPrice $unrounded $roundingDigits
        }
        default { return $null }
    }
}

function Test-TransientPricePreview(
    [object]$excel,
    [object]$book,
    [object]$productSnapshot,
    [object]$syncDictionary,
    [int]$roundingDigits
) {
    $sheet = $null
    $syncSheet = $null
    $priceCell = $null
    $rateCell = $null
    $wooPriceCell = $null
    $selectedRowRange = $null
    $selectedCell = $null
    $displayFormat = $null
    $font = $null
    $interior = $null
    $definedName = $null
    $marker = $null
    $savedRate = $null
    $savedWooPrice = $null
    $injected = $false
    $eventsWereEnabled = [bool]$excel.EnableEvents
    try {
        [void](Reset-SelectedProductRow $excel $book)
        $sheet = $book.Worksheets.Item(1)
        $syncSheet = $book.Worksheets.Item(4)
        $productRows = @($productSnapshot.Rows)
        $tableFirstColumn = [int]$productSnapshot.TableFirstColumn
        $tableColumnCount = [int]$productSnapshot.TableColumnCount
        if ($productRows.Count -eq 0 -or $tableFirstColumn -le 0 -or
            $tableColumnCount -le 0) {
            return [pscustomobject]@{
                found = $false
                row = 0
                code = ''
                expected = $null
                selectedValue = $null
                markerRow = 0
                grayFont = $false
                fontColor = $null
                yellowCellCount = 0
                expectedYellowCellCount = 0
                resetValue = $null
            }
        }
        $candidate = $null
        $expected = $null
        foreach ($row in $productRows) {
            $stock = Numeric-Or-Null $row.Stock
            if ($null -eq $stock -or $stock -gt 0) {
                continue
            }
            if (-not $syncDictionary.Values.ContainsKey([string]$row.Code)) {
                continue
            }
            $syncRow = $syncDictionary.Values[[string]$row.Code]
            $projected = Expected-ProjectedPrice $row $syncRow $roundingDigits
            if ($null -ne $projected -and $projected -gt 0) {
                $priceCell = $sheet.Cells.Item(
                    [int]$row.Row,
                    $tableFirstColumn
                )
                $rateCell = $sheet.Cells.Item(
                    [int]$row.Row,
                    $tableFirstColumn + 4
                )
                $wooPriceCell = $syncSheet.Cells.Item(
                    [int]$syncRow.Row,
                    [int]$syncRow.FirstColumn + 9
                )
                $savedRate = $rateCell.Value2
                $savedWooPrice = $wooPriceCell.Value2
                $excel.EnableEvents = $false
                [void]$rateCell.ClearContents()
                [void]$wooPriceCell.ClearContents()
                [void](Reset-SelectedProductRow $excel $book)
                [void]$priceCell.Calculate()
                if ($null -eq (Numeric-Or-Null $priceCell.Value2)) {
                    $candidate = $row
                    $expected = [double]$projected
                    $injected = $true
                    break
                }
                $rateCell.Value2 = $savedRate
                $wooPriceCell.Value2 = $savedWooPrice
                [void]$priceCell.Calculate()
                Release-ComObject $wooPriceCell
                Release-ComObject $rateCell
                Release-ComObject $priceCell
                $wooPriceCell = $null
                $rateCell = $null
                $priceCell = $null
            }
        }
        if ($null -eq $candidate) {
            return [pscustomobject]@{
                found = $false
                row = 0
                code = ''
                expected = $null
                selectedValue = $null
                markerRow = 0
                grayFont = $false
                fontColor = $null
                yellowCellCount = 0
                expectedYellowCellCount = 0
                resetValue = $null
            }
        }

        [void]$book.Activate()
        $sheet = $book.Worksheets.Item(1)
        [void]$sheet.Activate()
        $excel.EnableEvents = $true
        [void]$priceCell.Select()
        [void]$priceCell.Calculate()
        $selectedValue = Numeric-Or-Null $priceCell.Value2

        $definedName = $book.Names.Item('ProjectedPricePreviewRow')
        $marker = $definedName.RefersToRange
        $markerRow = [int]$marker.Value2

        $displayFormat = $priceCell.DisplayFormat
        $font = $displayFormat.Font
        $fontColor = [long]$font.Color
        $red = [int]($fontColor -band 255)
        $green = [int](($fontColor -shr 8) -band 255)
        $blue = [int](($fontColor -shr 16) -band 255)
        $channelMaximum = [Math]::Max($red, [Math]::Max($green, $blue))
        $channelMinimum = [Math]::Min($red, [Math]::Min($green, $blue))
        $channelAverage = ($red + $green + $blue) / 3
        $grayFont = (
            ($channelMaximum - $channelMinimum) -le 24 -and
            $channelAverage -ge 64 -and $channelAverage -le 192
        )
        Release-ComObject $font
        Release-ComObject $displayFormat
        $font = $null
        $displayFormat = $null

        $selectedRowRange = $priceCell.Resize(1, $tableColumnCount)
        $yellowCellCount = 0
        for ($column = 1; $column -le $selectedRowRange.Columns.Count; $column++) {
            $selectedCell = $selectedRowRange.Cells.Item(1, $column)
            $displayFormat = $selectedCell.DisplayFormat
            $interior = $displayFormat.Interior
            if ([int]$interior.Color -eq 13432063) {
                $yellowCellCount += 1
            }
            Release-ComObject $interior
            Release-ComObject $displayFormat
            Release-ComObject $selectedCell
            $interior = $null
            $displayFormat = $null
            $selectedCell = $null
        }
        $expectedYellowCellCount = [int]$selectedRowRange.Columns.Count

        [void](Reset-SelectedProductRow $excel $book)
        [void]$priceCell.Calculate()
        $resetValue = Numeric-Or-Null $priceCell.Value2
        return [pscustomobject]@{
            found = $true
            row = [int]$candidate.Row
            code = [string]$candidate.Code
            expected = $expected
            selectedValue = $selectedValue
            markerRow = $markerRow
            grayFont = $grayFont
            fontColor = $fontColor
            yellowCellCount = $yellowCellCount
            expectedYellowCellCount = $expectedYellowCellCount
            resetValue = $resetValue
        }
    } finally {
        try {
            $excel.EnableEvents = $false
            if ($injected -and $null -ne $rateCell -and $null -ne $wooPriceCell) {
                $rateCell.Value2 = $savedRate
                $wooPriceCell.Value2 = $savedWooPrice
            }
            [void](Reset-SelectedProductRow $excel $book)
        } catch {}
        $excel.EnableEvents = $eventsWereEnabled
        Release-ComObject $marker
        Release-ComObject $definedName
        Release-ComObject $interior
        Release-ComObject $font
        Release-ComObject $displayFormat
        Release-ComObject $selectedCell
        Release-ComObject $selectedRowRange
        Release-ComObject $wooPriceCell
        Release-ComObject $rateCell
        Release-ComObject $priceCell
        Release-ComObject $syncSheet
        Release-ComObject $sheet
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
$workbooks = $null
$candidateBook = $null
$referenceBook = $null
$validatorJobHandle = [IntPtr]::Zero
$excelProcessIdentity = $null
$excelProcessAssignedToJob = $false
$excelProcessId = 0
$excelProcessExited = $false
$excelProcessExitCode = $null
$validationException = $null
$cleanupExceptions = [Collections.Generic.List[Exception]]::new()
$report = $null
$syncRan = $false
$syncSucceeded = $false
$syncOperation = ''
$syncError = ''
$syncDiagnostic = ''
try {
    Set-ValidatorStage 'creating_excel'
    $validatorJobHandle = New-ValidatorKillOnCloseJob
    $excel = New-Object -ComObject Excel.Application
    $excelProcessIdentity = Get-ExcelProcessIdentity $excel
    $excelProcessId = [int]$excelProcessIdentity.Id
    # Persist the exact identity immediately, then explicitly contain the COM
    # local server itself. Excel may be launched by the COM service rather than
    # as a child of powershell.exe, so host-job inheritance is not sufficient.
    Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $false
    $excelProcessAssignedToJob = Add-ValidatorProcessToJob $validatorJobHandle $excelProcessIdentity
    Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $excelProcessAssignedToJob
    $excel.Visible = $false
    $excel.DisplayAlerts = $false
    $excel.AskToUpdateLinks = $false
    $excel.EnableEvents = $false

    # The validator always runs local audit/fixture macros embedded in the
    # candidate. --no-sync suppresses only RefreshAllData; disabling every
    # macro here made the remaining validation path impossible by definition.
    $excel.AutomationSecurity = 1

    Set-ValidatorStage 'opening_candidate' $candidatePath
    if ([IO.Path]::GetExtension($candidatePath).ToLowerInvariant() -eq '.xltm') {
        $workbooks = $excel.Workbooks
        $candidateBook = $workbooks.Add($candidatePath)
    } else {
        $workbooks = $excel.Workbooks
        $candidateBook = $workbooks.Open($candidatePath, 0, $true)
    }
    Release-ComObject $workbooks
    $workbooks = $null
    Set-ValidatorStage 'candidate_opened' ([string]$candidateBook.Name)

    if ($runSync) {
        Set-ValidatorStage 'starting_live_sync'
        $macroBookName = ([string]$candidateBook.Name).Replace("'", "''")
        [void]$excel.Run(
            "'$macroBookName'!ProductCatalogSync.RefreshAllDataForValidation"
        )
        $syncRan = $true
        $syncDeadline = [DateTime]::UtcNow.AddMilliseconds(
            [Math]::Max(1000, $validationTimeoutMilliseconds - 30000)
        )
        while (-not [bool](Invoke-ExcelBusyRetry {
            $excel.Run(
                "'$macroBookName'!ProductCatalogSync.AsyncPricingIdleForValidation"
            )
        } $syncDeadline 'checking asynchronous synchronization completion')) {
            if ([DateTime]::UtcNow -ge $syncDeadline) {
                try {
                    [void]$excel.Run(
                        "'$macroBookName'!ProductCatalogSync.CancelActivePricingOperations"
                    )
                } catch {}
                throw 'Asynchronous live synchronization did not reach a terminal callback before the validator deadline.'
            }
            # This waits only in the out-of-process native harness. The VBA
            # client itself never polls WinHTTP readiness or HTTP job status.
            Start-Sleep -Milliseconds 100
        }
        $syncOperation = [Convert]::ToString(
            $excel.Run(
                "'$macroBookName'!ProductCatalogSync.LastPricingOperationForValidation"
            ),
            $invariant
        )
        $syncSucceeded = [bool]$excel.Run(
            "'$macroBookName'!ProductCatalogSync.LastPricingOperationSucceededForValidation"
        )
        $syncError = [Convert]::ToString(
            $excel.Run(
                "'$macroBookName'!ProductCatalogSync.LastPricingOperationErrorForValidation"
            ),
            $invariant
        )
        $syncDiagnostic = [Convert]::ToString(
            $excel.Run(
                "'$macroBookName'!ProductCatalogSync.LastPricingOperationDiagnosticForValidation"
            ),
            $invariant
        )
        if (-not $syncSucceeded) {
            $syncFailureStatus = [Convert]::ToString(
                (Sheet-Scalar $candidateBook 3 'B6'),
                $invariant
            )
            throw "Live synchronization '$syncOperation' failed before validation: $syncFailureStatus $syncError; diagnostic=$syncDiagnostic"
        }
        Set-ValidatorStage 'live_sync_completed' $syncOperation
    }
    Set-ValidatorStage 'calculating_candidate'
    $excel.CalculateFullRebuild()
    Reset-SelectedProductRow $excel $candidateBook
    $candidateSyncStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 3 'B6'),
        $invariant
    )
    $candidatePricingStatus = [Convert]::ToString(
        (Sheet-Scalar $candidateBook 3 'B23'),
        $invariant
    )

    Set-ValidatorStage 'checking_branding'
    $brandingLayout = Test-BrandingLayout $candidateBook
    Set-ValidatorStage 'checking_literal_search'
    $searchLiteral = Test-SearchLiteralText $excel $candidateBook
    Set-ValidatorStage 'checking_status_summary'
    $statusSummary = Test-StatusSummaryFormatter $excel $candidateBook
    Set-ValidatorStage 'reading_candidate_products'
    $candidateProducts = Read-Products $candidateBook
    Set-ValidatorStage 'checking_title_direction'
    $titleDirection = Test-ProductTitleDirection $candidateBook
    Set-ValidatorStage 'checking_fonts'
    $fontAudit = Test-FontAudit $excel $candidateBook
    Set-ValidatorStage 'checking_writeback_ui'
    $pricingWritebackUI = Test-PricingWritebackUI $excel $candidateBook
    Set-ValidatorStage 'checking_progress_ui'
    $pricingProgressUI = Test-PricingProgressUI $excel $candidateBook
    Set-ValidatorStage 'checking_product_search'
    $candidateSearch = Test-ProductSearch $excel $candidateBook $candidateProducts.Rows
    Set-ValidatorStage 'checking_search_crash_regression'
    $searchRegression = Test-SearchCrashRegression $excel $candidateBook
    Reset-SelectedProductRow $excel $candidateBook
    Set-ValidatorStage 'reading_candidate_sync_data'
    $candidateSyncData = Read-SyncData $candidateBook
    $candidateConfig = [pscustomobject]@{
        autoSyncOnOpen = [Convert]::ToString(
            (Sheet-Scalar $candidateBook 3 'B5'),
            $invariant
        )
        yuan = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B10')
        usd = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B11')
        shipping = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B14')
        profit = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B13')
        roundingDigits = Numeric-Or-Null (Sheet-Scalar $candidateBook 3 'B15')
        cardYuan = Numeric-Or-Null (Table-Scalar $candidateBook 'Yuan_Price')
        cardShipping = Numeric-Or-Null (Table-Scalar $candidateBook 'Shipping')
        cardProfit = Numeric-Or-Null (Table-Scalar $candidateBook 'Profit')
    }
    Set-ValidatorStage 'checking_workbook_errors'
    $errors = Workbook-Errors $candidateBook

    $excel.AutomationSecurity = 3
    Set-ValidatorStage 'opening_reference' $referencePath
    $workbooks = $excel.Workbooks
    $referenceBook = $workbooks.Open($referencePath, 0, $true)
    Release-ComObject $workbooks
    $workbooks = $null
    Set-ValidatorStage 'reading_reference_products'
    $referenceProducts = Read-Products $referenceBook
    $referenceConfig = [pscustomobject]@{
        yuan = Numeric-Or-Null (Table-Scalar $referenceBook 'Yuan_Price')
        shipping = Numeric-Or-Null (Table-Scalar $referenceBook 'Shipping')
        profit = Numeric-Or-Null (Table-Scalar $referenceBook 'Profit')
    }

    Set-ValidatorStage 'comparing_candidate_reference'
    $candidateDictionary = Row-Dictionary $candidateProducts.Rows
    $candidateProductDictionary = ProductCode-Dictionary $candidateProducts.Rows
    $syncDictionary = Row-Dictionary $candidateSyncData.Rows
    $transientPricePreview = Test-TransientPricePreview $excel $candidateBook $candidateProducts $syncDictionary ([int]$candidateConfig.roundingDigits)
    Reset-SelectedProductRow $excel $candidateBook
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

    Set-ValidatorStage 'assembling_report'
    $report = [pscustomobject]@{
        sync = [pscustomobject]@{
            requested = $runSync
            ran = $syncRan
            succeeded = $syncSucceeded
            operation = $syncOperation
            error = $syncError
            diagnostic = $syncDiagnostic
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
        branding = $brandingLayout
        searchLiteral = $searchLiteral
        statusSummary = $statusSummary
        titleDirection = $titleDirection
        fontAudit = $fontAudit
        pricingWritebackUI = $pricingWritebackUI
        pricingProgressUI = $pricingProgressUI
        searchRegression = $searchRegression
        transientPricePreview = $transientPricePreview
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
}
catch {
    $validationException = $_.Exception
}
finally {
    Set-ValidatorStage 'cleanup_started'
    if ($null -ne $referenceBook) {
        try {
            [void](Invoke-ExcelBusyRetry {
                $referenceBook.Close($false)
            } ([DateTime]::UtcNow.AddSeconds(5)) 'closing the reference workbook')
        }
        catch {
            $cleanupExceptions.Add([InvalidOperationException]::new(
                'Failed to close the reference workbook.',
                $_.Exception
            ))
        }
    }
    Release-ComObject $referenceBook
    $referenceBook = $null
    if ($null -ne $candidateBook) {
        try {
            [void](Invoke-ExcelBusyRetry {
                $candidateBook.Close($false)
            } ([DateTime]::UtcNow.AddSeconds(5)) 'closing the candidate workbook')
        }
        catch {
            $cleanupExceptions.Add([InvalidOperationException]::new(
                'Failed to close the candidate workbook.',
                $_.Exception
            ))
        }
    }
    Release-ComObject $candidateBook
    $candidateBook = $null
    Release-ComObject $workbooks
    $workbooks = $null
    try { Invoke-ComFinalizerBarrier }
    catch {
        $cleanupExceptions.Add([InvalidOperationException]::new(
            'Failed to finalize workbook COM wrappers.',
            $_.Exception
        ))
    }
    if ($null -ne $excel) {
        try { $excel.EnableEvents = $true }
        catch {
            $cleanupExceptions.Add([InvalidOperationException]::new(
                'Failed to restore Excel event handling before Quit.',
                $_.Exception
            ))
        }
        try {
            [void](Invoke-ExcelBusyRetry {
                $excel.Quit()
            } ([DateTime]::UtcNow.AddSeconds(5)) 'closing the validator Excel process')
        }
        catch {
            $cleanupExceptions.Add([InvalidOperationException]::new(
                'Excel Application.Quit failed.',
                $_.Exception
            ))
        }
    }
    Release-ComObject $excel
    $excel = $null
    try { Invoke-ComFinalizerBarrier }
    catch {
        $cleanupExceptions.Add([InvalidOperationException]::new(
            'Failed to finalize the Excel application COM wrapper.',
            $_.Exception
        ))
    }
    if ($null -ne $excelProcessIdentity) {
        try {
            $exitResult = Wait-ValidatorProcessExit $excelProcessIdentity 15000 @(0)
            $excelProcessExited = [bool]$exitResult.Exited
            $excelProcessExitCode = [int]$exitResult.ExitCode
        }
        catch {
            $cleanupExceptions.Add([InvalidOperationException]::new(
                'The hidden validator Excel process did not exit normally.',
                $_.Exception
            ))
        }
        finally {
            $excelProcessIdentity.Process.Dispose()
        }
    }
}

$combinedException = New-ValidatorCombinedException $validationException $cleanupExceptions
if ($null -ne $combinedException) {
    throw $combinedException
}

$report | Add-Member -NotePropertyName validatorExcelProcessId -NotePropertyValue $excelProcessId
$report | Add-Member -NotePropertyName validatorExcelProcessAssignedToJob -NotePropertyValue $excelProcessAssignedToJob
$report | Add-Member -NotePropertyName validatorExcelProcessExited -NotePropertyValue $excelProcessExited
$report | Add-Member -NotePropertyName validatorExcelProcessExitCode -NotePropertyValue $excelProcessExitCode
$report | Add-Member -NotePropertyName validatorExcelProcessStartTimeUtc -NotePropertyValue $excelProcessIdentity.StartTimeUtc.ToString('o')
$report | Add-Member -NotePropertyName validatorExcelExecutablePath -NotePropertyValue $excelProcessIdentity.ExecutablePath
Set-ValidatorStage 'completed'
[Console]::Out.WriteLine(($report | ConvertTo-Json -Depth 10 -Compress))
`;

const ownedProcessCleanupPowerShell = String.raw`
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
${processSafetyPowerShell}
$identityPath = $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH
if ([string]::IsNullOrWhiteSpace($identityPath) -or
    -not (Test-Path -LiteralPath $identityPath -PathType Leaf)) {
    [ordered]@{ passed = $false; status = 'identity_missing' } |
        ConvertTo-Json -Compress
    exit 2
}

$identity = Get-Content -LiteralPath $identityPath -Raw | ConvertFrom-Json
$process = $null
try {
    try {
        $actualIdentity = Get-ValidatorProcessIdentityById (
            [int]$identity.pid
        ) ([IO.Path]::GetFileName([string]$identity.executable_path))
        $process = [Diagnostics.Process]$actualIdentity.Process
    }
    catch [ArgumentException] {
        [ordered]@{ passed = $true; status = 'already_exited' } |
            ConvertTo-Json -Compress
        exit 0
    }

    $actualStartTicks = [long]$actualIdentity.StartTimeUtcTicks
    $actualPath = [string]$actualIdentity.ExecutablePath
    if ($actualStartTicks -ne [long]$identity.start_time_utc_ticks -or
        -not $actualPath.Equals(
            [string]$identity.executable_path,
            [StringComparison]::OrdinalIgnoreCase
        )) {
        [ordered]@{
            passed = $true
            status = 'identity_mismatch_not_owned'
            pid = [int]$identity.pid
        } | ConvertTo-Json -Compress
        exit 0
    }

    $process.Kill()
    if (-not $process.WaitForExit(5000)) {
        [ordered]@{
            passed = $false
            status = 'owned_process_did_not_exit'
            pid = [int]$identity.pid
        } | ConvertTo-Json -Compress
        exit 3
    }
    [ordered]@{
        passed = $true
        status = 'terminated_exact_owned_process'
        pid = [int]$identity.pid
    } | ConvertTo-Json -Compress
}
finally {
    if ($null -ne $process) { $process.Dispose() }
}
`;

function parsePowerShellJson(result, purpose) {
  const output = String(result.stdout || '').replace(/^\uFEFF/u, '').trim();
  if (!output) {
    throw new Error(`${purpose} emitted no JSON; stderr=${String(result.stderr || '').trim()}`);
  }
  try {
    return JSON.parse(output.split(/\r?\n/u).filter(Boolean).at(-1));
  } catch (error) {
    throw new Error(`${purpose} emitted invalid JSON: ${error.message}; output=${output}`);
  }
}

function runPowerShellScript(scriptPath, environment, timeoutMs) {
  return spawnSync(
    'powershell.exe',
    [
      '-NoLogo',
      '-NoProfile',
      '-NonInteractive',
      '-ExecutionPolicy',
      'Bypass',
      '-File',
      scriptPath,
    ],
    {
      cwd: repoRoot,
      encoding: 'utf8',
      env: { ...process.env, ...environment },
      maxBuffer: 16 * 1024 * 1024,
      timeout: timeoutMs,
      windowsHide: true,
    },
  );
}

function cleanupExactOwnedProcess(tempDirectory, identityPath) {
  const cleanupPath = path.join(tempDirectory, 'cleanup-owned-process.ps1');
  fs.writeFileSync(cleanupPath, `\uFEFF${ownedProcessCleanupPowerShell}\n`, 'utf8');
  const result = runPowerShellScript(
    cleanupPath,
    { PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH: identityPath },
    10000,
  );
  const report = parsePowerShellJson(result, 'owned-process cleanup');
  if (result.error || result.status !== 0 || !report.passed) {
    throw new Error(
      `owned-process cleanup failed: status=${result.status} `
      + `error=${result.error ? result.error.message : ''} report=${JSON.stringify(report)}`,
    );
  }
  return report;
}

function resolveAbnormalValidatorCleanup(
  tempDirectory,
  identityPath,
  hostResult,
  cleanupFunction = cleanupExactOwnedProcess,
) {
  const abnormal = !hostResult || Boolean(hostResult.error) || hostResult.status !== 0;
  if (!abnormal) {
    return { abnormal: false, proven: true, report: null, error: null };
  }
  if (!fs.existsSync(identityPath)) {
    return {
      abnormal: true,
      proven: false,
      report: null,
      error: new Error(
        'validator host exited before the exact Excel identity was published; scoped cleanup is unproven',
      ),
    };
  }
  try {
    const report = cleanupFunction(tempDirectory, identityPath);
    if (!report || report.passed !== true) {
      throw new Error(`exact-process cleanup did not return a proven-success report: ${JSON.stringify(report)}`);
    }
    return { abnormal: true, proven: true, report, error: null };
  } catch (error) {
    return { abnormal: true, proven: false, report: null, error };
  }
}

function finalizeValidatorTempDirectory(tempDirectory, abnormalCleanupOutcome) {
  if (abnormalCleanupOutcome?.abnormal && abnormalCleanupOutcome.proven !== true) {
    // The private temp contents are the only deterministic recovery evidence
    // when the exact owned process could not be proven terminated. Keep the
    // identity and scoped cleanup script when published, or the pre-identity
    // script evidence otherwise; never guess at unrelated Excel processes.
    return path.resolve(tempDirectory);
  }
  fs.rmSync(tempDirectory, { recursive: true, force: true });
  return null;
}

function validatorRecoveryAggregate(primaryError, cleanupOutcome, tempDirectory) {
  const recoveryError = cleanupOutcome?.error || new Error('scoped cleanup is unproven');
  const preservedPath = path.resolve(tempDirectory);
  return new AggregateError(
    [primaryError, recoveryError],
    `${primaryError.message}; scoped Excel cleanup is unproven: ${recoveryError.message}; `
      + `recovery evidence preserved for exact-process remediation: ${preservedPath}`,
  );
}

function runProcessSafetySelfTest() {
  const tempDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-validator-safety-'));
  const readinessToken = randomUUID();
  const writeScript = (name, content) => {
    const scriptPath = path.join(tempDirectory, name);
    fs.writeFileSync(scriptPath, `\uFEFF${content}\n`, 'utf8');
    return scriptPath;
  };
  const gatedExitChild = (readyVariable, releaseVariable, exitCode) => String.raw`
[IO.File]::WriteAllText($env:${readyVariable}, $env:PATRIS_SELFTEST_READY_TOKEN)
$deadline = [DateTime]::UtcNow.AddSeconds(30)
while (-not (Test-Path -LiteralPath $env:${releaseVariable} -PathType Leaf)) {
    if ([DateTime]::UtcNow -ge $deadline) { exit 99 }
    Start-Sleep -Milliseconds 50
}
exit ${exitCode}
`;
  const exitSevenReady = path.join(tempDirectory, 'ready-exit-seven');
  const exitSevenRelease = path.join(tempDirectory, 'release-exit-seven');
  const exitZeroReady = path.join(tempDirectory, 'ready-exit-zero');
  const exitZeroRelease = path.join(tempDirectory, 'release-exit-zero');
  const behaviorLongReady = path.join(tempDirectory, 'ready-long-behavior');
  const timeoutLongReady = path.join(tempDirectory, 'ready-long-timeout');
  const missingReadyProbe = path.join(tempDirectory, 'ready-missing-probe');
  const malformedReadyProbe = path.join(tempDirectory, 'ready-malformed-probe');
  const exitSevenChild = writeScript(
    'exit-seven.ps1',
    gatedExitChild(
      'PATRIS_SELFTEST_EXIT_SEVEN_READY',
      'PATRIS_SELFTEST_EXIT_SEVEN_RELEASE',
      7,
    ),
  );
  const exitZeroChild = writeScript(
    'exit-zero.ps1',
    gatedExitChild(
      'PATRIS_SELFTEST_EXIT_ZERO_READY',
      'PATRIS_SELFTEST_EXIT_ZERO_RELEASE',
      0,
    ),
  );
  const longChild = writeScript(
    'long-child.ps1',
    String.raw`
[IO.File]::WriteAllText($env:PATRIS_SELFTEST_LONG_CHILD_READY, $env:PATRIS_SELFTEST_READY_TOKEN)
Start-Sleep -Seconds 60
exit 0
`,
  );
  const startChild = String.raw`
function Start-HiddenSelfTestChild([string]$scriptPath) {
    return Start-Process -FilePath 'powershell.exe' -ArgumentList @(
        '-NoLogo',
        '-NoProfile',
        '-NonInteractive',
        '-ExecutionPolicy',
        'Bypass',
        '-File',
        ('"' + $scriptPath + '"')
    ) -WindowStyle Hidden -PassThru
}

function Wait-SelfTestChildReady(
    [Diagnostics.Process]$process,
    [string]$readyPath,
    [string]$expectedValue,
    [int]$timeoutMilliseconds = 5000
) {
    $deadline = [DateTime]::UtcNow.AddMilliseconds($timeoutMilliseconds)
    $malformed = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($process.HasExited) {
            throw "Self-test child $($process.Id) exited before publishing readiness."
        }
        if (Test-Path -LiteralPath $readyPath -PathType Leaf) {
            $value = [IO.File]::ReadAllText($readyPath)
            if ($value -ceq $expectedValue) { return $true }
            $malformed = $true
        }
        Start-Sleep -Milliseconds 25
    }
    if ($malformed) {
        throw "Self-test child $($process.Id) published malformed readiness."
    }
    throw "Self-test child $($process.Id) did not publish readiness within $timeoutMilliseconds ms."
}
`;

  try {
    const behaviorScript = writeScript('behavior.ps1', String.raw`
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
${processSafetyPowerShell}
${startChild}
$jobHandle = New-ValidatorKillOnCloseJob
$crashRejected = $false
$identityHandlePassed = $false
$dualFailurePassed = $false
$explicitJobAssignmentPassed = $false
$missingReadinessRejected = $false
$malformedReadinessRejected = $false
$staleReadinessRejected = $false

$readinessProbeProcess = [Diagnostics.Process]::GetCurrentProcess()
try {
    try {
        [void](Wait-SelfTestChildReady $readinessProbeProcess $env:PATRIS_SELFTEST_MISSING_READY_PROBE $env:PATRIS_SELFTEST_READY_TOKEN 100)
    }
    catch {
        $missingReadinessRejected = $_.Exception.Message -match 'did not publish readiness'
    }
    [IO.File]::WriteAllText($env:PATRIS_SELFTEST_MALFORMED_READY_PROBE, 'not-ready')
    try {
        [void](Wait-SelfTestChildReady $readinessProbeProcess $env:PATRIS_SELFTEST_MALFORMED_READY_PROBE $env:PATRIS_SELFTEST_READY_TOKEN 100)
    }
    catch {
        $malformedReadinessRejected = $_.Exception.Message -match 'malformed readiness'
    }
}
finally {
    $readinessProbeProcess.Dispose()
}

$crashChild = Start-HiddenSelfTestChild $env:PATRIS_SELFTEST_EXIT_SEVEN
[void](Wait-SelfTestChildReady $crashChild $env:PATRIS_SELFTEST_EXIT_SEVEN_READY $env:PATRIS_SELFTEST_READY_TOKEN)
$staleReadinessRejected = [IO.File]::ReadAllText($env:PATRIS_SELFTEST_EXIT_SEVEN_READY) -ceq $env:PATRIS_SELFTEST_READY_TOKEN
$crashIdentity = Get-ValidatorProcessIdentityById $crashChild.Id 'powershell.exe'
$explicitJobAssignmentPassed = Add-ValidatorProcessToJob $jobHandle $crashIdentity
[IO.File]::WriteAllText($env:PATRIS_SELFTEST_EXIT_SEVEN_RELEASE, 'release')
try {
    try {
        [void](Wait-ValidatorProcessExit $crashIdentity 5000 @(0))
    }
    catch {
        $crashRejected = $_.Exception.Message -match 'unexpected code 7'
    }
}
finally {
    $crashIdentity.Process.Dispose()
    $crashChild.Dispose()
}

$firstChild = Start-HiddenSelfTestChild $env:PATRIS_SELFTEST_EXIT_ZERO
[void](Wait-SelfTestChildReady $firstChild $env:PATRIS_SELFTEST_EXIT_ZERO_READY $env:PATRIS_SELFTEST_READY_TOKEN)
$firstIdentity = Get-ValidatorProcessIdentityById $firstChild.Id 'powershell.exe'
$explicitJobAssignmentPassed = $explicitJobAssignmentPassed -and (Add-ValidatorProcessToJob $jobHandle $firstIdentity)
$secondChild = Start-HiddenSelfTestChild $env:PATRIS_SELFTEST_LONG_CHILD
[void](Wait-SelfTestChildReady $secondChild $env:PATRIS_SELFTEST_LONG_CHILD_READY $env:PATRIS_SELFTEST_READY_TOKEN)
$secondIdentity = Get-ValidatorProcessIdentityById $secondChild.Id 'powershell.exe'
$explicitJobAssignmentPassed = $explicitJobAssignmentPassed -and (Add-ValidatorProcessToJob $jobHandle $secondIdentity)
[IO.File]::WriteAllText($env:PATRIS_SELFTEST_EXIT_ZERO_RELEASE, 'release')
try {
    $firstResult = Wait-ValidatorProcessExit $firstIdentity 5000 @(0)
    $identityHandlePassed = (
        $firstResult.Id -eq $firstIdentity.Id -and
        $firstResult.StartTimeUtc.Ticks -eq $firstIdentity.StartTimeUtcTicks -and
        -not $secondIdentity.Process.HasExited
    )
}
finally {
    if (-not $secondIdentity.Process.HasExited) {
        $secondIdentity.Process.Kill()
        [void]$secondIdentity.Process.WaitForExit(5000)
    }
    $secondIdentity.Process.Dispose()
    $secondChild.Dispose()
    $firstIdentity.Process.Dispose()
    $firstChild.Dispose()
}

$cleanupFailures = [Collections.Generic.List[Exception]]::new()
$cleanupFailures.Add([InvalidOperationException]::new('cleanup sentinel'))
$combined = New-ValidatorCombinedException ([InvalidOperationException]::new('primary sentinel')) $cleanupFailures
$dualFailurePassed = (
    $combined -is [AggregateException] -and
    $combined.InnerExceptions.Count -eq 2 -and
    $combined.InnerExceptions[0].Message -eq 'primary sentinel' -and
    $combined.InnerExceptions[1].Message -eq 'cleanup sentinel'
)

[ordered]@{
    passed = $crashRejected -and $identityHandlePassed -and $dualFailurePassed -and
        $explicitJobAssignmentPassed -and $missingReadinessRejected -and $malformedReadinessRejected -and
        $staleReadinessRejected
    crash_exit_rejected = $crashRejected
    exact_process_handle_used = $identityHandlePassed
    dual_failure_preserved = $dualFailurePassed
    explicit_job_assignment_verified = $explicitJobAssignmentPassed
    missing_readiness_rejected = $missingReadinessRejected
    malformed_readiness_rejected = $malformedReadinessRejected
    stale_readiness_rejected = $staleReadinessRejected
} | ConvertTo-Json -Compress
`);
    // A formerly valid marker must not satisfy a new child generation. The
    // child replaces this stale value with the per-run nonce after its script
    // has actually started.
    fs.writeFileSync(exitSevenReady, 'ready', 'utf8');
    const behaviorResult = runPowerShellScript(
      behaviorScript,
      {
        PATRIS_SELFTEST_EXIT_SEVEN: exitSevenChild,
        PATRIS_SELFTEST_EXIT_SEVEN_READY: exitSevenReady,
        PATRIS_SELFTEST_EXIT_SEVEN_RELEASE: exitSevenRelease,
        PATRIS_SELFTEST_EXIT_ZERO: exitZeroChild,
        PATRIS_SELFTEST_EXIT_ZERO_READY: exitZeroReady,
        PATRIS_SELFTEST_EXIT_ZERO_RELEASE: exitZeroRelease,
        PATRIS_SELFTEST_LONG_CHILD: longChild,
        PATRIS_SELFTEST_LONG_CHILD_READY: behaviorLongReady,
        PATRIS_SELFTEST_MISSING_READY_PROBE: missingReadyProbe,
        PATRIS_SELFTEST_MALFORMED_READY_PROBE: malformedReadyProbe,
        PATRIS_SELFTEST_READY_TOKEN: readinessToken,
      },
      20000,
    );
    const behavior = parsePowerShellJson(behaviorResult, 'process-safety behavior test');
    if (behaviorResult.error || behaviorResult.status !== 0 || !behavior.passed) {
      throw new Error(`process-safety behavior test failed: ${JSON.stringify(behavior)}`);
    }

    const timeoutIdentity = path.join(tempDirectory, 'timeout-child-identity.json');
    const timeoutScript = writeScript('timeout.ps1', String.raw`
$ErrorActionPreference = 'Stop'
${processSafetyPowerShell}
${startChild}
$jobHandle = New-ValidatorKillOnCloseJob
$child = Start-HiddenSelfTestChild $env:PATRIS_SELFTEST_LONG_CHILD
[void](Wait-SelfTestChildReady $child $env:PATRIS_SELFTEST_LONG_CHILD_READY $env:PATRIS_SELFTEST_READY_TOKEN)
$identity = Get-ValidatorProcessIdentityById $child.Id 'powershell.exe'
$assigned = Add-ValidatorProcessToJob $jobHandle $identity
Write-ValidatorProcessIdentity $identity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $assigned
$identity.Process.Dispose()
$child.Dispose()
Start-Sleep -Seconds 60
`);
    const timeoutResult = runPowerShellScript(
      timeoutScript,
      {
        PATRIS_SELFTEST_LONG_CHILD: longChild,
        PATRIS_SELFTEST_LONG_CHILD_READY: timeoutLongReady,
        PATRIS_SELFTEST_READY_TOKEN: readinessToken,
        PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH: timeoutIdentity,
      },
      10000,
    );
    if (!timeoutResult.error || timeoutResult.error.code !== 'ETIMEDOUT') {
      throw new Error(`timeout self-test did not time out: ${JSON.stringify(timeoutResult.error)}`);
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 500);
    const timeoutCleanup = cleanupExactOwnedProcess(tempDirectory, timeoutIdentity);
    const goneReadback = cleanupExactOwnedProcess(tempDirectory, timeoutIdentity);
    const timeoutIdentityReport = JSON.parse(fs.readFileSync(timeoutIdentity, 'utf8'));
    if (timeoutIdentityReport.assigned_to_validator_job !== true) {
      throw new Error(`timeout child was not explicitly assigned to the validator job: ${JSON.stringify(timeoutIdentityReport)}`);
    }
    if (timeoutCleanup.status !== 'already_exited' || goneReadback.status !== 'already_exited') {
      throw new Error(
        `timeout child survived the validator job close: first=${JSON.stringify(timeoutCleanup)} `
        + `second=${JSON.stringify(goneReadback)}`,
      );
    }

    const preIdentityEvidenceDirectory = fs.mkdtempSync(
      path.join(os.tmpdir(), 'patris-validator-recovery-evidence-'),
    );
    const preIdentityPath = path.join(preIdentityEvidenceDirectory, 'excel-process-identity.json');
    const stubTimeout = {
      error: Object.assign(new Error('stubbed pre-identity timeout'), { code: 'ETIMEDOUT' }),
      status: null,
    };
    let preIdentityCleanupCalled = false;
    let preIdentityTimeoutEvidencePreserved = false;
    let preIdentityRecoveryMessagePresent = false;
    try {
      const outcome = resolveAbnormalValidatorCleanup(
        preIdentityEvidenceDirectory,
        preIdentityPath,
        stubTimeout,
        () => {
          preIdentityCleanupCalled = true;
          throw new Error('cleanup must not run without exact identity');
        },
      );
      const preservedPath = finalizeValidatorTempDirectory(preIdentityEvidenceDirectory, outcome);
      preIdentityTimeoutEvidencePreserved = preservedPath === path.resolve(preIdentityEvidenceDirectory)
        && fs.existsSync(preIdentityEvidenceDirectory)
        && !preIdentityCleanupCalled;
      preIdentityRecoveryMessagePresent = String(outcome.error?.message || '')
        .includes('before the exact Excel identity was published');
      if (!preIdentityTimeoutEvidencePreserved || !preIdentityRecoveryMessagePresent) {
        throw new Error('pre-identity timeout did not preserve recovery evidence and message');
      }
    } finally {
      fs.rmSync(preIdentityEvidenceDirectory, { recursive: true, force: true });
    }

    const cleanupFailureEvidenceDirectory = fs.mkdtempSync(
      path.join(os.tmpdir(), 'patris-validator-cleanup-failure-evidence-'),
    );
    const cleanupFailureIdentityPath = path.join(
      cleanupFailureEvidenceDirectory,
      'excel-process-identity.json',
    );
    let cleanupFailureEvidencePreserved = false;
    try {
      fs.writeFileSync(cleanupFailureIdentityPath, '{"pid":1234}', 'utf8');
      const outcome = resolveAbnormalValidatorCleanup(
        cleanupFailureEvidenceDirectory,
        cleanupFailureIdentityPath,
        stubTimeout,
        (directory) => {
          fs.writeFileSync(path.join(directory, 'cleanup-owned-process.ps1'), 'stub cleanup', 'utf8');
          throw new Error('simulated exact-process cleanup failure');
        },
      );
      const preservedPath = finalizeValidatorTempDirectory(cleanupFailureEvidenceDirectory, outcome);
      cleanupFailureEvidencePreserved = preservedPath === path.resolve(cleanupFailureEvidenceDirectory)
        && fs.existsSync(cleanupFailureIdentityPath)
        && fs.existsSync(path.join(cleanupFailureEvidenceDirectory, 'cleanup-owned-process.ps1'))
        && String(outcome.error?.message || '').includes('simulated exact-process cleanup failure');
      if (!cleanupFailureEvidencePreserved) {
        throw new Error('failed exact cleanup did not preserve identity recovery evidence');
      }
    } finally {
      fs.rmSync(cleanupFailureEvidenceDirectory, { recursive: true, force: true });
    }

    return {
      passed: true,
      pre_identity_timeout_evidence_preserved: preIdentityTimeoutEvidencePreserved,
      pre_identity_recovery_message_present: preIdentityRecoveryMessagePresent,
      recovery_evidence_preserved_on_cleanup_failure: cleanupFailureEvidencePreserved,
      behavior,
      timeout: {
        spawn_error_code: timeoutResult.error.code,
        assigned_to_job: timeoutIdentityReport.assigned_to_validator_job,
        first_cleanup_status: timeoutCleanup.status,
        gone_readback_status: goneReadback.status,
      },
    };
  } finally {
    fs.rmSync(tempDirectory, { recursive: true, force: true });
  }
}

function runNativeExcelTimeoutSelfTest() {
  const tempDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'patris-validator-native-timeout-'));
  const writeScript = (name, content) => {
    const scriptPath = path.join(tempDirectory, name);
    fs.writeFileSync(scriptPath, `\uFEFF${content}\n`, 'utf8');
    return scriptPath;
  };
  const controlIdentity = path.join(tempDirectory, 'control-excel-identity.json');
  const timeoutIdentity = path.join(tempDirectory, 'timeout-excel-identity.json');
  let activeIdentityPath = controlIdentity;
  let abnormalCleanupOutcome = null;
  const excelSetup = String.raw`
function Release-SelfTestComObject([object]$value) {
    if ($null -ne $value -and [Runtime.InteropServices.Marshal]::IsComObject($value)) {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($value)
    }
}
function Invoke-SelfTestFinalizerBarrier {
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}
`;

  try {
    const controlScript = writeScript('native-control.ps1', String.raw`
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
${processSafetyPowerShell}
${excelSetup}
$jobHandle = New-ValidatorKillOnCloseJob
$excel = New-Object -ComObject Excel.Application
$identity = Get-ExcelProcessIdentity $excel
Write-ValidatorProcessIdentity $identity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $false
$assigned = Add-ValidatorProcessToJob $jobHandle $identity
Write-ValidatorProcessIdentity $identity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $assigned
$excel.Visible = $false
$excel.DisplayAlerts = $false
$excel.Quit()
Release-SelfTestComObject $excel
$excel = $null
Invoke-SelfTestFinalizerBarrier
$exitResult = Wait-ValidatorProcessExit $identity 15000 @(0)
$identity.Process.Dispose()
[ordered]@{
    passed = $assigned -and $exitResult.Exited -and $exitResult.ExitCode -eq 0
    assigned_to_job = $assigned
    exit_code = $exitResult.ExitCode
    pid = $exitResult.Id
    executable_path = $exitResult.ExecutablePath
} | ConvertTo-Json -Compress
`);
    const controlResult = runPowerShellScript(
      controlScript,
      { PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH: controlIdentity },
      60000,
    );
    const control = parsePowerShellJson(controlResult, 'native Excel control');
    if (controlResult.error || controlResult.status !== 0 || !control.passed) {
      throw new Error(`native Excel control failed: ${JSON.stringify(control)}`);
    }

    activeIdentityPath = timeoutIdentity;
    const timeoutScript = writeScript('native-timeout.ps1', String.raw`
$ErrorActionPreference = 'Stop'
${processSafetyPowerShell}
$jobHandle = New-ValidatorKillOnCloseJob
$excel = New-Object -ComObject Excel.Application
$identity = Get-ExcelProcessIdentity $excel
Write-ValidatorProcessIdentity $identity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $false
$assigned = Add-ValidatorProcessToJob $jobHandle $identity
Write-ValidatorProcessIdentity $identity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $assigned
$excel.Visible = $false
$excel.DisplayAlerts = $false
Start-Sleep -Seconds 60
`);
    const timeoutResult = runPowerShellScript(
      timeoutScript,
      { PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH: timeoutIdentity },
      10000,
    );
    if (!timeoutResult.error || timeoutResult.error.code !== 'ETIMEDOUT') {
      throw new Error(`native Excel timeout self-test did not time out: ${JSON.stringify(timeoutResult.error)}`);
    }
    if (!fs.existsSync(timeoutIdentity)) {
      throw new Error('native Excel timeout occurred before the exact Excel identity was fenced');
    }
    const identity = JSON.parse(fs.readFileSync(timeoutIdentity, 'utf8'));
    if (identity.assigned_to_validator_job !== true
        || path.basename(String(identity.executable_path || '')).toUpperCase() !== 'EXCEL.EXE') {
      throw new Error(`native Excel was not explicitly fenced before timeout: ${JSON.stringify(identity)}`);
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 1000);
    const firstCleanup = cleanupExactOwnedProcess(tempDirectory, timeoutIdentity);
    const goneReadback = cleanupExactOwnedProcess(tempDirectory, timeoutIdentity);
    if (firstCleanup.status !== 'already_exited' || goneReadback.status !== 'already_exited') {
      throw new Error(
        `native Excel survived the validator job close: first=${JSON.stringify(firstCleanup)} `
        + `second=${JSON.stringify(goneReadback)}`,
      );
    }

    return {
      passed: true,
      control,
      timeout: {
        spawn_error_code: timeoutResult.error.code,
        assigned_to_job: identity.assigned_to_validator_job,
        pid: identity.pid,
        executable_path: identity.executable_path,
        first_cleanup_status: firstCleanup.status,
        gone_readback_status: goneReadback.status,
      },
    };
  } catch (primaryError) {
    abnormalCleanupOutcome = resolveAbnormalValidatorCleanup(
      tempDirectory,
      activeIdentityPath,
      { error: primaryError, status: 1 },
    );
    if (!abnormalCleanupOutcome.proven) {
      throw validatorRecoveryAggregate(primaryError, abnormalCleanupOutcome, tempDirectory);
    }
    throw primaryError;
  } finally {
    finalizeValidatorTempDirectory(tempDirectory, abnormalCleanupOutcome);
  }
}

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
  try {
    const moduleSource = fs.readFileSync(
      path.join(repoRoot, 'docs', 'examples', 'vba', 'ProductCatalogSync.bas'),
      'utf8',
    );
    const workbookSource = fs.readFileSync(
      path.join(repoRoot, 'docs', 'examples', 'vba', 'ThisWorkbook.cls'),
      'utf8',
    );
    const procedure = (source, name) => {
      const match = new RegExp(
        `(?:Public|Private) Sub ${name}\\b[\\s\\S]*?\\r?\\nEnd Sub`,
        'iu',
      ).exec(source);
      if (!match) throw new Error(`missing VBA procedure: ${name}`);
      return match[0];
    };
    const searchSource = procedure(moduleSource, 'SearchProducts');
    const enterSource = procedure(moduleSource, 'HandleProductSearchEnter');
    const sheetChangeSource = procedure(workbookSource, 'Workbook_SheetChange');
    const selectionSource = procedure(workbookSource, 'Workbook_SheetSelectionChange');
    const forbiddenSearchTokens = [
      'KickQueuedAsyncDispatch',
      'Application.Goto',
      'Application.OnKey',
      'PumpExcelMessages',
    ];
    const foundSearchToken = forbiddenSearchTokens.find((token) => searchSource.includes(token));
    if (foundSearchToken) {
      throw new Error(`SearchProducts contains forbidden re-entrant token: ${foundSearchToken}`);
    }
    if (/Application\.OnKey\s+"~"\s*,/iu.test(moduleSource)) {
      throw new Error('Enter must remain native; synchronous Application.OnKey binding is forbidden');
    }
    if (/\bSearchProducts\b/iu.test(enterSource)) {
      throw new Error('HandleProductSearchEnter must queue, never call SearchProducts synchronously');
    }
    if (!/\bQueueProductSearch\b/iu.test(sheetChangeSource)
        || /\bSearchProducts\b/iu.test(sheetChangeSource)) {
      throw new Error('Workbook_SheetChange must queue search without synchronous execution');
    }
    if (/\bKickQueuedAsyncDispatch\b/iu.test(selectionSource)) {
      throw new Error('Workbook_SheetSelectionChange must not dispatch network work');
    }
  } catch (error) {
    console.error(`Search re-entrancy source gate failed: ${error.message}`);
    process.exitCode = 1;
    return;
  }
  if (options.selfTestProcessSafety) {
    try {
      const report = runProcessSafetySelfTest();
      console.log(JSON.stringify(report, null, 2));
    } catch (error) {
      console.error(`Validator process-safety self-test failed: ${error.message}`);
      process.exitCode = 1;
    }
    return;
  }
  if (options.selfTestNativeExcelTimeout) {
    try {
      const report = runNativeExcelTimeoutSelfTest();
      console.log(JSON.stringify(report, null, 2));
    } catch (error) {
      console.error(`Validator native Excel timeout self-test failed: ${error.message}`);
      process.exitCode = 1;
    }
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
  const processIdentityPath = path.join(tempDirectory, 'excel-process-identity.json');
  const stagePath = path.join(tempDirectory, 'validator-stage.json');
  let result;
  let abnormalCleanup = null;
  let abnormalCleanupError = null;
  let abnormalCleanupOutcome = null;
  let preservedRecoveryDirectory = null;
  let lastStage = null;
  try {
    // Windows PowerShell 5.1 treats a BOM-less script as the active ANSI code
    // page. A UTF-8 BOM keeps the Persian literals used by the validator exact.
    fs.writeFileSync(powershellPath, `\uFEFF${powershell}\n`, 'utf8');
    try {
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
            PATRIS_VALIDATOR_TIMEOUT_MS: String(options.timeoutMs),
            PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH: processIdentityPath,
            PATRIS_VALIDATOR_STAGE_PATH: stagePath,
          },
          maxBuffer: 16 * 1024 * 1024,
          // The user-facing deadline governs workbook validation. Keep a bounded
          // host grace so COM activation can expose and explicitly job-fence the
          // exact Excel process before Node may terminate powershell.exe.
          timeout: options.timeoutMs + VALIDATOR_HOST_STARTUP_CLEANUP_GRACE_MS,
          windowsHide: true,
        },
      );
    } catch (error) {
      // Fixed inputs normally make spawnSync return an error object rather than
      // throw. Preserve the same abnormal/unproven evidence invariant even for
      // a synchronous host-runtime exception.
      result = {
        error: error instanceof Error ? error : new Error(String(error)),
        status: null,
        stdout: '',
        stderr: '',
      };
    }
    abnormalCleanupOutcome = resolveAbnormalValidatorCleanup(
      tempDirectory,
      processIdentityPath,
      result,
    );
    abnormalCleanup = abnormalCleanupOutcome.report;
    abnormalCleanupError = abnormalCleanupOutcome.error;
  } finally {
    try {
      if (fs.existsSync(stagePath)) lastStage = JSON.parse(fs.readFileSync(stagePath, 'utf8'));
    } catch {}
    preservedRecoveryDirectory = finalizeValidatorTempDirectory(
      tempDirectory,
      abnormalCleanupOutcome,
    );
  }

  if (result.error) {
    const suffix = result.error.code === 'ETIMEDOUT'
      ? ` after ${options.timeoutMs + VALIDATOR_HOST_STARTUP_CLEANUP_GRACE_MS} ms `
        + `(${options.timeoutMs} ms validation deadline plus bounded COM startup/cleanup grace)`
      : '';
    console.error(`Excel validation could not run${suffix}: ${result.error.message}`);
    if (lastStage) console.error(`Last completed validator checkpoint: ${JSON.stringify(lastStage)}`);
    if (abnormalCleanup) {
      console.error(`Scoped Excel cleanup: ${JSON.stringify(abnormalCleanup)}`);
    }
    if (abnormalCleanupError) {
      console.error(`Scoped Excel cleanup is unproven: ${abnormalCleanupError.message}`);
    }
    if (preservedRecoveryDirectory) {
      console.error(`Recovery evidence preserved for exact-process remediation: ${preservedRecoveryDirectory}`);
    }
    process.exitCode = 2;
    return;
  }
  if (result.status !== 0) {
    console.error('Excel validation process failed.');
    if (lastStage) console.error(`Last completed validator checkpoint: ${JSON.stringify(lastStage)}`);
    if (result.stderr.trim()) console.error(result.stderr.trim());
    if (result.stdout.trim()) console.error(result.stdout.trim());
    if (abnormalCleanup) {
      console.error(`Scoped Excel cleanup: ${JSON.stringify(abnormalCleanup)}`);
    }
    if (abnormalCleanupError) {
      console.error(`Scoped Excel cleanup is unproven: ${abnormalCleanupError.message}`);
    }
    if (preservedRecoveryDirectory) {
      console.error(`Recovery evidence preserved for exact-process remediation: ${preservedRecoveryDirectory}`);
    }
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
