# Raw records and pricing collections

This is the focused source successor to PR #239. The offline documentation portal, logo migration and release packaging remain separate work; the existing build/test workflows run this slice unchanged.

- GET /api/records returns minimally transformed source rows as an ordered JSON array. Duplicate identifiers, missing Code and unknown fields survive. It does not require a KALA profile or pricing inputs.
- GET /api/products returns the configured KALA projection; GET /api/categories returns its hierarchy. Generic sources expose only records. JSON, CSV, XLSX and the existing trusted XLSM/blank XLTM download contracts retain their distinct roles.
- GET /api/app declares records, products, categories and record_hashes capabilities. The viewer selects the advertised collection; a failed request never silently switches models. Missing optional product capability selects raw records.
- The viewer reuses WebSocket rows only when their raw/canonical projection matches its selected HTTP collection. Mismatched row events and source changes trigger a coalesced collection reload, including a final read when events arrive during a fetch. The configured external stream is unchanged; mismatched projections require a full HTTP reload instead of incremental row updates.
- Optional provider metadata round-trips through canonical typed JSON. Source.SameIdentity compares owner, dataset and revision; metadata is excluded from snapshot cache and idempotency keys. A changed owner revision still prevents stale reuse.
- canonical.hashes.enabled and canonical.hashes.expose control optional hashes on read surfaces. Raw records and products remain available without them. The existing revision-dependent replication and mutation operations still require the identity evidence needed to prevent unsafe writes; this slice does not loosen destination commit guards.
- Browser configuration hides and preserves server-owned credentials, protected Office paths and remote URL secrets. It does not introduce new ingress authentication policies.

The existing authenticated POST /api/refresh with delivery=wait remains the sync path. Its terminal receiver receipt is required before reporting delivered=true. This slice adds no refresh endpoint and does not choose a PHP/Go pricing authority.

A JSON body with `{"delivery":"wait"}` explicitly requests synchronous delivery and requires the existing loopback companion client/session headers; missing headers return `403 local_session_required` instead of asynchronous success. Requests without a body retain ordinary refresh behavior. After authentication, delivery validation and admission, a wait request invalidates the cached projection and owner catalog/assignment provider once, reads a fresh source snapshot with batched owner inputs, then pins that envelope through delivery and its terminal receipt. This refreshes observed inputs; it does not make source and owner reads a cross-system transaction.

Validation: canonical/appconfig/recordpipe/server tests, semantic identity/cache/idempotency regressions, viewer capability/export tests and web build. Tests use synthetic data and the repository's Paradox fixture. No production refresh, service restart, workbook promotion or price delivery is performed by these checks.
