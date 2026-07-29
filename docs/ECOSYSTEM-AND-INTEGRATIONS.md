# Ecosystem and integrations

Patris Export can run alone or as one module in a larger catalog system.
Digitalogic and WordPress are supported consumers, not mandatory owners of the
data model.

## Deployment patterns

### Standalone workstation

```text
Patris81 .db → Patris Export → Web UI / TUI / JSON / CSV / XLSX
```

Use this for inspection, local price lists, ad-hoc conversion, or another
platform that knows nothing about Digitalogic.

### Database publisher

```text
Patris81 .db → Patris Export → SQLite or MySQL/MariaDB
```

Use `upsert_only` for routine publishing. Missing-row reconciliation requires
a complete source and an explicit dry-run/confirmation workflow. See
[Configuration: SQL destinations](CONFIGURATION.md#sql-destinations).

### Generic web-service consumer

```text
Patris Export REST/WebSocket → desktop app, service, spreadsheet, or module
```

Choose the smallest surface:

- `/api/records` for raw arbitrary-table access;
- `/api/products` and `/api/categories` for catalog reading;
- `/ws` for live viewer/update events;
- `/api/product-sync` only for a stateful replica that needs its atomic
  compatibility envelope.

Do not require a consumer to understand every optional field. Parse known
values, retain extensions when forwarding, and report unsupported known
semantics clearly.

### Edge and central server

```text
Patris workstation stub → /api/edge/upload → central Patris Export
```

The `stub`/`edge` command watches one local file and uploads snapshots. The
central process applies its normal datasource/profile/export/UI pipeline. This
is useful when the Patris workstation should not run the full service.

Edge upload is not multi-database directory ingestion and does not yet use the
planned unified ACL.

### Embedded desktop application

Electron, Tauri, WebView2, and native applications can:

- start Patris Export and use loopback HTTP/WebSocket;
- enable local IPC and call the JSON-lines protocol;
- embed the Go server/router;
- load the C-compatible library build where its ABI fits the host.

See [Embedding and IPC](EMBEDDING.md). A browser renderer should call a trusted
main/native process for privileged filesystem or IPC work rather than receiving
database credentials.

## Digitalogic and WooCommerce

Digitalogic can supply remote pricing assignments and consume transformed
catalog data. The intended boundary is:

1. Patris Export reads raw Patris values.
2. The optional pricing provider supplies currencies, shipping methods, rates,
   margins, or assignments.
3. Patris Export calculates/projects the product fields only when that
   integration is active.
4. Digitalogic receives transformed products; it does not need to reinterpret
   `Sharh` or raw Patris encoding.

The reverse direction remains possible: Digitalogic can read Patris product
values and maintain its own WordPress/WooCommerce representation. Neither
project should depend on the other's internal database tables or PHP/Go class
layout.

Customer-facing Digitalogic surfaces should use `کد کالا` (“Product Code”).
Internal source keys and compatibility fields may remain stable, but public
content must not expose source-system branding as the product-code label.

For another commerce platform, replace the Digitalogic pricing provider and
receiver while keeping `/api/records`, `/api/products`, SQL sinks, and the
viewer usable.

## Outbound HTTP and command delivery

The `convert --watch` workflow can send the initial snapshot and later changes:

```bash
patris-export convert kala.db --format json --watch \
  --send-url https://receiver.example/catalog/events \
  --send-mode changes \
  --send-retry-attempts 3 \
  --send-retry-backoff 2s
```

Or pipe each payload to a local process:

```bash
patris-export convert kala.db --format json --watch \
  --send-command "node ./receiver.cjs"
```

The detailed REST, JSON-RPC, WordPress AJAX, and gRPC-gateway adapters are in
[Remote update delivery](REMOTE-API-EXAMPLES.md). Native gRPC and protobuf
output are not currently built into the CLI.

For n8n, use an authenticated Webhook node as the `--send-url`, validate the
content type and expected fields, make processing idempotent if retries are
enabled, and return a bounded success/error response. Keep n8n credentials in
its credential store, not in Patris config or workflow-export JSON.

## WebSocket

`/ws` serves the built-in viewer and external live clients. The AsyncAPI source
documents initial/update/config/process/toast events and client toast/refresh
commands:

```bash
websocat ws://127.0.0.1:8080/ws
```

See [WebSocat examples](examples/websocat.md) and the
[generated API reference](api/README.md). Current WebSocket deployment trusts
the surrounding network boundary; do not expose it directly to an untrusted
network while unified authentication is pending.

## Excel

There are two distinct paths:

1. `convert --format xlsx` or `/api/products.xlsx` creates a snapshot workbook
   with language, formula/precalculated, RTL, and zebra-row options.
2. The macro-enabled workbook integration reads the service repeatedly and
   maintains a business price-list experience.

See [Excel export](EXCEL-EXPORT.md) and
[Excel pricing sync](EXCEL-PRICING-SYNC.md). A client-side “filtered export”
mode is planned; today the server exports its route collection rather than the
browser's current search subset.

## Recent-sales data

`/api/recent-sales` is an optional product-level aggregate from a separately
configured sales datasource. It currently returns product code, aggregate sold
quantity, sale frequency, last-sold time, source revision, and window/page
metadata.

The endpoint is described as a product-level aggregate. Access policy belongs
to authentication/authorization. Its narrow model still avoids accidentally
coupling catalog automation to invoice/customer/order fields.
See [Recent-sales API](RECENT-SALES-API.md).

## Source and executable distribution

The service exposes:

- `/api/source/manifest` and `/api/source/file`;
- `/api/update/manifest` and `/api/update/executable`.

File endpoints support conditional requests and byte ranges. They transfer the
source/executable bytes, not generated JSON/CSV/XLSX cache artifacts.

## Static/offline documentation for partners

Build the public API documentation ZIP:

```bash
make docs-install
make docs-verify
make docs-package
```

Share the public archive plus its SHA-256 manifest. Do not share the internal
archive outside an authorized operator/developer boundary. A future GitHub
Pages site can publish the same public static build; the repository Markdown
should remain canonical so Wiki/Pages do not create a second hand-maintained
source.

## Integration checklist

1. Pick raw records, product/category collections, or replication events based
   on the consumer's actual job.
2. Agree on source identity and which fields are required versus optional.
3. Preserve omission versus explicit `null`.
4. Treat hashes as optional change identities, not authentication.
5. Bound request size/time and retries.
6. Keep credentials in environment/secret stores and logs value-free.
7. Test an unknown extension member and a missing optional member.
8. Test duplicate/quarantined product codes rather than silently overwriting.
9. Make destructive reconciliation an explicit operator action.
10. Keep the integration replaceable; do not read another project's private
    tables when an API or sink can express the same boundary.

## Roadmap boundaries

The following ecosystem pieces have approved design direction but are not
available yet:

- multi-DB `dataN` directory inventory/watch and automatic `company.inf`;
- filtered subset export from the viewer;
- TSV, BSON, MessagePack, Protocol Buffers, and SQLite API downloads;
- unified OS/API-key principals and per-dataset/action ACL;
- Windows-only Patris81 UI observation/control package.
