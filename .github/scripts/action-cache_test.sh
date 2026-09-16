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
assert_contains "$restore_step" "uses: actions/cache/restore@$CACHE_SHA"
assert_contains "$save_step" "uses: actions/cache/save@$CACHE_SHA"
assert_contains "$restore_step" "key: $CACHE_KEY"
assert_contains "$save_step" "key: $CACHE_KEY"
assert_contains "$save_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$setup_step" 'go-version-file: ${{ steps.reeve-hash.outputs.root }}/go.mod'
assert_contains "$setup_step" 'cache-dependency-path: ${{ steps.reeve-hash.outputs.root }}/go.sum'

if [[ "$setup_step" == *".."* ]]; then
  fail "setup-go paths must use the canonical action root"
fi

if grep -q 'uses: actions/cache@' "$ACTION_FILE"; then
  fail "monolithic actions/cache would save in a post-job hook"
fi

restore_line=$(step_line "Restore reeve binary cache")
classify_line=$(step_line "Classify prebuilt binary eligibility")
cosign_line=$(step_line "Install cosign for binary verification")
build_line=$(step_line "Build reeve")
save_line=$(step_line "Save reeve binary cache")
checkout_line=$(step_line "Checkout workload")
if ! (( restore_line < classify_line && classify_line < cosign_line && cosign_line < build_line && build_line < save_line && save_line < checkout_line )); then
  fail "cache restore/classification/build/save must complete before workload checkout"
fi

echo "action cache tests passed"
