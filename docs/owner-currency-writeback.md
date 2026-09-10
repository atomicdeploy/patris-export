# Shared settings owner: Go service integration

All seven ordinary editable pricing settings use the same PHP owner admission: `POST /wp-json/digitalogic/v1/pricing/settings`. Go forwards explicit changed fields, the caller request identity and expected owner revision. WordPress/Laravel share the PHP implementation; this Go-to-owner API does not introduce another PHP engine.

Supported fields: `yuan_price`, `dollar_price`, `cny_effective_date`, `usd_effective_date`, `profit_margin_percent`, `air_express_price_per_kg`, and `price_rounding_digits`. Currency dates are chosen by the owner at admission unless explicitly overridden. Owner-derived settings are read back, not independently reconstructed by Go.

The local API is `/api/pricing-sync/session` followed by `/api/pricing-sync/writebacks`. Existing local session checks apply. A batch lists only fields actually changed; unchanged fields are rejected before owner work. The queue observes the owner request and independently reads canonical settings before reporting `confirmed`. Ordinary settings do not use preview/apply/ACK.

## Uncertain outcomes and recovery

A durable intent is saved before submission. Request identity is preserved across queue restart. `observation_required` means the delivery outcome is unknown; it does not authorize resubmission or restoration of an older value. The explicit `/writebacks/{job_id}/observe` action observes the same owner identity and never submits a replacement write.

Historical owner completion is distinct from current values. `owner_terminal` carries the historical outcome without projecting stale settings. Authenticated `GET /writebacks/{job_id}/reconcile` reads current owner settings without mutation, refresh or snapshot construction. Existing transaction IDs can still enter explicit ACK recovery; this is not a new ordinary settings route. Remaining standalone preview/apply surfaces require separate retirement review.

The intent journal records operation identity, not product snapshots or a price rollback system. Optional snapshot/revision features remain deferred. Native consumer recovery must preserve newer user edits and must not manufacture an automatic retry.

## Persistent Windows deployment access

The managed task is `\AtomicDeploy\Patris Export Viewer`. Its launcher imports the existing dedicated `DIGITALOGIC_PRICING_WRITE_KEY` and `DIGITALOGIC_PRICING_WRITE_SECRET` environment variables on each start. `scripts/windows/Enable-CurrencyOwnerTaskEnvironment.ps1 -Apply` configures those imports persistently and verifies the task action without exposing credentials or restarting the service.

The authorized existing SSH connection to localhost was verified as the task user with elevated access. Use it for managed task updates when the interactive process is not elevated; do not infer an access blocker solely from the interactive token. This does not require creating another authentication mechanism. Use the existing `Install-PatrisExportScheduledTask.ps1` Stop/Start operations, verify no active pricing operation, and verify the installed binary and service readiness after replacement.

## Production evidence, 10 September 2026

Installed runtime source: `9afd3da7a4f9e85dbbed0e74b2cc537b033879a5`.
Windows binary SHA256: `C907A05A22465D5CC595FCF4B446B98145C0A300A32FE881149A7A4356297273`.

A real request through the installed local Go API changed CNY 35500 to 35600. Job `88af2e1ed042047a6876f0d98d1690bd` confirmed in 32718 ms. Restore job `c68189caf416c5c8dc74b466a178c28f` confirmed in approximately 31282 ms. All original settings were restored. These are workload observations, not a universal 60-second requirement.

Independent arithmetic and stock checks passed for all 1022 mapped products at both rates: 903 priced and 119 unpriced. Missing weight suppresses final price but does not suppress positive stock. This covers persisted product data; it is not proof of every native interface or rendered browser state. After restoration the service was idle, no work was pending, and pricing events were connected.

Earlier isolated live probes verified durable queue reconstruction, observation and current readback without mutation. The production service was installed through the existing scheduled task and independently checked. Native workbook import/execution and a production restart during an unresolved request remain unverified.

## Remaining work

- Native Excel acceptance of `docs/examples/vba/ProductCatalogSync.bas`, including newer-edit preservation and observation/reconciliation UI.
- All-domain truthful progress architecture and implementation: https://github.com/atomicdeploy/digitalogic-wp/issues/325.
- Exact evidence for remaining Patris mappings: https://github.com/atomicdeploy/digitalogic-wp/issues/314.
- Full cross-interface acceptance, configurable deferred features, and remaining branch integration. None is implied complete by this installed-service milestone.
