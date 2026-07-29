# Datasets, mappings, and i18n

Patris Export distinguishes physical Paradox fields, configurable field
mappings, semantic dataset profiles, and human-facing labels. Keeping those
layers separate lets an unknown `.db` remain readable without pretending its
columns mean the same thing as `kala.db`.

## Data layers

| Layer | Example | Purpose |
| --- | --- | --- |
| Physical source field | `Code`, `Sharh1`, `ANBAR1` | Exact field read from pxlib |
| Generic mapping key | `product_code`, `description` | Configurable rename/value transform |
| Semantic profile field | `product_code`, `warehouse_stock`, `final_price` | Known `kala` meaning and typed relationships |
| Human label | `Product Code`, `کد کالا` | UI/XLSX presentation only |

Never rename the machine key merely to translate a header. A receiver should
rely on `product_code`; people see `Product Code` or `کد کالا`.

## Arbitrary Paradox schemas

Start with:

```bash
patris-export info other.db
patris-export serve other.db --addr 127.0.0.1:8080
curl http://127.0.0.1:8080/api/records
```

`/api/records` returns an ordered JSON array. It preserves row count/order,
duplicate identifiers, rows without `Code`, and source field names. Internal
`Sort*` fields are excluded. There is no requirement that the table be named
`kala.db` or have a product key.

If no semantic profile exists, `/api/products` and `/api/categories` return
`404`. That is an honest “not applicable,” not a failure to read the database.

## The `kala` profile

The default config maps `kala.db` to profile type `kala`:

```yaml
canonical:
  enabled: true
  profiles:
    kala.db:
      type: kala
```

The profile separates:

- category/header records;
- sellable products;
- known reserved accounting/service codes;
- duplicate product codes;
- ambiguous rows that require review.

Products are available at `/api/products`; structural records are available at
`/api/categories`. A product may reference a category using `category_code`.
Excluded and quarantined codes remain available in the product-sync
compatibility envelope.

Classification uses code shape, descendants, stock/price/description signals,
and reserved-code evidence. A category-wide title rule must not be treated as
proof that every exact product belongs to the same category; exact-product
overrides require per-product evidence.

## Generic transform mapping

Use a mapping when another table has useful fields but no built-in semantic
profile.

Standalone `mapping.json`:

```json
{
  "enabled": true,
  "key_field": "item_code",
  "fields": {
    "Code": "item_code",
    "Name": "name",
    "Serial": "part_number"
  },
  "values": {
    "Unit": {
      "AD": "each",
      "KG": "kilogram"
    }
  },
  "defaults": {
    "source_system": "patris-office"
  },
  "include": ["Code", "Name", "Serial", "Unit", "FOROSH"],
  "numeric": {
    "FOROSH": {
      "multiplier": 1,
      "round": 0
    }
  },
  "tables": {
    "inventory.db": {
      "fields": {
        "FOROSH": "sale_price"
      }
    },
    "*": {
      "drop": ["Sort1", "Sort2"]
    }
  }
}
```

Run:

```bash
patris-export --mapping ./mapping.json convert inventory.db --format json
```

Rules:

- `fields` renames keys;
- `values` maps exact string forms of source values;
- `defaults` adds a key only when the mapped row does not already contain it;
- `include` creates an allowlist before renaming;
- `drop` removes fields;
- `numeric` can multiply, add, and round numeric values;
- `tables` overlays rules by lower-cased source base name, with `*` fallback;
- `key_field` defaults to `Code` and follows a `Code` rename.

Mappings are data-only and reused by the non-raw generic conversion pipeline,
including file/SQL output and server components that request that projection.
The generalized `/api/records` route deliberately bypasses mapping, and the
current typed `kala` profile uses its own semantic projector rather than the
generic field map. The standalone file is JSON; the same structure embedded in
the application config may be JSON, YAML, or TOML.

## Sparse fields and null

The intended rule is:

- the source never supplied a field: omit the output key;
- the source explicitly supplied `null`: keep the key with `null`;
- the source supplied a value: keep the key with that value.

Do not manufacture shipping methods, freight rates, integration revisions, or
other placeholders in standalone mode. Integration-only values appear only
when an active provider supplied them.

The typed `kala` model tracks presence for nullable/optional fields. Unknown
JSON extension members are retained across canonical decode/re-encode so a
newer sender is not reduced to an older closed struct. Consumers should ignore
unknown members they do not need while preserving them when acting as a proxy.

## Character conversion and RTL

Patris81 text uses a legacy character mapping. The default map is embedded in
the executable. Override it only for a verified custom encoding or debugging:

```bash
patris-export --charmap ./custom-farsi-chars.txt convert other.db --format json
```

The map implementation and embedded data live in:

- `pkg/converter/patris2fa.go`
- `pkg/converter/embedded_charmap.go`
- `pkg/converter/farsi_chars.txt`

`--raw` bypasses character conversion. `--rtl` is a separate opt-in logical
RTL transformation; it is not required merely to display Persian in an
RTL-aware Web UI. See [RTL conversion](RTL_CONVERSION.md).

## Labels and interface language

Current label sources are:

- `web/src/table-ux.js`: built-in English/Persian column labels and interface
  messages;
- `pkg/recordsink/xlsx_labels.go`: built-in XLSX labels;
- `column_labels` in application config: flat operator overrides;
- `ui.language`: `en` or `fa`;
- `export.xlsx_language`: `auto`, `en`, or `fa`.

Example:

```yaml
ui:
  language: fa
  rtl_text_direction: true

export:
  xlsx_language: auto

column_labels:
  product_code: کد کالا
  name: نام
  part_number: پارت نامبر
```

The current config override is not locale-nested: one configured label replaces
the generated/default label for that deployment. Built-in Web and XLSX label
registries are also currently duplicated. The desired follow-up is one shared,
locale-aware field registry that generates Web, XLSX, API documentation, and
consumer metadata without parallel hand-maintained lists.

Public Digitalogic wording:

| Machine key | English | Persian |
| --- | --- | --- |
| `product_code` | Product Code | کد کالا |
| `name` | Name | نام |
| `part_number` | Part Number | پارت نامبر |
| `warehouse_stock` | Warehouse Stock | موجودی انبار |
| `shipping_method_id` | Shipping Method | روش حمل |

Do not label `product_code` as “Patris Code” or `کد پاتریس` on customer-facing
UI, exports, descriptions, or reports.

## `company.inf`

Current support is explicit:

```bash
patris-export company /path/to/company.inf
```

Patris data directories commonly pair their tables with a same-directory
`company.inf` (not `company.ini`). Automatic discovery, UI display, API
metadata, and consumer propagation are planned for directory/multi-database
mode. The current single-file server does not automatically attach company
metadata.

## Multi-database direction

The present process serves one active `.db`. A future `dataN` loader should:

1. inventory every table and its schema through raw pxlib metadata;
2. discover `company.inf` once for the directory;
3. group each `.db` with required companion files for stable copying;
4. default-deny temporary, backup, tax, user, and sensitive transaction tables
   until an ACL permits them;
5. apply semantic profiles only to matching datasets;
6. expose raw rows for unmapped but authorized tables;
7. report per-table watch/read exceptions to configured observers.

That design must not assume all tables use `Code`, contain products, or share
`kala.db` field meanings.

## How to add another profile

1. Capture schema/row-shape evidence through `info` and raw records.
2. Define the product-independent meaning of the table.
3. Implement a profile that preserves row cardinality and reports ambiguity.
4. Add a `canonical.profiles` type and tests for missing, null, duplicate, and
   extension fields.
5. Add human labels without changing machine keys.
6. Update OpenAPI/AsyncAPI and this guide.
7. Keep `/api/records` available as the escape hatch for consumers that need
   the source rather than the new projection.
