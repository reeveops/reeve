# Configuration reference

Everything under `.reeve/` is strict YAML: unknown keys are errors, versions
are per-file, and schemas are stable within a major version.


## Find a setting

| Area | Reference |
| --- | --- |
| Bucket, comments, locks, approvals, apply | [shared.yaml](#sharedyaml) |
| Gate defaults | [Preconditions](#preconditions) |
| Pulumi, Terraform, OpenTofu | [Engine](#engine-eg-pulumiyaml) |
| Credentials and bindings | [auth.yaml](#authyaml) · [provider catalog](auth.md#provider-catalog) |
| Slack, webhooks, issues, timelines | [notifications.yaml](#notificationsyaml) · [channels](notifications.md#channel-types) |
| Telemetry and annotations | [observability.yaml](#observabilityyaml) |
| Scheduled checks | [drift.yaml](#driftyaml) · [drift behavior](drift.md) |
| Expansion and validation | [Token expansion](#token-expansion) · [Lint](#lint) |
| Compatibility | [Reserved fields](#reserved-fields) · [Migration](#migration) |

These are configuration examples, not a file to enable in full.
Start with [getting started](getting-started.md) and add only the features you need.

## File layout

```text
.reeve/
├── shared.yaml           # bucket, approvals, locking, preconditions, freeze, apply
├── auth.yaml             # credential providers and bindings
├── notifications.yaml    # notification channels (slack, webhook, pagerduty, ...)
├── observability.yaml    # OTEL + annotations
├── drift.yaml            # drift scope, schedules, channels
└── pulumi.yaml           # choose ONE engine file: pulumi, terraform, or tofu
```

Every file begins with:

```yaml
version: 1
config_type: <shared|engine|auth|notifications|observability|drift|user>
```

- `version` is per-file. Bumps affect only that schema.
- `config_type` is one-per-file. Engine files are keyed by `engine.type`,
  but reeve currently supports only one engine config per configured root - loading
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
    "*/prod":
      required_approvals: 2
      approvers: ["@org/sre", "@org/security"]
      require_all_groups: true     # one from each group, not N-of-any
    "payments/prod":
      approvers: ["@org/payments-leads"]

preconditions:
  # Both of these default to OFF when omitted, so that a first run works
  # before CI checks and branch protection are wired up. Setting them is
  # the recommended production posture; `reeve lint` warns while either is
  # unset. An explicit `false` is treated as an informed choice and is not
  # warned about. (`require_up_to_date` is intended for the apply-then-merge
  # flow - leave it off under `trigger: merge`, see below.)
  require_up_to_date: true
  require_checks_passing: true
  preview_freshness: 2h            # preview must be newer than this ("0" disables - see below)
  preview_max_commits_behind: 5

freeze_windows:
  - name: friday-afternoon
    cron: "0 15 * * 5"             # Fri 3pm
    duration: 65h                  # through Monday morning
    stacks: ["*/prod"]

break_glass:                       # opt-in emergency apply; OFF when absent
  authorized:                      # UNION: any matching source grants
    internal_list: ["alice", "myorg/sre"]
    codeowners: true               # owners of changed paths may break-glass
    anyone: false
  override_freeze: true            # default true
  reject_self_authorization: false # default false — see "Break-glass" below

apply:
  trigger: comment                 # comment (default) | merge — see "apply.trigger" below
  allow_fork_prs: false            # deny-by-default - review risk before flipping
```

### Preconditions

| Field | When omitted | Meaning |
| --- | --- | --- |
| `require_up_to_date` | `false` | Block an apply whose PR head is behind its base; leave off for merge-triggered apply. |
| `require_checks_passing` | `false` | Require the relevant GitHub checks to pass. |
| `preview_freshness` | `4h` | Maximum preview age; the literal `"0"` disables the age limit. |
| `preview_max_commits_behind` | `0` | When positive, permit this many commits behind as a warning in the up-to-date gate. |

`reeve init` writes stricter explicit values than the omitted-field defaults: both boolean checks on and a `2h` freshness window.
`reeve lint` warns about omitted boolean gates; see [PR workflow](pull-requests.md) for how gates interact.

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
| `comment` (default) | **Apply-then-merge.** Apply runs only from a `/reeve apply` (or `/reeve up`) PR comment, before the PR is merged. A merge event is a no-op. |
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

Controls how dashboard comments are keyed.

| Value | Behavior |
| --- | --- |
| `replace` (default) | One board per PR, under `<!-- reeve:pr-comment:v1 -->`. Every run edits it. |
| `section` | One board per commit, under `<!-- reeve:pr-comment:v1:<short-sha> -->`. Preview and apply of a commit share it; a new commit gets a new board and the old one is never rewritten, so each commit's plan stays on the PR. |
| `append` | A new comment every run; nothing is edited. |

`section` now splits by commit instead of by operation, so each commit keeps its
own board rather than two shared boards overwritten every run. The old
`<!-- reeve:apply:v1 -->` marker is retired, and comments already posted under it
are left in place.

> **Draft PRs:** apply is always blocked on draft PRs regardless of config.
> Convert to ready for review first. When a draft PR becomes ready, reeve runs `/reeve ready`
> automatically, notifying for approval if a plan has already succeeded.

### `comments.overflow`

What happens when a board exceeds GitHub's 65,536-char comment limit.

```yaml
comments:
  overflow:
    mode: continue      # drop (default) | continue
    split: divided      # divided (default) | stack | group
    max_parts: 10       # cap on comments per board
```

| `mode` | Behavior |
| --- | --- |
| `drop` (default) | One comment. Content is trimmed: engine output, then diffs, then summaries, then error text is shortened, then whole stack sections, then table rows. Whatever is dropped goes to the run log. |
| `continue` | The board continues into further comments. No stack detail is dropped. |

Under `continue` the stack table stays whole on the first comment - it is the
index of the run - and per-stack detail fills the parts after it. Each part says
which it is. A stack's detail is never split across two comments.

| `split` | Behavior |
| --- | --- |
| `divided` (default) | Fewest parts that fit, stacks spread evenly. Two parts of 20 rather than 39 and 1. |
| `stack` | Fill each part to capacity in render order. Fewer parts, ragged last one. |
| `group` | One part per status group: failures, blocked, applied, no-op. A group too big for one part splits again. |

`max_parts` bounds how many comments one board may occupy (default 10). Stacks
past the cap are named in the last part with a count and written to the run log.

A board that fits in one comment renders identically whether overflow is on or
off, so enabling it changes nothing until a board actually overflows.

Raw engine output is still dropped before paginating on a preview: it is the
`pulumi preview --json` blob, hundreds of KB per stack, and the diff carries what
a reviewer reads. On an apply or refresh it is the engine's own record of what
changed, so it paginates with everything else.

### Comment flags

Every `comments` setting has a CLI flag that overrides the config for one run:

| Flag | Overrides |
| --- | --- |
| `--comment-sort` | `comments.sort` |
| `--comment-stack-view` | `comments.stack_view` |
| `--comment-style` | `comments.style` |
| `--comment-show-gates` | `comments.show_gates` |
| `--comment-collapse-threshold` | `comments.collapse_threshold` |
| `--comment-overflow` | `comments.overflow.mode` |
| `--comment-overflow-split` | `comments.overflow.split` |
| `--comment-overflow-max-parts` | `comments.overflow.max_parts` |

A flag only takes effect when given, so leaving one off never overwrites your
config. An unrecognized value is rejected by name rather than falling back to
the default.

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

Run `reeve maintenance run` on a schedule to prune artifacts under `runs/`.

- Go duration string; default `720h` (1 month).
- `0` or negative disables pruning.
- Age-based only. Merged-PR cleanup needs VCS wiring reeve does not have, so artifacts age out.

### Approval rule merging

Pattern scalars override the default and approver lists union.
See [approval policies](pull-requests.md#approval-rule-merging) for specificity, public repositories, freshness, and group requirements.

### Approval sources

PR reviews are enabled by default; comment approvals are opt-in.
See [approval sources](pull-requests.md#approval-sources) for explicit-SHA comments and authorization.

### CODEOWNERS resolution

The last matching CODEOWNERS rule wins for each path; earlier matches do not contribute owners.
See [CODEOWNERS resolution](pull-requests.md#codeowners-resolution) for team expansion, ownerless rules, and email owners.

### Break-glass (`break_glass`)

Opt-in emergency apply: `/reeve breakglass "<justification>" apply`
overrides the approvals gate (and freeze windows unless
`override_freeze: false`) with a mandatory justification and a write-once audit record.
Locks, checks, up-to-date base, and policy hooks remain enforced; authorized recovery from unreadable preview history has the narrow preview-gate exception described in the dedicated guide. Absent the block, the
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
    max_parallel_stacks: 4           # default 1; same-directory stacks stay serial
    preview_timeout: 10m
    apply_timeout: 30m

  policy_hooks:                    # see docs/policy-hooks.md
    - name: opa-compliance
      command: ["conftest", "test", "--policy", "policies/", "{{plan_json}}"]
      on_fail: block               # block | warn
      required: true
```

For a Pulumi passphrase provider, set `type: passphrase`. Supply the value
through a configured literal or a state auth provider.

```yaml
state:
  backend: file
  url: file://./pulumi-state
  secrets_provider:
    type: passphrase
```

Reeve copies that variable into the isolated engine environment only when the
passphrase provider is selected. It never copies an ambient host passphrase or
expands `${env:...}` from this PR-controlled field.

Use a secret-manager provider for `engine.state.auth_provider` to emit
`PULUMI_CONFIG_PASSPHRASE`. The flagged `env_passthrough` provider also works
when its required acknowledgement is present.

Do not commit a real passphrase. Use the state auth provider for non-fixture
values.

`max_parallel_stacks` bounds concurrent preview processes. Reeve preserves
result order and serializes stacks that share one project directory.

`reeve run preview --max-parallel-stacks N` overrides the config for one run.

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
  - match: { stack: "*/prod" }
    providers: [aws-prod]

  - match: { stack: "*/prod", mode: drift }
    override: [aws-prod-readonly]    # replaces aws-prod for drift runs
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

### Migration and lifecycle

The old single `slack:` block is rejected; use `reeve migrate-config` to convert it with a backup.
[Notifications](notifications.md) owns the channel catalog, message lifecycle, and delivery guarantees.

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
  `{project}/{stack}` as the OTEL label, hiding the raw name without reducing
  the number of distinct stack labels. Use `allow` to expose names or `drop`
  to omit the stack label; project and environment labels still contribute cardinality.

---

## `drift.yaml`

See [drift.md](drift.md). Minimal:

```yaml
version: 1
config_type: drift

scope:
  include_patterns: ["*/prod", "*/staging"]
  exclude_patterns: ["*/scratch"]

behavior:
  refresh_before_check: true
  max_parallel_stacks: 8
  state_bootstrap:
    mode: require_manual           # baseline | alert_all | require_manual

schedules:
  critical:
    patterns: ["payments/prod", "auth/prod"]
  prod:
    patterns: ["*/prod"]
    exclude_patterns: ["payments/prod", "auth/prod"]

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

`preconditions.preview_freshness` defaults to `4h`; the literal string `"0"` disables the age gate.
See [preview freshness](pull-requests.md#preview-freshness) for its relationship to branch state and saved plans.

## Plan locking

`engine.plan_locking` defaults to `true` and applies a saved preview plan when one is available.
Missing or unreadable plan artifacts fall back to a fresh plan, and `--refresh` disables locking for that run; see [saved plans](pull-requests.md#plan-locking).

## Reserved fields

These fields parse for compatibility but do not enable the suggested behavior:

- `locking.reaper_interval`: use an external [maintenance schedule](operations.md#scheduled-maintenance).
- `apply.auto_ready`: does not dispatch readiness automatically; the workflow's `ready_for_review` event already invokes ready.
- `drift.behavior.state_bootstrap.baseline_max_age`: not enforced.
- `drift.freshness.respect_failures`: not a separate toggle; failed checks are retried.
- `drift.classification.treat_as_drift.missing_state`: requires unmanaged-resource inventory the current engines do not provide.
- `break_glass.authorized.vcs_bypass` and `groups`: rejected if configured; see [break-glass](break-glass.md).
- Auth provider `source`: does not wire a parent provider into secret-manager retrieval; see [secret managers](auth.md#secret-managers).

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
  configured root)
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
