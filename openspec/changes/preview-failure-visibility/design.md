# Design

## Exit status

`PreviewOutput` carries `Failed`, `FailedStacks`, and `CommentPosted`.
`Preview` returns both the output and an error when any stack failed, so the
caller keeps the rendered comment and artifacts while still failing the run.
`runPreview` returns that error after printing, and only claims a posted
comment when `CommentPosted` is true.

The dead `planSucceeded` helper is replaced by `previewFailedRefs`, which
returns failed stack refs in run order and is consumed by `Preview`.

## Logging

Per-stack failures log at ERROR because they change the run outcome. Stack
start and finish log at INFO because a sequential multi-stack preview is
otherwise unattributable. Setup timings log at DEBUG: they matter when
diagnosing a slow run, not on every run.

Error text logged is the redacted string already computed for the summary, so
the log path carries no redaction bypass.

## Log level reporting

Precedence is flag > env > config. The loader only knows the config file's
value, so it reports it as `config_log_level`. `log.FromConfig` emits the
effective level, format, and precedence source after installing the logger.
`Format` gains a `String()` method so the format reports as a name.
