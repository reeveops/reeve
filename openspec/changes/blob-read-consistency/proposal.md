# blob-read-consistency

## Why

`filesystem.Store.Get` opened the object and read its generation sidecar
outside the shard lock, while `writeAtomic` publishes a new generation before
renaming the new object into place. A read interleaved between those two steps
returned the old body with the new version. A caller could then `PutIfMatch`
with that version and install a transition derived from the stale body,
defeating the read-modify-write guarantee the lock state machine is built on.

## What

`Get` takes the same shard lock writers hold for the whole read: open, stat,
and version resolution. The returned body and version are always one published
pair. Reads of missing keys fail closed as before (`ErrNotFound`).

## Scope

- `internal/blob/filesystem` only.
- Shared blob contract gains a read-under-concurrent-write scenario.

## Impact

- No config change.
- Breaks nothing for users; reads of the filesystem store now serialize with
  writes on the same bounded shard set.
