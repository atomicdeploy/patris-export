# Transform, Raw, Export, and Send Update Examples

`patris-export` runs one shared record pipeline for CLI exports, the REST API,
WebSocket updates, watch-mode webhooks, and SQL/Excel outputs. `kala.db` selects
the fixed `kala_v1` canonical contract by default; the mapping examples below
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
patris-export convert C:\Patris\data4\kala.db -f xlsx -o exports

patris-export --mapping .\mapping.kala.json convert C:\Patris\data4\kala.db `
  -f sqlite `
  --sqlite-path .\exports\patris-products.sqlite `
  --sqlite-table products

$env:PATRIS_EXPORT_MYSQL_DSN = "user:password@tcp(127.0.0.1:3306)/shop?parseTime=true"
patris-export --mapping .\mapping.kala.json convert C:\Patris\data4\kala.db `
  -f mysql `
  --mysql-table products
```

SQLite and MySQL exports create the destination table when needed, add new
columns on later exports, preserve canonical numeric types, and upsert by the
configured key field.

## Watch and Send Updates

When watch mode is enabled, the initial payload can be sent once and later file
changes can be sent as either a changeset or a full snapshot. Payloads can go to
an HTTP endpoint, a command on stdin, or both. The initial event contains the
complete transformed snapshot. Later `changes` events use the same key-aware
diff implementation as the WebSocket server and contain `added`, `modified`,
and `deleted` entries. Modified entries include both the changed field values
and the complete transformed `record`, so downstream upserts do not need to
reconstruct unchanged fields.

For a canonical profile and JSON delivery, the webhook body is the direct
versioned `digitalogic.product-sync` envelope instead of this generic wrapper.
It carries deterministic event identity, complete changed products, and
deleted-Code tombstones. CSV delivery retains the generic tabular change form.

The existing HTTP sink serves webhook and REST destinations, including HTTP
adapters that accept the direct JSON envelope; there is no second Digitalogic
or JSON-RPC delivery implementation. For the Digitalogic v1 receiver, point it
at:

```text
POST https://digitalogic.example/wp-json/digitalogic/v1/patris/product-sync
```

The dedicated `X-Digitalogic-Product-Sync-Secret` value is resolved only at
request time from the environment variable named by
`product_sync_secret_env`. Inject that environment variable through the
service/process secret manager. Never put the value in the config `headers`
map, URL/query string, command line, or repository. A configured but missing or
empty secret fails closed before any request is made. Product-sync URLs with
userinfo or query parameters are rejected, and redirects are not followed when
the secret header is active.

Patris writes `X-Patris-Event`, `X-Patris-Source`, `X-Patris-Contract`,
`X-Patris-Contract-Version`, and `X-Patris-Event-ID` after custom headers, so
configuration cannot replace the canonical body identity. The receiver checks
the contract/version/event ID headers against the body.

Raw-mode outbound delivery fails closed by default. `send_updates.allow_raw`
must be explicitly enabled for a trusted, non-integration destination; never
enable it for Digitalogic or another product-sync consumer.

```powershell
patris-export convert C:\Patris\data4\kala.db -f json -w `
  --send-url https://digitalogic.example/wp-json/digitalogic/v1/patris/product-sync `
  --send-format json `
  --send-mode changes `
  --send-product-sync-secret-env DIGITALOGIC_PRODUCT_SYNC_SECRET `
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
    "url": "https://digitalogic.example/wp-json/digitalogic/v1/patris/product-sync",
    "method": "POST",
    "format": "json",
    "mode": "changes",
    "require_contract": true,
    "initial": true,
    "timeout": "10s",
    "retry_attempts": 3,
    "retry_backoff": "2s",
    "product_sync_secret_env": "DIGITALOGIC_PRODUCT_SYNC_SECRET",
    "headers": {
      "X-Integration-Name": "patris-office"
    }
  }
}
```

Set `require_contract` for every Digitalogic-compatible integration target.
It rejects generic transformed rows as well as raw rows, and it also rejects
CSV, so a missing profile or disabled canonical stage cannot leak legacy Patris
fields through a seemingly non-raw update.

The default is one HTTP attempt, preserving generic webhook behavior. Opt in
to retries only for an idempotent receiver. Digitalogic v1 retries are safe:
Patris encodes the envelope once and reuses the exact bytes, event ID, identity
headers, and secret for every attempt. Network failures, HTTP 408/425/429 and
selected 5xx transients, and receiver responses with `retryable: true`,
`partially_applied`, or `retry_pending` are retried. The receiver's
`accepted`, `already_current`, `replayed`, or `recovered` state is surfaced in
the CLI/server log. Exhaustion reports only sanitized endpoint and structured
status/attempt/pending counts; query strings, response bodies, request headers,
and credential values are never included.

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
