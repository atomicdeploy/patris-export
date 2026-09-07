# Authority integration status

This branch stages provider parsing and strict PHP final-snapshot discovery. It does not activate engine selection or claim that final publication follows the selected authority yet.

The remote owner's existing `/integration/catalog` document supplies `pricing.authority` as exactly `php` or `go`. The provider carries its validated value in `Resolution.Authority`, alongside the unchanged owner catalog revision. Missing/null or invalid values produce an empty authority and a bounded `AuthorityError`; there is no implicit engine. Local static inputs use `StaticConfig.Authority`, which participates in the existing derived catalog revision. These internal diagnostics do not alter product warnings or hashes before final projection routing is wired.

## Existing consumer to reuse

`pkg/server/excel_pricing_remote_snapshot.go` already implements an authenticated, bounded, integrity-checked PHP final snapshot client. `excelPricingRemoteSnapshotClient.Collect` uses the existing `/wp-json/digitalogic/pricing/sync/revision`, `/snapshots`, and `/builds/` routes. It checks source identity, owner/catalog/state revisions, pagination, page digests, and terminal receipts. Its `Rows` are canonical snapshot rows; `ProjectedRows` is the Excel-specific presentation.

`collectExcelPricingSnapshot` in `excel_pricing_snapshot.go` wraps that client but automatically redelivers source data on a source conflict. Final authority projection should reuse the client directly, not that conflict-repair wrapper, to avoid recursive publication.

## Remaining boundary

PHP retains `input_source`/`input_products` as the accepted upstream baseline, and derives `source`/`products` for the final projection. Delivery receipts still acknowledge the original input event and source revision. The PHP final revision can therefore differ from the delivered input revision without any conflict.

The client now supports internal `InputCatalogRevision` selection for PHP final-projection discovery. Its authenticated revision request uses the delivered input source. The response must carry `input_source` matching that complete identity, `source` with the same owner/dataset and a valid final revision, and the expected `owner_catalog_revision` (distinct from the report projection's existing `catalog_revision`). Missing binding fails even when the final and input revisions happen to match. Collection then pins the returned final source in a per-run client copy and retains all existing exact build, snapshot, reconciliation, terminal and URL checks. No auto-delivery occurs. Do not treat the input delivery receipt as proof of final snapshot identity, replace hashes, relax all identity checks, or add a second refresh endpoint.

The remaining selector must publish Go calculation only in Go mode; PHP mode supplies normalized input facts and consumes the PHP final projection through this client. Missing/invalid authority must block selected-engine publication. These internal components alone do not enforce that actuation boundary.

Validation: provider authority cases and static JSON/revision round-trip; pricingcatalog, canonical, recordpipe and appconfig suites; PHP snapshot binding and existing remote snapshot tests, including ready and async terminal delivery; missing binding and wrong input/final/catalog identity rejection. No production operations.
