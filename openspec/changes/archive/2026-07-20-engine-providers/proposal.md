# Engine providers: registry + Terraform + OpenTofu

## Why

reeve's engine seam existed on paper only. `internal/iac.Engine` was just
Name + Capabilities, the concrete operations lived on the pulumi adapter, and
six call sites (`cmd/reeve/{apply,run,lint,drift,stacks}.go`) hard-constructed
`pulumi.New(...)` — so `engine.type` in config selected nothing. The
modularity contract (openspec/specs/architecture, issue #13) requires that
providers are selected by configuration through a self-registering factory and
that core never branches on provider identity. Issue #34 lands Terraform and
OpenTofu as first-class engines on that seam, in three stacked phases.

## What

- **E1 — engine registry (refactor, zero behavior change).** `iac.Engine`
  becomes the full adapter contract (Name, Capabilities, EnumerateStacks,
  Preview, Apply, DriftCheck) composed from the narrow per-operation
  interfaces; shared option/result types live in `internal/iac`. A registry
  (`iac.Register` / `iac.New`) resolves engines purely by the config
  `engine.type` string; the pulumi adapter registers itself in `init()`; a
  blank-import manifest (`internal/iac/all`) compiles in the default set so
  builds can slice. All six call sites resolve through the factory; an
  unknown `engine.type` errors listing the registered engines, and
  `reeve lint` fails on it. Single-engine routing is preserved: the first
  engine config wins (multi-engine routing is a later phase).
- **E2 — Terraform adapter (`engine.type: terraform`).** CLI-driven, same
  shape as pulumi. Saved-plan lifecycle (`init` → `plan -out` → apply the
  saved plan), root-module dir = project / workspace = stack, plan JSON →
  `PreviewResult` with sensitive-value masking, refresh-only drift with
  fail-closed parsing, `stacks discover` root-module scanning. reeve never
  touches state; backends stay user-owned. Auth via existing env-var
  bindings.
- **E3 — OpenTofu (`engine.type: tofu`).** The same adapter parameterized
  (binary name, display name, capability deltas as they diverge); registers
  both types. `reeve init` offers pulumi/terraform/tofu for real.

## Scope

**In:** `internal/iac` (interface + registry + manifest), the pulumi adapter's
registration, cmd wiring, the terraform/tofu adapters (E2/E3), configuration
docs, golden plan-JSON fixtures, a terraform toy example.

**Out (tracked elsewhere):** multi-engine routing within one repo (call sites
keep first-engine-wins), auth/blob factory self-registration
(`split-builds`), build-tag gating of engine adapters (the manifest makes it
possible; wiring tags is `split-builds` territory).

## Behavior compatibility notes

- E1 is behavior-identical for valid configs: `engine.type` is already
  required and unique per file at load, and pulumi remains the only
  registered engine. The one sharpened edge: a config whose first engine's
  `type` is not a compiled-in engine now fails resolution (previously the
  pulumi adapter was constructed regardless), and `reeve stacks discover
  --engine <t>` reports "unknown engine type" listing the registered set
  instead of the hardcoded "pulumi only in v1" message.
