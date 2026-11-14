# explicit-command-gate

## Why

The classifier's explicit `command` input returned before the event, bot, and
`author_association` checks: the caller was assumed pre-trusted. On
`issue_comment` and `pull_request_review` events nothing upstream vouches for
the actor, so an explicit command could smuggle a run past the authorization a
comment command receives.

## What

On `issue_comment` and `pull_request_review` events an explicit command passes
the same bot and association gates as a comment command. A refused dispatch
exits non-zero and says what was refused, who, with which association, and what
the allowlist accepts:

> \<command\> refused: @user has association \<assoc\>; allowed: \<list\>.

Bot actors are refused under the self-trigger guard. Other events
(`workflow_dispatch`, `schedule`, `push`) keep dispatching an explicit command
unchanged: their caller is the workflow, not a comment.

## Scope

- `.github/scripts/classify-event.sh` and its test harness.

## Impact

- No config change.
- **Behavior change:** workflows passing `command:` on comment events become
  gated on the same association allowlist and bot guard as comment commands.
  The repo-permission boundary (e.g. requiring `write`) is specified separately.
