# Living Integration Standard

Patris Export and others share one current integration standard while all
producers, receivers, and deployments remain under coordinated control.

## Rules

- `schema` identifies the current document kind.
- Producers and consumers are deployed as a coordinated change when the shape
  evolves.
- Unknown fields and obsolete aliases fail closed. There are no compatibility
  branches for earlier payload shapes.
- Missing source or reference data omits the corresponding key.
- JSON `null` means the upstream source or reference explicitly supplied
  `null`. Code must never manufacture `null` to mean missing, unavailable,
  invalid, unparseable, or not calculated.
- Empty strings, empty objects, empty arrays, zero, and `false` remain distinct
  values when explicitly supplied or required by the current shape.
- Exact decimal text is preserved at integration boundaries. Derived
  `final_price` is omitted unless every required input is valid.
- `event_id`, source/content revisions, record hashes, tombstones, quarantine,
  warnings, category hierarchy, and exclusion projections remain part of the
  current standard because they provide identity, safety, and diagnostics
  without compatibility negotiation.

## Change process

An integration change updates producer code, all configured receivers, fixtures,
tests, endpoint documentation, and deployment configuration together. The
cutover sends a full snapshot after resetting receiver state when event identity
or payload meaning changes. Software release identifiers and internal database
migration counters remain packaging and deployment mechanics, outside the wire
contract.
