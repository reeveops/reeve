# comment-approval-ignore-edited - tasks

- [x] 1. `commentApprovals` refuses approval comments whose `updated_at` differs from `created_at`
- [x] 2. `Approval.Note` carries the source-authored refusal; Evaluate renders it and skips the approval
- [x] 3. Table test: unedited counts; edited does not; repost after edit counts; edit after approval supersedes
