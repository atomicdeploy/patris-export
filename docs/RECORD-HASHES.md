# Record hashes

Patris Export can attach SHA-256 identities to projected products/categories
and build source/event identities from them. They support change detection and
the existing product-sync replication workflow. They are optional and are not
part of the raw Paradox data.

## What a hash is—and is not

A record hash is:

- a compact identity for the known sparse JSON representation of a projected
  record;
- useful for comparing revisions, identifying an event, and avoiding a
  needless downstream write;
- formatted as `sha256:<lowercase-hex>`.

A record hash is **not**:

- authentication;
- a digital signature;
- proof of who created a record;
- encryption;
- a substitute for TLS, an API key, OS authentication, or an ACL;
- guaranteed to remain identical if the materialized model or presence of an
  extension field changes.

## Product and category hashes

For a product, Patris Export:

1. copies the projected product;
2. clears its own `record_hash`;
3. serializes the sparse JSON representation, including explicit nulls and
   retained extension members;
4. hashes those bytes with SHA-256.

Categories use the equivalent process. Because missing and explicit-null fields
are different JSON, they can produce different identities.

## Source revision

The product-sync source revision hashes sorted identity material:

- `product_code=record_hash`;
- `category:<category_code>=record_hash`;
- excluded codes;
- quarantined codes.

It represents the projected catalog state, not the byte hash of the original
`.db` file. `/api/source/file` has its own file SHA-256/ETag.

## Event ID

The product-sync event ID hashes the relevant event shape, including:

- schema and event type;
- optional pricing metadata;
- source identity and revision;
- generated time;
- product/category hash sets;
- exclusions, deletions, quarantine, and warnings.

Existing receivers can use it as an idempotency key. A general collection
consumer does not need an event ID.

## Controls

Configuration:

```yaml
canonical:
  hashes:
    enabled: true
    expose: false
```

CLI:

```bash
patris-export --record-hashes=false serve kala.db
patris-export --expose-record-hashes=false convert kala.db --format json
```

Environment:

```text
PATRIS_EXPORT_RECORD_HASHES=false
PATRIS_EXPORT_EXPOSE_RECORD_HASHES=false
```

Collection query:

```text
GET /api/products?include_hashes=false
GET /api/categories?include_hashes=0
```

The query can only hide hashes. `include_hashes=true` cannot override a
server-side `expose: false` or `enabled: false`.

## Behavior matrix

| `enabled` | `expose` | Products/categories and ordinary exports | `/api/product-sync` |
| --- | --- | --- | --- |
| `true` | `true` | `record_hash` may be present; request may hide it | Available for a profiled dataset |
| `true` | `false` | `record_hash` omitted | Available; compatibility identities remain in its envelope |
| `false` | any | `record_hash` omitted | Unavailable (`404`) |

When `enabled` is false, normalization forces `expose` false. `/api/records`
never needs projected record hashes because it is the raw row boundary.

Disabling hashes does not disable:

- `/api/records`;
- `/api/products` or `/api/categories`;
- JSON, CSV, XLSX, SQLite, or MySQL collection export;
- the built-in viewer;
- ordinary watcher/change comparison.

It does disable publication of the hash-dependent product-sync compatibility
contract and its outbound sync envelope. Configure a non-contract collection
delivery if a receiver does not require those identities.

## Hiding versus disabling

Choose **hide** (`enabled: true`, `expose: false`) when:

- an existing receiver still uses `/api/product-sync`;
- operators do not want identity columns in the viewer, CSV, XLSX, or SQL
  table;
- hashes are implementation metadata for the deployment.

Choose **disable** when:

- no receiver uses the compatibility contract;
- the deployment deliberately wants flexible collection-only output;
- revision/event identities provide no value for that workflow.

## Compatibility and extensions

Adding, removing, or changing a retained extension member can change the record
hash even when an older consumer ignores that member. That is expected: the
record's complete represented state changed.

Consumers must not treat a hash mismatch as a parsing error. They should parse
the fields they understand, retain extensions when forwarding, and use the hash
only for change/idempotency behavior they explicitly support.

## Troubleshooting

- **`record_hash` is missing:** check `canonical.hashes.expose`, the two
  environment/CLI controls, and `include_hashes=false`.
- **`/api/product-sync` returns 404:** confirm a canonical profile applies and
  `canonical.hashes.enabled` is true. `/api/products` can still work.
- **Hash changed after a software update:** compare sparse field presence,
  mapping/profile rules, pricing values, and retained extensions.
- **Need a source-file checksum:** use `/api/source/manifest` or the response
  headers from `/api/source/file`; a product revision is a different identity.
