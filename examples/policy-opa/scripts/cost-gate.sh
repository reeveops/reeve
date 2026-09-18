#!/usr/bin/env bash
# Cost gate - warns when monthly delta exceeds an env-specific cap.
#
# Usage: cost-gate.sh <plan.json> <env>
#
# Exits 0 on pass, 2 on threshold exceeded (we use 2 rather than 1 so
# misconfigured invocations - exit 1 from the shell itself - are
# distinguishable from policy violations).
set -euo pipefail

PLAN="${1:?plan path required}"
ENV="${2:-unknown}"

# Per-env monthly delta caps. Override at invoke time.
case "$ENV" in
  prod)    CAP="${MAX_MONTHLY_DELTA_USD:-1000}" ;;
  staging) CAP="${MAX_MONTHLY_DELTA_USD:-300}"  ;;
  *)       CAP="${MAX_MONTHLY_DELTA_USD:-100}"  ;;
esac

if ! command -v infracost >/dev/null 2>&1; then
  echo "cost-gate: infracost not installed; skipping" >&2
  exit 0
fi

# infracost emits a JSON with projects[].diff.totalMonthlyCost
DELTA=$(infracost breakdown --path "$PLAN" --format json 2>/dev/null \
  | jq -r '[.projects[].diff.totalMonthlyCost | tonumber] | add // 0')

if awk -v d="$DELTA" -v c="$CAP" 'BEGIN { exit (d > c) ? 0 : 1 }'; then
  printf 'cost-gate (%s): monthly delta $%s exceeds cap $%s\n' "$ENV" "$DELTA" "$CAP"
  exit 2
fi

printf 'cost-gate (%s): monthly delta $%s within cap $%s\n' "$ENV" "$DELTA" "$CAP"
