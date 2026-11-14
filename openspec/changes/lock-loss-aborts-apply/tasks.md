# lock-loss-aborts-apply - tasks

- [x] 1. Add the refresh-only lease transition (`corelocks.RefreshLease`) that cannot install a holder or enqueue
- [x] 2. Heartbeat refreshes via it; a lost lease reports the observed state once and stops refreshing
- [x] 3. Apply cancels the engine on loss (SIGINT + grace) and fails the stack with the abort message
- [x] 4. Refresh aborts the same way
- [x] 5. Run test: force-unlock mid-apply cancels the engine and fails the stack; lock not retaken
