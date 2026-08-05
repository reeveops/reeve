// Package timeline is the deployment-timeline channel pair. Where the
// dashboard channels (slack, the PR status comment) show the CURRENT state of a
// deployment - a snapshot edited in place - the timeline is the append-only
// activity heartbeat: one entry per lifecycle event, each carrying the
// event, the commit SHA, a timestamp, and the CI run URL of the run that
// produced it (preview and apply are different Actions runs).
//
// Two channel types share this package:
//
//   - timeline_slack: entries become thread replies under ONE PR-level
//     anchor message (no channel spam), composing with the dashboard slack
//     channel's per-PR message via the shared blob state.
//   - timeline_github: entries become PR comments GROUPED BY SHA - one
//     comment per commit, maintained via marker + in-place edit, so
//     "preview started/finished" stay visible even though GitHub renders
//     comment edits silently.
//
// Both are default-off: operators enable them explicitly in the `channels:`
// list.
package timeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/notify"
	slackapi "github.com/FynxLabs/reeve/internal/slack"
)

// Entry is one timeline line. It is also the JSON record persisted in the
// timeline_github blob state, so field changes must stay backward-readable.
type Entry struct {
	Event  string `json:"event"`
	SHA    string `json:"sha,omitempty"`
	At     string `json:"at"` // RFC3339 UTC
	RunURL string `json:"run_url,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// newEntry flattens a PR payload into a timeline entry stamped at now.
func newEntry(p notify.Payload, now time.Time) Entry {
	return Entry{
		Event:  string(p.Event),
		SHA:    p.PR.CommitSHA,
		At:     now.UTC().Format(time.RFC3339),
		RunURL: p.PR.RunURL,
		Detail: detailFor(p.Event, p.PR.Stacks),
	}
}

// eventLabel returns the human phrasing plus the GitHub (unicode) and Slack
// (colon-code) icons for an event. Unknown events - future additions - fall
// back to the raw event name so the timeline never drops an entry it
// subscribed to.
func eventLabel(ev string) (label, ghIcon, slackIcon string) {
	switch notify.Event(ev) {
	case notify.EventPlanning:
		return "preview started", "🔍", ":mag:"
	case notify.EventPlan:
		return "preview finished", "📋", ":clipboard:"
	case notify.EventReady:
		return "marked ready", "🟢", ":large_green_circle:"
	case notify.EventApproved:
		return "approved", "✅", ":white_check_mark:"
	case notify.EventApplying:
		return "apply started", "🚀", ":rocket:"
	case notify.EventApplied:
		return "apply finished", "✅", ":white_check_mark:"
	case notify.EventFailed:
		return "apply failed", "🔴", ":red_circle:"
	case notify.EventBlocked:
		return "apply blocked", "🔒", ":lock:"
	case notify.EventBreakGlass:
		return "break-glass override", "🚨", ":rotating_light:"
	}
	return ev, "▪️", ":black_small_square:"
}

// detailFor summarizes per-stack outcomes for the events that carry them.
func detailFor(ev notify.Event, stacks []notify.StackResult) string {
	switch ev {
	case notify.EventPlan, notify.EventApplied:
		return stackOutcomes(stacks)
	case notify.EventFailed:
		return refsWithStatus(stacks, "error")
	case notify.EventBlocked:
		return refsWithStatus(stacks, "blocked")
	}
	return ""
}

// stackOutcomes renders the per-stack outcome summary, e.g.
// "app/prod +1 ~2 -0 ±0, net/prod error, 2 no-op". No-op stacks are
// aggregated so a wide repo doesn't flood the entry.
func stackOutcomes(stacks []notify.StackResult) string {
	var parts []string
	noop := 0
	for _, s := range stacks {
		switch {
		case s.Status == "error":
			parts = append(parts, s.Project+"/"+s.Stack+" error")
		case s.Status == "blocked":
			parts = append(parts, s.Project+"/"+s.Stack+" blocked")
		case s.Total() == 0:
			noop++
		default:
			parts = append(parts, fmt.Sprintf("%s/%s +%d ~%d -%d ±%d",
				s.Project, s.Stack, s.Add, s.Change, s.Delete, s.Replace))
		}
	}
	if noop > 0 {
		parts = append(parts, fmt.Sprintf("%d no-op", noop))
	}
	return strings.Join(parts, ", ")
}

func refsWithStatus(stacks []notify.StackResult, status string) string {
	var refs []string
	for _, s := range stacks {
		if s.Status == status {
			refs = append(refs, s.Project+"/"+s.Stack)
		}
	}
	return strings.Join(refs, ", ")
}

// markdownLine renders one entry for the per-SHA GitHub comment. The SHA
// lives in the comment header (entries are grouped by it); each line carries
// the event, timestamp, detail, and its own run's CI URL.
func (e Entry) markdownLine() string {
	label, icon, _ := eventLabel(e.Event)
	line := fmt.Sprintf("- %s **%s**", icon, label)
	if e.Detail != "" {
		line += ": " + e.Detail
	}
	line += " · " + displayTime(e.At)
	if e.RunURL != "" {
		line += fmt.Sprintf(" · [run](%s)", e.RunURL)
	}
	return line
}

// slackText renders one entry as a thread reply. Detail text can carry
// externally-influenced strings (stack names), so it is escaped per Slack
// mrkdwn rules.
func (e Entry) slackText() string {
	label, _, icon := eventLabel(e.Event)
	line := fmt.Sprintf("%s *%s*", icon, label)
	if e.SHA != "" {
		line += fmt.Sprintf(" · `%s`", shortSHA(e.SHA))
	}
	line += " · " + displayTime(e.At)
	if e.RunURL != "" {
		line += fmt.Sprintf(" · <%s|run>", e.RunURL)
	}
	if e.Detail != "" {
		line += " — " + slackapi.Escape(e.Detail)
	}
	return line
}

// displayTime compacts the stored RFC3339 stamp for rendering; a malformed
// stamp passes through untouched.
func displayTime(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
