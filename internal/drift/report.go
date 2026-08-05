package drift

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReportMarkdown renders a human-readable markdown report suitable for
// $GITHUB_STEP_SUMMARY or a bucket artifact. Omits FullPlan bodies (too
// noisy for a summary).
func ReportMarkdown(out *RunOutput) string {
	var b strings.Builder
	dur := out.FinishedAt.Sub(out.StartedAt).Round(time.Second)
	fmt.Fprintf(&b, "# reeve · drift · `%s`\n\n", out.RunID)
	fmt.Fprintf(&b, "_%d stacks checked in %s_\n\n", len(out.Items), dur)

	// Counts by event.
	counts := map[Event]int{}
	for _, ev := range out.Events {
		counts[ev]++
	}
	fmt.Fprintf(&b, "| new drift | ongoing | resolved | errors |\n")
	fmt.Fprintf(&b, "|---|---|---|---|\n")
	fmt.Fprintf(&b, "| %d | %d | %d | %d |\n\n",
		counts[EventDriftDetected], counts[EventDriftOngoing],
		counts[EventDriftResolved], counts[EventCheckFailed])

	// Show drifted stacks prominently. Permanently-suppressed stacks are
	// listed separately (below) rather than in the alert table.
	drifted := filterUnsuppressed(filterItemsByOutcome(out.Items, OutcomeDriftDetected))
	if len(drifted) > 0 {
		b.WriteString("## 🔴 Drifted stacks\n\n")
		b.WriteString("| Stack | Env | +Add | ~Change | -Delete | ±Replace | Status | Open PRs |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, it := range drifted {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %s | %s |\n",
				it.Ref(), it.Env,
				it.Counts.Counts.Add, it.Counts.Counts.Change,
				it.Counts.Counts.Delete, it.Counts.Counts.Replace,
				eventLabel(it.Event),
				renderOverlapCell(it.OverlappingPRs))
		}
		b.WriteString("\n")

		anyOverlap := false
		for _, it := range drifted {
			if len(it.OverlappingPRs) > 0 {
				anyOverlap = true
				break
			}
		}
		if out.OverlapWarning != "" {
			fmt.Fprintf(&b, "> ⚠️ %s\n\n", out.OverlapWarning)
		}

		if anyOverlap {
			b.WriteString("### ⚠️ Drifted stacks with open PRs\n\n")
			b.WriteString("Long-lived IaC PRs over drifted infrastructure are compounding risk - ")
			b.WriteString("the plan reviewers approved may no longer match reality.\n\n")
			for _, it := range drifted {
				if len(it.OverlappingPRs) == 0 {
					continue
				}
				fmt.Fprintf(&b, "- **%s**: ", it.Ref())
				for i, pr := range it.OverlappingPRs {
					if i > 0 {
						b.WriteString(", ")
					}
					fmt.Fprintf(&b, "#%d (@%s)", pr.Number, pr.Author)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// Errors.
	errored := filterItemsByOutcome(out.Items, OutcomeError)
	if len(errored) > 0 {
		b.WriteString("## 🔴 Check failures\n\n")
		for _, it := range errored {
			fmt.Fprintf(&b, "- **%s**: %s\n", it.Ref(), it.Error)
		}
		b.WriteString("\n")
	}

	// Clean stacks summary.
	clean := filterItemsByOutcome(out.Items, OutcomeNoDrift)
	if len(clean) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d clean stacks</summary>\n\n", len(clean))
		for _, it := range clean {
			fmt.Fprintf(&b, "- `%s`\n", it.Ref())
		}
		b.WriteString("\n</details>\n\n")
	}

	// Permanently-suppressed stacks: checked and state-tracked, but their
	// drift alerts are silenced by drift.yaml permanent_suppressions.
	suppressed := filterSuppressed(out.Items)
	if len(suppressed) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d suppressed</summary>\n\n", len(suppressed))
		for _, it := range suppressed {
			line := fmt.Sprintf("- `%s` (%s)", it.Ref(), it.Outcome)
			if it.SuppressReason != "" {
				line += " — " + it.SuppressReason
			}
			fmt.Fprintf(&b, "%s\n", line)
		}
		b.WriteString("\n</details>\n\n")
	}

	if len(out.Skipped) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d skipped</summary>\n\n", len(out.Skipped))
		for _, s := range out.Skipped {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n</details>\n\n")
	}

	return b.String()
}

func filterItemsByOutcome(items []Item, want Outcome) []Item {
	var out []Item
	for _, it := range items {
		if it.Outcome == want {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out
}

// filterUnsuppressed drops permanently-suppressed items (used to keep them
// out of the drift-alert table).
func filterUnsuppressed(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if !it.Suppressed {
			out = append(out, it)
		}
	}
	return out
}

// filterSuppressed returns permanently-suppressed items, sorted by ref.
func filterSuppressed(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.Suppressed {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out
}

func renderOverlapCell(prs []OverlappingPR) string {
	if len(prs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(prs))
	for _, p := range prs {
		parts = append(parts, fmt.Sprintf("#%d", p.Number))
	}
	return strings.Join(parts, ", ")
}

func eventLabel(e Event) string {
	switch e {
	case EventDriftDetected:
		return "🆕 new drift"
	case EventDriftOngoing:
		return "🔁 ongoing"
	case EventDriftResolved:
		return "✅ resolved"
	case EventCheckFailed:
		return "💥 error"
	}
	return "·"
}
