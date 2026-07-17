#!/usr/bin/env bash

release_metadata() {
    local root="$1"
    local requested_tag="$2"
    local source_version

    source_version="$(sed -nE 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$root/pkg/version/version.go" | head -n 1)"
    if [[ -z "$source_version" ]]; then
        printf 'Unable to read the source version from pkg/version/version.go.\n' >&2
        return 1
    fi

    RELEASE_IS_CANDIDATE=0
    if [[ "$requested_tag" == "candidate" ]]; then
        RELEASE_VERSION="$source_version"
        RELEASE_TAG="v${source_version}"
        RELEASE_LABEL="v${source_version}-candidate"
        RELEASE_IS_CANDIDATE=1
    else
        if [[ ! "$requested_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
            printf 'Release tag must use vX.Y.Z syntax: %s\n' "$requested_tag" >&2
            return 2
        fi
        RELEASE_VERSION="${BASH_REMATCH[1]}"
        RELEASE_TAG="$requested_tag"
        RELEASE_LABEL="$requested_tag"
    fi
    if [[ "$RELEASE_VERSION" != "$source_version" ]]; then
        printf 'Tag %s does not match source version %s.\n' "$RELEASE_TAG" "$source_version" >&2
        return 1
    fi

    RELEASE_CHANGELOG="$(awk -v wanted="$RELEASE_VERSION" '
        $0 == "## [" wanted "]" || index($0, "## [" wanted "] - ") == 1 {
            found = 1
            next
        }
        found && /^## \[/ { exit }
        found { print }
        END { if (!found) exit 1 }
    ' "$root/CHANGELOG.md")" || {
        printf 'CHANGELOG.md has no section for %s.\n' "$RELEASE_VERSION" >&2
        return 1
    }
    if [[ -z "${RELEASE_CHANGELOG//[[:space:]]/}" ]]; then
        printf 'CHANGELOG.md section for %s is empty.\n' "$RELEASE_VERSION" >&2
        return 1
    fi

    export RELEASE_VERSION RELEASE_TAG RELEASE_LABEL RELEASE_IS_CANDIDATE RELEASE_CHANGELOG
}
