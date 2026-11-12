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

When the apply FAILS, runs PARTIALLY, or is INTERRUPTED, the lock stays held
and MUST record the reason: the holder PR, the recorded outcome
(`failed` | `partial` | `interrupted`), the time, the run URL, and the audit
record key. Every surface meeting that lock MUST show the reason: another
PR's lock-gate refusal, the `reeve locks` list/explain output, and the
force-unlock output - all name the holder PR, the outcome, and the pointers
(run URL, audit record), and state that the stack's real state MUST be
validated before the lock is cleared. A failed or partial apply MUST log at
error level and comment on the PR naming the stack, the failure, that the
lock is still held and why, and where the full detail is.

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
