# PR Comment Rendering

## Single comment, edited in place

Identified by hidden HTML marker (`<!-- reeve:pr-comment:v1 -->`). VCS
adapter's `UpsertComment` handles find-or-create via marker match. If the
VCS adapter reports `CommentCapabilities.SupportsEdit == false`, append
fallback kicks in - out of scope until a non-GitHub adapter ships.

## Layout

Header line: `## <status-icon> reeve · <op> · run #<n> · [commit <sha>]`
followed by total counts, duration, and a link to the CI run.

Table summarizing all affected stacks with columns:
`Stack | Env | Add | Change | Delete | Replace | Status`.

Per-stack sections below the table - status-grouped sort order (blocked,
ready, no-op last), each with: required approvers (if any), then collapsed
`<details>` for Summary and Full plan output. No-op stacks collapse to a
single table line with no section.

A help comment is upserted separately under marker `<!-- reeve:help -->`,
listing available commands. A ready comment is upserted under
`<!-- reeve:ready -->` when `/reeve ready` is triggered (manually or via `auto_ready`).

Apply comment mirrors preview structure, adds durations, floats failures
to top.

## `comments.style`

Controls how reeve posts PR comments. Three modes: `replace` (default) upserts
a single comment in place using the same marker (`<!-- reeve:pr-comment:v1 -->`);
`append` posts a new comment on every run without editing the previous one;
`section` uses a separate marker for apply results (`<!-- reeve:apply:v1 -->`)
while preview keeps `<!-- reeve:pr-comment:v1 -->`, so preview and apply history
remain distinct threads.

## `comments.stack_view`

Controls which stacks the table lists:

- `all` (default) - every declared stack, no-ops included.
- `changed` - only stacks with planned/applied changes.

Per-stack sections always skip no-ops regardless of view.

## Apply timeline

Each commit owns one comment, pinned by a per-commit marker
(`<!-- reeve:apply-timeline:<short-sha> -->`). Every run of that commit - the
first apply, a retry, a `--force` re-apply - appends to the same thread and
edits the comment in place rather than posting a new one. Entries are persisted
per commit (compare-and-swap) so concurrent runs never lose each other's
history, and the header shows the latest run to touch the commit. Because
editing a comment is silent while creating one fires an `issue_comment` webhook,
consolidating per commit also stops reeve from spawning a fresh (self-trigger
guard-skipped) workflow run for every progress update.

```
### 🚀 reeve · apply · [run #N](<url>) · [commit <sha>]
- 🚀 **apply starting**
- ✅ **applied**: 2 stack(s): api/prod, worker/prod
```

- 🚀 `apply starting` - posted before any stack runs.
- ✅ `applied` - changed stack refs.
- 🔴 `failed` - failing stack refs.
- 🔒 `blocked` - gate reason.
- ⏭️ `skipped` - commit already applied, or docs/asset-only changes.
- 📡 `scope broadened` - unmapped files; applying all stacks.

Separate from the replace-style dashboard comment.

## Safety rails

- Secrets marked by Pulumi `[secret]` are redacted before render.
- All rendered output funnels through `internal/core/redact` - no output
  path bypasses redaction.
- Replacement counts > 0 trigger a prominent warning block.

## Sort orders

- `status_grouped` (default): blocked → ready → no-op.
- `alphabetical`: by `{project}/{stack}`.
- `env_priority`: configured priority order (e.g. `prod > staging > dev`).

## Testing

Golden files. Every rendering change requires a new golden file + diff
review.
