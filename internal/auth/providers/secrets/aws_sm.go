// Package secrets implements the secret-manager provider types:
// aws_secrets_manager, aws_ssm_parameter, gcp_secret_manager,
// azure_key_vault, github_secret (env).
//
// Each provider retrieves the secret value at Acquire-time and exposes
// it via env vars the engine consumes. Secrets are never persisted.
package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/FynxLabs/reeve/internal/auth"
)

// AWSSecretsManager pulls a secret and exposes it via env vars.
type AWSSecretsManager struct {
	Name     string
	SecretID string
	Region   string
	EnvMap   map[string]string // envKey → secret JSON field ("" = whole value)
	TTL      time.Duration
}

func (p *AWSSecretsManager) ProviderName() string { return p.Name }
func (p *AWSSecretsManager) Type() string         { return "aws_secrets_manager" }

func (p *AWSSecretsManager) Acquire(ctx context.Context) (*auth.Credential, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg)
	out, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(p.SecretID),
	})
	if err != nil {
		return nil, err
	}
	value := aws.ToString(out.SecretString)
	env, err := applyEnvMap(p.EnvMap, value)
	if err != nil {
		return nil, fmt.Errorf("aws_secrets_manager %q: %w", p.SecretID, err)
	}
	ttl := p.TTL
	if ttl == 0 {
		ttl = time.Hour
	}
	return &auth.Credential{
		Env: env, Kind: "aws-secret", Source: p.Name,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// AWSSSMParameter pulls an SSM parameter (decrypted).
type AWSSSMParameter struct {
	Name      string
	Parameter string
	Region    string
	EnvMap    map[string]string
}

func (p *AWSSSMParameter) ProviderName() string { return p.Name }
func (p *AWSSSMParameter) Type() string         { return "aws_ssm_parameter" }

func (p *AWSSSMParameter) Acquire(ctx context.Context) (*auth.Credential, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return nil, err
	}
	cli := ssm.NewFromConfig(cfg)
	out, err := cli.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(p.Parameter), WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	val := aws.ToString(out.Parameter.Value)
	env, err := applyEnvMap(p.EnvMap, val)
	if err != nil {
		return nil, fmt.Errorf("aws_ssm_parameter %q: %w", p.Parameter, err)
	}
	return &auth.Credential{Env: env, Kind: "aws-ssm", Source: p.Name, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// GitHubSecret reads an env var that Actions surfaces from repo secrets.
// The workflow wires $MY_SECRET_ENV into GitHubSecret.EnvVar=MY_SECRET_ENV.
type GitHubSecret struct {
	Name   string
	EnvVar string
	EnvMap map[string]string
}

func (p *GitHubSecret) ProviderName() string { return p.Name }
func (p *GitHubSecret) Type() string         { return "github_secret" }

func (p *GitHubSecret) Acquire(ctx context.Context) (*auth.Credential, error) {
	val := os.Getenv(p.EnvVar)
	if val == "" {
		return nil, fmt.Errorf("github_secret: env var %s is empty", p.EnvVar)
	}
	env, err := applyEnvMap(p.EnvMap, val)
	if err != nil {
		return nil, fmt.Errorf("github_secret %s: %w", p.EnvVar, err)
	}
	return &auth.Credential{Env: env, Kind: "github-secret", Source: p.Name}, nil
}

// applyEnvMap maps `{envKey: secretField}` to env vars. FAIL CLOSED:
//
//   - field "" exports the whole secret value, but ONLY when the secret is
//     a plain string. A JSON-object secret (a credential bundle) is never
//     exported wholesale - name the field instead.
//   - a named field must exist as a string in the JSON secret; a missing
//     or non-string field is a hard error naming the field, never a
//     silent fallback to the whole bundle.
//
// Error messages never include the secret value itself.
func applyEnvMap(m map[string]string, value string) (map[string]string, error) {
	if len(m) == 0 {
		// Lint rejects secret providers without env_map; at runtime an
		// empty map simply exports nothing.
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(m))
	for envKey, field := range m {
		if field == "" {
			if isJSONObject(value) {
				return nil, fmt.Errorf("env_map %s: secret is a JSON object; whole-secret export (empty field value) is only allowed for plain-string secrets - name the field to export instead", envKey)
			}
			out[envKey] = value
			continue
		}
		extracted, err := extractJSONField(value, field)
		if err != nil {
			return nil, fmt.Errorf("env_map %s: %w", envKey, err)
		}
		out[envKey] = extracted
	}
	return out, nil
}

// isJSONObject reports whether raw parses as a top-level JSON object
// (i.e. a credential bundle rather than a plain-string secret).
func isJSONObject(raw string) bool {
	var obj map[string]any
	return json.Unmarshal([]byte(raw), &obj) == nil
}

// extractJSONField pulls a string field out of a top-level JSON object,
// failing closed (with the field name, never the secret value) when the
// secret is not a JSON object, the field is absent, or it is not a string.
func extractJSONField(raw, field string) (string, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", fmt.Errorf("secret is not a JSON object; cannot extract field %q", field)
	}
	v, ok := obj[field]
	if !ok {
		return "", fmt.Errorf("field %q not found in secret", field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q in secret is not a string", field)
	}
	return s, nil
}

// compile-time checks
var _ auth.Provider = &awsSMShim{}
var _ auth.Provider = &awsSSMShim{}
var _ auth.Provider = &githubSecretShim{}

// Shims so adapters satisfy the Provider interface via a thin wrapper -
// keeps Name()/Type() collision with embedded fields at bay.
type awsSMShim struct{ *AWSSecretsManager }

func (s *awsSMShim) Name() string { return s.AWSSecretsManager.Name }
func (s *awsSMShim) Type() string { return s.AWSSecretsManager.Type() }
func (s *awsSMShim) Acquire(ctx context.Context) (*auth.Credential, error) {
	return s.AWSSecretsManager.Acquire(ctx)
}

type awsSSMShim struct{ *AWSSSMParameter }

func (s *awsSSMShim) Name() string { return s.AWSSSMParameter.Name }
func (s *awsSSMShim) Type() string { return s.AWSSSMParameter.Type() }
func (s *awsSSMShim) Acquire(ctx context.Context) (*auth.Credential, error) {
	return s.AWSSSMParameter.Acquire(ctx)
}

type githubSecretShim struct{ *GitHubSecret }

func (s *githubSecretShim) Name() string { return s.GitHubSecret.Name }
func (s *githubSecretShim) Type() string { return s.GitHubSecret.Type() }
func (s *githubSecretShim) Acquire(ctx context.Context) (*auth.Credential, error) {
	return s.GitHubSecret.Acquire(ctx)
}

// Constructors returning the auth.Provider interface directly.
func NewAWSSecretsManager(p *AWSSecretsManager) auth.Provider { return &awsSMShim{p} }
func NewAWSSSMParameter(p *AWSSSMParameter) auth.Provider     { return &awsSSMShim{p} }
func NewGitHubSecret(p *GitHubSecret) auth.Provider           { return &githubSecretShim{p} }
