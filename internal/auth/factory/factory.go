// Package factory constructs auth.Provider instances from auth.yaml
// declarations. Kept separate from internal/auth to keep pure binding
// logic free of cloud SDK dependencies.
package factory

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/FynxLabs/reeve/internal/auth"
	"github.com/FynxLabs/reeve/internal/auth/providers/awsoidc"
	"github.com/FynxLabs/reeve/internal/auth/providers/azurefed"
	"github.com/FynxLabs/reeve/internal/auth/providers/gcpwif"
	"github.com/FynxLabs/reeve/internal/auth/providers/githubapp"
	"github.com/FynxLabs/reeve/internal/auth/providers/local"
	"github.com/FynxLabs/reeve/internal/auth/providers/secrets"
	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// Build returns a ready Registry for the given auth config. Each provider
// is materialized but not yet acquired; callers invoke
// Registry.AcquireAll(ctx, names) at run time.
func Build(ctx context.Context, cfg *schemas.Auth) (*auth.Registry, error) {
	r := auth.NewRegistry()
	if cfg == nil {
		return r, nil
	}
	for name, decl := range cfg.Providers {
		p, err := buildOne(name, decl)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		if err := r.Register(p); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// ValidateLint runs the auth.Validate conflict check and surfaces
// dangerous providers (env_passthrough without i_understand, long
// durations, etc.) as lint errors/warnings. Returns errors only; warnings
// are printed to stderr.
func ValidateLint(cfg *schemas.Auth, stackRefs []string) error {
	if cfg == nil {
		return nil
	}
	// Check dangerous providers.
	for name, decl := range cfg.Providers {
		switch decl.Type {
		case "env_passthrough":
			if !decl.IUnderstandThisIsDangerous {
				return fmt.Errorf("provider %q (env_passthrough): requires i_understand_this_is_dangerous: true", name)
			}
			fmt.Fprintf(os.Stderr, "⚠️  provider %q is env_passthrough - long-lived credentials bypass zero-trust\n", name)
		case "aws_secrets_manager", "aws_ssm_parameter", "gcp_secret_manager", "azure_key_vault", "github_secret":
			// A secret provider without env_map fetches the secret and
			// exports nothing - dead config at best, a silent no-op at
			// worst. Require the mapping.
			if len(decl.EnvMap) == 0 {
				return fmt.Errorf("provider %q (%s): env_map is required - without it the provider exports nothing (map env var names to secret fields, e.g. env_map: { MY_TOKEN: \"\" } for a plain-string secret)", name, decl.Type)
			}
		}
		if decl.Duration != "" {
			d, err := time.ParseDuration(decl.Duration)
			if err != nil {
				return fmt.Errorf("provider %q: invalid duration %q: %w", name, decl.Duration, err)
			}
			if d > 4*time.Hour {
				fmt.Fprintf(os.Stderr, "⚠️  provider %q duration=%s exceeds the 4h recommended cap\n", name, d)
			}
		}
		if decl.TTL != "" {
			if _, err := time.ParseDuration(decl.TTL); err != nil {
				return fmt.Errorf("provider %q: invalid ttl %q: %w", name, decl.TTL, err)
			}
		}
	}

	// Bindings → auth.Binding[] for the conflict check.
	bindings := make([]auth.Binding, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		bindings = append(bindings, auth.Binding{
			StackPattern: b.Match.Stack,
			Mode:         auth.Mode(b.Match.Mode),
			Providers:    b.Providers,
			Override:     b.Override,
		})
	}
	decls := map[string]auth.ProviderDecl{}
	for n, d := range cfg.Providers {
		decls[n] = auth.ProviderDecl{Name: n, Type: d.Type}
	}
	return auth.Validate(bindings, decls, stackRefs)
}

func buildOne(name string, d schemas.ProviderYAML) (auth.Provider, error) {
	// Malformed durations fail closed: a swallowed error here silently
	// replaced a typo'd `duration: 30minutes` with the provider default.
	// config.Validate also rejects these; this guards direct Build callers.
	dur, err := parseDurationOrZero(d.Duration)
	if err != nil {
		return nil, fmt.Errorf("duration: invalid duration %q: %w", d.Duration, err)
	}
	ttl, err := parseDurationOrZero(d.TTL)
	if err != nil {
		return nil, fmt.Errorf("ttl: invalid duration %q: %w", d.TTL, err)
	}
	switch d.Type {
	case "aws_oidc":
		return awsoidc.New(name, d.RoleARN, d.SessionName, d.Region, d.AudienceOverride, dur), nil

	case "gcp_wif":
		return gcpwif.New(name, d.WorkloadIdentityProvider, d.ServiceAccount, d.AudienceOverride, dur), nil

	case "azure_federated":
		return azurefed.New(name, d.TenantID, d.ClientID, d.SubscriptionID, d.AudienceOverride, dur), nil

	case "github_app":
		appID, err := anyToInt64(d.AppID)
		if err != nil {
			return nil, fmt.Errorf("app_id: %w", err)
		}
		instID, err := anyToInt64(d.InstallationID)
		if err != nil {
			return nil, fmt.Errorf("installation_id: %w", err)
		}
		key, err := loadPrivateKey(d.PrivateKey)
		if err != nil {
			return nil, err
		}
		return githubapp.New(name, appID, instID, key), nil

	case "aws_secrets_manager":
		return secrets.NewAWSSecretsManager(&secrets.AWSSecretsManager{
			Name: name, SecretID: d.SecretID, Region: d.Region,
			EnvMap: d.EnvMap, TTL: ttl,
		}), nil

	case "aws_ssm_parameter":
		return secrets.NewAWSSSMParameter(&secrets.AWSSSMParameter{
			Name: name, Parameter: d.Parameter, Region: d.Region,
			EnvMap: d.EnvMap,
		}), nil

	case "gcp_secret_manager":
		return secrets.NewGCPSecretManager(&secrets.GCPSecretManager{
			Name: name, SecretName: d.GCPName, EnvMap: d.EnvMap,
		}), nil

	case "azure_key_vault":
		return secrets.NewAzureKeyVault(&secrets.AzureKeyVault{
			Name: name, VaultName: d.VaultName, SecretName: d.SecretName,
			EnvMap: d.EnvMap,
		}), nil

	case "github_secret":
		return secrets.NewGitHubSecret(&secrets.GitHubSecret{
			Name: name, EnvVar: d.EnvVar, EnvMap: d.EnvMap,
		}), nil

	case "aws_profile":
		return &local.AWSProfile{ProviderName: name, Profile: d.Profile, Region: d.Region}, nil
	case "aws_sso":
		return &local.AWSSSO{ProviderName: name, Profile: d.Profile, Region: d.Region}, nil
	case "gcloud_adc":
		return &local.GcloudADC{ProviderName: name}, nil
	case "env_passthrough":
		return &local.EnvPassthrough{
			ProviderName: name,
			EnvVars:      d.EnvVars,
			IUnderstand:  d.IUnderstandThisIsDangerous,
		}, nil

	default:
		return nil, fmt.Errorf("unknown provider type %q", d.Type)
	}
}

func parseDurationOrZero(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func anyToInt64(v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return 0, fmt.Errorf("required")
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case float64:
		return int64(t), nil
	case string:
		return parseInt64String(t)
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func parseInt64String(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// loadPrivateKey handles three forms: base64 blob, literal PEM, or a
// file path.
func loadPrivateKey(src string) ([]byte, error) {
	if src == "" {
		return nil, fmt.Errorf("private_key required")
	}
	// Heuristic: if it starts with "-----BEGIN", it's literal PEM.
	if len(src) > 10 && src[:11] == "-----BEGIN " {
		return []byte(src), nil
	}
	// Try file.
	if _, err := os.Stat(src); err == nil {
		// #nosec G304 -- src is auth.yaml's private_key, which is documented as accepting a file
		// path; reading an operator-named key file is the feature
		return os.ReadFile(src)
	}
	// Fall back to base64.
	b, err := base64.StdEncoding.DecodeString(src)
	if err != nil {
		return nil, fmt.Errorf("private_key: not PEM, file, or base64")
	}
	return b, nil
}
