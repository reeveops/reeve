# Engine error detail

## Why

A failed apply reported one line. Both engine adapters passed their failure
message through `firstLine` before storing it, so everything after the first
newline was discarded on the way to the PR comment.

For Pulumi the loss was total. `pulumi up --json` writes engine events to
stdout, so a failed apply usually leaves only a generic wrapper line on
stderr while the resource-level cause arrives as a diagnostic event.
`parseApply` decoded those diagnostic events and then dropped them on the
floor, returning a hardcoded empty error string. The operator saw a wrapper
line, or nothing.

Policy hooks had the same shape: the reported error was the process error
("exit status 1") with the hook's own stderr held in the result and never
rendered.

## What

- Return error diagnostics from the Pulumi event stream instead of discarding
  them. Prefer them over the process error, which names no resource.
- Stop truncating engine failure messages to their first line, in apply,
  refresh, and workspace enumeration, for both adapters.
- Report a policy hook's stderr as its error, and render both streams on
  failure.
- Render a multi-line error in a fenced block. Single-line errors render
  exactly as before.

## Scope

Error capture in the IaC adapters and policy hooks, and how an error renders
in the PR comment. No change to how failures are classified, to exit status,
or to which surfaces receive errors.

## Compatibility

PR comments for failing stacks get longer. The comment-level size budget
already degrades progressively, so a large error consumes the space that would
have shown a diff for a stack that failed anyway.

Fenced error text is escaped so a ``` run in engine output cannot terminate
the block. Every error path continues through `internal/core/redact`.
