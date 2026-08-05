# Notification channels

## Why

reeve had two disjoint notification paths. Drift detection had a clean channel
abstraction (`internal/drift/channels`: interface + five adapters + factory),
while PR-flow notifications (Slack message lifecycle) were hand-rolled in
`internal/notifications` and wired point-to-point from the run pipeline. The
modularity contract (openspec/specs/architecture) names this as tracked debt,
along with two concrete violations in the drift path: the `github_issue` channel
and the channels factory imported `go-github` directly, and the factory
statically imported every channel.

The split also carried real defects: channels delivered serially with no
timeout or retry (`http.DefaultClient`), and the Slack PR path could post
duplicate messages when its blob-state load failed, clobber state without
compare-and-swap, and emit malformed/injectable mrkdwn.

## What

- Lift the channel framework into a shared `internal/notify` package: `Channel`
  interface, unified event model covering both producers (PR flow + drift),
  `Dispatch`, and a self-registration registry resolved purely by the config
  `type:` string. Concrete channels live in `internal/notify/channels/*` and
  register in `init()`; `internal/notify/all` blank-imports the default set
  so builds can slice.
- Migrate the five drift channels (slack, webhook, pagerduty, github_issue,
  otel_annotation) onto the framework with identical operator-visible
  behavior, and make the PR flow a producer: the Slack PR message lifecycle
  becomes the slack channel's PR-event handling.
- Fix the absorbed findings: route GitHub issue access through a narrow
  consumer-defined interface implemented by `internal/vcs/github` (no
  `go-github` outside the adapter); propagate Slack state-load errors
  (duplicate-post fix); conditional state writes via the blob store's
  `PutIfMatch`; mrkdwn escaping for externally-controlled text; fence-safe
  embedding; UTF-8-safe truncation; shared HTTP client with timeout;
  bounded retry with backoff for 5xx/network errors; concurrent dispatch
  with per-delivery timeouts.
- Open the v0.3.0 config line: `notifications.yaml` v2 declares channels
  generically (`type` + settings + `on:` subscriptions). v1 (`slack:` block)
  keeps working and maps onto the channel model internally; `reeve
  migrate-config` rewrites v1 → v2. Unknown `on:` event names fail
  load/lint; empty subscriptions warn.

## Scope

**In:** `internal/notify` (+ channels), drift/PR producers, `internal/slack`
mrkdwn hardening, `internal/vcs/github` issue surface, notifications config
v2 + migration, docs.

**Out (tracked elsewhere):** PR *comments* as a channel (comment rendering in
`shared.yaml: comments.*` is unchanged), auth/blob factory self-registration
(`split-builds`), named webhook payload presets.

## Behavior compatibility notes

- Drift channel output is byte-compatible (webhook JSON keys, PagerDuty event
  shape, issue bodies, Slack message text modulo new escaping of
  already-broken characters).
- The legacy Slack default subscription (all events at or after `trigger`)
  is preserved. One deliberate refinement: the terminal apply event is now
  the actual outcome (`applied` | `failed` | `blocked`) filtered by
  subscription, where the legacy code sent whichever outcome occurred as
  long as *any* of the three was enabled. Default configs (no explicit
  `events:`) are unaffected.
