# Transform, Raw, Export, and Send Update Examples

`patris-export` can now run one shared record pipeline for CLI exports, the REST API,
WebSocket updates, watch-mode webhooks, and SQL/Excel outputs.

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

SQLite and MySQL exports create the destination table when needed and upsert by
the configured key field.

## Watch and Send Updates

When watch mode is enabled, the initial payload can be sent once and later file
changes can be sent as either a changeset or a full snapshot. Payloads can go to
an HTTP endpoint, a command on stdin, or both. The initial event contains the
complete transformed snapshot. Later `changes` events use the same key-aware
diff implementation as the WebSocket server and contain `added`, `modified`,
and `deleted` entries. Modified entries include both the changed field values
and the complete transformed `record`, so downstream upserts do not need to
reconstruct unchanged fields.

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
    "initial": true,
    "timeout": "10s",
    "headers": {
      "X-Patris-Source": "edge-01"
    }
  }
}
```

In CSV `changes` mode, each row includes `_change_type` with `added`,
`modified`, or `deleted`. Modified rows contain the complete transformed row
and `_changed_fields`; deleted rows are tombstones containing the configured
key field. This makes the CSV stream actionable without exposing raw Patris
fields when transform mapping is enabled.
