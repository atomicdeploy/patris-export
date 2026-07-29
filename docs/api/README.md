# Patris Export API reference

This directory is the source for the complete Patris Export web-service
reference. The reference is generated from standards-based contracts rather
than copied from handler comments, so the built-in viewer, external clients,
code generators, and AI tools can consume the same descriptions and schemas.

## Sources of truth

- `openapi.yaml` describes every registered HTTP method and route, including
  request and response headers, media types, parameters, bodies, errors,
  availability gates, side effects, and the route's actual security boundary.
- `asyncapi.yaml` describes the `/ws` connection, all initial/update/config/
  process/toast server events, and the toast/refresh client commands.
- `redocly.yaml` contains the OpenAPI lint policy.
- The rendered `build/*/examples/` directories contain reusable, non-secret
  client examples generated from the applicable contract.

Descriptions may link to the repository's longer operational guides, but
request and response shapes must not be maintained in a second handwritten
schema. A code or route change is incomplete until the contracts and examples
change with it.

## Catalog route model

- `GET /api/records` is the generalized, minimally transformed datasource
  boundary. JSON is an ordered array so missing or duplicate source keys do not
  discard rows; CSV and XLSX contain the same source-dependent fields.
- `GET /api/products` (and its `.json`, `.csv`, and `.xlsx` forms) is the KALA
  product projection. `GET /api/categories` is the matching structural
  hierarchy.
- `GET /api/product-sync` is a temporary compatibility replication adapter for
  consumers that need one atomic envelope with collections, exclusions,
  quarantine/tombstone state, source revision, and event identity. It is not a
  second general query model and can be unavailable when hash identities are
  disabled.

The `include_hashes` query on product/category collection reads can only hide
`record_hash`; it never overrides server policy. Canonical typed objects retain
unknown extension properties, allowing independently evolving peers to ignore
fields they do not understand while continuing to validate known fields.

Every operation is classified as one of the externally supported,
integration-authenticated, local-operator, or private/internal surfaces. The
classification documents current behavior; it must not claim authentication
that the server does not actually enforce. Conditional operations also
document their disabled or unavailable response rather than representing an
unavailable data source as an empty result.

Browser-visible configuration and diagnostics strip protected URL components,
delivery commands, integration headers/tokens, and process command lines while
preserving their server-side values. This redaction is not authentication:
most viewer/diagnostic routes and `/ws` currently trust the deployment boundary,
and the WebSocket accepts arbitrary Origin values. Keep the service on trusted
loopback or behind a reviewed authenticated proxy; do not expose it directly to
an untrusted network.

## Build and validate

Node.js 24 and the lockfile in this directory are the supported toolchain.
The directory-local `.npmrc` disables dependency lifecycle scripts because
this documentation build only needs the packages' published files.

```bash
npm --prefix docs/api ci
npm --prefix docs/api run lint
npm --prefix docs/api run parity
npm --prefix docs/api test
npm --prefix docs/api run build
npm --prefix docs/api run check:determinism
```

The equivalent Make targets are:

```bash
make docs-install
make docs-verify
make docs-package
```

`parity` enumerates the routes registered by
`pkg/server/server.go::setupRoutes` and fails when an HTTP method/path is absent
from OpenAPI or an obsolete operation remains. WebSocket parity is checked
against AsyncAPI. Linting also requires both contract versions to match
`pkg/version/version.go`. Every operation/channel also carries a stable
`path/to/file.go#Receiver.method` implementation anchor. Linting rejects
missing handlers, stale symbols, test-file anchors, traversal, and numeric line
ranges. Linting and tests additionally reject broken references, incomplete or
drifting examples, missing visibility metadata, and unsafe sample values.

Do not put credentials, token values, live customer data, private deployment
addresses, database rows, or local filesystem identities in a contract,
example, rendered page, test snapshot, or archive.

## Generated output

Generated files are intentionally ignored by Git:

- `build/public/` is the static, responsive external reference. Its
  `index.html` is ready to become a future GitHub Pages artifact. Static builds
  are browse/copy-only: their request console is disabled because file/Pages
  origins are cross-origin from the Patris listener.
- `build/internal/` is the complete reference, including operator and
  private/internal routes. It must never be deployed to a public Pages site.
- `dist/patris-export-api-docs-public.zip` is the offline public reference.
- `dist/patris-export-api-docs-internal.zip` is the offline complete reference.
- `dist/SHA256SUMS` verifies both offline archives.

Each ZIP includes the rendered static reference, the applicable source
contracts in YAML and bundled JSON form, examples, and a concise machine-reader
entry point, plus the project license, notice, and embedded UI dependency
notices. It must work after ordinary extraction without a web server or network
connection.

The dedicated API documentation workflow runs on pull requests, `main`, release
tags, manual dispatch, and reusable workflow calls. It uses Node.js 24, runs
lint/parity/test/build/determinism gates, verifies both ZIPs, and uploads
only the public ZIP with its SHA-256 manifest. The complete internal ZIP is
validated ephemerally but is deliberately not uploaded from this public
repository. The workflow has read-only repository permissions and deliberately
has no `pages: write` or identity-token permission.

When both verified documentation ZIPs and `SHA256SUMS` are present,
`scripts/package-release.sh` copies the public ZIP into the deterministic
release output using the release's version/candidate label and adds it to the
release checksum manifest. The internal ZIP is included only when the trusted
packager explicitly sets `INCLUDE_INTERNAL_API_DOCS=1`; a public release must
not set that switch. Existing platform-only packaging remains supported when
no documentation output is present. The current durable release workflow does
not publish a Pages site; that can later reuse `build/public/` without changing
the contracts or exposing the internal build.
