# Approvals

## Sources

Pluggable. v1 ships:

- `pr_review` - default; reads PR reviews via the VCS adapter. On unless an
  explicit `sources` entry lists `pr_review` with `enabled: false`. When no
  `sources` block is configured at all, this is the only active source -
  identical to reeve's original behavior.
- `pr_comment` - opt-in; parses `/reeve approve` in PR comments. Off unless an
  explicit `sources` entry lists `pr_comment` with `enabled: true`.

Future (v2+): `slack_interaction`, `webhook`. Each is a new source
implementation - no core changes.

Source ordering in `shared.yaml` matters only for tie-breaking attribution.

### Public repositories and unlisted approvals

On a **public** repository any GitHub user can submit an approving review, so
the no-allow-list path (a bare `required_approvals` with no `approvers` list
and no CODEOWNERS — including the injected safety default of 1) is not a real
gate: an unlisted account's review would satisfy it. On a public repo that
path therefore fails closed with an actionable message telling the operator to
configure an `approvers` list or CODEOWNERS, or to opt in explicitly. The
opt-in is `approvals.allow_unlisted_approvals_on_public: true` (default
false); it does not block the ability, only makes the operator name the risk.
Private repositories are unaffected — there the reviewer set is already the
collaborator set — and a public repo that configures an `approvers` list or
CODEOWNERS is a real gate and never hits this guard. Repo visibility comes
from the VCS adapter (`PR.RepoPrivate`); an adapter that cannot determine it
reports public, the fail-closed (stricter) direction.

### Union across sources

Enabled sources are gathered independently and **unioned**. Deduplication is
by approver identity: a human who approves via *both* a review and a
`/reeve approve` comment counts **once** toward `required_approvals`. Every
per-approval rule below (the non-author rule, freshness, dismiss-on-new-commit)
is applied **uniformly** across sources before that identity dedup.

### `pr_comment` authorization (fail-closed)

A `/reeve approve` comment only counts when **all** hold:

- The comment's first line is `<prefix> approve`, where `<prefix>` exactly
  matches a configured command prefix (default `/reeve`, `@reeve`) - parsed the
  same way every other `/reeve` command is.
- The commenter's `author_association` is in the same allowlist that gates
  command dispatch (default `OWNER`, `MEMBER`, `COLLABORATOR`). The source
  re-checks this itself because it reads historical comments directly, not the
  dispatched event; an unauthorized commenter's `/reeve approve` is ignored.
  An empty allowlist denies everyone.
- The commenter is not a bot (mirrors the self-trigger guard) and is **not the
  PR author** (the non-author rule - an author never self-approves, via review
  or comment).

A comment approval is stamped with the SHA that was HEAD when the comment was
posted (the newest commit at or before the comment's creation time), mirroring
how a review carries its `commit_id`. Under `dismiss_on_new_commit` (default
on) a comment approval is therefore **dismissed when a newer commit lands**,
exactly like a stale review. Where the intended commit is ambiguous (a comment
predating every commit), the source picks the oldest commit so dismissal still
fires - the fail-closed choice.

## Rule resolution

Layered, not either/or:

- `default` baseline applies to all stacks.
- `stacks.<pattern>` rules **merge** with default: approver lists union,
  more specific overrides numeric fields (e.g. `required_approvals`).
- `require_all_groups: true` means one approval from each listed group,
  not N-of-any.
- CODEOWNERS integration optional; when enabled, honored alongside team
  rules.
- Stale reviews dismissed on new commits (configurable, GitHub-only
  capability - declared via VCS capability flag).

`reeve rules explain <stack>` shows full resolution trace.

## Break-glass (emergency override)

A `break_glass:` block in shared.yaml enables an emergency apply path that
overrides the approvals gate in exchange for a mandatory non-empty
justification and a loud immutable audit record. With no block configured,
`/reeve breakglass` fails closed with a polite error explaining how to
enable it, and no run starts. An empty or whitespace-only justification is
rejected before any lock, credential, or engine call.

### Authorization (fail-closed union)

`break_glass.authorized` supports a union of sources - any source granting
the actor is sufficient:

- `internal_list` - explicit logins and `org/team` slugs.
- `codeowners` - the actor is an owner (directly or via team) of at least
  one changed path.
- `anyone`.

The matched source is recorded narrowest-first, so the audit names the most
specific grant. A denial carries a trace explaining every source consulted.
The `vcs_bypass` source and `groups:` (`group:<provider>:<name>`) are
accepted by the config parser but rejected at authorization time with clear
"not yet supported" errors - rejected even when another source would match,
so operators immediately learn the source is inert.

### Head-resolved; self-add flagged, not forbidden (with opt-in lockdown)

Authorization resolves against the break-glass config and CODEOWNERS
content as of the PR HEAD. Adding oneself to the authorization surface in
the same PR is allowed BY DESIGN (the emergency responder may need to) — the
default trades this availability for a loud audit trail: the audit record
flags when any authorizing file - a `.reeve/*.yaml`/`.yml` config or a
CODEOWNERS file (`CODEOWNERS`, `.github/CODEOWNERS`, `docs/CODEOWNERS`) - was
modified in the same PR, listing the touched paths.

Operators who prefer to fail closed rather than allow same-PR
self-authorization set `break_glass.reject_self_authorization: true` (default
false). When set, a PR that modifies its own authorizing files cannot
authorize a break-glass apply no matter which source would grant — the denial
is evaluated before any source and its trace names the touched paths. This is
a per-repo choice: the default keeps late-night availability, the opt-in
locks it down.

Gate-override semantics live in `openspec/specs/core/preconditions`;
comment/audit surfacing in `openspec/specs/notifications`.

## Out of scope (v1)

- `requires_incident_link` on break-glass - dropped until a user asks. No
  runtime validation of incident links is specified.
