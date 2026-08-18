# Design

## Immutable trusted revision

The VCS PR shape gains the base commit SHA returned by the same metadata request that returns fork status and head SHA.
Every PR operation snapshots that SHA once and never resolves a moving branch name again during the run.

The VCS adapter reads `.reeve` files at the exact base SHA through the repository contents API.
The adapter rejects symlinks, submodules, oversized files, duplicate config types, and paths outside `.reeve`.

## Two parsed configurations

The existing strict decoder parses trusted bytes and checked-out HEAD bytes independently before any merge.
Unknown keys, invalid versions, missing required trusted files, or API failures stop the operation before credentials or configurable subprocesses.

Environment references in trusted configuration expand from the controller environment only after source ownership is established.
Source-aware validation rejects any environment reference introduced by HEAD at any path.

An environment reference is introduced when the HEAD raw scalar contains `${env:...}` and the base lacks a byte-identical scalar at the same path.
The generic validation error omits the variable name and does not inspect whether the variable exists.

For trusted-owned paths, an unchanged HEAD scalar is ignored and only the trusted scalar is eligible for expansion.
Workload-owned paths are copied from validated HEAD without trusted environment expansion.

## Field ownership

Ownership paths are qualified by config type and use YAML field names, `[]` for list entries, and `*` for every descendant.
The implementation encodes this table as data and tests every workload-owned pattern.

| Path | Owner | Additional validation |
| --- | --- | --- |
| `version`, `config_type` | trusted | HEAD parses independently with a supported version and the same config type. |
| `shared.*` | trusted | HEAD values never affect the effective configuration. |
| `auth.*` | trusted | HEAD cannot select providers, bindings, credentials, or durations. |
| `notifications.*` | trusted | HEAD cannot select channels, destinations, headers, or secrets. |
| `observability.*` | trusted | HEAD cannot select exporters, annotations, destinations, or secrets. |
| `drift.*` | trusted | Scheduled runs continue to use their checked-out trusted revision. |
| `engine.type` | trusted | HEAD must name the same engine type. |
| `engine.binary.*` | trusted | HEAD cannot select an executable or version. |
| `engine.state.*` | trusted | HEAD cannot select a backend, auth provider, or secrets provider. |
| `engine.execution.*` | trusted | HEAD cannot select timeouts or parallelism. |
| `engine.policy_hooks[]` | trusted | HEAD cannot select commands or failure behavior. |
| `engine.plan_locking` | trusted | HEAD cannot weaken plan locking. |
| `engine.stacks[].project` | workload | The enclosing entry has a unique stack identity. |
| `engine.stacks[].path` | workload | The path remains inside the checkout after normalization. |
| `engine.stacks[].pattern` | workload | The pattern passes existing stack-declaration validation. |
| `engine.stacks[].stacks[]` | workload | Values are unique within the enclosing entry. |
| `engine.filters.exclude[]` | workload | Entries pass existing filter validation and are unique after normalization. |
| `engine.change_mapping.ignore_changes[]` | workload | Patterns are unique after normalization. |
| `engine.change_mapping.extra_triggers[].project` | workload | The project is the keyed entry identity. |
| `engine.change_mapping.extra_triggers[].paths[]` | workload | Paths are unique within the enclosing entry. |
| `engine.change_mapping.scope` | workload | The value passes existing scope validation. |

Unknown keys fail strict decoding before ownership is evaluated.
Known fields without a workload-owned table match are trusted-owned.

A trusted-owned field absent from the base uses only its schema default or absence value.
It never falls back to the HEAD value.

Adding or widening a workload-owned path requires a spec delta and a security test.
Semantic validation must prove that the path cannot select an executable, credential, policy, or outbound sink.

## Merge behavior

The merge copies the validated trusted configuration and replaces only workload-owned paths from the validated HEAD configuration.
It operates on typed configuration values and never performs a generic YAML map overlay.

Explicit YAML null is rejected in both sources.
Empty scalars and collections are accepted only when the existing schema accepts them and never trigger fallback.

| Collection | Identity | Merge and deletion rule |
| --- | --- | --- |
| `engine.stacks[]` | normalized `(project, path, pattern)` tuple | HEAD selects identities and order. Matching entries preserve the base object and replace only workload-owned descendants. New entries use the base-resolved trusted template. Missing templates and duplicate identities fail. Omission deletes a workload entry. |
| `engine.stacks[].stacks[]` | stack name | HEAD replaces the nested set; duplicate names fail. |
| `engine.filters.exclude[]` | normalized scalar pattern or `stack` value | HEAD replaces the list; duplicate identities fail. |
| `engine.change_mapping.ignore_changes[]` | normalized pattern | HEAD replaces the list; duplicate patterns fail. |
| `engine.change_mapping.extra_triggers[]` | `project` | HEAD replaces the list; duplicate projects fail. |
| `engine.change_mapping.extra_triggers[].paths[]` | normalized path | HEAD replaces the nested set; duplicate paths fail. |

Trusted-owned maps and lists always come from the base without entry-level merging.
Any future workload-owned map or list requires an identity and deletion rule in the ownership table.

Ambiguous, duplicate, or invalid identities fail before credentials, network sinks, policy hooks, or engine execution.
Merging workload declarations cannot replace trusted auth, state, binary, execution, or policy-hook fields.

The trusted stack template contains the base-resolved values for every trusted-owned `engine.stacks[]` descendant.
The current schema has no trusted-owned stack descendants, so its template is empty.

Adding a trusted-owned stack descendant requires a base-owned template source and merge rule in the same spec delta.
Until that source exists, a new HEAD stack identity fails before side effects.

The run manifest and audit entry record provenance by ownership group instead of only by config type.
Each record includes the path pattern, keyed entry identity when applicable, source repository, and source commit without configuration values.

Trusted groups record the target repository and `trusted_config_revision`.
Workload groups record the head repository and immutable head SHA.

A missing trusted config type does not fall back to the PR version.
Repository bootstrap requires the configuration to land on the base branch before PR automation can use it.

The trusted engine config and its `engine` container must exist on the base revision.
HEAD may add a keyed stack entry only through the workload-owned paths in the table.

A PR-added or modified auth, binary, state, execution, or policy field inside a stack entry fails strict decoding under the current schema.
An unchanged trusted-owned descendant added by a future schema is ignored in HEAD and sourced from the matching base entry or trusted template.

HEAD cannot bootstrap a trusted policy container.

## Visibility

Changed-file analysis identifies PR edits to trusted fields and config files.
The preview comment lists those edits as proposed changes that are ignored for the current run and take effect after merge.

Logs and comments do not render secret values or expanded environment references.
All messages pass through the existing redactor.

## Apply consistency

`trusted_config_revision` is the canonical identifier for trusted configuration and equals the immutable target-repository base commit SHA.
Preview manifests record both head SHA and `trusted_config_revision`.

Apply snapshots PR metadata again and compares its BaseSHA with the manifest `trusted_config_revision`.
Any mismatch fails the preview consistency gate and requires a new preview.

This prevents an approval under one base policy from being applied after that policy changes.
The normal up-to-date gate remains independent.

## Local and drift behavior

Local commands have no PR trust boundary and load the working tree as before.
Drift runs against the repository revision checked out by the trusted scheduled workflow and record that commit SHA.
