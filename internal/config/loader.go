package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/notify"
)

// Config is the loaded, validated set of .reeve/*.yaml files.
type Config struct {
	Shared        *schemas.Shared
	Engines       []*schemas.Engine
	Auth          *schemas.Auth
	Notifications *schemas.Notifications
	Drift         *schemas.Drift
	Observability *schemas.Observability

	// ChannelSourceFiles lists the repo-relative config files that declare
	// notification channels (e.g. ".reeve/notifications.yaml",
	// ".reeve/drift.yaml"), derived from the file names actually loaded.
	// The preview path uses it to suppress pre-approval channel dispatch
	// when a PR modifies those files (PR-head config is untrusted before
	// approval).
	ChannelSourceFiles []string

	// ObservabilitySourceFiles is the same for the observability config
	// (OTEL exporter endpoint/headers): the preview path skips OTEL init
	// entirely for pre-approval runs when a PR modifies these files.
	ObservabilitySourceFiles []string

	// EnvExpandWarnings holds load-time warnings about "${env:...}"
	// references in non-designated fields (left literal). Surfaced by
	// `reeve lint` and logged at load.
	EnvExpandWarnings []string
}

// Load reads .reeve/ under root (or root itself if root points at a file),
// validates each file's header, and unmarshals strictly into the matching
// schema. Unknown keys are errors. Exactly one file per config_type;
// multiple engine files parse here (unique engine.type per file) but
// Validate rejects more than one until multi-engine routing exists.
func Load(root string) (*Config, error) {
	dir := filepath.Join(root, ".reeve")
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		files = append(files, filepath.Join(dir, n))
	}
	sort.Strings(files)

	cfg := &Config{}
	seenType := map[string]string{}
	seenEngine := map[string]string{}

	// Read every config through a root scoped to .reeve/. This directory is
	// checked out from the PR HEAD, and git can store symlinks: without the
	// root, a PR could commit `.reeve/shared.yaml -> ../../../etc/passwd`
	// and reeve would read whatever it pointed at (and can echo parse
	// failures back into the PR comment). os.Root refuses to traverse out.
	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer dirRoot.Close()

	for _, f := range files {
		// f stays absolute for error messages; only the read is scoped.
		data, err := dirRoot.ReadFile(filepath.Base(f))
		if err != nil {
			return nil, err
		}
		var hdr schemas.Header
		if err := strictDecode(data, &hdr); err != nil {
			// header pass is lenient on unknown keys (we re-decode strictly below)
			if err := nonStrictDecode(data, &hdr); err != nil {
				return nil, fmt.Errorf("%s: parse header: %w", f, err)
			}
		}
		if hdr.ConfigType == "" {
			return nil, fmt.Errorf("%s: missing config_type", f)
		}
		if max := maxSchemaVersion(hdr.ConfigType); hdr.Version < 1 || hdr.Version > max {
			if max == 1 {
				return nil, fmt.Errorf("%s: unsupported version %d (want 1)", f, hdr.Version)
			}
			return nil, fmt.Errorf("%s: unsupported version %d (want 1..%d)", f, hdr.Version, max)
		}

		switch hdr.ConfigType {
		case "shared":
			if prev, ok := seenType["shared"]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type shared (also in %s)", f, prev)
			}
			seenType["shared"] = f
			var s schemas.Shared
			if err := strictDecode(data, &s); err != nil {
				if hint := removedKeyHint(err); hint != "" {
					return nil, fmt.Errorf("%s: %s", f, hint)
				}
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			cfg.Shared = &s
		case "engine":
			var e schemas.Engine
			if err := strictDecode(data, &e); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			if e.Engine.Type == "" {
				return nil, fmt.Errorf("%s: engine.type is required", f)
			}
			if prev, ok := seenEngine[e.Engine.Type]; ok {
				return nil, fmt.Errorf("%s: duplicate engine.type %q (also in %s)", f, e.Engine.Type, prev)
			}
			seenEngine[e.Engine.Type] = f
			cfg.Engines = append(cfg.Engines, &e)
		case "auth":
			if prev, ok := seenType["auth"]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type auth (also in %s)", f, prev)
			}
			seenType["auth"] = f
			var a schemas.Auth
			if err := strictDecode(data, &a); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			cfg.Auth = &a
		case "notifications":
			if prev, ok := seenType["notifications"]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type notifications (also in %s)", f, prev)
			}
			seenType["notifications"] = f
			var n schemas.Notifications
			if err := strictDecode(data, &n); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			// The original single slack: block predates channels:. Alpha
			// rule: no silent compat - reject with the conversion pointer.
			if n.Slack != nil {
				return nil, fmt.Errorf("%s: the slack: block was replaced by channels: - run `reeve migrate-config` to convert the file (see docs/notifications.md)", f)
			}
			cfg.Notifications = &n
			cfg.ChannelSourceFiles = append(cfg.ChannelSourceFiles, repoRelConfigPath(f))
		case "drift":
			if prev, ok := seenType["drift"]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type drift (also in %s)", f, prev)
			}
			seenType["drift"] = f
			var d schemas.Drift
			if err := strictDecode(data, &d); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			// `sinks:` was the original spelling of `channels:`. Alpha rule:
			// no silent compat - reject with the conversion pointer.
			if len(d.DeprecatedSinks) > 0 {
				return nil, fmt.Errorf("%s: sinks: was renamed to channels: - run `reeve migrate-config` to convert the file, or rename the key", f)
			}
			cfg.Drift = &d
			cfg.ChannelSourceFiles = append(cfg.ChannelSourceFiles, repoRelConfigPath(f))
		case "observability":
			if prev, ok := seenType["observability"]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type observability (also in %s)", f, prev)
			}
			seenType["observability"] = f
			var o schemas.Observability
			if err := strictDecode(data, &o); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			cfg.Observability = &o
			cfg.ObservabilitySourceFiles = append(cfg.ObservabilitySourceFiles, repoRelConfigPath(f))
		case "user":
			// Scaffold: accept header but do nothing. Later phases land
			// concrete schemas.
			if prev, ok := seenType[hdr.ConfigType]; ok {
				return nil, fmt.Errorf("%s: duplicate config_type %s (also in %s)", f, hdr.ConfigType, prev)
			}
			seenType[hdr.ConfigType] = f
		default:
			return nil, fmt.Errorf("%s: unknown config_type %q", f, hdr.ConfigType)
		}
	}

	// Resolve ${env:NAME} references in the DESIGNATED credential-bearing
	// fields only (see env_expand.go); everywhere else the reference stays
	// literal and draws a warning, because config is loaded from the PR
	// HEAD and must not become an env-var oracle.
	cfg.EnvExpandWarnings = cfg.ExpandEnv()
	for _, w := range cfg.EnvExpandWarnings {
		slog.Warn("config: " + w)
	}

	if cfg.Shared != nil {
		slog.Info("config loaded", "dir", dir, "log_level", cfg.Shared.LogLevel, "log_format", cfg.Shared.LogFormat)
	}
	return cfg, nil
}

// maxSchemaVersion returns the highest supported schema version for a
// config_type. notifications is at 2 (the generic `channels:` list); the
// original version-1 `slack:` block is rejected at load with a pointer at
// `reeve migrate-config`.
func maxSchemaVersion(configType string) int {
	if configType == "notifications" {
		return 2
	}
	return 1
}

// Validate runs cross-file checks: exactly one engine config and a bucket.
func (c *Config) Validate() error {
	if len(c.Engines) == 0 {
		return errors.New("no engine config found (e.g. .reeve/pulumi.yaml)")
	}
	if len(c.Engines) > 1 {
		types := make([]string, 0, len(c.Engines))
		for _, e := range c.Engines {
			types = append(types, e.Engine.Type)
		}
		return fmt.Errorf("multiple engine configs found (%s); reeve currently supports one engine per repo",
			strings.Join(types, ", "))
	}
	if c.Shared == nil {
		return errors.New("no shared config found (.reeve/shared.yaml)")
	}
	if c.Shared.Bucket.Type == "" {
		return errors.New("shared.yaml: bucket.type is required")
	}
	// apply.trigger, when set, must be one of the two recognized modes. A
	// typo (e.g. "mrege") must not silently fall back to the comment
	// default and quietly disable the intended merge-mode auto-apply.
	if t := c.Shared.Apply.Trigger; t != "" && t != schemas.ApplyTriggerComment && t != schemas.ApplyTriggerMerge {
		return fmt.Errorf("shared.yaml: apply.trigger %q is invalid (want %q or %q)",
			t, schemas.ApplyTriggerComment, schemas.ApplyTriggerMerge)
	}
	// preview_freshness, when set, must parse. An unparseable value used to
	// leave the duration at zero, which DISABLES the gate - a typo silently
	// turning off a safety check is exactly the failure mode this rejects.
	if v := strings.TrimSpace(c.Shared.Preconditions.PreviewFreshness); v != "" && v != "0" {
		if _, err := time.ParseDuration(v); err != nil {
			return fmt.Errorf("shared.yaml: preconditions.preview_freshness %q is not a Go duration (e.g. 2h); use \"0\" to disable the gate deliberately", v)
		}
	}
	if err := c.validateChannels(); err != nil {
		return err
	}
	if err := c.validateDurations(); err != nil {
		return err
	}
	if err := c.validateApprovalSources(); err != nil {
		return err
	}
	return nil
}

// validateApprovalSources rejects unknown approvals.sources[].type values so a
// typo like `pr_commnet` fails loudly at load time instead of silently never
// gathering that source (a fail-open-looking surprise).
func (c *Config) validateApprovalSources() error {
	if c.Shared == nil {
		return nil
	}
	for _, s := range c.Shared.Approvals.Sources {
		switch s.Type {
		case approvals.SourcePRReview, approvals.SourcePRComment:
		case "":
			return fmt.Errorf("shared.yaml: approvals.sources: entry is missing required 'type'")
		default:
			return fmt.Errorf("shared.yaml: approvals.sources: unknown source type %q (valid: %s, %s)",
				s.Type, approvals.SourcePRReview, approvals.SourcePRComment)
		}
		// `enabled` is required on every listed source. Omitting it used to
		// read as false and silently disable the source (e.g. listing
		// pr_review to "keep it on" turned it off); reject it instead so the
		// intent is always explicit.
		if s.Enabled == nil {
			return fmt.Errorf("shared.yaml: approvals.sources: %q entry must set 'enabled: true' or 'enabled: false' explicitly", s.Type)
		}
	}
	return nil
}

// channelDefaultsSubscriptions reports whether a channel type has a non-empty
// default subscription when `on:` is omitted, and is therefore exempt from
// the empty-`on` warning: slack defaults to every lifecycle event at or
// after its trigger, and the timeline channels default to every PR-flow
// timeline event.
func channelDefaultsSubscriptions(typ string) bool {
	switch typ {
	case "slack", "timeline_slack", "timeline_github":
		return true
	}
	return false
}

// validateChannels checks every channel declaration (notifications.yaml `channels:`
// and drift.yaml `channels:`): `on:` entries must be known event names, and an
// empty subscription list draws a warning because the channel will never fire.
// (Types with default subscriptions - see channelDefaultsSubscriptions - are
// exempt.)
func (c *Config) validateChannels() error {
	// driftOnly restricts drift.yaml channels to drift events; PR-flow events
	// never fire in a drift run. notifications.yaml accepts the full set (it
	// may carry PR-flow channels and drift channels alike).
	check := func(file string, channels []schemas.ChannelYAML, driftOnly bool) error {
		for _, s := range channels {
			if s.Type == "" {
				return fmt.Errorf("%s: channel %q: type is required", file, s.EffectiveName())
			}
			for _, ev := range s.On {
				if driftOnly {
					if !schemas.IsValidDriftChannelEvent(ev) {
						return fmt.Errorf("%s: channel %q: unknown drift event %q in on: (valid: %s)",
							file, s.EffectiveName(), ev, strings.Join(schemas.ValidDriftChannelEvents, ", "))
					}
				} else if !schemas.IsValidChannelEvent(ev) {
					return fmt.Errorf("%s: channel %q: unknown event %q in on: (valid: %s)",
						file, s.EffectiveName(), ev, strings.Join(schemas.ValidChannelEvents, ", "))
				}
			}
			if len(s.On) == 0 && !channelDefaultsSubscriptions(s.Type) {
				slog.Warn("notification channel subscribes to no events and will never fire; set on:",
					"file", file, "channel", s.EffectiveName(), "type", s.Type)
			}
			if !notify.IsValidGroupingMode(s.Grouping) {
				return fmt.Errorf("%s: channel %q: unknown grouping %q (valid: %s)",
					file, s.EffectiveName(), s.Grouping, strings.Join(notify.ValidGroupingModes, ", "))
			}
		}
		return nil
	}
	if c.Notifications != nil {
		if err := check("notifications.yaml", c.Notifications.Channels, false); err != nil {
			return err
		}
	}
	if c.Drift != nil {
		if err := check("drift.yaml", c.Drift.Channels, true); err != nil {
			return err
		}
	}
	return nil
}

// LogSettings returns the configured log level and format, safely handling a
// missing shared config (nil Shared). Commands call this before Validate to
// initialise logging without risking a nil-pointer panic when .reeve/ lacks
// shared.yaml.
func (c *Config) LogSettings() (level, format string) {
	if c == nil || c.Shared == nil {
		return "", ""
	}
	return c.Shared.LogLevel, c.Shared.LogFormat
}

// removedKeyHint turns the raw yaml.v3 "field X not found in type Y" error for
// a deliberately-removed config key into an actionable message, or "" when the
// error is something else. Other retired keys (slack:, sinks:) get their own
// pointers at their decode site; this covers keys the strict decoder rejects
// generically with an unhelpful Go type name.
func removedKeyHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "field command not found in type schemas.ApplyConfig") {
		return "apply.command was removed: apply now runs the engine's native apply, so there is no custom command. Delete the key. To control how apply is triggered use apply.trigger (comment|merge). See docs/configuration.md"
	}
	return ""
}

func strictDecode(data []byte, out any) error {
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	// A second YAML document ("---") was silently ignored before, so any keys
	// in it never took effect - a real footgun for a supposedly strict loader.
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("multiple YAML documents in one file are not supported (found a second document)")
	}
	return nil
}

// repoRelConfigPath maps an absolute loaded-config path to the
// repo-relative form used in PR changed-file lists (".reeve/<name>").
func repoRelConfigPath(f string) string {
	return ".reeve/" + filepath.Base(f)
}

func nonStrictDecode(data []byte, out any) error {
	dec := yaml.NewDecoder(bytesReader(data))
	return dec.Decode(out)
}
