# Excel export

Patris Export writes `.xlsx` workbooks from the same transformed rows used by
JSON, CSV, SQL, REST, WebSocket, and update delivery. It does not add empty
pricing or shipping fields merely to complete a spreadsheet schema. Fields
that were never received stay absent; explicit null values produce blank cells.

## Language, direction, and labels

`xlsx_language = "auto"` follows `ui.language`. `en` and `fa` force English or
Persian human-readable column headings. Persian workbooks open right-to-left;
English workbooks remain left-to-right unless RTL is explicitly requested.
`column_labels` can still override individual headings. Machine keys in the
JSON, CSV, SQL, and API contracts are unchanged.

The public product identity heading is `Product Code` in English and `کد کالا`
in Persian. The Web table and its browser-generated CSV use the same localized
heading. Existing presentation settings containing the old defaults `Code` or
`کد`, or the deprecated branded labels `Patris Code` or `کد پاتریس`, are
rendered with the neutral localized heading. Other custom labels remain
authoritative. This is presentation-only: the canonical `product_code` key,
the legacy `Code` input alias, and machine-oriented HTTP/API CSV headers remain
compatible.

Structured `warehouse_stock` is expanded into deterministic numeric columns,
for example `Warehouse Stock 2` and `Warehouse Stock 10` (or their Persian
equivalents), rather than being serialized into one object cell.

## Price modes

- `precalculated` writes the canonical `final_price` value supplied by the
  shared transformation pipeline.
- `formula` writes a recalculating Excel formula in each final-price cell:

```text
IF price_source_kind="foreign_price" AND price_source_currency="CNY":
  shipping_irt = IF(shipping_price_per_kg_currency="CNY",
    weight_grams/1000*shipping_price_per_kg*irt_per_cny,
    weight_grams/1000*shipping_price_per_kg/10)
  unrounded = (price_source_amount*irt_per_cny + shipping_irt)
    * (1+markup_percent/100)
ELSE IF price_source_kind="partner_price" AND price_source_currency="IRR":
  unrounded = (price_source_amount/10)*(1+markup_percent/100)

ROUND(unrounded,-price_rounding_digits)
```

The generated formula uses `COUNT`, exact source-kind/currency guards, and
returns a blank when a required input is missing or invalid. CNY freight is
converted through the IRT/CNY rate; IRR freight is divided by 10. Freight is
never applied to the partner-price path. The workbook applies markup and rounds
once, at the configured trailing-digit amount. Excel is instructed to perform
a full recalculation on open and save. Formula and pricing columns only exist
when the active integration produced the current source and provenance fields.

## CLI

```powershell
patris-export convert C:\Patris\data4\kala.db -f xlsx -o .\exports `
  --xlsx-language fa `
  --xlsx-mode formula `
  --xlsx-zebra=true
```

Use `--xlsx-zebra=false` for plain rows.

## Configuration and environment

```toml
[ui]
language = "fa"

[export]
xlsx_language = "auto"
xlsx_mode = "precalculated"
xlsx_zebra_rows = true
```

The environment equivalents are `PATRIS_EXPORT_XLSX_LANGUAGE`,
`PATRIS_EXPORT_XLSX_MODE`, and `PATRIS_EXPORT_XLSX_ZEBRA_ROWS`.

## HTTP and Web UI

```text
GET /api/records.xlsx?download=1&language=fa&mode=formula&zebra=1
GET /api/records?format=xlsx&language=en&mode=precalculated&zebra=0&rtl=0
```

The Web UI download action sends its active language and the configured export
mode/zebra preference to this route. Query values override the config for that
one response.

## Dynamic macro templates

Two canonical right-to-left Persian templates are built from the same VBA
source:

- `docs/examples/لیست قیمت دیجیتالاجیک - استاندارد.xltm`
- `docs/examples/لیست قیمت دیجیتالاجیک - پیشرفته.xltm`

The Standard edition keeps the original eight visible columns, in the original
order:

```text
فی فروش | گرم | سایر | فی فروش2 | نرخ ارزی | همه انبارها | کد کالا | نام کالا
```

The Advanced edition keeps the same eight product facts and adds only four
normally visible operational columns: the linked WooCommerce ID, the
customer-visible WooCommerce price, its difference from the calculated price,
and the synchronization status. Its currency, profit, freight, and rate-date
audit columns are present but hidden by default. Technical join data is stored
only in the `xlSheetVeryHidden` sheet `داده‌های همگام‌سازی`.

Both templates are empty at rest: they contain no product rows, prices, cached
responses, or credential material. Opening an `.xltm` creates a separate
macro-enabled workbook instance, so **Save As** writes a working copy instead
of overwriting the canonical empty template.

Refresh-on-open and **همگام‌سازی اکنون** use only the local Patris companion:

```text
GET  http://127.0.0.1:18080/api/product-sync
POST http://127.0.0.1:18080/api/excel/pricing-sync/state
POST http://127.0.0.1:18080/api/excel/pricing-sync/preview
POST http://127.0.0.1:18080/api/excel/pricing-sync/apply
```

The workbook never receives a WordPress, WooCommerce, or local product-sync
secret. Before each pricing operation it opens a short-lived loopback companion
session and sends the companion client header plus CSRF token; the local service
injects the protected server-side credential and the exact current source ID,
dataset, and revision. The join key is the case-sensitive `product_code`
contract field, displayed to users as `کد کالا`; names are never identity
fallbacks and WooCommerce-only rows are never invented. Where a public page
exists, `WooID <id>` is the hyperlink text.

The three familiar calculator cards and table names remain:

- `Yuan_Price` / `بهای یوآن`;
- `Shipping` / `نرخ حمل CNY`;
- `Profit` / `درصد سود`.

They are blank in the template and filled dynamically. The Settings sheet shows
the live site values separately from the workbook proposal. A proposal can be
previewed and applied only with a current state revision, idempotency key,
server-bound preview digest, and an explicit Unicode confirmation. A rate older
than seven days or a currency/profit difference above seven percent is reported
in Persian and is never silently selected.

The calculated price converts goods and freight independently through their
declared currencies, applies the per-product or global percentage profit, and
rounds once to whole toman:

```text
ROUND(
  (foreign_price * goods_currency_to_irt
   + weight_grams / 1000 * freight_per_kg * freight_currency_to_irt)
  * (1 + profit_percent / 100),
  0
)
```

Missing weight makes only the freight component zero. Missing price, profit,
required exchange rate, or an unsupported/absent currency fails closed to a
blank result. `IFERROR` guards lookup failures, so the workbooks do not expose
`#N/A` or `#VALUE!`.

After an approved settings apply, the companion invalidates its pricing cache,
regenerates the full canonical Patris contract, sends it through the existing
WooCommerce product-sync receiver, and verifies a fresh state readback before
Excel reports success. The Advanced edition still shows deliberate
WooCommerce sale prices separately from the calculated canonical price.

Enable macros only after reviewing:

- `docs/examples/vba/JsonValue.cls`;
- `docs/examples/vba/JsonRuntime.bas`;
- `docs/examples/vba/ProductCatalogSync.bas`;
- `docs/examples/vba/ThisWorkbook.cls`.

The Windows builder removes local absolute paths, neutralizes Office author
metadata to `AtomicDeploy`, removes volatile core-property timestamps,
normalizes ZIP timestamps, and rejects external links/connections or private
workstation metadata. Checksums are written to one repository manifest, never
as Desktop sidecars.

The package is reopened by the Go/Excelize regression suite for macro-package
and metadata checks. Its VBA is also imported, compiled, calculated, and
integration-tested with native Excel 16. LibreOffice compatibility is not
claimed without a LibreOffice runtime in the validation environment.

![نسخه پیشرفته لیست قیمت دیجیتالاجیک](examples/لیست%20قیمت%20دیجیتالاجیک%20-%20پیشرفته%20-%20پیش‌نمایش.png)
