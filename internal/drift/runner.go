package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/iac"
	reeveotel "github.com/FynxLabs/reeve/internal/observability/otel"
)

// Engine is the subset of iac capabilities the drift runner needs.
type Engine interface {
	Name() string
	EnumerateStacks(ctx context.Context, root string) ([]discovery.Stack, error)
	DriftCheck(ctx context.Context, stack discovery.Stack, opts iac.PreviewOpts, refreshFirst bool) (iac.PreviewResult, error)
}

// AuthResolver returns env vars for a stack (usually via run.ResolveAuthEnv).
type AuthResolver func(ctx context.Context, stackRef string) (map[string]string, error)

// Options configures a drift run.
type Options struct {
	Engine   Engine
	RepoRoot string
	Decls    []discovery.Declaration
	Filter   discovery.Filter
	// Patterns further narrow the targets. Empty = all declared.
	IncludePatterns []string
	ExcludePatterns []string
	// RefreshFirst triggers `pulumi refresh` before the check.
	RefreshFirst bool
	// Freshness: skip a stack if its last successful check is within
	// FreshnessWindow. Zero disables.
	FreshnessWindow time.Duration
	// Bootstrap: how to handle first runs (no state file).
	BootstrapMode string // baseline | alert_all | require_manual
	// RenotifyAfter enables flap damping for drift notifications (see
	// dampNotification). Zero disables damping: every detection notifies.
	RenotifyAfter time.Duration
	// PerStackTimeout bounds a single stack's drift check attempt (wall clock).
	// Zero disables the bound (the pre-existing behavior). A stack that overruns
	// is classified as a check error (check_failed) with a timeout reason, and
	// the engine process is freed via context cancellation; the run continues
	// with the remaining stacks. A timeout is a run error, never a transient the
	// retry logic should re-attempt.
	PerStackTimeout time.Duration
	// AuthResolver acquires creds per stack.
	AuthResolver AuthResolver
	// Parallel caps concurrent checks. Default 1.
	Parallel int
	// StateStore + SuppressionStore persist outcomes.
	StateStore       *StateStore
	SuppressionStore *SuppressionStore
	// RunID is used for the run artifact prefix.
	RunID string
	// Redactor scrubs stdout.
	Redactor *redact.Redactor
	// OTEL is optional. nil is safe.
	OTEL *reeveotel.Provider
	// PROverlap is optional. If set, drifted stacks are annotated with
	// open PRs touching their paths.
	PROverlap PROverlapFinder
	// RetryOnTransientError bounds how many times a per-stack check is retried
	// on a transient failure (network error, expired credentials). Zero (the
	// default) means no retries. Non-transient failures are never retried.
	RetryOnTransientError int
	// RetryBackoff is the base delay before a network retry; the Nth retry
	// waits RetryBackoff*2^(N-1), capped at retryBackoffMax. Zero disables the
	// wait (used by tests). An immediate retry against a throttling API almost
	// always throttles again, so command wiring sets a real base. Credential
	// rebinds are not delayed (a rebind is not a throttle).
	RetryBackoff time.Duration
	// Classification filters engine diffs (ignore_properties / ignore_resources
	// / treat_as_drift) before a stack is classified as drift. nil / empty
	// leaves the engine's raw verdict untouched.
	Classification *Classification
	// PermanentSuppressions silence drift-lifecycle dispatch for matching
	// stacks unconditionally. Matched stacks are still checked and their state
	// persisted; check_failed is never suppressed.
	PermanentSuppressions []PermanentSuppression
	// Now is injectable for tests.
	Now func() time.Time
}

// RunOutput aggregates per-stack results and events.
type RunOutput struct {
	RunID      string
	Items      []Item
	Skipped    []string // "project/stack" refs
	Events     []Event  // parallel to Items (zero-value Event = none)
	StartedAt  time.Time
	FinishedAt time.Time
	// OverlapWarning is set when the open-PR overlap scan could not check
	// every PR (fetch failure or scan cap). The per-item OverlappingPRs are
	// then a lower bound, never proof of "no overlap"; the report surfaces
	// this warning naming the PRs that could not be checked.
	OverlapWarning string
}

// Item is one stack's drift check outcome.
type Item struct {
	Project string
	Stack   string
	Env     string
	Outcome Outcome
	Event   Event
	// NotifyEvent is the event actually dispatched to channels. Usually
	// equal to Event; flap damping (Options.RenotifyAfter) may silence it
	// (EventNone) or upgrade an ongoing episode to a drift_detected
	// re-alert. Reports, exit_on, and OTEL keep using Event - damping only
	// affects notification delivery.
	NotifyEvent    Event
	Counts         iac.PreviewResult
	Fingerprint    string
	Error          string
	DurationMS     int64
	Suppressed     bool
	SuppressReason string
	OverlappingPRs []OverlappingPR
	// OngoingSince carries the persisted episode start (state.OngoingSince
	// after classification) so run-level consumers (OTEL) don't have to
	// re-load state per item.
	OngoingSince time.Time
	// CheckRecovered marks the first successful check after one or more
	// failed ones. Carried separately from Event because recovery can
	// coincide with any classification (including a silent EventNone);
	// NotifyPayloads emits an additional check_recovered payload for it.
	CheckRecovered bool
}

// OverlappingPR is an open PR that touches paths owned by a drifted stack.
// Populated by the runner when the optional PROverlapFinder is supplied.
type OverlappingPR struct {
	Number   int       `json:"number"`
	Author   string    `json:"author"`
	OpenedAt time.Time `json:"opened_at"`
	HeadSHA  string    `json:"head_sha"`
	Paths    []string  `json:"paths,omitempty"`
}

// PROverlapFinder resolves open PRs touching the given paths. Usually
// backed by the GitHub VCS adapter's ListOpenPRsTouchingPaths.
type PROverlapFinder interface {
	FindOverlappingPRs(ctx context.Context, paths []string) ([]OverlappingPR, error)
}

// Ref returns "project/stack".
func (i Item) Ref() string { return i.Project + "/" + i.Stack }

// Run executes drift checks for every declared-and-included stack. State
// transitions persist; events surface via RunOutput.Events.
func Run(ctx context.Context, opts Options) (*RunOutput, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	started := now()
	if opts.RunID == "" {
		opts.RunID = fmt.Sprintf("drift-%s", started.UTC().Format("20060102T150405Z"))
	}
	if opts.Parallel < 1 {
		opts.Parallel = 1
	}
	if opts.Redactor == nil {
		opts.Redactor = redact.New()
	}

	enum, err := opts.Engine.EnumerateStacks(ctx, opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerate: %w", err)
	}
	declared := discovery.Resolve(enum, opts.Decls, opts.Filter)
	targets := filterPatterns(declared, opts.IncludePatterns, opts.ExcludePatterns)

	sem := make(chan struct{}, opts.Parallel)
	var mu sync.Mutex
	items := make([]Item, 0, len(targets))
	events := make([]Event, 0, len(targets))
	skipped := []string{}
	var wg sync.WaitGroup

	for _, s := range targets {
		s := s
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			item, ev, skip, reason := runOne(ctx, opts, s, now())
			mu.Lock()
			defer mu.Unlock()
			if skip {
				skipped = append(skipped, s.Ref()+":"+reason)
				return
			}
			items = append(items, item)
			events = append(events, ev)
		}()
	}
	wg.Wait()

	// Attach overlapping-PR info to drifted items. One scan for every
	// drifted path (the per-PR file listing is the expensive part; scanning
	// once per drifted stack multiplied it), then match PRs to items
	// locally. A scan failure degrades to a warning - it must never read as
	// "no overlap".
	overlapWarning := ""
	if opts.PROverlap != nil {
		pathByItem := map[int]string{}
		var paths []string
		for i, it := range items {
			if it.Outcome != OutcomeDriftDetected {
				continue
			}
			if path := stackPath(targets, it.Project, it.Stack); path != "" {
				pathByItem[i] = path
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			prs, err := opts.PROverlap.FindOverlappingPRs(ctx, paths)
			for i, path := range pathByItem {
				for _, pr := range prs {
					if prTouchesPath(pr, path) {
						items[i].OverlappingPRs = append(items[i].OverlappingPRs, pr)
					}
				}
			}
			if err != nil {
				overlapWarning = "open-PR overlap scan incomplete - overlap info may be missing: " + err.Error()
			}
		}
	}

	finished := now()
	out := &RunOutput{
		RunID:          opts.RunID,
		Items:          items,
		Skipped:        skipped,
		Events:         events,
		StartedAt:      started,
		FinishedAt:     finished,
		OverlapWarning: overlapWarning,
	}

	// Emit OTEL metrics for the whole run.
	if opts.OTEL != nil {
		runOutcome := "success"
		stacksInDriftByEnv := driftEnvCounts(items)
		for i, it := range items {
			opts.OTEL.RecordDriftDuration(ctx, it.Project, it.Stack, it.Env, float64(it.DurationMS)/1000.0)
			ev := events[i]
			opts.OTEL.RecordDriftDetection(ctx, it.Project, it.Stack, it.Env, string(it.Outcome))
			if it.Outcome == OutcomeError {
				runOutcome = "failed"
			}
			// ongoing_duration: hours drifted for ongoing items (episode
			// start rides on the item - no per-item state re-load), reset
			// to zero when the drift resolves so the gauge doesn't report
			// a stale age forever.
			switch {
			case ev == EventDriftOngoing && !it.OngoingSince.IsZero():
				opts.OTEL.RecordOngoingDuration(ctx, it.Project, it.Stack, finished.Sub(it.OngoingSince).Hours())
			case ev == EventDriftResolved:
				opts.OTEL.RecordOngoingDuration(ctx, it.Project, it.Stack, 0)
			}
		}
		for env, n := range stacksInDriftByEnv {
			opts.OTEL.RecordStacksInDrift(ctx, env, n)
		}
		opts.OTEL.RecordDriftRun(ctx, runOutcome)
	}

	return out, nil
}

// driftEnvCounts returns the drifted-stack count per env, with an explicit
// zero entry for every env observed this run. The zeros matter: the
// stacks_in_drift gauge is only written when sampled, so an env that
// recovered must be actively reset to 0 or dashboards keep showing its
// last non-zero value forever.
func driftEnvCounts(items []Item) map[string]int64 {
	counts := map[string]int64{}
	for _, it := range items {
		if _, ok := counts[it.Env]; !ok {
			counts[it.Env] = 0
		}
		if it.Outcome == OutcomeDriftDetected {
			counts[it.Env]++
		}
	}
	return counts
}

func runOne(ctx context.Context, opts Options, s discovery.Stack, now time.Time) (Item, Event, bool, string) {
	ref := s.Ref()
	item := Item{Project: s.Project, Stack: s.Name, Env: s.Env}

	// Suppression check. A load error is NOT "not suppressed": proceeding
	// could alert on a stack an operator explicitly silenced, so fail the
	// stack's check loudly instead (flows into exit_on.run_error).
	if opts.SuppressionStore != nil {
		sup, ok, err := opts.SuppressionStore.Get(ctx, s.Project, s.Name)
		if err != nil {
			item.Outcome = OutcomeError
			item.Error = opts.Redactor.Redact(fmt.Sprintf("load suppression state: %v", err))
			item.NotifyEvent = EventCheckFailed
			return item, EventCheckFailed, false, ""
		}
		if ok && sup.Active(now) {
			item.Suppressed = true
			item.Outcome = OutcomeSkipped
			return item, EventNone, true, "suppressed"
		}
	}

	// Prior state. A load error (corrupted/unreadable state file, transport
	// failure) must not silently degrade to first-run semantics - that would
	// re-fire baseline alerts or falsely classify ongoing drift as new. Fail
	// the stack's check instead; the state file is untouched for a retry.
	prev := State{Project: s.Project, Stack: s.Name}
	if opts.StateStore != nil {
		p, err := opts.StateStore.Load(ctx, s.Project, s.Name)
		if err != nil {
			item.Outcome = OutcomeError
			item.Error = opts.Redactor.Redact(fmt.Sprintf("load drift state: %v", err))
			item.NotifyEvent = EventCheckFailed
			return item, EventCheckFailed, false, ""
		}
		prev = p
	}
	if opts.FreshnessWindow > 0 && prev.LastOutcome != OutcomeDriftDetected {
		if !prev.LastSuccessfulAt.IsZero() && now.Sub(prev.LastSuccessfulAt) < opts.FreshnessWindow {
			item.Outcome = OutcomeSkipped
			return item, EventNone, true, "fresh"
		}
	}

	// Bootstrap guard: first run with require_manual → refuse.
	if prev.LastCheckedAt.IsZero() && opts.BootstrapMode == "require_manual" {
		item.Outcome = OutcomeError
		item.Error = "first run with state_bootstrap.mode=require_manual; run `reeve drift bootstrap` to record the baseline"
		item.NotifyEvent = EventCheckFailed
		return item, EventCheckFailed, false, ""
	}

	// Permanent suppression (config-level, always-on): matched stacks are
	// still checked and their state persisted, but their drift-lifecycle
	// dispatch is silenced later. Resolved here so it can also flag the item.
	permSup, permMatched := matchPermanentSuppression(opts.PermanentSuppressions, ref, now)
	if permMatched {
		item.Suppressed = true
		item.SuppressReason = permSup.Reason
	}

	// Resolve auth and run the drift check, retrying transient failures
	// (network errors, expired credentials) up to RetryOnTransientError
	// times. Expired credentials trigger a single rebind (re-resolve auth);
	// non-transient failures are never retried. Context cancellation between
	// retries stops the loop.
	var (
		env      map[string]string
		res      iac.PreviewResult
		checkErr error
		haveAuth bool
		timedOut bool
	)
	retriesUsed, rebindUsed := 0, false
	start := time.Now()
	for {
		timedOut = false
		if opts.AuthResolver != nil && !haveAuth {
			e, err := opts.AuthResolver(ctx, ref)
			if err != nil {
				res, checkErr = iac.PreviewResult{}, err
			} else {
				env, haveAuth = e, true
				for _, v := range env {
					opts.Redactor.AddSecret(v)
				}
			}
		}
		if haveAuth || opts.AuthResolver == nil {
			// Per-stack timeout bounds THIS attempt on the wall clock, derived
			// from the run context so a run-wide cancellation (SIGINT) still
			// propagates. The engine honors ctx cancellation (SetupGracefulStop),
			// so the deadline frees the engine process rather than leaking it.
			checkCtx := ctx
			var cancel context.CancelFunc
			if opts.PerStackTimeout > 0 {
				checkCtx, cancel = context.WithTimeout(ctx, opts.PerStackTimeout)
			}
			// TimeoutSec mirrors the per-stack bound so the adapter's OWN
			// internal timeout (default 10m) does not fire first and misreport
			// a per-stack timeout as a generic engine error. 0 keeps the
			// adapter default (no configured bound).
			res, checkErr = opts.Engine.DriftCheck(checkCtx, s, iac.PreviewOpts{
				Cwd:        opts.RepoRoot + "/" + s.Path,
				Env:        env,
				TimeoutSec: int(opts.PerStackTimeout / time.Second),
			}, opts.RefreshFirst)
			// A per-stack timeout is the deadline firing while the parent ctx is
			// still live (distinguished from a run-wide cancellation). Computed
			// before cancel() so checkCtx.Err() still reports the deadline.
			timedOut = opts.PerStackTimeout > 0 &&
				checkCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
			if cancel != nil {
				cancel()
			}
		}

		msg := failureText(res, checkErr)
		if msg == "" {
			break // check produced a verdict
		}
		if timedOut {
			break // a timeout is NOT transient - never retry it
		}
		if retriesUsed >= opts.RetryOnTransientError || ctx.Err() != nil {
			break
		}
		switch classifyDriftError(msg) {
		case errTransientNetwork:
			retriesUsed++
			slog.Warn("drift: retrying after transient network error",
				"stack", ref, "attempt", retriesUsed, "max", opts.RetryOnTransientError,
				"error", opts.Redactor.Redact(msg))
			// Back off before retrying: the transient set includes throttling
			// signatures, and an instant retry against a throttling API just
			// re-throttles. Sleeps on the run ctx (checkCtx is already
			// cancelled here) so SIGINT aborts the wait; a cancelled wait falls
			// through to stop retrying.
			if !backoffSleep(ctx, opts.RetryBackoff, retriesUsed) {
				break
			}
			continue
		case errAuthExpired:
			if rebindUsed {
				break // already rebound once; expiry persists
			}
			rebindUsed, haveAuth = true, false
			retriesUsed++
			slog.Warn("drift: rebinding and retrying after expired credentials",
				"stack", ref, "attempt", retriesUsed, "max", opts.RetryOnTransientError)
			continue
		}
		break // non-transient failure
	}
	item.DurationMS = time.Since(start).Milliseconds()

	// Filter drift noise (ignore_properties / ignore_resources /
	// treat_as_drift) before classification. Only when the check succeeded and
	// exposed a structured resource set; otherwise the raw verdict stands.
	if checkErr == nil && res.Error == "" && !opts.Classification.empty() && len(res.Resources) > 0 {
		if kept, removed := opts.Classification.filter(res.Resources); removed {
			res.Resources = kept
			res.Counts, res.DriftedURNs = applyCounts(kept)
		}
	}

	item.Counts = res
	item.Counts.PlanSummary = opts.Redactor.Redact(res.PlanSummary)
	item.Counts.FullPlan = opts.Redactor.Redact(res.FullPlan)

	// timedOut (tracked across the retry loop above) marks a per-stack timeout -
	// the deadline firing while the parent ctx stayed live, distinct from a
	// run-wide cancellation. It is a check error attributed to timeout_per_stack.

	// A failed check (timeout, non-nil error, or an Error string) is NEVER "no
	// drift". Treating an empty/failed result as no-drift would falsely resolve
	// an active drift alert, so classify it as an error and fail closed.
	switch {
	case timedOut:
		item.Outcome = OutcomeError
		item.Error = fmt.Sprintf("stack check exceeded timeout_per_stack=%s", opts.PerStackTimeout)
	case checkErr != nil || res.Error != "":
		item.Outcome = OutcomeError
		if res.Error != "" {
			item.Error = opts.Redactor.Redact(res.Error)
		} else {
			item.Error = opts.Redactor.Redact(checkErr.Error())
		}
	case res.Counts.Total() > 0:
		item.Outcome = OutcomeDriftDetected
		// Fingerprint only the drifted resources so a change in *which*
		// resources drift re-fires the alert (see Classify fingerprint check).
		item.Fingerprint = Fingerprint(res.DriftedURNs)
	default:
		item.Outcome = OutcomeNoDrift
	}

	result := Result{
		Project: s.Project, Stack: s.Name,
		Outcome: item.Outcome, Fingerprint: item.Fingerprint,
		CheckedAt: now, ErrorMessage: item.Error,
	}
	ev, next := Classify(prev, result)

	// Baseline bootstrap: on the first-ever check, accept whatever state we
	// find (including pre-existing drift) as the baseline and stay silent.
	// The state - fingerprint included - is still persisted, so only *new*
	// drift beyond the baseline alerts on later runs. Previously this
	// recorded no_drift and dropped the fingerprint, so identical drift
	// re-fired as a fresh detection on the second run.
	if prev.LastCheckedAt.IsZero() && opts.BootstrapMode == "baseline" && ev != EventCheckFailed {
		ev = EventNone
	}
	item.Event = ev
	// Flap damping (#47): decide what (if anything) actually goes to channels,
	// and stamp the notification time into the persisted state.
	notifyEv, notified := dampNotification(prev, ev, now, opts.RenotifyAfter)
	item.NotifyEvent = notifyEv
	if notified {
		next.LastNotifiedAt = now
	}
	// First success after failed checks: the check_failed condition has
	// cleared. Recorded on the item (not as the classification event) so
	// channels holding a check-failed incident/issue open get the
	// all-clear even when the classification itself is silent.
	item.CheckRecovered = prev.ConsecutiveErrors > 0 && item.Outcome != OutcomeError
	item.OngoingSince = next.OngoingSince

	// Persist state BEFORE applying permanent suppression (damping has already
	// stamped next.LastNotifiedAt) so a suppressed stack's resolution is still
	// tracked; then silence its drift-lifecycle dispatch. A failed check is
	// never suppressed - suppressing drift you have accepted must not hide the
	// checker itself breaking.
	if opts.StateStore != nil {
		if err := opts.StateStore.Save(ctx, next); err != nil {
			// A lost save means the next run re-classifies from stale state
			// (worst case: a duplicate alert). Never silently - log it.
			slog.Warn("drift: state save failed", "stack", ref, "err", err)
		}
	}
	// Permanent-suppression silencing (#50). Two separate notification signals
	// must both be quieted or a suppressed stack still notifies: the
	// classification event ev (drives reports/exit_on/OTEL) AND the
	// damping-produced NotifyEvent (drives channel dispatch). A check_failed is
	// never silenced.
	if permMatched && ev != EventCheckFailed {
		ev = EventNone
		item.NotifyEvent = EventNone
	}
	item.Event = ev
	return item, ev, false, ""
}

// prTouchesPath reports whether any of the PR's changed files fall under
// the stack path (exact match or directory prefix). Mirrors the VCS
// adapter's own path intersection so a multi-path scan can be re-split
// per stack.
func prTouchesPath(pr OverlappingPR, path string) bool {
	for _, f := range pr.Paths {
		if f == path || strings.HasPrefix(f, path+"/") {
			return true
		}
	}
	return false
}

// failureText returns the check's failure message, preferring the structured
// PreviewResult.Error over a returned error. Empty means the check produced a
// verdict (no failure).
func failureText(res iac.PreviewResult, err error) string {
	if res.Error != "" {
		return res.Error
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// stackPath returns the filesystem path declared for a stack ref,
// searching the enumerated targets. Empty if not found.
func stackPath(stacks []discovery.Stack, project, name string) string {
	for _, s := range stacks {
		if s.Project == project && s.Name == name {
			return s.Path
		}
	}
	return ""
}

func filterPatterns(stacks []discovery.Stack, include, exclude []string) []discovery.Stack {
	out := make([]discovery.Stack, 0, len(stacks))
outer:
	for _, s := range stacks {
		ref := s.Ref()
		if len(include) > 0 {
			hit := false
			for _, p := range include {
				if ok, _ := doublestar.Match(p, ref); ok {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		for _, p := range exclude {
			if ok, _ := doublestar.Match(p, ref); ok {
				continue outer
			}
		}
		out = append(out, s)
	}
	return out
}

// --- artifact writing ---

// WriteArtifacts persists run artifacts under drift/runs/{run-id}/.
// Returns the rendered report body (for callers that want to forward
// to $GITHUB_STEP_SUMMARY).
func WriteArtifacts(ctx context.Context, store blob.Store, out *RunOutput, report string) error {
	if store == nil {
		return nil
	}
	manifest := map[string]any{
		"run_id":      out.RunID,
		"started_at":  out.StartedAt.Format(time.RFC3339),
		"finished_at": out.FinishedAt.Format(time.RFC3339),
		"item_count":  len(out.Items),
		"skipped":     out.Skipped,
	}
	if out.OverlapWarning != "" {
		manifest["overlap_warning"] = out.OverlapWarning
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	if _, err := store.Put(ctx, fmt.Sprintf("drift/runs/%s/manifest.json", out.RunID), bytes.NewReader(mb)); err != nil {
		return err
	}
	for _, it := range out.Items {
		data, _ := json.MarshalIndent(it, "", "  ")
		key := fmt.Sprintf("drift/runs/%s/results/%s-%s.json", out.RunID, it.Project, it.Stack)
		if _, err := store.Put(ctx, key, bytes.NewReader(data)); err != nil {
			return err
		}
	}
	if report != "" {
		if _, err := store.Put(ctx, fmt.Sprintf("drift/runs/%s/report.md", out.RunID), bytes.NewReader([]byte(report))); err != nil {
			return err
		}
	}
	return nil
}
