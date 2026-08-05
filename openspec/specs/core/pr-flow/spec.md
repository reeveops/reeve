# PR Flow

## Overview

On PR open or update, reeve runs **preview** for every stack touched. A single
PR comment is posted and edited in place on subsequent runs. On `/reeve apply`
comment (or merge, depending on config), reeve acquires locks and runs **apply**.

## Pipeline

1. PR opened / updated → `reeve run preview` for touched stacks.
2. Single PR comment posted, identified by hidden HTML marker, edited in place
   on subsequent runs. Help comment upserted separately.
3. Slack message posted/updated (if configured).
4. If `auto_ready: true`, when PR converts from draft to ready for review and plan has
   succeeded, reeve fires `/reeve ready` automatically. Otherwise author runs it manually.
5. Reviewers approve per configured rules.
6. Apply is initiated per `apply.trigger` (see Apply trigger modes below):
   `comment` → on `/reeve apply`; `merge` → on PR merge. Either way reeve
   acquires locks, evaluates preconditions, and runs apply.
6a. Apply posts a per-run timeline comment, starting with `🚀 apply starting` and appending each event (see rendering spec).
7. Results update PR comment and Slack message.
8. Audit log entry written to bucket.
9. Locks released, queue advanced.

## Requirements

- Preview runs in parallel across stacks; apply serializes per-stack via locks.
- Preview artifacts persist under `runs/pr-{n}/{run-id}/` for the PR lifetime.
- Apply does **not** replay a plan saved by the earlier preview. Preview
  freshness is a gate, not plan reuse: apply requires a successful preview on
  the current HEAD SHA within `preconditions.preview_freshness`, then
  re-executes the engine (Pulumi runs `pulumi up`; Terraform/OpenTofu re-plan
  inside Apply and apply that just-produced plan file, giving
  plan-what-you-apply parity within the apply call itself).
- Apply on **fork PRs** is **deny by default**. Opt-in per repo with documented
  risk; fork PRs otherwise get dry-run-only credentials.
- Notifications run last in the pipeline so upstream failures are captured
  accurately in the authoritative "what happened" surface.
- SHA resolution: `apply`, `ready`, and `approved` commands resolve the commit
  SHA from the PR HEAD via the VCS API (`GetPR`), not from `GITHUB_SHA`. This
  ensures manifests and plan lookups use the branch tip SHA regardless of what
  the CI runner checked out.
- Stacks declared with `path: .` (repo root) are triggered by any changed file
  that survives `ignore_changes` filtering.
- Docs/asset-only changes (skip globs) run nothing; preview/apply report
  "Documentation/asset-only changes".
- Files mapping to no stack broaden to all stacks under `scope: auto` (default);
  `scope: pulumi_only` disables broadening. See discovery spec.

## Apply trigger modes

`apply.trigger` selects the apply-initiation path. It is a flow selector, not a
gate — it changes only *when* an apply starts, never *whether* the gates hold.

- `comment` (default) — apply-then-merge. Apply runs only from a `/reeve apply`
  (or `@reeve apply` / `up`) comment. A merge event is a no-op.
- `merge` — merge-then-apply (continuous delivery). Apply runs when the PR is
  merged (`pull_request` `closed` with `merged: true`). A `/reeve apply` comment
  is a no-op.

Requirements:

- The binary is the source of truth for the mode. `run apply` receives
  `--trigger-source comment|merge` from the action and compares it against the
  configured `apply.trigger`; a mismatch is a deliberate no-op (exit success,
  nothing applied, one log line) so a mis-dispatched event cannot force an apply.
  Exactly one initiation path applies per repo.
- `merge` mode evaluates every gate against the PR HEAD SHA, identical to
  `comment` mode: approvals, checks-green, preview freshness/success, locks, and
  freeze all resolve exactly as pre-merge. `require_up_to_date` is the one gate
  whose result can differ post-merge (the base has advanced past HEAD); it
  fail-closes (blocks) and never opens, is off by default, and is intended for
  the apply-then-merge flow.
- Only a **merged** close dispatches an apply; a close without merge runs
  nothing. The already-applied guard dedups re-fires on the same commit.
- Break-glass is exempt from the trigger selector (explicit authorized
  emergency override); it still passes every other gate.
- An invalid `apply.trigger` value is rejected by config validation.

## Already-applied guard

A fully-clean apply (no failed/blocked stacks) writes `runs/pr-{n}/applied/{sha}.json`. A later run at the same commit:

- **apply** - skips work, posts the ⏭️ timeline notice, exits success.
- **preview** - renders the plan with an "already applied" banner.
- `--force` - bypasses the guard on both; re-runs all side effects.

## Out of scope (v1)

- Multi-engine runs in one PR (v1 is Pulumi only).
