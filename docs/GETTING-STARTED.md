# Getting started

This guide takes a first-time user from a Paradox `.db` file to the viewer,
raw records, a product catalog, and an export. It uses loopback networking so
the service is not exposed to an untrusted network.

## 1. Install

Download a source-built package from the
[GitHub Releases page](https://github.com/atomicdeploy/patris-export/releases)
and follow [the binary installation guide](INSTALL-BINARIES.md).

The default build loads pxlib dynamically when a `.db` file is opened. On
Windows, keep the packaged `libpxlib.dll` beside `patris-export.exe`. On Linux,
use the packaged launcher or install the required pxlib runtime. The executable
can still show help and version information when pxlib is unavailable, but
reading a Paradox file requires a working backend.

Verify the executable:

```powershell
patris-export --version
patris-export --help
```

## 2. Inspect a database before transforming it

Show the table fields and record count:

```powershell
patris-export info C:\Patris\data4\kala.db
```

For any Paradox table, write raw pxlib rows to standard output:

```powershell
patris-export --raw convert C:\Patris\data4\kala.db --format json --output -
```

[`Raw`](GLOSSARY.md#raw) means source field names and values with no character
conversion, `ANBAR` compaction, keying, RTL conversion, configured mapping, or
`kala.db` pricing projection. Internal Paradox `Sort*` fields are omitted.

Temporary copying is the default for file output and services because it
reduces conflict with BDE writers. Use `--direct-access` only when you
understand the file-lock risk.

## 3. Open the one-shot viewer

All of these forms open the same app-style snapshot:

```powershell
patris-export C:\Patris\data4\kala.db
patris-export --db C:\Patris\data4\kala.db
patris-export view C:\Patris\data4\kala.db
```

Generate a self-contained HTML file without opening a window:

```powershell
patris-export view C:\Patris\data4\kala.db `
  --no-open `
  --html-output .\output\kala-viewer.html
```

## 4. Start the web service

```powershell
patris-export serve C:\Patris\data4\kala.db `
  --addr 127.0.0.1:8080
```

Open:

- viewer: <http://127.0.0.1:8080/viewer>
- service home: <http://127.0.0.1:8080/>
- raw records: <http://127.0.0.1:8080/api/records>
- database metadata: <http://127.0.0.1:8080/api/info>

For a configured `kala.db` profile:

- products: <http://127.0.0.1:8080/api/products>
- categories: <http://127.0.0.1:8080/api/categories>
- replication compatibility envelope:
  <http://127.0.0.1:8080/api/product-sync>

Use `/api/records` for arbitrary schemas and source inspection. Use
`/api/products` for the transformed sellable product collection. Do not use
`/api/product-sync` merely to list products; it also carries categories,
exclusions, quarantine state, tombstones, and sync identities for receivers
that need an atomic replication event. The reasoning is expanded in
[Architecture](ARCHITECTURE.md#why-product-sync-still-exists).

The server watches the source by default. Disable watching for a static
session:

```powershell
patris-export serve C:\Patris\data4\kala.db `
  --addr 127.0.0.1:8080 `
  --watch=false
```

## 5. Download a collection

Raw source rows:

```powershell
curl.exe http://127.0.0.1:8080/api/records -o records.json
curl.exe http://127.0.0.1:8080/api/records.csv -o records.csv
curl.exe "http://127.0.0.1:8080/api/records.xlsx?download=1" -o records.xlsx
```

Transformed `kala.db` products:

```powershell
curl.exe http://127.0.0.1:8080/api/products -o products.json
curl.exe http://127.0.0.1:8080/api/products.csv -o products.csv
curl.exe "http://127.0.0.1:8080/api/products.xlsx?download=1&language=fa" -o products-fa.xlsx
```

CSV and XLSX can also be selected through the `format` query or HTTP `Accept`
header. Exact request and response behavior is in the
[generated API reference](api/README.md).

## 6. Convert to files or a database

```powershell
patris-export convert C:\Patris\data4\kala.db --format json --output .\output
patris-export convert C:\Patris\data4\kala.db --format csv --output .\output
patris-export convert C:\Patris\data4\kala.db --format xlsx --output .\output `
  --xlsx-language fa `
  --xlsx-mode formula `
  --xlsx-zebra=true
```

SQLite:

```powershell
patris-export convert C:\Patris\data4\kala.db --format sqlite `
  --sqlite-path .\output\catalog.sqlite `
  --table products `
  --batch-size 250
```

MySQL or MariaDB (inject the DSN into the process rather than committing it):

```powershell
$env:PATRIS_EXPORT_MYSQL_DSN = 'user:password@tcp(db.example:3306)/catalog?parseTime=true'
patris-export convert C:\Patris\data4\kala.db --format mysql `
  --table products `
  --batch-size 250
Remove-Item Env:PATRIS_EXPORT_MYSQL_DSN
```

Both SQL sinks use `upsert_only` by default. Read
[Configuration: SQL destinations](CONFIGURATION.md#sql-destinations) before
using a missing-row reconciliation mode.

## 7. Use the terminal dashboard

```powershell
patris-export tui C:\Patris\data4\kala.db
```

The dashboard shows the source, detected fields, configuration, character map,
process/file-lock state, and local service shortcuts. See the
[TUI guide](TUI-GUIDE.md) for every key.

## 8. Add configuration

Patris Export discovers JSON, YAML, or TOML configuration and also accepts
repeatable `--config` flags:

```powershell
patris-export `
  --config .\config\base.yaml `
  --config .\config\office.yaml `
  serve
```

Files are layered in the supplied order; environment variables and explicit
CLI flags override them. Continue with [Configuration](CONFIGURATION.md).

## Safe first-run checklist

1. Keep the HTTP listener on `127.0.0.1` until an authentication boundary is
   deliberately configured.
2. Do not store a MySQL DSN, bearer token, or password in a repository.
3. Inspect unfamiliar tables through raw `/api/records` before adding mappings.
4. Use SQL `--dry-run` before any missing-row reconciliation.
5. Keep direct access off while Patris81 or another BDE writer may be active.
6. Treat record hashes as change identities, not signatures or credentials.
