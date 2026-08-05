# Deployment timeline

## Why

The status surfaces reeve maintains today are snapshots: the PR comment and
the Slack per-PR message are edited in place to show the CURRENT state. That
is the right quick-grok dashboard, but it silently discards history — GitHub
renders comment edits without any visible activity, so "did the preview even
run for this push?" has no answer short of opening the Actions tab. Slack is
slightly better (the channel posts terse thread notes) but entries are minimal
and only exist when the dashboard message exists.

A deployment (= the PR) needs an append-only activity heartbeat alongside
the snapshot: every lifecycle event, stamped with the commit SHA it ran for,
when it happened, and a link to the CI run that produced it (preview and
apply are different Actions runs).

## What

- Timeline channel pair on the B2 channel framework (`internal/notify/channels/timeline`),
  registered as two channel types so each can be enabled independently and each
  skips on its own unmet dependencies (registry convention):
  - `timeline_slack` — every subscribed event becomes a thread reply under
    ONE PR-level anchor message (no channel spam). The anchor is the
    dashboard slack channel's per-PR status message when that channel is enabled
    (shared blob state `notifications/pr-{n}/slack.json`, same CAS
    machinery); otherwise the timeline creates a minimal anchor that the
    dashboard channel later edits into the full status message. A
    `thread_owner` field in the shared state lets the timeline claim the
    thread; the dashboard then suppresses its own courtesy thread notes so
    events are not double-posted.
  - `timeline_github` — one PR comment per commit SHA, maintained via a NEW
    marker namespace (`<!-- reeve:timeline:v1:{shortsha} -->`) and in-place
    edit. Entry history is persisted per PR in blob state
    (`notifications/pr-{n}/timeline.json`, CAS append) because preview and
    apply are separate CI processes; each event re-renders that SHA's whole
    comment.
- Event model: `planning` (preview run started) fills the only gap — `plan`
  already means preview-finished, `applying`/`applied`/`failed`/`blocked`
  cover apply. `break_glass` is added as a reserved, subscribable event with
  no producer yet, keeping the surface extensible for emergency-override
  runs. The legacy default subscription (`notify.PREvents()`-derived) is
  deliberately NOT widened; timeline channels default to the full
  `notify.TimelinePREvents()` set.
- Producer: `run.Preview` emits `planning` at run start (the only producer
  change); all other timeline entries reuse existing events. Each event's
  payload already carries its own run's CI URL, so entries link per-run.
- Dependencies: `notify.Deps` gains a narrow `CommentClient`
  (marker upsert) implemented by the GitHub VCS adapter and the run
  pipeline's comment poster — no VCS SDK in channels (modularity contract).

## Scope

**In:** `internal/notify` (events, deps), `internal/notify/channels/timeline`,
slack channel state sharing + thread-ownership suppression, `run.Preview`
planning event, config validation for the new events/types, docs.

**Out:** a break-glass producer (event is reserved only); timeline for
drift events (drift has its own report/issue surfaces); retention/pruning of
timeline blob state (bounded per PR, cleaned with the PR's other
notification state).

## Behavior compatibility notes

- Default OFF: both types must be explicitly declared in `channels:`. With no
  timeline channel configured, no producer path, marker, or Slack call changes.
- All existing comment markers and `comments.*` config keys are untouched;
  the timeline uses a new marker namespace.
- Existing channel subscriptions are unchanged: `planning`/`break_glass` are
  excluded from the legacy Slack trigger-onward default and only delivered
  where explicitly subscribed (or via the timeline default).
- When `timeline_slack` is enabled alongside the dashboard slack channel, the
  dashboard stops posting its own terse thread notes (the timeline's richer
  entries replace them) and the anchor message may be created earlier (at
  `planning`) than the dashboard trigger alone would have created it.
