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
`<!-- reeve:ready -->` when `/reeve ready` is triggered (manually or from a ready-for-review event).

Apply comment mirrors preview structure, adds durations, floats failures
to top. Apply writes the same marker preview wrote for that commit, so a
commit has one board.

## `comments.style`

Controls how reeve posts dashboard comments. Three modes:

- `replace` (default) upserts one comment per PR under
  `<!-- reeve:pr-comment:v1 -->`. Every operation edits it.
- `section` upserts one comment per commit SHA, under
  `<!-- reeve:pr-comment:v1:<short-sha> -->`. Preview and apply of one SHA share
  that comment; a new SHA mints a new one, and a previous SHA's comment is never
  edited again, so the plan it recorded stays readable.
- `append` posts a new comment every run without editing the previous one.

`section` does not split by operation. The marker `<!-- reeve:apply:v1 -->` is
retired; comments already posted under it are left in place.

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

## Size-limit trimming

GitHub rejects an oversize comment with a non-recoverable 422, so a rendered body
must always fit. Renderers drop content in a fixed order, least-read first:

1. full engine output (raw plan blob)
2. per-stack diff
3. plan summaries
4. error text, clamped per stack rather than dropped
5. whole per-stack sections, every stack keeping its table row
6. table rows

Rung 1 is silent on a preview: the blob is never read from a comment and the
diff survives. On an apply or refresh it is the engine's own output, so it is
named. From rung 2 on, the body carries a note naming what is missing.

Trimming never cuts the document mid-structure. Each rung removes whole units,
so tables, code fences, and `<details>` blocks stay closed. Dropped sections and
dropped rows are stated in the comment with a count - silence would read as
"nothing to report" for those stacks.

Whatever a renderer reports dropping is written to the run log, so the note's
pointer at the full run output is true. Errors are logged whenever they were
clamped or their section went; a stack absent from the table is named nowhere in
the comment, so the log is its only record.

## Comment overflow

`comments.overflow.mode: continue` makes an oversize board continue into further
comments instead of dropping per-stack detail. Default is `drop`, the trim
ladder above.

Part 1 carries the board's own marker, byte-identical, so an existing board keeps
being edited. Later parts carry `:partN` inside that marker. A board that fits one
comment is byte-identical to an unpaginated render.

The stack table is whole on part 1 and absent from the rest: it indexes the run,
so every stack is listed there even when its detail is on a later part. A stack's
detail is never split across two parts. Every part states its ordinal and total.

`split` selects the distribution: `divided` (default) balances stacks across the
fewest parts that fit, `stack` fills each part in render order, `group` gives each
status group its own part.

`max_parts` bounds the count. Stacks past the cap are named with a count in the
last part and written to the run log.

A single stack too large for a whole comment falls back to the trim ladder for
that stack alone, leaving other parts untouched.

A run needing fewer parts than the last deletes the surplus comments. Failing to
delete is logged, never fatal.

## Safety rails

- Secrets marked by Pulumi `[secret]` are redacted before render.
- All rendered output funnels through `internal/core/redact` - no output
  path bypasses redaction.
- Replacement counts > 0 trigger a prominent warning block.

## Sort orders

- `status_grouped` (default): blocked → ready → no-op.
- `alphabetical`: by `{project}/{stack}`.
- Other sort values are not supported unless implemented by the current renderer and configuration schema.

## Testing

Golden files. Every rendering change requires a new golden file + diff
review.
