# Preview failure visibility

## Why

A preview whose stacks all failed reported success. The per-stack error paths
in `runPreviewOne` returned an error-status summary with no log line, the
`planSucceeded` helper that would have caught it was dead code, and the
command printed `posted preview comment` whenever `run.Preview` returned a nil
error, including when no comment was posted.

Runs were also opaque between startup and the first stack: config load, blob
open, engine construction, and federated credential exchange emitted nothing,
so multi-minute setup showed as a silent gap against the preview timeout.

## What

- Log every per-stack preview failure at ERROR with the stack ref and the
  redacted error.
- Exit nonzero when any stack fails preview, while still posting the comment
  and writing artifacts.
- Report whether a PR comment was actually posted, rather than inferring it
  from the absence of an error.
- Bracket each stack with start and finish lines carrying status and duration.
- Time blob store open, engine construction, auth registry build, artifact
  prune, and VCS client construction.
- Report the effective log level and its precedence source; stop presenting
  the config file's `log_level` field as the level in force.

## Scope

Observability and exit status for preview. No change to what preview plans,
what it renders, or how it gates apply.

## Compatibility

CI steps that treated a failed preview as success now see a nonzero exit. That
is the intended correction: a preview that could not plan every affected stack
is not a successful preview.
