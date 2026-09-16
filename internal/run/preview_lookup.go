package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/summary"
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

// PreviewSnapshot is the authoritative preview manifest for one PR and
// commit, indexed once for all stack-level gates in an invocation.
type PreviewSnapshot struct {
	statuses     map[string]PreviewStatus
	stackCount   int
	allSucceeded bool
}

// LoadPreviewSnapshot scans the PR's manifests once and indexes the newest
// preview for the requested commit.
func LoadPreviewSnapshot(ctx context.Context, store blob.Store, prNumber int, commitSHA string) PreviewSnapshot {
	if store == nil || prNumber == 0 {
		return PreviewSnapshot{}
	}
	best := newestPreviewManifest(ctx, store, prNumber, commitSHA)
	if best == nil {
		slog.Debug("preview lookup: no matching preview manifest for sha", "pr", prNumber, "sha", commitSHA)
		return PreviewSnapshot{}
	}
	slog.Debug("preview lookup: best manifest", "run_id", best.RunID, "created_at", best.CreatedAt, "stack_count", len(best.Stacks))

	createdAt, err := time.Parse(time.RFC3339, best.CreatedAt)
	if err != nil {
		createdAt = time.Now()
	}
	age := time.Since(createdAt)
	snapshot := PreviewSnapshot{
		statuses:     make(map[string]PreviewStatus, len(best.Stacks)),
		stackCount:   len(best.Stacks),
		allSucceeded: len(best.Stacks) > 0,
	}
	for _, ss := range best.Stacks {
		status := PreviewStatus{
			Found:      true,
			Age:        age,
			Succeeded:  ss.Status != summary.StatusError,
			HasChanges: ss.Counts.Total() > 0,
			RunID:      best.RunID,
		}
		if !status.Succeeded {
			status.ErrorMessage = ss.Error
			snapshot.allSucceeded = false
		}
		plan := ss
		status.Plan = &plan
		if _, exists := snapshot.statuses[ss.Ref()]; !exists {
			snapshot.statuses[ss.Ref()] = status
		}
	}
	return snapshot
}

// StackStatus returns the selected manifest's result for one stack.
func (s PreviewSnapshot) StackStatus(stackRef string) PreviewStatus {
	return s.statuses[stackRef]
}

// StackRefs returns every stack covered by the selected manifest.
func (s PreviewSnapshot) StackRefs() (map[string]bool, bool) {
	if s.stackCount == 0 {
		return nil, false
	}
	refs := make(map[string]bool, len(s.statuses))
	for ref := range s.statuses {
		refs[ref] = true
	}
	return refs, true
}

// Succeeded reports whether the selected manifest exists, covers at least one
// stack, and contains no stack errors.
func (s PreviewSnapshot) Succeeded() bool {
	return s.allSucceeded
}

// PlanSucceededForPR returns true if the most recent preview manifest for the
// given PR and commit SHA exists and has no stacks in error state.
//
// Selection goes through newestPreviewManifest for the same reason
// FindPreviewForStack does: if the "which manifest is authoritative"
// answers ever disagreed, `reeve ready` could report a green plan from one
// run while apply gated against another. This function used to re-implement
// the scan and had already drifted - it was missing the RunID tie-break for
// manifests written in the same second.
func PlanSucceededForPR(ctx context.Context, store blob.Store, prNumber int, commitSHA string) bool {
	return LoadPreviewSnapshot(ctx, store, prNumber, commitSHA).Succeeded()
}

// FindPreviewForStack scans runs/pr-{n}/ for manifests, picks the most
// recent one whose commit_sha + op=preview matches, and reports whether
// the named stack was present and successful there.
func FindPreviewForStack(ctx context.Context, store blob.Store, prNumber int, commitSHA, stackRef string) (PreviewStatus, error) {
	snapshot := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	return snapshot.StackStatus(stackRef), nil
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
	return LoadPreviewSnapshot(ctx, store, prNumber, commitSHA).StackRefs()
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
