# Notifications

## Scope

One shared channel framework (`internal/notify`) carries every outbound
human/machine notification for both producers: the PR-flow run pipeline and
the drift runner. Notifications run **last** in the pipeline so upstream
failures are captured accurately.

## Channel framework

- `Channel` interface: `Name` / `Subscribes` / `Deliver`. A destination
  implemented once can subscribe to events from either producer; payloads
  carry a producer field.
- Concrete channels live in their own packages under
  `internal/notify/channels/*` and self-register a constructor in `init()`.
  The factory resolves a config entry purely by its `type:` string against
  the registry and never statically imports concrete channels (modularity
  contract). The default set is compiled in via blank imports
  (`internal/notify/all`); a build can import a subset.
- An unknown channel `type:` fails loudly, naming the unknown and registered
  types. A channel whose runtime dependency is absent (no Slack token, no
  issue client, no annotation emitters) is skipped, not fatal.
- VCS SDKs stay out of channels: `github_issue` and `timeline_github`
  consume narrow, consumer-defined interfaces implemented by the GitHub VCS
  adapter. `go-github` is imported only by `internal/vcs/github`.

## Event model

PR-flow events, emitted by the run pipeline:
`planning` (preview started), `plan` (preview finished), `ready`,
`approved`, `applying`, `applied`, `failed`, `blocked`, and `break_glass`
(emitted for an authorized break-glass apply *in place of* `approved` -
approvals were bypassed, not granted; payload carries PR, commit SHA, run
URL, target stacks).

Drift events, emitted by the drift runner:
`drift_detected`, `drift_ongoing`, `drift_resolved`, `check_failed`,
`check_recovered` (see `openspec/specs/drift`).

Unknown `on:` names fail load/lint with the valid list; an empty `on:` list
warns (except the Slack trigger-onward default). Event-model additions never
widen legacy default subscriptions: channels without an explicit `on:` keep
their exact prior behavior (no `planning`, no `break_glass`).

## Configuration

Channels are declared as a generic list (`type` + settings + `on:`), in
`notifications.yaml` (v2) or `drift.yaml`; both feed the same dispatch. The
v1 `notifications.yaml` shape (single `slack:` block) keeps loading and maps
onto the channel model (`slack.events` → `on:`; trigger/icons/rules carry
over); `reeve migrate-config` rewrites v1 → v2 (with backup and
`--dry-run`), yielding the same effective channel list.

## Delivery guarantees

`Dispatch` delivers to channels concurrently (per-channel ordering
preserved) with a per-delivery timeout so one hung endpoint cannot starve
the rest. HTTP channels share a client with a sane timeout and retry
transient failures (network errors, 5xx, 429) with bounded exponential
backoff. Delivery errors are collected and logged, never fatal to the run.
Drift-event delivery is additionally durable/at-least-once via pending-event
markers (see `openspec/specs/drift`).

## Grouping

A channel may set `grouping:` to batch one drift run's alerts:

- `none` (default; unset behaves the same) - one message per drifted stack.
- `by_environment` - one message per environment, listing that
  environment's drifted stacks.

Grouping is a delivery-layer concern only: it never changes classification,
state, `exit_on`, or which events fire. It applies to the drift alert
lifecycle (`drift_detected`, `drift_ongoing`, `drift_resolved`);
`check_failed` is **never** grouped - each is a distinct per-stack incident.
Meaningful for `slack` and `webhook`; a no-op for channels where per-stack
tracking is the point (`github_issue` - an issue is a per-stack incident;
`otel_annotation` - one metric per stack). An unknown `grouping:` value is a
hard config error.

## Slack (PR dashboard channel)

- One message per PR, tracked by message ID in
  `notifications/pr-{n}/slack.json`.
- Main message: high-level status (planned → applying →
  applied/failed/closed-unmerged).
- Thread: timestamped timeline entries only (one per event).
- Block Kit layout; rule-gated (e.g. `environment: prod` only); always
  links back to the PR.
- `trigger` controls which event **creates** the initial message
  (`plan` | `ready` | `apply`); the default subscription remains every
  lifecycle event at or after the trigger.
- State safety: loading `slack.json` distinguishes not-found (fresh state)
  from failure - on failure the delivery errors instead of posting a
  duplicate message. Writes use the blob store's conditional-write
  primitive (`PutIfMatch`); on a create race the first writer's state wins
  and the conflict is surfaced.
- Text safety: externally-controlled text interpolated into mrkdwn (PR
  titles, authors, error messages) is escaped per Slack's rules
  (`&`, `<`, `>`); payloads in code fences are made fence-safe; truncation
  never splits a UTF-8 rune.

## Timeline channels

Two explicit, default-off channel types provide the deployment timeline;
with neither configured, behavior is byte-identical for existing users.
When `on:` is omitted they default to every PR-flow timeline event
(`planning` through `blocked`, plus `break_glass`). Every entry carries the
event, short commit SHA, timestamp, and the CI run URL of the run that
produced it (preview and apply link their own distinct runs); events with
stack results (`plan`, `applied`, `failed`, `blocked`) include a per-stack
outcome summary.

- `timeline_slack` - posts every entry as a thread reply under ONE PR-level
  anchor message. The anchor is the dashboard slack channel's per-PR status
  message when present (shared per-PR blob state, `PutIfMatch`); otherwise
  the timeline creates a minimal anchor the dashboard channel later edits
  into the status message. On a create race the first writer's anchor wins.
  Once the timeline claims the thread, the dashboard channel suppresses its
  own courtesy thread notes so events are not double-posted.
- `timeline_github` - one PR comment per commit SHA, identified by the
  marker namespace `<!-- reeve:timeline:v1:{shortsha} -->` and edited in
  place via the existing comment-upsert machinery (existing markers stay
  byte-identical). Entry history persists per PR in blob state with
  compare-and-swap appends so concurrent runs cannot lose each other's
  entries; each event re-renders the SHA's full comment from state.

Both timeline channels stay inside the modularity contract (narrow VCS
comment surface, no SDK imports) and skip - not fail - when their runtime
dependencies are absent (e.g. drift runs with no PR comment client).

## Break-glass surfacing

The apply PR comment for a break-glass run contains a distinct
marker-tagged section (`<!-- reeve:break-glass:v1 -->`) rendered as a
warning admonition: actor, matched authorization source, overridden gates,
the same-PR-authorizing-config-modified flag when set, and the
justification quoted verbatim. An audit record is written via the
write-once audit log with the same fields plus commit SHA, run URL, and
timestamps. A malformed `/reeve breakglass` command posts a usage-help
comment and runs nothing.

## Client sharing

The Slack API client (auth, message lifecycle, Block Kit primitives) lives
in `internal/slack` and is shared by the slack and timeline_slack channels;
PR-flow templates live here, drift-flow templates in the drift channel
packages.

## Future channels

Mattermost, Rocket.Chat, Teams. Each is a new self-registered adapter in
`internal/notify/channels/*`.
