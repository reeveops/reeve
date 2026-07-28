# Engine dialects — tasks

## D1 — extract the shared engine

- [ ] Move the lifecycle, discovery and plan parsing into `internal/iac/hcl`.
- [ ] Replace `Variant` with `hcl.Dialect`; `hcl.New(d, cfg)` builds an Engine.
- [ ] Move the package's tests with it; they exercise the shared code.

## D2 — thin engine packages

- [ ] `internal/iac/terraform`: Dialect + `Register("terraform")`, nothing else.
- [ ] `internal/iac/tofu`: Dialect + `Register("tofu")`, nothing else.
- [ ] `internal/iac/all` imports both.
- [ ] `reeve lint` still resolves both types; registry tests cover each package.

## D3 — stop misdescribing OpenTofu

- [ ] `SourceExts` per dialect (`.tofu` only for tofu).
- [ ] `Capabilities.SecretsProviderTypes` reports OpenTofu's state-encryption
      key providers; terraform keeps `nil` with the backend-concern note.
- [ ] Conformance runs as two subjects, terraform and tofu, not "whichever
      binary resolved".

## D4 — write down the fork tripwire

- [ ] Record in `openspec/specs/iac/spec.md` what forces a real fork:
      the plan JSON formats diverging, or a difference that changes the
      command sequence rather than its arguments.
