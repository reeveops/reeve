// Package slack is the Slack notification channel. One implementation serves
// both producers: drift events post dashboard-style messages; PR-flow events
// drive the per-PR message lifecycle (upsert + thread timeline) with state
// persisted in the blob store.
package slack

import (
	"context"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/envref"
	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/slack"
)

func init() {
	notify.Register("slack", New)
}

// Channel delivers events as Slack messages.
type Channel struct {
	name     string
	client   Client
	channel  string
	events   []notify.Event
	trigger  schemas.SlackTrigger
	icons    *schemas.SlackIcons
	rules    []schemas.SlackNotifyRule
	blob     blob.Store
	grouping string
}

// Client is the slack API surface the channel consumes; *slack.Client
// satisfies it. Narrow so tests can fake it.
type Client interface {
	Post(ctx context.Context, m slack.Message) (*slack.PostResult, error)
	Update(ctx context.Context, m slack.Message) (*slack.PostResult, error)
	Upsert(ctx context.Context, channel, ts, text string, blocks []slack.Block) (*slack.PostResult, error)
	PostThread(ctx context.Context, channel, parentTS, text string, blocks []slack.Block) (*slack.PostResult, error)
}

// New is the registered constructor. The token comes from the channel's
// auth_token (notifications.yaml) or falls back to Deps.SlackToken
// (drift.yaml channels read SLACK_BOT_TOKEN); with no token the channel is
// skipped, matching the previous factory behavior.
func New(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	token := envref.Expand(cfg.AuthToken)
	if token == "" {
		token = deps.SlackToken
	}
	if token == "" {
		return nil, nil
	}
	events := notify.ParseEvents(cfg.On)
	if len(cfg.On) == 0 {
		events = defaultPREvents(cfg.Trigger)
	}
	return &Channel{
		name:     cfg.EffectiveName(),
		client:   slack.New(token),
		channel:  cfg.Channel,
		events:   events,
		trigger:  cfg.Trigger,
		icons:    cfg.Icons,
		rules:    cfg.Rules,
		blob:     deps.Blob,
		grouping: cfg.Grouping,
	}, nil
}

func (s *Channel) Name() string               { return s.name }
func (s *Channel) Subscribes() []notify.Event { return s.events }

// GroupingMode implements notify.Grouper: drift alerts for this channel are
// batched per the configured `grouping:` value (none | by_environment).
func (s *Channel) GroupingMode() string { return s.grouping }

// Deliver routes by producer: drift payloads post standalone messages,
// PR payloads drive the per-PR message lifecycle.
func (s *Channel) Deliver(ctx context.Context, p notify.Payload) error {
	switch {
	case p.Drift != nil:
		return s.deliverDrift(ctx, p)
	case p.PR != nil:
		return s.deliverPR(ctx, p)
	}
	return nil
}

// defaultPREvents preserves the legacy default subscription: with no
// explicit events list, every lifecycle event at or after the trigger is
// enabled. `apply` (and empty) is not itself a lifecycle event, so it
// enables everything - exactly what schemas.SlackEventEnabled did.
func defaultPREvents(trigger schemas.SlackTrigger) []notify.Event {
	order := notify.PREvents()
	idx := 0
	for i, e := range order {
		if string(e) == string(trigger) {
			idx = i
			break
		}
	}
	return order[idx:]
}
