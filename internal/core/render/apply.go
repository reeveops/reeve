package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

// ApplyMarker identifies reeve's apply-specific PR comment slot.
const ApplyMarker = "<!-- reeve:apply:v1 -->"

// BreakGlassMarker tags the break-glass section inside an apply comment so
// tooling (and humans grepping the PR) can find emergency overrides.
const BreakGlassMarker = "<!-- reeve:break-glass:v1 -->"

// BreakGlassNote is the emergency-override context rendered LOUDLY at the
// top of the apply comment. Present only for break-glass runs.
type BreakGlassNote struct {
	Actor         string
	Justification string
	AuthorizedVia string   // matched authorization source
	Overridden    []string // gates overridden (e.g. approvals, not_in_freeze)
	// ConfigModifiedInPR flags that the authorizing config/CODEOWNERS was
	// modified in this same PR (self-add is by design, but loud).
	ConfigModifiedInPR bool
}

// Apply-run progress is now reported via the accumulating timeline comment
// (see timeline.go / run.applyTimeline) rather than a one-shot "starting"
// comment, so the prior ApplyStarting renderer was removed.

// ApplyInput mirrors PreviewInput but carries apply-specific data.
type ApplyInput struct {
	RunNumber   int
	CommitSHA   string
	DurationSec int
	CIRunURL    string
	Stacks      []summary.StackSummary
	SortMode    string
	Style       string
	StackView   string // "all" (default) lists every stack; "changed" hides no-ops
	// BreakGlass, when non-nil, renders the loud emergency-override
	// section at the top of the comment.
	BreakGlass *BreakGlassNote
}

// Apply renders the apply comment markdown. If the body would exceed
// GitHub's hard comment-size limit, drops per-stack FullPlan output and
// adds a notice pointing at the CI run. Hard-truncates as a last resort.
func Apply(in ApplyInput) string {
	body := renderApply(in, renderOpts{includeFullPlan: true})
	if len(body) <= githubCommentMaxLen {
		return body
	}

	note := truncationNote(PreviewInput{CIRunURL: in.CIRunURL})

	body = renderApply(in, renderOpts{
		truncationNote: note + " (omitted: full apply output)",
	})
	if len(body) <= githubCommentMaxLen {
		return body
	}

	const tail = "\n\n_…comment hard-truncated to fit GitHub's 65,536-char limit._\n"
	cutoff := githubCommentMaxLen - len(tail)
	if cutoff < 0 || cutoff > len(body) {
		return body
	}
	return body[:cutoff] + tail
}

func renderApply(in ApplyInput, opts renderOpts) string {
	var b strings.Builder
	if in.Style == "section" {
		b.WriteString(ApplyMarker)
	} else {
		b.WriteString(Marker)
	}
	b.WriteString("\n")

	icon := overallIcon(in.Stacks)
	if in.BreakGlass != nil {
		icon = "🚨"
	}
	fmt.Fprintf(&b, "## %s reeve · apply · %s · [commit %s]\n\n", icon, runRef(in.RunNumber, in.CIRunURL), shortSHA(in.CommitSHA))

	if in.BreakGlass != nil {
		b.WriteString(renderBreakGlassNote(*in.BreakGlass))
	}

	n := len(in.Stacks)
	noun := "stacks"
	if n == 1 {
		noun = "stack"
	}
	durBit := ""
	if in.DurationSec > 0 {
		durBit = fmt.Sprintf(" · ⏱ %ds", in.DurationSec)
	}
	fmt.Fprintf(&b, "**%d %s applied**%s\n\n", n, noun, durBit)

	if opts.truncationNote != "" {
		fmt.Fprintf(&b, "> ⚠️ %s\n\n", opts.truncationNote)
	}

	if n == 0 {
		b.WriteString("_No stacks applied._\n")
		return b.String()
	}

	// Table: failures first.
	rows := tableStacks(in.Stacks, in.StackView)
	b.WriteString("| Stack | Env | ➕ Add | 🔄 Change | ➖ Delete | 🔁 Replace | Duration | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	ordered := sortApply(rows, in.SortMode)
	for _, s := range ordered {
		dur := ""
		if s.DurationMS > 0 {
			dur = fmt.Sprintf("%ds", s.DurationMS/1000)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %s | %s |\n",
			s.Ref(), envOrDash(s.Env),
			s.Counts.Add, s.Counts.Change, s.Counts.Delete, s.Counts.Replace,
			dur, applyStatusCell(s))
	}
	b.WriteString("\n")

	// Per-stack details, failures first.
	for _, s := range ordered {
		if s.Status == summary.StatusNoOp {
			continue
		}
		b.WriteString("---\n\n")
		fmt.Fprintf(&b, "### %s · %s · %s\n\n", s.Ref(), envOrDash(s.Env), applyStatusCell(s))
		if s.Status == summary.StatusBlocked && len(s.Gates) > 0 {
			for _, g := range s.Gates {
				if g.Outcome == "fail" {
					fmt.Fprintf(&b, "**Blocked:** %s (`%s`)\n\n", g.Reason, g.Gate)
					break
				}
			}
		}
		if s.Error != "" {
			fmt.Fprintf(&b, "  **Error:** %s\n\n", s.Error)
		}
		if s.PlanSummary != "" {
			fmt.Fprintf(&b, "<details><summary>Summary (%d add, %d change, %d delete, %d replace)</summary>\n\n%s\n\n</details>\n\n",
				s.Counts.Add, s.Counts.Change, s.Counts.Delete, s.Counts.Replace,
				s.PlanSummary)
		}
		if s.FullPlan != "" && opts.includeFullPlan {
			b.WriteString("<details><summary>Full apply output</summary>\n\n```\n")
			b.WriteString(s.FullPlan)
			if !strings.HasSuffix(s.FullPlan, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n</details>\n\n")
		}
	}

	return b.String()
}

// renderBreakGlassNote emits the loud override section: its own marker, a
// GitHub warning admonition, the actor/source/overridden-gates line, the
// same-PR-config flag when set, and the justification as a quote.
func renderBreakGlassNote(n BreakGlassNote) string {
	var b strings.Builder
	b.WriteString(BreakGlassMarker + "\n")
	b.WriteString("> [!WARNING]\n")
	b.WriteString("> ### 🚨 BREAK-GLASS APPLY — emergency override\n")
	b.WriteString("> This run was forced past reeve's normal gates. Review it after the fire is out.\n")
	b.WriteString(">\n")
	fmt.Fprintf(&b, "> **Actor:** @%s · **Authorized via:** `%s`", strings.TrimPrefix(n.Actor, "@"), n.AuthorizedVia)
	if len(n.Overridden) > 0 {
		gates := make([]string, 0, len(n.Overridden))
		for _, g := range n.Overridden {
			gates = append(gates, "`"+g+"`")
		}
		fmt.Fprintf(&b, " · **Overridden gates:** %s", strings.Join(gates, ", "))
	} else {
		b.WriteString(" · **Overridden gates:** none (all gates would have passed)")
	}
	b.WriteString("\n")
	if n.ConfigModifiedInPR {
		b.WriteString(">\n> ⚠️ **The break-glass config or CODEOWNERS was modified in this same PR.** Authorization is head-resolved by design — verify the change was legitimate.\n")
	}
	b.WriteString(">\n> **Justification:**\n")
	for _, line := range strings.Split(strings.TrimSpace(n.Justification), "\n") {
		fmt.Fprintf(&b, "> > %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

func applyStatusCell(s summary.StackSummary) string {
	switch s.Status {
	case summary.StatusError:
		return "🔴 failed"
	case summary.StatusNoOp:
		return "· no-op"
	case summary.StatusBlocked:
		if s.BlockedBy > 0 {
			return fmt.Sprintf("🔒 blocked by #%d", s.BlockedBy)
		}
		return "🔒 blocked"
	case summary.StatusPlanned:
		return "✅ applied"
	}
	return string(s.Status)
}

// sortApply surfaces failures first, then applied, then blocked, then no-op.
func sortApply(ss []summary.StackSummary, mode string) []summary.StackSummary {
	out := make([]summary.StackSummary, len(ss))
	copy(out, ss)
	if mode == "alphabetical" {
		sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
		return out
	}
	rank := map[summary.Status]int{
		summary.StatusError:   0,
		summary.StatusBlocked: 1,
		summary.StatusPlanned: 2,
		summary.StatusNoOp:    3,
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Status] != rank[out[j].Status] {
			return rank[out[i].Status] < rank[out[j].Status]
		}
		return out[i].Ref() < out[j].Ref()
	})
	return out
}
