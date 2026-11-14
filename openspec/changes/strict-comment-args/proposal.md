# strict-comment-args

## Why

The comment classifier accepted an exact flag list per verb but silently
ignored anything else. `/reeve refresh --dry-rnu` dispatched a writing
`refresh`; `/reeve refresh --dry-run=true` dispatched a writing `refresh` too
(the flag with a value was not recognized). Refresh intentionally skips the
apply approval and freshness gates, so a malformed dry run became a write.

## What

Every comment verb accepts an exact argument set. The first token outside it
refuses the dispatch: the classifier exits non-zero, the job is red, and the
refusal names the verb, the failing argument, and everything the verb accepts:

> /reeve \<verb\> refused: unknown argument \`\<arg\>\`. Accepted: \<list\>.

Per-verb accepted sets: `apply`/`up` — `--force`, `force`, `--refresh`;
`refresh` — `--dry-run`, `dry-run`, `--all`, `all`; `unlock` — `--force`,
`force`, one `<project/stack>` positional; `explain` — one `<project/stack>`
selector (the existing "at most one" skip stays); `preview`/`plan`, `ready`,
`help` — no arguments. `approve` and `breakglass` accept their free-form
payload (the approval SHA is validated by the approval source with its own
refusal; the break-glass justification is the comment body).

## Scope

- `.github/scripts/classify-event.sh`.
- Tests drive the script directly through the existing harness.

## Impact

- No config change.
- **Behavior change:** previously-tolerated unknown or typo'd arguments now
  refuse the dispatch with a red job instead of silently ignoring.

**Follow-up (review):** a refusal is now ALSO posted as a reply on the PR
(the commenter watches the PR, not the job log), best effort - a posting
failure never changes the refusal outcome. The posting context rides the
existing action token; PR-side, nothing else changes. `explain a b` is now a
refusal with the accepted form instead of a quiet skip: a malformed command
must never dispatch as something else. The loop expansions use the
`${arr[@]+...}` guard so `set -u` holds on bash < 4.4 (macOS runners ship
bash 3.2).
