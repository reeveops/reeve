# Notification channels — tasks

- [x] Lift the channel framework into `internal/notify`: `Channel` interface,
      unified event model (PR flow + drift), registry with `init()`
      self-registration, factory resolving by config `type:` string.
- [x] Concurrent `Dispatch` with per-delivery timeout; shared HTTP client;
      bounded retry with backoff (`PostJSON`) for 5xx/network errors.
- [x] Migrate the five drift channels onto the framework, behavior-identical.
- [x] Fix the `go-github` leaks: `github_issue` consumes a narrow
      `notify.IssueClient` implemented by `internal/vcs/github`.
- [x] PR flow as producer: run pipeline (`preview`, `apply`, `ready`,
      `approved`) publishes events via `notify.Dispatch`; Slack PR message
      lifecycle moves into the slack channel.
- [x] Slack hardening: propagate state-load errors (duplicate-post fix),
      compare-and-swap state writes via `blob.PutIfMatch`, mrkdwn escaping,
      fence-safe embedding, UTF-8-safe truncation.
- [x] Config: `notifications.yaml` v2 `channels:` list; v1 `slack:` block maps
      onto the channel model internally; `on:` event validation at load/lint;
      warn on empty subscriptions.
- [x] `reeve migrate-config`: notifications v1 → v2 migration with tests.
- [x] Docs: docs/notifications.md (channel catalog, events, adding a
      destination), configuration.md notifications section, drift.md channel
      section points at the shared framework.
- [ ] Archive this change: fold the delta into
      `openspec/specs/notifications/spec.md` on merge.
