package config

import (
	"strings"
	"testing"
)

const sharedYAMLMin = `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
`

const engineYAMLMin = `version: 1
config_type: engine
engine:
  type: pulumi
  stacks:
    - project: api
      path: projects/api
      stacks: [dev]
`

const legacyNotifications = `version: 1
config_type: notifications
slack:
  enabled: true
  channel: "#infra-deploys"
  auth_token: xoxb-test
  trigger: plan
  events: [plan, applied, failed]
  icons:
    engine: ":pulumi:"
  rules:
    - environments: [prod]
`

func TestLegacySlackBlockRejectedWithHint(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml":        sharedYAMLMin,
		"pulumi.yaml":        engineYAMLMin,
		"notifications.yaml": legacyNotifications,
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "migrate-config") {
		t.Fatalf("want conversion hint for slack: block, got %v", err)
	}
}

func TestNotificationsV2ChannelsLoad(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"notifications.yaml": `version: 2
config_type: notifications
channels:
  - type: slack
    channel: "#deploys"
    auth_token: xoxb-x
    on: [plan, applied, drift_detected]
  - type: webhook
    name: audit
    url: https://example.test/hook
    on: [applied, failed]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	channels := cfg.Notifications.Channels
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}
	if channels[0].Type != "slack" || channels[1].EffectiveName() != "audit" {
		t.Fatalf("channels: %+v", channels)
	}
}

func TestTimelineChannelsLoadAndValidate(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"notifications.yaml": `version: 2
config_type: notifications
channels:
  - type: timeline_slack
    channel: "#infra-deploys"
    auth_token: xoxb-x
  - type: timeline_github
    on: [planning, plan, applying, applied, failed, blocked, break_glass]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	channels := cfg.Notifications.Channels
	if len(channels) != 2 || channels[0].Type != "timeline_slack" || channels[1].Type != "timeline_github" {
		t.Fatalf("channels: %+v", channels)
	}
	// timeline_slack with no on: is valid (defaults to all timeline events).
	if len(channels[0].On) != 0 {
		t.Fatalf("on: %v", channels[0].On)
	}
}

func TestNotificationsV3Rejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"notifications.yaml": `version: 3
config_type: notifications
`,
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported version 3 (want 1..2)") {
		t.Fatalf("want version error, got %v", err)
	}
}

func TestUnknownOnEventRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"notifications.yaml": `version: 2
config_type: notifications
channels:
  - type: webhook
    url: https://example.test/hook
    on: [aplied]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `unknown event "aplied"`) {
		t.Fatalf("want unknown-event error, got %v", err)
	}
	if !strings.Contains(err.Error(), "applied") {
		t.Fatalf("error should list valid events: %v", err)
	}
}

func TestUnknownOnEventInDriftRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"drift.yaml": `version: 1
config_type: drift
channels:
  - type: slack
    channel: "#drift"
    on: [drift_detcted]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "drift.yaml") {
		t.Fatalf("want drift.yaml event error, got %v", err)
	}
}

// A drift.yaml channel may only subscribe to drift events; a PR-flow event
// there is a config error (it would never fire in a drift run). This is what
// keeps reeve lint and the runtime loader in agreement.
func TestDriftChannelRejectsPRFlowEvent(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"drift.yaml": `version: 1
config_type: drift
channels:
  - type: slack
    channel: "#drift"
    on: [applied]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown drift event") {
		t.Fatalf("PR-flow event in drift.yaml must be rejected, got %v", err)
	}
}

func TestDriftChannelsLoad(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"drift.yaml": `version: 1
config_type: drift
channels:
  - type: slack
    channel: "#drift"
    on: [drift_detected]
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg.Drift.Channels) != 1 || cfg.Drift.Channels[0].Channel != "#drift" {
		t.Fatalf("channels: %+v", cfg.Drift.Channels)
	}
	if cfg.Drift.DeprecatedSinks != nil {
		t.Fatalf("DeprecatedSinks should be empty: %+v", cfg.Drift.DeprecatedSinks)
	}
}

func TestDriftBothSinksAndChannelsRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"drift.yaml": `version: 1
config_type: drift
channels:
  - type: slack
    channel: "#drift"
    on: [drift_detected]
sinks:
  - type: webhook
    url: https://example.test/hook
    on: [drift_detected]
`,
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "migrate-config") {
		t.Fatalf("want conversion hint for sinks: key, got %v", err)
	}
}
