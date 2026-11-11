# blob-read-consistency - tasks

- [x] 1. Add a concurrent writer/reader test to `internal/blob/filesystem` asserting body and version always match (fails before the fix)
- [x] 2. Take the shard lock in `filesystem.Store.Get` around open, stat, and version resolution
