package timeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/envref"
	"github.com/FynxLabs/reeve/internal/notify"
	slackchannel "github.com/FynxLabs/reeve/internal/notify/channels/slack"
	slackapi "github.com/FynxLabs/reeve/internal/slack"
)

func init() {
	notify.Register("timeline_slack", NewSlack)
}

// threadOwner is the value written to the shared PR state's ThreadOwner
// field. Once set, the dashboard slack channel suppresses its own courtesy
// thread entries so the timeline's entries are the only replies.
const threadOwner = "timeline"

// SlackChannel posts every subscribed event as a thread reply under ONE
// PR-level anchor message. The anchor is the dashboard slack channel's per-PR
// status message when that channel is enabled (shared blob state, same CAS
// machinery); otherwise the timeline creates a minimal anchor itself.
type SlackChannel struct {
	name    string
	client  slackchannel.Client
	channel string
	events  []notify.Event
	state   slackchannel.StateStore
	now     func() time.Time
}

// NewSlack is the registered constructor for `timeline_slack`. Skipped
// without a token (auth_token or Deps.SlackToken) or a blob store, matching
// the framework's unmet-optional-dependency convention.
func NewSlack(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	token := envref.Expand(cfg.AuthToken)
	if token == "" {
		token = deps.SlackToken
	}
	if token == "" || deps.Blob == nil {
		return nil, nil
	}
	events := notify.ParseEvents(cfg.On)
	if len(cfg.On) == 0 {
		events = notify.TimelinePREvents()
	}
	return &SlackChannel{
		name:    cfg.EffectiveName(),
		client:  slackapi.New(token),
		channel: cfg.Channel,
		events:  events,
		state:   slackchannel.StateStore{Blob: deps.Blob},
		now:     time.Now,
	}, nil
}

func (s *SlackChannel) Name() string               { return s.name }
func (s *SlackChannel) Subscribes() []notify.Event { return s.events }

// Deliver threads one timeline entry under the PR's anchor message,
// creating the anchor (and claiming thread ownership) on first delivery.
func (s *SlackChannel) Deliver(ctx context.Context, p notify.Payload) error {
	if p.PR == nil {
		return nil
	}
	in := *p.PR
	entry := newEntry(p, s.now())

	st, etag, err := s.state.Load(ctx, in.PR)
	if err != nil {
		// Unknown state must not create a duplicate anchor - fail the
		// delivery instead (same rule as the dashboard channel).
		return err
	}

	dirty := false
	if st.MainTS == "" {
		res, perr := s.client.Post(ctx, slackapi.Message{
			Channel: s.channel,
			Text:    anchorText(in),
		})
		if perr != nil {
			return perr
		}
		st.Channel = res.Channel
		st.MainTS = res.TS
		dirty = true
	}
	if st.ThreadOwner == "" {
		// Claim the thread so the dashboard channel stops posting its own
		// courtesy entries alongside ours.
		st.ThreadOwner = threadOwner
		dirty = true
	}
	if dirty {
		if serr := s.state.Save(ctx, in.PR, st, etag); serr != nil {
			// A concurrent writer (dashboard channel or parallel run) recorded a
			// different anchor first. First writer wins: reload and thread
			// under the surviving anchor; our stray message stays as a
			// one-off (Slack offers no delete via bot post).
			remote, _, lerr := s.state.Load(ctx, in.PR)
			if lerr != nil || remote.MainTS == "" {
				return errors.Join(serr, lerr)
			}
			slog.Warn("timeline_slack anchor conflict: threading under the first writer's message",
				"pr", in.PR, "ours", st.MainTS, "theirs", remote.MainTS)
			st = remote
		}
	}

	ch := st.Channel
	if ch == "" {
		ch = s.channel
	}
	_, terr := s.client.PostThread(ctx, ch, st.MainTS, entry.slackText(), nil)
	return terr
}

// anchorText is the minimal anchor the timeline creates when no dashboard
// message exists yet. If the dashboard slack channel is also enabled it will
// find this message in the shared state and edit it in place into the full
// status message - the anchor stays the current status, the thread is the
// timeline.
func anchorText(in notify.PRPayload) string {
	link := fmt.Sprintf("PR #%d", in.PR)
	if in.RepoFull != "" {
		link = fmt.Sprintf("<%s/%s/pull/%d|#%d>", notify.GitHubServerURL(), in.RepoFull, in.PR, in.PR)
	}
	txt := fmt.Sprintf(":satellite_antenna: Deployment timeline — %s", link)
	if in.Title != "" {
		txt += " — " + slackapi.Escape(in.Title)
	}
	return txt
}
