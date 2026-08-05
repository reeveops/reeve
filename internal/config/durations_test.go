package config

import (
	"strings"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// validBase returns a minimal Config that passes Validate, for per-field
// mutation.
func validBase() *Config {
	return &Config{
		Shared: &schemas.Shared{
			Bucket: schemas.BucketConfig{Type: "filesystem", Name: "./.state"},
		},
		Engines: []*schemas.Engine{
			{Engine: schemas.EngineBody{Type: "pulumi"}},
		},
	}
}

func TestValidateDurationsRejectsEachMalformedField(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantSub []string // substrings the error must contain (file + field)
	}{
		{
			name:    "locking.ttl",
			mutate:  func(c *Config) { c.Shared.Locking.TTL = "15minutes" },
			wantSub: []string{"shared.yaml", "locking.ttl", "15minutes"},
		},
		{
			name:    "locking.reaper_interval",
			mutate:  func(c *Config) { c.Shared.Locking.ReaperInterval = "1hr" },
			wantSub: []string{"shared.yaml", "locking.reaper_interval", "1hr"},
		},
		{
			name:    "retention.max_age",
			mutate:  func(c *Config) { c.Shared.Retention.MaxAge = "1month" },
			wantSub: []string{"shared.yaml", "retention.max_age", "1month"},
		},
		{
			name:    "preconditions.preview_freshness",
			mutate:  func(c *Config) { c.Shared.Preconditions.PreviewFreshness = "2 hours" },
			wantSub: []string{"shared.yaml", "preconditions.preview_freshness"},
		},
		{
			name:    "approvals.default.freshness",
			mutate:  func(c *Config) { c.Shared.Approvals.Default.Freshness = "1day" },
			wantSub: []string{"shared.yaml", "approvals.default.freshness", "1day"},
		},
		{
			name: "approvals.stacks freshness",
			mutate: func(c *Config) {
				c.Shared.Approvals.Stacks = map[string]schemas.ApprovalRuleYAML{
					"prod/*": {Freshness: "24hours"},
				}
			},
			wantSub: []string{"shared.yaml", "approvals.stacks[prod/*].freshness", "24hours"},
		},
		{
			name: "freeze window duration",
			mutate: func(c *Config) {
				c.Shared.FreezeWindows = []schemas.FreezeWindowYAML{
					{Name: "weekend", Cron: "0 18 * * FRI", Duration: "2d"},
				}
			},
			wantSub: []string{"shared.yaml", "freeze_windows[weekend].duration", "2d"},
		},
		{
			name:    "engine preview_timeout",
			mutate:  func(c *Config) { c.Engines[0].Engine.Execution.PreviewTimeout = "10min" },
			wantSub: []string{"engine.type=pulumi", "engine.execution.preview_timeout", "10min"},
		},
		{
			name:    "engine apply_timeout",
			mutate:  func(c *Config) { c.Engines[0].Engine.Execution.ApplyTimeout = "half an hour" },
			wantSub: []string{"engine.type=pulumi", "engine.execution.apply_timeout"},
		},
		{
			name: "drift freshness.window",
			mutate: func(c *Config) {
				c.Drift = &schemas.Drift{Freshness: schemas.DriftFreshness{Window: "4hours"}}
			},
			wantSub: []string{"drift.yaml", "freshness.window", "4hours"},
		},
		{
			name: "drift timeout_per_stack malformed",
			mutate: func(c *Config) {
				c.Drift = &schemas.Drift{Behavior: schemas.DriftBehavior{TimeoutPerStack: "15minutes"}}
			},
			wantSub: []string{"drift.yaml", "behavior.timeout_per_stack"},
		},
		{
			name: "drift timeout_per_stack non-positive",
			mutate: func(c *Config) {
				c.Drift = &schemas.Drift{Behavior: schemas.DriftBehavior{TimeoutPerStack: "0s"}}
			},
			wantSub: []string{"drift.yaml", "behavior.timeout_per_stack", "must be positive"},
		},
		{
			name: "drift renotify_after negative",
			mutate: func(c *Config) {
				c.Drift = &schemas.Drift{Behavior: schemas.DriftBehavior{RenotifyAfter: "-24h"}}
			},
			wantSub: []string{"drift.yaml", "behavior.renotify_after", "must be positive"},
		},
		{
			name:    "locking.ttl zero rejected",
			mutate:  func(c *Config) { c.Shared.Locking.TTL = "0s" },
			wantSub: []string{"shared.yaml", "locking.ttl", "must be positive"},
		},
		{
			name:    "locking.ttl negative rejected",
			mutate:  func(c *Config) { c.Shared.Locking.TTL = "-1h" },
			wantSub: []string{"shared.yaml", "locking.ttl", "must be positive"},
		},
		{
			name:    "apply_timeout zero rejected",
			mutate:  func(c *Config) { c.Engines[0].Engine.Execution.ApplyTimeout = "0" },
			wantSub: []string{"engine.execution.apply_timeout", "must be positive"},
		},
		{
			name: "drift baseline_max_age",
			mutate: func(c *Config) {
				c.Drift = &schemas.Drift{Behavior: schemas.DriftBehavior{
					StateBootstrap: schemas.StateBootstrap{BaselineMaxAge: "7x"},
				}}
			},
			wantSub: []string{"drift.yaml", "behavior.state_bootstrap.baseline_max_age", "7x"},
		},
		{
			name: "auth provider duration",
			mutate: func(c *Config) {
				c.Auth = &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
					"aws-prod": {Type: "aws_oidc", Duration: "30minutes"},
				}}
			},
			wantSub: []string{"auth.yaml", "providers[aws-prod].duration", "30minutes"},
		},
		{
			name: "auth provider ttl",
			mutate: func(c *Config) {
				c.Auth = &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
					"secrets": {Type: "aws_secrets_manager", TTL: "5 min"},
				}}
			},
			wantSub: []string{"auth.yaml", "providers[secrets].ttl"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBase()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("malformed duration must fail Validate")
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q must contain %q (name file+field+value)", err, sub)
				}
			}
		})
	}
}

func TestValidateDurationsAcceptsWellFormedFields(t *testing.T) {
	cfg := validBase()
	cfg.Shared.Locking.TTL = "4h"
	cfg.Shared.Retention.MaxAge = "720h"
	cfg.Shared.Preconditions.PreviewFreshness = "2h"
	cfg.Shared.Approvals.Default.Freshness = "24h"
	cfg.Shared.FreezeWindows = []schemas.FreezeWindowYAML{{Name: "weekend", Cron: "0 18 * * FRI", Duration: "65h"}}
	cfg.Engines[0].Engine.Execution.PreviewTimeout = "10m"
	cfg.Engines[0].Engine.Execution.ApplyTimeout = "30m"
	cfg.Drift = &schemas.Drift{
		Freshness: schemas.DriftFreshness{Window: "4h"},
		Behavior: schemas.DriftBehavior{
			// baseline_max_age is documented with day units ("7d"), so the
			// extended parser applies to it. timeout_per_stack and
			// renotify_after also accept extended units and must be positive.
			StateBootstrap:  schemas.StateBootstrap{BaselineMaxAge: "7d"},
			TimeoutPerStack: "1d",
			RenotifyAfter:   "24h",
		},
	}
	cfg.Auth = &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"aws-prod": {Type: "aws_oidc", Duration: "1h", TTL: "5m"},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("well-formed durations must pass Validate: %v", err)
	}
	// "0" disables retention and must stay valid.
	cfg.Shared.Retention.MaxAge = "0"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("retention.max_age=0 must stay valid: %v", err)
	}
}

// TestLoadedConfigRejectsMalformedTTL exercises the full Load+Validate path
// (what `reeve lint` and every command run): a typo'd lock TTL in
// shared.yaml is a validate error, not a silent 4h default.
func TestLoadedConfigRejectsMalformedTTL(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
locking:
  ttl: 15minutes
`,
		"pulumi.yaml": `version: 1
config_type: engine
engine:
  type: pulumi
  stacks:
    - project: api
      path: projects/api
      stacks: [prod]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "locking.ttl") {
		t.Fatalf("want locking.ttl validate error, got %v", err)
	}
}

func TestParseDurationExtended(t *testing.T) {
	cases := map[string]time.Duration{
		"48h":  48 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"1.5d": 36 * time.Hour,
	}
	for in, want := range cases {
		got, err := ParseDurationExtended(in)
		if err != nil || got != want {
			t.Fatalf("ParseDurationExtended(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"7x", "d", "-1d", "1 day"} {
		if _, err := ParseDurationExtended(bad); err == nil {
			t.Fatalf("ParseDurationExtended(%q) must fail", bad)
		}
	}
}
