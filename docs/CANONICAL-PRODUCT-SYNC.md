# Canonical `kala.db` Product and Pricing Contract

`kala.db` uses the built-in `kala` dataset profile by default. The profile is
one stage inside `recordpipe.Build`; CLI exports, REST, WebSocket, webhooks,
XLSX, SQLite, and MySQL do not have separate transformation implementations.
Raw mode remains available for diagnostics.

## What the profile produces

The profile converts Patris text, preserves `Code` as a string, compacts
warehouses, and parses:

- the fourth numeric `Sharh1` slot as the foreign CNY unit price;
- grams and the remaining storage location from `Sharh2`;
- serial, unit, source sale/purchase prices, minimum stock, and selected
  warehouse stock;
- the product-specific shipping method and percentage markup;
- `landed_price` with exact decimal arithmetic and one final half-up round.

The formula is:

```text
round_once((foreign_CNY + weight_g / 1000 * shipping_price_per_kg_CNY)
  * (1 + markup_percent / 100) * IRT_per_CNY)
```

The reference fixture `24.5 CNY + 240 g at 120 CNY/kg`, with `30%` markup and
`29,000 IRT/CNY`, is `2,009,410 IRT`.

Missing or conflicting price, weight, shipping method, shipping price, markup,
or FX values omit `final_price` and add sorted machine-readable warnings. They
never become a destructive zero or a generated JSON `null`. Duplicate Codes are
quarantined from the contract.

Raw `Sharh1`, `Sharh2`, `FOROSH`, `KHARYD`, `ALLANBAR`, and `ANBAR*` keys never
cross the `patris.product-sync` boundary.

The wire boundary is sparse. A key that was never received or derived from a
real value is omitted from JSON and from the union of CSV/XLSX/SQL columns. A
JSON `null` is emitted only when the source or pricing reference explicitly
supplied `null`; it is never shorthand for missing, unavailable, invalid, or
unparseable data. Pricing, formula, and shipping-method keys are emitted only
when static pricing is explicitly populated or a Digitalogic endpoint is
configured. The only shipping names are `shipping_method_id`,
`shipping_price_per_kg_cny`, and `shipping_methods`.

This is a living integration standard. The current field set and routes are the
only supported shape; producers and consumers change together.
Unknown fields fail closed rather than selecting a compatibility branch. See
[`INTEGRATION-STANDARD.md`](INTEGRATION-STANDARD.md) for the maintenance policy.

## Offline/static pricing

Static mode is the standalone default. It works without WordPress or a network:

```json
{
  "canonical": {
    "enabled": true,
    "source_id": "patris-office",
    "profiles": {
      "kala.db": {"type": "kala"}
    },
    "pricing": {
      "mode": "static",
      "static": {
        "revision": "office-rates-2026-07-16",
        "cny_to_irt": 29000,
        "currency_effective_date": "2026-07-16",
        "selected_warehouses": ["1", "2", "6"],
        "shipping_methods": [
          {"id": "air_express", "enabled": true, "price_per_kg_cny": 120}
        ],
        "assignments": {
          "113007045": {
            "shipping_method_id": "air_express",
            "profit_percent": 30
          }
        }
      }
    }
  }
}
```

## Digitalogic provider

Digitalogic mode reads, but does not mutate:

- `GET /wp-json/digitalogic/integration/catalog` for FX, selected warehouses,
  and shipping methods;
- `POST /wp-json/digitalogic/integration/pricing-assignments/batch` for ordered
  assignment prefetches of at most 500 unique Codes;
- `GET /wp-json/digitalogic/integration/products/by-code/{code}/pricing` for an
  exact Code/SKU lookup, including an explicitly retryable per-Code batch
  result.

Canonical transform collects unique, non-quarantined Codes before its normal
per-record resolution, prefetches them in bounded request-order chunks, and
stores results in the existing bounded assignment LRU. A transform-scoped
result barrier also retains only that run's unique Codes and releases each
outcome as the row resolves, so a deliberately small `max_entries` cannot evict
current-run successes or diagnostics and recreate single-Code N+1 requests. It
does not create a second pricing client or transformation path. With the
default `batch_size: 500` and `max_entries: 2048`, a cold 1,002-Code catalog
performs exactly three batch POSTs plus one catalog GET, and performs zero
single-Code requests. Duplicate source Codes remain quarantined before
prefetch.

All chunks are staged before one atomic cache commit. Every chunk must carry
the same non-empty default-markup revision and the same schema, configured
state, source, type, and exact value; a mismatch discards every staged result.
Batch successes consume the minimal assignment projection and preserve the
exact decimal together with its `profit_percent_source`. Pricing decimals must
be quoted, canonical base-10 strings with at most 12 fractional digits;
unquoted numbers, exponent notation, redundant formatting, and over-scale
values fail closed. A `global_default` value must exactly match the shared
default snapshot.

Outbound delivery uses only the dedicated `/patris/product-sync` receiver. It
validates the current `patris.product-sync` shape, merges update envelopes,
honours `deleted_codes`, and deduplicates `event_id`. Do not point canonical
delivery at `/patris/push` or another full-replacement parser.

Credentials are read from named environment variables; secret values are not
stored in the Patris config:

```json
{
  "canonical": {
    "enabled": true,
    "profiles": {"kala.db": {"type": "kala"}},
    "pricing": {
      "mode": "digitalogic",
      "digitalogic": {
        "base_url": "https://digitalogic.ir/wp-json/digitalogic",
        "bearer_token_env": "DIGITALOGIC_PRICING_READ_TOKEN",
        "batch_assignment_path": "integration/pricing-assignments/batch",
        "batch_size": 500,
        "fresh_for": "5m",
        "max_stale": "1h",
        "timeout": "15s",
        "max_entries": 2048,
        "max_concurrency": 8,
        "max_response_bytes": 2097152
      }
    }
  }
}
```

```powershell
$env:DIGITALOGIC_PRICING_READ_TOKEN = "..."
$env:PATRIS_EXPORT_PRICING_TIMEOUT = "15s"
$env:PATRIS_EXPORT_PRICING_BATCH_SIZE = "500"
patris-export serve C:\Patris\data4\kala.db
```

`PATRIS_EXPORT_PRICING_TIMEOUT` and `PATRIS_EXPORT_PRICING_BATCH_SIZE` override
the same normalized Digitalogic provider used by file configuration. Batch
size is capped at the endpoint maximum of 500. The 15-second timeout
default leaves headroom for a production 500-Code response without changing
the bounded request count or enabling single-Code fallback.

Only HTTPS is accepted remotely; plain HTTP is limited to loopback. Provider
paths must stay relative and on the configured origin, redirects are refused,
request/response bodies, assignment LRU, and diagnostic LRU are bounded,
configured credential environment variables must be populated, and cached
values are usable only inside `max_stale`. One `bearer_token_env` supplies the
same read token to the exact integration-catalog GET and assignment-batch POST;
the batch request otherwise reuses the existing HTTP client, timeout, TLS
rules, redirect policy, and response limit. The batch endpoint is required for
canonical transforms. A missing endpoint, authentication or transport failure,
oversized response, unknown field, malformed schema, changed result order/Code,
or inconsistent result count fails closed for that freshness window. Fresh
fail-closed diagnostics are
honored by later prefetch runs, including when the per-Code diagnostic LRU is
smaller than a transform, so repeated transforms do not hammer a failing batch
route. Typed not-found and ambiguous-Code results are cached as authoritative
empty assignments, never retried through a different lookup path. Only a
per-Code server error explicitly marked `retryable: true` with a 5xx status may
use the current exact-Code endpoint. Cached stale assignments may
still be used only within `max_stale`, with explicit warnings; recent catalog
failures are also backed off for the freshness window so an outage cannot
recreate an N+1 catalog storm.
Static/standalone mode implements no prefetch capability and makes no remote
requests.

The dedicated pricing-input bearer should authorize only the catalog, batch,
and exact-Code read routes, never a write route. Do not reuse a product-sync
write secret. Patris calculates only when the schema is exactly
`digitalogic.integration-catalog`, `currency.local` is `IRT`,
`currency.cny_to_irt` is valid, and `pricing.formula_id` is exactly
`landed_price`. A schema, currency, or formula mismatch omits `final_price` and
adds explicit warnings.

## Transport contract

Canonical JSON and JSON webhooks use the current `patris.product-sync` shape.
Retired shapes and compatibility negotiation are not accepted. Category and
exclusion projections are part of the current shape without exposing raw Patris
fields.

`event_id` covers the validated `generated_at` value as well as the sorted
record hashes and tombstones. Identical content generated at a later time is a
new ordered occurrence, so a receiver can advance its watermark without
allowing an older changed event through afterward.

The server exposes this envelope at `GET /api/product-sync`. The viewer-facing
`GET /api/records` endpoint remains a Code-keyed collection of canonical
product rows; `GET /api/categories` returns the separately keyed category
hierarchy. Envelope metadata never appears as table rows. The viewer presents
both projections through one Products/Categories segmented control and reuses
the same table, filtering, column, context-menu, accessibility, and RTL logic.

![Products and categories in the shared catalog viewer](screenshots/catalog-products-categories.png)

![Canonical landed-pricing row in the records viewer](screenshots/canonical-product-sync-viewer.png)

Cross-project contract verification uses the entirely synthetic two-product
fixture at `testdata/patris-product-sync.synthetic.json`; production
exports and catalog values must never be committed as golden fixtures.

```json
{
  "schema": "patris.product-sync",
  "event_type": "snapshot",
  "event_id": "sha256:...",
  "local_currency": "IRT",
  "formula_id": "landed_price",
  "source": {
    "id": "patris-office",
    "dataset": "kala.db",
    "revision": "sha256:..."
  },
  "generated_at": "2026-07-16T08:00:00Z",
  "products": [
    {
      "product_code": "113007045",
      "category_code": "113007",
      "foreign_currency": "CNY",
      "foreign_price": 24.5,
      "weight_grams": 240,
      "shipping_method_id": "air_express",
      "shipping_price_per_kg_cny": 120,
      "markup_percent": 30,
      "irt_per_cny": 29000,
      "final_price": 2009410,
      "record_hash": "sha256:...",
      "warnings": []
    }
  ],
  "categories": [
    {
      "category_code": "113",
      "name": "Modules",
      "parent_code": "",
      "depth": 1,
      "warnings": [],
      "record_hash": "sha256:..."
    },
    {
      "category_code": "113007",
      "name": "Development modules",
      "parent_code": "113",
      "depth": 2,
      "warnings": [],
      "record_hash": "sha256:..."
    }
  ],
  "excluded_codes": ["999010"],
  "quarantined_codes": [],
  "warnings": []
}
```

Product `category_code` is the longest supplied structural prefix (the
six-digit category is preferred over its three-digit parent). Partial extracts
without hierarchy rows keep the product and use an empty category code rather
than guessing. Category hashes cover code, name, parent, depth, and warnings;
category and exclusion changes participate in source revisions and event IDs.
Exact reserved accounting/service codes are excluded before classification,
and ambiguous numeric shapes, duplicates, or signal-bearing parent conflicts
are quarantined.

Record hashes and source revisions are stable over unchanged values. Event IDs
are deterministic for one occurrence and include its validated `generated_at`
wire value. Retries therefore reuse one envelope byte-for-byte, while a later
same-content occurrence receives a new ordered identity. Incremental events
contain complete changed products and deleted-Code tombstones such as
`{"product_code":"113007045","deleted":true}`.
Quarantined duplicate Codes are never tombstones: update diffs and SQL snapshot
reconciliation preserve the last known good downstream value until ambiguity
is resolved.

WebSocket messages retain their existing `added`/`modified`/`deleted` fields
and include the same living contract under `contract`. CSV and XLSX preserve
exact decimal lexemes. XLSX writes decimals that round-trip through Excel's
numeric representation as numeric cells and keeps longer precision as text
rather than silently changing it. Code is always text. Its frozen, filtered
Records sheet uses deterministic canonical columns, and its allowlisted
Metadata sheet carries the schema/formula/source revision/generated time and
normalized warnings without credentials, local paths, or raw Patris fields.
MySQL uses exact `DECIMAL` columns; SQLite declares
canonical decimal inputs as `DECIMAL_TEXT` so its numeric affinity cannot
silently coerce them through binary floating point. Final IRT prices remain
integers. Both SQL sinks add newly introduced columns on later exports.

![Accessible canonical XLSX download action](screenshots/canonical-xlsx-export-menu.png)

For direct delivery to a receiver, use the existing HTTP update sink with
`require_contract: true`, the dedicated `/patris/product-sync` endpoint, and a
`product_sync_secret_env` reference. The secret value is read from that named
environment variable at request time and sent only in
`X-Patris-Product-Sync-Secret`; it is not copied into persisted config or
delivery logs or inherited by an optional command sink. Custom header names
ending in `product-sync-secret` are rejected so a retired header cannot retain
or transmit a persisted credential. Remote secret-bearing destinations require
HTTPS. The sink preserves the canonical `X-Patris-*`
identity headers and uses contract `source.id` instead of the local database
path for `X-Patris-Source`. It parses the WordPress REST response, surfaces
apply state, and can retry the identical event when the receiver reports
partial/pending work. See
[`docs/examples/export-transform-send.md`](examples/export-transform-send.md)
for the complete configuration and retry policy.

The generic mapping pipeline remains available for datasets without a selected
profile. The canonical contract intentionally has fixed field names. To inspect
pxlib rows use `--raw`; to use generic mapping for `kala.db`, explicitly set
`PATRIS_EXPORT_CANONICAL=0`.
