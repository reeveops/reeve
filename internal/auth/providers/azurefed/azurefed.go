// Package azurefed implements the azure_federated provider. GitHub OIDC
// → Azure AD federated credential → short-lived access token for ARM.
// Emits AZURE_* env vars consumable by Pulumi's azure-native provider
// and the az CLI.
package azurefed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/auth"
)

// Provider is a single azure_federated provider instance.
type Provider struct {
	name           string
	tenantID       string
	clientID       string
	subscriptionID string
	audience       string
	duration       time.Duration
}

func New(name, tenant, client, subscription, audience string, duration time.Duration) *Provider {
	if audience == "" {
		audience = "api://AzureADTokenExchange"
	}
	if duration == 0 {
		duration = time.Hour
	}
	return &Provider{
		name: name, tenantID: tenant, clientID: client,
		subscriptionID: subscription, audience: audience, duration: duration,
	}
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Type() string { return "azure_federated" }

// Acquire exchanges a GitHub OIDC token for an Azure AD access token via
// the client credentials flow with client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer.
func (p *Provider) Acquire(ctx context.Context) (*auth.Credential, error) {
	oidc, err := fetchGitHubOIDC(ctx, p.audience)
	if err != nil {
		return nil, err
	}
	token, expiresAt, err := tokenExchange(ctx, p.tenantID, p.clientID, oidc)
	if err != nil {
		return nil, err
	}
	env := map[string]string{
		"AZURE_TENANT_ID":       p.tenantID,
		"AZURE_CLIENT_ID":       p.clientID,
		"AZURE_SUBSCRIPTION_ID": p.subscriptionID,
		"ARM_TENANT_ID":         p.tenantID,
		"ARM_CLIENT_ID":         p.clientID,
		"ARM_SUBSCRIPTION_ID":   p.subscriptionID,
		"AZURE_ACCESS_TOKEN":    token,
		"ARM_ACCESS_TOKEN":      token,
		"AZURE_USE_OIDC":        "true",
	}
	return &auth.Credential{
		Env: env, Kind: "azure-aad", Source: p.name, ExpiresAt: expiresAt,
	}, nil
}

// loginBase is a package var only so tests can point the exchange at an
// httptest server; production behavior is unchanged.
var loginBase = "https://login.microsoftonline.com"

func tokenExchange(ctx context.Context, tenant, clientID, assertion string) (string, time.Time, error) {
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", loginBase, tenant)
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	form.Set("client_assertion", assertion)
	form.Set("scope", "https://management.azure.com/.default")

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", time.Time{}, fmt.Errorf("azure token exchange %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, err
	}
	return out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn) * time.Second), nil
}

func fetchGitHubOIDC(ctx context.Context, audience string) (string, error) {
	u := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	tok := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if u == "" || tok == "" {
		return "", fmt.Errorf("ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not set (azure_federated works only inside GitHub Actions with id-token: write)")
	}
	if audience != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u = u + sep + "audience=" + audience
	}
	// #nosec G704 -- URL is ACTIONS_ID_TOKEN_REQUEST_URL, injected by the Actions runner, not read
	// from .reeve config or PR content; the call is refused when it or its paired
	// token is unset
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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
		return "", fmt.Errorf("oidc %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Value, nil
}

var _ auth.Provider = (*Provider)(nil)
