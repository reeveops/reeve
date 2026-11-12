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
  echo "Error: no matching signatures"
  exit 1
fi
# When COSIGN_FAKE_IDENTITY is set the fake cosign behaves like the real
# one for identity purposes: verify the --certificate-identity-regexp
# against the emulated certificate identity.
if [[ -n "${COSIGN_FAKE_IDENTITY:-}" ]]; then
  re=""
  while (( $# > 0 )); do
    if [[ "$1" == "--certificate-identity-regexp" ]]; then
      re="$2"
      break
    fi
    shift
  done
  if [[ "$COSIGN_FAKE_IDENTITY" =~ $re ]]; then
    echo "signature verified for $COSIGN_FAKE_IDENTITY"
    exit 0
  fi
  echo "Error: no matching signatures"
  echo "certificate: $COSIGN_FAKE_IDENTITY"
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
    COSIGN_FAKE_IDENTITY="${COSIGN_FAKE_IDENTITY:-}" \
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

# The signing identity is scoped per artifact kind (the maintainer decision:
# everything with a version tag is signed by a run on that tag; per-commit
# builds by the edge workflow on master).
required_identity version v0.9.0 reeveops/reeve id_re id_desc id_what
assert_eq "$id_re" '^https://github\.com/reeveops/reeve/\.github/workflows/release\.yml@refs/tags/v0\.9\.0$'
assert_eq "$id_desc" 'release.yml@refs/tags/v0.9.0'
assert_eq "$id_what" 'version v0.9.0'
required_identity commit "$commit_ref" reeveops/reeve id_re id_desc id_what
assert_eq "$id_re" '^https://github\.com/reeveops/reeve/\.github/workflows/edge-build\.yml@refs/heads/master$'
assert_eq "$id_desc" 'edge-build.yml@refs/heads/master'
required_identity version v0.9.0-rc.1 someone.example/forks/reeve id_re id_desc id_what
assert_eq "$id_re" '^https://github\.com/someone\.example/forks/reeve/\.github/workflows/release\.yml@refs/tags/v0\.9\.0-rc\.1$'

# A version tag verified against the release-tag identity is accepted. The
# release asset carries the version in its name, so the fixture must too.
version_fixture="$test_root/fixtures/version"
cp -R "$good_fixture" "$version_fixture"
(
  cd "$version_fixture" &&
    mv reeve_linux_amd64.tar.gz reeve_0.9.0_linux_amd64.tar.gz &&
    sha256sum reeve_0.9.0_linux_amd64.tar.gz source-hash.txt > checksums.txt
)
COSIGN_FAKE_IDENTITY="https://github.com/reeveops/reeve/.github/workflows/release.yml@refs/tags/v0.9.0"
version_dir=$(run_fetch version-signed-from-tag v0.9.0 "$version_fixture")
unset COSIGN_FAKE_IDENTITY
assert_eq "$(cat "$version_dir/output")" fetched=true
[[ -x "$version_dir/reeve" ]] || fail "version fetch did not install an executable"
assert_contains "$(cat "$version_dir/log")" "signature verified (cosign keyless)"
assert_contains "$(cat "$version_dir/cosign.calls")" 'release\.yml@refs/tags/v0\.9\.0'
if grep -q 'refs/(heads|tags)' "$version_dir/cosign.calls"; then
  fail "the identity must be scoped to this version's tag, not any workflow or ref"
fi

# A per-commit build verified against the edge-on-master identity is
# accepted - edge-build.yml@refs/heads/master, exactly.
COSIGN_FAKE_IDENTITY="https://github.com/reeveops/reeve/.github/workflows/edge-build.yml@refs/heads/master"
edge_dir=$(run_fetch edge-signed-from-master "$commit_ref" "$good_fixture")
unset COSIGN_FAKE_IDENTITY
assert_eq "$(cat "$edge_dir/output")" fetched=true
assert_contains "$(cat "$edge_dir/cosign.calls")" 'edge-build\.yml@refs/heads/master'
if grep -q 'refs/(heads|tags)' "$edge_dir/cosign.calls"; then
  fail "per-commit builds must pin edge-build.yml@refs/heads/master, not any workflow or ref"
fi

# A version tag signed the OLD way (release.yml on master, as v0.8.0 and
# earlier were) is REFUSED: the refusal names the version, the identity
# required, the identity cosign found, and what happens next.
COSIGN_FAKE_IDENTITY="https://github.com/reeveops/reeve/.github/workflows/release.yml@refs/heads/master"
old_signed_dir=$(run_fetch version-signed-from-master-refused v0.9.0 "$version_fixture")
unset COSIGN_FAKE_IDENTITY
assert_eq "$(cat "$old_signed_dir/output")" fetched=false
[[ ! -e "$old_signed_dir/reeve" ]] || fail "a wrong-identity signature installed a binary"
old_log="$(cat "$old_signed_dir/log")"
assert_contains "$old_log" "signature verification FAILED for version v0.9.0"
assert_contains "$old_log" "must identify release.yml@refs/tags/v0.9.0"
assert_contains "$old_log" "certificate: https://github.com/reeveops/reeve/.github/workflows/release.yml@refs/heads/master"
assert_contains "$old_log" "the prebuilt binary is NOT used"
assert_contains "$old_log" "falling back to source build"
assert_contains "$old_log" "prebuilt fetch failed: the checksums signature does not come from release.yml@refs/tags/v0.9.0"

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
classify_step=$(step_block "Classify prebuilt binary eligibility")
cosign_step=$(step_block "Install cosign for binary verification")
setup_step=$(step_block "Set up Go (from reeve's go.mod)")
build_step=$(step_block "Build reeve")
assert_contains "$fetch_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$classify_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$cosign_step" "steps.reeve-binary.outputs.eligible == 'true'"
assert_contains "$fetch_step" "steps.reeve-binary.outputs.eligible == 'true'"
assert_contains "$setup_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$setup_step" "steps.reeve-fetch.outputs.fetched != 'true'"
assert_contains "$build_step" "steps.reeve-cache.outputs.cache-hit != 'true'"
assert_contains "$build_step" "steps.reeve-fetch.outputs.fetched != 'true'"

echo "fetch-binary tests passed"
