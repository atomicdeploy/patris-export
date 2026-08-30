# Changelog

All notable changes to Patris Export are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Canonical pricing now exposes the first `Sharh1` slot independently as
  `partner_price_source`, supports the `domestic` / `خرید داخلی` route with an
  explicit zero IRR/kg rate, and provides a default-off
  `use_sale_price_direct_fallback` policy for exact unmodified `FOROSH` prices.

### Changed

- Pricing now falls through only after testing a complete route: foreign CNY
  requires positive weight and an enabled non-domestic freight assignment;
  partner price adds margin without freight and forces domestic shipping; the
  optional direct sale route applies neither margin nor rounding.

### Fixed

- `FOROSH` is no longer conflated with Patris' partner price. The producer reads
  partner price from the first `Sharh1` slot while retaining `FOROSH` as the
  separate sale-price fact.

## [1.3.3] - 2026-08-30

- Add a bounded authenticated revision probe so an unchanged Excel catalog can
  finish without starting a remote snapshot build. The workbook persists and
  compares the composite, pricing, catalog, and Patris source revisions plus
  its local row/coherence/parity gates; changed or unverifiable state still
  falls back to the complete fail-closed snapshot.
## [1.3.2] - 2026-08-30

- Complete ordinary Excel refreshes as a bounded no-op when the authenticated
  job's stable catalog dataset revision, pricing state revision, source
  revision, row count, local catalog coherence, identity status, and strict
  price parity all match the committed workbook snapshot. Changed or
  unverified identities still use the full fail-closed download and commit.

- Make unchanged Excel catalog refreshes reuse a five-minute authenticated
  snapshot while requiring the companion's live verified source, state, and
  catalog revision before any local cache reuse. A disconnected or changed
  revision falls back to the full fail-closed remote snapshot path.

## [1.3.1] - 2026-07-27

### Fixed

- Canonical pricing cache fills now use context-aware serialization, so a
  request waiting behind a slow projection returns at its own deadline instead
  of inheriting the earlier request's runtime. Startup also skips the
  canonical projection when initial delivery is disabled and bounds enabled
  initial projections to the canonical request ceiling.

## [1.3.0] - 2026-07-26

### Added

- Pull requests now prove SQL sink parity and transactional rollback against
  disposable MySQL 8.4, MariaDB 11.4, and SQLite targets while preserving
  leading-zero keys, additive schemas, user-owned columns, and guarded
  soft-delete/restore behavior.
- SQL targets now support guarded `soft_delete_missing` reconciliation through
  a reserved boolean tombstone, non-mutating operator evidence, exact preview
  confirmation, empty-source blocking, stale-plan detection, restoration, and
  rollback-safe retries with fresh-preview idempotency.
- MySQL/MariaDB targets now have a shared bounded read-only connection probe,
  protected custom-CA/server-name configuration, verified TLS 1.2+ connector,
  and typed secret-safe retry diagnostics for future authenticated UI controls.
- Product names and descriptions are checked for leading/trailing spaces,
  repeated spaces, and unseparated Persian/English/digit transitions. Stable
  warning codes are exported, summarized on the CLI, and highlighted in the
  Web UI without logging source values.
- Opt-in `cgo` and `cgo-static` pxlib backends now complement the default
  runtime-dynamic loader across Make, shell, and local Windows builds; manifests
  identify the selected backend and static packages omit the pxlib runtime DLL.
- Copy-paste remote delivery recipes distinguish native REST/WordPress REST from JSON-RPC, legacy WordPress AJAX, and gRPC gateway adapters, including secret injection, idempotency, retry, failure, and replay guidance.
- A dependency-free Node.js loopback adapter, local retryable mock receiver, sparse-payload tests, example configs, and a minimal gRPC HTTP-transcoding contract exercise non-native delivery without duplicating the Patris transformation pipeline.
- The Web UI now provides localized header context menus, a full-table column-resize guide, optional sticky first-column behavior, and independently selectable warehouse columns under a two-row Warehouse Stock header.
- Excel exports now support localized English/Persian human-readable headers, configurable pre-calculated or live-formula pricing, optional zebra rows, RTL/LTR workbook views, and one numeric column per available warehouse.
- A macro-enabled Patris/Digitalogic business-dashboard example includes refresh, search, reset, configurable endpoints, and company-logo placement without embedding credentials.
- The standalone home and character-map routes now share the viewer's complete
  English/Persian translation runtime, embedded Vazirmatn font, persisted
  language preference, RTL/LTR layout, and localized accessible controls.
- An authenticated recent-sales endpoint now projects a separately configured
  supported source into a deterministic, paged product-level aggregate while
  excluding customer, order, payment, and delivery details; its source/auth
  profile stays server-side across browser config reads and writes.
- The Windows scheduled-task helper can import explicitly named current
  user/machine environment values at process launch without storing credential
  values in task XML, arguments, configuration, logs, or release artifacts.
- The canonical Excel template now reads, previews, and explicitly applies
  global pricing settings through a loopback-only, CSRF-guarded companion that
  injects the protected source-scoped credential, preserves optimistic
  revisions and idempotency, and regenerates/delivers canonical products after
  apply without placing a credential in VBA or workbook cells.

### Changed

- Digitalogic assignment prefetch now uses a configurable, bounded worker pool
  with deterministic page-order validation; its 60-second default HTTP timeout
  is bounded to 80 seconds so canonical requests retain an 85-second ceiling.
- Canonical JSON, CSV, HTTP, spreadsheet, SQL, and outbound-update rows now share a sparse field boundary: never-received values are omitted while explicitly received nulls remain null.
- Product-sync is now one living standard with strict current-field decoding; obsolete aliases and compatibility branches are rejected.
- Shipping integration uses the paired `shipping_price_per_kg` and `shipping_price_per_kg_currency` fields; users must select uppercase `CNY` or `IRR` for every configured method.
- Landed-price calculation converts CNY freight through the CNY-to-IRT rate, converts IRR freight at ten IRR per IRT, then applies markup and rounds once.
- Missing source/reference values are omitted, while `null` is preserved only when explicitly supplied upstream.
- Successful product-sync responses must include typed status, identity, retry, pending, and deferred fields; missing or explicit-null fields fail closed.
- Persisted custom headers cannot use any product-sync-secret name; receiver credentials remain environment-backed only.
- English and Persian table labels now use human-readable source and canonical field names, Persian UI text consistently uses the embedded Vazirmatn font, and warning cells remain on one compact line.
- The Web UI's Excel action now sends the active interface language and configured formula/zebra preferences to the canonical server-side workbook writer.

### Fixed

- Scheduled-task stop and replacement now treat an empty verified process set
  as a successful no-op instead of failing after the task has stopped.
- Scheduled-task stop, restart, upgrade, uninstall, and replacement now
  terminate only the exact deployment's Patris child process, preventing an
  orphan from retaining the API port after the PowerShell parent exits.
- A terminal Digitalogic assignment-page failure now cancels in-flight sibling
  requests, commits no partial assignments, and emits only typed secret-safe
  diagnostics.
- Browser configuration responses, initial WebSocket snapshots, and config
  update events no longer expose the MySQL DSN or protected TLS paths/names;
  browser saves preserve the server-side values and ignore client-supplied
  replacements.
- Excel dashboard refresh now validates Code identity before mutating reviewed
  rows, rejects empty/non-JSON Digitalogic responses, uses deterministic
  canonical-over-legacy label precedence, and strips private Office path/author
  metadata plus external connections from the checked-in XLSM package.
- The pxlib reader now keeps native data as typed pointers, bounds record and string reads, serializes close against active reads, and passes `go vet` without suppressions.
- Standalone exports no longer contain Digitalogic pricing, formula, or shipping-method fields and warnings when no pricing integration is configured.
- Windows file-lock monitoring now uses targeted Restart Manager queries instead of overlapping system-wide handle snapshots that could exhaust committed memory.
- CGO and ALM build tags now compose instead of replacing one another, and
  static builds fail early with an actionable message when their pxlib archive
  is unavailable.
- Dialog More menus now perform their advertised actions with consistent spacing, and warehouse stock is no longer collapsed into pill badges.
- The welcome route now uses the shared allowlisted SVG primitives instead of
  raw emoji, and embedded bundle replacement no longer interprets JavaScript
  dollar-replacement tokens while generating offline HTML.

## [1.2.0] - 2026-07-17

### Added

- The canonical catalog separates typed, hashed category hierarchy rows and reserved accounting/service exclusions from commerce products, and assigns every product an explicit structural `category_code`.
- `GET /api/categories` and an accessible Products/Categories segmented view expose the hierarchy through the existing filterable, resizable, RTL-aware data grid.

### Changed

- The verified live Patris classification now yields 73 categories, 8 non-merchandise exclusions, and 921 commerce leaves from 1,002 source rows; ambiguous or contradictory records fail closed into quarantine.
- Category, exclusion, and product-category changes now participate in record/source/event identities, and category-only edits trigger outbound updates even when no product row changed.

### Fixed

- Valid product leaves in filtered extracts no longer require their parent rows to be present, while zero stock alone is no longer mistaken for a category signal.

## [1.1.0] - 2026-07-17

### Added

- Branded NSIS MUI2 Windows installers with current-user/all-users modes, runtime and optional C SDK components, preserved configuration by default, explicit purge uninstall, localized wizard support, silent deployment smoke tests, and release checksum/asset wiring.
- Optional Windows-only `alm_compat` build variants with deterministic AHK_ALM-compatible UTF-8 hardware challenges, per-user key management, read-only legacy adjacent-key discovery, CLI management commands, stable C ABI management symbols, fail-closed engine creation, and variant manifests.
- Stable C ABI version and capabilities queries describing RPC methods, transports, string ownership, threading, process-global settings, and licensing mode.
- A `POST /api/refresh` endpoint matching the existing WebSocket, IPC, and embedded-library manual-refresh command.

### Fixed

- Embedded engine handles now serialize calls, reject calls once close begins, wait for in-flight work, stop HTTP/IPC transports deterministically, contain recoverable panics at every C entry point, and clear stale ABI errors after successful calls.
- Installed TOML/YAML/JSON configuration names are discovered in the per-user configuration directory, and IPC endpoint binding failures are reported synchronously.
- Windows release verification now loads and exercises the published DLL ABI against the real database fixture before an installer can be published.

## [1.0.1] - 2026-07-17

### Fixed

- Web UI row-count logs now expand to show added and deleted row snapshots plus field-level before/after values for modified records.
- Event-log disclosures now include English and Persian labels, RTL-aware presentation, accessible native controls, and focus/open-state preservation during live updates.
- Row updates now use the change set's declared key field consistently in both the live table and detailed event log.
- Detailed log persistence is bounded, rejects malformed stored data safely, and remains compatible with existing count-only entries.
- File watcher callback claims are deduplicated atomically; debounce timers are synchronized; and unwatch/close cancel pending work and future claims while allowing already-claimed callbacks to finish safely.
- Makefile, build-helper documentation, and the Linux source installer now derive their default build version from the canonical Go version source.

## [1.0.0] - 2026-07-17

### Added

- Native Paradox/BDE database reading through pxlib with safe temporary-copy handling, integrity checks, process/lock visibility, and Patris81 Persian character conversion.
- JSON, CSV, canonical Excel, SQLite, MariaDB/MySQL, REST, RPC-style command, and webhook/update delivery outputs driven by one transformation pipeline.
- Product-sync delivery with landed-price calculation, currency and import-freight catalog lookup, product pricing assignments, raw-field rejection, hashes, replay safety, retries, and deferred reconciliation status.
- Real-time REST, WebSocket, polling, manual refresh, edge-upload, embedded-library, local IPC, self-update, and standalone operation modes.
- Responsive web viewer and terminal dashboard with RTL/LTR support, mapping/configuration tools, filters, sorting, column resizing and visibility, row selection, context actions, conditional icons, export controls, notifications, and accessible keyboard interactions.
- Persistent layered configuration, environment-variable secret injection, configurable system notifications, and source/update manifest endpoints.
- Linux, native Windows, and independently verified Windows cross-build automation with source-built pxlib, embedded Windows version resources, tests, artifact metadata, and GitHub Actions summaries.

### Changed

- Canonical product data is transformed before outbound delivery; raw Patris fields remain confined to source/debug paths.
- SQL synchronization now supports bounded batches, dry runs, safe reconciliation policies, protected/quarantined keys, exact decimals, and fail-closed transaction handling.
- Pricing assignment prefetching is atomic, scoped, cache-aware, and tolerant of configured production latency.
- Viewer data-grid behavior and canonical Excel output now share consistent transformed values and export semantics.

### Fixed

- Webhook change delivery now emits the intended changed records without duplicating the transformation path.
- Windows CGO builds consistently include pxlib and MinGW runtime dependencies.
- Canonical XLSX export, product reconciliation feedback, RTL mixed text, table scrolling, focus management, and persisted UI state were hardened through regression tests.
