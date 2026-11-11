# audit-force-unlock - tasks

- [x] 1. Force-unlock paths record actor, stack, previous holder, promotion, and scope
- [x] 2. Intent entry written write-once BEFORE the mutation; audit write failure refuses the unlock
- [x] 3. Reason and error text pass through redaction before storage
- [x] 4. Test asserts both entries exist, carry the previous holder, and are redacted
