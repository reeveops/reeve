package run

import (
	"context"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
)

type fakeProvider struct {
	name, typ string
	env       map[string]string
	err       error
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Type() string { return p.typ }
func (p *fakeProvider) Acquire(context.Context) (*auth.Credential, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &auth.Credential{Env: p.env, Kind: p.typ, Source: p.name}, nil
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
	env, cleanup, err := ResolveAuthEnv(context.Background(), localAuthCfg(), reg,
		"prod/api", auth.ModePreview, LocalAuth{Enabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if env["GOOGLE_APPLICATION_CREDENTIALS"] == "" {
		t.Fatalf("expected ADC env, got %v", env)
	}

	// CI run acquires the bound WIF provider (which fails here) with no hint.
	_, _, err = ResolveAuthEnv(context.Background(), localAuthCfg(), reg,
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

	_, _, err := ResolveAuthEnv(context.Background(), cfg, reg,
		"prod/api", auth.ModePreview, LocalAuth{Enabled: true})
	if err == nil {
		t.Fatal("expected acquire error")
	}
	if !strings.Contains(err.Error(), "--local-auth") || !strings.Contains(err.Error(), "local:") {
		t.Fatalf("local failure should hint at local: and --local-auth: %v", err)
	}

	// The CLI override rescues the same config.
	env, cleanup, err := ResolveAuthEnv(context.Background(), cfg, reg,
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
