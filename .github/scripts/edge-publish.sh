#!/usr/bin/env bash
# edge-publish.sh completes the per-push edge prerelease's artifact upload.
#
# The parent workflow has just created release $TAG (or raced a concurrent
# push and found it existing). This script decides between three states:
#   1. every expected asset present -> skip with the visible note; never
#      overwrite (builds embed the build date, so a re-upload would replace
#      signed artifacts with differently-signed bytes).
#   2. any asset missing      -> upload ONLY the missing files (completing a
#      partial upload never re-uploads what is already there).
#   3. the asset listing itself fails -> exit 1 with the reason: without the
#      listing we cannot tell complete from partial, and guessing would risk
#      either a duplicate upload or a silent skip.
# Usage: edge-publish.sh <tag> <commit> <dist-dir>   (releases in
# GITHUB_REPOSITORY; artifacts named dist/<asset>).
set -euo pipefail

edge_publish_main() {
  local tag="$1" commit="$2" dist="$3"
  local repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

  local expected=(
    "reeve_${commit}_linux_amd64.tar.gz"
    "reeve_${commit}_linux_arm64.tar.gz"
    "source-hash.txt"
    "checksums.txt"
    "checksums.txt.bundle"
  )

  # The listing decision must be made from REAL data: capture the jq output
  # and compare it, never gh's exit code - gh exits 0 whenever the release
  # exists, whatever the jq expression prints.
  local names names_log
  if ! names_log=$(gh release view "$tag" --repo "$repo" --json assets \
    --jq '.assets | map(.name) | join(",")' 2>&1); then
    echo "::error::cannot list the assets of release ${tag} (gh: ${names_log}). Without the listing this script cannot tell a complete release from a partial one, so it neither skips nor uploads; fix the release or the token and re-run" >&2
    exit 1
  fi
  names=",${names_log},"

  local missing=()
  local have=()
  local a
  for a in "${expected[@]}"; do
    if [[ "$names" == *",$a,"* ]]; then
      have+=("$a")
    else
      missing+=("$a")
    fi
  done

  if [[ ${#have[@]} -eq ${#expected[@]} ]]; then
    echo "edge release ${tag} already carries its artifacts; not overwriting"
    return 0
  fi

  if [[ ${#missing[@]} -lt ${#expected[@]} ]]; then
    echo "edge release ${tag} is missing ${#missing[@]} asset(s): ${missing[*]} - completing the upload (already-present assets are never re-uploaded)"
  else
    echo "edge release ${tag} is fresh - uploading all ${#missing[@]} artifact(s)"
  fi

  local files=()
  for a in "${missing[@]}"; do
    files+=("$dist/$a")
  done

  local upload_log
  if ! upload_log=$(gh release upload "$tag" --repo "$repo" "${files[@]}" 2>&1); then
    echo "::error::uploading ${#missing[@]} asset(s) to release ${tag} failed (gh: ${upload_log}). The release is left as-is - an asset set that is still partial completes on the next run of this commit" >&2
    exit 1
  fi
}

# Run only when executed, so tests can source the helper functions.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  edge_publish_main "$@"
fi
