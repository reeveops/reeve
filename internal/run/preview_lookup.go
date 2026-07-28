package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/summary"
)

// PreviewStatus is what apply needs to know about a prior preview for a
// given (PR, commit SHA, stack ref). Filled from the most recent matching
// run manifest in the bucket.
type PreviewStatus struct {
	Found        bool
	Age          time.Duration
	Succeeded    bool // false if the stack's preview errored
	HasChanges   bool
	ErrorMessage string
	RunID        string
	// Plan is the stored preview StackSummary for this stack (plan body,
	// counts, summary). Policy hooks evaluate this at apply time - the plan
	// that will actually be applied - rather than an empty pre-apply summary.
	Plan *summary.StackSummary
}

// PlanSucceededForPR returns true if the most recent preview manifest for the
// given PR and commit SHA exists and has no stacks in error state.
func PlanSucceededForPR(ctx context.Context, store blob.Store, prNumber int, commitSHA string) bool {
	if store == nil || prNumber == 0 {
		return false
	}
	prefix := fmt.Sprintf("runs/pr-%d/", prNumber)
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return false
	}
	var manifests []string
	for _, k := range keys {
		if strings.HasSuffix(k, "/manifest.json") {
			manifests = append(manifests, k)
		}
	}
	var best *manifest
	for _, k := range manifests {
		data, _, err := filesystem.ReadBytes(ctx, store, k)
		if err != nil {
			continue
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Op != "preview" || m.CommitSHA != commitSHA {
			continue
		}
		if best == nil || m.CreatedAt > best.CreatedAt {
			c := m
			best = &c
		}
	}
	if best == nil {
		return false
	}
	for _, ss := range best.Stacks {
		if ss.Status == summary.StatusError {
			return false
		}
	}
	return len(best.Stacks) > 0
}

// FindPreviewForStack scans runs/pr-{n}/ for manifests, picks the most
// recent one whose commit_sha + op=preview matches, and reports whether
// the named stack was present and successful there.
func FindPreviewForStack(ctx context.Context, store blob.Store, prNumber int, commitSHA, stackRef string) (PreviewStatus, error) {
	if store == nil || prNumber == 0 {
		return PreviewStatus{}, nil
	}
	// Same selection as PreviewedStackRefs, deliberately: if these two
	// disagreed about which manifest is authoritative, apply could target a
	// stack from one manifest and then gate it against another.
	best := newestPreviewManifest(ctx, store, prNumber, commitSHA)
	if best == nil {
		slog.Debug("preview lookup: no matching preview manifest for sha", "pr", prNumber, "sha", commitSHA)
		return PreviewStatus{}, nil
	}
	slog.Debug("preview lookup: best manifest", "run_id", best.RunID, "created_at", best.CreatedAt, "stack_count", len(best.Stacks))

	createdAt, err := time.Parse(time.RFC3339, best.CreatedAt)
	if err != nil {
		createdAt = time.Now()
	}
	st := PreviewStatus{
		Found:     true,
		Age:       time.Since(createdAt),
		Succeeded: true,
		RunID:     best.RunID,
	}
	for _, ss := range best.Stacks {
		if ss.Ref() != stackRef {
			continue
		}
		if ss.Status == summary.StatusError {
			st.Succeeded = false
			st.ErrorMessage = ss.Error
		}
		if ss.Counts.Total() > 0 {
			st.HasChanges = true
		}
		plan := ss
		st.Plan = &plan
		return st, nil
	}
	// Manifest exists for this SHA but doesn't cover this stack - treat as
	// "no fresh preview for this stack".
	return PreviewStatus{Found: false}, nil
}

// PreviewedStackRefs returns the set of stack refs the newest preview for
// this exact commit SHA covered, and whether such a preview exists.
//
// This is what binds apply's blast radius to what was actually planned and
// approved. Apply must not re-derive its target set from the PR's changed
// files: that list is a LIVE diff against a moving base, so a base branch
// that advanced between preview and apply can change which files appear,
// which changes which stacks map, which silently changes what apply touches.
// The manifest is pinned to the commit SHA and is immutable, so it is the
// only honest answer to "what was reviewed".
func PreviewedStackRefs(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (map[string]bool, bool) {
	if store == nil || prNumber == 0 || commitSHA == "" {
		return nil, false
	}
	best := newestPreviewManifest(ctx, store, prNumber, commitSHA)
	if best == nil || len(best.Stacks) == 0 {
		return nil, false
	}
	refs := make(map[string]bool, len(best.Stacks))
	for _, ss := range best.Stacks {
		refs[ss.Ref()] = true
	}
	return refs, true
}

// newestPreviewManifest returns the most recent preview manifest for the
// (PR, commit SHA) pair, or nil.
func newestPreviewManifest(ctx context.Context, store blob.Store, prNumber int, commitSHA string) *manifest {
	keys, err := store.List(ctx, fmt.Sprintf("runs/pr-%d/", prNumber))
	if err != nil {
		return nil
	}
	var best *manifest
	for _, k := range keys {
		if !strings.HasSuffix(k, "/manifest.json") {
			continue
		}
		data, _, err := filesystem.ReadBytes(ctx, store, k)
		if err != nil {
			continue
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Op != "preview" || m.CommitSHA != commitSHA {
			continue
		}
		if best == nil || m.CreatedAt > best.CreatedAt ||
			(m.CreatedAt == best.CreatedAt && m.RunID > best.RunID) {
			c := m
			best = &c
		}
	}
	return best
}
