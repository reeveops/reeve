# IaC Engine Interface

## Contract

`iac.Engine` is the complete contract an adapter satisfies: `Name()`
(display only - core **never branches** on it), `Capabilities()`, and the
operational surface `EnumerateStacks`, `Preview`, `Apply`, `DriftCheck`,
composed from narrow per-operation interfaces (`Enumerator`, `Previewer`,
`Applier`, `DriftChecker`) so consumers that need less depend on less.

Shared option and result types (`PreviewOpts`, `PreviewResult` including
the drifted-resource carrier, `ApplyOpts`, `ApplyResult`) live in
`internal/iac`, engine-neutral; engine-specific parsing stays in the
adapter package. `internal/iac` never imports engine SDKs or shells out
itself. Consumers (run pipeline, drift runner) depend on `iac` interface
types only; the concrete engine is injected by command wiring. Each adapter
carries a compile-time assertion (`var _ iac.Engine = (*Engine)(nil)`).

## Capabilities

```go
type Capabilities struct {
    SupportsSavedPlans   bool
    SupportsRefresh      bool
    SupportsPolicyNative bool
    SecretsProviderTypes []string
    PreviewOutputFormat  Format
}
```

Extended as new engines reveal needs. Adding a capability is a spec change.

## Structured per-resource drift diffs

`PreviewResult.Resources` (`[]ResourceChange`) carries the normalized,
engine-agnostic per-resource change shape the drift runner needs for noise
filtering: address, resource type, operation, changed property paths
(dotted, with array indices), and a drift `Category` of
`changed | orphaned | missing`. Pulumi derives it from preview steps +
`detailedDiff` (a create step after refresh is orphaned-state drift);
Terraform/OpenTofu derives it from `resource_drift`, walking
`before`/`after` into the same dotted-path shape - so
`classification.ignore_properties` globs apply to either engine unchanged.
Best-effort: an adapter with no structured diff leaves `Resources` nil and
the raw drift verdict stands.

## Engine registry

Concrete engines live in their own packages under `internal/iac/<engine>/`
and register a constructor via `iac.Register(type, ctor)` in `init()`. The
factory `iac.New(engineCfg)` resolves purely by the config `engine.type`
string and never statically imports concrete engines (modularity contract).
A default set is compiled in via blank imports (`internal/iac/all`); a
build can import a subset. An unknown `engine.type` fails loudly - naming
the unknown and registered types - at resolution and at `reeve lint`.
Duplicate registration of a type panics at startup.

Until multi-engine routing lands, commands resolve the first engine config
(`cfg.Engines[0]`, load order) through the factory; `reeve stacks discover
--engine <type>` resolves the requested type through the same factory,
preferring the matching engine config file's settings when present.

## Terraform / OpenTofu adapter

`engine.type: terraform` and `engine.type: tofu` resolve to **one** adapter
package parameterized by variant (binary name, display name, registry key).
Both types register from the same package `init()`; `engine.binary.path`
overrides the binary for either variant. Capabilities: saved-plan support,
refresh support, no native policy engine, no engine-side secrets providers
(state encryption is a backend concern).

**Stack model:** a root-module directory (`.tf` files with a `terraform {}`
block or provider configuration, excluding anything under `modules/`) is a
project; a `terraform workspace` is a stack. Dir-per-env layouts enumerate
as `<project>/default`. Config-declared stacks are authoritative for
enumeration - no workspace listing or init required; without declarations
the adapter may list workspaces via the CLI, falling back to `default` with
a log line (never an opaque failure). Workspaces are created only when
config-declared; undeclared workspaces are refused with an explanatory
error, never invented.

**Lifecycle per stack:** `init -input=false` → `workspace select` →
`plan -input=false -detailed-exitcode -out=<planfile>` →
`show -json <planfile>`, with `apply <planfile>` consuming the exact saved
plan the plan step wrote (plan-what-you-apply parity). Plan exit codes:
0 = no changes (apply skipped), 2 = changes, anything else = failure. Init
failures surface with a clear init-prefixed message carrying the CLI's
stderr.

**Drift checks** run `plan -refresh-only -input=false -detailed-exitcode`
plus `show -json`, taking the drifted resource set from `resource_drift`. A
refresh-only plan reads live infrastructure without writing state, so drift
checks never mutate engine state. Parseable plan JSON is authoritative for
the verdict regardless of exit code; a check producing no parseable JSON
(exit 1, missing binary, timeout, malformed output) returns a non-empty
error message AND a non-nil error - mirroring the pulumi adapter's contract
so the drift runner never misreads a failed check as "no drift". Drifted
addresses populate the shared drifted-resource carrier (address strings in
the role of URNs).

**Sensitive values:** values marked `before_sensitive` / `after_sensitive`
never appear in rendered property diffs (masked as `[sensitive]`) nor in
stored plan JSON (scrubbed before storage; a value/marker structure
mismatch masks the whole value; a scrub failure drops the blob entirely
rather than storing it raw). `after_unknown` values render as
"(known after apply)".

## Adding a new engine

1. Implement the interface in `internal/iac/<engine>/`.
2. Register via `iac.Register` in the package `init()`; add the blank
   import to `internal/iac/all`.
3. Ship.

Target: ~500–1000 lines of Go per engine. No changes to `internal/core/`,
no CLI changes, no config loader changes.
