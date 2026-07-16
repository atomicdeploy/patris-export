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

Raw-mode outbound delivery fails closed by default. `send_updates.allow_raw`
must be explicitly enabled for a trusted, non-integration destination; never
enable it for Digitalogic or another product-sync consumer.

```powershell
patris-export convert C:\Patris\data4\kala.db -f json -w `
  --send-url https://example.internal/patris-webhook `
  --send-format json `
  --send-mode changes
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
    "url": "https://example.internal/patris-webhook",
    "method": "POST",
    "format": "json",
    "mode": "changes",
    "require_contract": true,
    "initial": true,
    "timeout": "10s",
    "headers": {
      "X-Patris-Source": "edge-01"
    }
  }
}
```

Set `require_contract` for every Digitalogic-compatible integration target.
It rejects generic transformed rows as well as raw rows, and it also rejects
CSV, so a missing profile or disabled canonical stage cannot leak legacy Patris
fields through a seemingly non-raw update.

In CSV `changes` mode, each row includes `_change_type` with `added`,
`modified`, or `deleted`. Modified rows contain the complete transformed row
and `_changed_fields`; deleted rows are tombstones containing the configured
key field. This makes the CSV stream actionable without exposing raw Patris
fields when transform mapping is enabled.
