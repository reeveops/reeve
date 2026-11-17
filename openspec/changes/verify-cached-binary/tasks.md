# verify-cached-binary - tasks

- [x] 1. Cache downloaded release archives per resolved tag, never extracted binaries or local hash sidecars
- [x] 2. Fetch and signature-verify live checksums on every run before using a cached or fresh archive
- [x] 3. Hard-fail only a digest mismatch; soft-fallback with a reason for all other verification failures and non-executable binaries
- [x] 4. Never cache source builds or binaries that fail `reeve --version`
- [x] 5. Save a newly resolved tag under its own immutable cache key even when a restore supplied an earlier tag
- [x] 6. Script tests cover cold/cached/tampered archives, every fallback policy row, tag changes, and action cache conditions
