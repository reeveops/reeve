package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/slack"
)

// deliverDrift posts one message per drift event (dashboard-style; drift
// runs are usually daily and bucket-side idempotency covers re-runs). When the
// payload is grouped (channel `grouping:`), one message covers the whole group.
func (s *Channel) deliverDrift(ctx context.Context, p notify.Payload) error {
	var header, text string
	if len(p.Group) > 0 {
		header = fmt.Sprintf("reeve · drift · %s · %s", labelDriftEvent(p.Event), p.GroupKey)
		text = buildGroupedDriftMessage(p)
	} else {
		header = fmt.Sprintf("reeve · drift · %s", labelDriftEvent(p.Event))
		text = buildDriftMessage(p)
	}
	blocks := []slack.Block{
		slack.Header(header),
		slack.Section(text),
	}
	_, err := s.client.Upsert(ctx, s.channel, "", text, blocks)
	return err
}

func buildDriftMessage(p notify.Payload) string {
	d := p.Drift
	var b strings.Builder
	fmt.Fprintf(&b, "*%s* - %s (+%d ~%d -%d ±%d)\n",
		d.Ref(), labelDriftEvent(p.Event),
		d.Add, d.Change, d.Delete, d.Replace,
	)
	if d.Error != "" {
		fmt.Fprintf(&b, "_error:_ %s\n", slack.Escape(slack.Truncate(d.Error, 200)))
	}
	if d.PlanSummary != "" {
		fmt.Fprintf(&b, "```%s```\n", slack.FenceSafe(slack.Truncate(d.PlanSummary, 500)))
	}
	return b.String()
}

// buildGroupedDriftMessage renders one message covering every stack in the
// group, one line per stack, under an environment heading.
func buildGroupedDriftMessage(p notify.Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s* in `%s` - %d stack(s)\n",
		labelDriftEvent(p.Event), p.GroupKey, len(p.Group))
	for _, d := range p.Group {
		fmt.Fprintf(&b, "• *%s* (+%d ~%d -%d ±%d)", d.Ref(), d.Add, d.Change, d.Delete, d.Replace)
		if d.Error != "" {
			fmt.Fprintf(&b, " — _error:_ %s", slack.Escape(slack.Truncate(d.Error, 200)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func labelDriftEvent(e notify.Event) string {
	switch e {
	case notify.EventDriftDetected:
		return "🆕 drift detected"
	case notify.EventDriftOngoing:
		return "🔁 ongoing"
	case notify.EventDriftResolved:
		return "✅ resolved"
	case notify.EventCheckFailed:
		return "💥 check failed"
	}
	return string(e)
}
