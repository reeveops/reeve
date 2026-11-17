# approved-notification-after-gates - tasks

- [x] 1. Evaluate all stack gates before normal `approved`/`applying` events and list only passing stacks
- [x] 2. Send `break_glass` at authorization even when every stack is denied; send no `approved`/`applying` for that outcome
- [x] 3. Test fully blocked normal and break-glass runs, the passing control, and partial-pass announcement contents
- [x] 4. Heartbeat each acquired gate-phase lock until its apply completes or is refused
- [x] 5. Test that an earlier apply outliving the lease cannot expose a later stack's lock to another PR
