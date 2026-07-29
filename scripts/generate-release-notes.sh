#!/usr/bin/env bash
set -euo pipefail

if (($# != 3)); then
    printf 'Usage: %s <vX.Y.Z-tag|candidate> <source-commit> <output.md>\n' "$0" >&2
    exit 2
fi

requested_tag="$1"
source_commit="$2"
output="$3"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd -- "$script_dir/.." && pwd)"
# shellcheck source=scripts/release-lib.sh
source "$script_dir/release-lib.sh"
release_metadata "$root" "$requested_tag"
artifact_label="$RELEASE_LABEL"
if [[ "$RELEASE_IS_CANDIDATE" == "1" ]]; then
    artifact_label="${RELEASE_LABEL}-${source_commit:0:12}"
    heading_suffix=" candidate"
    distribution_statement="This is a non-published validation candidate built directly from source commit"
    provenance_line="- Candidate label: \`${artifact_label}\` (not a Git tag or published release)"
    builder_line="- Builder: GitHub Actions candidate workflow from the identified source commit"
    source_archive_line="- No GitHub Release or durable source archives are published for this candidate."
else
    heading_suffix=""
    distribution_statement="This release was built and verified directly from source commit"
    provenance_line="- Tag: \`${RELEASE_TAG}\`"
    builder_line="- Builder: GitHub Actions from the tagged source tree"
    source_archive_line="- GitHub's source archives are available in the release **Assets** section alongside the installable bundles."
fi

mkdir -p -- "$(dirname -- "$output")"
cat > "$output" <<EOF
# Patris Export ${RELEASE_VERSION}${heading_suffix}

Patris Export is a standalone Paradox/BDE extraction, transformation, pricing, integration, and live-view platform for Patris81 data. ${distribution_statement} [\`${source_commit}\`](https://github.com/atomicdeploy/patris-export/commit/${source_commit}).

## Choose an artifact

| Platform | Artifact | Contents |
| --- | --- | --- |
| Windows amd64 (recommended) | \`patris-export-${artifact_label}-windows-amd64-setup.exe\` | Branded assisted installer, uninstaller, runtime, optional C SDK, configuration guide, and license |
| Windows amd64 | \`patris-export-${artifact_label}-windows-amd64.zip\` | Executable, pxlib and MinGW runtime DLLs, C shared library/header, install guide, license, and build manifest |
| Linux amd64 | \`patris-export-${artifact_label}-linux-amd64.tar.gz\` | Executable, bundled pxlib runtime and launcher, C shared library/header, install guide, license, and build manifest |
| API documentation | \`patris-export-${artifact_label}-api-docs-public.zip\` | Offline public Scalar portal, OpenAPI/AsyncAPI contracts, client examples, licenses, and checksums |
| All | \`SHA256SUMS\` | SHA-256 verification manifest for the installer, platform archives, and public API reference |

## Install

- **Windows (recommended):** run the assisted setup executable. It installs the runtime and uninstaller, preserves configuration during upgrades, and offers the C SDK as an optional component.
- **Windows (portable/embedding):** extract the entire ZIP, keep every DLL beside \`patris-export.exe\`, then run \`.\patris-export.exe --version\`.
- **Linux:** extract the tarball and run \`./patris-export-linux-amd64/run-patris-export.sh --version\`.
- Detailed deployment, embedding, smoke-test, and upgrade instructions are included as \`INSTALL.md\` in both archives.

Configuration and secrets remain external to the release bundle, so upgrading does not replace a deployment's configuration. Patris Export and Digitalogic remain independently deployable; integration features activate only when configured.

## Verify

Download \`SHA256SUMS\` beside the files you use. On Linux, run \`grep 'linux-amd64.tar.gz$' SHA256SUMS | sha256sum -c -\`. On Windows, compare \`Get-FileHash <archive> -Algorithm SHA256\` with the matching manifest line. Download every listed platform and documentation archive before using \`sha256sum -c SHA256SUMS\` for the complete set.

## Changelog

${RELEASE_CHANGELOG}

## Source and build provenance

${provenance_line}
- Source commit: \`${source_commit}\`
${builder_line}
- Dependency lock: Go modules, \`web/package-lock.json\`, \`docs/api/package-lock.json\`, and pinned pxlib source revision
- Verification: Go and web tests, dependency audits, OpenAPI/AsyncAPI lint, source-route parity, API documentation tests, deterministic offline documentation packaging, Linux source build, native Windows source build and database smoke test, independent Windows cross-build and Wine smoke test, version-resource inspection, required-runtime checks, deterministic archive assembly, and SHA-256 generation
${source_archive_line}
EOF

printf 'Generated release notes for %s at %s\n' "$RELEASE_TAG" "$output"
