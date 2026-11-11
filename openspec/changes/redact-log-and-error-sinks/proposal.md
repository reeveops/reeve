# redact-log-and-error-sinks

## Why

Redaction was producer-opt-in: some engine output was redacted at the call
site, but the log handler had no redaction at all, apply manifest persistence
errors reached the job log, timeline, PR comment, and the returned run error
raw, and break-glass justifications flowed into every sink unfiltered. That
violates the stated invariant: no output path bypasses `internal/core/redact`.

## What

- The log handler (text and JSON, both split and direct writers) passes every
  record's message and string attribute values through the invocation-scoped
  redactor. Runs install their redactor at start; a secret registered for one
  stack masks in later log lines too.
- The apply run uses ONE invocation redactor for every sink: the manifest
  persistence error is redacted once and used for the job log, timeline event,
  PR comment warning, and the returned run error.
- Break-glass justifications pass through the redactor at each sink (log
  attribute, timeline detail, audit entry, rendered note). The justification
  stays readable; only credential-shaped values inside it are masked.

## Scope

- `internal/log`: redacting sink wrapper + `SetRedactor`.
- `internal/run/apply.go`: one invocation redactor; the manifest-error and
  justification sinks.

## Impact

- No config change.
- Output text may change slightly where a credential-shaped value used to
  leak; masked values render as `[redacted]`.
