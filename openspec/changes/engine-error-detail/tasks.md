# Tasks

## Implementation

- [x] Return error diagnostics from `parseApply` instead of discarding them.
- [x] Prefer diagnostics over the process error in pulumi apply and refresh,
      appending stderr when both are present.
- [x] Remove `firstLine` truncation from the pulumi adapter.
- [x] Remove `firstLine` truncation from the hcl adapter, including workspace
      enumeration's log line.
- [x] Report a policy hook's stderr as its error via `hookFailure`.
- [x] Render both stdout and stderr for a failed or warning hook.
- [x] Add `writeError` and use it in the preview, apply, and refresh comments.
- [x] Escape ``` runs in fenced error text.

## Verification

- [x] Test: error diagnostics are returned; info and warning severities are not.
- [x] Test: diagnostics survive alongside parsed counts.
- [x] Test: hcl apply keeps every line of the engine message.
- [x] Test: hook stderr reaches both the error and the rendered section.
- [x] Test: `hookFailure` precedence and no-output fallback.
- [x] Test: `writeError` inline vs fenced, and fence escaping.
- [x] Golden: multi-line error renders fenced; existing goldens unchanged.
- [x] `mise run check` green.
