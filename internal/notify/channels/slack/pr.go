package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/slack"
)

// attachment sidebar colors.
const (
	colorPending  = "#f2c744" // plan ready / pending approval
	colorApproved = "#0066cc" // approved, ready to apply
	colorApplying = "#6f42c1" // applying in progress
	colorSuccess  = "#28a745" // applied successfully
	colorFailed   = "#dc3545" // failed or blocked hard
	colorBlocked  = "#f2c744" // soft block (missing approval, lock)
)

// deliverPR drives the per-PR message lifecycle: one message per PR, edited
// in place as the run progresses, with a thread timeline. Whether an event
// may CREATE the message (vs only update an existing one) preserves the
// legacy trigger semantics.
func (s *Channel) deliverPR(ctx context.Context, p notify.Payload) error {
	if s.blob == nil {
		return errors.New("slack pr notifications need a blob store for message state")
	}
	in := *p.PR
	in.Stacks = s.filterStacks(in.Stacks)

	state, etag, err := s.loadPRState(ctx, in.PR)
	if err != nil {
		// Do NOT fall through to posting: with unknown state we would
		// create a duplicate message. Fail the delivery instead.
		return err
	}

	trigger := s.trigger
	if trigger == "" {
		trigger = schemas.SlackTriggerApply
	}

	if state.MainTS == "" {
		// No message yet - only some events may create one.
		create := false
		switch p.Event {
		case notify.EventPlan:
			create = trigger == schemas.SlackTriggerPlan
		case notify.EventReady:
			create = trigger != schemas.SlackTriggerApply
		case notify.EventApplying, notify.EventApplied, notify.EventBlocked:
			create = true
		case notify.EventApproved, notify.EventFailed:
			// approved only updates; errors never create (error rule).
			create = false
		}
		if !create {
			return nil
		}
	}

	return s.sendOrUpdate(ctx, in, state, etag, p.Event, eventColor(p.Event))
}

func eventColor(ev notify.Event) string {
	switch ev {
	case notify.EventApproved:
		return colorApproved
	case notify.EventApplying:
		return colorApplying
	case notify.EventApplied:
		return colorSuccess
	case notify.EventFailed:
		return colorFailed
	case notify.EventBlocked:
		return colorBlocked
	}
	return colorPending
}

// filterStacks applies the rule list (environments + glob patterns) to the
// payload's stacks. Empty rules = notify all.
func (s *Channel) filterStacks(ss []notify.StackResult) []notify.StackResult {
	if len(s.rules) == 0 {
		return ss
	}
	out := make([]notify.StackResult, 0, len(ss))
	for _, st := range ss {
		if stackMatchesAnyRule(s.rules, st) {
			out = append(out, st)
		}
	}
	return out
}

func stackMatchesAnyRule(rules []schemas.SlackNotifyRule, s notify.StackResult) bool {
	for _, r := range rules {
		for _, e := range r.Environments {
			if e == s.Env {
				return true
			}
		}
		for _, pat := range r.Stacks {
			if ok, _ := doublestar.Match(pat, s.Project+"/"+s.Stack); ok {
				return true
			}
		}
	}
	return false
}

func (s *Channel) sendOrUpdate(ctx context.Context, in notify.PRPayload, state *PRState, etag string, ev notify.Event, color string) error {
	blocks := s.buildMainBlocks(in, ev)
	text := mainFallbackText(in.RepoFull, in.PR, ev)

	var res *slack.PostResult
	var err error
	if state.MainTS == "" {
		res, err = s.client.Post(ctx, slack.Message{
			Channel:     s.channel,
			Text:        text,
			Attachments: []slack.Attachment{{Color: color, Blocks: blocks}},
		})
	} else {
		ch := state.Channel
		if ch == "" {
			ch = s.channel
		}
		res, err = s.client.Update(ctx, slack.Message{
			Channel:     ch,
			TS:          state.MainTS,
			Text:        text,
			Attachments: []slack.Attachment{{Color: color, Blocks: blocks}},
		})
	}
	if err != nil {
		return err
	}
	state.Channel = res.Channel
	state.MainTS = res.TS

	// Thread: first timeline entry initialises the thread; subsequent events
	// append. When a timeline channel owns the thread (state.ThreadOwner), the
	// dashboard suppresses its courtesy entries - the timeline's richer
	// entries are the only replies.
	if state.ThreadOwner == "" {
		ch := state.Channel
		if ch == "" {
			ch = s.channel
		}
		timelineText := timelineEntry(ev, in.CommitSHA, in.RunURL)
		tr, terr := s.client.PostThread(ctx, ch, res.TS, timelineText, nil)
		if terr != nil {
			// Thread post is a courtesy update on the main message; failure is
			// non-fatal but should be visible in operator logs.
			slog.Warn("slack thread post failed", "err", terr, "pr", in.PR, "channel", ch)
		} else if state.ThreadTS == "" {
			state.ThreadTS = tr.TS
		}
	}

	return s.savePRState(ctx, in.PR, state, etag)
}

// buildMainBlocks produces the attachment blocks for the main message.
func (s *Channel) buildMainBlocks(in notify.PRPayload, ev notify.Event) []slack.Block {
	engineIcon := s.icon("engine", ":building_construction:")
	runnerIcon := s.icon("runner", ":runner:")
	authorIcon := s.icon("author", ":bust_in_silhouette:")
	approverIcon := s.icon("approver", ":approved_stamp:")

	blocks := []slack.Block{
		slack.Header(fmt.Sprintf("%s IAC Changes", engineIcon)),
		slack.Divider(),
	}

	// Status + Author row
	blocks = append(blocks, slack.Fields(
		slack.MrkdwnField(fmt.Sprintf("%s *Status:* %s", statusEmoji(ev), eventTitle(ev))),
		slack.MrkdwnField(fmt.Sprintf("%s *Author:* %s", authorIcon, slack.Escape(in.Author))),
	))

	blocks = append(blocks, slack.Divider())

	// PR link. Title and label text are externally controlled - escape per
	// Slack mrkdwn rules so a title can't inject markup or mentions.
	prLink := fmt.Sprintf("<%s/%s/pull/%d|#%d", notify.GitHubServerURL(), in.RepoFull, in.PR, in.PR)
	if in.Title != "" {
		prLink += " - " + slack.Escape(in.Title)
	}
	prLink += ">"
	blocks = append(blocks, slack.Section(fmt.Sprintf(":writing_hand: *PR:* %s", prLink)))

	// Required approvers when pending
	if (ev == notify.EventPlan || ev == notify.EventReady) && len(in.RequiredApprovers) > 0 {
		blocks = append(blocks, slack.Section(
			fmt.Sprintf("%s *Needs approval from:* %s", approverIcon, slack.Escape(strings.Join(in.RequiredApprovers, ", "))),
		))
	}

	// Stack list (changed stacks only)
	stackLines := stackSummaryLines(in.Stacks)
	if stackLines != "" {
		blocks = append(blocks, slack.Divider())
		blocks = append(blocks, slack.Section(stackLines))
	}

	// Footer: view run button, apply hint only when approved/pending
	blocks = append(blocks, slack.Divider())
	var footerText string
	switch ev {
	case notify.EventApproved:
		footerText = "Run `/reeve apply` in the PR to apply."
	case notify.EventPlan, notify.EventReady:
		footerText = "Waiting for approval."
	case notify.EventApplying:
		footerText = ":hourglass_flowing_sand: Apply in progress..."
	case notify.EventApplied:
		footerText = ":white_check_mark: Applied successfully."
	case notify.EventFailed:
		footerText = ":x: Apply failed. Check the run for details."
	case notify.EventBlocked:
		footerText = ":lock: Apply blocked - preconditions not met."
	}

	if in.RunURL != "" {
		blocks = append(blocks, slack.SectionWithButton(
			footerText,
			fmt.Sprintf("%s View Run", runnerIcon),
			in.RunURL,
			"view_run",
		))
	} else if footerText != "" {
		blocks = append(blocks, slack.Section(footerText))
	}

	return blocks
}

func statusEmoji(ev notify.Event) string {
	switch ev {
	case notify.EventPlanning:
		return ":mag:"
	case notify.EventBreakGlass:
		return ":rotating_light:"
	case notify.EventPlan, notify.EventReady:
		return ":large_orange_circle:"
	case notify.EventApproved:
		return ":large_blue_circle:"
	case notify.EventApplying:
		return ":hourglass_flowing_sand:"
	case notify.EventApplied:
		return ":large_green_circle:"
	case notify.EventFailed:
		return ":red_circle:"
	case notify.EventBlocked:
		return ":large_yellow_circle:"
	}
	return ":white_circle:"
}

func stackSummaryLines(stacks []notify.StackResult) string {
	var sb strings.Builder
	for _, s := range stacks {
		if s.Total() == 0 && s.Status != "error" {
			continue
		}
		fmt.Fprintf(&sb, "%s `%s` — +%d ~%d -%d ±%d\n",
			stackIcon(s.Status), s.Stack,
			s.Add, s.Change, s.Delete, s.Replace)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (s *Channel) icon(kind, defaultEmoji string) string {
	if s.icons == nil {
		return defaultEmoji
	}
	switch kind {
	case "engine":
		if s.icons.Engine != "" {
			return s.icons.Engine
		}
	case "runner":
		if s.icons.Runner != "" {
			return s.icons.Runner
		}
	case "author":
		if s.icons.Author != "" {
			return s.icons.Author
		}
	case "approver":
		if s.icons.Approver != "" {
			return s.icons.Approver
		}
	}
	return defaultEmoji
}

func eventTitle(ev notify.Event) string {
	switch ev {
	case notify.EventPlanning:
		return "Preview Running..."
	case notify.EventBreakGlass:
		return "Break-Glass Override :rotating_light:"
	case notify.EventPlan:
		return "Planned - Pending Approval"
	case notify.EventReady:
		return "Ready for Approval"
	case notify.EventApproved:
		return "Approved - Ready to Apply"
	case notify.EventApplying:
		return "Applying..."
	case notify.EventApplied:
		return "Applied :white_check_mark:"
	case notify.EventFailed:
		return "Failed :x:"
	case notify.EventBlocked:
		return "Blocked :lock:"
	}
	return "Update"
}

// legacyEventName preserves the fallback-text wire strings from the previous
// implementation ("plan_ready" rather than "plan").
func legacyEventName(ev notify.Event) string {
	if ev == notify.EventPlan {
		return "plan_ready"
	}
	return string(ev)
}

func mainFallbackText(repoFull string, pr int, ev notify.Event) string {
	return fmt.Sprintf("%s %s - PR #%d", repoFull, legacyEventName(ev), pr)
}

func timelineEntry(ev notify.Event, sha, runURL string) string {
	ts := time.Now().UTC().Format("15:04 UTC")
	commit := ""
	if sha != "" {
		commit = fmt.Sprintf(" · `%s`", shortSHA(sha))
	}
	run := ""
	if runURL != "" {
		run = fmt.Sprintf(" · <%s|View Run>", runURL)
	}
	switch ev {
	case notify.EventPlanning:
		return fmt.Sprintf(":mag: *Preview started* · %s%s%s", ts, commit, run)
	case notify.EventBreakGlass:
		return fmt.Sprintf(":rotating_light: *Break-glass override* · %s%s%s", ts, commit, run)
	case notify.EventPlan:
		return fmt.Sprintf(":clipboard: *Plan ready* · %s%s%s", ts, commit, run)
	case notify.EventReady:
		return fmt.Sprintf(":white_check_mark: *Marked ready* · %s%s%s", ts, commit, run)
	case notify.EventApproved:
		return fmt.Sprintf(":approved_stamp: *Approved* · %s%s%s", ts, commit, run)
	case notify.EventApplying:
		return fmt.Sprintf(":hourglass_flowing_sand: *Applying* · %s%s%s", ts, commit, run)
	case notify.EventApplied:
		return fmt.Sprintf(":white_check_mark: *Applied* · %s%s%s", ts, commit, run)
	case notify.EventFailed:
		return fmt.Sprintf(":x: *Failed* · %s%s%s", ts, commit, run)
	case notify.EventBlocked:
		return fmt.Sprintf(":lock: *Blocked* · %s%s%s", ts, commit, run)
	}
	return fmt.Sprintf("· %s%s%s", ts, commit, run)
}

func stackIcon(status string) string {
	switch status {
	case "planned":
		return ":white_check_mark:"
	case "blocked":
		return ":lock:"
	case "error":
		return ":x:"
	case "noop":
		return ":dot:"
	}
	return ":grey_question:"
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
