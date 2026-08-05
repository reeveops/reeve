package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/core/render"
	"github.com/FynxLabs/reeve/internal/core/summary"
)

// applyTimeline maintains ONE PR comment per commit SHA (not per run): every
// event appends an entry to the commit's persisted timeline and re-renders the
// comment in place, so the first apply, a retry, and a --force re-apply of the
// same commit all grow a single chronological thread:
//
//	🚀 apply starting
//	✅ applied: 3 stacks
//
// Keying by commit means preview/apply of a commit converge on one active
// thread, and - because editing a comment is silent while creating one fires
// an issue_comment webhook - reeve stops spawning a fresh (self-triggered,
// then guard-skipped) workflow run for every progress update.
//
// It is best-effort: comment and state failures are logged, never fatal,
// because the apply itself must not hinge on PR comment delivery. When a blob
// store is available entries are persisted with compare-and-swap so concurrent
// runs of the same commit never lose each other's history; without one (local
// runs with no bucket) it accumulates in memory for the current run only.
type applyTimeline struct {
	vcs     commentPoster
	blob    blob.Store
	pr      int
	sha     string
	marker  string
	in      render.TimelineInput
	enabled bool
}

func newApplyTimeline(vcs commentPoster, store blob.Store, pr int, runID string, runNumber int, sha, ciRunURL string) *applyTimeline {
	return &applyTimeline{
		vcs:     vcs,
		blob:    store,
		pr:      pr,
		sha:     sha,
		marker:  render.ApplyTimelineMarker(shortSHA(sha)),
		enabled: vcs != nil && pr > 0,
		in: render.TimelineInput{
			RunID:     runID,
			RunNumber: runNumber,
			CommitSHA: sha,
			CIRunURL:  ciRunURL,
		},
	}
}

// applyTimelineState is the persisted per-commit entry log. It is JSON in blob
// storage, so field changes must stay backward-readable.
type applyTimelineState struct {
	Entries []render.TimelineEntry `json:"entries"`
}

// applyTimelineKey is the per-PR, per-commit blob object holding the entries.
func applyTimelineKey(pr int, sha string) string {
	if pr == 0 {
		return fmt.Sprintf("runs/local/timeline/%s.json", shortSHA(sha))
	}
	return fmt.Sprintf("runs/pr-%d/timeline/%s.json", pr, shortSHA(sha))
}

// add appends one event and re-upserts the commit's comment.
func (t *applyTimeline) add(ctx context.Context, icon, label, detail string) {
	if t == nil || !t.enabled {
		return
	}
	entry := render.TimelineEntry{Icon: icon, Label: label, Detail: detail}
	t.in.Entries = t.persist(ctx, entry)
	body := render.ApplyTimeline(t.in)
	if err := t.vcs.UpsertComment(ctx, t.pr, body, t.marker); err != nil {
		slog.Warn("apply timeline comment failed", "err", err, "pr", t.pr, "label", label)
	}
}

// persist appends the entry to the commit's timeline and returns the full
// accumulated list. With a blob store it uses compare-and-swap with retry, so
// a concurrent run's entries are merged rather than clobbered; on any storage
// failure (or with no store) it falls back to this run's in-memory list so the
// comment still updates.
func (t *applyTimeline) persist(ctx context.Context, e render.TimelineEntry) []render.TimelineEntry {
	if t.blob == nil {
		return append(t.in.Entries, e)
	}
	key := applyTimelineKey(t.pr, t.sha)
	for attempt := 0; attempt < 3; attempt++ {
		st, etag, err := t.loadState(ctx, key)
		if err != nil {
			slog.Warn("apply timeline state load failed; using in-memory entries", "err", err, "pr", t.pr)
			return append(t.in.Entries, e)
		}
		st.Entries = append(st.Entries, e)
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return append(t.in.Entries, e)
		}
		_, err = t.blob.PutIfMatch(ctx, key, strings.NewReader(string(data)), etag)
		if err == nil {
			return st.Entries
		}
		if !errors.Is(err, blob.ErrPreconditionFailed) {
			slog.Warn("apply timeline state save failed; using in-memory entries", "err", err, "pr", t.pr)
			return append(t.in.Entries, e)
		}
		// Precondition failed: a concurrent run wrote first. Reload and retry.
	}
	slog.Warn("apply timeline state save: too many conflicts; using in-memory entries", "pr", t.pr)
	return append(t.in.Entries, e)
}

// loadState reads the commit's persisted timeline. A missing object is fresh
// state; any other decode/read failure propagates so the caller can fall back.
func (t *applyTimeline) loadState(ctx context.Context, key string) (*applyTimelineState, string, error) {
	rc, meta, err := t.blob.Get(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return &applyTimelineState{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	var st applyTimelineState
	if err := json.NewDecoder(rc).Decode(&st); err != nil {
		return nil, "", err
	}
	etag := ""
	if meta != nil {
		etag = meta.ETag
	}
	return &st, etag, nil
}

// changedStacksDetail summarizes which stacks actually applied changes.
func changedStacksDetail(ss []summary.StackSummary) string {
	var refs []string
	for _, s := range ss {
		if s.Status == summary.StatusPlanned {
			refs = append(refs, s.Ref())
		}
	}
	if len(refs) == 0 {
		return "no changes"
	}
	return fmt.Sprintf("%d stack(s): %s", len(refs), strings.Join(refs, ", "))
}

func failedStacksDetail(ss []summary.StackSummary) string {
	return strings.Join(failedStackRefs(ss), ", ")
}

func blockedStacksDetail(ss []summary.StackSummary) string {
	var parts []string
	for _, s := range ss {
		if s.Status != summary.StatusBlocked {
			continue
		}
		reason := ""
		for _, g := range s.Gates {
			if g.Outcome == "fail" {
				reason = g.Reason
				break
			}
		}
		if reason != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", s.Ref(), reason))
		} else {
			parts = append(parts, s.Ref())
		}
	}
	return strings.Join(parts, ", ")
}
