# IAC — plan locking and refresh delta

## ADDED Requirements

### Requirement: An engine can persist a plan and later execute exactly that plan

`PreviewOpts.SavePlanPath` SHALL request that the engine persist the plan it
computes; `PreviewResult.PlanPath` SHALL report the artifact it actually
wrote. `ApplyOpts.PlanPath` SHALL name an artifact a previous preview
produced, and an engine handed one SHALL execute exactly that change set and
SHALL NOT compute a new one.

`Capabilities.SupportsSavedPlans` SHALL describe this preview → apply
round-trip, not an adapter-internal plan-then-apply sequence.

#### Scenario: A saved plan is written and executed

- **WHEN** a preview runs with `SavePlanPath` set on an engine whose
  `SupportsSavedPlans` is true
- **THEN** `PreviewResult.PlanPath` names a non-empty file at that path, and an
  apply given that path executes the saved change set without planning again

#### Scenario: The world moved under a saved plan

- **WHEN** an apply is given a saved plan whose state lineage or serial has
  advanced
- **THEN** the apply FAILS, rather than applying a change set nobody reviewed

#### Scenario: The engine cannot save plans

- **WHEN** a preview runs with `SavePlanPath` set on an engine whose
  `SupportsSavedPlans` is false
- **THEN** the preview succeeds normally and `PreviewResult.PlanPath` is empty:
  the field is a request, and plan locking degrades rather than breaking
  previews

#### Scenario: A failed plan saves nothing

- **WHEN** the plan step fails
- **THEN** `PreviewResult.PlanPath` is empty, so no caller can lock an apply to
  a file that was never written

### Requirement: Refresh is part of the engine contract

`iac.Engine` SHALL include `Refresher`. `Refresh` reconciles the engine's
recorded state with live infrastructure. It SHALL change no infrastructure.
`RefreshResult.Counts` SHALL describe state reconciliation, never
infrastructure change.

`RefreshOpts.PreviewOnly` SHALL produce the same answer without writing state.
An engine that cannot answer read-only SHALL return an error rather than run a
writing refresh.

#### Scenario: A dry-run refresh

- **WHEN** `Refresh` is called with `PreviewOnly: true`
- **THEN** engine state is byte-identical afterwards

#### Scenario: A refresh on a converged stack

- **WHEN** `Refresh` runs against a stack that matches its state
- **THEN** counts are zero, and the following preview is still a no-op

#### Scenario: An engine without the capability

- **WHEN** an engine reports `SupportsRefresh: false`
- **THEN** it still implements `Refresh` and returns an error saying so, so
  callers never type-assert to find out

### Requirement: Refresh and a locked plan are mutually exclusive

A refresh recomputes the diff, which is what a locked plan pins. An engine
given both `ApplyOpts.PlanPath` and `ApplyOpts.Refresh` SHALL fail rather than
silently dropping either.

#### Scenario: Both requested

- **WHEN** an apply is given a plan path and `Refresh: true`
- **THEN** it fails before any CLI process runs, naming the conflict

## MODIFIED Requirements

### Requirement: Capabilities

```go
type Capabilities struct {
    SupportsSavedPlans   bool // preview→apply plan round-trip, not internal plan-then-apply
    SupportsRefresh      bool // implements Refresher; Preview/Apply honor Refresh
    SupportsPolicyNative bool
    SecretsProviderTypes []string
    PreviewOutputFormat  Format
}
```

Extended as new engines reveal needs. Adding a capability is a spec change. A
capability SHALL describe what the adapter actually implements: a capability
that overstates the adapter makes the feature built on it degrade with no
signal.

#### Scenario: Pulumi declares saved plans

- **WHEN** the Pulumi adapter wires `preview --save-plan` and `up --plan`
- **THEN** `SupportsSavedPlans` is true, and the adapter sets
  `PULUMI_EXPERIMENTAL` on exactly those invocations rather than globally
