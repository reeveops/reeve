package run

import (
	"context"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/observability/annotations"
	reeveotel "github.com/FynxLabs/reeve/internal/observability/otel"
)

// BuildOTEL returns an OTEL provider if observability.yaml has
// otel.enabled=true. Nil is safe - all provider helpers are nil-tolerant.
func BuildOTEL(ctx context.Context, cfg *schemas.Observability) (*reeveotel.Provider, error) {
	if cfg == nil || !cfg.OTEL.Enabled {
		return nil, nil
	}
	return reeveotel.New(ctx, reeveotel.Options{
		Endpoint:         cfg.OTEL.Endpoint,
		ServiceName:      cfg.OTEL.ServiceName,
		ResourceAttrs:    cfg.OTEL.ResourceAttrs,
		Headers:          cfg.OTEL.Headers,
		StackCardinality: reeveotel.CardinalityMode(cfg.OTEL.StackCardinality),
	})
}

// BuildAnnotationEmitters returns configured annotation emitters.
func BuildAnnotationEmitters(cfg *schemas.Observability) []annotations.Emitter {
	return annotations.Build(cfg)
}

// PostAnnotation dispatches an event across all emitters. Errors are
// swallowed (annotations are auxiliary).
func PostAnnotation(ctx context.Context, emitters []annotations.Emitter, t annotations.EventType, project, stack, env, outcome, message string, pr int, sha string) {
	if len(emitters) == 0 {
		return
	}
	e := annotations.Event{
		Type: t, When: time.Now(),
		Project: project, Stack: stack, Env: env,
		PR: pr, CommitSHA: sha,
		Outcome: outcome, Message: message,
	}
	_ = annotations.Dispatch(ctx, emitters, e)
}
