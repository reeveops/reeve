// Package local implements local-dev auth providers (aws_profile,
// aws_sso, gcloud_adc) and env_passthrough. Local types refuse to run
// under CI=true per openspec/specs/auth.
package local

import (
	"context"
	"fmt"
	"os"

	"github.com/FynxLabs/reeve/internal/auth"
)

// AWSProfile reads credentials from the AWS config profile.
type AWSProfile struct {
	ProviderName string
	Profile      string
	Region       string
}

func (p *AWSProfile) Name() string { return p.ProviderName }
func (p *AWSProfile) Type() string { return "aws_profile" }

func (p *AWSProfile) Acquire(ctx context.Context) (*auth.Credential, error) {
	if err := auth.RefuseLocalInCI(p.Type(), os.Getenv("CI")); err != nil {
		return nil, err
	}
	env := map[string]string{"AWS_PROFILE": p.Profile}
	if p.Region != "" {
		env["AWS_REGION"] = p.Region
		env["AWS_DEFAULT_REGION"] = p.Region
	}
	return &auth.Credential{Env: env, Kind: "aws-profile", Source: p.ProviderName}, nil
}

// AWSSSO is a thin wrapper that asks the AWS SDK to use the SSO login
// cache (the user already ran `aws sso login`).
type AWSSSO struct {
	ProviderName string
	Profile      string
	Region       string
}

func (p *AWSSSO) Name() string { return p.ProviderName }
func (p *AWSSSO) Type() string { return "aws_sso" }

func (p *AWSSSO) Acquire(ctx context.Context) (*auth.Credential, error) {
	if err := auth.RefuseLocalInCI(p.Type(), os.Getenv("CI")); err != nil {
		return nil, err
	}
	env := map[string]string{"AWS_PROFILE": p.Profile}
	if p.Region != "" {
		env["AWS_REGION"] = p.Region
		env["AWS_DEFAULT_REGION"] = p.Region
	}
	return &auth.Credential{Env: env, Kind: "aws-sso", Source: p.ProviderName}, nil
}

// GcloudADC expects application-default credentials are already set up.
type GcloudADC struct {
	ProviderName string
}

func (p *GcloudADC) Name() string { return p.ProviderName }
func (p *GcloudADC) Type() string { return "gcloud_adc" }

func (p *GcloudADC) Acquire(ctx context.Context) (*auth.Credential, error) {
	if err := auth.RefuseLocalInCI(p.Type(), os.Getenv("CI")); err != nil {
		return nil, err
	}
	// Default location per `gcloud auth application-default login`.
	home, _ := os.UserHomeDir()
	adc := home + "/.config/gcloud/application_default_credentials.json"
	return &auth.Credential{
		Env: map[string]string{
			"GOOGLE_APPLICATION_CREDENTIALS": adc,
		},
		Kind: "gcloud-adc", Source: p.ProviderName,
	}, nil
}

// EnvPassthrough is the flagged escape hatch. Maps repo env vars to
// engine env vars. Refuses unless IUnderstand is true; emits a loud
// stderr warning every Acquire.
type EnvPassthrough struct {
	ProviderName string
	EnvVars      map[string]string // engineEnvKey → host env name
	IUnderstand  bool
}

func (p *EnvPassthrough) Name() string { return p.ProviderName }
func (p *EnvPassthrough) Type() string { return "env_passthrough" }

func (p *EnvPassthrough) Acquire(ctx context.Context) (*auth.Credential, error) {
	if !p.IUnderstand {
		return nil, fmt.Errorf("env_passthrough %q requires i_understand_this_is_dangerous: true", p.ProviderName)
	}
	fmt.Fprintf(os.Stderr, "⚠️  env_passthrough provider %q in use - long-lived credentials bypass reeve's zero-trust model\n", p.ProviderName)
	env := map[string]string{}
	for engineKey, hostKey := range p.EnvVars {
		env[engineKey] = os.Getenv(hostKey)
	}
	return &auth.Credential{Env: env, Kind: "env-passthrough", Source: p.ProviderName}, nil
}

// compile-time checks
var (
	_ auth.Provider = (*AWSProfile)(nil)
	_ auth.Provider = (*AWSSSO)(nil)
	_ auth.Provider = (*GcloudADC)(nil)
	_ auth.Provider = (*EnvPassthrough)(nil)
)
