#!/usr/bin/env bash
# edge-publish.sh completes the per-push edge prerelease's artifact upload.
#
# The parent workflow has just created release $TAG (or raced a concurrent
# push and found it existing). The decision is keyed on the checksums
# bundle - the LAST asset a consumer needs before it can verify anything:
#
#   1. every expected asset present -> skip with the visible note; never
#      overwrite (builds embed the build date, so a re-upload would replace
#      signed artifacts with differently-signed bytes).
#   2. checksums.txt.bundle ABSENT (whatever else is there) -> no consumer
#      can have verified this release: delete every present asset and
#      upload the current run's complete set, naming what was replaced.
#      The upload runs in TWO calls - the four non-bundle assets, then the
#      bundle alone LAST (gh uploads several files in parallel, so argument
#      order guarantees nothing) - so a run that dies midway always leaves
#      the "bundle absent" state, which this policy replaces on the next
#      run. Each call's failure message says the same.
#   3. bundle present but another asset missing -> the release is
#      inconsistent and may already have been consumed: FAIL with the
#      reason naming the missing assets. Do not upload, do not delete -
#      silently replacing a set a consumer may have verified is the tamper
#      the consumer's check exists to catch.
#   4. the listing itself fails -> exit 1 with the reason: without the
#      listing we cannot tell complete from partial from inconsistent.
#   5. an upload or delete failure -> exit 1 with the reason. A first-call
#      (four assets) failure or a bundle-upload failure both leave the
#      release in the replaceable ("bundle absent") state.
#
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
  local bundle="checksums.txt.bundle"

  # The listing decision must be made from REAL data: capture the jq output
  # and compare it, never gh's exit code - gh exits 0 whenever the release
  # exists, whatever the jq expression prints.
  local names names_log
  if ! names_log=$(gh release view "$tag" --repo "$repo" --json assets \
    --jq '.assets | map(.name) | join(",")' 2>&1); then
    echo "::error::cannot list the assets of release ${tag} (gh: ${names_log}). Without the listing this script cannot tell a complete release from a partial from an inconsistent one, so it neither skips nor uploads; fix the release or the token and re-run" >&2
    exit 1
  fi
  names=",${names_log},"

  local present=() missing=() a
  local bundle_present=false
  for a in "${expected[@]}"; do
    if [[ "$names" == *",$a,"* ]]; then
      present+=("$a")
      if [[ "$a" == "$bundle" ]]; then
        bundle_present=true
      fi
    else
      missing+=("$a")
    fi
  done

  # 1. Everything present: skip, never overwrite.
  if [[ ${#missing[@]} -eq 0 ]]; then
    echo "edge release ${tag} already carries its artifacts; not overwriting"
    return 0
  fi

  # 3. Bundle present but something else missing: inconsistent, possibly
  # already consumed. Fail; do not touch the release.
  if [[ "$bundle_present" == true ]]; then
    echo "::error::edge release ${tag} is INCONSISTENT: checksums.txt.bundle is published but ${#missing[@]} asset(s) are missing: ${missing[*]}. A consumer may already have verified artifacts against this bundle, so this run neither deletes nor uploads - silently replacing that set is the tamper the consumer's check exists to catch. Investigate the failed upload that produced this state; if it cannot be completed, delete the whole release and let the next push publish a fresh one" >&2
    exit 1
  fi

  # 2. Bundle absent: nothing can have been verified. Replace whatever is
  # there with the current run's complete set - the earlier run's archives
  # do not match this run's signed checksums - and say so, naming them.
  if [[ ${#present[@]} -eq 0 ]]; then
    echo "edge release ${tag} has no bundle and no assets - uploading the full set from this run"
  else
    echo "edge release ${tag} has no checksums.txt.bundle, so no consumer can have verified it - replacing the ${#present[@]} asset(s) from an earlier run (their bytes predate this build): ${present[*]}"
    local a2 del_log
    for a2 in "${present[@]}"; do
      if ! del_log=$(gh release delete-asset "$tag" "$a2" --repo "$repo" --yes 2>&1); then
        echo "::error::deleting stale asset ${a2} from release ${tag} failed (gh: ${del_log}). The release stays in the replaceable (bundle-absent) state; the next run of this commit retries the replacement" >&2
        exit 1
      fi
    done
  fi

  # Upload in TWO calls. `gh release upload` with several files uploads
  # them in PARALLEL (its internal errgroup concurrency is 5), so argument
  # order guarantees nothing about completion order. The four non-bundle
  # assets upload first; the bundle is its own SECOND call, only after the
  # first succeeded - a run that dies at any point never publishes the
  # bundle on top of stale or half-replaced assets, so the release always
  # stays in one of the states this policy handles (complete or
  # bundle-absent), never inconsistent.
  local files=()
  for a in "${expected[0]}" "${expected[1]}" "${expected[2]}" "${expected[3]}"; do
    files+=("$dist/$a")
  done

  local upload_log
  if ! upload_log=$(gh release upload "$tag" --repo "$repo" "${files[@]}" 2>&1); then
    echo "::error::uploading the four non-bundle assets to release ${tag} failed (gh: ${upload_log}). The release is left bundle-absent and replaceable: no consumer can have verified it, and the next run of this commit deletes the stale assets and retries the complete upload" >&2
    exit 1
  fi
  if ! upload_log=$(gh release upload "$tag" --repo "$repo" "$dist/$bundle" 2>&1); then
    echo "::error::uploading checksums.txt.bundle - the last asset a consumer needs before it can verify anything - to release ${tag} failed (gh: ${upload_log}). The release is left bundle-absent and replaceable: the four assets from this run are published but nothing can be verified against them yet, and the next run of this commit retries the replacement" >&2
    exit 1
  fi
}

# Run only when executed, so tests can source the helper functions.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  edge_publish_main "$@"
fi
