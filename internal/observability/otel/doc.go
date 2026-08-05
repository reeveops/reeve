// Package otel emits OpenTelemetry traces and metrics.
// Opt-in: off unless observability.yaml exists. Honors standard
// OTEL_EXPORTER_OTLP_* env vars. Stack-name label cardinality is gated
// via observability.otel.stack_cardinality config (default: hash).
package otel
