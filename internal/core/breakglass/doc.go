// Package breakglass holds the pure decision logic for emergency
// ("break-glass") applies: who is authorized to invoke one, how the strict
// `/reeve breakglass "<justification>" apply` command is parsed, and which
// changed paths count as "authorizing config modified in the same PR".
//
// Philosophy (see docs/break-glass.md): provide the tools, audit everything,
// don't babysit. Authorization is resolved against the PR HEAD's config -
// self-add is allowed BY DESIGN - but the audit record flags when the
// authorizing config or CODEOWNERS file was modified in the same PR.
//
// Everything in this package is pure (no I/O); run/apply.go supplies the
// resolved inputs (actor, CODEOWNERS ownership, team membership) at the
// edges. Fail-closed throughout: unconfigured, ambiguous, or
// not-yet-supported sources deny with a trace or a hard error.
package breakglass
