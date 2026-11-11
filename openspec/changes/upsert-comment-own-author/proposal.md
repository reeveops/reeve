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
