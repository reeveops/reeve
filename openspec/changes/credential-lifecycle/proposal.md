# Credential lifecycle cleanup

## Why

Credential providers can create temporary resources such as GCP WIF files.
Partial acquisition and drift runs currently leave those resources behind.

## What

- Unwind acquired credentials when a later provider fails.
- Give each drift credential binding an explicit cleanup owner.
- Clean old credentials before an auth-expiry rebind.
- Cover successful, failed, and rebound lifecycles with tests.

## Scope

This change closes credential cleanup gaps in auth acquisition and drift.
Worker isolation and fork authorization remain separate changes.
