# verify-cached-binary

## Why

A restored binary cache entry went straight to use: the cache KEY binds the
entry to the action source, but nothing bound the restored FILE to the bytes
that were hashed when it was saved. A corrupted or tampered entry (the review
noted cached-binary poisoning as practical) would execute.

## What

The save path records a sha256 sidecar (`reeve.sha256`) next to the binary,
and the cache stores the whole directory. A restore runs a verification step:
the restored binary must match the sidecar. On a mismatch (or an incomplete
entry: either file missing), the restored entry is deleted and the run
rebuilds from source - the visible line says what failed, the recorded hash,
the actual hash, and what happens next. All rebuild-path steps also run when
verification failed (`cache-hit != true || verified != true`), so a rejected
entry always rebuilds and never executes.

## Scope

- `.github/actions/reeve/action.yml`: restore path, verify step, record step,
  save path, and the rebuild conditions.

## Impact

- No config change.
- A cache entry written by earlier reeve versions (no sidecar) is treated as
  incomplete and rebuilt once.
