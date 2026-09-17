#!/usr/bin/env bash
set -euo pipefail

mode=${1:-resolve}

fail() {
  printf 'reeve workload identity: %s\n' "$1" >&2
  exit 1
}

validate_sha() {
  [[ "$1" =~ ^[0-9a-f]{40}$ ]] || fail "expected a full lowercase commit SHA"
}

validate_repo() {
  [[ "$1" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "invalid GITHUB_REPOSITORY"
}

fetch_pr_head() {
  local pr_number=$1
  local repository=${GITHUB_REPOSITORY:-}
  local token=${GITHUB_TOKEN:-}
  local api_url=${GITHUB_API_URL:-https://api.github.com}
  [[ "$pr_number" =~ ^[1-9][0-9]*$ ]] || fail "invalid pull request number"
  validate_repo "$repository"
  [[ -n "$token" ]] || fail "GitHub token is required to resolve a pull request head"

  local response
  response=$(curl --fail --silent --show-error --retry 2 \
    -H "Authorization: Bearer $token" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "${api_url%/}/repos/$repository/pulls/$pr_number") || fail "GitHub rejected the pull request lookup"

  local head
  head=$(jq -er '.head.sha' <<< "$response") || fail "pull request response has no head SHA"
  validate_sha "$head"
  printf '%s' "$head"
}

event_pr_number() {
  case "${GITHUB_EVENT_NAME:-}" in
    pull_request|pull_request_review)
      jq -er '.pull_request.number' < "$GITHUB_EVENT_PATH"
      ;;
    issue_comment)
      if [[ $(jq -r 'has("issue") and (.issue.pull_request != null)' < "$GITHUB_EVENT_PATH") == true ]]; then
        jq -er '.issue.number' < "$GITHUB_EVENT_PATH"
      fi
      ;;
  esac
}

resolve() {
  local pr_number=""
  pr_number=$(event_pr_number || true)
  local is_pr=false
  local sha=""

  if [[ -n "$pr_number" ]]; then
    is_pr=true
    case "${GITHUB_EVENT_NAME:-}" in
      pull_request|pull_request_review)
        sha=$(jq -er '.pull_request.head.sha' < "$GITHUB_EVENT_PATH") || fail "event has no pull request head SHA"
        ;;
      issue_comment)
        sha=$(fetch_pr_head "$pr_number")
        ;;
      *)
        fail "unsupported pull request event"
        ;;
    esac
  else
    sha=${GITHUB_SHA:-}
  fi

  validate_sha "$sha"
  {
    printf 'sha=%s\n' "$sha"
    printf 'pr_number=%s\n' "$pr_number"
    printf 'is_pr=%s\n' "$is_pr"
  } >> "$GITHUB_OUTPUT"
}

verify() {
  local expected=${REEVE_WORKLOAD_SHA:-}
  local root=${REEVE_WORKLOAD_ROOT:-${GITHUB_WORKSPACE:-}}
  local is_pr=${REEVE_WORKLOAD_IS_PR:-false}
  local pr_number=${REEVE_WORKLOAD_PR:-}

  validate_sha "$expected"
  [[ -n "$root" ]] || fail "workload root is required"

  local checked_out
  checked_out=$(git -C "$root" rev-parse --verify 'HEAD^{commit}') || fail "workload checkout has no commit"
  validate_sha "$checked_out"
  [[ "$checked_out" == "$expected" ]] || fail "checkout is $checked_out, expected $expected"

  if [[ "$is_pr" == true ]]; then
    local current
    current=$(fetch_pr_head "$pr_number")
    [[ "$current" == "$expected" ]] || fail "pull request head moved from $expected to $current"
  elif [[ "$is_pr" != false ]]; then
    fail "invalid PR identity flag"
  fi
}

case "$mode" in
  resolve) resolve ;;
  verify) verify ;;
  *) fail "unknown mode $mode" ;;
esac
