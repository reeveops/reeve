#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
conftest test --policy policies fixtures/allowed.json
for fixture in missing-tag empty-tag unknown-tag missing-tags second-resource-missing-tag disallowed-region missing-region production-delete wrong-shape; do
  if conftest test --policy policies "fixtures/$fixture.json"; then
    printf 'expected %s to be denied\n' "$fixture" >&2
    exit 1
  fi
done
python3 scripts/change-limit.py fixtures/allowed.json 20
if python3 scripts/change-limit.py fixtures/large-change.json 20; then
  printf 'expected advisory large-change warning\n' >&2
  exit 1
fi

# The cost integration sketch is retained; no live pricing API is exercised.
bash -n scripts/cost-gate.sh
