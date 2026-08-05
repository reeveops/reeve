# Break-glass apply — tasks

- [x] `internal/core/breakglass`: pure authorization decision
      (`Authorize`) over the source union (internal_list / codeowners /
      anyone), fail-closed for unconfigured, sourceless, `vcs_bypass`
      (not-yet-supported error), and `groups:` (phase-2 error); decision
      trace on every path.
- [x] Strict command parse (`ParseCommand`): non-empty double-quoted
      justification then `apply` (optional `--force`); typographic-quote
      tolerance; descriptive errors echoing usage; `MalformedComment`
      renderer for the no-run helpful comment.
- [x] `AuthorizingPathsTouched`: changed-paths diff against `.reeve/*.yaml`
      and CODEOWNERS locations for the same-PR self-add flag.
- [x] Schema: `break_glass:` block (`authorized:` union, `override_freeze`
      defaulting true) in shared.yaml, strict loader, off unless present.
- [x] Preconditions: `BreakGlass` / `BreakGlassOverrideFreeze` inputs;
      approvals gate warns-as-overridden, freeze conditionally; overridden
      gates reported in `Result.Overridden`; locks/checks/preview/policy/
      fork/draft untouched. Table tests for the composition.
- [x] Audit: `break_glass` block (justification, authorized_via,
      overridden_gates, authorizing_config_modified + paths) and `run_url`
      on the write-once entry.
- [x] `run.Apply`: justification fail-fast, head-resolved authorization
      before any lock/credential/engine call, break_glass notify event
      (producer for the reserved event), loud timeline entries, loud
      marker-tagged PR-comment section, audit wiring.
- [x] CLI: `reeve run apply --break-glass --justification "..."`; falls
      back to strictly parsing `$REEVE_BREAK_GLASS_COMMENT`; malformed →
      helpful PR comment + error, no run.
- [x] action.yml: `breakglass` verb in the existing comment dispatch,
      raw comment passed via env var (minimal delta; expected trivial
      conflict with the PR that reworks dispatch).
- [x] Docs: docs/break-glass.md, configuration.md block reference, command
      tables, README feature line.
- [ ] Phase 2: `groups:` (external identity providers) and `vcs_bypass`
      runtime resolution; freeze interactive confirm.
- [ ] Archive this change: fold the deltas into
      `openspec/specs/core/{approvals,preconditions}/spec.md` and
      `openspec/specs/notifications/spec.md` on merge.
