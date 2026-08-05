package render

import "fmt"

// ApplyTimelineMarker returns the hidden HTML marker that pins ONE apply
// timeline comment per commit. Keyed by short SHA (not run ID) so every run of
// the same commit - the first apply, a retry, a --force re-apply - edits the
// same comment in place instead of each posting a fresh one. That keeps the
// deployment log for a commit in a single active thread and avoids the extra
// issue_comment webhooks (and skipped self-trigger runs) that a new comment
// per run would spawn.
func ApplyTimelineMarker(shortSHA string) string {
	return fmt.Sprintf("<!-- reeve:apply-timeline:%s -->", shortSHA)
}

// TimelineEntry is one line in a run's timeline.
type TimelineEntry struct {
	Icon   string // emoji, e.g. "🚀" "✅" "🔴" "🔒" "⏭️"
	Label  string // short status, e.g. "apply starting", "applied"
	Detail string // optional extra context (reason, counts), may be empty
}

// TimelineInput is what ApplyTimeline renders.
type TimelineInput struct {
	RunID     string // identifies the writing run (audit/trace); the comment is pinned per commit SHA
	RunNumber int    // shown in the header as the latest run to touch this commit
	CommitSHA string // pins the comment marker (one timeline comment per commit)
	CIRunURL  string
	Entries   []TimelineEntry
}

// ApplyTimeline renders a run's full timeline comment. It is re-rendered from
// the in-memory entry list each time a new event lands and upserted under the
// per-commit marker, so the comment grows one line per event.
func ApplyTimeline(in TimelineInput) string {
	var b []byte
	b = append(b, ApplyTimelineMarker(shortSHA(in.CommitSHA))...)
	b = append(b, '\n')
	header := fmt.Sprintf("### 🚀 reeve · apply · %s · [commit %s]\n\n",
		runRef(in.RunNumber, in.CIRunURL), shortSHA(in.CommitSHA))
	b = append(b, header...)
	for _, e := range in.Entries {
		line := fmt.Sprintf("- %s **%s**", e.Icon, e.Label)
		if e.Detail != "" {
			line += ": " + e.Detail
		}
		b = append(b, (line + "\n")...)
	}
	return string(b)
}
