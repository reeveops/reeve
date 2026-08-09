// Package githuboidc fetches an OIDC ID token from the GitHub Actions token
// service.
//
// Every federated provider (aws_oidc, gcp_wif, azure_federated) starts by
// asking the runner for this token and then trades it for cloud credentials.
// That first half is identical across all three, so it lives here once.
package githuboidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// client bounds the token-service call. http.DefaultClient has no timeout,
// so a wedged runner endpoint would hang the run indefinitely; 20s matches
// the notify package's shared client.
var client = &http.Client{Timeout: 20 * time.Second}

// Fetch returns a GitHub Actions OIDC ID token for audience, or the
// runner's default audience when it is empty.
//
// provider names the calling provider type so the "not running in Actions"
// error tells the operator which binding needs `id-token: write`.
func Fetch(ctx context.Context, audience, provider string) (string, error) {
	endpoint := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	tok := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if endpoint == "" || tok == "" {
		return "", fmt.Errorf("ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not set (%s works only inside GitHub Actions with id-token: write)", provider)
	}
	if audience != "" {
		// Build the query properly rather than concatenating: audience comes
		// from the provider's `audience:` config override, so it is operator
		// input and must be escaped.
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("parse ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
		}
		q := u.Query()
		q.Set("audience", audience)
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}

	// #nosec G704 -- URL is ACTIONS_ID_TOKEN_REQUEST_URL, injected by the Actions runner, not read
	// from .reeve config or PR content; the call is refused when it or its paired
	// token is unset
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json; api-version=2.0")

	// #nosec G704 -- same runner-provided endpoint as the request above
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body is the token service's error text, never the token itself
		// (that only ships on 200), so it is safe to surface.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("oidc token service %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Value == "" {
		return "", fmt.Errorf("oidc token service returned an empty token")
	}
	return out.Value, nil
}
