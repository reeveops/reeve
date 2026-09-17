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
func LoadPreviewSnapshot(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (PreviewSnapshot, error) {
	if store == nil || prNumber == 0 {
		return PreviewSnapshot{}, nil
	}
	best, err := newestPreviewManifest(ctx, store, prNumber, commitSHA)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	if best == nil {
		slog.Debug("preview lookup: no matching preview manifest for sha", "pr", prNumber, "sha", commitSHA)
		return PreviewSnapshot{}, nil
	}
	slog.Debug("preview lookup: best manifest", "run_id", best.RunID, "created_at", best.CreatedAt, "stack_count", len(best.Stacks))

	createdAt, err := time.Parse(time.RFC3339, best.CreatedAt)
	if err != nil {
		return PreviewSnapshot{}, fmt.Errorf("preview manifest %q has invalid created_at %q: %w", best.RunID, best.CreatedAt, err)
	}
	age := time.Since(createdAt)
	snapshot := PreviewSnapshot{
		statuses:     make(map[string]PreviewStatus, len(best.Stacks)),
		stackCount:   len(best.Stacks),
		allSucceeded: len(best.Stacks) > 0,
	}
	for _, ss := range best.Stacks {
		if strings.TrimSpace(ss.Project) == "" || strings.TrimSpace(ss.Stack) == "" {
			return PreviewSnapshot{}, fmt.Errorf("preview manifest %q contains an invalid stack reference", best.RunID)
		}
		ref := ss.Ref()
		if _, exists := snapshot.statuses[ref]; exists {
			return PreviewSnapshot{}, fmt.Errorf("preview manifest %q contains duplicate stack %q", best.RunID, ref)
		}
		succeeded := false
		switch ss.Status {
		case summary.StatusPlanned, summary.StatusNoOp:
			succeeded = true
		case summary.StatusError:
			snapshot.allSucceeded = false
		default:
			return PreviewSnapshot{}, fmt.Errorf("preview manifest %q has invalid status %q for stack %q", best.RunID, ss.Status, ref)
		}
		status := PreviewStatus{
			Found:      true,
			Age:        age,
			Succeeded:  succeeded,
			HasChanges: ss.Counts.Total() > 0,
			RunID:      best.RunID,
		}
		if !status.Succeeded {
			status.ErrorMessage = ss.Error
			snapshot.allSucceeded = false
		}
		plan := ss
		status.Plan = &plan
		snapshot.statuses[ref] = status
	}
	return snapshot, nil
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
func PlanSucceededForPR(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (bool, error) {
	snapshot, err := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	return snapshot.Succeeded(), err
}

// FindPreviewForStack scans runs/pr-{n}/ for manifests, picks the most
// recent one whose commit_sha + op=preview matches, and reports whether
// the named stack was present and successful there.
func FindPreviewForStack(ctx context.Context, store blob.Store, prNumber int, commitSHA, stackRef string) (PreviewStatus, error) {
	snapshot, err := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	return snapshot.StackStatus(stackRef), err
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
func PreviewedStackRefs(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (map[string]bool, bool, error) {
	if store == nil || prNumber == 0 || commitSHA == "" {
		return nil, false, nil
	}
	snapshot, err := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	refs, ok := snapshot.StackRefs()
	return refs, ok, err
}

// newestPreviewManifest returns the most recent preview manifest for the
// (PR, commit SHA) pair, or nil. An unreadable object fails the lookup because
// it may be the authoritative candidate for this commit.
func newestPreviewManifest(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (*manifest, error) {
	prefix := fmt.Sprintf("runs/pr-%d/", prNumber)
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list preview manifests: %w", err)
	}
	var best *manifest
	var bestCreatedAt time.Time
	for _, k := range keys {
		runID, identityBound, candidate := previewManifestCandidate(k, prefix, commitSHA)
		if !candidate {
			continue
		}
		data, _, err := filesystem.ReadBytes(ctx, store, k)
		if err != nil {
			return nil, fmt.Errorf("read preview manifest %q: %w", k, err)
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("decode preview manifest %q: %w", k, err)
		}
		if m.Op != "preview" || m.CommitSHA != commitSHA {
			if identityBound {
				return nil, fmt.Errorf("preview manifest %q does not match its artifact identity", k)
			}
			continue
		}
		if m.PR != prNumber {
			return nil, fmt.Errorf("preview manifest %q has PR %d, want %d", k, m.PR, prNumber)
		}
		if m.RunID != runID {
			return nil, fmt.Errorf("preview manifest %q has run_id %q, want %q", k, m.RunID, runID)
		}
		createdAt, parseErr := time.Parse(time.RFC3339, m.CreatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("preview manifest %q has invalid created_at %q: %w", k, m.CreatedAt, parseErr)
		}
		if best == nil || createdAt.After(bestCreatedAt) ||
			(createdAt.Equal(bestCreatedAt) && m.RunID > best.RunID) {
			c := m
			best = &c
			bestCreatedAt = createdAt
		}
	}
	return best, nil
}

func previewManifestCandidate(key, prefix, commitSHA string) (string, bool, bool) {
	if commitSHA == "" || !strings.HasPrefix(key, prefix) {
		return "", false, false
	}
	relative := strings.TrimPrefix(key, prefix)
	runID, rest, ok := strings.Cut(relative, "/")
	if !ok || rest != "manifest.json" {
		return "", false, false
	}
	if strings.HasPrefix(runID, "apply-") || strings.HasPrefix(runID, "refresh-") {
		return "", false, false
	}
	if strings.HasPrefix(runID, "run-") && !strings.HasSuffix(runID, "-"+shortSHA(commitSHA)) {
		return "", false, false
	}
	return runID, strings.HasPrefix(runID, "run-"), true
}
