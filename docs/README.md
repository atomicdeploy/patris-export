# Patris Export documentation

This directory is the human-readable guide set for Patris Export. It explains
how to run the application, choose the correct data surface, configure
transformations and integrations, and understand the implementation without
requiring a particular consumer such as WordPress.

The guides are ordinary Markdown so they work in GitHub, an IDE, a source
archive, or a future Wiki mirror. The generated API reference remains the
machine-readable source for exact HTTP and WebSocket request/response shapes.

## Start here

| Goal | Guide |
| --- | --- |
| Install and view a database | [Getting started](GETTING-STARTED.md) |
| Find a command or flag | [CLI reference](CLI-REFERENCE.md) |
| Operate the terminal dashboard | [TUI guide](TUI-GUIDE.md) |
| Configure files, environment, mappings, and exports | [Configuration](CONFIGURATION.md) |
| Understand components and data flow | [Architecture](ARCHITECTURE.md) |
| Connect another application or service | [Ecosystem and integrations](ECOSYSTEM-AND-INTEGRATIONS.md) |
| Work with arbitrary schemas, `kala.db`, labels, and Persian text | [Datasets, mappings, and i18n](DATASETS-MAPPINGS-I18N.md) |
| Understand or turn off record identities | [Record hashes](RECORD-HASHES.md) |
| Look up an advanced term | [Glossary](GLOSSARY.md) |
| Use an HTTP or WebSocket route | [Generated API reference](api/README.md) |

## Capability map

Implemented today:

- one local or HTTP(S) Paradox `.db` source per running process;
- raw ordered records for arbitrary Paradox schemas;
- a configured `kala.db` product/category projection;
- JSON, CSV, XLSX, SQLite, and MySQL/MariaDB conversion targets;
- Web UI, REST, WebSocket, TUI, local IPC, one-shot viewer, and edge upload;
- optional outbound HTTP or command delivery;
- optional pricing integration and a separately configured recent-sales
  aggregate source;
- static public and internal API-reference ZIP builds.

Tracked as future work, and therefore not described as available:

- loading and watching a complete `dataN` directory as one multi-database
  source;
- automatic same-directory `company.inf` discovery;
- TSV, BSON, MessagePack, Protocol Buffers, and generated SQLite downloads from
  the data API;
- server-side export of the Web UI's filtered subset;
- one unified operating-system/API-key authentication and ACL layer;
- native Windows automation of the Patris81 desktop program.

The repository issues are the live roadmap. A guide may explain the intended
direction, but examples in these files use only behavior present in the source.

## Detailed references

### Installation, builds, and packaging

- [Binary installation](INSTALL-BINARIES.md)
- [Windows build](WINDOWS_BUILD.md)
- [Windows installer](WINDOWS_INSTALLER.md)
- [Native pxlib backends](NATIVE-PXLIB-BACKENDS.md)
- [Optional licensing build](LICENSING.md)

### Data and output

- [Excel export](EXCEL-EXPORT.md)
- [Excel pricing sync](EXCEL-PRICING-SYNC.md)
- [SQL target operator API](SQL-TARGET-OPERATIONS-API.md)
- [RTL conversion](RTL_CONVERSION.md)
- [Table virtualization](TABLE-VIRTUALIZATION.md)

### Integration

- [Integration standard](INTEGRATION-STANDARD.md)
- [Product-sync compatibility envelope](CANONICAL-PRODUCT-SYNC.md)
- [Recent-sales aggregate API](RECENT-SALES-API.md)
- [Remote delivery examples](REMOTE-API-EXAMPLES.md)
- [Embedding and IPC](EMBEDDING.md)
- [C ABI and pxlib FFI](PXLIB-FFI.md)
- [Examples index](examples/README.md)

## Documentation sources of truth

- Cobra declarations in `cmd/patris-export` define commands and flags.
- Configuration structs and normalization in `pkg/appconfig`,
  `pkg/canonical`, `pkg/recordmap`, `pkg/recentsales`, and `pkg/updateout`
  define configuration behavior.
- Route registration in `pkg/server/server.go` defines the HTTP surface.
- `docs/api/openapi.yaml` and `docs/api/asyncapi.yaml` define exact public and
  internal protocol shapes and are checked against the registered routes.
- This guide set explains intent, workflows, and relationships. It does not
  duplicate every wire schema.

When implementation and a guide disagree, treat that as a documentation defect
and correct both in the same change.

## Static and offline API documentation

From the repository root:

```bash
npm --prefix docs/api ci
npm --prefix docs/api run lint
npm --prefix docs/api run parity
npm --prefix docs/api test
npm --prefix docs/api run build
npm --prefix docs/api run check:determinism
```

The build creates a public reference, a complete internal reference, and
offline ZIP archives. See [the API reference build guide](api/README.md) for
the output paths and publication boundary. The internal archive contains
operator/private routes and must not be published as a public GitHub Pages
site.

## Documentation safety

Examples use placeholder hosts and environment-variable names. Do not add live
database rows, customer information, passwords, API keys, DSNs, private
deployment addresses, or local machine identities to documentation, examples,
issues, or generated archives.
