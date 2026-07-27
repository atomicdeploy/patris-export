# Excel pricing-settings companion

Patris Export is the credential boundary between the canonical Persian
`لیست قیمت دیجیتالاجیک.xltm` workbook and Digitalogic's guarded pricing-settings
API. The workbook never contains a WordPress, WooCommerce, or product-sync
credential.

## Local loopback contract

All routes are `POST` and accept JSON only:

```text
/api/excel/pricing-sync/session
/api/excel/pricing-sync/state
/api/excel/pricing-sync/preview
/api/excel/pricing-sync/apply
```

The session route accepts `{}` and returns:

```json
{
  "schema": "patris.excel-pricing-companion-session/v1",
  "csrf_token": "opaque-43-character-value",
  "expires_at": "2026-07-26T18:10:00Z"
}
```

Every call sends `X-Patris-Excel-Client:
digitalogic-price-calculator/v1`. State, preview, and apply also send the
short-lived token in `X-Patris-Excel-CSRF-Token`. The token is stored only as a
SHA-256 hash in memory, expires after ten minutes, and is never a remote
credential.

The local surface accepts only a direct loopback peer and loopback request
host. Proxy-forwarding markers and cross-origin requests are rejected.
Same-origin browser requests must provide their exact origin. No CORS
allowance or `OPTIONS` route is emitted. Requests are bounded to 64 KiB and
responses to 4 MiB.

## Local request schema

The workbook uses the local adapter schema
`patris.excel-pricing-companion-request/v1`. The adapter validates it and maps
it to Digitalogic's universal `digitalogic.pricing-sync-request/v1` contract,
adding only the protected credential and verified current Patris catalog
identity. State uses:

```json
{
  "schema": "patris.excel-pricing-companion-request/v1",
  "schema_version": 1,
  "operation": "state",
  "client_id": "digitalogic-price-calculator",
  "channel": "excel-workbook",
  "request_id": "excel-state-20260727-0001",
  "source": {
    "id": "patris-office",
    "dataset": "kala.db",
    "revision": "sha256:..."
  },
  "page": 1,
  "limit": 250,
  "locale": "fa"
}
```

Preview submits one complete settings document. The `Idempotency-Key` header
must equal the body value and `If-Match` must contain the quoted body revision.

```json
{
  "schema": "patris.excel-pricing-companion-request/v1",
  "schema_version": 1,
  "operation": "preview",
  "client_id": "digitalogic-price-calculator",
  "channel": "excel-workbook",
  "request_id": "excel-preview-20260727-0001",
  "idempotency_key": "excel-preview-20260727-0001",
  "expected_state_revision": "sha256:...",
  "settings": {
    "dollar_price": 200000,
    "yuan_price": 30000,
    "effective_date": "2026-07-27",
    "usd_effective_date": "2026-07-27",
    "cny_effective_date": "2026-07-27",
    "profit_margin_percent": 30,
    "air_express_price_per_kg": 120,
    "air_express_currency": "CNY",
    "shipping_catalog_revision": "sha256:..."
  },
  "product_changes": []
}
```

Apply repeats the exact settings and revision from preview and adds the bound
digest plus explicit confirmation:

```json
{
  "schema": "patris.excel-pricing-companion-request/v1",
  "schema_version": 1,
  "operation": "apply",
  "client_id": "digitalogic-price-calculator",
  "channel": "excel-workbook",
  "request_id": "excel-apply-20260727-0001",
  "idempotency_key": "excel-apply-20260727-0001",
  "expected_state_revision": "sha256:...",
  "settings": {
    "dollar_price": 200000,
    "yuan_price": 30000,
    "effective_date": "2026-07-27",
    "usd_effective_date": "2026-07-27",
    "cny_effective_date": "2026-07-27",
    "profit_margin_percent": 30,
    "air_express_price_per_kg": 120,
    "air_express_currency": "CNY",
    "shipping_catalog_revision": "sha256:..."
  },
  "product_changes": [],
  "preview_digest": "sha256:...",
  "confirmation": "APPLY"
}
```

Product-level changes are deliberately unsupported on this global-settings
surface. State paging is limited to 250 rows. Rates, date, profit margin,
shipping amount/currency/catalog revision, state revision, idempotency, and
confirmation are validated before network access. All pricing settings are one
atomic document; shipping is never applied separately.

## Protected remote boundary

The companion uses the existing `send_updates.url` only to derive the exact
same-origin WordPress routes:

```text
/wp-json/digitalogic/pricing/sync/state
/wp-json/digitalogic/pricing/sync/preview
/wp-json/digitalogic/pricing/sync/apply
```

It reads the credential named by `send_updates.product_sync_secret_env` and
injects it as `X-Patris-Product-Sync-Secret`. For read-only state paging, the
workbook forwards the source identity it just fetched from the local
`/api/product-sync` contract; the companion accepts it only when its ID and
dataset exactly match the configured local canonical source. This avoids
rebuilding the full catalog for every state page. Preview and apply never
accept a workbook-supplied source: the companion materializes a fresh canonical
`patris.product-sync` contract and injects its exact `{id,dataset,revision}`.
The workbook cannot provide or override the remote credential.

The destination must be HTTPS except for loopback development, may not contain
user information, a query, or a fragment, and must already be a WordPress REST
path. Redirects are not followed. Timeouts are bounded between one and thirty
seconds. Remote credentials, response bodies, target URLs, and transport errors
are not logged or copied into local errors.

Successful responses preserve Digitalogic's schemas:

- `digitalogic.pricing-sync-state/v1`
- `digitalogic.pricing-sync-preview/v1`
- `digitalogic.pricing-sync-apply/v1`

For state paging, `state.catalog.dataset` is
`reconciled_products`. The catalog envelope carries one stable
`dataset_revision`, a per-page `page_revision`, explicit reconciliation
counts, and rows from the complete
Patris/WooCommerce leaf-product union. Its row identity and status are shared
with Google Sheets:

- `woo:<id>` for every WooCommerce-backed leaf row;
- `patris:<exact-product-code>` for a Patris-only row;
- `matched`, `patris_only`, `woo_only`, or `ambiguous` as
  `reconciliation_status`.

The workbook consumes this projection directly and does not repeat the join.
Only global settings are writable from this template. An ambiguous catalog
identity, an invalid per-page revision, or reconciliation counts that disagree
with the returned rows is a hard failure. Across pages it also requires one
unchanged dataset revision, source revision, ordered column-key list, total,
page size, page count, and count document. A mismatch discards all fetched
pages and retries the full snapshot at most three times; no partial catalog is
imported.

After a successful apply, Patris invalidates its pricing-catalog cache,
regenerates the canonical product contract, synchronously sends that contract
through the existing protected product-sync delivery path, and refetches
pricing state with the new source revision. Apply succeeds locally only when
the product-sync receiver reports a terminal accepted/current/replayed/recovered
result with the exact event ID and zero pending or deferred products, and the
final state revision exactly matches Digitalogic's apply readback.

## Configuration

No new secret is introduced:

```toml
[send_updates]
enabled = true
url = "https://digitalogic.ir/wp-json/digitalogic/patris/product-sync"
method = "POST"
format = "json"
mode = "full"
require_contract = true
product_sync_secret_env = "PATRIS_PRODUCT_SYNC_SECRET"
timeout = "10s"
```

Set the named environment variable in the protected Patris runtime
environment. Never write its value into TOML/JSON/YAML, VBA, workbook cells,
URLs, logs, or command arguments.

## Workbook operator flow

On the Persian `تنظیمات` sheet:

1. Select **همگام‌سازی اکنون** on `محصولات`, or use refresh-on-open.
2. Review or edit the highlighted yuan/dollar rates, effective date, profit
   margin, or air-express shipping price. Editing one of those proposal cells
   immediately creates a fresh preview and opens the explicit Persian apply
   confirmation.
3. Review every warning in that confirmation and approve it only when the
   proposed document is correct. The separate preview/apply buttons remain
   available as a manual fallback.

Changing any setting or refreshing state after preview invalidates the local
apply guard. The remote service independently enforces the exact revision,
idempotency key, preview digest, settings document, source identity, and
`APPLY` confirmation.

Customer-facing calculated prices always use the last site-confirmed live
state, not uncommitted proposal cells. Product delivery gets a bounded
ten-attempt retry window. If apply or post-apply verification is uncertain,
the workbook retains its confirmed live values and the same still-current
preview reuses its apply idempotency key. Excel reports success only after a
fresh state readback matches the applied revision.

Terminal deferred rows whose exact reason is `missing` are allowed because
they have no WooCommerce page to disagree with. Pending rows, ambiguous
matches, receiver retries, identity mismatches, and incomplete readback remain
hard failures.

![Persian Excel pricing state, preview, and apply controls](examples/Digitalogic-Price-Calculator-settings.png)
