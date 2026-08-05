# Plan locking, refresh, and a real preview-freshness default

## Why

Three gaps, one root cause: reeve tells reviewers what it is going to do, and
then decides again at apply time.

**Apply does not execute the plan that was reviewed.** Both engine adapters
recompute the change set inside the apply call — Pulumi runs a bare
`pulumi up`, and the HCL adapter runs `plan -out=<tmp>` then `apply <tmp>`
within the same invocation, so the plan file never survives preview → apply.
The saved-plan lifecycle only guaranteed that apply executes what *apply* just
planned. What it never guaranteed is that apply executes what the *PR* planned.
"Last apply wins": another PR merging in between changes what ships, and
nothing anywhere says so.

`SupportsSavedPlans` described this honestly for Pulumi (`false`) and
misleadingly for Terraform/OpenTofu (`true`) — true of the adapter's internal
sequence, false of the promise a reviewer reads it as.

**There is no way to reconcile stale state.** When state is wrong — a console
edit, a half-finished apply, a resource deleted out of band — the plan is wrong
in a way no gate can catch, because every gate reasons about the plan. Both
engines can fix this (`pulumi refresh`, `terraform apply -refresh-only`) and
reeve exposes neither, except incidentally inside a scheduled drift check.

**Preview freshness was off by default.** The gate that bounds how stale a plan
may be shipped disabled unless an operator wrote the key. The failure it
prevents — an approval plus an old plan, applied later from a comment — is
most likely on exactly the busy repos least likely to have tuned the config.

## What

**Plan locking** (`engine.plan_locking`, default `true`). Preview asks the
engine to persist its plan (`PreviewOpts.SavePlanPath`); reeve stores the
artifact next to the run manifest, keyed by commit SHA; apply hands that exact
file back (`ApplyOpts.PlanPath`). Both engines gain the lifecycle:

| Engine | Preview | Apply |
|---|---|---|
| Terraform / OpenTofu | `plan -out=<file>` | `apply <file>` |
| Pulumi | `preview --save-plan=<file>` | `up --plan=<file>` |

An apply the world moved out from under now fails at the engine instead of
shipping a different change set. Degradation is one-directional: a missing or
unreadable artifact falls back to a re-plan and says so on the timeline; it
never applies something unreviewed silently.

**Refresh** as a first-class operation: `iac.Refresher` on the engine
contract, `/reeve refresh [--dry-run] [--all]` as a PR command, and
`/reeve apply --refresh` / `/reeve preview --refresh` as flags. Refresh writes
state and changes no infrastructure, so it carries the gates that protect
concurrent operations (locks, freeze, fork, draft) and not the ones that gate a
change set (approvals, checks, preview freshness) — it has no change set.

**`preview_freshness` defaults to 4h.** Only a literal `"0"` disables it.

## Trade-offs

**The stored plan is sensitive.** A plan artifact is the engine's serialized
change set: it carries resource attribute values, including ones the state
backend treats as secret. Unlike the plan summary in the run manifest it cannot
be redacted — redaction would make it useless as a plan. It lands in the
operator's own bucket under the run prefix and is pruned by the existing
`retention.max_age` sweep, but it is genuinely new sensitive data at rest, and
that is why `plan_locking` is a documented switch rather than unconditional.

**Pulumi's update-plan flags are experimental.** `--save-plan` and `--plan` sit
behind `PULUMI_EXPERIMENTAL`, which the adapter sets on exactly the invocations
that pass them rather than globally. Capability is declared `true` because the
lifecycle is wired; operators who do not want to ride an experimental flag turn
`plan_locking` off.

**Refresh is authorized by comment authorization, not approvals.** A refresh
can drop a resource from state, which a later apply would then recreate. That
is not nothing. Requiring approvals for it, though, would mean approving a
change set that does not exist, and would put the fix for stale state behind
the review cycle that stale state is currently breaking. Locks, freeze,
fork/draft, and the audit log carry it instead.
