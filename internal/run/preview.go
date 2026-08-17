package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/render"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/notify"
	reeveotel "github.com/reeveops/reeve/internal/observability/otel"
	"github.com/reeveops/reeve/internal/vcs"
)

// prReader is the subset of VCS we need for preview.
type prReader interface {
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
	ListChangedFiles(ctx context.Context, number int) ([]string, error)
}

// commentPoster is what preview writes back to.
type commentPoster interface {
	UpsertComment(ctx context.Context, number int, body, marker string) error
	PostComment(ctx context.Context, number int, body string) error
}

// Engine is what run/preview.go needs from an IaC adapter.
type Engine interface {
	iac.Enumerator
	iac.Previewer
	Name() string
}

// PreviewInput wires the dependencies and run context together.
type PreviewInput struct {
	PRNumber      int
	PRTitle       string
	CommitSHA     string
	RunNumber     int
	CIRunURL      string
	RepoRoot      string
	Engine        Engine
	Config        *schemas.Engine
	Shared        *schemas.Shared
	AuthConfig    *schemas.Auth
	AuthRegistry  *auth.Registry
	Notifications *schemas.Notifications
	// Observability is the loaded observability.yaml. Preview constructs
	// the OTEL provider itself - AFTER the pre-approval gate below - so a
	// PR that modifies observability config cannot point the OTLP exporter
	// (endpoint + headers carry expanded ${env:} credentials) at an
	// attacker collector during an automatic pre-approval preview.
	//
	// Annotation emitters (observability.yaml `annotations:`) need no gate
	// here: PostAnnotation only fires on apply/drift events, never during
	// preview.
	Observability *schemas.Observability
	Blob          blob.Store
	VCS           prReader      // may be nil for --local
	Comments      commentPoster // may be nil for --local
	// ChannelSourceFiles are the repo-relative config files the loader
	// sourced notification channels from (config.Config.ChannelSourceFiles).
	// If the PR's changed files include any of them, pre-approval events
	// (planning/plan) are NOT dispatched to channels. Empty falls back to
	// the default .reeve file names (fail closed).
	ChannelSourceFiles []string
	// ObservabilitySourceFiles is the same for observability.yaml
	// (config.Config.ObservabilitySourceFiles): if modified by the PR (or
	// the changed-file list is unavailable), OTEL init is skipped for this
	// preview. Empty falls back to ".reeve/observability.yaml".
	ObservabilitySourceFiles []string
	// Local skips change-mapping (run on all declared stacks).
	Local bool
	// LocalAuthProviders is the --local-auth override: declared provider
	// names that replace same-scope bound providers for every stack. Only
	// consulted when Local is true.
	LocalAuthProviders []string
	// Force re-runs even when this commit is already recorded as applied,
	// bypassing the already-applied guard.
	Force bool
	// Refresh reconciles engine state with live infrastructure before
	// planning, so the diff is against reality rather than against state
	// that clickops may have invalidated. Off by default: a refresh costs a
	// full provider read per stack on every push.
	Refresh bool
}

// PreviewOutput bundles the artifacts from a preview run.
type PreviewOutput struct {
	Stacks      []summary.StackSummary
	CommentBody string
	RunID       string
	DurationSec int
}

// Preview runs preview for every stack affected by the PR's changed files
// (or every declared stack if Local is true), writes artifacts to Blob,
// renders a PR comment, and - if Comments is non-nil - upserts it.
func Preview(ctx context.Context, in PreviewInput) (*PreviewOutput, error) {
	start := time.Now()

	// A local run keyed to a real PR would write PR-scoped artifacts (the
	// preview manifest) that apply's preview-freshness gate trusts - letting
	// a laptop run stand in for CI. Refuse before credentials or artifacts.
	if in.Local && in.PRNumber != 0 {
		return nil, fmt.Errorf("--local previews must not carry a PR number (got %d): local artifacts must stay outside PR context; drop --pr or run without --local", in.PRNumber)
	}

	in.CommitSHA = resolvePRHeadSHA(ctx, in.VCS, in.PRNumber, in.CommitSHA)
	slog.Debug("preview starting", "pr", in.PRNumber, "sha", in.CommitSHA, "local", in.Local)

	runID := fmt.Sprintf("run-%d-%s", in.RunNumber, shortSHA(in.CommitSHA))

	// Changed files are fetched up front (also reused for change mapping
	// below): both the pre-approval channel dispatch and the OTEL exporter
	// init must be decided BEFORE anything can reach the network.
	var changed []string
	var changedErr error
	if !in.Local && in.VCS != nil {
		changed, changedErr = in.VCS.ListChangedFiles(ctx, in.PRNumber)
	}

	// Pre-approval OTEL isolation: observability.yaml is loaded from the
	// untrusted PR HEAD and its endpoint/headers expand ${env:} references,
	// so when the PR modifies that config (or the changed-file list is
	// unavailable) the OTLP exporter is not initialized at all for this
	// preview - no connection, no headers sent. Same fail-closed semantics
	// as the notification-channel gate below; --local is unaffected.
	otelConfigured := in.Observability != nil && in.Observability.OTEL.Enabled
	suppressOTEL := false
	otelReason := ""
	if otelConfigured {
		suppressOTEL, otelReason = SuppressPreApprovalObservability(
			in.Local, in.VCS != nil, changed, changedErr, in.ObservabilitySourceFiles)
		if suppressOTEL {
			slog.Warn("SECURITY: OTEL telemetry suppressed for this pre-approval preview",
				"reason", otelReason, "pr", in.PRNumber, "sha", in.CommitSHA,
				"note", "telemetry resumes after approval/apply")
		}
	}
	var otelProvider *reeveotel.Provider
	if !suppressOTEL {
		var otelErr error
		otelProvider, otelErr = BuildOTEL(ctx, in.Observability)
		if otelErr != nil {
			slog.Warn("otel init failed", "err", otelErr)
		}
		defer func() {
			if err := otelProvider.Shutdown(ctx); err != nil {
				slog.Warn("otel shutdown failed", "err", err)
			}
		}()
	}

	// OTEL root span for this preview run. Registered after the provider
	// shutdown defer so the span ends before the exporter flushes.
	ctx, endRun := otelProvider.StartRunSpan(ctx, "preview", in.PRNumber, in.CommitSHA)
	outcome := "success"
	defer func() { endRun(outcome) }()

	// Pre-approval suppression: previews run automatically on the untrusted
	// PR HEAD, so when the PR modifies the notification config itself (or
	// the changed-file list is unavailable), no channel may receive the
	// planning/plan events - a webhook channel added in the PR could
	// otherwise exfiltrate expanded credentials before any human approves.
	notifyActive := in.PRNumber > 0 && in.Notifications != nil
	suppressChannels := false
	suppressReason := ""
	if notifyActive {
		suppressChannels, suppressReason = SuppressPreApprovalChannels(
			in.Local, in.VCS != nil, changed, changedErr, in.ChannelSourceFiles)
		if suppressChannels {
			slog.Warn("SECURITY: notification channels suppressed for this pre-approval preview",
				"reason", suppressReason, "pr", in.PRNumber, "sha", in.CommitSHA,
				"note", "channels resume after approval/apply")
		}
	}

	// Channels are built once and reused for the preview-started and
	// preview-finished events below.
	var channels []notify.Channel
	if notifyActive && !suppressChannels {
		channels = BuildNotifyChannels(ctx, in.Notifications, in.Blob, in.Comments)
		// Timeline heartbeat: preview started. PR title/author are not
		// fetched yet; the payload carries what the timeline needs (event,
		// SHA, this run's CI URL).
		if err := NotifyPREvent(ctx, channels, notify.EventPlanning, PRNotifyInput{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunURL: in.CIRunURL,
			PRTitle: in.PRTitle,
		}); err != nil {
			slog.Warn("notify planning failed", "err", err, "pr", in.PRNumber)
		}
	}

	executionEnv, executionCleanup, err := iac.ExecutionEnv()
	if err != nil {
		outcome = "failed"
		return nil, fmt.Errorf("prepare engine execution environment: %w", err)
	}
	defer executionCleanup()
	stateEnv, stateCleanup, err := ResolveStateAuthEnv(ctx, in.Config, in.AuthRegistry)
	if err != nil {
		outcome = "failed"
		return nil, err
	}
	defer stateCleanup()
	stateEnv = mergeEnv(executionEnv, stateEnv)
	if err := PulumiLogin(ctx, in.Config, stateEnv); err != nil {
		outcome = "failed"
		return nil, err
	}

	enum, err := in.Engine.EnumerateStacks(ctx, in.RepoRoot)
	if err != nil {
		outcome = "failed"
		return nil, fmt.Errorf("enumerate stacks: %w", err)
	}

	decls, filter := declarationsFromConfig(in.Config)
	declared := discovery.Resolve(enum, decls, filter)

	var target []discovery.Stack
	mappingNotice := ""
	if in.Local || in.VCS == nil {
		target = declared
		slog.Debug("preview target: all declared stacks", "count", len(target))
	} else {
		if changedErr != nil {
			outcome = "failed"
			return nil, fmt.Errorf("list changed files: %w", changedErr)
		}
		slog.Debug("changed files", "count", len(changed), "files", changed)
		cm := changeMappingFromConfig(in.Config)
		res := discovery.AffectedDetailed(declared, changed, cm)
		target = res.Stacks
		mappingNotice = mappingNoticeFor(res)
		slog.Debug("preview target: affected stacks", "count", len(target), "reason", res.Reason)
	}
	for _, s := range target {
		slog.Debug("target stack", "ref", s.Ref(), "path", s.Path)
	}

	appCfg := toApprovalsConfig(in.Shared)
	summaries := make([]summary.StackSummary, 0, len(target))
	for _, s := range target {
		ss := runPreviewOne(ctx, in, otelProvider, s, runID, stateEnv)
		rules := approvals.Resolve(appCfg, s.Ref())
		ss.RequiredApprovers = rules.Approvers
		summaries = append(summaries, ss)
	}

	sort := "status_grouped"
	if in.Shared != nil && in.Shared.Comments.Sort != "" {
		sort = in.Shared.Comments.Sort
	}
	dur := int(time.Since(start).Seconds())

	// Already-applied notice: if this commit was fully applied before and the
	// caller didn't force, flag it so reviewers know an apply re-run would be
	// a no-op (the plan still renders; preview is read-only).
	notice := mappingNotice
	if suppressChannels {
		notice = joinNotices(notice, fmt.Sprintf(
			"⚠️ Notification channels suppressed for this preview: %s; channels resume after approval/apply.",
			suppressReason))
	}
	if suppressOTEL {
		notice = joinNotices(notice, fmt.Sprintf(
			"⚠️ Telemetry (OTEL) suppressed for this preview: %s; telemetry resumes after approval/apply.",
			otelReason))
	}
	if !in.Force {
		if prior, _ := readAppliedState(ctx, in.Blob, in.PRNumber, in.CommitSHA); prior != nil {
			appliedNote := fmt.Sprintf("Commit %s was already applied on run #%d (%s). Re-running apply is a no-op unless you comment `/reeve apply --force`.",
				shortSHA(in.CommitSHA), prior.RunNumber, prior.AppliedAt)
			notice = joinNotices(notice, appliedNote)
		}
	}

	body := render.Preview(render.PreviewInput{
		Op:          "preview",
		RunNumber:   in.RunNumber,
		CommitSHA:   in.CommitSHA,
		DurationSec: dur,
		CIRunURL:    in.CIRunURL,
		Stacks:      summaries,
		SortMode:    sort,
		StackView:   stackView(in.Shared),
		Notice:      notice,
	})

	if err := writeManifest(ctx, in.Blob, in.PRNumber, runID, summaries, in.CommitSHA); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	if in.Comments != nil && in.PRNumber > 0 {
		if err := in.Comments.UpsertComment(ctx, in.PRNumber, body, render.Marker); err != nil {
			return nil, fmt.Errorf("upsert pr comment: %w", err)
		}
		autoReady := in.Shared != nil && in.Shared.Apply.AutoReady
		helpBody := render.BuildHelpComment(autoReady)
		if err := in.Comments.UpsertComment(ctx, in.PRNumber, helpBody, render.HelpMarker); err != nil {
			// Help comment is informational; failure must not block the run.
			slog.Warn("upsert help comment failed", "err", err, "pr", in.PRNumber)
		}
	}

	// Fetch PR metadata once for author + title (used by Slack).
	var prAuthor, prTitle string
	if in.PRNumber > 0 && !in.Local {
		if pr, err := in.VCS.GetPR(ctx, in.PRNumber); err == nil {
			prAuthor = pr.Author
			prTitle = pr.Title
		} else {
			slog.Warn("fetch pr metadata for slack failed", "err", err, "pr", in.PRNumber)
		}
	}
	if prTitle == "" {
		prTitle = in.PRTitle
	}

	// Notifications run last in the pipeline so upstream failures are
	// captured. Same pre-approval suppression as the planning event above.
	if notifyActive && !suppressChannels {
		if err := NotifyPREvent(ctx, channels, notify.EventPlan, PRNotifyInput{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunURL: in.CIRunURL,
			PRTitle: prTitle, PRAuthor: prAuthor, Stacks: summaries,
		}); err != nil {
			slog.Warn("notify plan-ready failed", "err", err, "pr", in.PRNumber)
		}
	}

	return &PreviewOutput{
		Stacks:      summaries,
		CommentBody: body,
		RunID:       runID,
		DurationSec: dur,
	}, nil
}

func runPreviewOne(ctx context.Context, in PreviewInput, otelProvider *reeveotel.Provider, s discovery.Stack, runID string, stateEnv map[string]string) summary.StackSummary {
	redactor := BuildRedactor(in.Shared)

	authEnv, authCleanup, authErr := ResolveAuthEnv(ctx, in.AuthConfig, in.AuthRegistry, s.Ref(), auth.ModePreview,
		LocalAuth{Enabled: in.Local, Providers: in.LocalAuthProviders})
	if authErr != nil {
		return summary.StackSummary{
			Project: s.Project, Stack: s.Name, Env: s.Env,
			Status: summary.StatusError, Error: redactor.Redact(authErr.Error()),
		}
	}
	defer authCleanup()
	authEnv = mergeEnv(stateEnv, authEnv)
	// Register every credential literal with the redactor - if any leaks
	// into stdout, it gets scrubbed.
	for _, v := range authEnv {
		redactor.AddSecret(v)
	}

	// Plan locking: ask the engine to save the plan it is about to compute
	// so apply can execute this exact change set instead of re-planning
	// against a world that may have moved. Best-effort by design - if the
	// temp file cannot be created the preview still runs, unlocked.
	savePlanPath := ""
	if in.Blob != nil && PlanLockingEnabled(in.Config) && EngineSupportsSavedPlans(in.Engine) {
		if f, terr := os.CreateTemp("", "reeve-save-plan-*"); terr == nil {
			savePlanPath = f.Name()
			_ = f.Close()
			defer os.Remove(savePlanPath)
		} else {
			slog.Warn("plan locking: temp file unavailable; preview will not save a plan", "stack", s.Ref(), "err", terr)
		}
	}

	stackCtx, endStack := otelProvider.StartStackSpan(ctx, s.Project, s.Name, s.Env, "preview")
	stackStart := time.Now()
	res, err := in.Engine.Preview(stackCtx, s, iac.PreviewOpts{
		Cwd:          absJoin(in.RepoRoot, s.Path),
		Env:          authEnv,
		TimeoutSec:   PreviewTimeoutSec(in.Config),
		SavePlanPath: savePlanPath,
		Refresh:      in.Refresh,
	})
	ss := summary.StackSummary{
		Project: s.Project,
		Stack:   s.Name,
		Env:     s.Env,
	}
	defer func() {
		outcome := "success"
		if ss.Status == summary.StatusError {
			outcome = "error"
		}
		endStack(outcome, time.Since(stackStart).Seconds())
	}()
	if err != nil {
		ss.Status = summary.StatusError
		ss.Error = redactor.Redact(err.Error())
		return ss
	}
	ss.Counts = res.Counts
	ss.PlanSummary = redactor.Redact(res.PlanSummary)
	ss.PlanDiff = redactor.Redact(res.PlanDiff)
	ss.FullPlan = redactor.Redact(res.FullPlan)
	otelProvider.RecordStackChanges(ctx, s.Project, s.Name, res.Counts.Add, res.Counts.Change, res.Counts.Delete, res.Counts.Replace)
	if res.Error != "" {
		ss.Status = summary.StatusError
		ss.Error = redactor.Redact(res.Error)
		return ss
	}
	// Persist the saved plan next to the run manifest. A failure here is
	// logged, not fatal: the preview itself is valid and apply falls back to
	// re-planning, which is exactly the pre-plan-locking behavior.
	if res.PlanPath != "" {
		key, perr := PutPlanArtifact(ctx, in.Blob, in.PRNumber, runID, s.Ref(), res.PlanPath)
		if perr != nil {
			slog.Warn("plan locking: storing the saved plan failed; apply will re-plan for this stack",
				"stack", s.Ref(), "err", perr)
		} else {
			ss.PlanKey = key
			slog.Debug("plan locking: saved plan stored", "stack", s.Ref(), "key", key)
		}
	}
	if ss.Counts.Total() == 0 {
		ss.Status = summary.StatusNoOp
	} else {
		ss.Status = summary.StatusPlanned
	}
	return ss
}

// planSucceeded returns true if every stack planned without error.
func planSucceeded(ss []summary.StackSummary) bool {
	if len(ss) == 0 {
		return false
	}
	for _, s := range ss {
		if s.Status == summary.StatusError {
			return false
		}
	}
	return true
}

func declarationsFromConfig(e *schemas.Engine) ([]discovery.Declaration, discovery.Filter) {
	if e == nil {
		return nil, discovery.Filter{}
	}
	decls := make([]discovery.Declaration, 0, len(e.Engine.Stacks))
	for _, s := range e.Engine.Stacks {
		d := discovery.Declaration{
			Project: s.Project,
			Path:    s.Path,
			Pattern: s.Pattern,
			Stacks:  s.Stacks,
		}
		decls = append(decls, d)
	}
	var filter discovery.Filter
	for _, ex := range e.Engine.Filters.Exclude {
		if ex.Stack != "" {
			filter.StackPatterns = append(filter.StackPatterns, ex.Stack)
		}
		if ex.Pattern != "" {
			filter.PathPatterns = append(filter.PathPatterns, ex.Pattern)
		}
	}
	return decls, filter
}

func changeMappingFromConfig(e *schemas.Engine) discovery.ChangeMapping {
	if e == nil {
		return discovery.ChangeMapping{}
	}
	cm := discovery.ChangeMapping{
		IgnoreChanges: e.Engine.ChangeMapping.IgnoreChanges,
		Scope:         e.Engine.ChangeMapping.Scope,
	}
	if len(e.Engine.ChangeMapping.ExtraTriggers) > 0 {
		cm.ExtraTriggers = map[string][]string{}
		for _, t := range e.Engine.ChangeMapping.ExtraTriggers {
			cm.ExtraTriggers[t.Project] = append(cm.ExtraTriggers[t.Project], t.Paths...)
		}
	}
	return cm
}

// manifest is the JSON we write to Blob for each run. Consumed by
// Phase 2 apply (freshness check, saved plan lookup).
type manifest struct {
	RunID     string                 `json:"run_id"`
	PR        int                    `json:"pr"`
	CommitSHA string                 `json:"commit_sha"`
	Op        string                 `json:"op"`
	CreatedAt string                 `json:"created_at"`
	Stacks    []summary.StackSummary `json:"stacks"`
}

func writeManifest(ctx context.Context, store blob.Store, pr int, runID string, stacks []summary.StackSummary, sha string) error {
	if store == nil {
		return nil
	}
	m := manifest{
		RunID:     runID,
		PR:        pr,
		CommitSHA: sha,
		Op:        "preview",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Stacks:    stacks,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("runs/pr-%d/%s/manifest.json", pr, runID)
	if pr == 0 {
		key = fmt.Sprintf("runs/local/%s/manifest.json", runID)
	}
	_, err = store.Put(ctx, key, strings.NewReader(string(data)))
	return err
}

func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	if s == "" {
		return "unknown"
	}
	return s
}

func absJoin(root, rel string) string {
	if rel == "" || rel == "." {
		return root
	}
	return root + "/" + rel
}
