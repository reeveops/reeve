# IaC — engine registry delta

## ADDED Requirements

### Requirement: The engine interface is the full adapter contract

`iac.Engine` SHALL be the complete contract an adapter satisfies: `Name()`
(display only — core never branches on it), `Capabilities()`, and the
operational surface `EnumerateStacks`, `Preview`, `Apply`, `DriftCheck`,
composed from narrow per-operation interfaces (`Enumerator`, `Previewer`,
`Applier`, `DriftChecker`) so consumers that need less can depend on less.
Shared option and result types (`PreviewOpts`, `PreviewResult` including the
drifted-resource carrier, `ApplyOpts`, `ApplyResult`) SHALL live in
`internal/iac`, engine-neutral; engine-specific parsing stays in the adapter
package. `internal/iac` SHALL NOT import engine SDKs or shell out itself.

#### Scenario: Adapters satisfy the contract at compile time

- **WHEN** an adapter package (e.g. `internal/iac/pulumi`) builds
- **THEN** a compile-time assertion (`var _ iac.Engine = (*Engine)(nil)`)
  proves it implements the full contract

#### Scenario: Consumers stay engine-agnostic

- **WHEN** a core package (run pipeline, drift runner) needs engine
  operations
- **THEN** it depends on `iac` interface types and shared result types only,
  and the concrete engine is injected by command wiring

### Requirement: Engines self-register; the factory resolves by config type

Concrete engines SHALL live in their own packages under `internal/iac/<engine>/`
and register a constructor via `iac.Register(type, ctor)` in `init()`. The
factory `iac.New(engineCfg)` SHALL resolve purely by the config `engine.type`
string and SHALL NOT statically import concrete engines (modularity
contract). A default set is compiled in via blank imports
(`internal/iac/all`); a build can import a subset instead.

#### Scenario: Unknown engine type fails loudly

- **WHEN** a config declares an `engine.type` that no compiled-in engine
  registered
- **THEN** resolution fails with an error naming the unknown type and the
  registered types, and `reeve lint` fails on the same condition

#### Scenario: Registration is unique

- **WHEN** two packages register the same engine type
- **THEN** the second registration panics at startup, surfacing the wiring
  bug immediately

### Requirement: Single-engine routing picks the first engine config

Until multi-engine routing lands, commands SHALL resolve the first engine
config (`cfg.Engines[0]`, load order) through the factory. `reeve stacks
discover --engine <type>` SHALL resolve the requested type through the same
factory, preferring the matching engine config file's settings when present.

#### Scenario: Existing single-engine repos are unaffected

- **WHEN** a repo declares one engine config with `engine.type: pulumi`
- **THEN** every command behaves exactly as before the registry existed

### Requirement: Terraform and OpenTofu share one parameterized adapter

`engine.type: terraform` and `engine.type: tofu` SHALL resolve to one
adapter package parameterized by variant (binary name, display name,
registry key) — not two copies. Both types register from the same
package `init()`; `engine.binary.path` SHALL override the binary for
either variant. Capabilities SHALL report saved-plan support and
refresh support, no native policy engine, and no engine-side secrets
providers (state encryption is a backend concern).

#### Scenario: Both types resolve to the shared adapter

- **WHEN** configs declare `engine.type: terraform` and `engine.type: tofu`
- **THEN** each resolves through the registry to the shared adapter with
  its variant's default binary (`terraform` / `tofu`) and display name

### Requirement: Terraform stack model is root module + workspace

For the terraform/tofu adapter, a root-module directory (`.tf` files
with a `terraform {}` block or provider configuration, excluding
anything under a `modules/` directory) SHALL be a project, and a
`terraform workspace` SHALL be a stack. Dir-per-env layouts SHALL
enumerate as `<project>/default`. Config-declared stacks SHALL be
authoritative for enumeration — no workspace listing or init required;
without declarations the adapter MAY list workspaces via the CLI and
SHALL fall back to the `default` workspace with a log line (never an
opaque failure) when listing is unavailable. The adapter SHALL create a
workspace only when it is config-declared, and SHALL never invent
undeclared workspaces.

#### Scenario: Declared stacks enumerate without init

- **WHEN** engine config declares `stacks:` entries matching a root
  module that has never been initialized
- **THEN** enumeration returns the declared (project, workspace) pairs
  without invoking the engine binary

#### Scenario: Undeclared workspaces are refused

- **WHEN** an operation targets a workspace that does not exist and is
  not declared in engine config
- **THEN** the operation fails with an explanatory error instead of
  creating the workspace

### Requirement: Terraform apply consumes the saved plan from its own plan step

The terraform/tofu lifecycle per stack SHALL be `init -input=false` →
`workspace select` → `plan -input=false -detailed-exitcode
-out=<planfile>` → `show -json <planfile>`, with `apply <planfile>`
consuming the exact saved plan file the plan step wrote
(plan-what-you-apply parity). Plan exit codes SHALL classify as: 0 = no
changes (apply is skipped), 2 = changes, anything else = failure. Init
failures SHALL surface with a clear init-prefixed message carrying the
CLI's stderr.

#### Scenario: Exit-code classification

- **WHEN** `plan -detailed-exitcode` exits 1
- **THEN** the operation reports failure with the CLI's stderr, and no
  apply runs

### Requirement: Terraform drift checks are refresh-only and fail closed

Drift checks SHALL run `plan -refresh-only -input=false
-detailed-exitcode` plus `show -json`, taking the drifted resource set
(addresses and counts) from `resource_drift`. A refresh-only plan reads
live infrastructure without writing state, so drift checks SHALL NOT
mutate engine state. Parseable plan JSON SHALL be authoritative for the
verdict regardless of exit code; a check that produces no parseable
JSON (exit 1, missing binary, timeout, malformed output) SHALL return a
non-empty error message AND a non-nil error — mirroring the pulumi
adapter's contract so the drift runner never misreads a failed check as
"no drift". Drifted addresses SHALL populate the shared drifted-resource
carrier used for fingerprinting (address strings in the role of URNs).

#### Scenario: Unparseable drift JSON fails closed

- **WHEN** a drift check's `show -json` output cannot be parsed
- **THEN** the result carries a non-empty error and the call returns a
  non-nil error, and the runner records a failed check (never
  "no drift")

### Requirement: Terraform sensitive values are masked everywhere

Values marked by `before_sensitive` / `after_sensitive` in the plan
JSON SHALL never appear in rendered property diffs (masked as
`[sensitive]`) nor in the stored plan JSON (scrubbed before storage; a
value/marker structure mismatch masks the whole value, and a scrub
failure drops the blob entirely rather than storing it raw). Values
marked by `after_unknown` SHALL render as "(known after apply)".

#### Scenario: Sensitive attribute in an update diff

- **WHEN** an updated attribute is marked sensitive on either side
- **THEN** the property diff shows `[sensitive]` in place of the
  value(s) and the raw values appear nowhere in stored output
