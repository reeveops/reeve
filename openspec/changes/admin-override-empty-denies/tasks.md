# admin-override-empty-denies - tasks

- [x] 1. Empty (or unset) `locking.admin_override.allowed` denies the admin force-unlock
- [x] 2. The refusal names the stack, the rule, and the fix, before any mutation
- [x] 3. Listed actors still pass; PR-scoped removal is not admin-gated
- [x] 4. Table-style tests: empty list denies; listed actor passes; PR releasing its own lock unaffected
