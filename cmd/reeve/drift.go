package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/auth"
	authfac "github.com/FynxLabs/reeve/internal/auth/factory"
	"github.com/FynxLabs/reeve/internal/blob/factory"
	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/drift"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/run"
	gh "github.com/FynxLabs/reeve/internal/vcs/github"
)

func newDriftCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "drift", Short: "Drift detection commands"}

	runSub := &cobra.Command{Use: "run", Short: "Execute a drift check", RunE: driftRun}
	runSub.Flags().String("pattern", "", "Restrict to stacks matching this pattern")
	runSub.Flags().String("schedule", "", "Run a named schedule from drift.yaml")
	runSub.Flags().Bool("if-stale", false, "Skip stacks checked within freshness window")
	runSub.Flags().String("root", "", "Repo root (default: cwd)")

	bootstrapSub := &cobra.Command{Use: "bootstrap", Short: "Record current stack state as the drift baseline (no events)", RunE: driftBootstrap}
	bootstrapSub.Flags().String("pattern", "", "Restrict to stacks matching this pattern")
	bootstrapSub.Flags().String("schedule", "", "Run a named schedule from drift.yaml")
	bootstrapSub.Flags().String("root", "", "Repo root (default: cwd)")

	statusSub := &cobra.Command{Use: "status", Short: "Read last drift run results", RunE: driftStatus}
	statusSub.Flags().String("stack", "", "Limit to a single project/stack")

	reportSub := &cobra.Command{Use: "report", Short: "Render drift report to stdout", RunE: driftReport}
	reportSub.Flags().String("format", "markdown", "Output format (markdown|json)")

	suppress := &cobra.Command{Use: "suppress", Short: "Manage time-bounded suppressions"}
	addSub := &cobra.Command{Use: "add <project/stack>", Args: cobra.ExactArgs(1), Short: "Create a suppression", RunE: driftSuppressAdd}
	addSub.Flags().String("until", "24h", "Suppression duration (e.g. 24h, 7d)")
	addSub.Flags().String("reason", "", "Why suppressed (for audit)")
	suppress.AddCommand(
		addSub,
		&cobra.Command{Use: "list", Short: "List active suppressions", RunE: driftSuppressList},
		&cobra.Command{Use: "clear <project/stack>", Args: cobra.ExactArgs(1), Short: "Clear a suppression", RunE: driftSuppressClear},
	)

	cmd.AddCommand(runSub, bootstrapSub, statusSub, reportSub, suppress)
	return cmd
}

func loadDriftCtx(cmd *cobra.Command) (context.Context, *config.Config, string, error) {
	root := flagStringOrDefault(cmd, "root", "")
	if root == "" {
		root, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(root)
	root = abs
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, "", err
	}
	applyLogConfig(cfg.LogSettings())
	if err := cfg.Validate(); err != nil {
		return nil, nil, "", err
	}
	// Cancelled by SIGINT/SIGTERM via the root signal context (see main.go).
	return cmd.Context(), cfg, root, nil
}

func driftRun(cmd *cobra.Command, _ []string) error { return runDrift(cmd, false) }

// driftBootstrap records the current state of every stack as the drift
// baseline without emitting any events. Required to unblock
// state_bootstrap.mode=require_manual (whose first real run refuses until a
// baseline exists), and useful to silently seed state in any mode.
func driftBootstrap(cmd *cobra.Command, _ []string) error { return runDrift(cmd, true) }

func runDrift(cmd *cobra.Command, bootstrap bool) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	engineCfg := cfg.Engines[0]
	engine, err := iac.New(engineCfg.Engine)
	if err != nil {
		return err
	}

	// Build the auth resolver on top of the auth registry.
	authReg, err := authfac.Build(ctx, cfg.Auth)
	if err != nil {
		return err
	}
	resolver := func(ctx context.Context, ref string) (map[string]string, error) {
		// Drift currently doesn't expose a per-call cleanup hook; once the
		// drift runner gains stack-scoped lifecycles we should plumb the
		// cleanup func through. For now an unrun cleanup leaks the GCP WIF
		// temp file until the process exits, which is bounded by the run's
		// duration on GitHub Actions.
		env, _, err := run.ResolveAuthEnv(ctx, cfg.Auth, authReg, ref, auth.ModeDrift)
		return env, err
	}

	decls := make([]discovery.Declaration, 0, len(engineCfg.Engine.Stacks))
	for _, s := range engineCfg.Engine.Stacks {
		decls = append(decls, discovery.Declaration{
			Project: s.Project, Path: s.Path, Pattern: s.Pattern, Stacks: s.Stacks,
		})
	}
	var filter discovery.Filter
	for _, ex := range engineCfg.Engine.Filters.Exclude {
		if ex.Stack != "" {
			filter.StackPatterns = append(filter.StackPatterns, ex.Stack)
		}
		if ex.Pattern != "" {
			filter.PathPatterns = append(filter.PathPatterns, ex.Pattern)
		}
	}

	include, exclude, err := buildScope(cfg, cmd)
	if err != nil {
		return err
	}

	otelProvider, _ := run.BuildOTEL(ctx, cfg.Observability)
	defer func() {
		if otelProvider != nil {
			_ = otelProvider.Shutdown(ctx)
		}
	}()

	// Optional PR-overlap support (drift reports link back to open PRs).
	// A client construction failure disables the feature EXPLICITLY - a
	// silent skip would read as "no overlapping PRs".
	var overlap drift.PROverlapFinder
	repoFullForOverlap := os.Getenv("GITHUB_REPOSITORY")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && repoFullForOverlap != "" {
		if parts := strings.SplitN(repoFullForOverlap, "/", 2); len(parts) == 2 {
			client, err := gh.New(ctx, tok, parts[0], parts[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: PR-overlap scan disabled (github client: %v)\n", err)
			} else {
				overlap = drift.NewGitHubPROverlap(client)
			}
		}
	}

	opts := drift.Options{
		Engine:           engine,
		RepoRoot:         root,
		Decls:            decls,
		Filter:           filter,
		IncludePatterns:  include,
		ExcludePatterns:  exclude,
		AuthResolver:     resolver,
		StateStore:       &drift.StateStore{Blob: store},
		SuppressionStore: &drift.SuppressionStore{Blob: store},
		Redactor:         run.BuildRedactor(cfg.Shared),
		OTEL:             otelProvider,
		PROverlap:        overlap,
	}
	if cfg.Drift != nil {
		opts.RefreshFirst = cfg.Drift.Behavior.RefreshBeforeCheck
		opts.Parallel = cfg.Drift.Behavior.MaxParallelStacks
		opts.RetryOnTransientError = cfg.Drift.Behavior.RetryOnTransientError
		opts.RetryBackoff = 2 * time.Second // base; grows exponentially, ctx-aware
		opts.Classification = buildClassification(cfg.Drift.Classification)
		opts.PermanentSuppressions = buildPermanentSuppressions(cfg.Drift.PermanentSuppressions)
		if cfg.Drift.Behavior.StateBootstrap.Mode != "" {
			opts.BootstrapMode = cfg.Drift.Behavior.StateBootstrap.Mode
		}
		if ra := cfg.Drift.Behavior.RenotifyAfter; ra != "" {
			// Validated at config load (validateDurations, extended units).
			if d, err := config.ParseDurationExtended(ra); err == nil {
				opts.RenotifyAfter = d
			}
		}
		if to := cfg.Drift.Behavior.TimeoutPerStack; to != "" {
			// Validated at config load (validateDurations, extended units,
			// must be positive); parse the same way here.
			d, perr := config.ParseDurationExtended(to)
			if perr != nil {
				return fmt.Errorf("drift.yaml: behavior.timeout_per_stack %q: %w", to, perr)
			}
			opts.PerStackTimeout = d
		}
		if w := cfg.Drift.Freshness.Window; w != "" && (cfg.Drift.Freshness.Enabled || flagBool(cmd, "if-stale")) {
			if d, err := time.ParseDuration(w); err == nil {
				opts.FreshnessWindow = d
			}
		}
	}

	if bootstrap {
		// Force baseline recording: bypass the require_manual refusal, accept
		// current state (including pre-existing drift) silently, and always
		// check (ignore freshness) so every stack gets a baseline.
		opts.BootstrapMode = "baseline"
		opts.FreshnessWindow = 0
	}

	out, err := drift.Run(ctx, opts)
	if err != nil {
		return err
	}

	report := drift.ReportMarkdown(out)
	if err := drift.WriteArtifacts(ctx, store, out, report); err != nil {
		return err
	}

	// $GITHUB_STEP_SUMMARY: always write when set.
	// 0644 is correct here - the file is rendered in the public Actions UI
	// and the runner writes it as the workflow user; tighter perms break
	// the runner's own reader.
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		// Two rules are suppressed on the line below, space-separated in one
		// directive: gosec honors a single rule list per node, so a second
		// annotation on the same statement would silently replace the first.
		// G306 is the deliberate 0644 explained above; G703 is the path,
		// which is $GITHUB_STEP_SUMMARY as provided by the Actions runner.
		_ = os.WriteFile(p, []byte(report), 0o644) // #nosec G306 G703 -- runner-provided path; 0644 required by the Actions UI reader
	}

	// Bootstrap is silent by design: state is recorded, no channels fire.
	if bootstrap {
		fmt.Fprintf(cmd.OutOrStdout(), "baseline recorded for %d stack(s); drift runs will now compare against it\n", len(out.Items))
		fmt.Fprintln(cmd.OutOrStdout(), report)
		return nil
	}

	// Dispatch to configured channels via the shared notify framework:
	// drift.yaml channels plus any notifications.yaml channels subscribed to
	// drift events.
	repoFull := os.Getenv("GITHUB_REPOSITORY")
	var issues notify.IssueClient
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		if parts := strings.SplitN(repoFull, "/", 2); len(parts) == 2 {
			client, err := gh.New(ctx, tok, parts[0], parts[1])
			if err != nil {
				// Without a client the github_issue channel is skipped at
				// build time; say so instead of silently dropping alerts.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: github_issue channel disabled (github client: %v)\n", err)
			} else {
				issues = client
			}
		}
	}
	var channelCfgs []schemas.ChannelYAML
	if cfg.Drift != nil {
		channelCfgs = append(channelCfgs, cfg.Drift.Channels...)
	}
	if cfg.Notifications != nil {
		channelCfgs = append(channelCfgs, cfg.Notifications.Channels...)
	}
	channels, serr := notify.Build(ctx, channelCfgs, notify.Deps{
		Blob:       store,
		Issues:     issues,
		Emitters:   run.BuildAnnotationEmitters(cfg.Observability),
		SlackToken: os.Getenv("SLACK_BOT_TOKEN"),
		RepoFull:   repoFull,
	})
	if serr != nil {
		return serr
	}
	if len(channels) > 0 {
		// Durable dispatch: payloads persist as undelivered markers before
		// delivery and clear only on success, so a crash or channel outage
		// after the baseline advanced still redelivers on the next run
		// (at-least-once; see internal/drift/pending.go).
		pending := &drift.PendingStore{Blob: store}
		leftover, perrs := pending.List(ctx)
		for _, e := range perrs {
			fmt.Fprintf(cmd.ErrOrStderr(), "pending-event error: %v\n", e)
		}
		payloads := drift.MergePending(leftover, drift.NotifyPayloads(out))
		errs := drift.DispatchDurable(ctx, channels, payloads, pending)
		for _, e := range errs {
			fmt.Fprintf(cmd.ErrOrStderr(), "channel error: %v\n", e)
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), report)
	return driftExitError(cfg, out)
}

// driftExitError maps drift.yaml behavior.exit_on onto the process exit
// code: when a configured condition occurred this run, a non-nil error is
// returned so `reeve drift run` exits nonzero and CI can gate on it. All
// conditions default to off (exit 0), matching the previous behavior.
func driftExitError(cfg *config.Config, out *drift.RunOutput) error {
	if cfg.Drift == nil {
		return nil
	}
	eo := cfg.Drift.Behavior.ExitOn
	countEvent := func(want drift.Event) int {
		n := 0
		for _, ev := range out.Events {
			if ev == want {
				n++
			}
		}
		return n
	}
	var reasons []string
	if eo.DriftDetected {
		if n := countEvent(drift.EventDriftDetected); n > 0 {
			reasons = append(reasons, fmt.Sprintf("new drift on %d stack(s) (exit_on.drift_detected)", n))
		}
	}
	if eo.DriftOngoing {
		if n := countEvent(drift.EventDriftOngoing); n > 0 {
			reasons = append(reasons, fmt.Sprintf("ongoing drift on %d stack(s) (exit_on.drift_ongoing)", n))
		}
	}
	if eo.RunError {
		if n := countEvent(drift.EventCheckFailed); n > 0 {
			reasons = append(reasons, fmt.Sprintf("%d check(s) failed (exit_on.run_error)", n))
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("drift run exit condition met: %s", strings.Join(reasons, "; "))
}

// buildClassification maps the drift.yaml classification block onto the
// runner's filter. treat_as_drift.orphaned_state / missing_state default to
// true (a resource that has gone missing, or exists untracked, is drift);
// only an explicit `false` opts that category out.
func buildClassification(c schemas.DriftClassification) *drift.Classification {
	out := &drift.Classification{
		IgnoreResources:      c.IgnoreResources,
		TreatOrphanedAsDrift: c.TreatAsDrift.OrphanedState == nil || *c.TreatAsDrift.OrphanedState,
		TreatMissingAsDrift:  c.TreatAsDrift.MissingState == nil || *c.TreatAsDrift.MissingState,
	}
	for _, ip := range c.IgnoreProperties {
		out.IgnoreProperties = append(out.IgnoreProperties, drift.IgnoreProperty{
			ResourceType: ip.ResourceType,
			Properties:   ip.Properties,
		})
	}
	return out
}

// buildPermanentSuppressions maps drift.yaml permanent_suppressions onto the
// runner. `until` is an optional RFC3339 expiry; omitted means permanent. An
// unparseable `until` is treated as permanent (logged), never as "no
// suppression", so a typo can't silently un-suppress accepted drift.
func buildPermanentSuppressions(sups []schemas.SuppressionYAML) []drift.PermanentSuppression {
	out := make([]drift.PermanentSuppression, 0, len(sups))
	for _, s := range sups {
		if s.Stack == "" {
			continue
		}
		ps := drift.PermanentSuppression{Stack: s.Stack, Reason: s.Reason}
		if s.Until != "" {
			if t, err := time.Parse(time.RFC3339, s.Until); err == nil {
				ps.Until = t
			} else {
				slog.Warn("drift: permanent suppression has an unparseable until (want RFC3339 date); treating as permanent",
					"stack", s.Stack, "until", s.Until)
			}
		}
		out = append(out, ps)
	}
	return out
}

func buildScope(cfg *config.Config, cmd *cobra.Command) (include, exclude []string, err error) {
	if cfg.Drift != nil {
		include = append(include, cfg.Drift.Scope.IncludePatterns...)
		exclude = append(exclude, cfg.Drift.Scope.ExcludePatterns...)
	}
	if sched := flagStringOrDefault(cmd, "schedule", ""); sched != "" {
		// An unknown schedule name must not silently fall back to the
		// global scope - a typo would run drift against every stack.
		var s schemas.Schedule
		ok := false
		if cfg.Drift != nil {
			s, ok = cfg.Drift.Schedules[sched]
		}
		if !ok {
			names := make([]string, 0)
			if cfg.Drift != nil {
				for n := range cfg.Drift.Schedules {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			if len(names) == 0 {
				return nil, nil, fmt.Errorf("unknown schedule %q: no schedules configured in drift.yaml", sched)
			}
			return nil, nil, fmt.Errorf("unknown schedule %q: configured schedules are %s", sched, strings.Join(names, ", "))
		}
		include = append([]string{}, s.Patterns...)
		exclude = append([]string{}, s.ExcludePatterns...)
	}
	if pat := flagStringOrDefault(cmd, "pattern", ""); pat != "" {
		include = []string{pat}
	}
	return include, exclude, nil
}

func driftStatus(cmd *cobra.Command, _ []string) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	ss := &drift.StateStore{Blob: store}
	keys, err := store.List(ctx, "drift/state")
	if err != nil {
		return err
	}
	want := flagStringOrDefault(cmd, "stack", "")
	w := cmd.OutOrStdout()
	for _, k := range keys {
		trimmed := strings.TrimPrefix(k, "drift/state/")
		trimmed = strings.TrimSuffix(trimmed, ".json")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) != 2 {
			continue
		}
		if want != "" && trimmed != want {
			continue
		}
		st, err := ss.Load(ctx, parts[0], parts[1])
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "%s/%s\tlast=%s\tat=%s\tongoing_since=%s\n",
			st.Project, st.Stack, st.LastOutcome,
			st.LastCheckedAt.Format(time.RFC3339),
			st.OngoingSince.Format(time.RFC3339))
	}
	return nil
}

func driftReport(cmd *cobra.Command, _ []string) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	body, err := drift.StoredReport(ctx, store, flagStringOrDefault(cmd, "format", "markdown"))
	if err != nil {
		if errors.Is(err, drift.ErrNoRuns) {
			fmt.Fprintln(cmd.OutOrStdout(), "no drift runs found")
			return nil
		}
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(body, "\n"))
	return nil
}

func driftSuppressAdd(cmd *cobra.Command, args []string) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	parts := splitRef(args[0])
	if parts == nil {
		return fmt.Errorf("expected project/stack, got %q", args[0])
	}
	dur, err := parseDurationExtended(flagStringOrDefault(cmd, "until", "24h"))
	if err != nil {
		return err
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	ss := &drift.SuppressionStore{Blob: store}
	return ss.Set(ctx, drift.Suppression{
		Project: parts[0], Stack: parts[1],
		Until:  time.Now().Add(dur),
		Reason: flagStringOrDefault(cmd, "reason", ""),
	})
}

func driftSuppressList(cmd *cobra.Command, _ []string) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	ss := &drift.SuppressionStore{Blob: store}
	active, err := ss.List(ctx, time.Now())
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	for _, s := range active {
		fmt.Fprintf(w, "%s/%s\tuntil=%s\treason=%s\n", s.Project, s.Stack, s.Until.Format(time.RFC3339), s.Reason)
	}
	return nil
}

func driftSuppressClear(cmd *cobra.Command, args []string) error {
	ctx, cfg, root, err := loadDriftCtx(cmd)
	if err != nil {
		return err
	}
	parts := splitRef(args[0])
	if parts == nil {
		return fmt.Errorf("expected project/stack, got %q", args[0])
	}
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}
	ss := &drift.SuppressionStore{Blob: store}
	return ss.Clear(ctx, parts[0], parts[1])
}
