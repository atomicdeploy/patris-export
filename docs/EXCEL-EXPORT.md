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
shipping_irt = IF(shipping_price_per_kg_currency="CNY",
  weight_grams/1000*shipping_price_per_kg*irt_per_cny,
  weight_grams/1000*shipping_price_per_kg/10)
ROUND((foreign_price*irt_per_cny + shipping_irt)*(1+markup_percent/100),0)
```

The generated formula uses `COUNT` plus an exact currency-token guard and
returns a blank when any numeric input is missing/non-numeric or the shipping
currency is not uppercase `CNY` or `IRR`. CNY freight is converted through the
IRT/CNY rate; IRR freight is divided by 10. The workbook applies markup and
rounds once, at the final whole-IRT result. Excel is instructed to perform a
full recalculation on open and save. Formula and shipping/profit columns only
exist when the active integration produced the required amount/currency pair.

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

## Dynamic macro template

`docs/examples/Digitalogic-Price-Calculator.xltm` is the canonical
calculator template. It preserves the original right-to-left Persian calculator
instead of introducing a second dashboard schema. The visible `Products` table
starts at `B5` and has exactly the original columns in their original order:

```text
فی فروش | گرم | سایر | فی فروش2 | نرخ ارزی | همه انبارها | کد کالا | نام کالا
```

The template stores no product rows or cached REST responses. Opening the
`.xltm` creates a separate macro-enabled workbook instance, so a later **Save
As** operation saves a working copy and does not overwrite the canonical empty
template.

Refresh-on-open and **Sync now** fetch:

```text
http://127.0.0.1:18080/api/product-sync
https://digitalogic.ir/wp-json/wc/store/v1/products
```

The local product service returns the canonical `patris.product-sync` JSON
envelope. The public WooCommerce Store API only supplies the public product ID
and permalink. No workbook credential is required or stored. The join key is
exact, case-sensitive `product_code` to WooCommerce `sku`; product names are
never identity fallbacks and WooCommerce-only rows are never added. A matching
name cell ends with `WooID <id>` and links to the public product page.

The `تنظیمات` sheet also exposes explicit **دریافت وضعیت قیمت**,
**پیش‌نمایش تغییرات**, and **اعمال تغییرات** actions. They call only the local
loopback companion and never store a remote credential. The companion injects
the protected source-scoped credential and canonical source identity, preserves
the Digitalogic revision/idempotency/preview/confirmation contract, and
regenerates and verifies product sync after apply. See
[Excel pricing-settings companion](EXCEL-PRICING-SYNC.md).

The three familiar calculator inputs remain in their original cells and table
names:

- `Yuan_Price` / `بهای یوآن` at `M6:M7` (initial value `29500`);
- `Shipping` / `نرخ حمل` at `O6:O7` (initial value `120` CNY/kg);
- `Profit` / `درصد سود` at `O9:O10` (initial value `30%`).

They are user configuration, not product data, so a refresh never clears or
silently overwrites them with an incomplete remote assignment. Column `B`
calculates the same original price:

```text
((weight_grams * shipping_cny_per_kg / 1000) + foreign_price_cny)
  * (1 + profit_fraction)
  * irt_per_cny
```

The guarded formula returns a blank when weight or foreign price is absent,
instead of emitting `#N/A` or `#VALUE!`. The `تنظیمات` sheet remains available
for the two source URLs, refresh status, last-success timestamp, auto-refresh
choice, and read-only mirrors of the three calculator inputs. All visible labels
and messages are Persian.

Enable macros only after reviewing:

- `docs/examples/vba/JsonValue.cls`;
- `docs/examples/vba/JsonRuntime.bas`;
- `docs/examples/vba/ProductCatalogSync.bas`;
- `docs/examples/vba/ThisWorkbook.cls`.

The Windows builder removes local absolute paths, neutralizes Office author
metadata to `AtomicDeploy`, removes volatile core-property timestamps,
normalizes ZIP timestamps, and rejects external links/connections or private
workstation metadata before publishing the template and checksum.

The package is reopened by the Go/Excelize regression suite for macro-package
and metadata checks. Its VBA is also imported, compiled, calculated, and
integration-tested with native Excel 16. LibreOffice compatibility is not
claimed without a LibreOffice runtime in the validation environment.

![Digitalogic price calculator template](examples/Digitalogic-Price-Calculator-preview.png)
