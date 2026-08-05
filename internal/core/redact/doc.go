// Package redact is the central secret-redaction pipeline. Every user-visible
// output path (PR comment render, audit log, run artifacts, telemetry, policy
// hook stdout) funnels through it. Honors Pulumi [secret] markers plus
// user-configurable redaction regexes.
package redact
