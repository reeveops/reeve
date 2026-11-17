# Plan locking, refresh, freshness default — tasks

## Digest binding and fail-closed availability (remediation 2026-11-11 delta)

- [ ] Record the plan artifact's SHA-256 on the manifest entry at preview time.
- [ ] Verify the digest at apply time and fail the stack closed on missing,
      unreadable, oversized, or mismatched artifacts.
- [ ] Add sibling boolean `engine.plan_locking_allow_replan` (default deny) while keeping `engine.plan_locking` as a boolean, and add the `--replan` per-run availability-recovery flag, both loud on timeline and audit. Existing boolean YAML needs no migration.
- [ ] Restrict direct recovery to missing, unreadable, or oversized artifacts. On digest mismatch or a key without a digest, reject `--replan` and require a new preview plus new approvals; old approvals must not authorize the replacement plan.
- [ ] Keep the no-plan-key compatibility path narrow and loud; confirm the
      fresh-plan timeline entry names the stack and reason.
- [ ] Malicious-corruption and digest-mismatch tests; prove replan cannot reuse old approvals after tamper or missing digests; availability-recovery audit tests; schema tests for boolean `plan_locking` plus its sibling key.

## P1 — engine contract

- [x] `PreviewOpts.SavePlanPath` / `PreviewResult.PlanPath` / `ApplyOpts.PlanPath`,
      with the degradation and refusal rules written into the doc comments.
- [x] `PreviewOpts.Refresh` / `ApplyOpts.Refresh`.
- [x] `iac.Refresher` (`RefreshOpts`, `RefreshResult`) added to `iac.Engine`.
- [x] `Capabilities.SupportsSavedPlans` redefined as the preview→apply
      round-trip.

## P2 — adapters

- [x] HCL: `plan -out=<SavePlanPath>` kept on disk; `apply <PlanPath>` skips the
      plan step entirely; `Refresh` via `plan -refresh-only` + `apply <file>`,
      stopping after the plan when `PreviewOnly`.
- [x] Pulumi: `preview --save-plan`, `up --plan`, `refresh [--preview-only]`;
      `PULUMI_EXPERIMENTAL` scoped to the update-plan invocations.
- [x] Both: refuse `PlanPath` + `Refresh` together.
- [x] Pulumi `SupportsSavedPlans: true`.

## P3 — run pipeline

- [x] Preview saves a plan per stack, uploads it under the run prefix, records
      `StackSummary.PlanKey`.
- [x] Apply fetches the artifact for the previewed commit and passes it through;
      the original absent-artifact fallback is SUPERSEDED by the open fail-closed tasks above: a recorded-key absence must fail, and only a no-plan-key manifest retains the loud compatibility fallback.
- [x] `run.Refresh` runner: scope, gates, locks, comment, audit.
- [x] `engine.plan_locking` (default true) and `PlanLockingEnabled` /
      `EngineSupportsSavedPlans` resolution.

## P4 — surfaces

- [x] `reeve run refresh` with `--dry-run`, `--all`, `--local`.
- [x] `--refresh` on `run apply` and `run preview`.
- [x] action.yml dispatch for `/reeve refresh` and `/reeve apply --refresh`.
- [x] Help comment, README, getting-started tables.

## P5 — freshness

- [x] `DefaultPreviewFreshness = 4h`; `ResolvedPreviewFreshness()`.
- [x] Loader rejects non-positive durations.
- [x] docs/configuration.md: default, opt-out, and what plan locking does and
      does not cover.

## P6 — tests

- [x] Contract suite: saved-plan round trip, capability-optional save,
      read-only dry-run refresh, converged refresh.
- [x] HCL unit tests: locked apply never re-plans, failed plan saves nothing,
      dry-run refresh never applies.
- [x] Run unit tests: artifact key cannot escape the run prefix, round trip is
      byte-exact and 0600, apply executes the stored plan, missing artifact is
      reported, `--refresh` drops locking.
- [x] Schema tests: `plan_locking` and `preview_freshness` defaults.

## P7 — follow-ups (not in this change)

- [ ] Pulumi through the engine conformance contract, which would exercise its
      saved-plan and refresh paths the way the HCL engines are exercised now.
- [ ] Object-level encryption for stored plan artifacts, so plan locking does
      not depend on the bucket already being treated as sensitive.
