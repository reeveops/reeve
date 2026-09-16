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
  if [[ "${GH_FAKE_API_EMPTY:-false}" != "true" ]]; then
    printf '%s\n' fake-release
  fi
  exit 0
fi
if [[ "${1:-}" == "release" && "${2:-}" == "download" ]]; then
  if [[ "${GH_FAKE_DOWNLOAD_FAIL:-false}" == "true" ]]; then
    exit 1
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
  cp -R "$GH_FAKE_FIXTURE"/* "$output_dir/"
  exit 0
fi
exit 1
EOF
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
  local name="$1" ref="$2" fixture="$3"
  local case_dir="$test_root/cases/$name"
  mkdir -p "$case_dir"
  : > "$case_dir/gh.calls"
  : > "$case_dir/cosign.calls"
  env \
    PATH="$fake_bin:$PATH" \
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
    bash "$FETCH_SCRIPT" > "$case_dir/log" 2>&1
  printf '%s\n' "$case_dir"
}

good_fixture="$test_root/fixtures/good"
make_fixture "$good_fixture" "$source_hash"

cold_dir=$(run_fetch cold-hit "$commit_ref" "$good_fixture")
assert_eq "$(cat "$cold_dir/output")" fetched=true
[[ -x "$cold_dir/reeve" ]] || fail "cold hit did not install an executable"
assert_contains "$(cat "$cold_dir/log")" "Using prebuilt binary: reeve fixture"
assert_contains "$(cat "$cold_dir/cosign.calls")" "verify-blob"

GH_FAKE_API_EMPTY=true
missing_dir=$(run_fetch missing-release "$commit_ref" "$good_fixture")
unset GH_FAKE_API_EMPTY
assert_eq "$(cat "$missing_dir/output")" fetched=false
[[ ! -e "$missing_dir/reeve" ]] || fail "missing release installed a binary"
assert_contains "$(cat "$missing_dir/log")" "no retained prerelease matches commit"

bad_checksum_fixture="$test_root/fixtures/bad-checksum"
cp -R "$good_fixture" "$bad_checksum_fixture"
printf 'corrupt\n' >> "$bad_checksum_fixture/reeve_linux_amd64.tar.gz"
bad_checksum_dir=$(run_fetch bad-checksum "$commit_ref" "$bad_checksum_fixture")
assert_eq "$(cat "$bad_checksum_dir/output")" fetched=false
[[ ! -e "$bad_checksum_dir/reeve" ]] || fail "bad checksum installed a binary"
assert_contains "$(cat "$bad_checksum_dir/log")" "checksum mismatch"

COSIGN_FAKE_FAIL=true
bad_signature_dir=$(run_fetch bad-signature "$commit_ref" "$good_fixture")
unset COSIGN_FAKE_FAIL
assert_eq "$(cat "$bad_signature_dir/output")" fetched=false
[[ ! -e "$bad_signature_dir/reeve" ]] || fail "bad signature installed a binary"
assert_contains "$(cat "$bad_signature_dir/log")" "signature verification FAILED"

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
setup_step=$(step_block "Set up Go (from reeve's go.mod)")
build_step=$(step_block "Build reeve")
assert_contains "$fetch_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$setup_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$setup_step" "steps.reeve-fetch.outputs.fetched != 'true'"
assert_contains "$build_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$build_step" "steps.reeve-fetch.outputs.fetched != 'true'"

echo "fetch-binary tests passed"
