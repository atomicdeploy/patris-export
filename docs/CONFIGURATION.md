# Configuration

Patris Export combines defaults, one or more configuration files, environment
variables, and explicit command-line flags. JSON, YAML, and TOML represent the
same configuration [model](GLOSSARY.md#model).

## Precedence

Later layers override earlier layers:

1. compiled defaults;
2. configuration files in resolved order;
3. `PATRIS_EXPORT_*` environment variables;
4. flags explicitly supplied to the command.

`--config` is repeatable:

```bash
patris-export \
  --config ./config/base.yaml \
  --config ./config/office.yaml \
  serve
```

Nested maps and structs are decoded into the existing defaults. Keep each
overlay focused; the last value supplied for a setting wins.

## File discovery

When `--config` is not used, Patris Export checks:

1. the path list in `PATRIS_EXPORT_CONFIG_FILES` or
   `PATRIS_EXPORT_CONFIG_PATHS`;
2. `PATRIS_EXPORT_CONFIG` or `PATRIS_EXPORT_CONFIG_FILE`;
3. `patris-export.{json,yaml,yml,toml}` in the current directory;
4. the same names under the current `config/` directory;
5. the executable's `config/` directory;
6. the user config directory.

On Windows, the default user path is:

```text
%APPDATA%\Patris Export\config.json
```

Other platforms use the operating system's user config directory under
`patris-export/config.json`.

If no file exists, the manager creates its write target with defaults. Relative
`runtime.temp_dir` values resolve relative to the executable directory when
possible.

## Minimal server example

```yaml
schema_version: 1

server:
  host: 127.0.0.1
  port: 8080
  watch: true
  debounce: 500ms
  http: true
  ipc:
    enabled: false
    path: ""

database:
  path: C:\Patris\data4\kala.db
  charmap: ""
  direct_access: false
  rtl_conversion: false
  raw: false

runtime:
  temp_dir: system
  temp_strategy: auto
  temp_memory_limit_mb: 100
  debug: false

convert:
  output: ./exports
  format: json
  watch: false
  debounce: 1s
  raw: false
```

`database.raw` and `convert.raw` converge on one raw-mode decision at runtime.
Raw mode is appropriate for inspecting a Paradox schema, not for producing the
`kala.db` product projection.

## Canonical profile and record identities

```yaml
canonical:
  enabled: true
  source_id: office-catalog
  profiles:
    kala.db:
      type: kala
  hashes:
    enabled: true
    expose: false
```

- `canonical.enabled` enables configured dataset profiles.
- `profiles` maps a case-insensitive source base name (or `*`) to a profile.
  The only built-in semantic profile today is `kala`.
- `hashes.enabled` controls hash-dependent replication identities and
  `/api/product-sync`.
- `hashes.expose` controls `record_hash` in ordinary products, categories, and
  file/database exports. It cannot expose hashes when `enabled` is false.

See [Record hashes](RECORD-HASHES.md) for the behavior matrix.

## Transform mappings

Mappings may be embedded under `transform` or loaded from a standalone JSON
file through `transform.mapping_file`, `--mapping`, or
`PATRIS_EXPORT_MAPPING_FILE`.

```yaml
transform:
  enabled: true
  key_field: product_code
  fields:
    Code: product_code
    Name: name
  values:
    Unit:
      AD: each
  defaults:
    source_system: office
  include:
    - Code
    - Name
    - Unit
  drop:
    - Sort1
  numeric:
    FOROSH:
      multiplier: 1
      add: 0
      round: 0
  tables:
    other.db:
      fields:
        Serial: part_number
```

The standalone mapping file accepts JSON only. Table keys are matched against
the lower-cased source base name; `*` is the fallback. A table overlay can
override `enabled`, `key_field`, field/value/default/numeric maps, `include`,
and `drop`.

Mapping is part of the generalized non-raw transform path. The current
`kala.db` profile takes its typed product/category branch and does not apply
generic `transform` field rules to those canonical products. Customize the
profile/provider for semantic product changes, or use labels for presentation;
do not assume a generic rename changed `/api/products`. See
[Datasets, mappings, and i18n](DATASETS-MAPPINGS-I18N.md).

## SQL destinations

```yaml
convert:
  table: products

export:
  sqlite_path: ./exports/catalog.sqlite
  sqlite_table: products
  mysql_dsn: ""
  mysql_table: products
  mysql_tls_ca_file: /run/secrets/catalog-ca.pem
  mysql_tls_server_name: db.example
  mysql_connect_timeout: 10s
  batch_size: 250
  reconciliation: upsert_only
  dry_run: false
```

Use `convert.table`/`--table` as the common destination name. If absent, the
CLI considers the target-specific table settings and finally derives a safe
name from the source file.

### SQLite

```bash
patris-export convert kala.db --format sqlite \
  --sqlite-path ./exports/catalog.sqlite \
  --table products
```

If no SQLite path is supplied, the CLI writes
`<output>/<source-base>.sqlite`.

### MySQL/MariaDB

Prefer a protected environment value:

```bash
PATRIS_EXPORT_MYSQL_DSN='user:password@tcp(db.example:3306)/catalog?parseTime=true' \
  patris-export convert kala.db --format mysql --table products
```

Relevant variables:

| Variable | Setting |
| --- | --- |
| `PATRIS_EXPORT_MYSQL_DSN` | `export.mysql_dsn` |
| `PATRIS_EXPORT_MYSQL_TABLE` | `export.mysql_table` |
| `PATRIS_EXPORT_MYSQL_TLS_CA_FILE` | `export.mysql_tls_ca_file` |
| `PATRIS_EXPORT_MYSQL_TLS_SERVER_NAME` | `export.mysql_tls_server_name` |
| `PATRIS_EXPORT_MYSQL_CONNECT_TIMEOUT` | `export.mysql_connect_timeout` |
| `PATRIS_EXPORT_BATCH_SIZE` | `export.batch_size` |
| `PATRIS_EXPORT_SQL_RECONCILIATION` | `export.reconciliation` |
| `PATRIS_EXPORT_SQL_DRY_RUN` | `export.dry_run` |

Never return DSNs or TLS file paths to a browser client. The Web UI's bounded
SQL operator API reports readiness booleans and sanitized diagnostics instead;
see [SQL target operations API](SQL-TARGET-OPERATIONS-API.md).

## Excel output

```yaml
export:
  xlsx_language: auto
  xlsx_mode: precalculated
  xlsx_zebra_rows: true

ui:
  language: fa
  rtl_text_direction: true

column_labels:
  product_code: کد کالا
  part_number: پارت نامبر
```

- language: `auto`, `en`, or `fa`;
- mode: `precalculated` or `formula`;
- zebra rows: boolean.

`column_labels` is a flat override map. The Web UI also has built-in English
and Persian labels. Their current locations and limitations are documented in
[Datasets, mappings, and i18n](DATASETS-MAPPINGS-I18N.md#labels-and-interface-language).

## Pricing integration

Pricing is optional. Standalone Patris Export does not invent shipping or
remote pricing fields when no integration supplied them.

```yaml
canonical:
  pricing:
    mode: static
```

The complete static and Digitalogic pricing structure is defined in
`pkg/pricingcatalog` and covered by
[the product-sync guide](CANONICAL-PRODUCT-SYNC.md). Credentials are referenced
by environment-variable **name**, not stored as values in browser-visible
configuration.

Common environment overrides:

```text
PATRIS_EXPORT_PRICING_MODE
PATRIS_EXPORT_USE_SALE_PRICE_DIRECT_FALLBACK
PATRIS_EXPORT_DIGITALOGIC_URL
PATRIS_EXPORT_DIGITALOGIC_USERNAME_ENV
PATRIS_EXPORT_DIGITALOGIC_PASSWORD_ENV
PATRIS_EXPORT_DIGITALOGIC_BEARER_ENV
PATRIS_EXPORT_PRICING_FRESH_FOR
PATRIS_EXPORT_PRICING_MAX_STALE
PATRIS_EXPORT_PRICING_TIMEOUT
PATRIS_EXPORT_PRICING_BATCH_SIZE
PATRIS_EXPORT_PRICING_BATCH_CONCURRENCY
```

## Outbound updates

```yaml
send_updates:
  enabled: true
  url: https://receiver.example/catalog/events
  method: POST
  format: json
  mode: changes
  initial: true
  allow_raw: false
  require_contract: false
  timeout: 10s
  retry_attempts: 3
  retry_backoff: 2s
  product_sync_secret_env: PATRIS_PRODUCT_SYNC_SECRET
```

`require_contract: true` is for receivers that specifically require the
product-sync compatibility envelope. General integrations should consume the
collection or change shape they need and must handle optional/unknown fields.
See [Remote delivery examples](REMOTE-API-EXAMPLES.md).

## Recent-sales aggregate source

The recent-sales endpoint reads a separately configured datasource. It does not
infer sales from `kala.db`.

```yaml
recent_sales:
  enabled: true
  source: C:\Patris\data4\sales-aggregate.db
  source_id: sales
  token_env: PATRIS_EXPORT_RECENT_SALES_TOKEN
  product_code_field: product_code
  quantity_field: quantity
  sold_at_field: sold_at
  event_id_field: sale_event_id
  max_window: 2160h
  max_page_size: 500
  max_source_rows: 1000000
  max_source_mb: 256
```

The source must be local, distinct from the primary product database, and not
named `kala.db`. Authentication currently uses the configured bearer-token
environment variable. A future unified ACL will replace route-specific policy
without changing the aggregate's purpose.

## Edge mode

```yaml
edge:
  enabled: true
  target_url: https://central.example/patris
  token: ""
  source_id: office-a
  debounce: 1s
  max_upload_mb: 512
  upload_dir: edge-uploads
```

Prefer `PATRIS_EXPORT_EDGE_TOKEN` over storing the token in a file. Edge upload
is a transport for one source snapshot; it is not the planned multi-database
directory loader.

## Browser configuration API

`GET /api/config` returns a browser-safe view. It clears MySQL connection
material, integration tokens/headers/commands, recent-sales details, URL
credentials/query strings, and internal `extra` values.

`PUT /api/config` replaces the complete config and persists it. Always
GET-modify-PUT the whole object; sending a partial object may reset unrelated
settings. Sanitization is not authentication, so keep the service behind the
deployment boundary described in the generated API reference.

## Principal environment variables

This is a practical index, not a substitute for the config structs:

| Area | Variables |
| --- | --- |
| Config | `PATRIS_EXPORT_CONFIG`, `PATRIS_EXPORT_CONFIG_FILE`, `PATRIS_EXPORT_CONFIG_FILES`, `PATRIS_EXPORT_CONFIG_PATHS` |
| Server/source | `PATRIS_EXPORT_HOST`, `PATRIS_EXPORT_PORT`, `PATRIS_EXPORT_ADDR`, `PATRIS_EXPORT_DB_PATH`, `PATRIS_EXPORT_WATCH`, `PATRIS_EXPORT_DEBOUNCE`, `PATRIS_EXPORT_HTTP`, `PATRIS_EXPORT_IPC`, `PATRIS_EXPORT_IPC_PATH` |
| Conversion | `PATRIS_EXPORT_CHARMAP`, `PATRIS_EXPORT_DIRECT_ACCESS`, `PATRIS_EXPORT_RTL`, `PATRIS_EXPORT_RAW`, `PATRIS_EXPORT_MAPPING_FILE`, `PATRIS_EXPORT_OUTPUT`, `PATRIS_EXPORT_FORMAT`, `PATRIS_EXPORT_TABLE` |
| Canonical identity | `PATRIS_EXPORT_CANONICAL`, `PATRIS_EXPORT_CANONICAL_SOURCE_ID`, `PATRIS_EXPORT_RECORD_HASHES`, `PATRIS_EXPORT_EXPOSE_RECORD_HASHES` |
| Temp/runtime | `PATRIS_EXPORT_TEMP_DIR`, `PATRIS_EXPORT_TMPDIR`, `PATRIS_EXPORT_TEMP_STRATEGY`, `PATRIS_EXPORT_TEMP_MEMORY_LIMIT_MB`, `PATRIS_EXPORT_TMPFS_LIMIT_MB`, `PATRIS_EXPORT_DEBUG` |
| Convert watch | `PATRIS_EXPORT_CONVERT_WATCH`, `PATRIS_EXPORT_CONVERT_DEBOUNCE` |
| Excel | `PATRIS_EXPORT_XLSX_LANGUAGE`, `PATRIS_EXPORT_XLSX_MODE`, `PATRIS_EXPORT_XLSX_ZEBRA_ROWS` |
| Delivery | `PATRIS_EXPORT_SEND_UPDATES`, `PATRIS_EXPORT_SEND_URL`, `PATRIS_EXPORT_SEND_METHOD`, `PATRIS_EXPORT_SEND_FORMAT`, `PATRIS_EXPORT_SEND_MODE`, `PATRIS_EXPORT_SEND_INITIAL`, `PATRIS_EXPORT_SEND_ALLOW_RAW`, `PATRIS_EXPORT_SEND_REQUIRE_CONTRACT`, `PATRIS_EXPORT_SEND_TIMEOUT`, `PATRIS_EXPORT_SEND_RETRY_ATTEMPTS`, `PATRIS_EXPORT_SEND_RETRY_BACKOFF`, `PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV`, `PATRIS_EXPORT_SEND_COMMAND` |

The source remains authoritative for less common notification, edge,
recent-sales, and pricing overrides.
