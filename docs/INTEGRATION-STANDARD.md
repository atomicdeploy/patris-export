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
- A usable price source is a strictly positive amount. Explicit zero remains a
  source fact and is never confused with omission or null, but it is not
  commercially determined and cannot populate the selected-source trio.
- `price_source_amount`, `price_source_currency`, and `price_source_kind` are
  all present or all omitted. CNY foreign price is preferred only with
  strictly positive weight and a complete enabled non-domestic shipment route.
  The positive first `Sharh1` slot (`partner_price_source`) is the IRR
  margin-only fallback and always uses `domestic` shipping at zero IRR/kg.
  Positive Patris `FOROSH` (`sale_price_source`) may be used without markup or
  rounding only through the default-off `use_sale_price_direct_fallback`
  policy.
- `price_rounding_digits` is 0 through 9 and
  `price_rounding_mode` is `nearest_half_up`. Rounding happens exactly once,
  after markup, to the nearest `10^digits` IRT for foreign and partner routes.
  The opt-in `sale_price_direct` route omits both fields and converts IRR to a
  whole IRT value exactly.
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
