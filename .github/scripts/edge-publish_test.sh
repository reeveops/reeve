#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PUBLISH_SCRIPT="$SCRIPT_DIR/edge-publish.sh"

fail() {
  echo "$1" >&2
  exit 1
}

assert_contains() {
  if [[ "$1" != *"$2"* ]]; then
    fail "missing '$2' in: $1"
  fi
}

# run_publish <name> [extra env assignments are ambient]. Requires set
# fixtures: DIST with the 5 artifact files, GH_FAKE_ASSETS naming what the
# release already carries (comma-separated), GH_FAKE_* knobs.
run_publish() {
  local name="$1"
  local case_dir="$test_root/cases/$name"
  mkdir -p "$case_dir"
  : > "$case_dir/gh.calls"
  env \
    PATH="$fake_bin:$PATH" \
    GITHUB_REPOSITORY=reeveops/reeve \
    GH_FAKE_CALL_LOG="$case_dir/gh.calls" \
    GH_FAKE_ASSETS="${GH_FAKE_ASSETS:-}" \
    GH_FAKE_VIEW_FAIL="${GH_FAKE_VIEW_FAIL:-false}" \
    GH_FAKE_UPLOAD_FAIL="${GH_FAKE_UPLOAD_FAIL:-false}" \
    bash "$PUBLISH_SCRIPT" "$tag" "$commit_sha" "$dist" > "$case_dir/log" 2>&1
  printf '%s\n' "$?" > "$case_dir/code"
  printf '%s\n' "$case_dir"
}

test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

branch=master
commit_sha=0123456789abcdef0123456789abcdef01234567
tag="${branch}-${commit_sha}"
assets_amd64="reeve_${commit_sha}_linux_amd64.tar.gz"
assets_arm64="reeve_${commit_sha}_linux_arm64.tar.gz"
dist="$test_root/dist"
mkdir -p "$dist"
for f in "$assets_amd64" "$assets_arm64" source-hash.txt checksums.txt checksums.txt.bundle; do
  echo bytes > "$dist/$f"
done
ALL_ASSETS="${assets_amd64},${assets_arm64},source-hash.txt,checksums.txt,checksums.txt.bundle"

fake_bin="$test_root/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$GH_FAKE_CALL_LOG"
if [[ "${1:-}" == "release" && "${2:-}" == "view" ]]; then
  if [[ "${GH_FAKE_VIEW_FAIL:-false}" == "true" ]]; then
    echo "gh: simulated listing failure (HTTP 522)" >&2
    exit 1
  fi
  # The --jq asset-name filter is faked: the response IS the joined names.
  printf '%s\n' "${GH_FAKE_ASSETS:-}"
  exit 0
fi
if [[ "${1:-}" == "release" && "${2:-}" == "upload" ]]; then
  if [[ "${GH_FAKE_UPLOAD_FAIL:-false}" == "true" ]]; then
    echo "gh: simulated upload failure (HTTP 502)" >&2
    exit 1
  fi
  exit 0
fi
exit 1
EOF
chmod +x "$fake_bin/gh"

# --- Case 1: a fresh release (listing empty) uploads every artifact.
GH_FAKE_ASSETS=""
d1=$(run_publish fresh-release)
[[ "$(cat "$d1/code")" -eq 0 ]] || fail "a fresh release upload must succeed"
assert_contains "$(cat "$d1/log")" "is fresh - uploading all 5 artifact(s)"
calls="$(cat "$d1/gh.calls")"
assert_contains "$calls" "release upload $tag"
for f in "$assets_amd64" "$assets_arm64" source-hash.txt checksums.txt checksums.txt.bundle; do
  assert_contains "$calls" "dist/$f"
done

# --- Case 2: a complete release skips with the note and does NOT upload.
GH_FAKE_ASSETS="$ALL_ASSETS"
d2=$(run_publish complete-release-skip)
[[ "$(cat "$d2/code")" -eq 0 ]] || fail "a complete release must exit 0"
assert_contains "$(cat "$d2/log")" "already carries its artifacts; not overwriting"
if grep -q "release upload" "$d2/gh.calls"; then
  fail "a complete release must not be re-uploaded"
fi

# --- Case 3: a partial release completes the upload, uploading only the
# missing files - already-present assets are NEVER re-uploaded.
GH_FAKE_ASSETS="$assets_amd64"
d3=$(run_publish partial-release-completes)
[[ "$(cat "$d3/code")" -eq 0 ]] || fail "a partial release must complete"
assert_contains "$(cat "$d3/log")" "missing 4 asset(s):"
assert_contains "$(cat "$d3/log")" "never re-uploaded"
calls="$(cat "$d3/gh.calls")"
assert_contains "$calls" "release upload $tag"
for f in "$assets_arm64" source-hash.txt checksums.txt checksums.txt.bundle; do
  assert_contains "$calls" "dist/$f"
done
# The one asset that IS present must not appear in the upload.
if grep -qE "release upload [^#]*dist/$assets_amd64" "$d3/gh.calls"; then
  fail "an asset already present must not be re-uploaded (never overwrite)"
fi

# --- Case 4: the listing itself failing fails the step with the reason -
# neither skip nor upload can be decided blind.
GH_FAKE_VIEW_FAIL=true
d4=$(run_publish listing-failure)
unset GH_FAKE_VIEW_FAIL
if [[ "$(cat "$d4/code")" -eq 0 ]]; then
  fail "a failed asset listing must fail the step"
fi
assert_contains "$(cat "$d4/log")" "cannot list the assets of release $tag"
assert_contains "$(cat "$d4/log")" "gh: simulated listing failure (HTTP 522)"
assert_contains "$(cat "$d4/log")" "neither skips nor uploads"
if grep -q "release upload" "$d4/gh.calls"; then
  fail "a failed listing must not upload"
fi

# --- Case 5: an upload failure fails the step with the reason and leaves
# the release as-is.
GH_FAKE_ASSETS=""
GH_FAKE_UPLOAD_FAIL=true
d5=$(run_publish upload-failure)
unset GH_FAKE_UPLOAD_FAIL
if [[ "$(cat "$d5/code")" -eq 0 ]]; then
  fail "a failed upload must fail the step"
fi
assert_contains "$(cat "$d5/log")" "uploading 5 asset(s) to release $tag failed"
assert_contains "$(cat "$d5/log")" "left as-is"

echo "edge-publish tests passed"
