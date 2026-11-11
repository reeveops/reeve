# comment-approval-ignore-edited

## Why

A `/reeve approve` comment whose body is edited after posting still counted.
The gate sees the edited body, but the edit can silently change reviewed
intent: the approval that existed before the edit no longer describes what the
commenter agreed to, and nothing told the reviewer an edit happened.

## What

A comment approval is accepted only while the comment is unedited: `updated_at`
must equal `created_at`. An edited approval comment does not count, and the
trace states the refusal with the corrective step:

> Approval by @user ignored: the comment was edited after posting. Post a new
> `/reeve approve <head>` comment.

GitHub moves `updated_at` when a comment is edited; the API does not
distinguish body edits from other metadata edits, so any `updated_at` movement
refuses (over-refusing a cheap re-post is safe; trusting an edited approval is
not).

## Scope

- `internal/vcs/github/comment_approvals.go`: the edit check and the note.
- `internal/core/approvals`: an approval may carry a user-facing `Note`; when
  set, evaluation skips the approval and renders the note on the trace.

## Impact

- No config change.
- **Breaking for the opt-in `pr_comment` source:** an edited approval must be
  re-posted.
