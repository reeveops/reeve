# dequeue-on-gate-block - tasks

- [x] 1. Gate-blocked, queued, non-holder PRs remove their lock queue entry
- [x] 2. Only lock-gate blocks keep the deliberate queue entry
- [x] 3. The gate-blocked comment carries the removal note and the re-apply step; failed removals report the error
- [x] 4. Run test: blocked PR absent from the queue; next PR promoted on holder release
