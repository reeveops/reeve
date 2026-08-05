package run

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/FynxLabs/reeve/internal/audit"
	"github.com/FynxLabs/reeve/internal/auth"
	"github.com/FynxLabs/reeve/internal/blob"
	blocks "github.com/FynxLabs/reeve/internal/blob/locks"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
	"github.com/FynxLabs/reeve/internal/core/render"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/vcs"
)

// refreshEngine is what run/refresh.go needs from an IaC adapter.
type refreshEngine interface {
	Engine
	iac.Refresher
	Capabilities() iac.Capabilities
}

// refreshVCS is the narrow VCS surface a refresh needs: the PR (for the
// fork/draft gates and the head SHA), its changed files (for scoping), and
// somewhere to report.
type refreshVCS interface {
	prReader
	commentPoster
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
}

// RefreshInput wires dependencies and run context for a refresh.
type RefreshInput struct {
	PRNumber     int
	CommitSHA    string
	RunNumber    int
	CIRunURL     string
	RepoRoot     string
	RepoFull     string
	Actor        string
	Engine       refreshEngine
	Config       *schemas.Engine
	Shared       *schemas.Shared
	AuthConfig   *schemas.Auth
	AuthRegistry *auth.Registry
	Blob         blob.Store
	Locks        *blocks.Store
	VCS          refreshVCS // nil for --local
	AuditWriter  *audit.Writer
	// DryRun reports what a refresh would reconcile and writes no state.
	DryRun bool
	// All refreshes every declared stack instead of the ones this PR's
	// changed files map to. State drifts for reasons unrelated to the PR, so
	// "refresh everything" is a legitimate ask - it is just not the default,
	// because a PR-scoped command that silently touched every stack's state
	// is the same blast-radius surprise apply used to have.
	All bool
	// Local skips VCS entirely and refreshes every declared stack.
	Local bool
}

// RefreshOutput bundles the artifacts of a refresh run.
type RefreshOutput struct {
	Stacks      []summary.StackSummary
	CommentBody string
	RunID       string
	DurationSec int
	Failed      bool
	Blocked     bool
}

// Refresh reconciles engine state with live infrastructure for the stacks
// in scope. It changes no infrastructure - it rewrites the engine's record
// of it - which is why its gate set differs from apply's:
//
//	enforced: fork-PR policy, draft PR, freeze windows, per-stack locks
//	not enforced: approvals, required checks, preview freshness
//
// The gates that are missing are the ones that gate a CHANGE SET, and a
// refresh has none: there is no plan to be stale, nothing to approve, and
// no relationship to the PR's diff. What remains is the set that protects
// concurrent operations (locks) and scheduled quiet periods (freeze).
// Authorization comes from the same comment-authorization check that
// dispatches every other reeve command.
//
// It is still a state mutation, so every stack is refreshed under its own
// lock and the run is audited.
func Refresh(ctx context.Context, in RefreshInput) (*RefreshOutput, error) {
	start := time.Now()
	runID := fmt.Sprintf("refresh-%d-%s", in.RunNumber, shortSHA(in.CommitSHA))

	if !in.Engine.Capabilities().SupportsRefresh {
		return nil, fmt.Errorf("engine %s does not support refresh", in.Engine.Name())
	}
	if err := PulumiLogin(ctx, in.Config); err != nil {
		return nil, err
	}

	enum, err := in.Engine.EnumerateStacks(ctx, in.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerate stacks: %w", err)
	}
	decls, filter := declarationsFromConfig(in.Config)
	declared := discovery.Resolve(enum, decls, filter)

	target := declared
	if !in.Local && in.VCS != nil {
		pr, gerr := in.VCS.GetPR(ctx, in.PRNumber)
		if gerr != nil {
			return nil, fmt.Errorf("get pr: %w", gerr)
		}
		forkOptIn := in.Shared != nil && in.Shared.Apply.AllowForkPRs
		if pr.IsDraft {
			return nil, fmt.Errorf("PR #%d is in draft - convert to ready for review before refreshing state", in.PRNumber)
		}
		if pr.IsFork && !forkOptIn {
			return nil, fmt.Errorf("PR #%d is from a fork - refresh writes engine state and is denied by default; see docs/auth.md#fork-pr-policy", in.PRNumber)
		}
		if !in.All {
			changed, cerr := in.VCS.ListChangedFiles(ctx, in.PRNumber)
			if cerr != nil {
				return nil, fmt.Errorf("list changed files: %w", cerr)
			}
			// Same anti-broadening rule apply uses: a changed file that maps
			// to no stack must not turn a scoped command into a global one.
			res := discovery.AffectedDetailed(declared, changed, changeMappingFromConfig(in.Config))
			target = res.Stacks
			if res.Reason == discovery.ReasonBroadened {
				slog.Info("refresh scope: changed files map to no specific stack; refreshing only the precise matches",
					"unmapped", res.Unmapped)
				target = res.Matched
			}
		}
	}
	if len(target) == 0 {
		return &RefreshOutput{RunID: runID, DurationSec: int(time.Since(start).Seconds())}, nil
	}

	freezeCfg := toFreezeConfig(in.Shared)
	ttl := LockTTL(in.Shared)
	now := time.Now()

	summaries := make([]summary.StackSummary, 0, len(target))
	anyBlocked, anyFailed := false, false
	for _, s := range target {
		ss := summary.StackSummary{Project: s.Project, Stack: s.Name, Env: s.Env}
		if ctx.Err() != nil {
			ss.Status = summary.StatusError
			ss.Error = "run cancelled before this stack was refreshed: " + ctx.Err().Error()
			anyFailed = true
			summaries = append(summaries, ss)
			continue
		}

		// Freeze applies to a refresh too: a freeze window means "nothing
		// touches this stack right now", and a state rewrite mid-incident is
		// exactly what those windows exist to prevent.
		name, active, ferr := freezeActiveFor(freezeCfg, s.Ref(), now)
		// Fail closed, exactly as apply does: a window that cannot be
		// evaluated blocks rather than letting the refresh through.
		blockReason := ""
		switch {
		case ferr != nil:
			blockReason = "freeze evaluation failed: " + ferr.Error()
		case active:
			blockReason = fmt.Sprintf("in freeze window %q", name)
		}
		if blockReason != "" {
			ss.Status = summary.StatusBlocked
			ss.Error = blockReason
			ss.Gates = []summary.GateTrace{{Gate: "not_in_freeze", Outcome: "fail", Reason: blockReason}}
			anyBlocked = true
			summaries = append(summaries, ss)
			continue
		}

		// A dry run reads providers and writes nothing, so it needs no lock:
		// taking one would block a real apply for the duration of a read.
		acquired := true
		if !in.DryRun && in.Locks != nil {
			lock, ok, lerr := in.Locks.TryAcquire(ctx, s.Project, s.Name, corelocks.Holder{
				PR: in.PRNumber, CommitSHA: in.CommitSHA, RunID: runID, Actor: in.Actor,
			}, ttl)
			if lerr != nil {
				ss.Status = summary.StatusError
				ss.Error = fmt.Sprintf("lock acquire: %v", lerr)
				anyFailed = true
				summaries = append(summaries, ss)
				continue
			}
			acquired = ok
			if !ok {
				holder := 0
				if lock.Holder != nil {
					holder = lock.Holder.PR
				}
				ss.Status = summary.StatusBlocked
				ss.BlockedBy = holder
				ss.Error = fmt.Sprintf("stack is locked by PR #%d; refresh rewrites state and must not race an apply", holder)
				ss.Gates = []summary.GateTrace{{Gate: "lock_acquirable", Outcome: "fail", Reason: ss.Error}}
				anyBlocked = true
				summaries = append(summaries, ss)
				continue
			}
		}

		redactor := BuildRedactor(in.Shared)
		// ModeApply: a refresh writes state, so it needs write credentials,
		// not the read-only preview role.
		authEnv, authCleanup, aerr := ResolveAuthEnv(ctx, in.AuthConfig, in.AuthRegistry, s.Ref(), auth.ModeApply)
		if aerr != nil {
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(aerr.Error())
			anyFailed = true
			if acquired && !in.DryRun {
				releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "auth resolve failed")
			}
			summaries = append(summaries, ss)
			continue
		}
		for _, v := range authEnv {
			redactor.AddSecret(v)
		}

		var stopHeartbeat func()
		if acquired && !in.DryRun && in.Locks != nil {
			stopHeartbeat = in.Locks.StartHeartbeat(ctx, s.Project, s.Name, corelocks.Holder{
				PR: in.PRNumber, CommitSHA: in.CommitSHA, RunID: runID, Actor: in.Actor,
			}, ttl)
		}
		res, rerr := in.Engine.Refresh(ctx, s, iac.RefreshOpts{
			Cwd:         absJoin(in.RepoRoot, s.Path),
			Env:         authEnv,
			TimeoutSec:  ApplyTimeoutSec(in.Config),
			PreviewOnly: in.DryRun,
		})
		if stopHeartbeat != nil {
			stopHeartbeat()
		}
		authCleanup()
		if !in.DryRun && in.Locks != nil {
			releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "stack refresh complete")
		}

		ss.DurationMS = res.DurationMS
		ss.Counts = res.Counts
		ss.PlanSummary = redactor.Redact(res.Summary)
		ss.FullPlan = redactor.Redact(res.Output)
		switch {
		case rerr != nil:
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(rerr.Error())
			anyFailed = true
		case res.Error != "":
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(res.Error)
			anyFailed = true
		case res.Counts.Total() == 0:
			ss.Status = summary.StatusNoOp
		default:
			ss.Status = summary.StatusPlanned
		}
		summaries = append(summaries, ss)
	}

	dur := int(time.Since(start).Seconds())
	body := render.Refresh(render.RefreshInput{
		RunNumber:   in.RunNumber,
		CommitSHA:   in.CommitSHA,
		DurationSec: dur,
		CIRunURL:    in.CIRunURL,
		DryRun:      in.DryRun,
		Stacks:      summaries,
		SortMode:    sortModeFor(in.Shared),
		StackView:   stackView(in.Shared),
	})

	pctx, endTerminal := terminalContext(ctx)
	defer endTerminal()

	if in.VCS != nil && in.PRNumber > 0 {
		if err := in.VCS.UpsertComment(pctx, in.PRNumber, body, render.RefreshMarker); err != nil {
			slog.Warn("refresh comment failed", "err", err, "pr", in.PRNumber)
		}
	}

	// A state rewrite is an operation on the operator's infrastructure
	// record; it belongs in the audit log even though it shipped no change.
	if in.AuditWriter != nil && !in.DryRun {
		outcome := "success"
		switch {
		case anyFailed:
			outcome = "failed"
		case anyBlocked:
			outcome = "blocked"
		}
		if err := in.AuditWriter.Write(pctx, audit.Entry{
			RunID:      runID,
			Op:         "refresh",
			StartedAt:  start.UTC().Format(time.RFC3339),
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
			Actor:      in.Actor,
			PR:         in.PRNumber,
			CommitSHA:  in.CommitSHA,
			Repo:       in.RepoFull,
			RunURL:     in.CIRunURL,
			Outcome:    outcome,
			Stacks:     toAuditStacks(summaries),
			DurationMS: time.Since(start).Milliseconds(),
		}); err != nil {
			slog.Error("audit write failed - refresh already ran", "err", err, "run_id", runID)
		}
	}

	return &RefreshOutput{
		Stacks:      summaries,
		CommentBody: body,
		RunID:       runID,
		DurationSec: dur,
		Failed:      anyFailed,
		Blocked:     anyBlocked,
	}, nil
}

// sortModeFor resolves the comment sort mode from shared.yaml.
func sortModeFor(s *schemas.Shared) string {
	if s != nil && s.Comments.Sort != "" {
		return s.Comments.Sort
	}
	return "status_grouped"
}
