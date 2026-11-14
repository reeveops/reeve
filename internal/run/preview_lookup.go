package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/summary"
)

// maxPreviewClockSkew bounds how far ahead of the local clock a manifest's
// created_at may be and still count as created in the past. A stamp beyond
// this skew is skipped at selection - it never counts as fresh, no matter
// how new its timestamp sorts: a future-dated manifest computes a NEGATIVE
// age, which would hold the freshness gate at green indefinitely.
const maxPreviewClockSkew = 2 * time.Minute

// FuturePreviewError reports that every candidate manifest for the commit
// is dated ahead of the clock beyond the allowed skew. The apply fails
// closed; the named manifest is the newest skipped one. Re-run
// `/reeve preview` to publish a current manifest - the next lookup selects
// it because only FUTURE-dated manifests are skipped.
type FuturePreviewError struct {
	// Key is the object key of the newest skipped manifest.
	Key string
	// CreatedAt is its stored stamp, and Clock is the local time it was
	// compared against.
	CreatedAt string
	Clock     time.Time
}

func (e *FuturePreviewError) Error() string {
	return fmt.Sprintf("preview %q rejected: created_at %q is in the future (clock is %s); the manifest cannot count as fresh. Re-run /reeve preview to publish a current preview.",
		e.Key, e.CreatedAt, e.Clock.Format(time.RFC3339))
}

var errInvalidPreviewManifest = errors.New("invalid preview manifest")

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
	return loadPreviewSnapshot(ctx, store, prNumber, commitSHA, time.Now)
}

// loadPreviewSnapshot is LoadPreviewSnapshot with the clock injected so
// tests exercise skew without a mutable package variable.
func loadPreviewSnapshot(ctx context.Context, store blob.Store, prNumber int, commitSHA string, now func() time.Time) (PreviewSnapshot, error) {
	if store == nil || prNumber == 0 {
		return PreviewSnapshot{}, nil
	}
	best, err := newestPreviewManifest(ctx, store, prNumber, commitSHA, now)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	if best == nil {
		slog.Debug("preview lookup: no matching preview manifest for sha", "pr", prNumber, "sha", commitSHA)
		return PreviewSnapshot{}, nil
	}
	slog.Debug("preview lookup: best manifest", "run_id", best.RunID, "created_at", best.CreatedAt, "stack_count", len(best.Stacks))

	// Future-dated manifests never reach this function: selection skips
	// them (newestPreviewManifest) and fails with FuturePreviewError when
	// nothing valid remains, so a future stamp can never count as fresh.
	createdAt, err := time.Parse(time.RFC3339, best.CreatedAt)
	if err != nil {
		return PreviewSnapshot{}, fmt.Errorf("preview manifest %q has invalid created_at %q: %w", best.RunID, best.CreatedAt, err)
	}
	age := now().Sub(createdAt)
	snapshot := PreviewSnapshot{
		statuses:     make(map[string]PreviewStatus, len(best.Stacks)),
		stackCount:   len(best.Stacks),
		allSucceeded: len(best.Stacks) > 0,
	}
	for _, ss := range best.Stacks {
		if strings.TrimSpace(ss.Project) == "" || strings.TrimSpace(ss.Stack) == "" {
			return PreviewSnapshot{}, fmt.Errorf("%w %q: invalid stack reference", errInvalidPreviewManifest, best.RunID)
		}
		ref := ss.Ref()
		if _, exists := snapshot.statuses[ref]; exists {
			return PreviewSnapshot{}, fmt.Errorf("%w %q: duplicate stack %q", errInvalidPreviewManifest, best.RunID, ref)
		}
		succeeded := false
		switch ss.Status {
		case summary.StatusPlanned, summary.StatusNoOp:
			succeeded = true
		case summary.StatusError:
			snapshot.allSucceeded = false
		default:
			return PreviewSnapshot{}, fmt.Errorf("%w %q: invalid status %q for stack %q", errInvalidPreviewManifest, best.RunID, ss.Status, ref)
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

// PlanSucceededForPR reports whether the shared snapshot for one PR and commit
// exists and has no stack errors.
func PlanSucceededForPR(ctx context.Context, store blob.Store, prNumber int, commitSHA string) (bool, error) {
	snapshot, err := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	return snapshot.Succeeded(), err
}

// FindPreviewForStack loads the shared snapshot and returns one stack status.
func FindPreviewForStack(ctx context.Context, store blob.Store, prNumber int, commitSHA, stackRef string) (PreviewStatus, error) {
	snapshot, err := LoadPreviewSnapshot(ctx, store, prNumber, commitSHA)
	return snapshot.StackStatus(stackRef), err
}

// PreviewedStackRefs returns the stack refs in the selected snapshot.
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
func newestPreviewManifest(ctx context.Context, store blob.Store, prNumber int, commitSHA string, now func() time.Time) (*manifest, error) {
	prefix := fmt.Sprintf("runs/pr-%d/", prNumber)
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list preview manifests: %w", err)
	}
	var best *manifest
	var bestCreatedAt time.Time
	var skippedFuture []struct {
		key       string
		createdAt string
		clock     time.Time
	}
	for _, k := range keys {
		runID, identityBound, candidate := previewManifestCandidate(k, prefix, commitSHA)
		if !candidate {
			continue
		}
		data, _, err := filesystem.ReadBytes(ctx, store, k)
		if err != nil {
			if !identityBound {
				slog.Debug("skip unreadable unbound preview manifest", "key", k, "err", err)
				continue
			}
			return nil, fmt.Errorf("read preview manifest %q: %w", k, err)
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			if !identityBound {
				slog.Debug("skip malformed unbound preview manifest", "key", k, "err", err)
				continue
			}
			return nil, fmt.Errorf("decode preview manifest %q: %w", k, err)
		}
		if m.Op != "preview" || m.CommitSHA != commitSHA {
			if identityBound {
				return nil, fmt.Errorf("%w %q: content does not match artifact identity", errInvalidPreviewManifest, k)
			}
			continue
		}
		if m.PR != prNumber {
			return nil, fmt.Errorf("%w %q: PR %d, want %d", errInvalidPreviewManifest, k, m.PR, prNumber)
		}
		if m.RunID != runID {
			return nil, fmt.Errorf("%w %q: run_id %q, want %q", errInvalidPreviewManifest, k, m.RunID, runID)
		}
		createdAt, parseErr := time.Parse(time.RFC3339, m.CreatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("preview manifest %q has invalid created_at %q: %w", k, m.CreatedAt, parseErr)
		}
		// A future-dated manifest never counts as fresh and never wins
		// selection: skip it with the object key and its stamp in the
		// warning. Selection then falls to the newest VALID manifest, so a
		// fresh /reeve preview clears the refusal.
		if ahead := createdAt.Sub(now()); ahead > maxPreviewClockSkew {
			slog.Warn("preview manifest dated in the future; skipped",
				"key", k, "created_at", m.CreatedAt, "clock", now().Format(time.RFC3339))
			skippedFuture = append(skippedFuture, struct {
				key       string
				createdAt string
				clock     time.Time
			}{k, m.CreatedAt, now()})
			continue
		}
		if best == nil || createdAt.After(bestCreatedAt) ||
			(createdAt.Equal(bestCreatedAt) && newerPreviewRunID(m.RunID, best.RunID, commitSHA)) {
			c := m
			best = &c
			bestCreatedAt = createdAt
		}
	}
	// Nothing valid remained but future-dated manifests were present: fail
	// closed with the typed refusal naming the newest skipped manifest.
	// Once a current preview exists, the next lookup selects it - the skip
	// is what breaks the "re-run never clears it" loop.
	if best == nil && len(skippedFuture) > 0 {
		newest := skippedFuture[0]
		for _, sk := range skippedFuture[1:] {
			if sk.createdAt > newest.createdAt {
				newest = sk
			}
		}
		return nil, &FuturePreviewError{Key: newest.key, CreatedAt: newest.createdAt, Clock: newest.clock}
	}
	return best, nil
}

func newerPreviewRunID(candidate, current, commitSHA string) bool {
	candidateRun, candidateAttempt, candidateOK := previewRunOrder(candidate, commitSHA)
	currentRun, currentAttempt, currentOK := previewRunOrder(current, commitSHA)
	if candidateOK && currentOK {
		if candidateRun != currentRun {
			return candidateRun > currentRun
		}
		if candidateAttempt != currentAttempt {
			return candidateAttempt > currentAttempt
		}
	}
	return candidate > current
}

func previewRunOrder(runID, commitSHA string) (int, int, bool) {
	const prefix = "run-"
	if !strings.HasPrefix(runID, prefix) {
		return 0, 0, false
	}
	suffix := "-" + artifactSHA(commitSHA)
	if !strings.HasSuffix(runID, suffix) {
		suffix = "-" + shortSHA(commitSHA)
		if !strings.HasSuffix(runID, suffix) {
			return 0, 0, false
		}
	}

	identity := strings.TrimSuffix(strings.TrimPrefix(runID, prefix), suffix)
	parts := strings.Split(identity, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, false
	}
	runNumber, err := strconv.Atoi(parts[0])
	if err != nil || runNumber < 0 {
		return 0, 0, false
	}
	if len(parts) == 1 {
		return runNumber, 0, true
	}
	runAttempt, err := strconv.Atoi(parts[1])
	if err != nil || runAttempt <= 0 {
		return 0, 0, false
	}
	return runNumber, runAttempt, true
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
	if strings.HasPrefix(runID, "run-") {
		if strings.HasSuffix(runID, "-"+artifactSHA(commitSHA)) {
			return runID, true, true
		}
		if strings.HasSuffix(runID, "-"+shortSHA(commitSHA)) {
			return runID, false, true
		}
		return "", false, false
	}
	return runID, false, true
}
