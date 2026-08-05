# IaC — retroactive sync delta

## ADDED Requirements

### Requirement: Preview results expose structured per-resource drift diffs

`PreviewResult.Resources` (`[]ResourceChange`) SHALL carry the normalized,
engine-agnostic per-resource change shape the drift runner needs for noise
filtering: address, resource type, operation, changed property paths
(dotted, with array indices), and a drift `Category` of
`changed | orphaned | missing`. The Pulumi adapter derives it from preview
steps + `detailedDiff` (a create step after refresh is orphaned-state
drift); the Terraform/OpenTofu adapter derives it from `resource_drift`,
walking `before`/`after` into the same dotted-path shape. The field is
best-effort: an adapter with no structured diff SHALL leave `Resources`
nil, and the raw drift verdict stands untouched.

#### Scenario: Engines converge on one shape

- **WHEN** a Pulumi stack and a Terraform stack each drift on one nested
  attribute
- **THEN** both adapters report a `ResourceChange` with the same
  dotted-path property style, so `classification.ignore_properties` globs
  apply to either engine unchanged

#### Scenario: No structured diff, no filtering

- **WHEN** an adapter cannot produce a structured diff for a check
- **THEN** `Resources` is nil and the stack's raw verdict is classified
  without noise filtering
