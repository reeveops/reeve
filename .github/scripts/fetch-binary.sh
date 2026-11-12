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

# verify_signature <checksums-file> <bundle-file> <id-re> <required-id> <what>
# Verifies the cosign keyless bundle over the checksums file. The checksum
# already chains every artifact to this file, so signing it signs the set.
# A valid bundle and cosign are mandatory. Any failure falls back to a source
# build, so an unverified binary never reaches the action cache.
#
# The identity is scoped per artifact kind - the signing run must be the one
# that actually produced the artifact:
#   version tag (stable/rc): release.yml@refs/tags/<requested version>
#   per-commit master/edge:  edge-build.yml@refs/heads/master
# A signature from any other workflow or ref (e.g. v0.8.0 and earlier, signed
# from master) is refused and names what was required and found.
verify_signature() {
  local sums="$1" bundle="$2" id_re="$3" required_id="$4" what="$5"
  if ! command -v cosign > /dev/null 2>&1; then
    echo "signature verification FAILED for ${what}: no verifier available" >&2
    echo "  rule: the signing certificate must identify ${required_id}" >&2
    echo "  found: nothing - cosign is not installed" >&2
    echo "  next step: the prebuilt binary is NOT used; this run builds reeve from source" >&2
    return 1
  fi
  if [[ ! -f "$bundle" ]]; then
    echo "signature verification FAILED for ${what}: the release published no signature bundle" >&2
    echo "  rule: the signing certificate must identify ${required_id}" >&2
    echo "  found: nothing - no bundle to check" >&2
    echo "  next step: the prebuilt binary is NOT used; this run builds reeve from source" >&2
    return 1
  fi
  # GitHub's OIDC issuer everywhere, including GHES installs of reeve-proper;
  # the repo part is derived from the source repository.
  if ! cosign_out=$(cosign verify-blob \
    --bundle "$bundle" \
    --certificate-identity-regexp "$id_re" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "$sums" 2>&1); then
    echo "signature verification FAILED for ${what} ($sums)" >&2
    echo "  rule: the signing certificate must identify ${required_id}" >&2
    echo "  found (cosign):" >&2
    printf '  %s\n' "$cosign_out" >&2
    echo "  next step: the prebuilt binary is NOT used; this run builds reeve from source. Releases signed by a different workflow or ref than the required identity (v0.8.0 and earlier were signed from refs/heads/master) never verify." >&2
    return 1
  fi
  echo "signature verified (cosign keyless)"
  return 0
}

# re_quote escapes regular-expression metacharacters so a literal repo, tag
# or version can anchor an identity regexp.
re_quote() {
  printf '%s' "$1" | sed 's/[][\.*^$()+?{|}]/\\&/g'
}

# required_identity <kind> <ref> <repo> <id-re-var> <id-desc-var> <what-var>
# Sets the per-kind signing identity: regexp, human description, and the
# artifact label the refusal renders.
required_identity() {
  local kind="$1" ref="$2" repo="$3" repo_re
  repo_re=$(re_quote "${repo:-.+}")
  case "$kind" in
    version)
      printf -v "$4" '%s' "^https://github\.com/${repo_re}/\.github/workflows/release\.yml@refs/tags/$(re_quote "$ref")$"
      printf -v "$5" '%s' "release.yml@refs/tags/${ref}"
      printf -v "$6" '%s' "version ${ref}"
      ;;
    edge | commit)
      printf -v "$4" '%s' "^https://github\.com/${repo_re}/\.github/workflows/edge-build\.yml@refs/heads/master$"
      printf -v "$5" '%s' "edge-build.yml@refs/heads/master"
      printf -v "$6" '%s' "per-commit master build ${ref}"
      ;;
    *)
      return 1
      ;;
  esac
}

fetch_main() {
  local out="${GITHUB_OUTPUT:-/dev/stdout}"
  local ref="${REEVE_REF:-}" repo="${REEVE_REPO:-}"
  local dest="${REEVE_DEST:?REEVE_DEST is required}"
  local arch kind workdir asset sums source_hash_file tag ok=false reason=''

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
    fallback "ref '$ref' is not a release tag, edge branch, or full commit SHA"
    return 0
  fi

  # The signing identity is scoped to the artifact kind: a version tag must
  # be signed by a run on its own tag, a per-commit build by the edge
  # workflow on master. Everything downstream verifies against exactly that.
  local id_re id_desc what
  required_identity "$kind" "$ref" "$repo" id_re id_desc what

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
      source_hash_file="source-hash.txt"
      # Resolve the release for the source tree GitHub already checked out.
      # A branch may advance during a run, so "newest" alone is not enough.
      tag=$(gh api "repos/${repo}/releases?per_page=100" \
        --jq "[.[] | select(.prerelease and (.tag_name | startswith(\"${ref}-\")) and ((.body // \"\") | contains(\"reeve-source-hash: ${REEVE_SOURCE_HASH}\")))] | sort_by(.created_at) | reverse | .[0].tag_name" 2> /dev/null || true)
      if [[ -z "$tag" || "$tag" == "null" ]]; then
        echo "no ${ref}-* prerelease matches this action source (edge build may still be running)" >&2
      else
        echo "Edge ref '$ref': source-matched prerelease is $tag; downloading $asset from $repo"
        if gh release download "$tag" --repo "$repo" \
          --pattern "$asset" --pattern "$sums" --pattern "${sums}.bundle" \
          --pattern "$source_hash_file" --dir "$workdir"; then
          ok=true
        else
          echo "download failed for $tag (missing asset or network error)" >&2
        fi
      fi
      ;;
    commit)
      asset="reeve_linux_${arch}.tar.gz"
      sums="checksums.txt"
      source_hash_file="source-hash.txt"
      tag=$(gh api "repos/${repo}/releases?per_page=100" \
        --jq "[.[] | select(.prerelease and .target_commitish == \"${ref}\" and ((.body // \"\") | contains(\"reeve-source-hash: ${REEVE_SOURCE_HASH}\")))] | sort_by(.created_at) | reverse | .[0].tag_name" 2> /dev/null || true)
      if [[ -z "$tag" || "$tag" == "null" ]]; then
        echo "no retained prerelease matches commit $ref and this action source" >&2
      else
        echo "Commit ref '${ref:0:12}': prerelease is $tag; downloading $asset from $repo"
        if gh release download "$tag" --repo "$repo" \
          --pattern "$asset" --pattern "$sums" --pattern "${sums}.bundle" \
          --pattern "$source_hash_file" --dir "$workdir"; then
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
      reason="the downloaded archive does not match its checksums entry"
    elif ! verify_signature "$workdir/$sums" "$workdir/${sums}.bundle" "$id_re" "$id_desc" "$what"; then
      ok=false
      reason="the checksums signature does not come from ${id_desc}"
    elif [[ "$kind" != "version" ]] && ! verify_sha256 "$workdir/$source_hash_file" "$workdir/$sums"; then
      ok=false
      reason="the fetched source-hash.txt does not match the signed checksums"
    elif [[ "$kind" != "version" ]] && ! verify_source_hash "$workdir/$source_hash_file"; then
      ok=false
      reason="the release source hash does not match this action source"
    elif ! tar -xzf "$workdir/$asset" -C "$workdir" reeve; then
      echo "could not extract reeve from $asset" >&2
      ok=false
      reason="the archive could not be extracted"
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

  fallback "prebuilt fetch failed: ${reason:-see the log above}"
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
