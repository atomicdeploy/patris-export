# Excel pricing-settings companion

Patris Export is the credential boundary between the canonical Persian
`لیست قیمت دیجیتالاجیک.xltm` workbook and Digitalogic's guarded pricing-settings
API. The workbook never contains a WordPress, WooCommerce, or product-sync
credential.

## Local loopback contract

All routes are `POST` and accept JSON only:

```text
/api/pricing-sync/session
/api/pricing-sync/state
/api/pricing-sync/preview
/api/pricing-sync/apply
```

These four generalized routes are the complete local pricing surface. No
client-specific alias prefix is registered; unknown prefixes return `404`.

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

One session/CSRF token and one revision-pinned state snapshot are reused across
the bounded state pages. A pristine template schedules refresh-on-open after a
short delay so every normal open becomes populated without blocking cell,
keyboard, or Esc input. The visible sync button remains available for an
explicit reload. Network waits are asynchronous and pump the Excel message loop. The workbook
records separate phase timings for the
session, local contract fetch, total product/site state fetch, every state page,
reconcile, pricing computation, batch table write, hyperlink/formatting, Excel
calculation, and save. Existing progress messages, request timeouts, and
no-hard-failure refresh behavior remain in force.

The observed pre-change baseline was 1,092 rows over five state pages with
about 110 seconds in server fetch. The state route now validates the workbook's
already-fetched canonical source identity without rebuilding the canonical
catalog for each page; only the required five paged receiver calls remain.
Rows are accumulated once in the cached in-memory snapshot, then written to
the Products and SyncData tables as arrays before role-level formatting. The
per-page and total timers make the next controlled run comparable without
claiming a production improvement before deployment.

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
path. Redirects are not followed. Each remote request uses the configured
timeout bounded between one second and two minutes. The complete apply
operation has an eight-minute server budget, and Excel waits up to ten minutes,
so a full catalog delivery can finish its retry and final state readback after
an upstream gateway timeout. Remote credentials, response bodies, target URLs,
and transport errors are not logged or copied into local errors.

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
page size, page count, and count document. A recognized cross-page snapshot
drift discards all fetched pages and retries the full snapshot once; deterministic
transport, schema, and integrity failures are not retried. No partial catalog is
imported. Every supplied submitted, current, and reconciliation-source revision
must agree with the local Patris contract; the legacy source revision remains a
compatibility fallback when the newer current/reconciliation fields are absent.
Any backend `product_type_cache_drift*` or `projection_integrity*` warning in
state/catalog metadata aborts the entire import before product rows are used.
The bounded recursive warning scan covers metadata objects and arrays, including
`integrity.warnings[].code`, while deliberately excluding the catalog's
per-product `rows` payload. Live settings, proposal baselines, and reconciliation
counts are snapshotted before the request and restored if any page, revision,
pagination, or integrity check rejects the snapshot.

After a successful apply, Patris invalidates its pricing-catalog cache,
regenerates the canonical product contract, synchronously sends that contract
through the existing protected product-sync delivery path, and refetches
pricing state with the new source revision. Apply succeeds locally only when
the product-sync receiver reports a terminal accepted/current/replayed/recovered
result with the exact event ID, zero pending or ambiguous products, and every
terminal deferred product classified as missing in WooCommerce. The final
state revision must exactly match Digitalogic's apply readback.

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

Patris sends only the canonical transformed `patris.product-sync` envelope.
Creation/publication/backfill policy is not transport data and no policy header
is emitted. The authenticated WordPress receiver owns its local fail-closed
gates, durable run/item ledger, bounded asynchronous worker, idempotency,
retry, compensation, rollback, and backfill behavior. The bridge and workbook
do not invent, transmit, or apply receiver publication policy.

## Workbook operator flow

On the Persian `تنظیمات` sheet:

1. Open the template and let its deferred refresh populate `محصولات`, or
   select **همگام‌سازی اکنون** for an explicit reload.
2. Review or edit the highlighted yuan/dollar rates, effective date, profit
   margin, or air-express shipping price. Editing one of those proposal cells
   invalidates the previous preview without starting network or mutation work.
   Select **پیش‌نمایش تغییرات** when the proposal is ready.
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
