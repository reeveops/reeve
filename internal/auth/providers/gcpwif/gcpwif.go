// Package gcpwif implements the gcp_wif provider: GitHub Actions OIDC
// token → Workload Identity Federation → short-lived GCP access token.
// Emits GOOGLE_APPLICATION_CREDENTIALS pointing at an ambient-credentials
// file the Google SDK can read, plus GCP_* env vars for Pulumi.
package gcpwif

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/auth/githuboidc"
)

// Provider is a single gcp_wif provider instance.
type Provider struct {
	name                     string
	workloadIdentityProvider string
	serviceAccount           string
	duration                 time.Duration
	audience                 string
}

// New returns a configured provider.
func New(name, wip, sa, audience string, duration time.Duration) *Provider {
	if duration == 0 {
		duration = time.Hour
	}
	if audience == "" {
		audience = "//iam.googleapis.com/" + wip
	}
	return &Provider{
		name: name, workloadIdentityProvider: wip,
		serviceAccount: sa, duration: duration, audience: audience,
	}
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Type() string { return "gcp_wif" }

// Acquire runs the WIF dance:
// 1. Get GitHub OIDC token with audience=//iam.googleapis.com/<wip>
// 2. POST to STS token exchange for a federated access token
// 3. POST to IAM Credentials generateAccessToken for an impersonated SA token
// Writes an ambient-credentials file and returns the path in
// GOOGLE_APPLICATION_CREDENTIALS.
func (p *Provider) Acquire(ctx context.Context) (*auth.Credential, error) {
	oidcToken, err := githuboidc.Fetch(ctx, p.audience, "gcp_wif")
	if err != nil {
		return nil, fmt.Errorf("fetch oidc token: %w", err)
	}

	stsToken, err := exchangeSTS(ctx, oidcToken, p.workloadIdentityProvider)
	if err != nil {
		return nil, fmt.Errorf("sts exchange: %w", err)
	}

	saToken, expiresAt, err := generateSAAccessToken(ctx, stsToken, p.serviceAccount, p.duration)
	if err != nil {
		return nil, fmt.Errorf("generate sa token: %w", err)
	}

	// Write ambient credentials file so SDKs + pulumi can find it. The
	// returned dir is captured by Cleanup so the file does not outlive the
	// run - long-lived self-hosted runners would otherwise accumulate
	// valid-token credentials in /tmp until reboot.
	credPath, credDir, err := writeAmbientCreds(p.name, saToken)
	if err != nil {
		return nil, err
	}

	return &auth.Credential{
		Env: map[string]string{
			"GOOGLE_APPLICATION_CREDENTIALS": credPath,
			"CLOUDSDK_AUTH_ACCESS_TOKEN":     saToken,
			"GOOGLE_OAUTH_ACCESS_TOKEN":      saToken,
		},
		Kind:      "gcp-sa",
		Source:    p.name,
		ExpiresAt: expiresAt,
		Cleanup: func() error {
			return os.RemoveAll(credDir)
		},
	}, nil
}

// stsEndpoint and iamCredentialsBase are package vars only so tests can
// point the exchange at an httptest server; production behavior is
// unchanged.
var (
	stsEndpoint        = "https://sts.googleapis.com/v1/token"
	iamCredentialsBase = "https://iamcredentials.googleapis.com" // #nosec G101 -- public API endpoint URL, not a credential
)

func exchangeSTS(ctx context.Context, oidcToken, wip string) (string, error) {
	form := url.Values{}
	form.Set("audience", "//iam.googleapis.com/"+wip)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("requested_token_type", "urn:ietf:params:oauth:token-type:access_token")
	form.Set("scope", "https://www.googleapis.com/auth/cloud-platform")
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:jwt")
	form.Set("subject_token", oidcToken)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		stsEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("sts %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.AccessToken, nil
}

func generateSAAccessToken(ctx context.Context, stsToken, sa string, ttl time.Duration) (string, time.Time, error) {
	payload := map[string]any{
		"scope":    []string{"https://www.googleapis.com/auth/cloud-platform"},
		"lifetime": fmt.Sprintf("%ds", int(ttl.Seconds())),
	}
	body, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("%s/v1/projects/-/serviceAccounts/%s:generateAccessToken", iamCredentialsBase, sa)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+stsToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", time.Time{}, fmt.Errorf("iamcredentials %d: %s", resp.StatusCode, string(rbody))
	}
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  string `json:"expireTime"`
	}
	if err := json.Unmarshal(rbody, &out); err != nil {
		return "", time.Time{}, err
	}
	exp, _ := time.Parse(time.RFC3339, out.ExpireTime)
	return out.AccessToken, exp, nil
}

// writeAmbientCreds materialises an external-account impersonation file the
// Google SDK can read via $GOOGLE_APPLICATION_CREDENTIALS, and returns both
// the file path (for the env var) and the temp directory (for Cleanup).
func writeAmbientCreds(name, accessToken string) (string, string, error) {
	dir, err := os.MkdirTemp("", "reeve-gcp-creds-")
	if err != nil {
		return "", "", err
	}
	creds := map[string]any{
		"type":         "external_account_authorized_user",
		"access_token": accessToken,
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("marshal credentials: %w", err)
	}
	path := filepath.Join(dir, "reeve-"+name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return path, dir, nil
}

// compile-time check
var _ auth.Provider = (*Provider)(nil)
