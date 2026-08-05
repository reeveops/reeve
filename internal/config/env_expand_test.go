package config

import (
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

func TestExpandEnvDesignatedFields(t *testing.T) {
	t.Setenv("TEST_BUCKET", "resolved-bucket")
	t.Setenv("TEST_TOKEN", "resolved-token")
	t.Setenv("TEST_TENANT", "resolved-tenant")
	t.Setenv("TEST_APP_ID", "12345")

	c := &Config{
		Shared: &schemas.Shared{},
		Auth: &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
			"azure": {Type: "azure_federated", TenantID: "${env:TEST_TENANT}"},
			"gh":    {Type: "github_app", AppID: "${env:TEST_APP_ID}", PrivateKey: "${env:TEST_TOKEN}"},
		}},
		Notifications: &schemas.Notifications{Channels: []schemas.ChannelYAML{
			{
				Type:      "webhook",
				URL:       "https://api.example.com/hook/${env:TEST_TOKEN}", // embedded reference
				Headers:   map[string]string{"Authorization": "Bearer ${env:TEST_TOKEN}"},
				AuthToken: "${env:TEST_TOKEN}",
			},
			{Type: "pagerduty", IntegrationKey: "${env:TEST_TOKEN}"},
		}},
		Observability: &schemas.Observability{
			OTEL: schemas.OTELConfig{
				Endpoint: "${env:TEST_BUCKET}",
				Headers:  map[string]string{"Authorization": "${env:TEST_TOKEN}"},
			},
			Annotations: []schemas.AnnotationConfig{{Type: "grafana", APIKey: "${env:TEST_TOKEN}"}},
		},
	}
	c.Shared.Bucket.Name = "${env:TEST_BUCKET}" // designated
	c.Shared.Bucket.Type = "gcs"                // literal, must not change
	c.Shared.Locking.AdminOverride.Allowed = []string{"${env:TEST_TENANT}", "@literal"}

	warnings := c.ExpandEnv()
	if len(warnings) != 0 {
		t.Fatalf("designated-only references must not warn: %v", warnings)
	}

	if c.Shared.Bucket.Name != "resolved-bucket" {
		t.Errorf("bucket.name not expanded: %q", c.Shared.Bucket.Name)
	}
	if c.Shared.Bucket.Type != "gcs" {
		t.Errorf("literal bucket.type mutated: %q", c.Shared.Bucket.Type)
	}
	if got := c.Shared.Locking.AdminOverride.Allowed; got[0] != "resolved-tenant" || got[1] != "@literal" {
		t.Errorf("admin_override.allowed expansion wrong: %v", got)
	}
	if got := c.Auth.Providers["azure"].TenantID; got != "resolved-tenant" {
		t.Errorf("auth tenant_id not expanded: %q", got)
	}
	if got := c.Auth.Providers["gh"].PrivateKey; got != "resolved-token" {
		t.Errorf("auth private_key not expanded: %q", got)
	}
	if got := c.Auth.Providers["gh"].AppID; got != "12345" {
		t.Errorf("auth app_id (any-typed) not expanded: %v", got)
	}
	wh := c.Notifications.Channels[0]
	if wh.URL != "https://api.example.com/hook/resolved-token" {
		t.Errorf("embedded url reference not expanded: %q", wh.URL)
	}
	if wh.Headers["Authorization"] != "Bearer resolved-token" {
		t.Errorf("embedded header reference not expanded: %q", wh.Headers["Authorization"])
	}
	if wh.AuthToken != "resolved-token" {
		t.Errorf("auth_token not expanded: %q", wh.AuthToken)
	}
	if got := c.Notifications.Channels[1].IntegrationKey; got != "resolved-token" {
		t.Errorf("integration_key not expanded: %q", got)
	}
	if c.Observability.OTEL.Endpoint != "resolved-bucket" {
		t.Errorf("otel endpoint not expanded: %q", c.Observability.OTEL.Endpoint)
	}
	if c.Observability.OTEL.Headers["Authorization"] != "resolved-token" {
		t.Errorf("otel header not expanded: %q", c.Observability.OTEL.Headers["Authorization"])
	}
	if c.Observability.Annotations[0].APIKey != "resolved-token" {
		t.Errorf("annotation api_key not expanded: %q", c.Observability.Annotations[0].APIKey)
	}
}

// TestExpandEnvNonDesignatedStaysLiteral is the allow-list security
// property: a ${env:NAME} reference OUTSIDE the designated field set is
// never expanded (no env-var oracle for attacker-controlled PR-head
// config) and draws a warning naming the field.
func TestExpandEnvNonDesignatedStaysLiteral(t *testing.T) {
	t.Setenv("SUPER_SECRET", "hunter2-super-secret")

	c := &Config{
		Shared: &schemas.Shared{},
		Engines: []*schemas.Engine{{Engine: schemas.EngineBody{
			State: schemas.EngineState{URL: "${env:SUPER_SECRET}"},
		}}},
	}
	c.Shared.Approvals.Stacks = map[string]schemas.ApprovalRuleYAML{
		"prod/*": {Approvers: []string{"${env:SUPER_SECRET}"}},
	}
	c.Shared.LogLevel = "${env:SUPER_SECRET}"

	warnings := c.ExpandEnv()

	if got := c.Shared.Approvals.Stacks["prod/*"].Approvers[0]; got != "${env:SUPER_SECRET}" {
		t.Errorf("non-designated approver expanded: %q", got)
	}
	if got := c.Engines[0].Engine.State.URL; got != "${env:SUPER_SECRET}" {
		t.Errorf("non-designated engine state url expanded: %q", got)
	}
	if c.Shared.LogLevel != "${env:SUPER_SECRET}" {
		t.Errorf("non-designated log_level expanded: %q", c.Shared.LogLevel)
	}
	if len(warnings) != 3 {
		t.Fatalf("want 3 warnings (one per literal ref), got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, "\n")
	for _, path := range []string{"approvals.stacks[prod/*].approvers[0]", "engines[0].engine.state.url", "log_level"} {
		if !strings.Contains(joined, path) {
			t.Errorf("warnings missing field path %q: %v", path, warnings)
		}
	}
	for _, w := range warnings {
		if !strings.Contains(w, "env expansion is not supported") {
			t.Errorf("warning should say expansion is unsupported: %q", w)
		}
	}
}

func TestExpandEnvMissingVarBecomesEmpty(t *testing.T) {
	// os.Getenv of an unset var is "" - matches the notify helper's behavior.
	c := &Config{Shared: &schemas.Shared{}}
	c.Shared.Bucket.Name = "${env:DEFINITELY_UNSET_XYZ}"
	c.ExpandEnv()
	if c.Shared.Bucket.Name != "" {
		t.Fatalf("unset env should expand to empty, got %q", c.Shared.Bucket.Name)
	}
}

func TestStrictDecodeRejectsMultiDoc(t *testing.T) {
	single := []byte("version: 1\nconfig_type: shared\n")
	if err := strictDecode(single, &schemas.Header{}); err != nil {
		t.Fatalf("single doc should decode: %v", err)
	}
	multi := []byte("version: 1\nconfig_type: shared\n---\nversion: 1\nconfig_type: auth\n")
	if err := strictDecode(multi, &schemas.Header{}); err == nil {
		t.Fatal("multi-doc must be rejected, not silently ignored")
	}
}

func TestMigrateHeaderOrderIndependent(t *testing.T) {
	// Reversed key order with an interleaved comment: the old single regex
	// failed; the split regexes must still find both.
	data := []byte("config_type: shared\n# a comment\nversion: 1\n")
	if m := versionRE.FindSubmatch(data); m == nil || string(m[1]) != "1" {
		t.Fatalf("versionRE failed on reversed+commented header: %v", m)
	}
	if m := configTypeRE.FindSubmatch(data); m == nil || string(m[1]) != "shared" {
		t.Fatalf("configTypeRE failed on reversed+commented header: %v", m)
	}
}
