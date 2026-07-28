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
ELSE IF price_source_kind="sale_price_direct"
  AND price_source_currency="IRR"
  AND MOD(price_source_amount,10)=0:
  final_price = price_source_amount/10 exactly

For foreign_price and partner_price:
  ROUND(unrounded,-price_rounding_digits)
```

The generated formula uses `COUNT`, exact source-kind/currency guards, and
returns a blank when a required input is missing or invalid. CNY freight is
converted through the IRT/CNY rate; IRR freight is divided by 10. Freight is
never applied to the partner-price or direct-sale paths. The workbook applies
markup and rounds once for calculated foreign/partner sources. The explicitly
enabled direct-sale route applies neither and accepts only an exact whole-IRT
conversion. Excel is instructed to perform a full recalculation on open and
save. Formula and pricing columns only exist when the active integration
produced the current source and provenance fields.

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

There is one canonical right-to-left Persian template:

```text
docs/examples/لیست قیمت دیجیتالاجیک.xltm
```

Its `محصولات` table uses this exact user-facing contract:

```text
قیمت فروش (تومان) | وزن کالا (گرم) | سایر | محل کالا |
قیمت خرید (یوآن) | موجودی کل | کد کالا | نام کالا |
شناسه ووکامرس | دسته‌بندی
```

`سایر` contains raw compatibility text and is hidden by default. WooCommerce
ID has its own column. A product name is bold and linked only when a verified
WooCommerce permalink exists; link color distinguishes published, draft-like,
and missing products. Visible categories come only from the WooCommerce/site
record and never fall back to a Patris category code.

`داشبورد` contains formula-backed catalog and publication summaries.
`تنظیمات` contains the live site values, proposed edits, warnings, and the
guarded preview/apply controls. Product search and clear buttons live directly
on `محصولات`; selecting a product highlights its complete table row. Technical
join and audit data is stored only in the `xlSheetVeryHidden` sheet
`داده‌های همگام‌سازی`.

The template is empty at rest: it contains no product rows, prices, cached
responses, or credential material. Opening an `.xltm` creates a separate
macro-enabled workbook instance, so **Save As** writes a working copy instead
of overwriting the canonical empty template. Saving as `.xlsx` creates a
macro-free snapshot and removes the search, sync, preview/apply buttons, their
macro assignments, and the selection-highlighting controls. The logo, chart,
tables, formulas, and synchronized values remain.

Refresh-on-open and **همگام‌سازی اکنون** use only the local Patris companion:

```text
GET  http://127.0.0.1:18080/api/product-sync
POST http://127.0.0.1:18080/api/excel/pricing-sync/state
POST http://127.0.0.1:18080/api/excel/pricing-sync/preview
POST http://127.0.0.1:18080/api/excel/pricing-sync/apply
```

The `/api/excel/pricing-sync/*` path is intentionally a private Excel adapter:
it handles the loopback session, CSRF token, and workbook-specific request
validation. The WordPress endpoint and its response schemas are application
neutral (`/wp-json/digitalogic/pricing/sync/*`); they are not an Excel API.

The workbook never receives a WordPress, WooCommerce, or local product-sync
secret. Before each pricing operation it opens a short-lived loopback companion
session and sends the companion client header plus CSRF token; the local service
injects the protected server-side credential and the exact current source ID,
dataset, and revision.

`state.catalog` must be the shared `reconciled_products` projection used by both
the workbook and the Google Sheets workflow. It is the complete leaf-product
union, not a workbook-side join: `matched`, `patris_only`, `woo_only`, and
`ambiguous` are explicit row states; variable WooCommerce parent rows are
excluded. A WooCommerce-backed row uses `woo:<id>` as its technical sync key.
A Patris-only row uses `patris:<exact-product-code>`. Names are never identity
fallbacks, and an ambiguous identity blocks apply.

Every state page carries the same SHA-256 `catalog.dataset_revision`, ordered
column-key contract, reconciliation counts, source revision, page size, total,
and page count. Excel discards the complete partial result and retries the
snapshot up to three times if any one of those values changes, a page is
missing or duplicated, or the final unique-key count differs from the declared
total. It fails closed after the bounded retries instead of showing a
cross-revision catalog.

The visible table is the reconciled union of Patris and WooCommerce rows.
A WooCommerce-only row shows its WooCommerce `sku` in `کد کالا` when one
exists, otherwise that visible cell remains blank. It retains its separate
WooCommerce ID, page, category, state, and effective-price fallback, and uses
only the verified `woo:<id>` sync key in the hidden technical sheet. A visible
SKU is never treated as a Patris identity or writeback key. Where a public page
exists, the bold product name is the hyperlink and WooID remains in its own
column.

The three familiar calculator cards and table names remain:

- `Yuan_Price` / `بهای یوآن`;
- `Shipping` / `نرخ حمل هوایی (یوآن/کیلوگرم)`;
- `Profit` / `حاشیه سود`.

They are blank in the template and filled dynamically. The Settings sheet shows
the live site values separately from the workbook proposal. Yuan, USD, profit
margin, and air-express shipping are one atomic settings document. A proposal
can be previewed and applied only with the current settings and shipping-catalog
revisions, an idempotency key, a server-bound preview digest, and an explicit
Unicode confirmation. A rate older than seven days or a difference above seven
percent is reported in Persian and is never silently selected.

The price cards, hidden calculation inputs, and final-price formulas use only
the live site-confirmed values. Editing a proposal cell invalidates every older
preview, automatically opens a fresh preview plus one Persian confirmation, and
does not affect displayed customer prices until the apply/readback transaction
finishes. Product delivery gets a bounded ten-attempt retry window. On an
uncertain response, Excel keeps the unchanged live values and preserves the
same apply idempotency key for a safe retry. A success message is shown only
after a fresh state readback matches the applied revision.

The calculated price converts goods and freight independently through their
declared currencies, applies the shared profit margin, and rounds once with the
site-owned 0–9 digit `nearest_half_up` policy:

```text
ROUND(
  (foreign_price * goods_currency_to_irt
   + weight_grams / 1000 * freight_per_kg * freight_currency_to_irt)
  * (1 + profit_percent / 100),
  -rounding_digits
)
```

Missing or non-positive weight makes the foreign landed-cost route unavailable
instead of silently treating freight as free. Missing price, profit, required
exchange rate, or an unsupported/absent currency also fails closed to a blank
result. `IFERROR` guards lookup failures, so the workbooks do not expose
`#N/A` or `#VALUE!`.

After an approved settings apply, the companion invalidates its pricing cache,
regenerates the full canonical Patris contract, sends it through the existing
WooCommerce product-sync receiver, and verifies a fresh state readback before
Excel reports success. The customer-visible WooCommerce price and `قیمت فروش
(تومان)` are one invariant; the workbook never presents a second customer
price as an alternative.

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
