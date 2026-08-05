# Break-glass apply

Break-glass is reeve's opt-in emergency apply: it skips the approvals gate
(and freeze windows, by default) in exchange for a **mandatory
justification** and a **loud, immutable audit trail**. It exists so the 3am
"prod is down and both approvers are asleep" fix happens *inside* reeve —
scoped, constrained, and recorded — instead of from someone's laptop with
no gates at all.

Philosophy: provide the tools, audit everything, don't babysit.

## When to use it

- A production incident needs an infrastructure change *now* and the
  required approvers are unavailable.
- A freeze window is blocking an emergency fix (freeze override is on by
  default; disable with `override_freeze: false` if your freezes must bind
  even in emergencies).

Break-glass is **not** a shortcut for slow reviews. Every use is audited
with the actor, justification, and everything that was overridden — expect
to explain it afterwards.

## What is and is NOT bypassed

Break-glass replaces *human sign-off*. It is not a license to apply stale
or unchecked code:

| Gate | Break-glass behavior |
|---|---|
| Approvals (incl. CODEOWNERS review rules) | **Overridden** — always |
| Freeze windows | **Overridden** when `override_freeze: true` (the default); binding when `false` |
| Per-stack locks | **NEVER bypassed** — a held lock still blocks |
| Required checks green | Still enforced |
| Up-to-date with base | Still enforced |
| Preview succeeded / preview freshness | Still enforced |
| Policy hooks | Still enforced |
| Fork-PR / draft-PR gates | Still enforced |

Overridden gates show up as **warnings** in the gate trace — visible in the
PR comment, never silent.

## Configuration

Break-glass is **off unless configured**. Add a `break_glass:` block to
`.reeve/shared.yaml`:

```yaml
break_glass:
  authorized:                      # UNION: any matching source grants
    internal_list:                 # explicit logins and org/team slugs
      - alice
      - myorg/sre
    codeowners: true               # anyone CODEOWNERS makes an owner of a changed path
    anyone: false                  # any actor (justification + audit still apply)
    vcs_bypass: false              # GitHub ruleset bypass actors — NOT YET SUPPORTED (see below)
    # groups:                      # phase 2 — parsed but rejected today
    #   - "group:aws_iam:oncall"
  override_freeze: true            # default true; false keeps freezes binding
```

- `internal_list` — explicit user logins (case-insensitive, `@` optional)
  and `org/team` slugs (expanded via the VCS API; an unresolvable team
  never matches — fail closed).
- `codeowners` — the actor is an owner (directly or via team) of at least
  one file changed by the PR, per the CODEOWNERS file at the PR HEAD.
- `anyone` — any actor. Justification and audit still apply. Use with care.
- `vcs_bypass` — reserved for GitHub ruleset bypass actors. The config key
  parses, but authorization fails with a clear "not yet supported" error:
  resolving bypass actors (team IDs, repository roles, integrations) to
  logins requires org-level APIs beyond reeve's repo-scoped credentials.
- `groups` — phase-2 surface for external identity providers
  (`group:<provider>:<name>`, e.g. `group:aws_iam:oncall`). Parsed and
  rejected with a phase-2 error today.

Sources are a **union**: any one granting is enough. When several match,
the audit records the narrowest (internal_list before codeowners before
anyone). Configuring `vcs_bypass` or `groups` makes break-glass error even
if another source would match — silently ignoring a configured
authorization source would leave you believing it works.

## Command

From a PR comment (strict syntax — anything else posts a helpful error
comment and runs nothing):

```
/reeve breakglass "<justification>" apply
/reeve breakglass "<justification>" apply --force
```

The justification must be a **non-empty, double-quoted** string, followed
by the verb. Typographic quotes (“ ”) from mobile keyboards are accepted.

CLI parity:

```
reeve run apply --pr 123 --break-glass --justification "prod is down, hotfixing LB target group"
```

An empty justification is rejected before anything runs.

## Authorization is head-resolved (self-add by design)

The `break_glass:` config and CODEOWNERS content **as of the PR HEAD**
decide who is authorized. That means a PR can add its own author to
`internal_list` and then break-glass — deliberately: in a real emergency
the responder may need to grant themselves access, and reeve's philosophy
is to allow-and-audit rather than babysit.

The counterweight: the audit record (and the PR comment) carry an explicit
**"authorizing config modified in this same PR"** flag whenever the PR
touches `.reeve/*.yaml` or a CODEOWNERS file (`CODEOWNERS`,
`.github/CODEOWNERS`, `docs/CODEOWNERS`), listing the touched paths. A
self-add cannot happen quietly.

> **Known and intentional.** Same-PR self-authorization is a deliberate
> tradeoff: at 3am the responder who needs to act may be the only one who
> can grant access, and blocking that would defeat the purpose of
> break-glass. It is safe *because* it is loud — every self-add is flagged
> and written to the immutable audit trail. This is a documented property,
> not a gap.

### Locking it down (`reject_self_authorization`)

If your threat model prefers availability-loss over any chance of same-PR
self-authorization, set `break_glass.reject_self_authorization: true`
(default `false`). With it set, a PR that modifies its own authorizing files
cannot authorize a break-glass apply *at all* — the run is denied before any
source is evaluated, and the denial trace names the touched paths. Everything
else about break-glass is unchanged. The default stays allow-and-audit; this
is the opt-in for teams that want the harder guarantee.

## Audit trail

Every break-glass run writes the standard write-once audit entry
(`audit/YYYY/MM/DD/<run-id>.json` in your bucket) extended with:

```json
{
  "actor": "alice",
  "commit_sha": "…",
  "run_url": "https://github.com/…/actions/runs/…",
  "outcome": "success",
  "break_glass": {
    "justification": "prod is down, hotfixing LB target group",
    "authorized_via": "internal_list",
    "overridden_gates": ["approvals", "not_in_freeze"],
    "authorizing_config_modified": false
  }
}
```

`overridden_gates` lists only gates that **would have failed** and were
overridden — a break-glass run whose approvals happened to be satisfied
records nothing overridden.

In addition to the completion entry, an **intent entry**
(`audit/YYYY/MM/DD/<run-id>-intent.json`, `"outcome": "break_glass_intent"`)
is written **before** the engine runs — its write is a hard requirement: if
the audit store cannot record the intent, the break-glass apply refuses to
start. This guarantees a durable trace of every override attempt even if the
process dies mid-apply.

Beyond the audit file:

- The apply PR comment leads with a loud, marker-tagged warning section
  (`<!-- reeve:break-glass:v1 -->`): actor, matched source, overridden
  gates, the same-PR flag, and the justification quoted verbatim.
- The reserved `break_glass` notification event fires: the
  [deployment timeline](notifications.md) records the override, and any
  channel subscribed to `break_glass` (Slack, webhook, PagerDuty, …) is
  notified.
- The run log prints a `BREAK-GLASS apply authorized` warning line.

## Failure modes (all fail closed)

| Situation | Result |
|---|---|
| No `break_glass:` block | Polite error explaining how to enable; no run |
| Block present, no sources | Error; no run |
| Actor matches no source | Denied with a per-source trace; no run |
| `vcs_bypass` / `groups` configured | "Not yet supported" / "phase 2" error; no run |
| Malformed comment command | Helpful usage comment posted; no run |
| Empty justification | Rejected before any lock/credential/engine call |

## Phase 2 (designed, not implemented)

- **External identity groups** — `group:<provider>:<name>` entries under
  `authorized.groups` (AWS IAM, Okta, …). The syntax is parsed and
  rejected today so configs written for phase 2 fail loudly, not silently.
- **`vcs_bypass` resolution** — mapping GitHub ruleset bypass actors to
  logins once an org-scoped credential path exists.
- **Freeze interactive confirm** — an extra confirmation round-trip before
  overriding a freeze window.
