<!-- reeve:explain:3f9a1c2 -->
### 🔎 reeve · explain · [run](https://example.com/runs/12) · commit 3f9a1c2

Report-only: nothing below ran an engine, took a lock, or wrote state.

---

#### api/prod

**Approval rules**

- required approvals: 2 (have 1 of 2)
- approvers: @dana, @sam
- missing: @sam
- require_all_groups: false · codeowners: false · dismiss_on_new_commit: true

**Lock**

- held by PR #481 (run 4817, acquired 2026-08-06T12:10:02Z, expires 2026-08-06T16:10:02Z)
- queue: #482 → #490

🔐 api/prod apply gates:
  ✅ up_to_date: branch up-to-date with base
  ❌ approvals: approvals not satisfied
  ❌ lock_acquirable: blocked by lock held by PR #481

---

#### api/staging

**Approval rules**

- required approvals: 1 (have 1 of 1)
- approvers: @dana, @sam
- require_all_groups: false · codeowners: false · dismiss_on_new_commit: false

**Lock**

- free

🔐 api/staging apply gates:
  ✅ approvals: approvals satisfied
  ✅ lock_acquirable: lock is acquirable

---

_`/reeve explain [project/stack]` re-renders this comment. Gates are evaluated now; a later push or approval changes the answer._
