#!/usr/bin/env bash
set -euo pipefail

run=false
command=""
auto_args='[]'
unlock_ref=""
break_glass=false

append_arg() {
  auto_args=$(jq -cn --argjson current "$auto_args" --arg value "$1" '$current + [$value]')
}

emit() {
  {
    printf 'run=%s\n' "$run"
    printf 'command=%s\n' "$command"
    printf 'auto_args=%s\n' "$auto_args"
    printf 'unlock_ref=%s\n' "$unlock_ref"
    printf 'break_glass=%s\n' "$break_glass"
  } >> "$GITHUB_OUTPUT"
}

skip() {
  printf '%s\n' "$1"
  run=false
  command=""
  auto_args='[]'
  unlock_ref=""
  break_glass=false
  emit
  exit 0
}

authorized_association() {
  local association
  association=$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')
  local allowed
  IFS=',' read -ra entries <<< "${REEVE_ALLOWED_ASSOCIATIONS:-OWNER,MEMBER,COLLABORATOR}"
  for allowed in "${entries[@]}"; do
    allowed=$(printf '%s' "$allowed" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')
    if [[ -n "$allowed" && "$allowed" == "$association" ]]; then
      return 0
    fi
  done
  return 1
}

accepted_prefix() {
  local prefix
  IFS=',' read -ra prefixes <<< "${REEVE_COMMAND_PREFIXES:-/reeve}"
  for prefix in "${prefixes[@]}"; do
    prefix=$(printf '%s' "$prefix" | tr -d '[:space:]')
    if [[ -n "$prefix" && "$prefix" == "$1" ]]; then
      return 0
    fi
  done
  return 1
}

if [[ -n "${REEVE_INPUT_COMMAND:-}" ]]; then
  if [[ "$REEVE_INPUT_COMMAND" == *$'\n'* || "$REEVE_INPUT_COMMAND" == *$'\r'* ]]; then
    printf 'command input must be a single line\n' >&2
    exit 1
  fi
  run=true
  command="$REEVE_INPUT_COMMAND"
  emit
  exit 0
fi

case "${GITHUB_EVENT_NAME:-}" in
  pull_request_review)
    review_action=$(jq -r '.action // ""' < "$GITHUB_EVENT_PATH")
    if [[ "$review_action" != "submitted" ]]; then
      skip "pull_request_review action '$review_action' does not trigger reeve - skipping."
    fi
    if [[ "${REEVE_RUN_ON_APPROVAL:-false}" != "true" ]]; then
      skip "pull_request_review dispatch is opt-in (run-on-approval is not 'true') - skipping."
    fi
    review_state=$(jq -r '.review.state // ""' < "$GITHUB_EVENT_PATH")
    if [[ "$review_state" != "approved" ]]; then
      skip "Review state '$review_state' - skipping."
    fi
    association=$(jq -r '.review.author_association // "NONE"' < "$GITHUB_EVENT_PATH")
    if ! authorized_association "$association"; then
      skip "Reviewer not authorized (association: $association) - skipping."
    fi
    command="approved"
    ;;

  pull_request)
    action=$(jq -r '.action // ""' < "$GITHUB_EVENT_PATH")
    case "$action" in
      opened|reopened|synchronize)
        command="preview"
        ;;
      ready_for_review)
        command="ready"
        ;;
      closed)
        merged=$(jq -r '.pull_request.merged // false' < "$GITHUB_EVENT_PATH")
        if [[ "$merged" != "true" ]]; then
          skip "pull_request closed without merge - skipping."
        fi
        command="apply"
        append_arg "--trigger-source"
        append_arg "merge"
        ;;
      *)
        skip "pull_request action '$action' does not trigger reeve - skipping."
        ;;
    esac
    ;;

  issue_comment)
    comment_action=$(jq -r '.action // ""' < "$GITHUB_EVENT_PATH")
    if [[ "$comment_action" != "created" ]]; then
      skip "issue_comment action '$comment_action' does not trigger reeve - skipping."
    fi
    if [[ "$(jq -r 'has("issue") and (.issue.pull_request != null)' < "$GITHUB_EVENT_PATH")" != "true" ]]; then
      skip "Comment is not attached to a pull request - skipping."
    fi
    author_type=$(jq -r '.comment.user.type // ""' < "$GITHUB_EVENT_PATH")
    author_login=$(jq -r '.comment.user.login // ""' < "$GITHUB_EVENT_PATH")
    if [[ "$author_type" == "Bot" || "$author_login" == *"[bot]" ]]; then
      skip "Comment authored by bot '$author_login' - skipping (self-trigger guard)."
    fi
    comment_body=$(jq -r '.comment.body // ""' < "$GITHUB_EVENT_PATH")
    comment_body=${comment_body//$'\r'/}
    first_line=${comment_body%%$'\n'*}
    read -r prefix verb rest <<< "$first_line"
    if ! accepted_prefix "${prefix:-}"; then
      skip "Not a reeve command, skipping."
    fi
    association=$(jq -r '.comment.author_association // "NONE"' < "$GITHUB_EVENT_PATH")
    if ! authorized_association "$association"; then
      skip "Commenter not authorized to run reeve commands (association: $association) - skipping."
    fi
    case "${verb:-}" in
      apply|up)
        command="apply"
        append_arg "--trigger-source"
        append_arg "comment"
        if [[ " $rest " == *" --force "* || " $rest " == *" force "* ]]; then
          append_arg "--force"
        fi
        if [[ " $rest " == *" --refresh "* ]]; then
          append_arg "--refresh"
        fi
        ;;
      refresh)
        command="refresh"
        if [[ " $rest " == *" --dry-run "* || " $rest " == *" dry-run "* ]]; then
          append_arg "--dry-run"
        fi
        if [[ " $rest " == *" --all "* || " $rest " == *" all "* ]]; then
          append_arg "--all"
        fi
        ;;
      ready)
        command="ready"
        ;;
      approve)
        command="approved"
        ;;
      breakglass)
        command="apply"
        append_arg "--break-glass"
        break_glass=true
        ;;
      preview|plan)
        command="preview"
        append_arg "--plan-requested"
        ;;
      unlock)
        command="unlock"
        read -r first _ <<< "$rest"
        if [[ -n "${first:-}" && "$first" != -* && "$first" == */* ]]; then
          unlock_ref="$first"
        fi
        if [[ " $rest " == *" --force "* || " $rest " == *" force "* ]]; then
          append_arg "--force"
        fi
        ;;
      explain)
        command="run explain"
        read -r first second _ <<< "$rest"
        if [[ -n "${second:-}" ]]; then
          skip "explain takes at most one project/stack selector, got: $rest - skipping."
        fi
        if [[ -n "${first:-}" && "$first" != -* ]]; then
          append_arg "--stack"
          append_arg "$first"
        fi
        ;;
      help)
        command="pr-help"
        ;;
      *)
        skip "Unknown reeve command '${verb:-}', skipping."
        ;;
    esac
    ;;

  *)
    skip "Not a reeve event (${GITHUB_EVENT_NAME:-}), skipping."
    ;;
esac

run=true
emit
