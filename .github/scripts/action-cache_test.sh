#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ACTION_FILE="$SCRIPT_DIR/../actions/reeve/action.yml"
CACHE_SHA=55cc8345863c7cc4c66a329aec7e433d2d1c52a9
# CACHE_KEY intentionally contains a literal GitHub expression under test.
# shellcheck disable=SC2016
CACHE_KEY='reeve-bin-v2-${{ inputs.source-repository || github.action_repository }}-full-${{ runner.os }}-${{ runner.arch }}-${{ steps.reeve-hash.outputs.hash }}'

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

restore_step=$(step_block "Restore reeve binary cache")
save_step=$(step_block "Save reeve binary cache")
setup_step=$(step_block "Set up Go (from reeve's go.mod)")
stage_step=$(step_block "Stage Go cache metadata")
assert_contains "$restore_step" "uses: actions/cache/restore@$CACHE_SHA"
assert_contains "$save_step" "uses: actions/cache/save@$CACHE_SHA"
assert_contains "$restore_step" "key: $CACHE_KEY"
assert_contains "$save_step" "key: $CACHE_KEY"
assert_contains "$save_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
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

# The restored cache entry is checked against the hash recorded at save time
# before anything executes it. The cache KEY binds the entry to the action
# source; the verify step binds the FILE to the bytes that were hashed - a
# corrupted or tampered entry is deleted and the run rebuilds instead.
verify_step=$(step_block "Verify cached reeve binary")
assert_contains "$verify_step" "steps.reeve-cache.outputs.cache-hit == 'true'"
assert_contains "$verify_step" 'sha256sum "$BIN_DIR/reeve"'
assert_contains "$verify_step" 'expected=$(cut -d'"'"' '"'"' -f1 "$BIN_DIR/reeve.sha256")'
assert_contains "$verify_step" 'echo "verified=false" >> "$GITHUB_OUTPUT"'
assert_contains "$verify_step" 'rm -rf "$BIN_DIR"'
assert_contains "$verify_step" 'rebuilding from source'

# The save path records the sidecar BEFORE caching, and the cache stores the
# whole directory (binary + sidecar), not a lone file.
record_step=$(step_block "Record reeve binary hash")
assert_contains "$record_step" 'sha256sum "$BIN_DIR/reeve"'
assert_contains "$record_step" 'reeve.sha256'
assert_contains "$save_step" "path: \${{ runner.temp }}/reeve-bin"
if [[ "$save_step" == *"reeve-bin/reeve$"* ]]; then
  fail "cache must store binary + hash sidecar, not the lone file"
fi
assert_contains "$restore_step" "path: \${{ runner.temp }}/reeve-bin"

# Every rebuild-path step also runs when the restored entry failed (or
# lacked) verification: verified=false means rebuild, never execute.
rebuild_condition="(steps.reeve-cache.outputs.cache-hit != 'true' || steps.reeve-verify.outputs.verified != 'true')"
for step_name in "Classify prebuilt binary eligibility" "Fetch prebuilt binary" "Stage Go cache metadata" "Set up Go (from reeve's go.mod)" "Build reeve" "Save reeve binary cache"; do
  step=$(step_block "$step_name")
  assert_contains "$step" "$rebuild_condition"
done
if [[ "$verify_step" == *"$rebuild_condition"* ]]; then
  fail "the verify step itself must run on a plain cache hit"
fi

restore_line=$(step_line "Restore reeve binary cache")
classify_line=$(step_line "Classify prebuilt binary eligibility")
cosign_line=$(step_line "Install cosign for binary verification")
stage_line=$(step_line "Stage Go cache metadata")
build_line=$(step_line "Build reeve")
save_line=$(step_line "Save reeve binary cache")
checkout_line=$(step_line "Checkout workload")
if ! (( restore_line < classify_line && classify_line < cosign_line && cosign_line < stage_line && stage_line < build_line && build_line < save_line && save_line < checkout_line )); then
  fail "cache restore/classification/build/save must complete before workload checkout"
fi

echo "action cache tests passed"
