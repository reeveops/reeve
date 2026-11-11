# blob-slug-empty-component

## Why

`SlugComponent("", "stack")` returned `"stack"` verbatim - the fallback
landed in the pass-through namespace, so an empty stack name and a stack
literally named "stack" addressed the same lock object. The same aliasing
held for any fallback a component could also be named.

## What

Cherry-picked from `fix/audit-cas-and-lock-keys` (commit `72612ed`, recovered
from the local object database after the branch was deleted; the branch tip
is preserved by the review as "port/review the final commit before
deleting").

Every input that is not a non-empty pass-through name now lands in the
transformed namespace, empty included: the fallback only supplies something
readable, the digest is what identifies it. The injectivity test
brute-forces path-like, degenerate, unicode, and output-shaped names instead
of asserting over a hand-picked list.

## Stop-and-report analysis (plan condition)

**Not met - ordinary names keep their keys.** `TestSlugOrdinaryNamesKeepKeys`
compares the new mapping against an in-test replication of the EXACT pre-fix
formula for ordinary names (pass-through and sanitize-surviving): identical.
Key changes are confined to degenerate inputs:

- empty component: `fallback` verbatim → `<fallback>__<digest-of-empty>`
- names that sanitize to nothing (e.g. `///`): `key__<hash>` →
  `<fallback>__<hash>`

Neither can arise from a valid Pulumi/Terraform/OpenTofu or reeve
configuration (engine stack and project names are word-like), so no existing
lock or audit object is orphaned.

## Scope

- `internal/blob/key.go` and its test file. Callers (lock keys, audit keys)
  need no change: the mapping is injective per name and ordinary names are
  byte-identical.

## Impact

- No config change.
