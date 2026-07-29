# CLI reference

The executable uses Cobra. `patris-export --help` and
`patris-export <command> --help` are the runtime source of truth. This guide
explains the current command surface and the non-obvious interactions between
flags and configuration.

## Invocation and precedence

```text
patris-export [global flags] <command> [command flags] [arguments]
```

Effective settings are applied in this order:

1. compiled defaults;
2. discovered or explicitly supplied configuration files, layered in order;
3. `PATRIS_EXPORT_*` environment variables;
4. flags explicitly supplied on this invocation.

See [Configuration](CONFIGURATION.md) for discovery paths and examples.

## Global flags

| Flag | Meaning |
| --- | --- |
| `-c, --config <path>` | Add a JSON/YAML/TOML config file. Repeat to layer files. |
| `--db <path>` | Open this database in one-shot viewer mode when no subcommand is used. |
| `-m, --charmap <path>` | Override the embedded Patris81 character map. |
| `-o, --output <path>` | Output directory, or `-` for JSON/CSV stdout. Default `.`. |
| `--temp-dir <path>` | Temp directory for copied or downloaded sources. |
| `--temp-strategy <auto\|system\|memory>` | Choose temp storage. `auto` may use `/dev/shm` on Linux. |
| `--temp-memory-limit-mb <n>` | Maximum known file size eligible for memory temp storage. |
| `-v, --verbose` | Enable verbose logging. |
| `-d, --direct-access` | Read the source directly instead of through a temp copy. |
| `-r, --rtl` | Apply opt-in logical RTL conversion for mixed Persian/Latin output. |
| `--raw` | Bypass conversion, compaction, keying, RTL, mapping, and canonical projection. |
| `--record-hashes=<bool>` | Enable or disable hash-dependent sync identities. Default `true`. |
| `--expose-record-hashes=<bool>` | Show or hide `record_hash` in ordinary projected outputs. Default `true`. |
| `--mapping <path>` | Load a standalone JSON transform mapping. |
| `--version` | Print detailed build/version information. |

`-d` means **direct access**. Debounce is the command-specific
`--debounce` flag and has no short form.

## `convert`

```text
patris-export convert [global flags] <database-file-or-url> [flags]
```

Converts once or watches a local/HTTP(S) source.

| Flag | Meaning |
| --- | --- |
| `-f, --format <name>` | `json`, `csv`, `xlsx`, `sqlite`, or `mysql`. `excel` normalizes to `xlsx`. |
| `-w, --watch` | Re-run when a local file changes or a remote source changes. |
| `--debounce <duration>` | Local debounce or remote polling interval. Default `1s`; a non-positive remote value becomes `5m`. |
| `--table <name>` | Shared SQLite/MySQL destination table name. |
| `--sqlite-path <path>` | SQLite destination file. Default `<output>/<source>.sqlite`. |
| `--sqlite-table <name>` | SQLite-specific fallback table name. Prefer `--table` in reusable commands. |
| `--mysql-dsn <dsn>` | MySQL/MariaDB DSN. Prefer `PATRIS_EXPORT_MYSQL_DSN` to avoid shell history. |
| `--mysql-table <name>` | MySQL-specific fallback table name. Prefer `--table`. |
| `--batch-size <n>` | Maximum rows per prepared SQL batch. Config default `500`. |
| `--reconciliation <mode>` | `upsert_only`, `soft_delete_missing`, or `delete_missing`. |
| `--reconciliation-token <digest>` | Exact dry-run digest required for `soft_delete_missing` apply. |
| `--dry-run` | Calculate SQL insert/update/delete counts without applying changes. |
| `--xlsx-language <auto\|en\|fa>` | Header language. `auto` follows the UI language. |
| `--xlsx-mode <precalculated\|formula>` | Write final values or spreadsheet formulas. |
| `--xlsx-zebra=<bool>` | Enable alternating data-row fills. |
| `--send-url <url>` | HTTP endpoint for initial/watch delivery. |
| `--send-format <json\|csv>` | Outbound update encoding. |
| `--send-mode <changes\|full>` | Send a change set or full snapshot. |
| `--send-command <command>` | Run a command and write the payload to its stdin. |
| `--send-initial=<bool>` | Send the initial full state before watch updates. |
| `--send-product-sync-secret-env <name>` | Environment-variable name containing the outbound header secret. |
| `--send-retry-attempts <n>` | Total HTTP attempts. Default `1`. |
| `--send-retry-backoff <duration>` | Delay between retryable attempts. |

Examples:

```bash
patris-export convert kala.db --format json --output exports
patris-export --raw convert any-table.db --format csv --output -
patris-export convert kala.db --format xlsx --xlsx-language fa --xlsx-mode formula
```

Stdout supports JSON and CSV only, and cannot be combined with watch mode.

### SQLite

```bash
patris-export convert kala.db --format sqlite \
  --sqlite-path ./exports/catalog.sqlite \
  --table products \
  --batch-size 250
```

SQLite is embedded; no separate database server is required. The sink creates
the destination directory, creates a table when needed, and evolves columns
additively. The projected key field becomes the row identity.

### MySQL and MariaDB

```bash
export PATRIS_EXPORT_MYSQL_DSN='user:password@tcp(db.example:3306)/catalog?parseTime=true'
patris-export convert kala.db --format mysql \
  --table products \
  --batch-size 250
unset PATRIS_EXPORT_MYSQL_DSN
```

The Go MySQL driver is compatible with MySQL and MariaDB. Configure verified
TLS with `export.mysql_tls_ca_file` and
`export.mysql_tls_server_name`, or their environment equivalents. The CLI
applies a two-minute operation context and a separately normalized connection
timeout between `100ms` and `2m`.

### SQL reconciliation

- `upsert_only` inserts new rows and updates changed rows; destination-only
  rows remain. This is the safe default.
- `soft_delete_missing` marks destination-only rows and requires a matching
  digest from an unchanged dry-run plan.
- `delete_missing` physically removes destination-only rows. Use it only in an
  explicitly protected server-side workflow with a complete authoritative
  source.

Preview first:

```bash
patris-export convert kala.db --format sqlite \
  --sqlite-path ./exports/catalog.sqlite \
  --table products \
  --reconciliation soft_delete_missing \
  --dry-run
```

Then repeat against the unchanged source/target using the printed
`sha256:...` confirmation:

```bash
patris-export convert kala.db --format sqlite \
  --sqlite-path ./exports/catalog.sqlite \
  --table products \
  --reconciliation soft_delete_missing \
  --reconciliation-token 'sha256:<exact-preview-digest>'
```

## `info`

```text
patris-export info <database-file>
```

Prints file metadata, record count, and Paradox field definitions. It does not
apply a `kala.db` product projection.

## `company`

```text
patris-export company <company.inf>
```

Parses one explicitly supplied `company.inf`. Automatic discovery beside a
database and multi-directory company loading are planned, not implemented.

## Root and `view`

```text
patris-export [database-file]
patris-export --db <database-file>
patris-export view [database-file-or-url]
```

`view` flags:

| Flag | Meaning |
| --- | --- |
| `--html-output <path>` | Write the self-contained snapshot to this file or directory. |
| `--no-open` | Generate without launching the native/browser window. |
| `--title <text>` | Override the viewer window title. |

If `view` has no argument, it uses `database.path` from configuration.

## `serve`

```text
patris-export serve [database-file-or-url] [flags]
```

The source argument is optional only when `database.path` is configured.

| Flag | Meaning |
| --- | --- |
| `-a, --addr <address>` | Bind override such as `127.0.0.1:8080` or `:8080`. |
| `--host <host>` | Bind host/interface. |
| `--port <n>` | Bind port. |
| `-w, --watch=<bool>` | Watch/poll and broadcast changes. Default `true`. |
| `--debounce <duration>` | Local debounce or URL polling interval. Default `0s`. |
| `--http=<bool>` | Enable HTTP, REST, WebSocket, and Web UI. Default `true`. |
| `--ipc` | Enable local IPC. |
| `--ipc-path <path>` | Override the platform-specific IPC endpoint. |

At least one of HTTP or IPC must be enabled. The effective serve configuration
is saved through the active configuration manager.

## `stub` / `edge`

```text
patris-export stub [database-file] [flags]
patris-export edge [database-file] [flags]
```

Watches a source beside Patris81 and uploads the raw file to a central Patris
Export server.

| Flag | Meaning |
| --- | --- |
| `--target-url <url>` | Base URL or `/api/edge/upload` URL. |
| `--token <value>` | Optional receiver bearer token. Prefer protected config/environment. |
| `--source-id <id>` | Stable source label included with uploads. |
| `--debounce <duration>` | Delay before upload after a change. |
| `--once` | Upload once and exit. |
| `--initial=<bool>` | Upload current file before watching. Default `true`. |
| `--max-upload-mb <n>` | Maximum source size. Config default `512`. |

## `ipc`

```text
patris-export ipc <method> [json-params] [--ipc-path <path>]
```

Calls the local JSON-lines IPC endpoint. Current handler methods include
`records`, `info`, `app`, `config.get`, `config.set`, `toast`, `refresh`, and
`subscribe`. See [Embedding](EMBEDDING.md) for exact messages and platform
paths.

## `tui`

```text
patris-export tui [database-file]
```

Starts the keyboard-first terminal dashboard. With no argument it uses
`database.path`. See [TUI guide](TUI-GUIDE.md).

## `verify`

```text
patris-export verify <snapshot.json|->
```

Validates a product-sync compatibility snapshot without applying it. `-` reads
stdin. This is for consumers of the compatibility envelope, not a requirement
for parsing `/api/records` or `/api/products`.

## `update`

```text
patris-export update [flags]
```

| Flag | Meaning |
| --- | --- |
| `-b, --branch <name>` | GitHub Actions branch. Default `main`. |
| `--api-url <url>` | Fetch `/api/update/manifest` from a running Patris Export service. |
| `--manifest-url <url>` | Use an explicit executable manifest URL. |

`GITHUB_TOKEN` may be used for private repositories or higher API limits.

## `license`

Optional licensing commands exist in all builds:

```text
patris-export license status
patris-export license challenge
patris-export license install [license-key] [--file <path>]
patris-export license remove
```

Whether a license is enforced depends on the build variant. See
[Licensing](LICENSING.md).

## Formats not yet supported

The current `convert` command does **not** accept TSV, BSON, MessagePack, or
Protocol Buffers. The data API does not yet generate an SQLite download. These
are roadmap items and should not be inferred from integration examples.
