// Package awsoidc implements the aws_oidc provider. It exchanges a
// GitHub Actions OIDC token for short-lived AWS STS credentials via
// AssumeRoleWithWebIdentity. Reeve never stores long-lived secrets.
package awsoidc

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/auth/githuboidc"
)

// Provider is a single aws_oidc provider instance.
type Provider struct {
	name        string
	roleARN     string
	sessionName string
	region      string
	duration    time.Duration
	audience    string
}

// New returns a configured provider. Required: name, roleARN.
func New(name, roleARN, sessionName, region, audience string, duration time.Duration) *Provider {
	if sessionName == "" {
		sessionName = "reeve"
	}
	if duration == 0 {
		duration = time.Hour
	}
	if audience == "" {
		audience = "sts.amazonaws.com"
	}
	return &Provider{
		name: name, roleARN: roleARN, sessionName: sessionName,
		region: region, duration: duration, audience: audience,
	}
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Type() string { return "aws_oidc" }

// Acquire exchanges the GitHub OIDC token at $ACTIONS_ID_TOKEN_REQUEST_URL
// + $ACTIONS_ID_TOKEN_REQUEST_TOKEN for STS creds via
// AssumeRoleWithWebIdentity. Emits AWS_* env vars for the engine.
func (p *Provider) Acquire(ctx context.Context) (*auth.Credential, error) {
	token, err := githuboidc.Fetch(ctx, p.audience, "aws_oidc")
	if err != nil {
		return nil, fmt.Errorf("fetch oidc token: %w", err)
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if p.region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(p.region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	stsCli := sts.NewFromConfig(cfg)
	out, err := stsCli.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(p.roleARN),
		RoleSessionName:  aws.String(p.sessionName),
		WebIdentityToken: aws.String(token),
		DurationSeconds:  aws.Int32(int32(p.duration.Seconds())),
	})
	if err != nil {
		return nil, err
	}
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     aws.ToString(out.Credentials.AccessKeyId),
		"AWS_SECRET_ACCESS_KEY": aws.ToString(out.Credentials.SecretAccessKey),
		"AWS_SESSION_TOKEN":     aws.ToString(out.Credentials.SessionToken),
	}
	if p.region != "" {
		env["AWS_REGION"] = p.region
		env["AWS_DEFAULT_REGION"] = p.region
	}
	return &auth.Credential{
		Env:       env,
		Kind:      "aws-sts",
		Source:    p.name,
		ExpiresAt: aws.ToTime(out.Credentials.Expiration),
	}, nil
}

// compile-time check
var _ auth.Provider = (*Provider)(nil)
