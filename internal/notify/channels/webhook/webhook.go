// Package webhook is the generic webhook channel. Ships `raw` format only
// (event payload as JSON). Named presets are out-of-scope until a user
// provides a real target payload.
package webhook

import (
	"context"
	"encoding/json"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
)

func init() {
	notify.Register("webhook", New)
}

// Channel publishes events as HTTP POST with an optional header map.
// Deliveries retry with backoff on 5xx/network errors (notify.PostJSON).
type Channel struct {
	name     string
	url      string
	headers  map[string]string
	events   []notify.Event
	format   string // "raw" (default) - future: "incident_io" | "opsgenie" | ...
	grouping string
	client   notify.HTTPDoer
}

// New is the registered constructor.
func New(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	return &Channel{
		name:     cfg.EffectiveName(),
		url:      cfg.URL,
		headers:  cfg.Headers,
		events:   notify.ParseEvents(cfg.On),
		format:   cfg.Payload.Format,
		grouping: cfg.Grouping,
		client:   deps.HTTP,
	}, nil
}

func (s *Channel) Name() string               { return s.name }
func (s *Channel) Subscribes() []notify.Event { return s.events }

// GroupingMode implements notify.Grouper: drift payloads are batched per the
// configured `grouping:` value (none | by_environment). A grouped POST carries
// a `stacks` array plus the `group` key instead of top-level stack fields.
func (s *Channel) GroupingMode() string { return s.grouping }

func (s *Channel) Deliver(ctx context.Context, p notify.Payload) error {
	body, err := json.Marshal(payloadJSON(p))
	if err != nil {
		return err
	}
	return notify.PostJSON(ctx, s.client, "webhook "+s.name, s.url, s.headers, body)
}

// payloadJSON keeps the drift wire format byte-for-byte compatible with the
// previous drift-only channel; PR-flow events get an analogous raw shape.
func payloadJSON(p notify.Payload) map[string]any {
	if p.Drift != nil {
		if len(p.Group) > 0 {
			return groupedDriftJSON(p)
		}
		d := p.Drift
		return map[string]any{
			"event":   p.Event,
			"project": d.Project,
			"stack":   d.Stack,
			"env":     d.Env,
			"outcome": d.Outcome,
			"counts": map[string]int{
				"add":     d.Add,
				"change":  d.Change,
				"delete":  d.Delete,
				"replace": d.Replace,
			},
			"fingerprint": d.Fingerprint,
			"error":       d.Error,
			"run_id":      d.RunID,
		}
	}
	pr := p.PR
	stacks := make([]map[string]any, 0, len(pr.Stacks))
	for _, st := range pr.Stacks {
		stacks = append(stacks, map[string]any{
			"project": st.Project,
			"stack":   st.Stack,
			"env":     st.Env,
			"status":  st.Status,
			"counts": map[string]int{
				"add":     st.Add,
				"change":  st.Change,
				"delete":  st.Delete,
				"replace": st.Replace,
			},
		})
	}
	return map[string]any{
		"event":      p.Event,
		"pr":         pr.PR,
		"repo":       pr.RepoFull,
		"title":      pr.Title,
		"author":     pr.Author,
		"commit_sha": pr.CommitSHA,
		"run_url":    pr.RunURL,
		"stacks":     stacks,
	}
}

// groupedDriftJSON is the wire shape for a grouped drift delivery: the group
// key (environment) plus a `stacks` array, one entry per member.
func groupedDriftJSON(p notify.Payload) map[string]any {
	stacks := make([]map[string]any, 0, len(p.Group))
	runID := ""
	for _, d := range p.Group {
		if runID == "" {
			runID = d.RunID
		}
		stacks = append(stacks, map[string]any{
			"project": d.Project,
			"stack":   d.Stack,
			"env":     d.Env,
			"outcome": d.Outcome,
			"counts": map[string]int{
				"add":     d.Add,
				"change":  d.Change,
				"delete":  d.Delete,
				"replace": d.Replace,
			},
			"fingerprint": d.Fingerprint,
			"error":       d.Error,
		})
	}
	return map[string]any{
		"event":  p.Event,
		"group":  p.GroupKey,
		"stacks": stacks,
		"run_id": runID,
	}
}
