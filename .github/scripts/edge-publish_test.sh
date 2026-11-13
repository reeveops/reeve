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
    GH_FAKE_DELETE_FAIL="${GH_FAKE_DELETE_FAIL:-false}" \
    GH_FAKE_UPLOAD2_FAIL="${GH_FAKE_UPLOAD2_FAIL:-false}" \
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
if [[ "${1:-}" == "release" && "${2:-}" == "delete-asset" ]]; then
  if [[ "${GH_FAKE_DELETE_FAIL:-false}" == "true" ]]; then
    echo "gh: simulated delete failure (HTTP 524)" >&2
    exit 1
  fi
  exit 0
fi
if [[ "${1:-}" == "release" && "${2:-}" == "upload" ]]; then
  # Each invocation is a fresh process; the call ordinal comes from the
  # call log (this call's own line is already appended at the top, so the
  # count IS the ordinal).
  upload_n=$(grep -c "release upload" "$GH_FAKE_CALL_LOG" || true)
  if [[ "${GH_FAKE_UPLOAD_FAIL:-false}" == "true" ]]; then
    echo "gh: simulated upload failure (HTTP 502)" >&2
    exit 1
  fi
  if [[ "${GH_FAKE_UPLOAD2_FAIL:-false}" == "true" && "$upload_n" -eq 2 ]]; then
    echo "gh: simulated bundle upload failure (HTTP 503)" >&2
    exit 1
  fi
  exit 0
fi
exit 1
EOF
chmod +x "$fake_bin/gh"

# ---- Policy keyed on checksums.txt.bundle: the last asset a consumer
# needs before it can verify anything.

# assert_two_upload_calls <call-log>: gh uploads several files in
# PARALLEL (internal errgroup, concurrency 5), so the two-call shape is the
# ONLY order guarantee: the first call carries exactly the four non-bundle
# assets; the second call carries the bundle ALONE.
assert_two_upload_calls() {
  local log="$1" first second count
  first=$(grep "release upload" "$log" | sed -n 1p)
  second=$(grep "release upload" "$log" | sed -n 2p)
  count=$(grep -c "release upload" "$log" || true)
  if [[ $count -ne 2 ]]; then
    fail "expected exactly two upload calls (four assets, then the bundle alone), got $count: $(cat "$log")"
  fi
  for f in "$assets_amd64" "$assets_arm64" source-hash.txt checksums.txt; do
    if [[ "$first" != *"$dist/$f"* ]]; then
      fail "the first upload call must carry the four non-bundle assets, missing $f: $first"
    fi
  done
  if [[ "$first" == *"$dist/checksums.txt.bundle"* ]]; then
    fail "the bundle must NOT ride the first upload call: $first"
  fi
  if [[ "$second" != *"$dist/checksums.txt.bundle" ]]; then
    fail "the second upload call must end in the bundle: $second"
  fi
  # "checksums.txt" is a prefix of "checksums.txt.bundle", so membership
  # matching lies here; count the dist paths on the second line instead -
  # exactly one arg means the bundle is alone.
  if [[ $(grep -o "$dist/" <<<"$second" | wc -l) -ne 1 ]]; then
    fail "the second upload call must carry ONLY the bundle: $second"
  fi
}

# Row 1: everything present -> skip with the note; never overwrite.
GH_FAKE_ASSETS="$ALL_ASSETS"
d1=$(run_publish complete-release-skip)
[[ "$(cat "$d1/code")" -eq 0 ]] || fail "a complete release must exit 0"
assert_contains "$(cat "$d1/log")" "already carries its artifacts; not overwriting"
if grep -q "release upload" "$d1/gh.calls"; then
  fail "a complete release must not be re-uploaded"
fi
if grep -q "delete-asset" "$d1/gh.calls"; then
  fail "a complete release must not delete assets"
fi

# Row 2: bundle absent, stale present assets -> REPLACE. The current run's
# signed checksums cannot match earlier-run archives, so delete every stale
# asset (named in the log) and upload THIS run's complete set, bundle LAST.
GH_FAKE_ASSETS="$assets_amd64,source-hash.txt"
d2=$(run_publish bundle-absent-replaces)
unset GH_FAKE_ASSETS
[[ "$(cat "$d2/code")" -eq 0 ]] || fail "a bundle-absent release must be replaced"
assert_contains "$(cat "$d2/log")" "no consumer can have verified it - replacing the 2 asset(s)"
assert_contains "$(cat "$d2/log")" "$assets_amd64"
assert_contains "$(cat "$d2/log")" "source-hash.txt"
calls="$(cat "$d2/gh.calls")"
if [[ "$(grep -c "delete-asset" "$d2/gh.calls")" -ne 2 ]]; then
  fail "each stale asset must be deleted exactly once: $calls"
fi
assert_contains "$calls" "delete-asset $tag $assets_amd64"
assert_contains "$calls" "delete-asset $tag source-hash.txt"
del_line=$(grep -n "delete-asset" "$d2/gh.calls" | head -1 | cut -d: -f1)
up_line=$(grep -n "release upload" "$d2/gh.calls" | head -1 | cut -d: -f1)
if [[ -z "$up_line" || "$del_line" -ge "$up_line" ]]; then
  fail "stale assets must be deleted before the upload: $calls"
fi
assert_contains "$calls" "release upload $tag"
for f in "$assets_amd64" "$assets_arm64" source-hash.txt checksums.txt checksums.txt.bundle; do
  assert_contains "$calls" "dist/$f"
done
assert_two_upload_calls "$d2/gh.calls"

# Row 2b: bundle absent and nothing present (truly fresh) -> no deletes,
# full upload, bundle last.
GH_FAKE_ASSETS=""
d2b=$(run_publish fresh-upload)
unset GH_FAKE_ASSETS
[[ "$(cat "$d2b/code")" -eq 0 ]] || fail "a fresh release must upload"
assert_contains "$(cat "$d2b/log")" "no bundle and no assets - uploading the full set"
if grep -q "delete-asset" "$d2b/gh.calls"; then
  fail "a fresh release has nothing to delete"
fi
for f in "$assets_amd64" "$assets_arm64" source-hash.txt checksums.txt checksums.txt.bundle; do
  assert_contains "$(cat "$d2b/gh.calls")" "dist/$f"
done
assert_two_upload_calls "$d2b/gh.calls"

# Row 3: bundle PRESENT but another asset missing -> inconsistent,
# possibly already consumed: fail naming the missing assets; upload
# nothing, delete nothing.
GH_FAKE_ASSETS="${assets_amd64},${assets_arm64},checksums.txt,checksums.txt.bundle"
inlog=$(run_publish inconsistent-release)
unset GH_FAKE_ASSETS
if [[ "$(cat "$inlog/code")" -eq 0 ]]; then
  fail "an inconsistent release (bundle present, asset missing) must fail"
fi
assert_contains "$(cat "$inlog/log")" "is INCONSISTENT: checksums.txt.bundle is published but 1 asset(s) are missing: source-hash.txt"
assert_contains "$(cat "$inlog/log")" "A consumer may already have verified artifacts against this bundle"
if [[ "$(grep -cE "delete-asset|release upload" "$inlog/gh.calls")" -ne 0 ]]; then
  fail "an inconsistent release must not be touched"
fi

# Row 4 (ordering): the bundle uploads LAST - on the replacement upload
# (rows 2, 2b) and on a failing one. A run that dies midway always leaves
# bundle-absent; the failure text says so.
GH_FAKE_ASSETS=""
GH_FAKE_UPLOAD_FAIL=true
d6=$(run_publish upload-order-and-failure)
unset GH_FAKE_ASSETS GH_FAKE_UPLOAD_FAIL
if [[ "$(cat "$d6/code")" -eq 0 ]]; then
  fail "a failed upload must fail the step"
fi
assert_contains "$(cat "$d6/log")" "uploading the four non-bundle assets to release $tag failed"
assert_contains "$(cat "$d6/log")" "bundle-absent and replaceable"
if [[ "$(grep -c "release upload" "$d6/gh.calls")" -ne 1 ]]; then
  fail "when the first upload call fails there must be no second call: $(cat "$d6/gh.calls")"
fi
if grep -q "dist/checksums.txt.bundle" <(grep "release upload" "$d6/gh.calls"); then
  fail "the bundle must never upload in the first call"
fi

# Row 4b: only the BUNDLE upload fails (the four non-bundle assets land).
# The failure names the bundle and the replaceable state; two upload calls
# happened, the first successful.
GH_FAKE_ASSETS="$assets_amd64"
GH_FAKE_UPLOAD2_FAIL=true
d8=$(run_publish bundle-upload-failure)
unset GH_FAKE_ASSETS GH_FAKE_UPLOAD2_FAIL
if [[ "$(cat "$d8/code")" -eq 0 ]]; then
  fail "a failed bundle upload must fail the step"
fi
assert_contains "$(cat "$d8/log")" "uploading checksums.txt.bundle - the last asset a consumer needs before it can verify anything - to release $tag failed"
assert_contains "$(cat "$d8/log")" "gh: simulated bundle upload failure (HTTP 503)"
assert_contains "$(cat "$d8/log")" "bundle-absent and replaceable"
assert_two_upload_calls "$d8/gh.calls"
first=$(grep "release upload" "$d8/gh.calls" | sed -n 1p)
assert_contains "$first" "dist/$assets_amd64"

# Row 5: the listing itself failing -> fail with the reason; no skip, no
# replacement, no upload.
GH_FAKE_VIEW_FAIL=true
d4=$(run_publish listing-failure)
unset GH_FAKE_VIEW_FAIL
if [[ "$(cat "$d4/code")" -eq 0 ]]; then
  fail "a failed asset listing must fail the step"
fi
assert_contains "$(cat "$d4/log")" "cannot list the assets of release $tag"
assert_contains "$(cat "$d4/log")" "gh: simulated listing failure (HTTP 522)"
assert_contains "$(cat "$d4/log")" "neither skips nor uploads"
if [[ "$(grep -cE "delete-asset|release upload" "$d4/gh.calls")" -ne 0 ]]; then
  fail "a failed listing must not touch the release"
fi

# Row 6: a failed deletion -> fail with the reason; the release stays
# replaceable (bundle absent) and nothing is uploaded.
GH_FAKE_ASSETS="$assets_amd64"
GH_FAKE_DELETE_FAIL=true
d7=$(run_publish delete-failure)
unset GH_FAKE_ASSETS GH_FAKE_DELETE_FAIL
if [[ "$(cat "$d7/code")" -eq 0 ]]; then
  fail "a failed deletion must fail the step"
fi
assert_contains "$(cat "$d7/log")" "deleting stale asset $assets_amd64 from release $tag failed"
assert_contains "$(cat "$d7/log")" "bundle-absent"
if grep -q "release upload" "$d7/gh.calls"; then
  fail "a failed deletion must not lead to an upload"
fi

echo "edge-publish tests passed"
