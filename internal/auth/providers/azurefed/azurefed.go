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
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/auth/githuboidc"
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
	oidc, err := githuboidc.Fetch(ctx, p.audience, "azure_federated")
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

var _ auth.Provider = (*Provider)(nil)
