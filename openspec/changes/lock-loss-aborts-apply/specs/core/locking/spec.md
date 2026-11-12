# Locking - deltas

## MODIFIED Requirements

### Requirement: A lock held by an apply never lapses on its own

Once an apply has STARTED (the hold is marked persistent), the lock is
released only by that run's successful completion or an explicit unlock:

- Lease expiry MUST NOT free the hold or let another PR acquire; the reaper
  MUST leave started holds untouched. Queue fairness is unaffected: blocked
  PRs queue behind the hold.
- Heartbeat refresh failures MUST NOT free the hold. They MUST be logged
  with the stack and the last successful refresh time. Heartbeat ownership
  loss (an explicit third-party unlock) remains the one abort path and keeps
  the existing interruption behavior.
- A same-PR retry MUST be refused (ErrHeldBySamePR) until the run releases
  or an explicit unlock clears the hold - lease-expiry adoption no longer
  applies to started holds.

The engine MUST NOT start until the hold is marked persistent: if marking
the hold fails, the apply MUST refuse before any engine call - the stack
errors with the reason, the acquired lock is released, and nothing is
applied.

A runner killed between hold start and outcome recording leaves a hold with
NO recorded outcome. Every surface showing the holder MUST render that state
explicitly: hold started, lease expired at T, no outcome recorded, the run
most likely died (runner killed), and that the stack's real state MUST be
validated before unlocking - covering `reeve locks` list and explain, the
other PR's blocked message, the refresh blocked message, the admin
force-unlock output, and the PR-scoped unlock path with and without
`--force`.

When the apply FAILS, runs PARTIALLY, or is INTERRUPTED, the lock stays held
and MUST record the reason: the holder PR, the recorded outcome
(`failed` | `partial` | `interrupted`), the time, the run URL, and the audit
record key. The record is retried within the detached deadline; if it still
fails, the run MUST log that the lock remains held with no recorded reason
and give the run URL (it MUST NOT claim the lock reverted to lease
semantics). Every surface meeting that lock MUST show the reason - another
PR's lock-gate refusal, the same-PR retry refusal, the `reeve locks`
list/explain output, and the force-unlock output all name the holder PR, the
outcome, and the pointers (run URL, audit record), and state that the
stack's real state MUST be validated before the lock is cleared. The
PR-scoped unlock paths (`UnlockPR` with and without `--force`) MUST word the
refusal by the recorded state and show the removed hold's status alongside
the removal. A failed or partial apply MUST log at error level, comment on
the PR, and add the held-lock fact with its pointers to the run's timeline
entry for the failed stack - all naming the exact command that clears the
hold once the state is validated.

#### Scenario: A failed apply leaves the lock held with its reason

- **WHEN** an apply's engine call fails
- **THEN** the lock stays held by the failing run with outcome `failed`
  (or `partial` when earlier stacks succeeded), the run URL, the audit
  record key, and a timestamp
- **AND** the run logs at error level and comments on the PR naming the
  stack, the failure, that the lock is still held and why, and where the
  full detail is
- **AND** the lock is not re-taken or freed by the failing run

#### Scenario: Expiry and heartbeat failure never free a started hold

- **GIVEN** an apply that started its hold and a heartbeat whose every
  refresh fails (a CAS outage)
- **WHEN** far more than the lease duration passes
- **THEN** a second PR queues behind the held lock instead of acquiring
- **AND** the reaper leaves the started hold
- **AND** the heartbeat failure is logged with the stack and the last
  successful refresh time

#### Scenario: Everyone meeting the hold sees the reason

- **GIVEN** a lock held with a recorded outcome
- **WHEN** another PR's lock gate refuses, or `reeve locks` renders the
  lock, or the force-unlock path overrides it
- **THEN** each surface states the holder PR, the recorded outcome, and the
  run URL / audit record key
- **AND** each states that the stack's real state must be validated before
  the lock is cleared

#### Scenario: A hold that cannot be marked prevents the engine

- **WHEN** marking the apply hold persistent fails for a stack
- **THEN** the engine is not started and nothing is applied
- **AND** the stack errors saying the hold could not be marked and that
  nothing ran
- **AND** the acquired lock is released

#### Scenario: A runner-killed hold renders the ambiguous state

- **GIVEN** a hold that started with no recorded outcome and an expired
  lease
- **WHEN** the lock is displayed or encountered: `reeve locks` list/explain,
  another PR's blocked message, the refresh blocked message, the admin
  force-unlock output, the PR-scoped unlock path with and without `--force`
- **THEN** each renders: hold started, lease expired, no outcome recorded,
  the run most likely died, validate the stack before unlocking

#### Scenario: A same-PR retry refusal carries the reason and the command

- **GIVEN** an earlier run of the PR whose hold recorded outcome `failed`
- **WHEN** another run of the same PR reaches the lock gate
- **THEN** the refusal shows the recorded outcome, the run URL, and the
  audit record key
- **AND** it names the exact command that clears the hold once the stack's
  real state is validated

#### Scenario: Force-unlock mid-apply still aborts

- **GIVEN** an apply holding a stack with a live heartbeat
- **WHEN** the lock is force-unlocked mid-apply
- **THEN** the heartbeat reports the loss naming the observed state
- **AND** the engine context is cancelled and the stack fails with an abort
  message that states the lock was lost, when, the observed state, that
  resources may be partially applied, and that `/reeve preview` shows the
  current state
- **AND** the aborted operation leaves the lock in the state the unlock
  produced and does not re-acquire it
