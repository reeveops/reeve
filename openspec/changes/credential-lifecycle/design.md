# Design

## Partial acquisition

`Registry.AcquireAll` cleans credentials in reverse acquisition order when a later provider fails.
The returned error preserves both the acquisition failure and any cleanup failures.

## Drift ownership

`AuthResolver` returns the environment and its cleanup callback.
The drift attempt defers the active callback and replaces it only after cleaning the expired credential set.

## Failure behavior

Credential cleanup remains best-effort after completed work and logs provider cleanup errors.
Acquisition rollback errors are returned because the requested credential set was never usable.
