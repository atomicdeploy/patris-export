#!/usr/bin/env bash

set -u

summary_file="${GITHUB_STEP_SUMMARY:-}"
if [[ -z "$summary_file" ]]; then
  summary_file="$(mktemp)"
  echo "GITHUB_STEP_SUMMARY is not set; writing summary preview to $summary_file" >&2
fi

repo="${GITHUB_REPOSITORY:-atomicdeploy/patris-export}"
server_url="${GITHUB_SERVER_URL:-https://github.com}"
run_id="${GITHUB_RUN_ID:-local}"
run_attempt="${GITHUB_RUN_ATTEMPT:-1}"
workflow="${GITHUB_WORKFLOW:-local workflow}"
job_status="${JOB_STATUS:-unknown}"
event_name="${GITHUB_EVENT_NAME:-local}"
ref_name="${GITHUB_HEAD_REF:-${GITHUB_REF_NAME:-$(git branch --show-current 2>/dev/null || echo local)}}"
sha="${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || echo local)}"
short_sha="${sha:0:12}"
actor="${GITHUB_ACTOR:-local}"
run_url="${server_url}/${repo}/actions/runs/${run_id}"
commit_url="${server_url}/${repo}/commit/${sha}"
repo_url="${server_url}/${repo}"
branch_url="${server_url}/${repo}/tree/${ref_name}"
artifact_url="${run_url}#artifacts"
summary_generated_at="$(date -u '+%Y-%m-%d %H:%M:%SZ')"

build_version() {
  local version="${VERSION:-}"
  if [[ -z "$version" && -f pkg/version/version.go ]]; then
    version="$(sed -nE 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' pkg/version/version.go | head -n 1)"
  fi
  if [[ -z "$version" ]]; then
    version="$(git describe --tags --abbrev=0 2>/dev/null || printf 'v1.0.0')"
    version="${version#v}"
  fi
  if [[ ! "$version" =~ ^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9._-]+)?$ ]]; then
    version="1.0.0"
  fi
  printf '%s' "$version"
}

status_icon() {
  case "$job_status" in
    success) printf "✅" ;;
    failure) printf "❌" ;;
    cancelled) printf "🚫" ;;
    skipped) printf "⏭️" ;;
    *) printf "🧭" ;;
  esac
}

human_size() {
  local bytes="${1:-0}"
  awk -v b="$bytes" 'BEGIN {
    split("B KiB MiB GiB TiB", u, " ");
    i = 1;
    while (b >= 1024 && i < 5) { b = b / 1024; i++ }
    if (i == 1) printf "%d %s", b, u[i];
    else printf "%.2f %s", b, u[i];
  }'
}

file_size() {
  local path="$1"
  stat -c '%s' "$path" 2>/dev/null || wc -c < "$path" | tr -d ' '
}

file_modified() {
  local path="$1"
  date -u -r "$path" '+%Y-%m-%d %H:%M:%SZ' 2>/dev/null || printf "unknown"
}

file_hash() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$path" | awk '{print $NF}'
  else
    printf "unavailable"
  fi
}

classify_file() {
  local path="$1"
  local base="${path##*/}"
  case "$base" in
    *.exe) printf "🪟 Windows executable" ;;
    *.dll) printf "🧩 Windows DLL" ;;
    *.so) printf "🐧 Linux shared object" ;;
    *.h) printf "📜 C header" ;;
    *.zip|*.7z|*.tar.gz) printf "📦 Archive" ;;
    patris-export-linux-*|patris-export) printf "🐧 Linux executable" ;;
    *) printf "📄 File" ;;
  esac
}

append_context() {
  cat >> "$summary_file" <<EOF

### 🧭 Run Context

| Property | Value |
| --- | --- |
| Repository | [\`${repo}\`](${repo_url}) |
| Branch / Ref | [\`${ref_name}\`](${branch_url}) |
| Commit | [\`${short_sha}\`](${commit_url}) |
| Actor | [@${actor}](${server_url}/${actor}) |
| Workflow | \`${workflow}\` |
| Run | [\`${run_id}\` attempt \`${run_attempt}\`](${run_url}) |
| Event | \`${event_name}\` |
| Job status | $(status_icon) \`${job_status}\` |

EOF
}

append_toolchain() {
  local go_version node_version npm_version os_line
  go_version="$(go version 2>/dev/null || printf 'go unavailable')"
  node_version="$(node --version 2>/dev/null || printf 'node unavailable')"
  npm_version="$(npm --version 2>/dev/null || printf 'npm unavailable')"
  os_line="$(uname -a 2>/dev/null || printf 'unknown runner')"

  cat >> "$summary_file" <<EOF
### 🧰 Toolchain Snapshot

| Tool | Version |
| --- | --- |
| Go | \`${go_version}\` |
| Node.js | \`${node_version}\` |
| npm | \`${npm_version}\` |
| Runner | \`${os_line}\` |

EOF
}

append_build_metadata() {
  local version build_date source_note
  version="$(build_version)"
  build_date="${BUILD_DATE:-${summary_generated_at}}"
  if [[ -n "${BUILD_DATE:-}" ]]; then
    source_note="provided by build environment"
  else
    source_note="summary generation time"
  fi

  cat >> "$summary_file" <<EOF
### 🏷️ Build Metadata

| Property | Value |
| --- | --- |
| Version | \`${version}\` |
| Build date UTC | \`${build_date}\` |
| Build date source | ${source_note} |
| Commit | [\`${short_sha}\`](${commit_url}) |

EOF
}

artifact_retention_estimate() {
  local days="${ARTIFACT_RETENTION_DAYS:-${GITHUB_RETENTION_DAYS:-}}"
  if [[ "$days" =~ ^[0-9]+$ ]]; then
    local expires
    expires="$(date -u -d "+${days} days" '+%Y-%m-%d %H:%M:%SZ' 2>/dev/null || printf '')"
    if [[ -n "$expires" ]]; then
      printf 'Configured retention is `%s` day(s); estimated expiration is `%s` unless the repository or organization policy changes it.' "$days" "$expires"
    else
      printf 'Configured retention is `%s` day(s); GitHub computes the exact artifact expiration.' "$days"
    fi
  else
    printf 'Retention is controlled by the repository or organization Actions artifact policy; GitHub reports the exact expiration after upload.'
  fi
}

append_uploaded_artifact_metadata() {
  local artifact_name="$1"
  local token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"

  cat >> "$summary_file" <<EOF
### 🗄️ Uploaded Artifact

| Property | Value |
| --- | --- |
| Artifact set | [\`${artifact_name}\`](${artifact_url}) |
EOF

  if [[ -n "$token" && "$run_id" != "local" ]] && command -v python3 >/dev/null 2>&1; then
    if python3 - "$server_url" "$repo" "$run_id" "$artifact_name" "$token" >> "$summary_file" <<'PY'
import json
import sys
import urllib.request

server_url, repo, run_id, artifact_name, token = sys.argv[1:6]
api_base = server_url.replace("https://github.com", "https://api.github.com")
url = f"{api_base}/repos/{repo}/actions/runs/{run_id}/artifacts?per_page=100"

def human_size(size):
    value = float(size or 0)
    units = ["B", "KiB", "MiB", "GiB", "TiB"]
    index = 0
    while value >= 1024 and index < len(units) - 1:
        value /= 1024
        index += 1
    return f"{int(value)} {units[index]}" if index == 0 else f"{value:.2f} {units[index]}"

request = urllib.request.Request(
    url,
    headers={
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {token}",
        "X-GitHub-Api-Version": "2022-11-28",
    },
)
with urllib.request.urlopen(request, timeout=20) as response:
    payload = json.load(response)

artifact = next((item for item in payload.get("artifacts", []) if item.get("name") == artifact_name), None)
if not artifact:
    print(f"| API lookup | Artifact `{artifact_name}` not found yet; use the run artifact list above. |")
else:
    size = artifact.get("size_in_bytes", 0)
    print(f"| Artifact ID | `{artifact.get('id', 'unknown')}` |")
    print(f"| Archive size | {human_size(size)} (`{size}` bytes) |")
    print(f"| Created at | `{artifact.get('created_at', 'unknown')}` |")
    print(f"| Expires at | `{artifact.get('expires_at', 'unknown')}` |")
    print(f"| Expired | `{artifact.get('expired', 'unknown')}` |")
    print(f"| Archive download URL | [GitHub API download]({artifact.get('archive_download_url', '#')}) |")
PY
    then
      :
    else
      printf '| API lookup | GitHub artifact metadata lookup failed; use the run artifact list above. |\n' >> "$summary_file"
    fi
  else
    printf '| Archive metadata | GitHub API metadata unavailable in this context. |\n' >> "$summary_file"
  fi

  cat >> "$summary_file" <<EOF
| Retention note | $(artifact_retention_estimate) |

EOF
}

append_artifact_table() {
  local artifact_name="$1"
  shift
  local existing=0 missing=0
  declare -A seen_paths=()

  cat >> "$summary_file" <<EOF
### 📦 Artifact Manifest

Uncompressed files included in artifact bundle [\`${artifact_name}\`](${artifact_url}).

| Type | File | Size | Bytes | SHA-256 | Modified UTC |
| --- | --- | ---: | ---: | --- | --- |
EOF

  for path in "$@"; do
    if [[ -n "${seen_paths[$path]+x}" ]]; then
      continue
    fi
    seen_paths[$path]=1

    if [[ -f "$path" ]]; then
      local bytes hash modified kind
      bytes="$(file_size "$path")"
      hash="$(file_hash "$path")"
      modified="$(file_modified "$path")"
      kind="$(classify_file "$path")"
      printf '| %s | `%s` | %s | %s | `%s` | `%s` |\n' "$kind" "$path" "$(human_size "$bytes")" "$bytes" "$hash" "$modified" >> "$summary_file"
      existing=$((existing + 1))
    else
      printf '| ⚠️ Missing | `%s` | - | - | - | - |\n' "$path" >> "$summary_file"
      missing=$((missing + 1))
    fi
  done

  cat >> "$summary_file" <<EOF

EOF

  if [[ "$existing" -eq 0 ]]; then
    cat >> "$summary_file" <<EOF
> ❌ No expected output files were found. Check the build log above this summary for the failing command.

EOF
  elif [[ "$missing" -gt 0 ]]; then
    cat >> "$summary_file" <<EOF
> ⚠️ ${existing} expected file(s) were produced, but ${missing} expected path(s) were missing.

EOF
  else
    cat >> "$summary_file" <<EOF
> ✅ All expected output files were produced and fingerprinted.

EOF
  fi
}

append_integrity_commands() {
  local files=("$@")
  cat >> "$summary_file" <<EOF
<details>
<summary>🔐 Reproduce integrity checks locally</summary>

\`\`\`bash
EOF

  for path in "${files[@]}"; do
    [[ -f "$path" ]] || continue
    printf 'sha256sum %q\n' "$path" >> "$summary_file"
  done

  cat >> "$summary_file" <<EOF
\`\`\`

</details>

EOF
}

append_footer() {
  cat >> "$summary_file" <<EOF
---

_Generated by Patris Export CI summaries for [\`${repo}\`](${repo_url})._

EOF
}

mode="${1:-}"
shift || true

case "$mode" in
  build)
    title="${1:-Build summary}"
    description="${2:-Build completed.}"
    artifact_name="${3:-artifact}"
    build_kind="${4:-Build}"
    shift 4 || true
    files=("$@")

    cat >> "$summary_file" <<EOF
# $(status_icon) ${title}

${description}

### ✨ What Happened

| Item | Detail |
| --- | --- |
| Build kind | **${build_kind}** |
| Artifact set | \`${artifact_name}\` |
| pxlib strategy | Upstream source build when available; package/system fallback where configured |
| Frontend assets | HTML5 SPA bundle generated before native compilation |
| Native layer | CGO-enabled Patris/pxlib integration |

EOF
    append_context
    append_build_metadata
    append_toolchain
    append_uploaded_artifact_metadata "$artifact_name"
    append_artifact_table "$artifact_name" "${files[@]}"
    append_integrity_commands "${files[@]}"
    append_footer
    ;;

  test)
    log_file="${1:-test-results.log}"
    package_total=0
    package_ok=0
    package_fail=0
    if [[ -f "$log_file" ]]; then
      package_ok="$(grep -c '^ok[[:space:]]' "$log_file" 2>/dev/null || true)"
      package_fail="$(grep -c '^FAIL[[:space:]]' "$log_file" 2>/dev/null || true)"
      package_total=$((package_ok + package_fail))
    fi

    cat >> "$summary_file" <<EOF
# $(status_icon) Test Summary

The Go test suite and web asset build ran with the same dependency shape used by the build jobs.

### 🧪 Test Results

| Metric | Value |
| --- | ---: |
| Passing packages | ${package_ok} |
| Failing packages | ${package_fail} |
| Packages observed | ${package_total} |
| Test log | \`${log_file}\` |

EOF
    append_context
    append_toolchain
    if [[ -f "$log_file" ]]; then
      cat >> "$summary_file" <<EOF
<details>
<summary>🧾 Final test log lines</summary>

\`\`\`text
EOF
      tail -n 80 "$log_file" >> "$summary_file" 2>/dev/null || true
      cat >> "$summary_file" <<EOF
\`\`\`

</details>

EOF
    else
      cat >> "$summary_file" <<EOF
> ⚠️ The test log file was not produced, which usually means an earlier setup step failed.

EOF
    fi
    append_footer
    ;;

  release)
    title="${1:-Release summary}"
    description="${2:-Release was prepared.}"
    shift 2 || true
    files=("$@")
    tag="${RELEASE_TAG:-${GITHUB_REF_NAME:-${GITHUB_REF:-local}}}"
    tag="${tag##*/}"
    release_url="${server_url}/${repo}/releases/tag/${tag}"
    if [[ "$job_status" == "success" ]]; then
      tag_value="[\`${tag}\`](${release_url})"
      notes_value="Curated from \`CHANGELOG.md\` and completed with GitHub's merged-change history"
      source_value="Tagged source builds packaged and verified in this workflow"
    else
      tag_value="\`${tag}\` (publication not confirmed)"
      notes_value="Candidate notes were generated; inspect the failed job before retrying"
      source_value="Candidate files from the failed publication job"
    fi

    cat >> "$summary_file" <<EOF
# $(status_icon) ${title}

${description}

### 🚀 Release Package

| Property | Value |
| --- | --- |
| Tag | ${tag_value} |
| Release notes | ${notes_value} |
| Download source | ${source_value} |

EOF
    if [[ "$job_status" == "success" ]]; then
      cat >> "$summary_file" <<EOF
### Durable downloads

| Asset | Download |
| --- | --- |
EOF
      for path in "${files[@]}"; do
        [[ -f "$path" ]] || continue
        base="${path##*/}"
        printf '| `%s` | [GitHub Release asset](%s/releases/download/%s/%s) |\n' \
          "$base" "$repo_url" "$tag" "$base" >> "$summary_file"
      done
    else
      cat >> "$summary_file" <<'EOF'
### Candidate files

The files below were produced locally in the job but are not presented as durable release downloads because publication did not succeed.

EOF
    fi
    cat >> "$summary_file" <<'EOF'

### Install

- Windows: extract the complete `windows-amd64.zip` archive and keep all DLLs beside `patris-export.exe`.
- Linux: extract the `linux-amd64.tar.gz` archive and run `run-patris-export.sh`.
- Verify either archive against `SHA256SUMS` before installation.

EOF
    append_context
    append_build_metadata
    append_artifact_table "release-${tag}" "${files[@]}"
    append_integrity_commands "${files[@]}"
    append_footer
    ;;

  *)
    cat >&2 <<EOF
Usage:
  $0 build "Title" "Description" "artifact-name" "build-kind" <files...>
  $0 test <test-log>
  $0 release "Title" "Description" <files...>
EOF
    exit 2
    ;;
esac
