# Configuration reference

Everything under `.reeve/` is strict YAML: unknown keys are errors, versions
are per-file, and schemas are stable within a major version.

## File layout

```text
.reeve/
├── shared.yaml           # bucket, approvals, locking, preconditions, freeze, apply
├── auth.yaml             # credential providers and bindings
├── notifications.yaml    # notification channels (slack, webhook, pagerduty, ...)
├── observability.yaml    # OTEL + annotations
├── drift.yaml            # drift scope, schedules, channels
├── pulumi.yaml           # engine: pulumi
└── terraform.yaml        # engine: terraform / tofu
```

Every file begins with:

```yaml
version: 1
config_type: <shared|engine|auth|notifications|observability|drift|user>
```

- `version` is per-file. Bumps affect only that schema.
- `config_type` is one-per-file. Engine files are keyed by `engine.type`,
  but reeve currently supports only one engine config per repo - loading
  more than one is a validation error.
- Unknown top-level keys fail `reeve lint`.

Config always lives under `.reeve/`. reeve reads that directory and only that
directory; there is no root-level single-file `reeve.yaml` form.

---

## `shared.yaml`

```yaml
version: 1
config_type: shared

bucket:
  type: s3                         # filesystem | s3 | gcs | azblob | r2
  name: mycompany-reeve
  region: us-east-1
  prefix: reeve/                   # optional sub-prefix

comments:
  sort: status_grouped             # status_grouped (default) | alphabetical
  collapse_threshold: 10
  show_gates: true
  style: replace                   # replace (default) | append | section
  stack_view: all                  # all (default) | changed

retention:
  max_age: 720h                    # default 720h (1 month); 0 disables pruning

locking:
  ttl: 4h                          # default 4h; also bounds the lease of holders promoted from the queue
  queue: fifo                      # v1: fifo (only option)
  reaper_interval: 15m             # informational; reaper is opportunistic
  admin_override:                  # gates force-unlock (locks unlock without --pr);
    allowed: ["@org/sre-leads"]    # PR-scoped removal (--pr / "/reeve unlock") is
    requires_reason: true          # self-service and not gated here

# Locks require a bucket that ENFORCES conditional writes (If-Match /
# If-None-Match). Real S3, GCS, Azure Blob, current MinIO/R2, and the
# filesystem backend all do; some older S3-compatibles accept the headers
# but ignore them, which would turn locks into silent no-ops. reeve
# probes this once per process on first lock use (two conditional writes
# against a throwaway locks/.cas-probe/* key) and refuses to operate if
# the bucket does not enforce conditions.

approvals:
  sources:
    - type: pr_review              # default VCS reviews
      enabled: true
    - type: pr_comment             # opt-in: "/reeve approve" in PR comments
      enabled: false
      command: "/reeve approve"
  allow_unlisted_approvals_on_public: false  # public repos: see note below
  default:
    required_approvals: 1
    approvers: ["@org/infra-reviewers"]
    codeowners: true               # honor CODEOWNERS alongside team rules
    dismiss_on_new_commit: true
  stacks:
    "prod/*":
      required_approvals: 2
      approvers: ["@org/sre", "@org/security"]
      require_all_groups: true     # one from each group, not N-of-any
    "prod/payments":
      approvers: ["@org/payments-leads"]

preconditions:
  require_up_to_date: true
  require_checks_passing: true
  preview_freshness: 2h            # preview must be newer than this ("0" disables - see below)
  preview_max_commits_behind: 5

freeze_windows:
  - name: friday-afternoon
    cron: "0 15 * * 5"             # Fri 3pm
    duration: 65h                  # through Monday morning
    stacks: ["prod/*"]

break_glass:                       # opt-in emergency apply; OFF when absent
  authorized:                      # UNION: any matching source grants
    internal_list: ["alice", "myorg/sre"]
    codeowners: true               # owners of changed paths may break-glass
    anyone: false
    vcs_bypass: false              # config surface only — not yet supported
  override_freeze: true            # default true
  reject_self_authorization: false # default false — see "Break-glass" below

apply:
  trigger: comment                 # comment (default) | merge — see "apply.trigger" below
  allow_fork_prs: false            # deny-by-default - review risk before flipping
  auto_ready: false                # reserved — not yet enforced (draft→ready already notifies for
                                   # approval whenever a plan has succeeded)
```

### `apply.trigger`

Selects **how an apply is initiated**. It is a flow selector, not a gate: it
changes only *when* an apply starts, never *whether* the gates hold. Every gate
(approvals, checks-green, preview freshness, locks, freeze windows, fork policy)
is enforced identically in both modes. Exactly one initiation path applies per
repo — the binary is the source of truth and no-ops (with a log line) on the
path that does not match the configured mode, so a mis-fired event can never
force an apply.

| Value | Behavior |
| --- | --- |
| `comment` (default) | **Apply-then-merge.** Apply runs only from a `/reeve apply` (or `@reeve apply` / `@reeve up`) PR comment, before the PR is merged. A merge event is a no-op. |
| `merge` | **Merge-then-apply (continuous delivery).** Apply runs automatically the moment the PR is **merged**. A `/reeve apply` comment is a no-op. |

Break-glass (`/reeve breakglass "<reason>" apply`) is exempt from the trigger
selector and works in either mode — it is an explicit, authorized emergency
override with its own authorization and audit trail.

**Enabling `merge` mode** requires two changes:

1. Set `apply.trigger: merge` in `.reeve/shared.yaml`.
2. Add `closed` to the workflow's `pull_request` trigger so reeve sees the
   merge, and keep the merge-apply out of the cancel-on-push concurrency group
   (a merge-triggered apply holds per-stack locks and must never be cancelled):

   ```yaml
   on:
     pull_request:
       types: [opened, reopened, synchronize, ready_for_review, closed]
   concurrency:
     # closed (merge) events join the non-cancellable "command" group.
     group: reeve-${{ (github.event_name == 'pull_request' && github.event.action != 'closed') && 'preview' || 'command' }}-${{ github.event.pull_request.number || github.event.issue.number }}
     cancel-in-progress: ${{ github.event_name == 'pull_request' && github.event.action != 'closed' }}
   ```

Only a **merged** close dispatches an apply; a PR closed without merging runs
nothing. On a merged PR every gate is still evaluated against the PR HEAD SHA
(the same SHA preview recorded against), so approvals, checks, preview
freshness, locks, and freeze all resolve exactly as they would pre-merge. The
one gate whose *result* can differ post-merge is `require_up_to_date`: after the
merge the base branch has advanced past the PR HEAD, so if you enable that gate
it will report "behind base" and **block** (fail-closed) — it never opens.
`require_up_to_date` is off by default and is intended for the apply-then-merge
flow; leave it off under `merge` mode.

### `comments.style`

Controls how the apply comment relates to the preview comment.

| Value | Behavior |
| --- | --- |
| `replace` (default) | Apply upserts using the same marker as preview, replacing it in-place. |
| `append` | Apply always posts a new comment; the preview comment is left untouched. |
| `section` | Apply upserts with a separate marker (`<!-- reeve:apply:v1 -->`), so preview and apply each occupy their own comment slot. |

> **Draft PRs:** apply is always blocked on draft PRs regardless of config.
> Convert to ready for review first. When a draft PR becomes ready, reeve runs `/reeve ready`
> automatically, notifying for approval if a plan has already succeeded.

### `comments.stack_view`

Controls which stacks the comment table lists.

| Value | Behavior |
| --- | --- |
| `all` (default) | Lists every declared stack, no-ops included. |
| `changed` | Lists only stacks with planned/applied changes. |

### Apply timeline

Each commit owns one PR comment, pinned by a per-commit marker. Every run of
that commit (first apply, retry, `--force` re-apply) appends to the same thread
and edits the comment in place instead of posting a new one; entries persist per
commit so concurrent runs never lose each other's history. Events append in
order:

- 🚀 `apply starting`
- ✅ `applied` — with changed stack refs
- 🔴 `failed` — with failing stack refs
- 🔒 `blocked` — with gate reason
- ⏭️ `skipped` — commit already applied

### Already-applied guard

A fully-clean apply writes `runs/pr-<n>/applied/<sha>.json`. Re-running at the same commit:

- **apply** — skips, posts the ⏭️ timeline notice, exits success.
- **preview** — renders the plan with an "already applied" banner.
- `--force` — bypasses the guard on both.

### `retention.max_age`

Run artifacts under `runs/` (manifests, applied-state pointers) are pruned at run start.

- Go duration string; default `720h` (1 month).
- `0` or negative disables pruning.
- Age-based only — merged-PR cleanup needs VCS wiring reeve does not have, so artifacts age out.

### Approval rule merging

- `approvals.default` is the baseline.
- `approvals.stacks.<pattern>` entries merge with the default for matching
  stacks.
- Scalar fields (`required_approvals`, `require_all_groups`, `codeowners`,
  `dismiss_on_new_commit`, `freshness`) on a pattern **override** the
  default.
- `approvers` lists **union** (deduplicated).
- Patterns with more literal characters win specificity ties, and the
  more-specific pattern's scalar fields override the broader one's.
- `require_all_groups: true` changes semantics: every listed approver
  group must contribute one approval, regardless of `required_approvals`.

**Secure defaults.** reeve fails closed on approvals:

- A stack with **no matching approval policy** still requires **one**
  non-author approval — it does not auto-pass.
- `required_approvals: N` with **no `approvers` list** counts any `N`
  distinct non-author approvals (GitHub's "require N approvals" behavior),
  rather than being unsatisfiable — **on private repos**. On a **public**
  repo this path is blocked (see below), because anyone can review.
- **Public repositories.** On a public repo any GitHub user can submit an
  approving review, so a bare `required_approvals` with no `approvers` list
  and no CODEOWNERS is not a real gate. reeve fails such a stack closed with
  a message telling you to add an `approvers` list or CODEOWNERS — or to set
  `approvals.allow_unlisted_approvals_on_public: true` if you genuinely want
  to count unlisted reviews. The default (`false`) does not remove the
  ability, only forces you to name the risk. Private repos are unaffected,
  and a public repo that already uses an `approvers` list or CODEOWNERS never
  hits this.
- `dismiss_on_new_commit` defaults to **`true`**: pushing a new commit
  invalidates prior approvals. Set it to `false` explicitly to opt out.
- Only a reviewer's **most recent** review counts. A reviewer who approves
  and later requests changes (or whose approval is dismissed) no longer
  counts toward the gate.
- `freshness: <duration>` (opt-in, e.g. `24h`): an approval older than the
  window at evaluation time does not count and must be re-given. Stale
  approvals are called out in the rule trace and the missing list, so a
  blocked apply says exactly whose approval expired. `0`/unset means no
  freshness constraint. An approval without a submission timestamp fails
  closed when freshness is set.

### Approval sources

`approvals.sources` selects which signals count as approvals. Sources are
gathered independently and **unioned** — a human who approves via *both* a
review and a comment counts **once**.

| Source | Default | Signal |
| --- | --- | --- |
| `pr_review` | **on** | A GitHub PR review whose current state is `APPROVED`. |
| `pr_comment` | off (opt-in) | An authorized non-author posting `/reeve approve` in a PR comment. |

- **Omitting the `sources` block** leaves `pr_review` as the only active
  source — identical to reeve's original behavior. No existing config changes.
- `pr_review` stays on unless you list it explicitly with `enabled: false`.
- `pr_comment` is off unless you list it with `enabled: true`.
- **`enabled` is required on every listed source.** If you list a source you
  must set `enabled: true` or `enabled: false` — an omitted `enabled` is a
  load/lint error, not a silent "off". (Listing `pr_review` with no `enabled`
  used to disable reviews, the opposite of the obvious intent.)

```yaml
approvals:
  sources:
    - type: pr_review
      enabled: true
    - type: pr_comment
      enabled: true
      command: "/reeve approve"   # trigger phrase; default "/reeve approve"
```

**`pr_comment` authorization (fail-closed).** A `/reeve approve` comment counts
only when every condition holds:

- Its first line is `<prefix> approve`, where `<prefix>` exactly matches a
  configured command prefix (the action's `command-prefix`, default `/reeve`
  and `@reeve`) — parsed the same way as every other `/reeve` command.
- The commenter's `author_association` is in the same allowlist that gates
  command dispatch (the action's `allowed-associations`, default `OWNER`,
  `MEMBER`, `COLLABORATOR`). reeve **re-checks this at apply time** because it
  reads historical comments directly, not the dispatched event, so an
  unauthorized commenter's `/reeve approve` never counts.
- The commenter is not a bot and is **not the PR author** (the same non-author
  rule reviews follow — an author never self-approves).

**Commit binding under `dismiss_on_new_commit` (default on).** A PR review
carries an authoritative commit id from GitHub, but a comment does not — and the
SHA that was HEAD when a comment was posted *cannot* be reconstructed after the
fact, because git committer timestamps are settable by whoever pushes (a commit
can be backdated to appear older than an approval). So a comment approval is
bound to a commit **only when the commenter names it**:

- `/reeve approve <sha>` — pins the approval to `<sha>` (a 7+ character prefix of
  the commit). If `<sha>` is the current HEAD the approval counts; once a new
  commit lands it no longer matches HEAD and is dismissed, exactly like a stale
  review. Re-approve the new HEAD to satisfy the gate again.
- Bare `/reeve approve` (no SHA) — is **unpinned**. When `dismiss_on_new_commit`
  is on (the default) an unpinned comment approval is **dismissed** (the rule
  trace explains why and suggests re-approving with the SHA). When
  `dismiss_on_new_commit` is `false`, a bare `/reeve approve` counts.

reeve posts the current HEAD short-SHA in its PR comments, so approvers can copy
`/reeve approve <sha>` directly.

**Opting out — `allow_unpinned_comment_approvals`.** If your team trusts its
allowed approvers and prefers the convenience of a bare `/reeve approve`, set
`allow_unpinned_comment_approvals: true`. Unpinned comment approvals then count
even under `dismiss_on_new_commit` (approve-and-stick: the approval survives new
commits). It defaults to `false` (the secure behavior above), rides on any
approval rule so it can be scoped per pattern (e.g. loosen it on `dev/*` while
leaving `prod/*` strict), and has no effect on `pr_review` approvals — those are
always pinned to GitHub's authoritative commit id, and a pinned-but-stale
approval is still dismissed.

```yaml
approvals:
  default:
    allow_unpinned_comment_approvals: false   # secure default
  stacks:
    "dev/*":
      allow_unpinned_comment_approvals: true  # bare /reeve approve is fine on dev
```

> Posting `/reeve approve` also refreshes the approved-state notification
> (Slack "ready to apply"), mirroring the `pull_request_review` path. The
> comment itself is the approval — the apply gate re-reads it (and re-checks
> authorization) at apply time; the comment never triggers an apply.

### CODEOWNERS resolution

When `codeowners: true`, reeve parses the repo's `CODEOWNERS` file and
requires at least one approval from an owner of each changed file.

Owner resolution unions **all** matching rules for a file. For example:

```
* @org/platform
Pulumi.*.yaml @org/engineering
```

A `Pulumi.*.yaml` file matches both rules, so owners =
`[@org/platform, @org/engineering]`. Either team's member satisfies the
gate for that file.

Team slugs in CODEOWNERS are expanded the same way as `approvers` entries:
reeve resolves `org/team` → member logins via the VCS API before evaluation.

**Email owners are unenforceable.** GitHub allows email addresses as
CODEOWNERS entries (e.g. `docs@example.com`), but reeve has no
commit-email → login resolution, so email owners are excluded from the
gate: a path owned by both an email and a login/team still requires the
login/team's approval, and a path owned *only* by emails adds no
requirement (the evaluation trace notes the skipped entries instead of
wedging the gate forever). `reeve lint` warns about email owners in
CODEOWNERS.

Inspect the merged result:

```bash
reeve rules explain prod/payments
```

### Break-glass (`break_glass`)

Opt-in emergency apply: `/reeve breakglass "<justification>" apply`
overrides the approvals gate (and freeze windows unless
`override_freeze: false`) with a mandatory justification and a loud,
write-once audit record. Locks, checks, up-to-date base, preview
freshness, and policy hooks are **never** bypassed. Absent the block, the
command fails closed with a polite error.

`authorized:` is a union of sources — `internal_list` (logins and
`org/team` slugs), `codeowners`, `anyone`; `vcs_bypass` and
`groups:` (`group:<provider>:<name>`) are parsed but rejected as
not-yet-supported/phase-2. Authorization is resolved against the PR HEAD
(self-add is by design; the audit flags same-PR modification of
`.reeve/*.yaml` or CODEOWNERS).

`reject_self_authorization: true` (default `false`) locks that down: a PR
that modifies its own authorizing files (`.reeve/*.yaml`/`.yml` or a
CODEOWNERS file) cannot authorize a break-glass apply, no matter which
source would grant. The default keeps the flag-and-audit behavior — useful
when a late-night responder legitimately needs to add themselves; set this
true when you would rather fail closed than allow same-PR self-authorization.

Full reference: [break-glass.md](break-glass.md).

---

## `engine` (e.g. `pulumi.yaml`)

```yaml
version: 1
config_type: engine

engine:
  type: pulumi                     # pulumi | terraform | tofu

  binary:
    path: pulumi
    version: "3.150.0"             # optional pin

  # State backend - reeve configures the engine before each run using
  # short-lived creds. reeve does NOT manage state itself.
  state:
    backend: s3
    url: s3://mycompany-pulumi-state
    auth_provider: aws-state       # refers to auth.yaml provider
    secrets_provider:
      type: awskms
      key: arn:aws:kms:us-east-1:111:key/abc-123-def

  # Stack declarations. Runtime behavior is always explicit - either a
  # literal or a declared pattern must match.
  stacks:
    - project: api                 # literal
      path: projects/api
      stacks: [dev, staging, prod]

    - pattern: "projects/*"        # doublestar glob (no regex escape hatch in v1)
      stacks: [dev, staging, prod]

  filters:
    exclude:
      - "projects/sandbox/**"      # path glob
      - stack: "*/scratch"         # or stack-ref glob

  change_mapping:
    scope: auto                    # auto (default) | pulumi_only
    ignore_changes:
      - "**/docs/**"
    extra_triggers:
      - project: api
        paths: ["shared/types/**", "protos/**"]

  execution:
    max_parallel_stacks: 4
    preview_timeout: 10m
    apply_timeout: 30m

  policy_hooks:                    # see docs/policy-hooks.md
    - name: opa-compliance
      command: ["conftest", "test", "--policy", "policies/", "{{plan_json}}"]
      on_fail: block               # block | warn
      required: true
```

`engine.type` selects a registered engine adapter — the binary compiles in a
default set (`pulumi`, `terraform`, `tofu`), and `reeve lint` fails when the
type doesn't resolve to a compiled-in engine.

### Terraform / OpenTofu

`engine.type: terraform` drives the `terraform` CLI; `engine.type: tofu`
drives OpenTofu — one adapter, two registrations, so everything below
applies to both (`engine.binary.path` overrides the binary for either).

```yaml
version: 1
config_type: engine

engine:
  type: terraform                  # or tofu
  binary:
    path: terraform                # or tofu, or an absolute path

  # A root-module DIRECTORY is a project; a WORKSPACE is a stack.
  stacks:
    - project: network             # literal root module
      path: envs/network
      stacks: [dev, prod]          # workspaces

    - pattern: "envs/*"            # doublestar glob over root-module paths
      stacks: [default]            # dir-per-env layouts: default workspace
```

**Stack model.** A directory containing root-module `.tf` files (a
`terraform {}` block or provider config) is a project; each `terraform
workspace` in it is a stack. Layouts that use one directory per
environment enumerate as `<project>/default` — declare
`stacks: [default]` for them.

**Declared stacks are authoritative.** When `stacks:` entries match a
root module, reeve uses the declared workspace names without running
`terraform workspace list` (no init required just to enumerate). A
declared-but-missing workspace is created on first use; an undeclared
workspace is never created. Without declarations, `reeve stacks
discover` lists workspaces via the CLI when the module is initialized
and falls back to `default` (with a log line) when it isn't.

**Lifecycle.** Per stack reeve runs `init -input=false` →
`workspace select` → `plan -detailed-exitcode -out=<planfile>` →
`show -json <planfile>`. Apply consumes that exact saved plan file
(plan-what-you-apply parity). Drift checks use `plan -refresh-only`,
which inspects live infrastructure without writing state. Sensitive
values (`before_sensitive`/`after_sensitive` in the plan JSON) are
masked in every rendered diff and in the stored plan JSON.

reeve never touches engine state: backends, state encryption, and
credentials stay yours — configure the backend in your `.tf` files and
provide credentials via `auth.yaml` env bindings, exactly as you would
for the CLI.

### Discovery pipeline

1. **Declare** - literal `{project, path, stacks}` entries and `pattern:`
   globs from this file.
2. **Include** - engine enumerates on disk (pulumi: `Pulumi.yaml` +
   `Pulumi.<stack>.yaml` files; terraform/tofu: root-module dirs +
   workspaces).
3. **Exclude** - `filters.exclude` drops entries.
4. **Resolve** - engine validates each remaining stack.
5. **Map to changes** - drop skippable files, match the rest to stacks by path
   / `extra_triggers`; unmapped files broaden to all stacks (`scope: auto`).

**Shared directories.** Many stacks can live in one directory, each with its own `Pulumi.<name>.yaml`. Change-mapping is per-file:

- `Pulumi.<name>.yaml` change — affects only stack `<name>`.
- Sibling `Pulumi.<other>.yaml` — ignored.
- Shared `Pulumi.yaml`, program code, nested files — affect every stack in the directory.

**Docs/asset-only changes.** Built-in skip globs cover non-load-bearing files: `*.md`, `*.markdown`, `*.adoc`, `*.asciidoc`, `*.rst`, `*.txt`, `LICENSE`, images (`*.png/jpg/jpeg/gif/svg/webp`). Merged with `ignore_changes`.

- All changed files skippable — run nothing, post "Documentation/asset-only changes".
- `docs/` directories are not skipped; they can hold config or program-read data.

**`change_mapping.scope`.** Controls behavior when a changed file maps to no specific stack (shared lib, provider code, root `go.mod`).

| Value | Behavior |
| --- | --- |
| `auto` (default) | Preview/apply all stacks; post a header naming the unmapped files. |
| `pulumi_only` | Act only on files inside a stack directory; never broaden. |

Inspect it:

```bash
reeve stacks             # prints declared-and-resolved stacks
```

---

## `auth.yaml`

See [auth.md](auth.md) for the full provider catalog. Minimal shape:

```yaml
version: 1
config_type: auth

providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111111111111:role/reeve-prod
    region: us-east-1
    duration: 1h

  aws-prod-readonly:                 # used only for drift
    type: aws_oidc
    role_arn: arn:aws:iam::111111111111:role/reeve-drift-readonly

bindings:
  - match: { stack: "prod/*" }
    providers: [aws-prod]

  - match: { stack: "prod/*", mode: drift }
    providers: [aws-prod-readonly]   # replaces aws-prod for drift runs
```

---

## `notifications.yaml`

Notification destinations ("channels") are declared generically: `type`
chooses the adapter, `on:` chooses the subscribed events. One channel
implementation serves both PR-flow events (`plan` … `blocked`) and drift
events (`drift_detected` …) — see [notifications.md](notifications.md)
for the full channel catalog and event list.

```yaml
version: 2
config_type: notifications

channels:
  - type: slack
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
    trigger: plan
    on: [plan, ready, approved, applying, applied, failed, blocked]

  - type: webhook
    name: audit-feed
    url: https://example.internal/hooks/reeve
    on: [applied, failed, drift_detected]

  # Deployment timeline (append-only activity heartbeat, default off):
  - type: timeline_slack
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
  - type: timeline_github
```

The `timeline_*` channels complement the dashboard surfaces above: the status
comment/message is the edited-in-place snapshot; the timeline is one entry
per event (SHA, timestamp, per-run CI link) — thread replies in Slack, one
comment per commit SHA on GitHub. See
[notifications.md](notifications.md#the-deployment-timeline).

### Converting from the original config

The original single `slack:` block (and drift.yaml's `sinks:` key) no
longer load — reeve errors with a conversion pointer. Run
`reeve migrate-config` to rewrite them to the `channels:` shape
(originals backed up as `*.bak`; `--dry-run` previews), or hand-edit —
see [notifications.md](notifications.md#converting-from-the-original-config).

```yaml
version: 1
config_type: notifications

slack:
  enabled: true
  channel: "#infra-deploys"
  auth_token: ${env:SLACK_BOT_TOKEN}

  # trigger controls when the initial Slack message is created.
  # Subsequent events always update the existing message in place.
  #
  #   apply  (default) - message created only when /reeve apply is invoked
  #   plan             - message created when a plan finishes (status: pending approval)
  #   ready            - message created only when /reeve ready is run
  trigger: plan

  # events lists which lifecycle events emit a Slack notification.
  # When omitted, all events at or after the trigger fire (default behavior).
  # Valid values: plan, ready, approved, applying, applied, failed, blocked
  #
  # Example: only notify on plan and final result, skip the intermediate steps:
  #   events: [plan, applied, failed, blocked]
  #
  # events: [plan, ready, approved, applying, applied, failed, blocked]

  # icons overrides the default emoji used in the message layout.
  # All fields are optional. Useful when your Slack workspace has custom emoji
  # (e.g. :pulumi:, :github:) that aren't available by default.
  icons:
    engine: ":building_construction:"   # repo/project header icon
    runner: ":runner:"                  # CI runner / view-run button
    author: ":bust_in_silhouette:"      # PR author field
    approver: ":approved_stamp:"        # required approvers field

  rules:
    - environments: [prod, staging]  # only notify these envs
    - stacks: ["prod/payments", "prod/auth"]
```

### Message lifecycle

reeve sends one message per PR and edits it in place as the run progresses.
The sidebar color and status field update at each stage:

| Stage | Trigger | Color |
| --- | --- | --- |
| Plan ready | `trigger: plan` - plan finishes | 🟡 yellow |
| Ready | `/reeve ready`, or draft→ready with a successful plan | 🟡 yellow |
| Approved | Preconditions passed, apply imminent | 🔵 blue |
| Applying | Apply loop started | 🟣 purple |
| Applied | Apply completes successfully | 🟢 green |
| Failed | Apply errors | 🔴 red |
| Blocked | Preconditions not met | 🟡 yellow |

**Error rule:** if no message exists yet and apply fails, no message is created.
Errors only update an existing message.

> The Approved update can also fire the moment a PR review is approved
> (`reeve run approved`), but only if the GitHub Action is configured with
> `run-on-approval: "true"` and the workflow subscribes to
> `pull_request_review` events. By default that dispatch is skipped - the
> apply gate re-checks approvals anyway - so Slack flips to approved at
> apply time instead.

**`/reeve apply` hint** only appears when status is `approved`. Pending-approval
states show "Waiting for approval." instead.

### Thread timeline

The first message opens a Slack thread. Each event appends a timestamped
timeline entry (planned, ready, approved, applying, applied, failed).
When a `timeline_slack` channel is enabled it takes over the thread with
richer entries (per-stack outcomes, per-run CI links) and these courtesy
entries are suppressed.

No plan output is sent to Slack. Full output is in the GitHub Actions run log.

Token expansion: `${env:NAME}` pulls from the process environment.

---

## `observability.yaml`

```yaml
version: 1
config_type: observability

otel:
  enabled: true
  endpoint: ${env:OTEL_EXPORTER_OTLP_ENDPOINT}
  service_name: reeve
  resource_attributes:
    team: platform
    repo: ${env:GITHUB_REPOSITORY}
  stack_cardinality: hash            # allow | hash (default) | drop
  headers:
    Authorization: ${env:OTEL_AUTH_HEADER}

annotations:
  - type: grafana
    url: https://grafana.mycompany.internal
    api_key: ${env:GRAFANA_API_KEY}
    events: [apply_started, apply_completed, apply_failed]

  - type: datadog
    url: https://api.datadoghq.com
    api_key: ${env:DATADOG_API_KEY}
    events: [apply_completed, apply_failed, drift_detected]

  - type: webhook
    url: https://hooks.mycompany.internal/reeve
    events: [apply_started, apply_completed]
```

- Fully opt-in. Without `observability.yaml`, reeve emits no telemetry.
- `stack_cardinality: hash` emits a stable 64-bit fingerprint of
  `{project}/{stack}` as the OTEL label - prevents cardinality blow-up on
  big monorepos. Use `allow` for small deployments, `drop` to omit the
  stack label entirely.

---

## `drift.yaml`

See [drift.md](drift.md). Minimal:

```yaml
version: 1
config_type: drift

scope:
  include_patterns: ["prod/*", "staging/*"]
  exclude_patterns: ["*/scratch"]

behavior:
  refresh_before_check: true
  max_parallel_stacks: 8
  state_bootstrap:
    mode: require_manual           # baseline | alert_all | require_manual
    baseline_max_age: 7d           # reserved — parsed but not yet enforced

schedules:
  critical:
    patterns: ["prod/payments", "prod/auth"]
  prod:
    patterns: ["prod/*"]
    exclude_patterns: ["prod/payments", "prod/auth"]

channels:
  - type: slack
    channel: "#infra-drift"
    on: [drift_detected, check_failed]

  - type: pagerduty
    integration_key: ${env:PD_CHANGE_EVENTS_KEY}
    on: [drift_detected]
    severity_map:
      prod: error
      staging: warning
```

---

## `user.yaml` (local only)

Location: `~/.config/reeve/user.yaml`. Never committed to a repo.

Reserved for local-only preferences that don't belong in team config.
v1 scope is minimal - most local overrides happen via CLI flags or env
vars. The schema exists as a forward-compatible slot.

```yaml
version: 1
config_type: user
```

---

## Token expansion

`${env:NAME}` expansion is restricted to an **enumerated allow-list of
credential-bearing fields**. Config is loaded from the PR HEAD, which is
untrusted before approval — expanding env references everywhere would turn
any config field into an env-var oracle. The designated fields are, exactly:

- `shared.yaml`: `bucket.name`, `bucket.region`, `bucket.prefix`,
  `locking.admin_override.allowed`
- `auth.yaml` providers: `tenant_id`, `client_id`, `subscription_id`,
  `private_key`, `app_id`, `installation_id`
- `notifications.yaml` / `drift.yaml` channels: `auth_token` (slack),
  `integration_key` (pagerduty), `url` and `headers` values (webhook)
- `observability.yaml`: `otel.endpoint`, `otel.headers`,
  `otel.resource_attributes`, and `annotations[*]` `url`, `endpoint`,
  `api_key`, `headers`

Designated fields support both exact references (`${env:TOKEN}`) and
embedded ones (`Bearer ${env:TOKEN}`, `https://host/${env:TOKEN}`).
`${env:X}` expands at load time via `os.Getenv("X")`. Missing env vars
expand to empty strings (not an error) - so token references safely
degrade when a feature is optional.

Everywhere else `${env:...}` is kept as **literal text** and `reeve lint`
(and the loader log) warns "env expansion is not supported for this
field", so typos and unsupported placements surface instead of failing
silently. A new config field gets no expansion unless it is deliberately
added to the allow-list (`expand:"env"` struct tag in
`internal/config/schemas`).

Note that even for designated fields, pre-approval previews fail closed
when the PR modifies the config that carries them: channel dispatch is
suppressed when notification config changed, and OTEL exporter init is
skipped when `observability.yaml` changed — see
[notifications.md](notifications.md#pre-approval-channel-isolation).

## Preview freshness

`preconditions.preview_freshness` bounds how old a plan may be at apply time.
It exists for two failure modes, and both are about the world moving while a
plan sits waiting for a human.

**Stale plans on busy repos.** A plan records what *your* PR intended against
the state it saw. It does not record that the state still looks that way. On a
repo where several PRs land in a day, another PR can merge and change a
resource your plan touches — and what happens next depends on
[plan locking](#plan-locking):

- **Plan locking on** (the default): apply executes the plan artifact the
  preview stored. If state moved since that plan was made, the engine
  *refuses* it — Terraform says "saved plan is stale", Pulumi says the update
  exceeds its plan. Safe, but the failure arrives late: after the run started,
  after the stack lock was taken, and on a queue where the next PR is waiting
  behind it. Freshness turns that into an upfront block.
- **Plan locking off**: apply computes a new change set at apply time. Nothing
  errors. The apply simply executes something other than what the reviewer
  read, and the wider the gap between preview and apply, the more it can
  differ.

Plan locking protects you from *reeve* applying something other than what it
planned. Neither setting protects you from *reality* having changed — that is
what freshness is for.

This is also the axis `require_up_to_date` and `preview_max_commits_behind`
cannot cover. Those compare your branch to its base — code drift. Freshness
bounds *state* drift, which includes changes with no PR behind them at all: a
console edit, an out-of-band apply, a drift-correction run. A branch can be
perfectly up to date and its plan still describe a world that is gone.

**Click-ops protection.** An approval plus an old plan is an apply anyone can
trigger later from a comment. A freshness window forces the plan to be
re-derived against current state before that is allowed, so an apply reflects
a recent decision rather than a stale one someone stumbled back onto.

### Default

```yaml
preconditions:
  preview_freshness: "4h"          # the default when the key is omitted
```

Omitting the key gives you **4 hours**. That is roughly "planned this working
session": long enough that a normal review cycle does not force a re-plan,
short enough that a plan cannot survive a day of other merges. Set your own
window if your review cadence is faster or slower.

### Disabling it

```yaml
preconditions:
  preview_freshness: "0"           # deliberately disabled
```

Only a literal `"0"` disables the gate; omitting the key no longer does.
Disabled, a plan of any age may be applied, and reeve records that on the gate
trace as *"preview_freshness disabled - a plan of any age may be applied;
concurrent merges are not accounted for"*, so it stays visible in the PR
comment rather than looking like the check passed.

Disabling is a reasonable choice for a low-traffic repo, a single-owner
environment, or where an external process already serialises changes. It is a
poor choice on a shared repo with concurrent merges — that is precisely the
case the gate is for.

A value that is not a Go duration — or one that is not positive — is a load
error rather than a silent disable. Previously `preview_freshness: 2hrs`
parsed as nothing, left the window at zero, and turned the gate off without
saying so.

Note that disabling freshness does not disable the `preview_succeeded` gate: a
stack with no plan at all for the current commit is still blocked.

## Plan locking

```yaml
# .reeve/<engine>.yaml
engine:
  type: terraform
  plan_locking: true          # default; omit to get this
```

Plan locking binds an apply to the plan its preview produced. With it on,
reeve stores the engine's plan artifact next to the run manifest at preview
time and hands that exact file back to the engine at apply time:

| Engine | Preview | Apply |
|---|---|---|
| Terraform / OpenTofu | `plan -out=<file>` | `apply <file>` |
| Pulumi | `preview --save-plan=<file>` | `up --plan=<file>` |

With it off, both engines compute a fresh change set inside the apply call.
That is not a small difference:

```
plan_locking: false                  plan_locking: true

  preview  →  plan A  (reviewed)       preview  →  plan A  (reviewed, stored)
     ⋮         someone merges             ⋮         someone merges
  apply    →  plan B  → SHIPS          apply    →  plan A  → engine REFUSES
                                                   (state moved under it)
```

Off, "last apply wins": what ships is whatever the world looks like at apply
time, which is not necessarily what anyone approved. On, an apply the world
moved out from under fails loudly instead.

Reeve still re-derives nothing about *which* stacks apply — that is bound to
the preview manifest independently of this setting.

### When it degrades

Locking is best-effort in one direction only: it never applies something
unreviewed, but it can fall back to a re-plan. That happens when the preview
stored no artifact (locking was off then, the upload failed, the manifest
predates this feature) or the stored plan cannot be read back. The apply still
runs, and the run's timeline says **"plan lock unavailable"** with the reason —
"this apply re-planned" is never something you should have to infer.

`/reeve apply --refresh` also turns locking off for that run, by construction:
a refresh changes the diff, which is the one thing a locked plan pins.

### Reasons to turn it off

- **Pulumi's update-plan flags are still experimental.** `--save-plan` and
  `--plan` are gated behind `PULUMI_EXPERIMENTAL`, which reeve sets on exactly
  the two invocations that pass them. If your Pulumi version does not support
  them, the apply fails rather than silently applying something else — set
  `plan_locking: false`.
- **The stored plan is sensitive.** A plan artifact is the engine's own
  serialized change set: it contains resource attribute values, including
  ones your state backend treats as secret. Pulumi's own docs say as much
  about `--save-plan`. Unlike the plan *summary* in the run manifest, it
  cannot be redacted — redacting it would make it unusable as a plan.

  It lands in your own bucket, under `runs/pr-<n>/<run-id>/plans/`, and is
  pruned by the same `retention.max_age` sweep as the rest of the run's
  artifacts. If that bucket is not already treated as sensitive — object
  encryption, access limited to the people who can already read state — treat
  this as the reason to fix that, or turn plan locking off.

## Lint

```bash
reeve lint
```

Catches:

- Unknown top-level keys
- Unsupported `version` values
- Duplicate `config_type` (except `engine`, where the duplicate check is
  per `engine.type`)
- Missing required fields (`bucket.type`, an engine config)
- More than one engine config (reeve currently supports one engine per
  repo)
- Auth provider scope conflicts (see [auth.md](auth.md))
- `env_passthrough` without `i_understand_this_is_dangerous: true`

## Migration

When a schema bumps version (e.g. `shared: 1 → 2`):

```bash
reeve migrate-config --dry-run   # preview
reeve migrate-config             # writes; keeps *.bak backups
```

Per-file version bumps - migrations don't have to be in lockstep across
config types.
