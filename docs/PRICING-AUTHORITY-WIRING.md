# Selected pricing authority

The existing authenticated `POST /api/refresh` with `{"delivery":"wait"}` selects the owner authority before dispatch. `go` publishes the existing calculated envelope. `php` sends normalized input facts without a Go final price, waits for the exact input delivery receipt, then collects the verified PHP final projection. It sends one input event; snapshot discovery never redelivers it.

`GET /api/products` and its downloads consume the selected final projection. PHP rows come from the existing pricing snapshot response's `canonical_product`, not from a lossy report-price overlay. Shared WebSocket input events trigger an HTTP reload for PHP authority; failed final reads clear old viewer prices. `/api/records` remains the raw source adapter. Product-sync remains the input replication boundary.

## Owner and identity checks

- The remote `/integration/catalog` supplies exact `pricing.authority` values `php` or `go`, bound to its existing owner catalog revision. Missing, invalid or stale selection fails closed for configured server pricing. Static standalone inputs explicitly use `StaticConfig.Authority`; standalone source conversion with no pricing inputs has no price publisher.
- PHP discovery queries the existing authenticated `/wp-json/digitalogic/pricing/sync/revision` using the delivered input source. The response must contain matching `input_source`, a valid final `source` in the same owner/dataset, and matching `owner_catalog_revision`. The report projection's existing `catalog_revision` retains its separate meaning.
- Collection pins the discovered final source for all existing build, snapshot, page, reconciliation, terminal and URL checks. It retains owner snapshot digests and normalizes known canonical numeric strings lexically before product decoding, without a float conversion step.
- Each canonical product must match its report `patris_code`, owner catalog revision, existing record hash, and complete expected source product set. The final source hash is recomputed only for verification against the owner value. Incoming source/event identities remain the delivery acknowledgment; completion `source_revision` identifies the final PHP projection.
- Source, configuration and owner invalidations clear both input and final-projection caches. Wait uses its pinned input for final collection; it does not reread source rows during delivery.
- Go and PHP selected calculation inputs share the same non-negative decimal admission: at most 15 integer and 12 fractional digits, without exponent notation. Raw/report decimals retain their original domain; unsupported selected inputs fail closed rather than producing an engine-specific price.

## Scope and validation

The existing authenticated owner event stream queues one pricing worker. It reads the actual owner catalog revision, coalesces concurrent changes, and shares the delivery permit with synchronous refresh and source delivery. Go authority calculates and dispatches one pinned input event; report revision feedback cannot trigger a second writer. PHP authority refreshes the exact final source named by the authenticated event without resending input.

The config-adjacent `.pricing.json` checkpoint contains bounded operation status and source/event/revision identities, with no products or credentials. `/api/status` exposes this under `pricing`; WebSocket `pricing_progress` messages report transitions. `dispatching` is persisted before transmission. Only an exact successful receipt with every pending/deferred count zero becomes `complete`. Same-owner recovery reads the authenticated `/revision.delivery` ledger: absent or mismatched evidence remains `recovery_required` and never authorizes a resend. A newer manual receipt can be adopted only when its owner and source equal freshly built desired input. A committed new owner revision starts a distinct operation after validating fresh input; receiver owner fences reject old-owner writes. One `previous_operation` retains the superseded outcome, so old pending/deferred work cannot permanently block new prices. Checkpoint write failure rejects event acknowledgement. Read-only deployments must provide a writable config directory to enable owner actuation.

Successful configured wait and owner actuation require the receiver's actual nested `data.delivery` identities. The top-level replay acknowledgment can name the old request while its receipt names a newer accepted event; request source/owner values are never substituted as completion proof.

This remains a draft integration, not a deployed release. Static/offline catalog support does not authorize a separate local catalog to overwrite WordPress owner prices. Combined PHP/Go operational acceptance has not been performed.

Tests cover both authorities through the refresh and product-read path, one dispatch, missing authority/binding, stale viewer clearing, exact high-precision numeric-string transport, forged hashes, cache bindings, and existing snapshot ready/async terminal behavior. Existing full server/canonical/recordpipe/provider suites, vet, and web tests/build are required before review.
