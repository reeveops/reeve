# Notifications — channel framework delta

## ADDED Requirements

### Requirement: A shared channel framework serves both producers

Notification delivery SHALL go through the shared channel framework
(`internal/notify`): a `Channel` interface (Name / Subscribes / Deliver), a
unified event model covering PR-flow events (`plan`, `ready`, `approved`,
`applying`, `applied`, `failed`, `blocked`) and drift events
(`drift_detected`, `drift_ongoing`, `drift_resolved`, `check_failed`), and a
`Dispatch` that filters by subscription. A destination implemented once
SHALL be able to subscribe to events from either producer.

#### Scenario: One implementation, two producers

- **WHEN** a channel type (e.g. slack, webhook) is configured with both PR-flow
  and drift events in `on:`
- **THEN** the same channel instance receives payloads from the run pipeline and
  the drift runner, distinguished by the payload's producer field

### Requirement: Channels self-register; the factory resolves by config type

Concrete channels SHALL live in their own packages under
`internal/notify/channels/*` and register a constructor in `init()`. The
factory SHALL resolve a config entry purely by its `type:` string against
the registry and SHALL NOT statically import concrete channels (modularity
contract). A default set is compiled in via blank imports
(`internal/notify/all`); a build can import a subset instead.

#### Scenario: Unknown channel type fails loudly

- **WHEN** a config declares a channel `type:` that no compiled-in channel
  registered
- **THEN** building the channel list fails with an error naming the unknown type
  and the registered types

#### Scenario: Optional dependencies skip, not fail

- **WHEN** a channel's runtime dependency is absent (no Slack token, no GitHub
  issue client, no annotation emitters)
- **THEN** that channel is skipped, matching prior factory behavior

### Requirement: VCS SDKs stay out of channels

The `github_issue` channel SHALL consume a narrow, consumer-defined issue
interface (find-by-marker / create / update / close) implemented by the
GitHub VCS adapter. `go-github` SHALL NOT be imported outside
`internal/vcs/github`.

#### Scenario: SDK confined

- **WHEN** grepping for `google/go-github` imports
- **THEN** only `internal/vcs/github` matches

### Requirement: Delivery is concurrent, bounded, and retried

`Dispatch` SHALL deliver to channels concurrently (per-channel ordering preserved)
with a per-delivery timeout so one hung endpoint cannot starve the rest.
HTTP channels SHALL use a shared client with a sane timeout and SHALL retry
transient failures (network errors, HTTP 5xx, 429) with bounded exponential
backoff. Delivery errors are collected and logged, never fatal to the run.

#### Scenario: Hung endpoint does not starve dispatch

- **WHEN** one channel's endpoint hangs past the per-delivery timeout
- **THEN** other channels' deliveries complete normally and the hung channel
  contributes a timeout error to the collected results

#### Scenario: Transient 5xx recovers

- **WHEN** a webhook endpoint returns 503 once and then 200
- **THEN** the delivery succeeds after retrying with backoff

### Requirement: Generic channel configuration with back-compat

`notifications.yaml` v2 SHALL declare channels as a generic list (`type` +
settings + `on:` event subscriptions). The v1 shape (single `slack:` block)
SHALL keep loading and be mapped onto the channel model internally
(`slack.events` → `on:`; trigger/icons/rules carry over). `reeve
migrate-config` SHALL rewrite v1 files to v2 (with backup and `--dry-run`).
Unknown `on:` event names SHALL fail load/lint with the valid list; an empty
subscription list SHALL warn (except the Slack trigger-onward default).

#### Scenario: Legacy config unchanged

- **WHEN** a repo with a v1 `notifications.yaml` (slack block) upgrades reeve
- **THEN** Slack notifications behave exactly as before, with no config edit
  required

#### Scenario: Migration produces the same effective channels

- **WHEN** `reeve migrate-config` rewrites a v1 notifications file
- **THEN** the migrated v2 file loads, validates, and yields the same
  effective channel list as the v1 file did

#### Scenario: Typo in on: caught at lint

- **WHEN** a channel declares `on: [aplied]`
- **THEN** `reeve lint` (and config load validation) fails, listing the valid
  event names

### Requirement: Slack PR state is loaded safely and written conditionally

The Slack PR message state (`notifications/pr-{n}/slack.json`) SHALL be
read distinguishing not-found (fresh state) from failure: on failure the
delivery errors instead of posting a duplicate message. Writes SHALL use the
blob store's conditional-write primitive (`PutIfMatch`); on conflict with a
concurrent run that created a different message, the first writer's state
wins and the conflict is surfaced.

#### Scenario: State outage does not duplicate the message

- **WHEN** the blob store errors (not not-found) while loading Slack state
- **THEN** no Slack message is posted and the delivery reports the error

### Requirement: Slack text is escaped and truncated safely

Externally-controlled text interpolated into Slack mrkdwn (PR titles,
authors, error messages) SHALL be escaped per Slack's rules (`&`, `<`, `>`).
Payloads embedded in code fences SHALL be made fence-safe (a ``` run cannot
terminate the fence). Truncation SHALL never split a UTF-8 rune.

#### Scenario: Title injection neutralized

- **WHEN** a PR title contains `<!channel>`
- **THEN** the Slack message renders it as literal text, not a mention

### Requirement: PR-flow notifications ride the channel framework

PR-flow notifications SHALL be published as producer events through the
shared framework rather than a hand-rolled backend. The Slack per-PR message
lifecycle (one message per PR, upsert-in-place, thread timeline, trigger
semantics for message creation, rule-gated stacks) is the slack channel's
handling of PR events and SHALL be preserved: `trigger` still controls
which event creates the message; the default subscription remains every
lifecycle event at or after the trigger.

#### Scenario: Default Slack behavior preserved

- **WHEN** a repo uses the default configuration (trigger `apply`, no events
  list)
- **THEN** the Slack message is created at apply time and updated through
  applied/failed/blocked, identical to the previous release
