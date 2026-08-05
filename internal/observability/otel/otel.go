// Package otel wires OpenTelemetry traces + metrics. Opt-in: callers
// only construct a Provider if observability.yaml has otel.enabled=true.
// Zero-cost when disabled - all helpers accept a nil provider.
package otel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/FynxLabs/reeve/internal/core/envref"
)

// CardinalityMode controls the stack label.
type CardinalityMode string

const (
	CardinalityAllow CardinalityMode = "allow"
	CardinalityHash  CardinalityMode = "hash"
	CardinalityDrop  CardinalityMode = "drop"
)

// Options carries constructor inputs.
type Options struct {
	Endpoint         string
	ServiceName      string
	ResourceAttrs    map[string]string
	Headers          map[string]string
	StackCardinality CardinalityMode
}

// Provider bundles the tracer + meter + shutdown.
type Provider struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider

	tracer trace.Tracer
	meter  metric.Meter

	cardinality CardinalityMode

	// Named instruments.
	runCounter       metric.Int64Counter
	stackDuration    metric.Float64Histogram
	preconFailed     metric.Int64Counter
	policyViolations metric.Int64Counter
	stackChanges     metric.Int64Counter

	// Drift instruments.
	driftDetections    metric.Int64Counter
	driftDuration      metric.Float64Histogram
	driftRuns          metric.Int64Counter
	driftStacksInDrift metric.Int64Gauge
	driftOngoingHours  metric.Float64Gauge
}

// New constructs a Provider. Callers close via Shutdown.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.ServiceName == "" {
		opts.ServiceName = "reeve"
	}
	if opts.StackCardinality == "" {
		opts.StackCardinality = CardinalityHash
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
	}
	for k, v := range opts.ResourceAttrs {
		attrs = append(attrs, attribute.String(k, envref.Expand(v)))
	}
	res, err := resource.New(ctx, resource.WithAttributes(attrs...))
	if err != nil {
		return nil, err
	}

	traceOpts := []otlptracehttp.Option{}
	metricOpts := []otlpmetrichttp.Option{}
	if ep := opts.Endpoint; ep != "" {
		ep = envref.Expand(ep)
		traceOpts = append(traceOpts, otlptracehttp.WithEndpointURL(ep))
		metricOpts = append(metricOpts, otlpmetrichttp.WithEndpointURL(ep))
	}
	if len(opts.Headers) > 0 {
		h := map[string]string{}
		for k, v := range opts.Headers {
			h[k] = envref.Expand(v)
		}
		traceOpts = append(traceOpts, otlptracehttp.WithHeaders(h))
		metricOpts = append(metricOpts, otlpmetrichttp.WithHeaders(h))
	}

	traceExp, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	tracer := tp.Tracer("github.com/FynxLabs/reeve")
	meter := mp.Meter("github.com/FynxLabs/reeve")

	p := &Provider{
		tp:          tp,
		mp:          mp,
		tracer:      tracer,
		meter:       meter,
		cardinality: opts.StackCardinality,
	}

	p.runCounter, _ = meter.Int64Counter("reeve.runs.total")
	p.stackDuration, _ = meter.Float64Histogram("reeve.stack.duration")
	p.preconFailed, _ = meter.Int64Counter("reeve.preconditions.failed")
	p.policyViolations, _ = meter.Int64Counter("reeve.policy.violations")
	p.stackChanges, _ = meter.Int64Counter("reeve.stack.changes")

	p.driftDetections, _ = meter.Int64Counter("reeve.drift.detections.total")
	p.driftDuration, _ = meter.Float64Histogram("reeve.drift.duration")
	p.driftRuns, _ = meter.Int64Counter("reeve.drift.runs.total")
	p.driftStacksInDrift, _ = meter.Int64Gauge("reeve.drift.stacks_in_drift")
	p.driftOngoingHours, _ = meter.Float64Gauge("reeve.drift.ongoing_duration")

	return p, nil
}

// Shutdown flushes and closes.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	if err := p.tp.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := p.mp.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("otel shutdown: %v", errs)
	}
	return nil
}

// StartRunSpan starts the root span for a run.
func (p *Provider) StartRunSpan(ctx context.Context, op string, pr int, sha string) (context.Context, func(outcome string)) {
	if p == nil {
		return ctx, func(string) {}
	}
	ctx, span := p.tracer.Start(ctx, "reeve.run",
		trace.WithAttributes(
			attribute.String("reeve.op", op),
			attribute.Int("pr.number", pr),
			attribute.String("commit.sha", sha),
		),
	)
	return ctx, func(outcome string) {
		span.SetAttributes(attribute.String("outcome", outcome))
		p.runCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("op", op),
				attribute.String("outcome", outcome),
			),
		)
		span.End()
	}
}

// StartStackSpan starts a child span for a stack.
func (p *Provider) StartStackSpan(ctx context.Context, project, stack, env, op string) (context.Context, func(outcome string, durationSec float64)) {
	if p == nil {
		return ctx, func(string, float64) {}
	}
	attrs := []attribute.KeyValue{
		attribute.String("project", project),
		attribute.String("env", env),
		attribute.String("op", op),
	}
	if stackAttr := p.stackLabel(project, stack); stackAttr != "" {
		attrs = append(attrs, attribute.String("stack", stackAttr))
	}
	ctx, span := p.tracer.Start(ctx, "reeve.stack."+op, trace.WithAttributes(attrs...))
	return ctx, func(outcome string, durationSec float64) {
		span.SetAttributes(attribute.String("outcome", outcome))
		p.stackDuration.Record(ctx, durationSec, metric.WithAttributes(append(attrs, attribute.String("outcome", outcome))...))
		span.End()
	}
}

// RecordPreconditionFailure increments the failed-precondition counter.
func (p *Provider) RecordPreconditionFailure(ctx context.Context, gate string) {
	if p == nil {
		return
	}
	p.preconFailed.Add(ctx, 1, metric.WithAttributes(attribute.String("gate", gate)))
}

// RecordPolicyViolation increments the policy-violation counter.
func (p *Provider) RecordPolicyViolation(ctx context.Context, policyName string) {
	if p == nil {
		return
	}
	p.policyViolations.Add(ctx, 1, metric.WithAttributes(attribute.String("policy_name", policyName)))
}

// RecordStackChanges emits counts per change type. Cardinality is
// controlled by the configured mode.
func (p *Provider) RecordStackChanges(ctx context.Context, project, stack string, add, change, del, repl int) {
	if p == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("project", project)}
	if s := p.stackLabel(project, stack); s != "" {
		attrs = append(attrs, attribute.String("stack", s))
	}
	record := func(kind string, n int) {
		if n == 0 {
			return
		}
		p.stackChanges.Add(ctx, int64(n), metric.WithAttributes(append(attrs, attribute.String("type", kind))...))
	}
	record("add", add)
	record("change", change)
	record("delete", del)
	record("replace", repl)
}

// RecordDriftDetection emits a detection event (counter) with labels.
func (p *Provider) RecordDriftDetection(ctx context.Context, project, stack, env, outcome string) {
	if p == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("env", env),
		attribute.String("outcome", outcome),
	}
	if s := p.stackLabel(project, stack); s != "" {
		attrs = append(attrs, attribute.String("stack", s))
	}
	p.driftDetections.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordDriftDuration records one stack's drift-check duration (histogram).
func (p *Provider) RecordDriftDuration(ctx context.Context, project, stack, env string, seconds float64) {
	if p == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("env", env)}
	if s := p.stackLabel(project, stack); s != "" {
		attrs = append(attrs, attribute.String("stack", s))
	}
	p.driftDuration.Record(ctx, seconds, metric.WithAttributes(attrs...))
}

// RecordDriftRun emits the run-level outcome counter.
func (p *Provider) RecordDriftRun(ctx context.Context, outcome string) {
	if p == nil {
		return
	}
	p.driftRuns.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordStacksInDrift sets the per-env gauge for currently-drifted stacks.
func (p *Provider) RecordStacksInDrift(ctx context.Context, env string, count int64) {
	if p == nil {
		return
	}
	p.driftStacksInDrift.Record(ctx, count, metric.WithAttributes(attribute.String("env", env)))
}

// RecordOngoingDuration sets the "how long has this stack been drifted"
// gauge. hours is fractional.
func (p *Provider) RecordOngoingDuration(ctx context.Context, project, stack string, hours float64) {
	if p == nil {
		return
	}
	attrs := []attribute.KeyValue{}
	if s := p.stackLabel(project, stack); s != "" {
		attrs = append(attrs, attribute.String("stack", s))
	}
	p.driftOngoingHours.Record(ctx, hours, metric.WithAttributes(attrs...))
}

// stackLabel applies the cardinality gate.
func (p *Provider) stackLabel(project, stack string) string {
	switch p.cardinality {
	case CardinalityAllow:
		return project + "/" + stack
	case CardinalityDrop:
		return ""
	default: // hash
		h := sha256.Sum256([]byte(project + "/" + stack))
		return hex.EncodeToString(h[:8])
	}
}
