# Transform, Raw, Export, and Send Update Examples

`patris-export` runs one shared record pipeline for CLI exports, the REST API,
WebSocket updates, watch-mode webhooks, and SQL/Excel outputs. `kala.db` selects
the fixed `kala` canonical contract by default; the mapping examples below
apply to other datasets, or to `kala.db` after explicitly setting
`PATRIS_EXPORT_CANONICAL=0`. See [the canonical contract](../CANONICAL-PRODUCT-SYNC.md).

## Raw Mode

Raw mode is for debugging and comparison. It returns the rows as pxlib reads them
from the Paradox table. Character conversion, RTL conversion, ANBAR compaction,
Code-keyed JSON shaping, and transform mapping are intentionally disabled.

```powershell
patris-export --raw convert C:\Patris\data4\kala.db --format json --output raw-dumps
patris-export --raw serve C:\Patris\data4\kala.db --addr 127.0.0.1:18080
```

## Key and Value Mapping

Mapping files are JSON. Global rules apply to every source; table rules override
them by basename, so `kala.db` can have different field names, value maps, and
numeric transforms from another table.

```json
{
  "enabled": true,
  "key_field": "sku",
  "tables": {
    "kala.db": {
      "key_field": "sku",
      "fields": {
        "Code": "sku",
        "Name": "title",
        "FOROSH": "price"
      },
      "numeric": {
        "FOROSH": {
          "multiplier": 1.09,
          "round": 0
        }
      },
      "defaults": {
        "source": "patris"
      }
    }
  }
}
```

```powershell
patris-export --mapping .\mapping.kala.json convert C:\Patris\data4\kala.db -f json -o exports
patris-export --mapping .\mapping.kala.json serve C:\Patris\data4\kala.db --addr :18080
```

Environment equivalent:

```powershell
$env:PATRIS_EXPORT_MAPPING_FILE = "C:\Patris\mapping.kala.json"
$env:PATRIS_EXPORT_RAW = "0"
```

## Excel, SQLite, and MySQL

```powershell
patris-export convert C:\Patris\data4\kala.db -f xlsx -o exports `
  --xlsx-language fa `
  --xlsx-mode formula `
  --xlsx-zebra=true

patris-export --mapping .\mapping.kala.json convert C:\Patris\data4\kala.db `
  -f sqlite `
  --sqlite-path .\exports\patris-products.sqlite `
  --sqlite-table products `
  --batch-size 250

# Keep credentials in the process secret environment, not a committed config.
# The timeout parameters bound connection and I/O waits; tls=true verifies the
# server with the system trust store.
$env:PATRIS_EXPORT_MYSQL_DSN = "user:password@tcp(db.example:3306)/shop?parseTime=true&timeout=5s&readTimeout=30s&writeTimeout=30s&tls=true"
patris-export --mapping .\mapping.kala.json convert C:\Patris\data4\kala.db `
  -f mysql `
  --mysql-table products `
  --batch-size 250
```

For a private certificate authority, keep the CA path and optional certificate
server name in protected server configuration (or environment variables), not
browser-managed configuration:

```yaml
export:
  mysql_tls_ca_file: C:/Patris/certificates/database-ca.pem
  mysql_tls_server_name: mysql.service.internal
  mysql_connect_timeout: 10s
```

The equivalent environment variables are
`PATRIS_EXPORT_MYSQL_TLS_CA_FILE`,
`PATRIS_EXPORT_MYSQL_TLS_SERVER_NAME`, and
`PATRIS_EXPORT_MYSQL_CONNECT_TIMEOUT`. A CA file is read with a 4 MiB bound,
added to the operating-system trust roots, and forces verified TLS 1.2 or
newer. It overrides `tls=preferred`, `tls=skip-verify`, or plaintext fallback
from the DSN. Do not use an insecure TLS mode in production. Browser config,
WebSocket snapshots, config broadcasts, and browser saves omit and preserve the
DSN, CA path, and server-name override.

`--xlsx-language auto` follows `ui.language`; `--xlsx-mode precalculated`
writes the transformed final values, while `formula` emits auditable Excel
formulas that remain blank until every required input is numeric. Warehouse
stock is exported as one column per available warehouse. See
[Excel export](../EXCEL-EXPORT.md) for config, environment, and HTTP examples.

SQLite and MySQL exports create the destination table when needed, add new
columns on later exports, preserve canonical numeric types, and upsert by the
configured key field. `batch_size` is the maximum number of rows in each
prepared multi-row statement (and is reduced only when required by the
driver's parameter limit).

SQL reconciliation is explicit and non-destructive by default:

```yaml
export:
  batch_size: 250
  reconciliation: upsert_only
  dry_run: false
```

`upsert_only` inserts and updates supplied Codes but preserves destination rows
that are absent from the current input. Use `delete_missing` only for a known
complete authoritative snapshot. Canonical quarantine/protected Codes remain
preserved even in `delete_missing` mode.

Preview the exact operation counts without changing the destination:

```powershell
patris-export convert C:\Patris\data4\kala.db -f sqlite `
  --sqlite-path .\exports\patris-products.sqlite `
  --sqlite-table products `
  --reconciliation delete_missing `
  --dry-run
```

The result line reports `inserted`, `updated`, `unchanged`, `deleted`, `failed`,
and `elapsed` milliseconds; it never includes the DSN. A dry run against a
missing SQLite path does not create the file or its parent directory.

MySQL/MariaDB connections use a finite 10-second connection bound by default
(configurable from 100 ms through 2 minutes) in addition to any DSN I/O
timeouts and caller cancellation. Connection, TLS, authentication, permission,
constraint, timeout, cancellation, transient, unavailable, schema, and unknown
failures are converted to typed secret-safe diagnostics. Raw driver messages
are retained only as an in-process wrapped cause and are never serialized or
printed by the SQL target API. Classification does not automatically replay a
whole transaction: a lost connection during commit can have an ambiguous
outcome and must be reconciled before retrying.

The shared `recordsink.ProbeSQLTarget` operation is non-mutating: it performs a
bounded ping plus `SELECT VERSION()` and a best-effort session TLS-status read.
Its result contains only connection state, driver/vendor, a bounded printable
version, TLS state, and elapsed time. It is the backend primitive for the
authenticated connection-test UI follow-up.

Watch mode uses this same `recordpipe` -> `recordsink` route for its initial
write and every debounced update; there is no separate live SQL implementation:

```powershell
patris-export convert C:\Patris\data4\kala.db -f sqlite -w `
  --sqlite-path .\exports\patris-products.sqlite `
  --sqlite-table products
```

Reproduce the SQLite initial-write/update proof locally, and optionally run the
same proof against a disposable MariaDB/MySQL database:

```powershell
go test ./pkg/recordsink -run TestSQLiteSyncInitialUpdateDryRunAndProtectedDelete -v

$env:PATRIS_EXPORT_TEST_MYSQL_DSN = "test_user:test_password@tcp(127.0.0.1:3306)/patris_test?parseTime=true&timeout=5s&readTimeout=30s&writeTimeout=30s"
go test ./pkg/recordsink -run TestMariaDBSyncInitialWriteAndUpdate -v
Remove-Item Env:PATRIS_EXPORT_TEST_MYSQL_DSN
```

The MariaDB test is skipped unless its dedicated test DSN is present and drops
only the uniquely named table it creates. Authenticated browser connection-test
and manual-sync controls, `soft_delete_missing`, and the broader database CI
matrix remain separate follow-up work.

## Watch and Send Updates

When watch mode is enabled, the initial payload can be sent once and later file
changes can be sent as either a changeset or a full snapshot. Payloads can go to
an HTTP endpoint, a command on stdin, or both. The initial event contains the
complete transformed snapshot. Later `changes` events use the same key-aware
diff implementation as the WebSocket server and contain `added`, `modified`,
and `deleted` entries. Modified entries include both the changed field values
and the complete transformed `record`, so downstream upserts do not need to
reconstruct unchanged fields.

For a canonical profile and JSON delivery, the webhook body is the direct,
current `patris.product-sync` envelope instead of this generic wrapper.
It carries deterministic event identity, complete changed products, and
deleted-Code tombstones. CSV delivery retains the generic tabular change form.

The existing HTTP sink serves webhook and REST destinations, including HTTP
adapters that accept the direct JSON envelope; there is no second
receiver-specific or JSON-RPC delivery implementation. Point it at the
configured receiver's product-sync route:

```text
POST https://receiver.example/wp-json/receiver/patris/product-sync
```

For complete native REST, JSON-RPC adapter, WordPress AJAX adapter, gRPC
gateway, authentication, retry, and local mock-receiver recipes, see
[Remote Update Delivery](../REMOTE-API-EXAMPLES.md).

The dedicated `X-Patris-Product-Sync-Secret` value is resolved only at
request time from the environment variable named by
`product_sync_secret_env`. Inject that environment variable through the
service/process secret manager. Never put the value in the config `headers`
map, URL/query string, command line, or repository. A configured but missing or
empty secret fails closed before any request is made. Product-sync URLs with
userinfo or query parameters are rejected, and redirects are not followed when
the secret header is active. Remote destinations must use HTTPS; plain HTTP is
accepted only for loopback test/development hosts. If the optional command sink
is also enabled, the HTTP receiver secret is removed from that child process's
environment.

Patris writes `X-Patris-Event`, `X-Patris-Source`, `X-Patris-Contract`, and
`X-Patris-Event-ID` after custom headers, so configuration cannot replace the
canonical body identity. The receiver checks the contract and event-ID headers
against the body. For canonical events,
`X-Patris-Source` is the public contract `source.id`; the local database path
is never sent in that header. Generic non-contract webhooks retain their
configured event source.

Raw-mode outbound delivery fails closed by default. `send_updates.allow_raw`
must be explicitly enabled for a trusted, non-integration destination; never
enable it for a product-sync consumer or others.

```powershell
patris-export convert C:\Patris\data4\kala.db -f json -w `
  --send-url https://receiver.example/wp-json/receiver/patris/product-sync `
  --send-format json `
  --send-mode changes `
  --send-product-sync-secret-env PATRIS_PRODUCT_SYNC_SECRET `
  --send-retry-attempts 3 `
  --send-retry-backoff 2s
```

```powershell
patris-export convert C:\Patris\data4\kala.db -f csv -w `
  --send-command "powershell -NoProfile -File .\scripts\consume-patris-update.ps1" `
  --send-mode full
```

Config equivalent:

```json
{
  "send_updates": {
    "enabled": true,
    "url": "https://receiver.example/wp-json/receiver/patris/product-sync",
    "method": "POST",
    "format": "json",
    "mode": "changes",
    "require_contract": true,
    "initial": true,
    "timeout": "10s",
    "retry_attempts": 3,
    "retry_backoff": "2s",
    "product_sync_secret_env": "PATRIS_PRODUCT_SYNC_SECRET",
    "headers": {
      "X-Integration-Name": "patris-office"
    }
  }
}
```

Set `require_contract` for every product-sync integration target.
It rejects generic transformed rows as well as raw rows, and it also rejects
CSV, so a missing profile or disabled canonical stage cannot leak legacy Patris
fields through a seemingly non-raw update.

The default is one HTTP attempt, preserving generic webhook behavior. Opt in
to retries only for an idempotent receiver. Product-sync retries are safe:
Patris encodes the envelope once and reuses the exact bytes, event ID, identity
headers, and secret for every attempt. Network failures, HTTP 408/425/429 and
selected 5xx transients, and receiver responses with `retryable: true`,
`partially_applied`, or `retry_pending` are retried. The receiver's
`accepted`, `already_current`, `replayed`, or `recovered` state is surfaced in
the CLI/server log. Those terminal states also report a bounded, non-negative
`deferred_products` count for Codes that require later catalog
reconciliation (for example, a product that does not exist in WooCommerce or
an exact Code/SKU collision that must not be guessed). Deferred reconciliation
is durable receiver work, not a delivery failure: it is logged as a count only
and does not cause another HTTP attempt. Current successful responses must
include `status`, `event_id`, `retryable`, `pending_products`, and
`deferred_products`; omission and explicit `null` both fail closed rather than
being converted to empty strings, `false`, or zero.

The receiver may include a sibling `deferred_reconciliation` summary containing
native integer `missing`, `ambiguous`, and `details_truncated` counts plus at
most 100 detail objects. Patris validates that missing plus ambiguous, and the
detail count plus truncated count, both equal `deferred_products`; it then
discards the detail objects. The summary is optional in the current standard,
and neither its product Codes nor reason details enter logs or delivery errors.

Only `pending_products` represents transient apply work and causes a retry.
A mixed response may carry both counts, but it remains partial only while the
pending count is positive. Once pending work recovers, the receiver returns a
terminal state with `retryable: false` and `pending_products: 0`, even if a
nonzero deferred count remains. Exhaustion reports only the sanitized endpoint
and structured status/attempt/pending/deferred counts; query strings, response
bodies, product identities, request headers, and credential values are never
included. This distinction is paired with bounded reconciliation state in each
receiver.

Typed receiver responses fail closed unless their state is internally
consistent: terminal states require zero pending products and
`retryable: false`; partial/pending states require a positive pending count and
`retryable: true`. The deferred count must be an integer from zero through
2,147,483,647; negative, fractional, null, overflowing, or otherwise malformed
counts fail closed. Unknown states and a mismatched or missing event ID are not
treated as delivery success.

Equivalent environment-only configuration uses
`PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV`,
`PATRIS_EXPORT_SEND_RETRY_ATTEMPTS`, and
`PATRIS_EXPORT_SEND_RETRY_BACKOFF`. The first contains an environment-variable
*name*, not the secret itself.

In CSV `changes` mode, each row includes `_change_type` with `added`,
`modified`, or `deleted`. Modified rows contain the complete transformed row
and `_changed_fields`; deleted rows are tombstones containing the configured
key field. This makes the CSV stream actionable without exposing raw Patris
fields when transform mapping is enabled.
