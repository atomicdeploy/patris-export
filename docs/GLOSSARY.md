# Glossary

These definitions use Patris Export's meaning. Link to the relevant heading
when an advanced term appears in another guide or generated description.

## ACL

An access-control list: rules that decide whether a
[principal](#principal) may perform an action on a dataset or service surface.
The unified ACL described in the roadmap is not implemented yet.

## Canonical

A known semantic projection used consistently within one integration boundary.
In Patris Export, the built-in `kala` projection assigns meanings such as
`product_code`, separates products/categories, and tracks ambiguity. Canonical
does not mean every Paradox table or external platform must use that model.

## Collection

A set or ordered sequence of records returned for reading/export. Collections
are appropriate for listing, searching, analysis, and snapshot export. They do
not necessarily contain the replication state carried by an [envelope](#envelope).

## Contract

An agreed behavior at a boundary: fields, types, errors, side effects, and
compatibility rules. A contract may be flexible and extension-tolerant; it does
not require every implementation to serialize every optional field. In this
project, “product-sync contract” specifically names the existing atomic
replication compatibility envelope.

## Dataset

One logical source table or, in future directory mode, one named table within a
source directory. Today a running Patris Export server has one active database
dataset.

## Deterministic identity

An identity calculated so the same represented inputs produce the same result.
Patris Export uses this property for optional hashes and idempotency. It does
not require consumers to reject unknown extensions or bind every API to one
closed version.

## Envelope

An object that wraps data with event/source metadata. `/api/product-sync` uses
an envelope because a replica needs products, categories, exclusions,
quarantine, deletions, and identities together. `/api/products` is the simpler
collection boundary.

## Extension field

An unknown member added by a newer producer or a deployment-specific module.
Extensible decoders retain such members during decode/re-encode. Consumers may
ignore fields they do not understand but should not silently destroy them when
acting as a proxy.

## Idempotency

The property that retrying the same accepted event does not apply it twice.
Existing product-sync receivers can key this behavior by `event_id`.

## Keyed

Represented as an object/map whose property names are record identifiers, for
example `{ "113001001": { ... } }`. Keying is convenient for lookup but can
collapse duplicates and cannot naturally represent a row with no key.
`/api/records` therefore returns an ordered array; `/api/products` JSON is keyed
by the already validated unique `product_code`.

## Materialize

Construct an in-memory or serialized representation from source data—for
example, turning a typed product into a sparse JSON row or building an XLSX
file. Materialization may perform work and allocate storage; it is not merely a
name for reading bytes.

## Model

The application representation of meaningful data and its relationships. A
model may be typed (`Product`, `Category`) or dynamic (raw field/value maps).
It is distinct from the physical Paradox schema and from a translated label.

## Principal

An authenticated caller identity, such as a local OS user or API-key owner.
The planned unified authentication layer will resolve callers to principals
before evaluating an [ACL](#acl).

## Profile

A configured dataset-specific semantic adapter. The built-in `kala` profile
classifies rows and projects products/categories. An unprofiled table remains
readable through raw records.

## Projection

A view derived from source data for a purpose. `/api/products` is a semantic
projection of eligible `kala.db` rows; it is not a byte-for-byte raw table
dump.

## Quarantine

Withholding an ambiguous record from downstream mutation while preserving
evidence that it needs review. Duplicate/conflicting product codes are
quarantined so a receiver does not mistake ambiguity for deletion.

## Raw

Source rows before character conversion, ANBAR compaction, keying, RTL,
configured mapping, pricing, or semantic profile transformation. Patris Export
still removes internal `Sort*` fields from its raw transport.

## Reconcile

Compare a source collection with a destination and decide what to do with rows
missing from the source. `upsert_only` does not remove them;
`soft_delete_missing` marks them after an exact preview; `delete_missing`
physically removes them.

## Replication adapter

A boundary that helps one stateful system mirror another, including change
identity, deletions, and ambiguity—not merely a list endpoint.
`/api/product-sync` is retained as such a compatibility adapter.

## Revision

An identity for one represented state. The product-sync source revision is
derived from product/category record identities plus exclusion/quarantine
state. It is different from the SHA-256 of the source `.db` bytes.

## Schema

A description of known fields, types, relationships, and allowed shapes. A
schema can allow additional properties and optional fields. A physical Paradox
schema describes table columns; OpenAPI/AsyncAPI schemas describe network
messages.

## Source dependent

Behavior whose meaning depends on the active dataset or profile. For example,
`/api/products` is available for a configured `kala` source, while
`/api/records` works for any readable Paradox source.

## Source identity

The stable label and dataset name attached to an event. It tells receivers
which producer/dataset a revision belongs to; it is not authentication.

## Sparse semantics

Encoding only facts that exist. Missing means unseen/unavailable; explicit
`null` means the source explicitly supplied null. Patris Export avoids adding
empty integration keys merely to fill a broad schema.

## Tombstone

An explicit deletion marker, such as a `product_code` with `deleted: true`.
Tombstones let a replica remove state without confusing a filtered,
quarantined, or temporarily unavailable record with deletion.

## Transform

An operation that changes representation or meaning, such as character
conversion, field renaming, value mapping, numeric adjustment, product
classification, or pricing projection. Raw record access avoids these
significant transformations.
