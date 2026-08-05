// Package awsoidc implements the aws_oidc provider. It exchanges a
// GitHub Actions OIDC token for short-lived AWS STS credentials via
// AssumeRoleWithWebIdentity. Reeve never stores long-lived secrets.
package awsoidc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/reeveops/reeve/internal/auth"
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
	token, err := fetchGitHubOIDC(ctx, p.audience)
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

// fetchGitHubOIDC calls the GH Actions token service. Outside of Actions
// this fails with a clear error - users can then switch to a non-OIDC
// provider for local dev.
func fetchGitHubOIDC(ctx context.Context, audience string) (string, error) {
	url := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	tok := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if url == "" || tok == "" {
		return "", fmt.Errorf("ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not set (aws_oidc works only inside GitHub Actions with id-token: write)")
	}
	if audience != "" {
		sep := "?"
		if contains(url, "?") {
			sep = "&"
		}
		url = url + sep + "audience=" + audience
	}
	// #nosec G704 -- URL is ACTIONS_ID_TOKEN_REQUEST_URL, injected by the Actions runner, not read
	// from .reeve config or PR content; the call is refused when it or its paired
	// token is unset
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json; api-version=2.0")

	// #nosec G704 -- same runner-provided endpoint as the request above
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("oidc token service %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Value, nil
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// compile-time check
var _ auth.Provider = (*Provider)(nil)
