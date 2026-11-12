#!/usr/bin/env bash
# Fetch a prebuilt reeve binary so the GitHub Action can skip the source
# build. Called by action.yml on a cache miss; never fails the job - every
# error path logs why and reports fetched=false so the action falls back to
# building from source.
#
# Ref semantics (the wrapper's action ref or reusable workflow SHA):
#   vX.Y.Z[-pre]  -> that release's goreleaser tarball, verified against the
#                    release's checksums.txt (signed release pipeline).
#   master | next -> the per-push prerelease whose signed source hash matches
#                    the action source already on disk.
#   full git SHA  -> that commit's retained per-push prerelease when available.
#   anything else -> source build (feature branches, forks).
#
# Inputs (env):
#   REEVE_REF      exact action ref or reusable workflow SHA
#   REEVE_REPO     action or reusable workflow owner/repo
#   REEVE_SOURCE_HASH canonical hash of the action source already on disk
#   REEVE_OS       runner.os   (Linux, macOS, Windows)
#   REEVE_ARCH     runner.arch (X64, ARM64, ...)
#   REEVE_DEST     where to place the binary (the cached path)
#   GH_TOKEN       token for gh (dodges rate limits; repo is public)
#
# Output: fetched=true|false appended to $GITHUB_OUTPUT (stdout if unset).
set -euo pipefail

# classify_ref <ref> -> version | edge | commit | other
classify_ref() {
  local ref="${1:-}"
  if [[ "$ref" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
    echo version
  elif [[ "$ref" == "master" || "$ref" == "next" ]]; then
    echo edge
  elif [[ "$ref" =~ ^[0-9a-fA-F]{40}$ ]]; then
    echo commit
  else
    echo other
  fi
}

# prebuilt_eligible reports whether this invocation can possibly use a
# published binary. It performs no network access and keeps unsupported refs
# and platforms from installing the verifier before the source-build fallback.
prebuilt_eligible() {
  local ref="${REEVE_REF:-}" repo="${REEVE_REPO:-}" kind
  [[ -n "$ref" && -n "$repo" ]] || return 1
  [[ "${REEVE_OS:-}" == "Linux" ]] || return 1
  [[ -n "$(map_arch "${REEVE_ARCH:-}")" ]] || return 1
  kind=$(classify_ref "$ref")
  [[ "$kind" != "other" ]] || return 1
  [[ "$kind" == "version" || "${REEVE_SOURCE_HASH:-}" =~ ^[0-9a-f]{64}$ ]]
}

classify_main() {
  local out="${GITHUB_OUTPUT:-/dev/stdout}"
  if prebuilt_eligible; then
    echo "eligible=true" >> "$out"
  else
    echo "eligible=false" >> "$out"
  fi
}

# verify_source_hash <source-hash-file>
# Requires the signed release metadata to identify the exact source tree
# already checked out for this action invocation.
verify_source_hash() {
  local file="$1" expected="${REEVE_SOURCE_HASH:-}" actual
  if [[ ! "$expected" =~ ^[0-9a-f]{64}$ ]]; then
    echo "canonical action source hash is unavailable or invalid" >&2
    return 1
  fi
  actual=$(tr -d '[:space:]' < "$file")
  if [[ "$actual" != "$expected" ]]; then
    echo "release source hash mismatch: expected $expected, got $actual" >&2
    return 1
  fi
}

# map_arch <runner.arch> -> amd64 | arm64 | "" (unsupported)
map_arch() {
  case "${1:-}" in
    X64) echo amd64 ;;
    ARM64) echo arm64 ;;
    *) echo "" ;;
  esac
}

# checksum_entry <file> <sums-file>: prints the recorded digest for file's
# basename, or "" when the checksums file lists no entry for it.
checksum_entry() {
  local base
  base=$(basename "$1")
  awk -v f="$base" '$2 == f || $2 == "*" f {print $1; exit}' "$2"
}

verify_sha256() {
  local file="$1" sums="$2" base expected actual
  base=$(basename "$file")
  expected=$(awk -v f="$base" '$2 == f || $2 == "*" f {print $1; exit}' "$sums")
  if [[ -z "$expected" ]]; then
    echo "no checksum entry for $base in $(basename "$sums")" >&2
    return 1
  fi
  actual=$(sha256sum "$file" | cut -d' ' -f1)
  if [[ "$expected" != "$actual" ]]; then
    echo "checksum mismatch for $base: expected $expected, got $actual" >&2
    return 1
  fi
}

# verify_signature <checksums-file> <bundle-file>
# Verifies the cosign keyless bundle over the checksums file. The checksum
# already chains every artifact to this file, so signing it signs the set.
# A valid bundle and cosign are mandatory. Any failure falls back to a source
# build, so an unverified binary never reaches the action cache.
verify_signature() {
  local sums="$1" bundle="$2"
  if ! command -v cosign > /dev/null 2>&1; then
    echo "cosign is not installed; cannot verify release signature" >&2
    return 1
  fi
  if [[ ! -f "$bundle" ]]; then
    echo "release published no signature bundle" >&2
    return 1
  fi
  # Keyless identity: any workflow in the source repo, GitHub's OIDC issuer.
  local repo="${REEVE_REPO:-}" id_re
  if [[ -n "$repo" ]]; then
    id_re="^https://github.com/${repo}/\.github/workflows/.+@refs/(heads|tags)/.+$"
  else
    id_re="^https://github.com/.+/\.github/workflows/.+@refs/(heads|tags)/.+$"
  fi
  if cosign verify-blob \
    --bundle "$bundle" \
    --certificate-identity-regexp "$id_re" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "$sums" > /dev/null 2>&1; then
    echo "signature verified (cosign keyless)"
    return 0
  fi
  echo "signature verification FAILED for $(basename "$sums")" >&2
  return 1
}

# fetch_main resolves and verifies the prebuilt binary.
#
# Verification is MANDATORY and against the LIVE signed checksums:
#   1. checksums.txt and its cosign bundle are fetched fresh each run and
#      the signature verified - a failure here fails the run (exit 1, the
#      reason on stderr); nothing unverified is consumed.
#   2. the archive - cached from an earlier run or downloaded fresh - MUST
#      digest-match the verified checksums. A mismatch fails the run with
#      the expected digest, the actual digest, and the cache key. No silent
#      fresh re-download, no unverified use.
#   3. a fresh download that verifies is stored into the cache dir
#      (REEVE_ARCHIVE_DIR) for the next run.
# A verified artifact that is simply not for this action source (source-hash
# mismatch) still falls back to a source build with the reason: nothing was
# consumed, and the wrong-artifact case is a selector problem, not a
# tampering signal.
fetch_main() {
  local out="${GITHUB_OUTPUT:-/dev/stdout}"
  local ref="${REEVE_REF:-}" repo="${REEVE_REPO:-}"
  local dest="${REEVE_DEST:?REEVE_DEST is required}"
  local archive_dir="${REEVE_ARCHIVE_DIR:-}"
  local cache_key="${REEVE_CACHE_KEY:-}"
  local arch kind workdir asset sums sums_dir source_hash_file tag expected actual

  fallback() {
    echo "$1 - falling back to source build"
    echo "fetched=false" >> "$out"
  }

  # The ONE hard failure: a digest mismatch against the verified signed
  # checksums - a tamper signal. Nothing was or will be consumed, and the
  # reason lands on stderr before the run fails.
  verify_hard_fail() {
    echo "prebuilt verification failed: $1" >&2
    echo "fetched=false" >> "$out"
    exit 1
  }

  # Never hardcode the repo: forks and local runs (empty contexts) degrade
  # gracefully to the source build.
  if [[ -z "$ref" || -z "$repo" ]]; then
    fallback "action ref/repository not available"
    return 0
  fi
  if [[ "${REEVE_OS:-}" != "Linux" ]]; then
    fallback "prebuilt binaries are only published for Linux (runner.os=${REEVE_OS:-unset})"
    return 0
  fi
  arch=$(map_arch "${REEVE_ARCH:-}")
  if [[ -z "$arch" ]]; then
    fallback "unsupported runner.arch '${REEVE_ARCH:-unset}'"
    return 0
  fi
  if ! command -v gh > /dev/null 2>&1; then
    fallback "gh CLI not available"
    return 0
  fi

  kind=$(classify_ref "$ref")
  if [[ "$kind" == "other" ]]; then
    fallback "ref '$ref' is not a release tag, edge branch, or full commit SHA"
    return 0
  fi

  if [[ "$kind" != "version" && ! "${REEVE_SOURCE_HASH:-}" =~ ^[0-9a-f]{64}$ ]]; then
    fallback "canonical action source hash is unavailable or invalid"
    return 0
  fi
  if [[ "$kind" == "commit" ]]; then
    ref=${ref,,}
  fi

  workdir=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$workdir'" EXIT

  case "$kind" in
    version)
      asset="reeve_${ref#v}_linux_${arch}.tar.gz"
      sums="checksums.txt"
      tag="$ref"
      ;;
    edge)
      asset="reeve_linux_${arch}.tar.gz"
      sums="checksums.txt"
      source_hash_file="source-hash.txt"
      tag=$(gh api "repos/${repo}/releases?per_page=100" \
        --jq "[.[] | select(.prerelease and (.tag_name | startswith(\"${ref}-\")) and ((.body // \"\") | contains(\"reeve-source-hash: ${REEVE_SOURCE_HASH}\")))] | sort_by(.created_at) | reverse | .[0].tag_name" 2> /dev/null || true)
      if [[ -z "$tag" || "$tag" == "null" ]]; then
        fallback "no ${ref}-* prerelease matches this action source (edge build may still be running)"
        return 0
      fi
      asset="reeve_linux_${arch}.tar.gz"
      ;;
    commit)
      sums="checksums.txt"
      source_hash_file="source-hash.txt"
      tag=$(gh api "repos/${repo}/releases?per_page=100" \
        --jq "[.[] | select(.prerelease and .target_commitish == \"${ref}\" and ((.body // \"\") | contains(\"reeve-source-hash: ${REEVE_SOURCE_HASH}\")))] | sort_by(.created_at) | reverse | .[0].tag_name" 2> /dev/null || true)
      if [[ -z "$tag" || "$tag" == "null" ]]; then
        fallback "no retained prerelease matches commit $ref and this action source"
        return 0
      fi
      asset="reeve_linux_${arch}.tar.gz"
      ;;
  esac

  sums_dir="$workdir"
  local expected actual sums_log asset_log cached_file
  # The cache stores one archive per RESOLVED release tag: an archive cached
  # for a different tag (edge resolves a new prerelease for the same source
  # hash) is a cache MISS, never a mismatch. Only a mismatch for the same
  # tag is the hard tamper failure.
  cached_file="$archive_dir/$(printf '%s' "$tag" | sed 's|[^A-Za-z0-9._-]|_|g').tar.gz"

  # 1. LIVE checksums + bundle, every run. Nothing is consumed without
  # them. Their fetch failure is a SOFT fallback stating the reason (gh's
  # real error surfaces); the cached archive is ignored.
  echo "Fetching signed checksums for $tag from $repo"
  if ! sums_log=$(gh release download "$tag" --repo "$repo" \
    --pattern "$sums" --pattern "${sums}.bundle" --dir "$sums_dir" \
    ${source_hash_file:+ --pattern "$source_hash_file"} \
    --clobber 2>&1); then
    fallback "checksums.txt or its signature could not be fetched for $tag (gh: $sums_log). The cached archive is ignored for this run; nothing was used"
    return 0
  fi

  # 2. Signature verified against the LIVE bundle. An unverifiable signature
  # (bundle missing, cosign unavailable, sig invalid) is a SOFT fallback
  # with the reason; the cached archive is ignored.
  if ! verify_signature "$sums_dir/$sums" "$sums_dir/${sums}.bundle"; then
    fallback "$sums for $tag could not be verified; the cached archive is ignored for this run and nothing was consumed"
    return 0
  fi

  # Edge/commit: bind the artifact to this exact action source (the signed
  # checksums cover source-hash.txt). A mismatch is a selector problem:
  # soft fallback with the reason.
  if [[ -n "$source_hash_file" ]]; then
    if [[ -f "$sums_dir/$source_hash_file" ]]; then
      if ! verify_sha256 "$sums_dir/$source_hash_file" "$sums_dir/$sums"; then
        fallback "the fetched source-hash.txt does not match the signed checksums for $tag; the cached archive is ignored for this run"
        return 0
      fi
    fi
    REEVE_SOURCE_HASH="${REEVE_SOURCE_HASH:-}" verify_source_hash "$sums_dir/$source_hash_file" || true
    if [[ "$(cat "$sums_dir/$source_hash_file" 2>/dev/null)" != "${REEVE_SOURCE_HASH:-}" ]]; then
      fallback "release source hash does not match this action source; the cached archive is ignored for this run"
      return 0
    fi
  fi

  # 3. The archive for the resolved tag: cached or fresh. A digest mismatch
  # against the verified signed checksums is the ONE tamper signal and
  # hard-fails the run. Everything else falls back with the reason; the
  # cached archive is never used unverified.
  local source_archive="$workdir/$asset"
  if [[ -f "$cached_file" ]]; then
    source_archive="$cached_file"
    echo "Using cached prebuilt archive for $tag (re-verified against the live signed checksums)"
  else
    echo "Downloading $asset from $repo"
    if ! asset_log=$(gh release download "$tag" --repo "$repo" \
      --pattern "$asset" --dir "$workdir" 2>&1); then
      fallback "release asset $asset could not be fetched for $tag (gh: $asset_log). The cached archive for other tags is untouched; nothing was used"
      return 0
    fi
  fi
  expected=$(checksum_entry "$asset" "$sums_dir/$sums")
  if [[ -z "$expected" ]]; then
    fallback "checksums.txt for $tag lists no entry for $asset (the release artifact set does not match); the cached archive is ignored for this run"
    return 0
  fi
  actual=$(sha256sum "$source_archive" | cut -d' ' -f1)
  if [[ "$expected" != "$actual" ]]; then
    verify_hard_fail "digest mismatch for the $([ "$source_archive" == "$cached_file" ] && echo cached || echo "freshly downloaded") archive of $tag (cache key \"$cache_key\"): expected $expected, got $actual. The archive does not match the SIGNED checksums of $tag; it was not extracted. This is a tamper signal - clear the cache and investigate"
  fi
  if ! tar -xzf "$source_archive" -C "$workdir" reeve; then
    if [[ "$source_archive" == "$cached_file" ]]; then
      rm -f "$cached_file"
    fi
    fallback "the archive for $tag could not be extracted; nothing was executed; a broken cached archive is removed"
    return 0
  fi
  mkdir -p "$(dirname "$dest")"
  install -m 0755 "$workdir/reeve" "$dest"
  # Sanity-run the binary: a digest-verified binary that will not execute
  # falls back to a source build with the reason (and a cached archive is
  # dropped so the next run re-downloads).
  if ! "$dest" --version > /dev/null 2>&1; then
    rm -f "$dest"
    if [[ "$source_archive" == "$cached_file" ]]; then
      rm -f "$cached_file"
    fi
    fallback "the prebuilt binary for $tag does not execute (reeve --version failed); nothing ran"
    return 0
  fi
  # A freshly downloaded archive that verified and executes is stored for
  # the next run, under the resolved release tag. Source builds are never
  # cached - only this verified-download path writes the cache.
  if [[ "$source_archive" != "$cached_file" && -n "$archive_dir" ]]; then
    mkdir -p "$archive_dir"
    cp "$source_archive" "$cached_file"
  fi
  echo "Using prebuilt binary: $("$dest" --version)"
  echo "fetched=true" >> "$out"
  return 0
}

# Run only when executed, so tests can source the helper functions.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  if [[ "${1:-}" == "classify" ]]; then
    classify_main
  else
    fetch_main
  fi
fi
