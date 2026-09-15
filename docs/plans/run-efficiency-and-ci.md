# Run efficiency, GitHub Actions, and end-to-end testing

## Status

- Planning document, based on `master` at `1847a83f6ec8cf4b6d9baa4054393f6666a51ad9`.
- Reviewed on 2026-09-14 against the supplied v0.7.0 performance review and current source.
- Implementation starts on `fix/run-maintenance-efficiency` with lock snapshots and request-count regression tests.
- Remaining phases ship through the reviewed PRs and OpenSpec changes listed below.

## Implementation progress

- `reeve-test` has a Go harness with 24 passing real CLI/OpenTofu scenarios and a passing GitHub Actions lane.
- Its live GitHub App lane is implemented locally; verification is waiting for accessible App settings.
- This branch implements lock snapshot reuse and avoids unchanged maintenance writes, with CAS conflict regression tests.
- Retention, preview snapshot reuse, event routing, reusable workflows, and cloud lanes remain in the delivery checklist below.

## Recommended order

1. Measure startup and request counts; build the local regression fixtures.
2. Fix retention and lock-walker amplification.
3. Select and reuse one preview snapshot per apply or explain invocation.
4. Filter events before action setup and make binary installation match the pinned source.
5. Ship reusable workflows and a consumer test repository.
6. Add real bucket coverage and run the complete preview-to-apply lifecycle.
7. Add bounded preview concurrency and credential reuse after isolation and lifecycle tests pass.

## Findings verified in the current tree

| Priority | Finding | Evidence | Consequence |
| --- | --- | --- | --- |
| P0 | Preview and apply synchronously prune all `runs/` objects. Pruning calls `Get` for each object's age. | [gc.go](../../internal/run/gc.go), [preview command](../../cmd/reeve/run.go), [apply command](../../cmd/reeve/apply.go) | Startup cost grows with bucket history, including unrelated PRs. |
| P0 | GCS `Get` calls `Attrs` and opens a reader; `List` discards the returned modification time. | [GCS adapter](../../internal/blob/gcs/gcs.go) | Approximately two read requests per object before considering retries. |
| P0 | Apply and refresh run `ReapAll`; it decodes each lock twice and writes unchanged locks. | [lock store](../../internal/blob/locks/store.go), [refresh command](../../cmd/reeve/refresh.go) | Global reads and conditional writes precede work on the target stacks. |
| P0 | Apply scans preview history for scope, then again for every stack; explain scans per stack. | [preview lookup](../../internal/run/preview_lookup.go), [apply](../../internal/run/apply.go), [explain](../../internal/run/explain.go) | With S stacks and M manifests, apply performs roughly `(S + 1) * M` manifest reads plus repeated listings. |
| P0 | Event parsing is the final composite-action step, after installation, checkout, authentication, and optional Pulumi installation. | [action.yml](../../action.yml) | Unrelated comments and disabled review events still incur setup cost. |
| P1 | SHA refs build from source on cache misses; branch refs download the newest prerelease. | [binary fetch](../../.github/scripts/fetch-binary.sh) | Secure SHA pins lose the download fast path; branch downloads can cache a different revision under the checked-out source hash. |
| P1 | `ListAll` reads each lock twice; `UnlockPRAll` reads unrelated locks twice and matching locks again during mutation. | [lock store](../../internal/blob/locks/store.go) | End-of-apply cleanup and lock inspection also scale with global lock count. |
| P1 | Command wrappers and pipelines repeat PR reads; comment upserts rescan PR comments. | [apply command](../../cmd/reeve/apply.go), [refresh command](../../cmd/reeve/refresh.go), [preview](../../internal/run/preview.go), [GitHub client](../../internal/vcs/github/client.go) | Redundant API work and opportunities to mix different PR revisions. |
| P1 | Preview acquires state auth and logs into the backend before discovering that no stacks are affected. | [preview](../../internal/run/preview.go) | Docs-only changes still do backend setup. |
| P1 | Preview loops over stacks serially; `engine.execution.max_parallel_stacks` has no preview consumer. | [preview](../../internal/run/preview.go), [schema](../../internal/config/schemas/schemas.go) | Independent stack duration adds up rather than overlapping. |
| P2 | Workload auth is acquired for each stack without a registry cache. | [auth registry](../../internal/auth/provider.go), [auth resolution](../../internal/run/auth_helper.go) | Identical bindings can repeat federated exchanges. |

### What the supplied review establishes

- Its retention and reaper findings still apply to this checkout.
- A multi-minute GCS delay is consistent with the request pattern; no production timing logs or controlled A/B results were supplied.
- Preview logs pruning duration at debug level; apply and refresh omit equivalent maintenance timing.
- The `runenv.go` gap comment concerns bucket and auth construction, not a measured pruning diagnosis.
- Retention disabled with `retention.max_age: "0"` isolates pruning only; apply and refresh still run the reaper.

### Existing work to preserve

- PR gate inputs and team expansions are already gathered outside the stack loop; notification channels are already reused during preview.
- Saved-plan execution already exists for supported engines through [plan locking](../../openspec/changes/plan-locking/tasks.md).
- [Engine conformance](../../.github/workflows/ci.yml) already executes real Terraform and OpenTofu binaries against local fixtures.
- Scheduled security scans already avoid duplicating the normal PR vulnerability scans.
- The supplied review's fixes do not require new services, telemetry, or a control plane.

## 1. Baseline and regression harness

### Measurements

- Record action classification, binary restore/download/build, checkout, auth, engine installation, and CLI durations separately.
- Record CLI config loading, bucket opening, auth construction, retention, reaping, preview lookup, gate reads, engine work, persistence, and comment delivery.
- Emit local counters for logical blob operations and provider HTTP requests, including pages and retries.
- Record outcomes and durations on failures and cancellation as well as success.
- Route all diagnostics through redaction; record counts and operation names without credential values or request bodies.

### Controlled comparisons

- Seed an isolated test namespace with 0, 1,000, 2,000, and 10,000 run objects.
- Independently vary manifest count, lock count, comment count, and target stack count.
- Compare retention enabled and disabled against equivalent seeded namespaces and identical code, engine versions, and region.
- Use a fake engine for overhead measurements, then repeat selected cases with real engines.
- Record cold installation, warm installation, CLI overhead, and engine duration separately.
- Establish absolute latency targets from these measurements; enforce request-count limits immediately.

### First acceptance tests

- Unrelated `runs/` history cannot increase normal preview or apply startup requests after scheduled maintenance is adopted.
- Pruning 2,000 objects performs listing requests and eligible deletes, with zero content `Get` calls.
- An unchanged lock receives no conditional write during maintenance.
- Apply with 20 stacks and 100 historical manifests reads each candidate manifest at most once, excluding explicit conflict retries or revision revalidation.
- Skipped comments perform zero Reeve installation, checkout, cloud authentication, or engine-install steps.

## 2. Blob maintenance

### A. Eliminate unnecessary object reads

- Add a consumer-defined metadata-listing interface that preserves key, last-modified time, size, and version/ETag where available.
- Implement it for filesystem, S3/R2, GCS, and Azure without branching on provider identity in consumers.
- Preserve recursive prefix semantics and pagination; use a bounded page/callback interface for large maintenance scans.
- Keep objects with absent or invalid age metadata; a missing timestamp never authorizes deletion.
- For adapters without metadata listing, report unsupported maintenance or use a metadata-only capability; do not open every object body as a fallback.
- Optimize ordinary GCS reads to obtain metadata from the same reader response, after validating the pinned SDK's attributes and missing-object behavior.

### B. Make lock walkers efficient

- Return decoded lock content and its version together from exact-key reads.
- Keep the validation that authoritative project/stack names derive back to the listed key.
- Reap each lock from that first read; write only when the transition changes state.
- On a failed compare-and-swap, reread the lock and recompute the transition.
- Apply the same read reuse to `ListAll` and `UnlockPRAll`; preserve run-scoped ownership, FIFO promotion, and active-holder protection.
- Keep the existing conditional-write enforcement probe; separate its one-time cost from per-lock costs.
- Avoid a blanket equality shortcut in `mutate`: same-holder reacquisition and heartbeats intentionally renew leases.

### C. Remove global maintenance from the execution path

- Use targeted TTL eviction in `TryAcquire` for apply and refresh correctness.
- Keep global `reeve locks reap` as explicit maintenance and add an explicit artifact-pruning command.
- Recommend scheduled maintenance; retain an explicit compatibility mode for opportunistic cleanup during migration.
- Provide CLI/config parity for maintenance mode, age, operation budget, and dry-run behavior.
- Remove or migrate the unused `locking.reaper_interval` setting rather than leaving an interval that does nothing.
- If interval-based opportunistic maintenance is retained, use a bucket-backed CAS lease across runners; a process-local timer cannot limit separate CI jobs.
- Keep end-of-apply PR queue cleanup until its complete lifecycle is covered; optimizing startup reaping does not authorize dropping that cleanup.

### D. Protect concurrent writers and retention semantics

- Test an object replaced after listing but before deletion; skip a changed generation through conditional deletion or exclude mutable objects from that sweep.
- Separate disposable run artifacts from applied-state guards, mutable timelines, active saved plans, locks, and audit records.
- Define the intended lifetime of applied-state deduplication explicitly before changing lifecycle rules.
- Offer bucket lifecycle recipes only for eligible prefixes and account for provider age precision.
- Keep explicit maintenance for local adapters and cases lifecycle policies cannot express.
- Never apply a bucket-wide expiration rule to the entire Reeve prefix.

## 3. Reuse data with explicit freshness boundaries

| Data | Reuse boundary | Required refresh or invalidation |
| --- | --- | --- |
| Parsed config and discovery mapping | One invocation and immutable source revision | A new source revision or explicit configuration reload. |
| PR metadata used for setup | One resolved head/base snapshot | Before state-changing work, confirm the checkout, target PR head, and applicable trusted revision still agree. |
| Changed files | Repository, PR, head SHA, and base SHA | Either revision changes; a PR number alone is not a valid key. |
| Selected preview manifest and stack lookup map | One apply/explain decision snapshot | Explicit revision or selected-artifact version mismatch. |
| Preview freshness | Store the original timestamp | Recompute age using the current clock when each stack is gated. |
| Approvals, checks, CODEOWNERS, and team membership | One documented gate-evaluation boundary | New invocation, post-wait boundary, or configured revalidation; never a cross-run approval cache. |
| Lock content and CAS tokens | One attempted transition | Every CAS conflict, heartbeat, acquire, and release reads or validates live state. |
| Comment IDs | One client/invocation, keyed by repository, PR, marker, and author | Update after creates/deletes; rediscover on 404 without blind mutation retries. |
| Timeline state | Versioned read/modify/write | Preserve the current CAS merge against concurrent writers. |
| Federated credentials | Explicit shared lease inside one invocation | Expiry safety margin, rejected/expired auth, changed scope, or cleanup. |

### Preview snapshot implementation

- Load the selected preview once and build a stack-ref map used by both scope binding and per-stack gates.
- Preserve the newest-manifest ordering, including the existing RunID tie-break.
- Make missing, unreadable, malformed, and incomplete preview results explicit; errors must deny apply rather than fall back to an older successful manifest.
- Reject malformed creation timestamps instead of treating them as newly created previews.
- Resolve PR identity before SHA-keyed guard, scope, artifact, or audit operations.
- Define what happens if a new preview completes during apply: retain one coherent selection, revalidate at the execution boundary, and refuse incompatible changes rather than silently mixing selections.
- Add a per-PR/head index only after snapshot reuse is measured; an index needs atomic publication, an older-bucket fallback, and retention handling.

### Artifact identity prerequisite

- Current run keys contain workflow run number and short SHA, and manifests/plans use unconditional `Put`.
- GitHub reruns can therefore overwrite the same keys; calling those objects immutable is insufficient.
- Include workflow/run identity and attempt, or another collision-resistant invocation identity, in new artifact keys.
- Publish completed manifests after all referenced artifacts and bind saved-plan content to the selected version or digest.
- Retain reads of existing artifact layouts during migration; test reruns and concurrent preview publication.

### Additional savings

- Move authoritative no-op decisions before bucket construction, state authentication, and engine initialization where discovery permits it.
- Reuse PR metadata for final notifications instead of issuing a duplicate setup read solely for title or author.
- Cache owned comment IDs after their first discovery; avoid rescanning all comments for every timeline or multipart update.
- Fetch only policy-required gate data after the trusted configuration is known, preserving diagnostic behavior and fail-closed evaluation.
- Overlap independent gate reads with bounded concurrency after their dependencies and error behavior are explicit.

## 4. Action setup and binary distribution

### Dispatch before setup

- Extract event classification into a small, tested script using the runner's event JSON and trusted action inputs.
- Run it before binary hashing, cache restore, checkout, cloud authentication, and engine installation.
- Emit a validated command, target PR, trigger source, and structured argument data; never evaluate comment text as shell syntax.
- Gate every setup step on the classifier result and install only what the selected operation needs.
- Preserve explicit-command mode for scheduled maintenance, drift, local usage, and manual dispatch.
- Reject plain issue comments, unauthorized authors, bot comments, unknown verbs, malformed commands, and unsupported event actions before setup.
- Preserve configured command prefixes and exact verb matching, including existing aliases.
- Revalidate authorization and mutable PR identity in the CLI before side effects.

### Binary identity and caching

- Publish a signed artifact manifest binding binary digest, source identity, platform, and build variant.
- Resolve exact artifacts for pinned SHAs and version tags; branch use must resolve the action's actual source identity.
- Verify checksum, signature, allowed release workflow identity, and source identity before installation or cache save.
- Change the cache-key version so entries created by the old branch-resolution behavior cannot survive the fix.
- Include trusted source repository, complete build inputs, OS, architecture, and build variant in the cache key.
- Support prerelease tags explicitly; classify artifact eligibility before installing cosign on unsupported refs/platforms.
- Keep exact-source builds as the fallback when the matching verified artifact is unavailable.
- Save a verified binary before running PR-controlled code; avoid a post-job save of a binary path that workload code could modify.
- Treat Go, provider, and language dependency caches as downloads/build inputs; never put plans, state, tokens, or approval decisions in Actions caches.
- Make repeated provider installation cheaper without skipping backend validation or changing refresh semantics.

### Checkout and credentials

- Have one component own workload checkout; generated consumers should not checkout and immediately repeat checkout inside the action.
- Resolve and checkout an immutable PR head SHA; compare it with the SHA used by gates and artifacts.
- Load trusted controls before credential acquisition, with the existing trusted-config proposal as the dependency.
- Disable persisted checkout credentials and isolate controller credentials from workload processes.
- Keep storage auth, engine-state auth, and workload auth distinct; workload bindings do not automatically authenticate `factory.Open`.
- Replace unpinned refs and moving engine versions in examples with validated release pins.

## 5. Reusable workflows and event policy

### Consumer experience

- Ship `.github/workflows/reeve.yml` using `workflow_call`, plus a maintenance workflow.
- Retain the composite action for consumers that need custom steps.
- Put runner selection, routing, tool versions, caches, checkout, timeout, summary, and concurrency policy in the maintained workflow.
- Expose typed inputs for engine/version, root, command prefixes, associations, runner/environment, and supported federation settings.
- Map named secrets explicitly; avoid a default `secrets: inherit`.
- Keep workflow and action versions bound to the same tested release.

Proposed integration block, after this workflow ships:

```yaml
jobs:
  reeve:
    uses: reeveops/reeve/.github/workflows/reeve.yml@<full-release-commit-sha>
```

- The job call fits the requested four-line setup; the caller still declares triggers, permissions, and repository-specific federation settings.
- Generate the full caller through `reeve init` and validate it against the public workflow contract.
- A called workflow cannot elevate caller permissions; default checkout also targets the caller repository, so Reeve tooling must be referenced explicitly. [GitHub reusable workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows), [checkout behavior](https://docs.github.com/en/actions/concepts/workflows-and-actions/reusing-workflow-configurations).

### Event matrix

| Event | Default behavior | Heavy setup |
| --- | --- | --- |
| PR opened, reopened, or synchronized | Classify affected stacks and preview. | Only if work remains. |
| New commits on an open PR | Use `pull_request.synchronize`; avoid a second branch-push preview trigger. | Once for the selected head. |
| Docs/assets-only changes | Complete a no-op result using Reeve's change mapping. | None after classification where metadata-only mapping is possible. |
| PR ready for review | Existing ready/notification behavior. | No engine installation. |
| Review submitted | Off unless approval notifications are requested. | No engine installation. |
| Authorized PR comment beginning with an accepted prefix and verb | Dispatch the requested command. | Only the command's dependencies. |
| Ordinary comment, bot, unauthorized author, plain issue, or unknown command | Skip. | None. |
| PR merged | Apply only in merge-trigger mode. | Classified as apply, never as a cancellable preview. |
| PR closed without merge | Existing no-op behavior. | None. |
| Scheduled drift or maintenance | Explicit trusted command on the default branch. | Only required dependencies. |

### What GitHub can filter

- `issue_comment` supports activity types, not a comment-body trigger filter; job conditions are the mechanism for rejecting bodies before runner work. [GitHub events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#issue_comment).
- Use a job-level prefilter for standard prefixes and an exact classifier before setup; custom-prefix and whitespace behavior must agree with the parser.
- GitHub can still show a skipped workflow entry for rejected comments; the target is zero heavy jobs for them.
- Keep custom-prefix routing centralized instead of requiring every consumer to maintain a separate parser expression.
- Avoid workflow-level concurrency for unclassified comments, since irrelevant events can occupy or replace pending slots.

### Commits and ignore rules

- Treat Reeve's discovery mapping as authoritative, including root stacks, `ignore_changes`, shared files, and `extra_triggers`.
- Do not assume GitHub glob semantics equal Reeve's doublestar rules.
- Use workflow path filters only as conservative optional coarse filters; include configuration, shared modules, policy files, and dependency lockfiles.
- Prefer a lightweight no-op job when Reeve is a required check: workflow-level path skipping can leave a required check pending. [GitHub workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#onpushpull_requestpull_request_targetpathspaths-ignore).
- Handle renamed/deleted files, API pagination/truncation, and base/head changes without false-negative skips.
- Do not skip explicit apply, refresh, unlock, or explain commands solely because recent commits matched ignore patterns.
- Keep draft preview behavior unchanged initially; add a draft-skip setting only with matching CLI/config behavior and a preview when it becomes ready.

### Concurrency and checks

- Separate automatic previews, mutating commands, and informational commands after classification.
- Cancel superseded automatic previews; never let a push, review, help comment, or merge classification cancel an active apply/refresh.
- Preserve explicit manual command intent when selecting groups and pending-run behavior.
- The default GitHub concurrency queue replaces an existing pending run even with `cancel-in-progress: false`; current GitHub.com supports `queue: max`, with a 100-pending limit and no `cancel-in-progress: true`. [GitHub concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).
- Verify queue support for the supported GHES versions; where unavailable, avoid a shared replaceable queue for distinct commands and rely on Reeve's lock refusal semantics.
- Reeve's bucket locks remain authoritative for cross-PR stack exclusion; GitHub job ordering does not replace them.
- Publish a stable aggregate result name and update self-check exclusions for reusable/nested job names so apply does not wait on itself.
- Test required checks for no-op, failed, cancelled, and fork-validation runs, including repositories using merge queues.

## 6. Full consumer test repository

### Layout

Create `reeve-e2e` as a disposable consumer of released and candidate Reeve versions.
Keep reusable contract fixtures in this repository so failures can be reproduced locally.

```text
reeve-e2e/
  .github/workflows/
    reeve.yml                # public consumer workflow
    scenarios.yml            # trusted explicit lifecycle scenarios
    cloud-smoke.yml          # federated provider tests
    cleanup.yml              # interrupted-test cleanup
  fixtures/
    pulumi/.reeve/
    terraform/.reeve/
    tofu/.reeve/
  scenarios/
    create-update-delete/
    failures/
    gates-and-locks/
    event-routing/
    large-history/
  bootstrap/                 # one-time buckets and federation
```

- Use independent roots/configurations for the three engines; current validation permits one engine per root.
- Separate Reeve coordination/artifact storage, engine state storage, and infrastructure under test.
- Give each scenario a unique namespace; preview and apply for that scenario intentionally share it.
- Provide `mise` tasks for bootstrap, scenario execution, assertions, cleanup, and failure reproduction.

### Test layers

| Layer | Execution | What it proves |
| --- | --- | --- |
| Local contract tests | Every code PR; fake VCS, injectable clock, filesystem store, HTTP fixtures | Request budgets, routing, gates, CAS retries, retention races, and deterministic failures. |
| Real engines with local state | Every relevant PR; Terraform/OpenTofu contract suite plus new Pulumi subject | Actual CLI arguments, create/update/delete/no-op, saved plans, refresh, error output, and redaction. |
| Optional S3 emulator | Relevant PRs or explicit CI scenario | SDK/HTTP behavior, prefix listing, conditional writes, and lifecycle in one disposable environment. |
| Real S3/GCS/R2 stores | Trusted nightly/candidate/release jobs | Actual storage semantics, auth, pagination, latency, and independent-run lock contention. |
| Consumer GitHub workflows | Candidate releases and manual acceptance | Event payloads, permissions, job skipping, exact checkout, comments, approvals, and reusable workflow integration. |

- Reuse [enginetest](../../internal/iac/enginetest/enginetest.go); add deletion/replacement and a real Pulumi fixture rather than duplicating the existing HCL suite.
- Add a shared blob contract run against every shipped adapter, including Azure for metadata/CAS interface changes.
- Make a missing required binary, backend, or credential fail the relevant configured CI lane rather than silently skip it.
- Test engines and blob adapters independently on PRs; run selected integrated combinations nightly and all supported combinations before release.

### State lifecycle

1. Bootstrap isolated storage and seed a known baseline using the real engine.
2. Change fixture inputs and assert the expected preview counts and artifacts.
3. Apply using the same state and verify the resulting resources and audit records.
4. Preview again and assert no changes.
5. Remove or replace a resource and assert delete/replacement behavior.
6. Inject engine, gate, credential, storage, and cancellation failures.
7. Run cleanup even on failure; a scheduled sweeper removes abandoned test namespaces.

- An emulator can be discarded after a complete scenario; its state must survive all steps within that scenario.
- Separate GitHub preview/apply jobs cannot share a job-local service container by default.
- Use real shared test buckets for separate-event and concurrent-run tests; do not use Actions cache as a shared lock store.
- Keep test resources disposable while preserving enough private diagnostics to reproduce a failure.

### Scenario inventory

- Resource create, update, delete, replace, no-op, multi-stack changes, and partial failure.
- Plan-locking on/off, missing or stale plan, refresh-before-apply, dry-run refresh, and already-applied reruns.
- Preview failure with nonzero exit, successful persistence of its failure manifest, and an accurate PR result.
- Missing approvals, stale approvals, denied CODEOWNERS, failing checks, freeze, draft, and stale HEAD.
- Fork validation, unauthorized command, bot command, malformed prefix/verb, shell metacharacters, and policy/config edits in a PR.
- Concurrent PRs on one stack, concurrent runs of one PR, FIFO promotion, heartbeat renewal, expired holder, and cancelled engine.
- Reaper no-op, orphan queue cleanup, malformed locks, CAS conflicts, ignored conditional writes, and read/write outages.
- Large historical buckets, large PR comment histories, paginated listings, API retries, and unchanged operation counts as stack count grows.
- Object deletion/update during retention, manifest replacement during apply, and reruns with different attempt identities.
- Cold download, warm cache, exact SHA pin, missing release, invalid checksum/signature, wrong-source artifact, and source fallback.
- Two consumers with different prefixes, engines, permissions, and roots using the same reusable workflow release.

### Emulator and cloud choices

- Default to filesystem/HTTP fixtures and cloud-free engine workloads; add an emulator where it proves an SDK contract the local fixtures cannot.
- LocalStack supports ephemeral CI, but current distributions require an authentication token and license selection; make it optional and flag its non-MIT distribution before adopting it. [LocalStack CI](https://docs.localstack.cloud/aws/getting-started/ci-cd/), [plans](https://docs.localstack.cloud/aws/licensing/).
- LocalStack's AWS coverage does not establish GCS generation or R2 compatibility; real backend lanes remain necessary.
- Start live integration with AWS OIDC and GCP WIF using separate test roles, buckets, and prefixes.
- R2 now supports short-lived scoped credentials derived from a parent API token; this is not direct proof of GitHub OIDC federation. [R2 temporary credentials](https://developers.cloudflare.com/r2/api/s3/temporary-credentials/).
- Make a credential path compliant with the repository's no-long-lived-secrets invariant an acceptance prerequisite for the R2 lane; do not copy the current static-key example into new workflows.
- Keep R2 auth discovery bounded; AWS/GCP and local test coverage can ship independently.
- Validate endpoint/path-style configuration through the public bucket config/CLI, not adapter-only test construction.

### Driving real GitHub events

- Use explicit trusted `workflow_dispatch` scenarios for automated lifecycle tests and a separate human-command acceptance checklist.
- Preserve the production bot-command guard: an App-authored `/reeve apply` comment is supposed to be ignored.
- Automated bot-comment tests assert rejection; do not add a production bypass to make the test driver work.
- Include real human-authored command and approval checks in release acceptance until a trusted event-driving design covers them.
- When automating PR creation, account for GitHub token event-suppression behavior and use an explicitly authorized test identity.

## 7. Safe concurrency and further caching

### Preview execution

- Wire `engine.execution.max_parallel_stacks` to a bounded worker pool with CLI parity.
- Keep deterministic summary order, cancellation propagation, per-stack timeouts, and complete error reporting.
- Isolate mutable workspace data before running stacks in the same directory concurrently.
- HCL workspace selection and initialization share `.terraform` data today; use per-stack execution directories/data directories or serialize a shared directory.
- Check Pulumi backend/session files and provider installation for the same shared-directory problem.
- Keep apply sequencing unchanged in the initial concurrency PR.

### Credential reuse

- Measure exchange counts by resolved provider binding first.
- Add reuse only for providers declaring compatible lease semantics, with keys including provider identity, role/account, audience, mode, and scope.
- Keep cached credentials in memory and preserve isolated child-process delivery; never persist the cache.
- Acquire once for concurrent compatible consumers, renew before expiry, and release resources only after the last consumer finishes.
- Do not cache failures or treat absent expiry as proof that a federated credential is reusable.
- Preserve the cleanup guarantees in [credential lifecycle](../../openspec/changes/credential-lifecycle/tasks.md).
- Do not reuse a mutable engine session or backend initialization across differing directories, workspaces, credentials, or backend configuration.

## Delivery checklist and OpenSpec mapping

| PR | Deliverable | Spec/change dependencies | Exit criterion |
| --- | --- | --- | --- |
| 1 | Local stage timings and counting fixtures | Existing blob, locking, PR-flow contracts | Baseline overhead and request counts are reproducible. |
| 2 | Metadata listing, GCS read reduction, efficient lock walkers | New `run-maintenance-efficiency`; blob and locking deltas | Zero pruning content reads; unchanged locks are not written; CAS/race tests pass. |
| 3 | Explicit maintenance and migration | Same maintenance proposal; config and CLI deltas | Preview/apply/refresh avoid global startup sweeps; targeted expiry still works. |
| 4 | Preview snapshots and artifact identity | New `run-data-reuse`; PR-flow/preconditions/blob deltas | One selection per run; reruns cannot substitute plan content; all lookup errors deny apply. |
| 5 | Early action routing | New `github-workflow-efficiency`; PR-flow/config deltas | Event fixture matrix proves zero setup on skips and preserves explicit commands. |
| 6 | Exact-source binary installation | Same workflow proposal; distribution design | SHA-pinned warm/cold paths and hostile artifact cases pass. |
| 7 | Reusable workflow and generated callers | Same workflow proposal; trusted-config dependency | Two consumer configurations pass job, permission, and self-check tests. |
| 8 | Consumer test repository and live backend lanes | New `consumer-conformance`; relevant blob/IaC/PR-flow scenarios | Separate preview/apply events, cross-run locks, and cleanup pass on configured backends. |
| 9 | Bounded previews and opt-in credential leases | New `run-execution-efficiency`; IaC/auth/config deltas | Shared-directory, expiry, cleanup, and cancellation tests pass under the race detector. |

- Every new non-trivial change gets `proposal.md`, `design.md`, `tasks.md`, and capability deltas with MUST/MAY requirements and scenario blocks before implementation.
- The local harness starts in PR 1 and grows alongside each fix; it does not wait for the external test repository.
- Action/lock/auth/precondition PR descriptions explicitly identify their security-sensitive paths.
- Coordinate trusted controls with [trusted-config-source](../../openspec/changes/trusted-config-source/proposal.md) and fork execution with [fork-preview-authorization](../../openspec/changes/fork-preview-authorization/proposal.md).
- Deny credentialed fork execution in the shared workflow until the required trusted-config and worker-isolation boundaries exist.
- Synchronize stale base specs during the corresponding implementation changes; current PR-flow text still says apply never replays saved plans, despite implemented plan locking.
- Run `mise run check` before pushing code, required tagged builds/tests, golden regeneration for rendered changes, and strict OpenSpec validation for proposals.

## Review validation

- Read the supplied review, current implementation, relevant specs, pending security proposals, workflow scripts, examples, and engine contract tests.
- Checked current GitHub Actions, LocalStack, and R2 documentation for platform-dependent planning assumptions.
- Ran the focused existing retention, preview lookup, lock reaper/CAS, action routing, action security, and binary-verification tests through `mise`; all selected tests passed.
- No production bucket timings or live cloud/GitHub workflow scenarios were run during this review.
