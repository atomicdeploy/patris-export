# Recent-sales aggregate API

`GET /api/recent-sales` is an authenticated, read-only enrichment feed for
product-level sales recency and frequency. It never returns source rows or
order/customer fields.

## Authentication

The endpoint requires an `Authorization: Bearer ...` header. The secret is read
only from the environment variable named by `recent_sales.token_env`; it is not
stored in the config file or returned by any API. The default variable name is
`PATRIS_EXPORT_RECENT_SALES_TOKEN`.

The complete recent-sales source/auth profile is server-side configuration. It
is redacted from browser/WebSocket config payloads and preserved if the browser
saves unrelated UI settings.

If the feature is enabled but the configured variable is missing or shorter
than 16 characters, the endpoint fails closed with HTTP 503. Missing or invalid
credentials return HTTP 401. Query-string credentials are not supported.

## Supported source profile

The source must be a separate local `.json` or Paradox `.db` file supported by
the existing datasource abstraction. The primary product database and every
file named `kala.db` are explicitly refused. Remote URLs are also refused so
the service can hash and verify one stable local snapshot before and after the
read.

No sales database schema is assumed. Configure the exact field names:

```json
{
  "recent_sales": {
    "enabled": true,
    "source": "C:/Patris/integration/sales-events.json",
    "source_id": "office-sales",
    "token_env": "PATRIS_EXPORT_RECENT_SALES_TOKEN",
    "product_code_field": "product_code",
    "quantity_field": "quantity",
    "sold_at_field": "sold_at",
    "event_id_field": "sale_event_id",
    "max_window": "2160h",
    "max_page_size": 500,
    "max_source_rows": 1000000,
    "max_source_mb": 256
  }
}
```

The equivalent operational environment overrides are:

```text
PATRIS_EXPORT_RECENT_SALES_ENABLED=true
PATRIS_EXPORT_RECENT_SALES_SOURCE=C:\Patris\integration\sales-events.json
PATRIS_EXPORT_RECENT_SALES_SOURCE_ID=office-sales
PATRIS_EXPORT_RECENT_SALES_TOKEN_ENV=PATRIS_EXPORT_RECENT_SALES_TOKEN
PATRIS_EXPORT_RECENT_SALES_PRODUCT_CODE_FIELD=product_code
PATRIS_EXPORT_RECENT_SALES_QUANTITY_FIELD=quantity
PATRIS_EXPORT_RECENT_SALES_SOLD_AT_FIELD=sold_at
PATRIS_EXPORT_RECENT_SALES_EVENT_ID_FIELD=sale_event_id
PATRIS_EXPORT_RECENT_SALES_MAX_WINDOW=2160h
PATRIS_EXPORT_RECENT_SALES_MAX_PAGE_SIZE=500
PATRIS_EXPORT_RECENT_SALES_MAX_SOURCE_ROWS=1000000
PATRIS_EXPORT_RECENT_SALES_MAX_SOURCE_MB=256
```

Each source row must contain:

- an exact non-empty textual product code (JSON sources must quote it, which
  preserves leading zeroes);
- a positive finite sold quantity;
- an RFC3339 sale timestamp;
- a stable event/line identifier used only for deduplication.

Identical duplicate event identifiers count once. Conflicting duplicates fail
the whole request closed. Other source fields are ignored and cannot cross the
response boundary. Prefer a pre-sanitized integration source even though the
response projector also uses a closed allowlist.

## Request

Both time bounds are required. The window is inclusive at `from` and exclusive
at `to`, then normalized to UTC:

```powershell
$headers = @{ Authorization = "Bearer $env:PATRIS_EXPORT_RECENT_SALES_TOKEN" }
Invoke-RestMethod `
  -Headers $headers `
  -Uri 'http://127.0.0.1:18080/api/recent-sales?from=2026-07-01T00%3A00%3A00Z&to=2026-07-08T00%3A00%3A00Z&page=1&page_size=100'
```

Allowed query parameters are only:

- `from`: required RFC3339 timestamp;
- `to`: required RFC3339 timestamp;
- `page`: optional positive integer, default `1`, maximum `1000000`;
- `page_size`: optional positive integer, default `100`, maximum `500` or the
  lower configured bound.

The default maximum window is 90 days. Configuration can reduce it or raise it
only as far as 365 days. Source ingestion is independently bounded by row count
and file size; defaults are 1,000,000 rows and 256 MiB, and the hard file-size
ceiling is 1 GiB.

## Response

The media type is
`application/vnd.patris.recent-sales-aggregate+json`.
Product aggregates are byte-sorted by `product_code` before paging:

```json
{
  "schema": "patris.recent-sales-aggregate",
  "version": 1,
  "source": {
    "id": "office-sales",
    "dataset": "sales-events.json",
    "revision": "sha256:..."
  },
  "window": {
    "from": "2026-07-01T00:00:00Z",
    "to": "2026-07-08T00:00:00Z"
  },
  "page": {
    "number": 1,
    "size": 100,
    "total_items": 2,
    "total_pages": 1
  },
  "sales": [
    {
      "product_code": "113006048",
      "sold_quantity": 4,
      "sale_frequency": 3,
      "last_sold_at": "2026-07-07T10:20:00Z"
    }
  ]
}
```

The `sales` object has exactly four fields. Customer identity/contact/address,
invoice or payment details, discounts, destinations, source event identifiers,
and all other order-level data are excluded. `sale_frequency` is the count of
unique configured event/line identifiers for that product inside the window.

`testdata/recent-sales-config.synthetic.json` and
`testdata/recent-sales-events.synthetic.json` provide a credential-free,
non-production profile for isolated build/API probes. Supply the bearer value
only through `PATRIS_EXPORT_RECENT_SALES_TOKEN`.

## Current source limitation

The application does not discover or infer a live sales table. An operator must
configure a separately supported source and its exact field mapping. This is
intentional: guessing a schema or deriving sales from `kala.db` would violate
the access and product-identity boundary.
