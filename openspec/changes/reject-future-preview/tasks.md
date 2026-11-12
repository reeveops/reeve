# reject-future-preview - tasks

- [x] 1. Manifest created_at ahead of the clock beyond maxPreviewClockSkew (2m) rejects the snapshot
- [x] 2. Rejection names the run id, the stamp, the clock, and the re-run fix; the apply fails closed
- [x] 3. Injected-clock tests: +1h rejected; +30s accepted
