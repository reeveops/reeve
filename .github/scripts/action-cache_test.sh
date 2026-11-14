#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ACTION_FILE="$SCRIPT_DIR/../actions/reeve/action.yml"
CACHE_SHA=55cc8345863c7cc4c66a329aec7e433d2d1c52a9
# CACHE_KEY intentionally contains a literal GitHub expression under test.
# shellcheck disable=SC2016
CACHE_KEY='reeve-asset-v1-${{ inputs.source-repository || github.action_repository }}-full-${{ runner.os }}-${{ runner.arch }}-${{ inputs.source-ref || github.action_ref }}-${{ steps.reeve-hash.outputs.hash }}'

fail() {
  echo "$1" >&2
  exit 1
}

assert_contains() {
  if [[ "$1" != *"$2"* ]]; then
    fail "missing '$2' in: $1"
  fi
}

step_block() {
  local name="$1"
  awk -v target="    - name: $name" '
    /^    - name: / {
      if (found) exit
      if ($0 == target) found = 1
    }
    found { print }
  ' "$ACTION_FILE"
}

step_line() {
  local name="$1"
  awk -v target="    - name: $name" '$0 == target { print NR; exit }' "$ACTION_FILE"
}

restore_step=$(step_block "Restore reeve asset cache")
save_step=$(step_block "Save reeve asset cache")
setup_step=$(step_block "Set up Go (from reeve's go.mod)")
stage_step=$(step_block "Stage Go cache metadata")
assert_contains "$restore_step" "uses: actions/cache/restore@$CACHE_SHA"
assert_contains "$save_step" "uses: actions/cache/save@$CACHE_SHA"
assert_contains "$restore_step" 'path: ${{ runner.temp }}/reeve-asset'
assert_contains "$save_step" 'path: ${{ runner.temp }}/reeve-asset'
assert_contains "$restore_step" "key: $CACHE_KEY"
assert_contains "$save_step" "key: $CACHE_KEY"
# The archive cache is written only after a SUCCESSFUL verified fetch.
assert_contains "$save_step" "steps.reeve-fetch.outputs.fetched == 'true'"
# The cache key must bind the artifact identity (the pinned ref), not just
# the action source: different refs use different archives.
assert_contains "$restore_step" '${{ inputs.source-ref || github.action_ref }}'
assert_contains "$save_step" '${{ inputs.source-ref || github.action_ref }}'
assert_contains "$stage_step" 'CACHE_DEPENDENCY_FILE: ${{ github.workspace }}/.reeve-action-go.sum'
assert_contains "$stage_step" 'cp "$ACTION_ROOT/go.sum" "$CACHE_DEPENDENCY_FILE"'
assert_contains "$setup_step" 'go-version-file: ${{ steps.reeve-hash.outputs.root }}/go.mod'
assert_contains "$setup_step" 'cache-dependency-path: ${{ github.workspace }}/.reeve-action-go.sum'

if [[ "$setup_step" == *".."* ]]; then
  fail "setup-go paths must use the canonical action root"
fi

if grep -q 'uses: actions/cache@' "$ACTION_FILE"; then
  fail "monolithic actions/cache would save in a post-job hook"
fi

# The verify-then-extract design: NO sidecar step, NO binary-side cache.
# The archive is verified against the LIVE signed checksums inside the
# fetch script on every run.
if grep -q "Verify cached reeve binary" "$ACTION_FILE"; then
  fail "the sidecar verify step must be gone: verification lives in fetch-binary.sh against LIVE checksums"
fi
if grep -q "reeve.sha256" "$ACTION_FILE"; then
  fail "the hash sidecar must be gone: a poisoned cache carries a matching sidecar"
fi
fetch_step_full=$(step_block "Fetch prebuilt binary")
assert_contains "$fetch_step_full" "REEVE_ARCHIVE_DIR: \${{ runner.temp }}/reeve-asset"
assert_contains "$fetch_step_full" "REEVE_CACHE_KEY:"

# Gating for the verify-then-extract design:
#
# 1. The classification, cosign install, and fetch steps run on cache hits
#    TOO - the cached ARCHIVE is re-verified against the LIVE signed
#    checksums on every run, so a cache hit must never skip verification.
# 2. Only the source-build steps are gated on fetched != 'true' (a soft
#    fallback), with no cache-hit term: the fetch decision is the script's.
# 3. The cache save runs only after a verified fetch on a cache miss.
for step_name in "Classify prebuilt binary eligibility" "Install cosign for binary verification" "Fetch prebuilt binary"; do
  step=$(step_block "$step_name")
  assert_contains "$step" "steps.reeve-dispatch.outputs.run == 'true'"
  if [[ "$step" == *"reeve-cache.outputs.cache-hit"* ]]; then
    fail "$step_name must not be gated on a cache hit: the cached archive is re-verified every run"
  fi
done
fetch_step=$(step_block "Fetch prebuilt binary")
assert_contains "$fetch_step" "steps.reeve-binary.outputs.eligible == 'true'"
assert_not_contains() {
  if [[ "$1" == *"$2"* ]]; then
    fail "unexpected '$2' in: $1"
  fi
}
assert_not_contains "$fetch_step" "reeve-cache.outputs.cache-hit"
for step_name in "Stage Go cache metadata" "Set up Go (from reeve's go.mod)" "Build reeve"; do
  step=$(step_block "$step_name")
  assert_contains "$step" "steps.reeve-fetch.outputs.fetched != 'true'"
  assert_not_contains "$step" "reeve-cache.outputs.cache-hit"
done
assert_contains "$save_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$save_step" "steps.reeve-fetch.outputs.fetched == 'true'"

# The cache stores the archive directory, never the binary directory, and
# never a hash sidecar.
assert_not_contains "$restore_step" "reeve-bin"
assert_not_contains "$save_step" "reeve-bin"

restore_line=$(step_line "Restore reeve asset cache")
classify_line=$(step_line "Classify prebuilt binary eligibility")
cosign_line=$(step_line "Install cosign for binary verification")
fetch_line=$(step_line "Fetch prebuilt binary")
stage_line=$(step_line "Stage Go cache metadata")
build_line=$(step_line "Build reeve")
save_line=$(step_line "Save reeve asset cache")
checkout_line=$(step_line "Checkout workload")
if ! (( restore_line < classify_line && classify_line < cosign_line && cosign_line < fetch_line && fetch_line < stage_line && stage_line < build_line && build_line < save_line && save_line < checkout_line )); then
  fail "cache restore/verification/build/save must complete before workload checkout"
fi

echo "action cache tests passed"
