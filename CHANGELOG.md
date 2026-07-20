# Changelog

All notable changes to Patris Export are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Canonical JSON, CSV, HTTP, spreadsheet, SQL, and outbound-update rows now share a sparse field boundary: never-received values are omitted while explicitly received nulls remain null.
- Shipping integration output uses `shipping_method_id` and `shipping_price_per_kg_cny`; legacy freight-named catalog and assignment keys remain accepted as input during migration.

### Fixed

- Standalone exports no longer contain Digitalogic pricing, formula, or shipping-method fields and warnings when no pricing integration is configured.

## [1.2.0] - 2026-07-17

### Added

- Canonical catalog contract v1.1 separates typed, hashed category hierarchy rows and reserved accounting/service exclusions from commerce products, and assigns every product an explicit structural `category_code`.
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
- Versioned Digitalogic product-sync contracts with landed-price calculation, currency and import-freight catalog lookup, product pricing assignments, raw-field rejection, hashes, replay safety, retries, and deferred reconciliation status.
- Real-time REST, WebSocket, polling, manual refresh, edge-upload, embedded-library, local IPC, self-update, and standalone operation modes.
- Responsive web viewer and terminal dashboard with RTL/LTR support, mapping/configuration tools, filters, sorting, column resizing and visibility, row selection, context actions, conditional icons, export controls, notifications, and accessible keyboard interactions.
- Persistent layered configuration, environment-variable secret injection, configurable system notifications, and source/update manifest endpoints.
- Linux, native Windows, and independently verified Windows cross-build automation with source-built pxlib, embedded Windows version resources, tests, artifact metadata, and GitHub Actions summaries.

### Changed

- Canonical product data is transformed before Digitalogic receives it; raw Patris fields remain confined to source/debug paths.
- SQL synchronization now supports bounded batches, dry runs, safe reconciliation policies, protected/quarantined keys, exact decimals, and fail-closed transaction handling.
- Pricing assignment prefetching is atomic, scoped, cache-aware, and tolerant of configured production latency.
- Viewer data-grid behavior and canonical Excel output now share consistent transformed values and export semantics.

### Fixed

- Webhook change delivery now emits the intended changed records without duplicating the transformation path.
- Windows CGO builds consistently include pxlib and MinGW runtime dependencies.
- Canonical XLSX export, product reconciliation feedback, RTL mixed text, table scrolling, focus management, and persisted UI state were hardened through regression tests.
