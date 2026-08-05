# Engine providers — tasks

## E1 — engine registry (refactor, zero behavior change)

- [x] Extract the de-facto engine interface from the call sites:
      `iac.Engine` composes Name/Capabilities with Enumerator, Previewer,
      Applier, DriftChecker; shared option/result types
      (`PreviewOpts`/`PreviewResult` incl. `DriftedURNs`,
      `ApplyOpts`/`ApplyResult`) live in `internal/iac`.
- [x] Self-registering factory: `iac.Register(type, ctor)` +
      `iac.New(engineCfg)` resolving purely by `engine.type`; unknown type
      errors listing registered engines; pulumi registers in `init()`;
      `internal/iac/all` blank-import manifest imported by `cmd/reeve`.
- [x] Update the six call sites (`cmd/reeve/{apply,run,lint,drift,stacks}.go`)
      to resolve via the registry; first-engine-wins routing preserved;
      `reeve lint` fails when any `engine.type` doesn't resolve.
- [x] Pulumi `Capabilities()` accurate as the reference implementation
      (saved plans, refresh, native policy, secrets-provider types).
- [x] Tests: registry register/resolve/unknown-type/duplicate-panic;
      compile-time `var _ iac.Engine = (*pulumi.Engine)(nil)`; existing suite
      green unmodified.
- [x] Docs: configuration.md engine section notes that `engine.type` selects
      a registered engine.

## E2 — Terraform adapter (`engine.type: terraform`)

- [x] Lifecycle: `init -input=false` → `plan -out=<file> -detailed-exitcode`
      → `show -json <file>` → apply the saved plan file (plan-what-you-apply
      parity; `SupportsSavedPlans: true`).
- [x] Stack model: root-module dir = project, `terraform workspace` = stack;
      dir-per-env layouts enumerate as `project/default`; declared stacks
      are authoritative and work without init (undeclared dirs fall back to
      `workspace list`, then `default` with a log line). Per-env var-file
      mapping deferred — the workspace model covers the decided scope.
- [x] Plan JSON → `PreviewResult`: op counts (replace = both action
      orders), property-level diffs from `resource_changes`,
      sensitive-value masking (rendered diffs AND stored plan JSON),
      `after_unknown` as "(known after apply)", drifted addresses
      (fingerprinting + rendering neutral to URN-vs-address).
- [x] Drift: `plan -refresh-only -detailed-exitcode` (exit 2 = drift,
      drifted set from `resource_drift`), fail closed on exit 1 or
      unparseable JSON — non-empty Error and non-nil error, mirroring the
      pulumi contract.
- [x] `stacks discover`: scan for root modules (dirs with .tf +
      terraform{}/provider block, excluding `modules/`).
- [x] Auth via existing env-var credential bindings — no new auth surface.
- [x] Golden fixtures from `terraform show -json` for parse tests; guarded
      live smoke test (REEVE_TF_SMOKE_BIN) for the full lifecycle.
- [x] Example: `examples/toy-stack-terraform` (random/null provider, local
      backend — no cloud creds).

## E3 — OpenTofu (`engine.type: tofu`)

- [x] Same adapter parameterized (binary name, display name, capability
      deltas as they diverge — none yet); registers both types;
      `engine.binary.path` overrides work for both.
- [x] `reeve init` offers pulumi/terraform/tofu for real (drops
      "coming soon"); non-interactive detection never auto-picks tofu.
- [x] Capabilities additions as needed — none needed: the existing fields
      (saved plans, refresh, no native policy, nil secrets providers)
      describe both variants accurately.
- [ ] Archive this change: fold the delta into `openspec/specs/iac/spec.md`
      on merge.
