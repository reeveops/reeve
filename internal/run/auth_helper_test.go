package run

import (
	"context"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
)

type fakeProvider struct {
	name, typ     string
	env           map[string]string
	secretEnvKeys []string
	err           error
	cleanup       func() error
	acquired      *bool
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Type() string { return p.typ }
func (p *fakeProvider) Acquire(context.Context) (*auth.Credential, error) {
	if p.acquired != nil {
		*p.acquired = true
	}
	if p.err != nil {
		return nil, p.err
	}
	return &auth.Credential{Env: p.env, Kind: p.typ, Source: p.name, Cleanup: p.cleanup, SecretEnvKeys: p.secretEnvKeys}, nil
}

func TestResolveStateAuthEnv(t *testing.T) {
	cleaned := false
	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{
		name: "pulumi-state", typ: "gcp_secret_manager",
		env:     map[string]string{"PULUMI_ACCESS_TOKEN": "short-lived"},
		cleanup: func() error { cleaned = true; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	engine := &schemas.Engine{Engine: schemas.EngineBody{
		State: schemas.EngineState{AuthProvider: "pulumi-state"},
	}}
	env, _, cleanup, err := ResolveStateAuthEnv(context.Background(), engine, reg)
	if err != nil {
		t.Fatal(err)
	}
	if env["PULUMI_ACCESS_TOKEN"] != "short-lived" {
		t.Fatalf("state credential missing: %#v", env)
	}
	cleanup()
	if !cleaned {
		t.Fatal("state credential cleanup did not run")
	}
}

// TestResolveAuthEnvNarrowsSecretRegistration pins the classification
// contract: a credential that declares SecretEnvKeys opts its non-secret
// keys OUT (region and path values are operator-meaningful and must stay
// readable in output); a credential that does not classify fails closed -
// every value stays a secret (over-redact, never under-redact).
func TestResolveAuthEnvNarrowsSecretRegistration(t *testing.T) {
	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{
		name: "aws-classified", typ: "aws_sts",
		env: map[string]string{
			"AWS_REGION":        "us-east-1",
			"AWS_SESSION_TOKEN": "aws-session-token-value",
		},
		secretEnvKeys: []string{"AWS_SESSION_TOKEN"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeProvider{
		name: "gcp-classified", typ: "gcp_wif",
		env: map[string]string{
			"GOOGLE_APPLICATION_CREDENTIALS": "/home/run/.reeve/creds.json",
			"CLOUDSDK_AUTH_ACCESS_TOKEN":     "gcp-access-token-value",
		},
		secretEnvKeys: []string{"CLOUDSDK_AUTH_ACCESS_TOKEN"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeProvider{
		name: "secrets-manager", typ: "aws_secret_manager",
		env: map[string]string{"SECRET": "sm-value"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &schemas.Auth{
		Providers: map[string]schemas.ProviderYAML{
			"aws-classified":  {Type: "aws_sts"},
			"gcp-classified":  {Type: "gcp_wif"},
			"secrets-manager": {Type: "aws_secret_manager"},
		},
		Bindings: []schemas.BindingYAML{{
			Match:     schemas.BindingMatch{Stack: "*/*", Mode: "apply"},
			Providers: []string{"aws-classified", "gcp-classified", "secrets-manager"},
		}},
	}
	env, secrets, cleanup, err := resolveAuthEnv(context.Background(), cfg, reg, "proj/dev", auth.ModeApply, LocalAuth{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if env == nil || env["AWS_REGION"] != "us-east-1" {
		t.Fatalf("env map must carry all values: %#v", env)
	}
	for _, want := range []string{"aws-session-token-value", "gcp-access-token-value", "sm-value"} {
		if _, ok := secrets[want]; !ok {
			t.Fatalf("credential values must stay registered as secrets: missing %q in %v", want, secrets)
		}
	}
	for _, notSecret := range []string{"us-east-1", "/home/run/.reeve/creds.json"} {
		if _, ok := secrets[notSecret]; ok {
			t.Fatalf("region and path values must NOT be registered as secrets: %q in %v", notSecret, secrets)
		}
	}
}

// TestSecretValuesClassification is the table over the resolver rule itself:
// nil keys = all values are secrets (fail closed for unclassified
// providers like secret managers and env passthrough); an explicit set
// registers exactly those values; an explicitly EMPTY set registers
// nothing (AWS_PROFILE and friends carry names, not credentials).
func TestSecretValuesClassification(t *testing.T) {
	cases := []struct {
		name        string
		cred        *auth.Credential
		wantSecrets []string
		wantLen     int
	}{
		{
			"nil keys registers everything",
			&auth.Credential{Env: map[string]string{"A": "va", "B": "vb"}},
			[]string{"va", "vb"}, 2,
		},
		{
			"explicit keys registers only those",
			&auth.Credential{
				Env:           map[string]string{"TOKEN": "vt", "REGION": "vr"},
				SecretEnvKeys: []string{"TOKEN"},
			},
			[]string{"vt"}, 1,
		},
		{
			"explicit empty registers nothing",
			&auth.Credential{
				Env:           map[string]string{"AWS_PROFILE": "prod-write", "AWS_REGION": "us-east-1"},
				SecretEnvKeys: []string{},
			},
			nil, 0,
		},
		{
			"a key named but absent from env registers nothing",
			&auth.Credential{
				Env:           map[string]string{"REGION": "vr"},
				SecretEnvKeys: []string{"TOKEN"},
			},
			nil, 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := authSecretValues([]*auth.Credential{c.cred})
			for _, want := range c.wantSecrets {
				if _, ok := got[want]; !ok {
					t.Fatalf("want %q registered, got %v", want, got)
				}
			}
			if len(got) != c.wantLen {
				t.Fatalf("registered %v, want exactly %d values", got, c.wantLen)
			}
		})
	}
}

func TestResolveStateAuthEnvPulumiPassphraseSelection(t *testing.T) {
	tests := []struct {
		name       string
		typ        string
		configured string
		controller string
		want       string
		wantSet    bool
	}{
		{name: "selected ignores controller value", typ: "passphrase", controller: "workflow-passphrase"},
		{name: "configured value wins", typ: "passphrase", configured: "configured-passphrase", controller: "workflow-passphrase", want: "configured-passphrase", wantSet: true},
		{name: "selected without value", typ: "passphrase"},
		{name: "unselected provider", typ: "awskms", controller: "must-stay-in-controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PULUMI_CONFIG_PASSPHRASE", tt.controller)
			engine := &schemas.Engine{Engine: schemas.EngineBody{State: schemas.EngineState{
				SecretsProvider: schemas.EngineSecretsProvider{Type: tt.typ, Passphrase: tt.configured},
			}}}
			env, _, cleanup, err := ResolveStateAuthEnv(context.Background(), engine, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			got, ok := env["PULUMI_CONFIG_PASSPHRASE"]
			if ok != tt.wantSet || got != tt.want {
				t.Fatalf("PULUMI_CONFIG_PASSPHRASE = %q, present=%t; want %q, present=%t", got, ok, tt.want, tt.wantSet)
			}
		})
	}
}

func TestResolveStateAuthEnvPulumiPassphraseProvider(t *testing.T) {
	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{
		name: "pulumi-passphrase", typ: "gcp_secret_manager",
		env: map[string]string{"PULUMI_CONFIG_PASSPHRASE": "provider-passphrase"},
	}); err != nil {
		t.Fatal(err)
	}
	engine := &schemas.Engine{Engine: schemas.EngineBody{State: schemas.EngineState{
		AuthProvider: "pulumi-passphrase",
		SecretsProvider: schemas.EngineSecretsProvider{
			Type:       "passphrase",
			Passphrase: "configured-passphrase",
		},
	}}}
	env, _, cleanup, err := ResolveStateAuthEnv(context.Background(), engine, reg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got := env["PULUMI_CONFIG_PASSPHRASE"]; got != "provider-passphrase" {
		t.Fatalf("PULUMI_CONFIG_PASSPHRASE = %q, want provider value", got)
	}
}

func TestResolveAuthEnvHandlesNilRegistry(t *testing.T) {
	t.Parallel()

	env, _, cleanup, err := ResolveAuthEnv(context.Background(), localAuthCfg(), nil,
		"prod/api", auth.ModePreview, LocalAuth{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(env) != 0 {
		t.Fatalf("nil registry returned credentials: %#v", env)
	}
}

func TestResolveStateAuthEnvHandlesNilRegistry(t *testing.T) {
	t.Parallel()

	engine := &schemas.Engine{Engine: schemas.EngineBody{State: schemas.EngineState{
		AuthProvider: "missing",
	}}}
	env, _, cleanup, err := ResolveStateAuthEnv(context.Background(), engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(env) != 0 {
		t.Fatalf("nil registry returned state credentials: %#v", env)
	}
}

func localAuthCfg() *schemas.Auth {
	return &schemas.Auth{
		Providers: map[string]schemas.ProviderYAML{
			"gcp-prod":  {Type: "gcp_wif"},
			"gcp-local": {Type: "gcloud_adc"},
		},
		Bindings: []schemas.BindingYAML{
			{
				Match:     schemas.BindingMatch{Stack: "prod/*"},
				Providers: []string{"gcp-prod"},
				Local:     []string{"gcp-local"},
			},
		},
	}
}

func TestResolveAuthEnvLocalSubstitution(t *testing.T) {
	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{name: "gcp-prod", typ: "gcp_wif",
		err: context.DeadlineExceeded}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeProvider{name: "gcp-local", typ: "gcloud_adc",
		env: map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": "/home/u/adc.json"}}); err != nil {
		t.Fatal(err)
	}

	// Local run resolves the substitute, never touching the failing WIF provider.
	env, _, cleanup, err := ResolveAuthEnv(context.Background(), localAuthCfg(), reg,
		"prod/api", auth.ModePreview, LocalAuth{Enabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if env["GOOGLE_APPLICATION_CREDENTIALS"] == "" {
		t.Fatalf("expected ADC env, got %v", env)
	}

	// CI run acquires the bound WIF provider (which fails here) with no hint.
	_, _, _, err = ResolveAuthEnv(context.Background(), localAuthCfg(), reg,
		"prod/api", auth.ModePreview, LocalAuth{})
	if err == nil {
		t.Fatal("expected CI acquire error")
	}
	if strings.Contains(err.Error(), "--local-auth") {
		t.Fatalf("CI error should not carry the local hint: %v", err)
	}
}

func TestResolveAuthEnvLocalFailureHint(t *testing.T) {
	cfg := localAuthCfg()
	b := cfg.Bindings[0]
	b.Local = nil // no substitute configured
	cfg.Bindings[0] = b

	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{name: "gcp-prod", typ: "gcp_wif",
		err: context.DeadlineExceeded}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeProvider{name: "gcp-local", typ: "gcloud_adc"}); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := ResolveAuthEnv(context.Background(), cfg, reg,
		"prod/api", auth.ModePreview, LocalAuth{Enabled: true})
	if err == nil {
		t.Fatal("expected acquire error")
	}
	if !strings.Contains(err.Error(), "--local-auth") || !strings.Contains(err.Error(), "local:") {
		t.Fatalf("local failure should hint at local: and --local-auth: %v", err)
	}

	// The CLI override rescues the same config.
	env, _, cleanup, err := ResolveAuthEnv(context.Background(), cfg, reg,
		"prod/api", auth.ModePreview, LocalAuth{Enabled: true, Providers: []string{"gcp-local"}})
	if err != nil {
		t.Fatalf("unexpected error with --local-auth: %v", err)
	}
	defer cleanup()
	if len(env) != 0 {
		// gcp-local fake exports nothing; just assert resolution succeeded.
		t.Fatalf("unexpected env %v", env)
	}
}
