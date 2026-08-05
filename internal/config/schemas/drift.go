package schemas

// Drift is .reeve/drift.yaml.
type Drift struct {
	Header                `yaml:",inline"`
	Scope                 DriftScope          `yaml:"scope"`
	Behavior              DriftBehavior       `yaml:"behavior"`
	Classification        DriftClassification `yaml:"classification"`
	Freshness             DriftFreshness      `yaml:"freshness"`
	Schedules             map[string]Schedule `yaml:"schedules"`
	PermanentSuppressions []SuppressionYAML   `yaml:"permanent_suppressions"`
	Channels              []ChannelYAML       `yaml:"channels"`
	// DeprecatedSinks is the original spelling of Channels. drift.yaml's
	// `sinks:` key shipped in v0.2.0, so the loader keeps accepting it as a
	// deprecated alias: it is mapped onto Channels at load time (with a
	// deprecation warning), and setting both keys is an error.
	// `reeve migrate-config` rewrites `sinks:` to `channels:`.
	DeprecatedSinks []ChannelYAML `yaml:"sinks,omitempty"`
}

type DriftScope struct {
	IncludePatterns []string `yaml:"include_patterns"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

type DriftBehavior struct {
	RefreshBeforeCheck    bool   `yaml:"refresh_before_check"`
	MaxParallelStacks     int    `yaml:"max_parallel_stacks"`
	TimeoutPerStack       string `yaml:"timeout_per_stack"`
	RetryOnTransientError int    `yaml:"retry_on_transient_error"`
	// RenotifyAfter enables flap damping for drift notifications: after a
	// drift alert goes out, further alerts for the stack stay silent until
	// the drift resolves or this window elapses (then a re-alert fires).
	// Accepts extended durations ("24h", "3d", "1w"). Empty = no damping:
	// every new detection notifies (the pre-existing behavior).
	RenotifyAfter  string         `yaml:"renotify_after"`
	ExitOn         DriftExitOn    `yaml:"exit_on"`
	StateBootstrap StateBootstrap `yaml:"state_bootstrap"`
}

type DriftExitOn struct {
	DriftDetected bool `yaml:"drift_detected"`
	DriftOngoing  bool `yaml:"drift_ongoing"`
	RunError      bool `yaml:"run_error"`
}

type StateBootstrap struct {
	Mode           string `yaml:"mode"` // baseline | alert_all | require_manual
	BaselineMaxAge string `yaml:"baseline_max_age"`
}

type DriftClassification struct {
	IgnoreProperties []IgnoreProp `yaml:"ignore_properties"`
	IgnoreResources  []string     `yaml:"ignore_resources"`
	TreatAsDrift     TreatAsDrift `yaml:"treat_as_drift"`
}

type IgnoreProp struct {
	ResourceType string   `yaml:"resource_type"`
	Properties   []string `yaml:"properties"`
}

// TreatAsDrift decides whether orphaned/missing resources count as drift.
// Both fields default to true when unset (the documented default: a resource
// that has gone missing, or exists untracked, is drift). Pointers let an
// omitted key stay at that default while an explicit `false` opts out.
type TreatAsDrift struct {
	OrphanedState *bool `yaml:"orphaned_state"`
	MissingState  *bool `yaml:"missing_state"`
}

type DriftFreshness struct {
	Enabled         bool   `yaml:"enabled"`
	Window          string `yaml:"window"`
	RespectFailures bool   `yaml:"respect_failures"`
}

type Schedule struct {
	Patterns        []string `yaml:"patterns"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

type SuppressionYAML struct {
	Stack  string `yaml:"stack"`
	Until  string `yaml:"until"`
	Reason string `yaml:"reason"`
}

// DriftPayload tunes the webhook channel's payload shape.
type DriftPayload struct {
	Format      string            `yaml:"format,omitempty"`
	DedupeKey   string            `yaml:"dedupe_key,omitempty"`
	SeverityMap map[string]string `yaml:"severity_map,omitempty"`
}
