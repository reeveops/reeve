# Tasks

## Implementation

- [x] Log per-stack preview failures at ERROR with stack ref and redacted error.
- [x] Add `Failed`, `FailedStacks`, and `CommentPosted` to `PreviewOutput`.
- [x] Return an error from `Preview` when any stack failed, alongside the output.
- [x] Replace dead `planSucceeded` with `previewFailedRefs` and consume it.
- [x] Gate the `posted preview comment` line on `CommentPosted`.
- [x] Bracket each stack with start and finish lines at INFO.
- [x] Time blob open, engine construction, auth registry build, prune, VCS client.
- [x] Report effective log level, format, and precedence source.
- [x] Rename the loader's log fields to `config_log_level` / `config_log_format`.
- [x] Add `Format.String()`.

## Verification

- [x] `previewFailedRefs` unit test replaces the `planSucceeded` test.
- [x] `mise run check` green.
