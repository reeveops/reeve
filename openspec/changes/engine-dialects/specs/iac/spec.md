# IAC — engine dialects delta

## ADDED Requirements

### Requirement: HCL engines share one implementation, parameterized by dialect

Terraform and OpenTofu SHALL be served by one shared engine implementation in
`internal/iac/hcl`, parameterized by a `Dialect` that declares how a given
engine differs. Each engine SHALL live in its own package that contributes only
its dialect and its registry entry.

#### Scenario: A dialect-level difference

- **WHEN** an engine differs in a value (binary name, source extensions,
  advertised capabilities)
- **THEN** the difference is expressed as a field on that engine's `Dialect`,
  and the shared code reads the field rather than testing which engine it is

#### Scenario: Registration is package-independent

- **WHEN** `engine.type: tofu` is configured
- **THEN** `iac.New` resolves it through the registry regardless of which
  package registered it, and no configuration changes if the packages are
  later reorganized

### Requirement: OpenTofu's capabilities are reported accurately

The `tofu` dialect SHALL advertise the capabilities OpenTofu actually has,
independently of Terraform's. It SHALL NOT inherit a capability set that
describes Terraform.

#### Scenario: State encryption

- **WHEN** capabilities are read for `engine.type: tofu`
- **THEN** the engine-side state-encryption key providers are reported, because
  OpenTofu configures encryption in the language, unlike Terraform where it is
  a backend concern

### Requirement: A fork has a written trigger

The shared implementation SHALL be split into independent engines when, and
only when, one of the following holds. Until then, divergence is expressed
through the dialect.

- The plan JSON emitted by `show -json` diverges between the engines such that
  one parser can no longer serve both.
- A difference changes the *sequence* of commands the adapter runs, rather than
  their arguments or values.
- Dialect-conditional behavior (as opposed to dialect data) exceeds a handful of
  branches in the shared code.

#### Scenario: Deciding whether to fork

- **WHEN** a new OpenTofu or Terraform feature must be supported
- **THEN** it is added as dialect data or a dialect method if the command
  sequence is unchanged; otherwise the fork trigger is met and the engines are
  split, with plan parsing extracted to a package both depend on
