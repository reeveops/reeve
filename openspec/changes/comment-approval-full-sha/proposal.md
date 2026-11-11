# comment-approval-full-sha

## Why

`resolveApprovedCommit` bound `/reeve approve <sha>` to HEAD when `<sha>` was
any prefix of at least 7 characters. Committer timestamps are attacker-settable,
so a 7-character prefix can be ground for ~2^28 work. An approval naming a
planted prefix, written before the commit it names existed, would later bind to
the malicious commit once it became HEAD and count as a review of code nobody
saw.

## What

A comment approval pins to HEAD only when the argument is the full 40-character
HEAD SHA. Any other argument binds to the literal value, never equals HEAD, and
is reported as ignored with the failing value and the corrective step:

> Approval by @user ignored: `<arg>` is not the full 40-character SHA of HEAD
> `<head>`. Re-approve with `/reeve approve <head>`.

## Scope

- `internal/vcs/github/comment_approvals.go` (`resolveApprovedCommit`).
- The refusal text renders through the approval evaluation trace
  (`internal/core/approvals`): the trace line is where approval outcomes reach
  the PR, so the message for non-40-character comment bindings was added there.

## Impact

- No config change.
- **Breaking for the opt-in `pr_comment` source only:** short-SHA approvals stop
  counting. Users must re-approve with `/reeve approve <full-sha>`.
- Nothing is silently skipped: the ignored approval is named with the value that
  failed.
