#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
FETCH_SCRIPT="$SCRIPT_DIR/fetch-binary.sh"
ACTION_FILE="$SCRIPT_DIR/../actions/reeve/action.yml"
# shellcheck source=.github/scripts/fetch-binary.sh
source "$FETCH_SCRIPT"

fail() {
  echo "$1" >&2
  exit 1
}

assert_eq() {
  if [[ "$1" != "$2" ]]; then
    fail "got '$1', want '$2'"
  fi
}

assert_contains() {
  if [[ "$1" != *"$2"* ]]; then
    fail "missing '$2' in: $1"
  fi
}

assert_eq "$(classify_ref v1.2.3)" version
assert_eq "$(classify_ref v1.2.3-rc.1)" version
assert_eq "$(classify_ref v1.2.3-rc.1+build.7)" other
assert_eq "$(classify_ref v1.2.3-)" other
assert_eq "$(classify_ref master)" edge
assert_eq "$(classify_ref next)" edge
assert_eq "$(classify_ref 0123456789abcdef0123456789abcdef01234567)" commit
assert_eq "$(classify_ref 0123456789ABCDEF0123456789ABCDEF01234567)" commit
assert_eq "$(classify_ref 0123456)" other
assert_eq "$(classify_ref feature/test)" other

test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
source_hash=$(printf 'a%.0s' {1..64})
other_hash=$(printf 'b%.0s' {1..64})
commit_ref=0123456789abcdef0123456789abcdef01234567

REEVE_REF=feature/test REEVE_REPO=reeveops/reeve REEVE_SOURCE_HASH=$source_hash REEVE_OS=Linux REEVE_ARCH=X64 \
  prebuilt_eligible && fail "feature ref was prebuilt eligible"
REEVE_REF=$commit_ref REEVE_REPO=reeveops/reeve REEVE_SOURCE_HASH=$source_hash REEVE_OS=macOS REEVE_ARCH=X64 \
  prebuilt_eligible && fail "macOS was prebuilt eligible"
REEVE_REF=$commit_ref REEVE_REPO=reeveops/reeve REEVE_SOURCE_HASH=$source_hash REEVE_OS=Linux REEVE_ARCH=X64 \
  prebuilt_eligible || fail "commit ref was not prebuilt eligible"
REEVE_REF=v1.2.3-rc.1 REEVE_REPO=reeveops/reeve REEVE_SOURCE_HASH=invalid REEVE_OS=Linux REEVE_ARCH=X64 \
  prebuilt_eligible || fail "prerelease ref was not prebuilt eligible"

printf '%s\n' "$source_hash" > "$test_root/source-hash.txt"
REEVE_SOURCE_HASH=$source_hash verify_source_hash "$test_root/source-hash.txt"
printf '%s\n' "$other_hash" > "$test_root/source-hash.txt"
if REEVE_SOURCE_HASH=$source_hash verify_source_hash "$test_root/source-hash.txt" 2> /dev/null; then
  fail "verify_source_hash accepted a mismatch"
fi

fake_bin="$test_root/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$GH_FAKE_CALL_LOG"
if [[ "${1:-}" == "api" ]]; then
  # The --jq filtering is faked: the response IS the resolved release tag.
  if [[ "${GH_FAKE_API_EMPTY:-false}" != "true" ]]; then
    printf '%s\n' "${GH_FAKE_API_TAG:-fake-release}"
  fi
  exit 0
fi
if [[ "${1:-}" == "release" && "${2:-}" == "download" ]]; then
  if [[ "${GH_FAKE_DOWNLOAD_FAIL:-false}" == "true" ]]; then
    echo "gh: simulated release download failure (HTTP 502)" >&2
    exit 1
  fi
  tag=""
  for ((i = 1; i <= $#; i++)); do
    if [[ "${!i}" == "download" ]]; then
      j=$((i + 1))
      tag="${!j}"
      break
    fi
  done
  fixture="$GH_FAKE_FIXTURE"
  if [[ -n "${GH_FAKE_TAG2:-}" && "$tag" == "$GH_FAKE_TAG2" && -n "${GH_FAKE_FIXTURE2:-}" ]]; then
    fixture="$GH_FAKE_FIXTURE2"
  fi
  output_dir=""
  while (( $# > 0 )); do
    if [[ "$1" == "--dir" ]]; then
      output_dir="$2"
      break
    fi
    shift
  done
  [[ -n "$output_dir" ]]
  mkdir -p "$output_dir"
  cp -R "$fixture"/* "$output_dir/"
  exit 0
fi
exit 1
EOF
# A second PATH without cosign, for the cosign-unavailable fallback row.
fake_bin_nc="$test_root/bin-no-cosign"
mkdir -p "$fake_bin_nc"
cp "$fake_bin/gh" "$fake_bin_nc/gh"
chmod +x "$fake_bin_nc/gh"
cat > "$fake_bin/cosign" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$COSIGN_FAKE_CALL_LOG"
if [[ "${COSIGN_FAKE_FAIL:-false}" == "true" ]]; then
  exit 1
fi
EOF
chmod +x "$fake_bin/gh" "$fake_bin/cosign"

make_fixture() {
  local dir="$1" artifact_hash="$2"
  mkdir -p "$dir/payload"
  cat > "$dir/payload/reeve" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  echo "reeve fixture"
  exit 0
fi
exit 1
EOF
  chmod +x "$dir/payload/reeve"
  tar -C "$dir/payload" -czf "$dir/reeve_linux_amd64.tar.gz" reeve
  printf '%s\n' "$artifact_hash" > "$dir/source-hash.txt"
  (
    cd "$dir"
    sha256sum reeve_linux_amd64.tar.gz source-hash.txt > checksums.txt
  )
  : > "$dir/checksums.txt.bundle"
}

run_fetch() {
  local name="$1" ref="$2" fixture="$3" bin_dir="${4:-$fake_bin}"
  local case_dir="$test_root/cases/$name"
  mkdir -p "$case_dir"
  : > "$case_dir/gh.calls"
  : > "$case_dir/cosign.calls"
  # The exit code is captured per case: hard tamper failures exit 1, every
  # other failure is a soft fallback and exits 0.
  env \
    PATH="$bin_dir:$PATH" \
    REEVE_REF="$ref" \
    REEVE_REPO=reeveops/reeve \
    REEVE_SOURCE_HASH="$source_hash" \
    REEVE_OS=Linux \
    REEVE_ARCH=X64 \
    REEVE_DEST="$case_dir/reeve" \
    GITHUB_OUTPUT="$case_dir/output" \
    GH_FAKE_FIXTURE="$fixture" \
    GH_FAKE_CALL_LOG="$case_dir/gh.calls" \
    COSIGN_FAKE_CALL_LOG="$case_dir/cosign.calls" \
    GH_FAKE_API_EMPTY="${GH_FAKE_API_EMPTY:-false}" \
    GH_FAKE_DOWNLOAD_FAIL="${GH_FAKE_DOWNLOAD_FAIL:-false}" \
    COSIGN_FAKE_FAIL="${COSIGN_FAKE_FAIL:-false}" \
    GH_FAKE_API_TAG="${GH_FAKE_API_TAG:-}" \
    GH_FAKE_TAG2="${GH_FAKE_TAG2:-}" \
    GH_FAKE_FIXTURE2="${GH_FAKE_FIXTURE2:-}" \
    REEVE_ARCHIVE_DIR="${REEVE_ARCHIVE_DIR:-}" \
    REEVE_CACHE_KEY="${REEVE_CACHE_KEY:-}" \
    bash "$FETCH_SCRIPT" > "$case_dir/log" 2>&1
  printf '%s\n' "$?" > "$case_dir/code"
  printf '%s\n' "$case_dir"
}

good_fixture="$test_root/fixtures/good"
make_fixture "$good_fixture" "$source_hash"
sums_digest=$(cd "$good_fixture" && sha256sum reeve_linux_amd64.tar.gz | cut -d' ' -f1)

# The cache stores one archive per RESOLVED release tag (fake-release for
# the fake api in these tests).
good_cached_dir="$test_root/cached-good"
mkdir -p "$good_cached_dir"

# Policy row: cold download verifies and stores the archive under the
# resolved release tag.
REEVE_ARCHIVE_DIR="$good_cached_dir"
cold_dir=$(run_fetch cold-hit "$commit_ref" "$good_fixture")
assert_eq "$(cat "$cold_dir/output")" fetched=true
[[ -x "$cold_dir/reeve" ]] || fail "cold hit did not install an executable"
assert_contains "$(cat "$cold_dir/log")" "Using prebuilt binary: reeve fixture"
assert_contains "$(cat "$cold_dir/cosign.calls")" "verify-blob"
[[ -f "$good_cached_dir/fake-release.tar.gz" ]] || fail "cold hit did not store the verified archive under the resolved tag"

# Policy row: cached archive for THIS tag is re-verified against the LIVE
# signed checksums each run and is NOT re-downloaded.
REEVE_ARCHIVE_DIR="$good_cached_dir"
cached_dir=$(run_fetch cache-hit "$commit_ref" "$good_fixture")
unset REEVE_ARCHIVE_DIR REEVE_CACHE_KEY
assert_eq "$(cat "$cached_dir/output")" fetched=true
[[ -x "$cached_dir/reeve" ]] || fail "cache hit did not install an executable"
assert_contains "$(cat "$cached_dir/log")" "Using cached prebuilt archive for fake-release"
if grep -q "release download fake-release --repo reeveops/reeve --pattern reeve_linux_amd64.tar.gz" "$cached_dir/gh.calls"; then
  fail "cache hit must not re-download the release asset"
fi

# Policy row: a tampered cached archive (digest mismatch vs the verified
# signed checksums) is the ONE hard failure - expected digest, actual
# digest, cache key; nothing extracted.
tampered_cached_dir="$test_root/cached-bad"
mkdir -p "$tampered_cached_dir"
cp "$good_fixture/reeve_linux_amd64.tar.gz" "$tampered_cached_dir/fake-release.tar.gz"
printf 'tamper' >> "$tampered_cached_dir/fake-release.tar.gz"
REEVE_ARCHIVE_DIR="$tampered_cached_dir"
REEVE_CACHE_KEY="reeve-asset-v1-test-key"
tampered_dir=$(run_fetch tampered-cache "$commit_ref" "$good_fixture")
unset REEVE_ARCHIVE_DIR REEVE_CACHE_KEY
if [[ "$(cat "$tampered_dir/code")" -eq 0 ]]; then
  fail "a tampered cached archive must fail the fetch"
fi
assert_eq "$(cat "$tampered_dir/output")" fetched=false
[[ ! -e "$tampered_dir/reeve" ]] || fail "tampered cache installed a binary"
assert_contains "$(cat "$tampered_dir/log")" "digest mismatch for the cached archive of fake-release"
assert_contains "$(cat "$tampered_dir/log")" "reeve-asset-v1-test-key"
assert_contains "$(cat "$tampered_dir/log")" "expected $sums_digest"

# Policy row: checksums/bundle unfetchable -> SOFT fallback with gh's real
# error surfaced; a cached archive is ignored (present but untouched).
GH_FAKE_DOWNLOAD_FAIL=true
REEVE_ARCHIVE_DIR="$good_cached_dir"
REEVE_CACHE_KEY="reeve-asset-v1-test-key"
sums_dir_fail=$(run_fetch sums-fetch-failure "$commit_ref" "$good_fixture")
unset GH_FAKE_DOWNLOAD_FAIL REEVE_ARCHIVE_DIR REEVE_CACHE_KEY
assert_eq "$(cat "$sums_dir_fail/output")" fetched=false
[[ "$(cat "$sums_dir_fail/code")" -eq 0 ]] || fail "a sums fetch failure must be a soft fallback, not a failed run"
[[ ! -e "$sums_dir_fail/reeve" ]] || fail "sums fetch failure installed a binary"
assert_contains "$(cat "$sums_dir_fail/log")" "could not be fetched for fake-release"
assert_contains "$(cat "$sums_dir_fail/log")" "gh: simulated release download failure (HTTP 502)"
assert_contains "$(cat "$sums_dir_fail/log")" "cached archive is ignored"
[[ -f "$good_cached_dir/fake-release.tar.gz" ]] || fail "the cached archive must not be touched on a sums fetch failure"

# Policy row: cosign unavailable -> SOFT fallback; cached archive ignored.
REEVE_ARCHIVE_DIR="$good_cached_dir"
no_cosign_dir=$(run_fetch cosign-unavailable "$commit_ref" "$good_fixture" "$fake_bin_nc")
unset REEVE_ARCHIVE_DIR
assert_eq "$(cat "$no_cosign_dir/output")" fetched=false
[[ "$(cat "$no_cosign_dir/code")" -eq 0 ]] || fail "a missing verifier must be a soft fallback, not a failed run"
assert_contains "$(cat "$no_cosign_dir/log")" "could not be verified"
assert_contains "$(cat "$no_cosign_dir/log")" "cached archive is ignored"
[[ -f "$good_cached_dir/fake-release.tar.gz" ]] || fail "the cached archive must not be touched when cosign is unavailable"

# Policy row: signature that does not verify -> SOFT fallback; cached
# archive ignored.
COSIGN_FAKE_FAIL=true
REEVE_ARCHIVE_DIR="$good_cached_dir"
bad_signature_dir=$(run_fetch bad-signature "$commit_ref" "$good_fixture")
unset COSIGN_FAKE_FAIL REEVE_ARCHIVE_DIR
assert_eq "$(cat "$bad_signature_dir/output")" fetched=false
[[ "$(cat "$bad_signature_dir/code")" -eq 0 ]] || fail "an unverifiable signature must be a soft fallback, not a failed run"
[[ ! -e "$bad_signature_dir/reeve" ]] || fail "bad signature installed a binary"
assert_contains "$(cat "$bad_signature_dir/log")" "signature verification FAILED"
assert_contains "$(cat "$bad_signature_dir/log")" "could not be verified; the cached archive is ignored"
[[ -f "$good_cached_dir/fake-release.tar.gz" ]] || fail "the cached archive must not be touched on a bad signature"

GH_FAKE_API_EMPTY=true
missing_dir=$(run_fetch missing-release "$commit_ref" "$good_fixture")
unset GH_FAKE_API_EMPTY
assert_eq "$(cat "$missing_dir/output")" fetched=false
[[ ! -e "$missing_dir/reeve" ]] || fail "missing release installed a binary"
assert_contains "$(cat "$missing_dir/log")" "no retained prerelease matches commit"

# Policy row: freshly downloaded archive with a digest mismatch -> hard
# failure naming both digests.
bad_checksum_fixture="$test_root/fixtures/bad-checksum"
cp -R "$good_fixture" "$bad_checksum_fixture"
printf 'corrupt\n' >> "$bad_checksum_fixture/reeve_linux_amd64.tar.gz"
bad_checksum_dir=$(run_fetch bad-checksum "$commit_ref" "$bad_checksum_fixture")
if [[ "$(cat "$bad_checksum_dir/code")" -eq 0 ]]; then
  fail "a digest mismatch on a fresh download must fail the fetch"
fi
assert_eq "$(cat "$bad_checksum_dir/output")" fetched=false
[[ ! -e "$bad_checksum_dir/reeve" ]] || fail "bad checksum installed a binary"
assert_contains "$(cat "$bad_checksum_dir/log")" "digest mismatch for the freshly downloaded archive of fake-release"
assert_contains "$(cat "$bad_checksum_dir/log")" "expected $sums_digest"

# Policy row: a verified binary that will not execute -> SOFT fallback with
# the reason, and nothing is cached.
noexec_fixture="$test_root/fixtures/noexec"
make_fixture "$noexec_fixture" "$source_hash"
mkdir -p "$noexec_fixture/payload2"
printf '#!/usr/bin/env bash\nexit 3\n' > "$noexec_fixture/payload2/reeve"
chmod +x "$noexec_fixture/payload2/reeve"
tar -C "$noexec_fixture/payload2" -czf "$noexec_fixture/reeve_linux_amd64.tar.gz" reeve
(
  cd "$noexec_fixture" &&
    sha256sum reeve_linux_amd64.tar.gz source-hash.txt > checksums.txt
)
noexec_cached="$test_root/cached-noexec"
mkdir -p "$noexec_cached"
# The run IS pointed at the cache dir: the assertion below (nothing stored)
# is then load-bearing instead of vacuous.
REEVE_ARCHIVE_DIR="$noexec_cached"
noexec_dir=$(run_fetch noexec-binary "$commit_ref" "$noexec_fixture")
unset REEVE_ARCHIVE_DIR
assert_eq "$(cat "$noexec_dir/output")" fetched=false
[[ "$(cat "$noexec_dir/code")" -eq 0 ]] || fail "a non-executable verified binary must be a soft fallback"
assert_contains "$(cat "$noexec_dir/log")" "does not execute (reeve --version failed)"
[[ ! -e "$noexec_dir/reeve" ]] || fail "a non-executable binary was installed"
if [[ "$(cat "$noexec_dir/output")" == fetched=true ]]; then
  fail "the verified-but-non-executable download must not complete as fetched"
fi
[[ -z "$(ls -A "$noexec_cached" 2>/dev/null)" ]] || fail "a non-executable binary must not be cached: $(ls -A "$noexec_cached")"

wrong_source_fixture="$test_root/fixtures/wrong-source"
make_fixture "$wrong_source_fixture" "$other_hash"
wrong_source_dir=$(run_fetch wrong-source "$commit_ref" "$wrong_source_fixture")
assert_eq "$(cat "$wrong_source_dir/output")" fetched=false
[[ ! -e "$wrong_source_dir/reeve" ]] || fail "wrong source installed a binary"
assert_contains "$(cat "$wrong_source_dir/log")" "release source hash mismatch"

fallback_dir=$(run_fetch source-fallback feature/test "$good_fixture")
assert_eq "$(cat "$fallback_dir/output")" fetched=false
[[ ! -e "$fallback_dir/reeve" ]] || fail "unsupported ref installed a binary"
[[ ! -s "$fallback_dir/gh.calls" ]] || fail "unsupported ref called GitHub"
assert_contains "$(cat "$fallback_dir/log")" "falling back to source build"

# Policy row: the cache is keyed by the RESOLVED release tag. Two edge
# releases can share one action-source hash (different engine commits with
# an unchanged action), so a newly resolved tag is a cache MISS -> fresh
# download -> stored under ITS tag; the earlier tag's archive is never
# compared, never deleted.
edgeA_fixture="$test_root/fixtures/edge-a"
make_fixture "$edgeA_fixture" "$source_hash"
edgeB_fixture="$test_root/fixtures/edge-b"
make_fixture "$edgeB_fixture" "$source_hash"
printf 'extra\n' >> "$edgeB_fixture/payload/reeve"
tar -C "$edgeB_fixture/payload" -czf "$edgeB_fixture/reeve_linux_amd64.tar.gz" reeve
(
  cd "$edgeB_fixture" &&
    sha256sum reeve_linux_amd64.tar.gz source-hash.txt > checksums.txt
)
edge_cache="$test_root/cached-edge"
mkdir -p "$edge_cache"
GH_FAKE_API_TAG=master-aaaa
REEVE_ARCHIVE_DIR="$edge_cache"
edge_a_dir=$(run_fetch edge-tag-a master "$edgeA_fixture")
GH_FAKE_API_TAG=master-bbbb
GH_FAKE_TAG2=master-bbbb
GH_FAKE_FIXTURE2="$edgeB_fixture"
edge_b_dir=$(run_fetch edge-tag-b master "$edgeA_fixture")
unset GH_FAKE_API_TAG GH_FAKE_TAG2 GH_FAKE_FIXTURE2 REEVE_ARCHIVE_DIR
assert_eq "$(cat "$edge_a_dir/output")" fetched=true
assert_eq "$(cat "$edge_b_dir/output")" fetched=true
[[ -f "$edge_cache/master-aaaa.tar.gz" ]] || fail "tag a archive missing"
[[ -f "$edge_cache/master-bbbb.tar.gz" ]] || fail "tag b archive missing"
if grep -q "master-aaaa.tar.gz" "$edge_b_dir/log"; then
  fail "a different tag's cache entry must not even be compared"
fi

# ...and the archive cached for master-bbbb is reused without a re-download.
GH_FAKE_API_TAG=master-bbbb
REEVE_ARCHIVE_DIR="$edge_cache"
edge_b2_dir=$(run_fetch edge-tag-b-again master "$edgeB_fixture")
unset GH_FAKE_API_TAG REEVE_ARCHIVE_DIR
assert_eq "$(cat "$edge_b2_dir/output")" fetched=true
assert_contains "$(cat "$edge_b2_dir/log")" "Using cached prebuilt archive for master-bbbb"
if grep -q "release download master-bbbb --repo reeveops/reeve --pattern reeve_linux_amd64.tar.gz" "$edge_b2_dir/gh.calls"; then
  fail "the second run of the same edge tag must not re-download"
fi

# A tampered archive for the SAME edge tag is the hard failure again.
edge_tamper="$test_root/cached-edge-tamper"
mkdir -p "$edge_tamper"
cp "$edge_cache/master-bbbb.tar.gz" "$edge_tamper/master-bbbb.tar.gz"
printf 'x' >> "$edge_tamper/master-bbbb.tar.gz"
GH_FAKE_API_TAG=master-bbbb
REEVE_ARCHIVE_DIR="$edge_tamper"
REEVE_CACHE_KEY="edge-key"
edge_tamper_dir=$(run_fetch edge-tag-tampered master "$edgeB_fixture")
unset GH_FAKE_API_TAG REEVE_ARCHIVE_DIR REEVE_CACHE_KEY
if [[ "$(cat "$edge_tamper_dir/code")" -eq 0 ]]; then
  fail "a tampered same-tag edge archive must fail the fetch"
fi
assert_contains "$(cat "$edge_tamper_dir/log")" "digest mismatch for the cached archive of master-bbbb"

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

fetch_step=$(step_block "Fetch prebuilt binary")
classify_step=$(step_block "Classify prebuilt binary eligibility")
cosign_step=$(step_block "Install cosign for binary verification")
setup_step=$(step_block "Set up Go (from reeve's go.mod)")
build_step=$(step_block "Build reeve")
# The verify-then-extract design: classification, cosign install, and fetch
# run on cache hits TOO - the cached archive is re-verified against the LIVE
# signed checksums every run. Only the source-build steps carry the
# fetched != 'true' fallback gate.
assert_contains "$classify_step" "steps.reeve-dispatch.outputs.run == 'true'"
assert_contains "$cosign_step" "steps.reeve-binary.outputs.eligible == 'true'"
assert_contains "$fetch_step" "steps.reeve-binary.outputs.eligible == 'true'"
assert_contains "$setup_step" "steps.reeve-fetch.outputs.fetched != 'true'"
assert_contains "$build_step" "steps.reeve-fetch.outputs.fetched != 'true'"
assert_contains "$fetch_step" "REEVE_ARCHIVE_DIR: \${{ runner.temp }}/reeve-asset"

echo "fetch-binary tests passed"
