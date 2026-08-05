<!-- reeve:pr-comment:v1 -->
## 🔴 reeve · apply · [run #99](https://example.com/runs/99) · [commit deadbee]

**3 stacks applied** · ⏱ 120s

| Stack | Env | ➕ Add | 🔄 Change | ➖ Delete | 🔁 Replace | Duration | Status |
|---|---|---|---|---|---|---|---|
| worker/prod | prod | 0 | 0 | 0 | 0 | 12s | 🔴 failed |
| api/staging | staging | 0 | 0 | 0 | 0 |  | 🔒 blocked by #501 |
| api/prod | prod | 2 | 1 | 0 | 0 | 47s | ✅ applied |

---

### worker/prod · prod · 🔴 failed

  **Error:** aws:rds: Permission denied

---

### api/staging · staging · 🔒 blocked by #501

---

### api/prod · prod · ✅ applied

<details><summary>Summary (2 add, 1 change, 0 delete, 0 replace)</summary>

+ s3 bucket
~ iam role

</details>

