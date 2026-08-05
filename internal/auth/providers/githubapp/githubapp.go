// Package githubapp implements the github_app provider. Signs a JWT with
// the app's private key, exchanges it for a short-lived installation
// access token, and emits GITHUB_TOKEN for consumers.
package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/FynxLabs/reeve/internal/auth"
)

// Provider is a single github_app provider instance.
type Provider struct {
	name           string
	appID          int64
	installationID int64
	privateKey     []byte // PEM
}

func New(name string, appID, installID int64, privateKeyPEM []byte) *Provider {
	return &Provider{name: name, appID: appID, installationID: installID, privateKey: privateKeyPEM}
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Type() string { return "github_app" }

// apiBase is a package var only so tests can point the exchange at an
// httptest server; production behavior is unchanged.
var apiBase = "https://api.github.com"

// Acquire signs a JWT with the app's private key, then exchanges it at
// /app/installations/{id}/access_tokens for an installation token.
func (p *Provider) Acquire(ctx context.Context) (*auth.Credential, error) {
	signed, err := signAppJWT(p.appID, p.privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, p.installationID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		return nil, fmt.Errorf("github app token %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &auth.Credential{
		Env: map[string]string{
			"GITHUB_TOKEN": out.Token,
		},
		Kind:      "github-app",
		Source:    p.name,
		ExpiresAt: out.ExpiresAt,
	}, nil
}

func signAppJWT(appID int64, pemBytes []byte) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return t.SignedString(key)
}

var _ auth.Provider = (*Provider)(nil)
