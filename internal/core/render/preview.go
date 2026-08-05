// Package render builds the PR comment markdown. Pure string-in / string-out.
// Identified by a hidden HTML marker so the VCS adapter can upsert the
// same comment across runs.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

// Marker is the hidden HTML comment that identifies reeve's PR comment.
// The VCS adapter uses it to find-or-create on UpsertComment.
const Marker = "<!-- reeve:pr-comment:v1 -->"

// githubCommentMaxLen is GitHub's hard limit on issue/PR comment body
// length. The 422 error returned past this is non-recoverable, so the
// renderer must guarantee the body never exceeds it. We target a small
// safety margin so any wrapper formatting (e.g. quote-reply prefixes some
// VCS adapters might prepend) doesn't push us over.
const githubCommentMaxLen = 65_000

// PreviewInput is what the preview renderer consumes. Pure data - no
// imports beyond summary and stdlib.
type PreviewInput struct {
	Op          string // "preview" or "apply"
	RunNumber   int
	CommitSHA   string
	DurationSec int
	CIRunURL    string
	Stacks      []summary.StackSummary
	SortMode    string // "status_grouped" (default), "alphabetical"
	StackView   string // "all" (default) lists every stack; "changed" hides no-ops
	Notice      string // optional info banner (e.g. "already applied"); rendered above the table
}

// StackView values for the comment table.
const (
	StackViewAll     = "all"     // default: list every declared stack, no-ops included
	StackViewChanged = "changed" // only list stacks with planned/applied changes
)

// tableStacks returns the stacks to render in the comment table, honoring the
// view mode. "changed" drops no-ops; anything else (including "" -> default)
// keeps every stack.
func tableStacks(stacks []summary.StackSummary, view string) []summary.StackSummary {
	if view != StackViewChanged {
		return stacks
	}
	out := make([]summary.StackSummary, 0, len(stacks))
	for _, s := range stacks {
		if s.Status == summary.StatusNoOp {
			continue
		}
		out = append(out, s)
	}
	return out
}

// renderOpts controls which per-stack sections the renderer emits. Used
// internally to progressively drop content when the body would exceed
// GitHub's comment-size limit.
type renderOpts struct {
	includeFullPlan bool
	includeDiff     bool
	truncationNote  string
}

// Preview returns the full comment body, marker included. If the body
// would exceed GitHub's hard comment-size limit, drops the heaviest
// per-stack content (FullPlan, then PlanDiff) and -- only when actual
// reviewer-visible content is lost -- adds a notice pointing at the
// CI run. Hard-truncates as a last resort so the body always fits,
// even on pathological inputs.
//
// FullPlan is the raw `pulumi preview --json` blob, which is hundreds
// of KB per stack and effectively never reviewed from a PR comment
// (run logs have it). Dropping it is silent because the diff -- which
// IS what reviewers read -- stays intact. Only when the diff itself
// has to be dropped, or the body still doesn't fit after both drops,
// do we stamp the trim warning.
func Preview(in PreviewInput) string {
	body := renderPreview(in, renderOpts{includeFullPlan: true, includeDiff: true})
	if len(body) <= githubCommentMaxLen {
		return body
	}

	// Silent drop: FullPlan was over budget but diff is still intact, so
	// the reviewer sees everything they would have read anyway.
	body = renderPreview(in, renderOpts{includeDiff: true})
	if len(body) <= githubCommentMaxLen {
		return body
	}

	// Now we're dropping content reviewers actually look at; stamp the note.
	note := truncationNote(in)
	body = renderPreview(in, renderOpts{
		truncationNote: note + " (omitted: full plan output, per-stack diff)",
	})
	if len(body) <= githubCommentMaxLen {
		return body
	}

	// Even with both heavy sections dropped we're over budget. The table
	// itself or stacked summaries are oversize; hard-truncate the tail so
	// the POST still lands.
	const tail = "\n\n_…comment hard-truncated to fit GitHub's 65,536-char limit._\n"
	cutoff := githubCommentMaxLen - len(tail)
	if cutoff < 0 || cutoff > len(body) {
		return body
	}
	return body[:cutoff] + tail
}

// renderPreview builds the body honoring per-section opts. Pure
// string-builder; called multiple times by Preview when shrinking.
func renderPreview(in PreviewInput, opts renderOpts) string {
	var b strings.Builder
	b.WriteString(Marker)
	b.WriteString("\n")
	writeHeader(&b, in)
	if opts.truncationNote != "" {
		fmt.Fprintf(&b, "> ⚠️ %s\n\n", opts.truncationNote)
	}
	if in.Notice != "" {
		fmt.Fprintf(&b, "> ℹ️ %s\n\n", in.Notice)
	}
	writeTable(&b, in)
	writeSections(&b, in, opts)
	return b.String()
}

func truncationNote(in PreviewInput) string {
	note := "Output trimmed to fit GitHub's 65,536-char comment limit."
	if in.CIRunURL != "" {
		note += fmt.Sprintf(" See the [full run output](%s) for the complete plan.", in.CIRunURL)
	}
	return note
}

func writeHeader(b *strings.Builder, in PreviewInput) {
	icon := overallIcon(in.Stacks)
	op := in.Op
	if op == "" {
		op = "preview"
	}
	fmt.Fprintf(b, "## %s reeve · %s · %s · [commit %s]\n\n", icon, op, runRef(in.RunNumber, in.CIRunURL), shortSHA(in.CommitSHA))

	// "X stacks changed" counts only stacks that actually have planned/applied
	// changes -- no-op stacks appear in the per-stack table for completeness
	// but they didn't change anything, so summing them in the headline is
	// misleading (was reading "18 stacks changed" with 15 no-ops + 3 actual).
	n := 0
	for _, s := range in.Stacks {
		if s.Status != summary.StatusNoOp {
			n++
		}
	}
	noun := "stacks"
	if n == 1 {
		noun = "stack"
	}
	durBit := ""
	if in.DurationSec > 0 {
		durBit = fmt.Sprintf(" · ⏱ %ds", in.DurationSec)
	}
	fmt.Fprintf(b, "**%d %s changed**%s\n\n", n, noun, durBit)
}

func writeTable(b *strings.Builder, in PreviewInput) {
	if len(in.Stacks) == 0 {
		b.WriteString("_No stacks affected by this change._\n\n")
		return
	}
	rows := tableStacks(in.Stacks, in.StackView)
	if len(rows) == 0 {
		b.WriteString("_No stacks with changes._\n\n")
		return
	}
	b.WriteString("| Stack | Env | ➕ Add | 🔄 Change | ➖ Delete | 🔁 Replace | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	ordered := sorted(rows, in.SortMode)
	anyReplace := false
	for _, s := range ordered {
		if s.Counts.Replace > 0 {
			anyReplace = true
		}
		fmt.Fprintf(b, "| %s | %s | %d | %d | %d | %d | %s |\n",
			s.Project+"/"+s.Stack, envOrDash(s.Env),
			s.Counts.Add, s.Counts.Change, s.Counts.Delete, s.Counts.Replace,
			statusCell(s))
	}
	b.WriteString("\n")
	b.WriteString("<sub>Legend: `+` create · `~` update in place · `-` delete · `±` replace (delete & recreate)</sub>\n\n")
	if anyReplace {
		b.WriteString("⚠️ Replacements detected - review carefully.\n\n")
	}
}

func writeSections(b *strings.Builder, in PreviewInput, opts renderOpts) {
	ordered := sorted(in.Stacks, in.SortMode)
	for _, s := range ordered {
		if s.Status == summary.StatusNoOp {
			continue // no-ops collapse into the table line only
		}
		b.WriteString("---\n\n")
		fmt.Fprintf(b, "### %s · %s · %s\n\n", s.Ref(), envOrDash(s.Env), statusCell(s))
		if s.Status == summary.StatusBlocked && s.BlockedBy > 0 {
			fmt.Fprintf(b, "  Queued behind #%d.\n\n", s.BlockedBy)
		}
		if s.Error != "" {
			fmt.Fprintf(b, "  **Error:** %s\n\n", s.Error)
		}
		if len(s.RequiredApprovers) > 0 {
			fmt.Fprintf(b, "👥 **Required approvers:** %s\n\n", strings.Join(s.RequiredApprovers, ", "))
		}
		if s.PlanSummary != "" {
			fmt.Fprintf(b, "<details><summary>Summary (%d add, %d change, %d delete, %d replace)</summary>\n\n```diff\n%s\n```\n\n</details>\n\n",
				s.Counts.Add, s.Counts.Change, s.Counts.Delete, s.Counts.Replace,
				s.PlanSummary)
		}
		if s.PlanDiff != "" && opts.includeDiff {
			b.WriteString("<details><summary>Diff</summary>\n\n```diff\n")
			b.WriteString(s.PlanDiff)
			if !strings.HasSuffix(s.PlanDiff, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n</details>\n\n")
		}
		if s.FullPlan != "" && opts.includeFullPlan {
			b.WriteString("<details><summary>Full plan output</summary>\n\n```\n")
			b.WriteString(s.FullPlan)
			if !strings.HasSuffix(s.FullPlan, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n</details>\n\n")
		}
		if len(s.Gates) > 0 {
			fmt.Fprintf(b, "🔐 %s apply gates:\n", s.Ref())
			for _, g := range s.Gates {
				fmt.Fprintf(b, "  %s %s: %s\n", gateIcon(g.Outcome), g.Gate, g.Reason)
			}
			b.WriteString("\n")
		}
	}
}

func gateIcon(outcome string) string {
	switch outcome {
	case "pass":
		return "✅"
	case "fail":
		return "❌"
	case "warn":
		return "⚠️"
	case "skipped":
		return "⏸"
	}
	return "·"
}

func overallIcon(ss []summary.StackSummary) string {
	errored, blocked, changed := false, false, false
	for _, s := range ss {
		switch s.Status {
		case summary.StatusError:
			errored = true
		case summary.StatusBlocked:
			blocked = true
		case summary.StatusPlanned:
			if s.Counts.Total() > 0 {
				changed = true
			}
		}
	}
	switch {
	case errored:
		return "🔴"
	case blocked:
		return "🟡"
	case changed:
		return "🟢"
	default:
		return "⚪"
	}
}

func statusCell(s summary.StackSummary) string {
	switch s.Status {
	case summary.StatusBlocked:
		if s.BlockedBy > 0 {
			return fmt.Sprintf("🔒 blocked by #%d", s.BlockedBy)
		}
		return "🔒 blocked"
	case summary.StatusError:
		return "🔴 error"
	case summary.StatusNoOp:
		return "· no-op"
	case summary.StatusPlanned:
		return "✅ planned"
	}
	return string(s.Status)
}

func envOrDash(env string) string {
	if env == "" {
		return "-"
	}
	return env
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// runRef renders the "run #N" header token. GitHub auto-links a bare commit
// SHA, but never the run number, so when the CI run URL is known we hyperlink
// the run number straight to its run; otherwise it stays plain text. This is
// the single link to the run - callers no longer append a separate
// "[View run]" bit.
func runRef(runNumber int, ciRunURL string) string {
	if ciRunURL != "" {
		return fmt.Sprintf("[run #%d](%s)", runNumber, ciRunURL)
	}
	return fmt.Sprintf("run #%d", runNumber)
}

// sorted returns a copy of ss in the requested order.
// status_grouped (default): blocked first, then ready, then no-op, then error last.
// alphabetical: by Ref().
func sorted(ss []summary.StackSummary, mode string) []summary.StackSummary {
	out := make([]summary.StackSummary, len(ss))
	copy(out, ss)
	switch mode {
	case "alphabetical":
		sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	default: // status_grouped
		rank := map[summary.Status]int{
			summary.StatusBlocked: 0,
			summary.StatusPlanned: 1,
			summary.StatusError:   2,
			summary.StatusNoOp:    3,
		}
		sort.SliceStable(out, func(i, j int) bool {
			if rank[out[i].Status] != rank[out[j].Status] {
				return rank[out[i].Status] < rank[out[j].Status]
			}
			return out[i].Ref() < out[j].Ref()
		})
	}
	return out
}
