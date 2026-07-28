# Engine dialects: separate OpenTofu from Terraform

## Why

`internal/iac/terraform` serves both `engine.type: terraform` and
`engine.type: tofu` from one adapter parameterized by a `Variant` struct. That
was correct when OpenTofu was a drop-in fork. It is no longer true, and the
adapter has already been caught by it twice:

- **Source discovery.** OpenTofu reads `*.tofu` in addition to `*.tf`, and
  where a base name exists as both, `.tofu` wins. A `.tofu`-only repo
  enumerated zero stacks with no error.
- **Capabilities.** OpenTofu 1.7+ has native, language-level state encryption
  (an `encryption` block with key providers). Terraform has no equivalent -
  its state encryption is a backend concern. The shared adapter reports
  `SecretsProviderTypes: nil` for both, so it actively misdescribes OpenTofu.

Both were found by asking whether the two are still interchangeable, not by a
failure in production. The next divergence will not be found that way. The two
projects are under different licences (BUSL vs MPL) and are not going to
re-converge; the list only grows.

The counter-argument is real: the operational surface reeve drives is ~95%
identical (`init`, `workspace select`/`new`, `plan -detailed-exitcode -out`,
`show -json`, `apply <planfile>`, `plan -refresh-only`), and the plan JSON that
`show -json` emits is deliberately compatible. Forking the package outright
would duplicate ~1,500 lines to express differences that are currently two
fields, and divergence would then creep in on both sides unnoticed.

So this change does not fork. It makes the split structural *before* it is
forced, so the shared 90% stays shared on purpose rather than by accident, and
each engine has an obvious, empty-by-default place to put what is genuinely its
own.

## What

Extract the shared engine into `internal/iac/hcl` and reduce each engine to a
declaration of how it differs.

```
internal/iac/hcl/          the shared engine (lifecycle, plan parsing, discovery)
internal/iac/terraform/    Dialect + Register("terraform")
internal/iac/tofu/         Dialect + Register("tofu")
```

`hcl.Dialect` is the seam. It starts as data, because every difference today is
data:

| field | terraform | tofu |
|---|---|---|
| `TypeName` | `terraform` | `tofu` |
| `Display` | `Terraform` | `OpenTofu` |
| `Binary` | `terraform` | `tofu` |
| `SourceExts` | `.tf` | `.tf`, `.tofu` |
| `Capabilities` | no engine-side secrets provider | state-encryption key providers |

When a difference is behavioral rather than data, `Dialect` grows a method and
the shared code calls it. That is the whole point of doing this now: there is a
named place for the next divergence, so it lands as a two-line dialect change
instead of a conditional buried in shared code.

Core is unaffected. `iac.Register` is keyed by the config `type:` string and
does not care which package registers it, so this is invisible to configuration
and to every consumer of `iac.Engine`.

## Non-goals

- **Not forking the lifecycle or the plan parser.** They stay shared until the
  plan JSON formats actually diverge. That is the deep coupling and the
  tripwire for a real fork (see the spec).
- **Not adding OpenTofu-only features.** Driving `encryption` config or
  `-exclude` is separate work; this change only stops reeve from claiming
  OpenTofu cannot do things it can.
- **No config change.** `engine.type: terraform` and `engine.type: tofu` behave
  exactly as before, minus the two bugs above.
