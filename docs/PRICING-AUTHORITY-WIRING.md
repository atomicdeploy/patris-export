# Selected pricing authority

The existing authenticated `POST /api/refresh` with `{"delivery":"wait"}` selects the owner authority before dispatch. `go` publishes the existing calculated envelope. `php` sends normalized input facts without a Go final price, waits for the exact input delivery receipt, then collects the verified PHP final projection. It sends one input event; snapshot discovery never redelivers it.

`GET /api/products` and its downloads consume the selected final projection. PHP rows come from the existing pricing snapshot response's `canonical_product`, not from a lossy report-price overlay. Shared WebSocket input events trigger an HTTP reload for PHP authority; failed final reads clear old viewer prices. `/api/records` remains the raw source adapter. Product-sync remains the input replication boundary.

## Owner and identity checks

- The remote `/integration/catalog` supplies exact `pricing.authority` values `php` or `go`, bound to its existing owner catalog revision. Missing, invalid or stale selection fails closed for configured server pricing. Static standalone inputs explicitly use `StaticConfig.Authority`; standalone source conversion with no pricing inputs has no price publisher.
- PHP discovery queries the existing authenticated `/wp-json/digitalogic/pricing/sync/revision` using the delivered input source. The response must contain matching `input_source`, a valid final `source` in the same owner/dataset, and matching `owner_catalog_revision`. The report projection's existing `catalog_revision` retains its separate meaning.
- Collection pins the discovered final source for all existing build, snapshot, page, reconciliation, terminal and URL checks. It retains owner snapshot digests and normalizes known canonical numeric strings lexically before product decoding, without a float conversion step.
- Each canonical product must match its report `patris_code`, owner catalog revision, existing record hash, and complete expected source product set. The final source hash is recomputed only for verification against the owner value. Incoming source/event identities remain the delivery acknowledgment; completion `source_revision` identifies the final PHP projection.
- Source, configuration and owner invalidations clear both input and final-projection caches. Wait uses its pinned input for final collection; it does not reread source rows during delivery.

## Scope and validation

This is a draft integration, not a deployed release. Owner-setting actuation in Go-authority deployments remains fail-closed on the PHP side until its dispatcher is wired. Static/offline catalog support does not authorize a separate local catalog to overwrite WordPress owner prices. No production acceptance has been performed.

Tests cover both authorities through the refresh and product-read path, one dispatch, missing authority/binding, stale viewer clearing, exact high-precision numeric-string transport, forged hashes, cache bindings, and existing snapshot ready/async terminal behavior. Existing full server/canonical/recordpipe/provider suites, vet, and web tests/build are required before review.
