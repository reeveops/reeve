// Package github_issue opens or updates a GitHub issue per drifted stack.
// Issues are identified by a marker in the body; re-runs update instead of
// duplicating. GitHub access goes through notify.IssueClient (implemented
// by internal/vcs/github) - no VCS SDK is imported here (modularity
// contract).
package github_issue

import (
	"context"
	"fmt"
	"strings"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
)

func init() {
	notify.Register("github_issue", New)
}

// Channel manages one issue per drifted stack. PR-flow events are no-ops.
//
// github_issue is intentionally NOT a notify.Grouper: an issue is a per-stack
// incident (one thing to fix and close), so `grouping:` does not apply here -
// it is a no-op, exactly like otel_annotation. Grouping (by_environment)
// affects only slack and webhook, where a combined message reduces noise.
type Channel struct {
	name      string
	issues    notify.IssueClient
	labels    []string
	assignees []string
	events    []notify.Event
}

// New is the registered constructor. Without an issue client (no GitHub
// token/repo in the environment) the channel is skipped, matching the previous
// factory behavior.
func New(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	if deps.Issues == nil {
		return nil, nil
	}
	return &Channel{
		name:      cfg.EffectiveName(),
		issues:    deps.Issues,
		labels:    cfg.Labels,
		assignees: cfg.Assignees,
		// A check_failed subscription implies check_recovered: the issue a
		// failed check opens must close when the check heals, even if the
		// config predates the recovery event.
		events: notify.WithImpliedEvents(notify.ParseEvents(cfg.On)),
	}, nil
}

func (s *Channel) Name() string               { return s.name }
func (s *Channel) Subscribes() []notify.Event { return s.events }

// Deliver maintains independent issues, with distinct markers so they never
// stomp each other:
//   - "reeve:drift:<ref>": drift_detected/ongoing open+update, drift_resolved
//     closes
//   - "reeve:drift-check:<ref>": check_failed opens, check_recovered closes
//
// Sharing one marker (the old behavior) let a check_failed blip overwrite
// a real drift issue's body, and left check-failure issues open forever
// because nothing ever closed them.
func (s *Channel) Deliver(ctx context.Context, p notify.Payload) error {
	if p.Drift == nil {
		return nil
	}
	var marker, title, body string
	closing := p.Event == notify.EventDriftResolved
	switch p.Event {
	case notify.EventCheckFailed, notify.EventCheckRecovered:
		// check_failed/check_recovered live on a separate marker so a check
		// blip can't stomp a real drift issue.
		marker = fmt.Sprintf("<!-- reeve:drift-check:%s -->", p.Drift.Ref())
		title = fmt.Sprintf("drift check failed: %s", p.Drift.Ref())
		body = marker + "\n\n" + renderBody(p)
		closing = p.Event == notify.EventCheckRecovered
	default:
		marker = fmt.Sprintf("<!-- reeve:drift:%s -->", p.Drift.Ref())
		title = fmt.Sprintf("drift: %s", p.Drift.Ref())
		body = marker + "\n\n" + renderBody(p)
	}

	number, found, err := s.issues.FindIssueByMarker(ctx, marker)
	if err != nil {
		return err
	}

	if closing {
		if found {
			return s.issues.CloseIssue(ctx, number, body)
		}
		return nil
	}

	if found {
		return s.issues.UpdateIssue(ctx, number, title, body)
	}
	_, err = s.issues.CreateIssue(ctx, title, body, s.labels, s.assignees)
	return err
}

func renderBody(p notify.Payload) string {
	d := p.Drift
	var b strings.Builder
	switch p.Event {
	case notify.EventCheckFailed:
		fmt.Fprintf(&b, "## Drift check failed on `%s`\n\n", d.Ref())
		fmt.Fprintf(&b, "- **Env:** %s\n", d.Env)
		fmt.Fprintf(&b, "- **Event:** %s\n", p.Event)
		if d.Error != "" {
			fmt.Fprintf(&b, "\n```\n%s\n```\n", d.Error)
		}
	case notify.EventCheckRecovered:
		fmt.Fprintf(&b, "## Drift check recovered on `%s`\n\n", d.Ref())
		fmt.Fprintf(&b, "- **Env:** %s\n", d.Env)
		fmt.Fprintf(&b, "- **Event:** %s\n", p.Event)
	default:
		fmt.Fprintf(&b, "## Drift detected on `%s`\n\n", d.Ref())
		fmt.Fprintf(&b, "- **Env:** %s\n", d.Env)
		fmt.Fprintf(&b, "- **Event:** %s\n", p.Event)
		fmt.Fprintf(&b, "- **Changes:** +%d ~%d -%d ±%d\n",
			d.Add, d.Change, d.Delete, d.Replace)
		if d.PlanSummary != "" {
			fmt.Fprintf(&b, "\n```\n%s\n```\n", d.PlanSummary)
		}
	}
	fmt.Fprintf(&b, "\n_run:_ `%s`\n", d.RunID)
	return b.String()
}
