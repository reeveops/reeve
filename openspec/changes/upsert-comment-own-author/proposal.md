# upsert-comment-own-author

## Why

`UpsertComment` matched ANY comment whose body contained the marker and
rewrote it. A foreign comment that merely quotes the marker (e.g. a
maintainer quoting reeve's board, a template with the hidden comment in it)
was overwritten - destroying someone else's text under reeve's name.

## What

The VCS adapter requires the marker AND the authenticated author: a marker
comment may be edited only when its author is the account the token
authenticates as (the existing `authenticatedCommentAuthor` resolution,
including the `github-actions[bot]` fallback). A foreign marker comment is
left alone and logged with its author; reeve creates its own comment
instead. Identity resolution failing fails the upsert closed - without it
reeve cannot tell its own comments from anyone else's, so nothing is edited
or created.

## Scope

- `internal/vcs/github/client.go` (`upsertCommentLocked` author gate).

## Impact

- No config change.
- One duplicate comment appears if the token identity changes between runs.

**Follow-ups (review):** the author is resolved through `/user` ONLY - the
old `github-actions[bot]` guess is gone: it was wrong for GitHub App
installation tokens (whose bot is `<slug>[bot]`, not
`github-actions[bot]`), and a wrong author makes every upsert post a
duplicate comment forever. When the identity cannot be resolved, the write
is refused with a typed error naming the fix, logged loudly. The explicit
escape hatch: `comments.author` in shared yaml and the `--comment-author`
CLI flag (every repo command), declaring the login the token writes as and
bypassing `/user` resolution entirely. No new keys were needed for the
match-author rule itself.
