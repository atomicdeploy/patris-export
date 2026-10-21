# API compatibility boundary audit

This audit supports issue #240. It identifies every current HTTP route and the
source locations that declare a schema identity, `schema_version`, strict JSON
decoder, or explicit version/schema mismatch guard. The deterministic
machine-readable inventory is
[`api-compatibility-audit.json`](api-compatibility-audit.json).

Run:

```console
node scripts/check-api-compatibility.cjs --check
node --test scripts/check-api-compatibility.test.cjs
```

After an intentional compatibility change, review the diff produced by:

```console
node scripts/check-api-compatibility.cjs --write
```

The inventory contains only repository source locations and excerpts. It does
not inspect runtime configuration, credentials, customer databases, request
payloads, or logs.

## Governing rule

`/api/records`, `/api/products`, and `/api/categories` are general collection
boundaries. They remain unversioned and must evolve through capability
discovery, content negotiation, optional fields, sparse omission, preserved
extension members, and actionable partial or incompatibility errors. A client
must not require a `schema_version` tied to one producer build before it can
read these collections.

Representation suffixes such as `/api/records.csv` and
`/api/records.xlsx` are aliases for negotiated formats, not protocol versions.
They may coexist with `Accept` and query negotiation while clients migrate.

The `/api/products` boundary is being introduced by draft PR #239. This
independent audit does not duplicate or alter that branch.

## Boundary decisions

| Boundary | Current evidence | Decision |
| --- | --- | --- |
| General records, products, categories | HTTP route inventory and format aliases | Keep unversioned. Negotiate capabilities and representations. Preserve unknown extension fields and sparse omission. |
| `patris.product-sync` | `pkg/canonical`, `pkg/updateout`, delivery adapter examples | Retain the schema identity only while atomic replication, tombstones, source/event identity, retry state, and acknowledgement semantics remain unique. Do not add `schema_version`. Strict extension handling is migrated by #239. |
| Excel pricing companion and Digitalogic sync | `pkg/server/excel_pricing.go`, `ProductCatalogSync.bas`, `docs/EXCEL-PRICING-SYNC.md` | Existing `/v1` schema identities are a live multi-consumer boundary. Migrate producer and workbook/remote consumers together to declared capabilities and optional fields; do not delete or loosen one side independently. |
| Digitalogic pricing catalog | `pkg/pricingcatalog` | Exact schema checks and strict decoders protect a live upstream integration today. Replace them only with an upstream-coordinated capability and partial-result contract. Presence/event-mesh assets are outside this issue. |
| Recent-sales aggregate | `pkg/recentsales` | Retain the explicit schema/version as a privacy-minimized aggregate contract until its authenticated consumers negotiate a replacement. It is not a general records endpoint. |
| Application configuration | `pkg/appconfig`, `pkg/embedded` | Retain the persisted/embedded configuration version as a migration boundary. It does not constrain API collection fields. |
| Scheduled-task process state | `scripts/windows/*ScheduledTask*` | Retain versions 1 and 2 as an OS process-state migration boundary. This is not an HTTP data model. |
| JavaScript native bridge ABI | `integrations/javascript/src/native-worker.cjs` | Retain the numeric ABI guard because incompatible native call layouts cannot be made safe by ignoring fields. |
| SQL operation request decoding | `pkg/server/sql_operations.go` | Retain strict decoding as an operator-input safety boundary. Report unknown inputs clearly; do not treat it as a collection schema version. |
| External gRPC gateway example | `docs/examples/patris-product-sync.proto` | `/v1/patris:apply` is an explicit example gateway boundary, not a Patris Export HTTP route. Keep its evolution policy with the protobuf consumer. |

## Migration sequence

1. Land #239's unversioned raw records, KALA products, structural categories,
   and extension-preserving canonical types after explicit owner approval.
2. Add machine-readable capability discovery without requiring one closed
   producer version.
3. Migrate list, search, viewer, export, and workbook reads to
   records/products/categories and negotiated representations.
4. Migrate Excel pricing and Digitalogic catalog exchanges only alongside
   their consumers; errors must identify missing capabilities or partial data.
5. Keep product-sync only for replication semantics that no general collection
   or future change stream provides. Deprecation requires receiver evidence and
   explicit owner approval.

The audit check fails when a general collection is placed under `/api/vN`, when
`schema_version` is added to product-sync replication code, or when the
committed route/guard inventory drifts without review.
