# Modularity contract — tasks

Documentation-only change; no code. The delta folded into
`openspec/specs/architecture/spec.md` on archive.

- [x] Land the `architecture` capability spec (the contract).
- [x] Adopt it as the review checklist for `notification-channels` and any new
      provider axis.
- [x] Track the known current violations against their owning changes:
  - [ ] `auth/factory` + `blob/factory` static provider imports → carried
        forward to the `split-builds` change; the violation is restated in
        the architecture spec so it stays visible.
  - [x] PR-flow notifications off the channel abstraction →
        `notification-channels`. PR-flow events now publish through
        `internal/notify`; `internal/notifications/slack_templates.go` is gone.
  - [x] drift `github_issue`/`factory` importing `go-github` directly →
        `notification-channels`. The channel now depends on the
        consumer-defined `notify.IssueClient`; no VCS SDK is imported under
        `internal/notify`.
