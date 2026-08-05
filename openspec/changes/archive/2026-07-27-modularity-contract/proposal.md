# Modularity contract

## Why

reeve's design intent is a modular core with pluggable providers on every
axis: IAC engine (Pulumi/Terraform), VCS (GitHub/GitLab/Gitea), auth, blob,
notification channels, approval sources. An audit this cycle found the intent
held ~80% — IAC, cloud auth/blob, VCS interfaces, and approvals are genuinely
modular — but with concentrated, nameable debt:

- PR-flow notifications hand-rolled off the channel abstraction (addressed by the
  `notification-channels` change).
- Two `go-github` leaks in drift channels.
- **`auth/factory` and `blob/factory` statically import every concrete
  provider**, so no build can exclude one — the AWS, GCP, and Azure SDKs all
  link into every binary (~47 MB stripped). Split builds are impossible until
  this is fixed.

Without a written, enforceable rule, the next provider added the convenient
way deepens this debt and the eventual split-builds refactor grows from small
to a rewrite. This change writes the rule down so it's reviewable, not tribal.

## What

Add an `architecture` capability spec stating the modularity contract every
provider axis must satisfy:

- Each axis is consumed through an interface; core depends on the interface,
  never a concrete provider.
- Concrete provider SDKs are imported only in that provider's own package -
  never in core.
- Core must not branch on a provider's identity/`Name()`; capability flags
  express differences.
- Factories resolve providers by config; providers self-register so a build
  can compile in a subset (build-tag-sliceable). No factory statically
  imports every concrete provider.
- Heavy new provider dependencies land behind a build tag.

This is documentation of intent — no runtime behavior changes. It becomes the
review checklist for `notification-channels`, `split-builds`, and any new axis.

## Scope

**In:** the `architecture` spec (the contract).

**Out (tracked elsewhere):** actually fixing the factory self-registration
(the `split-builds` change), the notification unification (`notification-channels`),
and the drift-channel `go-github` leaks. This change only states the rule; the
existing violations are called out here as known debt to be resolved by those
changes, not by this one.
