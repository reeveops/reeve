#!/usr/bin/env bash
# Fetch a prebuilt reeve binary so the GitHub Action can skip the source
# build. Called by action.yml on a cache miss; never fails the job - every
# error path logs why and reports fetched=false so the action falls back to
# building from source.
#
# Ref semantics (github.action_ref):
#   vX.Y.Z        -> that release's goreleaser tarball, verified against the
#                    release's checksums.txt (signed release pipeline).
#   master | next -> the newest <branch>-<sha> per-push prerelease published by
#                    edge-build.yml; download its linux binary + checksum. No
#                    matching prerelease yet (edge build still running) falls
#                    back to source.
#   anything else -> source build (SHA pins, feature branches, forks).
#
# Inputs (env):
#   REEVE_REF      github.action_ref       (may be empty, e.g. local runs)
#   REEVE_REPO     github.action_repository ("owner/repo"; empty on some runners)
#   REEVE_OS       runner.os   (Linux, macOS, Windows)
#   REEVE_ARCH     runner.arch (X64, ARM64, ...)
#   REEVE_DEST     where to place the binary (the cached path)
#   GH_TOKEN       token for gh (dodges rate limits; repo is public)
#
# Output: fetched=true|false appended to $GITHUB_OUTPUT (stdout if unset).
set -euo pipefail

# classify_ref <ref> -> version | edge | other
classify_ref() {
  local ref="${1:-}"
  if [[ "$ref" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo version
  elif [[ "$ref" == "master" || "$ref" == "next" ]]; then
    echo edge
  else
    echo other
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

# verify_sha256 <file> <checksums-file>
# Checks <file> against its (basename-keyed) line in a sha256sum-format
# checksums file. Returns non-zero on a missing entry or a mismatch.
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
# Default is best-effort: without cosign or a published bundle we proceed on
# the checksum alone (so existing consumers are unaffected), but a bundle
# that is PRESENT and fails to verify is always rejected. Set
# REEVE_REQUIRE_SIGNATURE=1 to make a valid signature mandatory.
verify_signature() {
  local sums="$1" bundle="$2"
  local require="${REEVE_REQUIRE_SIGNATURE:-}"
  if ! command -v cosign > /dev/null 2>&1; then
    if [[ "$require" == "1" ]]; then
      echo "REEVE_REQUIRE_SIGNATURE=1 but cosign is not installed; cannot verify signature" >&2
      return 1
    fi
    echo "cosign not installed; skipping signature check (checksum still enforced)" >&2
    return 0
  fi
  if [[ ! -f "$bundle" ]]; then
    if [[ "$require" == "1" ]]; then
      echo "REEVE_REQUIRE_SIGNATURE=1 but this release published no signature bundle" >&2
      return 1
    fi
    echo "no signature bundle for this release; skipping signature check" >&2
    return 0
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

fetch_main() {
  local out="${GITHUB_OUTPUT:-/dev/stdout}"
  local ref="${REEVE_REF:-}" repo="${REEVE_REPO:-}"
  local dest="${REEVE_DEST:?REEVE_DEST is required}"
  local arch kind workdir asset sums ok=false

  fallback() {
    echo "$1 - falling back to source build"
    echo "fetched=false" >> "$out"
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
    fallback "ref '$ref' is not a release tag or edge branch"
    return 0
  fi

  workdir=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$workdir'" EXIT

  case "$kind" in
    version)
      asset="reeve_${ref#v}_linux_${arch}.tar.gz"
      sums="checksums.txt"
      echo "Pinned release $ref: downloading $asset from $repo"
      if gh release download "$ref" --repo "$repo" \
        --pattern "$asset" --pattern "$sums" --pattern "${sums}.bundle" --dir "$workdir"; then
        ok=true
      else
        echo "download failed (missing release/asset, or network error)" >&2
      fi
      ;;
    edge)
      asset="reeve_linux_${arch}.tar.gz"
      sums="checksums.txt"
      # Resolve the newest <branch>-<sha> per-push prerelease. gh returns
      # releases newest-first, so pick the first prerelease whose tag is
      # prefixed with "<ref>-".
      local tag
      tag=$(gh api "repos/${repo}/releases?per_page=100" \
        --jq "[.[] | select(.prerelease and (.tag_name | startswith(\"${ref}-\")))] | sort_by(.created_at) | reverse | .[0].tag_name" 2> /dev/null || true)
      if [[ -z "$tag" || "$tag" == "null" ]]; then
        echo "no ${ref}-* prerelease found (edge build may still be running)" >&2
      else
        echo "Edge ref '$ref': newest prerelease is $tag; downloading $asset from $repo"
        if gh release download "$tag" --repo "$repo" \
          --pattern "$asset" --pattern "$sums" --pattern "${sums}.bundle" --dir "$workdir"; then
          ok=true
        else
          echo "download failed for $tag (missing asset or network error)" >&2
        fi
      fi
      ;;
  esac

  if [[ "$ok" == true ]]; then
    if ! verify_sha256 "$workdir/$asset" "$workdir/$sums"; then
      ok=false
    elif ! verify_signature "$workdir/$sums" "$workdir/${sums}.bundle"; then
      ok=false
    elif ! tar -xzf "$workdir/$asset" -C "$workdir" reeve; then
      echo "could not extract reeve from $asset" >&2
      ok=false
    fi
  fi

  if [[ "$ok" == true ]]; then
    mkdir -p "$(dirname "$dest")"
    install -m 0755 "$workdir/reeve" "$dest"
    # Sanity-run the binary; a broken download must not poison the cache.
    if ! "$dest" --version > /dev/null 2>&1; then
      rm -f "$dest"
      fallback "downloaded binary failed to execute"
      return 0
    fi
    echo "Using prebuilt binary: $("$dest" --version)"
    echo "fetched=true" >> "$out"
    return 0
  fi

  fallback "prebuilt fetch failed"
  return 0
}

# Run only when executed, so tests can source the helper functions.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  fetch_main
fi
