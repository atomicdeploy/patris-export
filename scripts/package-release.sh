#!/usr/bin/env bash
set -euo pipefail

if (($# != 4)); then
    printf 'Usage: %s <vX.Y.Z-tag|candidate> <source-commit> <artifact-root> <dist-dir>\n' "$0" >&2
    exit 2
fi

requested_tag="$1"
source_commit="$2"
artifact_root="$(cd -- "$3" && pwd)"
dist_input="$4"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd -- "$script_dir/.." && pwd)"
# shellcheck source=scripts/release-lib.sh
source "$script_dir/release-lib.sh"
release_metadata "$root" "$requested_tag"
artifact_label="$RELEASE_LABEL"
if [[ "$RELEASE_IS_CANDIDATE" == "1" ]]; then
    artifact_label="${RELEASE_LABEL}-${source_commit:0:12}"
    provenance_line="Candidate label: ${artifact_label} (not a Git tag or published release)"
else
    provenance_line="Tag: ${RELEASE_TAG}"
fi

for tool in install touch zip tar gzip sha256sum sort xargs; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'Required release packaging tool is missing: %s\n' "$tool" >&2
        exit 1
    fi
done

if [[ -e "$dist_input" ]]; then
    printf 'Release output must be a new directory: %s\n' "$dist_input" >&2
    exit 2
fi
dist_parent_input="$(dirname -- "$dist_input")"
mkdir -p -- "$dist_parent_input"
dist_parent="$(cd -- "$dist_parent_input" && pwd)"
dist="$dist_parent/$(basename -- "$dist_input")"
allowed_output=0
if [[ "$dist" == "$root/release-package" || "$dist" == "$root/build/"* ]]; then
    allowed_output=1
elif [[ -n "${RUNNER_TEMP:-}" ]]; then
    runner_temp="$(cd -- "$RUNNER_TEMP" && pwd)"
    [[ "$dist" == "$runner_temp/"* ]] && allowed_output=1
fi
if [[ "$allowed_output" != "1" ]]; then
    printf 'Release output must be repo/release-package, repo/build/*, or RUNNER_TEMP/*: %s\n' "$dist" >&2
    exit 2
fi
mkdir -p -- "$dist"

windows_source="$artifact_root/windows"
linux_source="$artifact_root/linux"
windows_stage="$dist/stage/patris-export-windows-amd64"
linux_stage="$dist/stage/patris-export-linux-amd64"
mkdir -p -- "$windows_stage" "$linux_stage"

required_windows=(
    patris-export-windows-amd64.exe
    patris-export.dll
    patris-export.h
    libpxlib.dll
    libgcc_s_seh-1.dll
    libstdc++-6.dll
    libwinpthread-1.dll
)
for file in "${required_windows[@]}"; do
    if [[ ! -f "$windows_source/$file" ]]; then
        printf 'Required Windows artifact is missing: %s\n' "$file" >&2
        exit 1
    fi
done

install -m 0755 "$windows_source/patris-export-windows-amd64.exe" "$windows_stage/patris-export.exe"
for file in patris-export.dll patris-export.h libpxlib.dll libgcc_s_seh-1.dll libstdc++-6.dll libwinpthread-1.dll; do
    install -m 0644 "$windows_source/$file" "$windows_stage/$file"
done
install -m 0644 "$root/scripts/windows/Install-PatrisExportScheduledTask.ps1" "$windows_stage/Install-PatrisExportScheduledTask.ps1"
install -m 0644 "$root/scripts/windows/Run-PatrisExportScheduledTask.ps1" "$windows_stage/Run-PatrisExportScheduledTask.ps1"

required_linux=(
    patris-export-linux-amd64
    libpatris-export.so
    libpatris-export.h
)
for file in "${required_linux[@]}"; do
    if [[ ! -f "$linux_source/$file" ]]; then
        printf 'Required Linux artifact is missing: %s\n' "$file" >&2
        exit 1
    fi
done

shopt -s nullglob
pxlib_linux=("$linux_source"/libpx*.so*)
shopt -u nullglob
if ((${#pxlib_linux[@]} == 0)); then
    printf 'The Linux artifact does not contain a bundled pxlib runtime.\n' >&2
    exit 1
fi

install -m 0755 "$linux_source/patris-export-linux-amd64" "$linux_stage/patris-export"
install -m 0755 "$linux_source/libpatris-export.so" "$linux_stage/libpatris-export.so"
install -m 0644 "$linux_source/libpatris-export.h" "$linux_stage/libpatris-export.h"
for file in "${pxlib_linux[@]}"; do
    install -m 0755 "$file" "$linux_stage/$(basename -- "$file")"
done

cat > "$linux_stage/run-patris-export.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
app_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export LD_LIBRARY_PATH="$app_dir${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$app_dir/patris-export" "$@"
EOF
chmod 0755 "$linux_stage/run-patris-export.sh"

for stage in "$windows_stage" "$linux_stage"; do
    install -m 0644 "$root/docs/INSTALL-BINARIES.md" "$stage/INSTALL.md"
    install -m 0644 "$root/docs/LICENSING.md" "$stage/LICENSING.md"
    install -m 0644 "$root/README.md" "$stage/README.md"
    install -m 0644 "$root/LICENSE" "$stage/LICENSE"
    install -m 0644 "$root/NOTICE" "$stage/NOTICE"
    cat > "$stage/BUILD-VARIANT.json" <<EOF
{
  "variant": "standard",
  "licensing_mode": "none",
  "license_required": false
}
EOF
    cat > "$stage/BUILD-MANIFEST.txt" <<EOF
Patris Export ${RELEASE_VERSION}
${provenance_line}
Source commit: ${source_commit}
pxlib source commit: $(tr -d '[:space:]' < "$root/dependencies/pxlib.ref")
Built by: GitHub Actions
EOF
done

source_date_epoch="$(git -C "$root" show -s --format=%ct "$source_commit")"
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]]; then
    printf 'Unable to resolve a source timestamp for %s.\n' "$source_commit" >&2
    exit 1
fi
shopt -s globstar nullglob dotglob
stage_paths=("$dist/stage" "$dist/stage"/**/*)
touch -h -d "@$source_date_epoch" "${stage_paths[@]}"

windows_archive="patris-export-${artifact_label}-windows-amd64.zip"
linux_archive="patris-export-${artifact_label}-linux-amd64.tar.gz"
(
    cd "$dist/stage"
    windows_paths=()
    for path in patris-export-windows-amd64/**/*; do
        [[ -f "$path" ]] && windows_paths+=("$path")
    done
    printf '%s\0' "${windows_paths[@]}" |
        LC_ALL=C sort -z |
        xargs -0 zip -X -q "$dist/$windows_archive"
    tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 --numeric-owner \
        -cf - patris-export-linux-amd64 |
        gzip -n -9 > "$dist/$linux_archive"
)

(
    cd "$dist"
    sha256sum "$windows_archive" "$linux_archive" > SHA256SUMS
)
bash "$script_dir/generate-release-notes.sh" "$requested_tag" "$source_commit" "$dist/RELEASE_NOTES.md"

rm -rf -- "$dist/stage"
printf 'Created release package:\n'
sed 's/^/  /' "$dist/SHA256SUMS"
