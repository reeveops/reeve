# Design

## Where the Pulumi cause lives

`--json` puts the engine event stream on stdout. `parseApply` already decoded
`diagnosticEvent` alongside `summaryEvent`, but only consumed the latter and
returned `""` for the error on both of its exit paths. The fix collects
messages whose severity is `error`, joined newline-separated in stream order,
and returns them.

Precedence on failure is diagnostics, then stderr, then the process error.
`exit status 255` identifies nothing; a diagnostic names the resource and the
cause. When both exist the stderr text is appended rather than dropped, since
it can carry a login or backend failure the event stream never mentions.

Counts and diagnostics are independent: a partially applied update reports
both what changed and what failed.

## Truncation

`firstLine` is removed from both adapters. It was applied at the point of
storing an already-assembled message, so removing it is not a change of what
gets assembled. The HCL adapter's `failureMessage` (stderr, plus the process
error in parentheses, with a no-output fallback) already produced the right
text and was only being cut.

`internal/iac/hcltest` keeps its own `firstLine` for test diagnostics.

## Rendering

The three inline `**Error:** %s` sites become one `writeError` helper. A
single-line message stays inline, byte-identical to before, so existing
goldens do not move. A multi-line message goes in a fenced block, matching how
plan output is already presented.

Engine errors quote resource names and property values that originate in the
PR, so the fenced payload has its ``` runs broken with a zero-width space.
`internal/core` cannot import `internal/slack`'s equivalent helper under the
core purity rule, so the escape is inline.
