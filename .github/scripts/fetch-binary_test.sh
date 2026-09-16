#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=.github/scripts/fetch-binary.sh
source "$SCRIPT_DIR/fetch-binary.sh"

assert_eq() {
  if [[ "$1" != "$2" ]]; then
    echo "got '$1', want '$2'" >&2
    exit 1
  fi
}

assert_eq "$(classify_ref v1.2.3)" version
assert_eq "$(classify_ref master)" edge
assert_eq "$(classify_ref next)" edge
assert_eq "$(classify_ref 0123456789abcdef0123456789abcdef01234567)" commit
assert_eq "$(classify_ref 0123456789ABCDEF0123456789ABCDEF01234567)" commit
assert_eq "$(classify_ref 0123456)" other
assert_eq "$(classify_ref feature/test)" other

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
REEVE_SOURCE_HASH=$(printf 'a%.0s' {1..64})
export REEVE_SOURCE_HASH
printf '%s\n' "$REEVE_SOURCE_HASH" > "$workdir/source-hash.txt"
verify_source_hash "$workdir/source-hash.txt"

printf '%064d\n' 0 > "$workdir/source-hash.txt"
if verify_source_hash "$workdir/source-hash.txt" 2> /dev/null; then
  echo "verify_source_hash accepted a mismatch" >&2
  exit 1
fi

echo "fetch-binary tests passed"
