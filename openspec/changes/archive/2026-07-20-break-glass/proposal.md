# Break-glass apply

## Why

reeve gates `/reeve apply` behind approvals, CODEOWNERS, checks, freeze
windows, and locks — correctly, for the 99% case. The 1% case is the 3am
incident: prod is down, the fix is a one-line PR, and the two required
approvers are asleep. Today the only options are to wait, or to go around
reeve entirely (apply from a laptop), which is worse: no gates at all, no
audit trail, no PR record.

An emergency path should exist INSIDE the tool, where it can be scoped
(who), constrained (what it does and does not bypass), and loudly audited
(what happened, why, by whom). Philosophy: provide the tools, audit
everything, don't babysit.

## What

- **Config**: a new opt-in `break_glass:` block in `shared.yaml`
  (config_type=shared, strict loader). OFF unless configured — the command
  errors politely otherwise.
  - `authorized:` is a UNION of sources: `internal_list` (explicit logins
    and `org/team` slugs), `codeowners` (anyone CODEOWNERS makes an owner
    of a changed path), `anyone`, and `vcs_bypass` (GitHub ruleset bypass
    actors — config surface only; the runtime rejects it with a clear "not
    yet supported" error because resolving bypass actors to logins requires
    org-level APIs beyond repo-scoped credentials). A phase-2 `groups:`
    source (`group:<provider>:<name>`) is parsed and rejected with a clear
    phase-2 error.
  - `override_freeze:` defaults to TRUE — break-glass overrides freeze
    windows unless explicitly disabled.
- **Authorization is head-resolved**: the config (and CODEOWNERS) as of the
  PR HEAD decides. Self-add is allowed BY DESIGN — an emergency responder
  may need to add themselves in the same PR — but the audit record flags
  when any authorizing file (`.reeve/*.yaml`, CODEOWNERS locations) was
  modified in the same PR.
- **Semantics**: overrides the approvals gate always; overrides freeze when
  `override_freeze`; NEVER bypasses locks; every other precondition
  (checks, up-to-date base, preview freshness/success, policy hooks,
  fork/draft) still applies. Break-glass replaces human sign-off; it is not
  a license to apply stale or unchecked code. Overridden gates surface as
  warnings in the gate trace, never silently.
- **Command**: `/reeve breakglass "<justification>" apply` with a STRICT
  parse — the justification must be a non-empty double-quoted string
  followed by the verb. Malformed input posts a helpful error comment and
  runs nothing. CLI parity: `reeve run apply --break-glass --justification
  "..."` (empty justification rejected). action.yml adds the verb to the
  existing comment dispatch and passes the raw comment via env var
  (`REEVE_BREAK_GLASS_COMMENT`) for the CLI to parse.
- **Audit**: the write-once audit entry gains a `break_glass` block —
  justification, matched authorization source, overridden gates, the
  same-PR-authorizing-config-modified flag and paths — plus a `run_url`
  field. The reserved `break_glass` notify event (deployment-timeline
  change) gains its producer: timeline entries and subscribed channels fire.
  The apply PR comment renders a loud, marker-tagged warning section
  (`<!-- reeve:break-glass:v1 -->`) with the justification quoted.

## Scope

**In:** `internal/core/breakglass` (pure authorization + strict command
parse + authorizing-path detection), `shared.yaml` schema,
`internal/core/preconditions` override composition, `internal/audit`
break-glass record, `internal/run/apply.go` wiring, apply-comment
rendering, CLI flags, action.yml verb dispatch, docs.

**Out (phase 2, surface designed only):** external identity groups
(`group:<provider>:<name>` — parse-and-reject), `vcs_bypass` runtime
resolution (config surface + not-yet-supported error), and an interactive
freeze-confirm flow.

## Behavior compatibility notes

- Default OFF: with no `break_glass:` block, every existing path is
  byte-identical and the new command fails closed with a polite error.
- Non-break-glass applies are unchanged: same gates, same comment, same
  audit shape (the new fields are omitempty).
- The `break_glass` notify event was already reserved and excluded from
  legacy default subscriptions; producing it changes nothing for channels that
  did not subscribe.
