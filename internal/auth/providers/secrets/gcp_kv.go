package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	azsecrets "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/FynxLabs/reeve/internal/auth"
)

// GCPSecretManager reads a secret via the Google SDK-free REST API.
// Uses GOOGLE_APPLICATION_CREDENTIALS or CLOUDSDK_AUTH_ACCESS_TOKEN from
// the environment (typically populated by a gcp_wif provider earlier in
// the binding list).
type GCPSecretManager struct {
	Name       string
	SecretName string // projects/X/secrets/Y/versions/Z (or /latest)
	EnvMap     map[string]string
}

func (p *GCPSecretManager) ProviderName() string { return p.Name }
func (p *GCPSecretManager) Type() string         { return "gcp_secret_manager" }

// secretManagerBase is a package var only so tests can point the lookup
// at an httptest server; production behavior is unchanged.
var secretManagerBase = "https://secretmanager.googleapis.com" // #nosec G101 -- public API endpoint URL, not a credential

func (p *GCPSecretManager) Acquire(ctx context.Context) (*auth.Credential, error) {
	token := os.Getenv("CLOUDSDK_AUTH_ACCESS_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("gcp_secret_manager: no CLOUDSDK_AUTH_ACCESS_TOKEN; bind a gcp_wif provider first")
	}
	url := fmt.Sprintf("%s/v1/%s:access", secretManagerBase, p.SecretName)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("secretmanager %d: %s", resp.StatusCode, string(body))
	}
	// Response shape: {"payload":{"data":"<base64>"}} - the secret bytes
	// are base64 nested under payload, per the Secret Manager access API.
	var out struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Payload.Data == "" {
		return nil, fmt.Errorf("secretmanager: unexpected payload")
	}
	decoded, err := base64StdDecode(out.Payload.Data)
	if err != nil {
		return nil, err
	}
	env, err := applyEnvMap(p.EnvMap, decoded)
	if err != nil {
		return nil, fmt.Errorf("gcp_secret_manager %q: %w", p.SecretName, err)
	}
	return &auth.Credential{
		Env: env, Kind: "gcp-secret", Source: p.Name,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// AzureKeyVault reads a secret from Azure Key Vault using
// DefaultAzureCredential.
type AzureKeyVault struct {
	Name       string
	VaultName  string
	SecretName string
	EnvMap     map[string]string
}

func (p *AzureKeyVault) ProviderName() string { return p.Name }
func (p *AzureKeyVault) Type() string         { return "azure_key_vault" }

func (p *AzureKeyVault) Acquire(ctx context.Context) (*auth.Credential, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", p.VaultName)
	cli, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.GetSecret(ctx, p.SecretName, "", nil)
	if err != nil {
		return nil, err
	}
	value := ""
	if resp.Value != nil {
		value = *resp.Value
	}
	env, err := applyEnvMap(p.EnvMap, value)
	if err != nil {
		return nil, fmt.Errorf("azure_key_vault %q: %w", p.SecretName, err)
	}
	return &auth.Credential{
		Env: env, Kind: "azure-kv", Source: p.Name,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// Constructors.
type gcpShim struct{ *GCPSecretManager }

func (s *gcpShim) Name() string { return s.GCPSecretManager.Name }
func (s *gcpShim) Type() string { return s.GCPSecretManager.Type() }
func (s *gcpShim) Acquire(ctx context.Context) (*auth.Credential, error) {
	return s.GCPSecretManager.Acquire(ctx)
}

type azShim struct{ *AzureKeyVault }

func (s *azShim) Name() string { return s.AzureKeyVault.Name }
func (s *azShim) Type() string { return s.AzureKeyVault.Type() }
func (s *azShim) Acquire(ctx context.Context) (*auth.Credential, error) {
	return s.AzureKeyVault.Acquire(ctx)
}

func NewGCPSecretManager(p *GCPSecretManager) auth.Provider { return &gcpShim{p} }
func NewAzureKeyVault(p *AzureKeyVault) auth.Provider       { return &azShim{p} }

// base64StdDecode is isolated for testing and to avoid pulling encoding/base64
// into every file; re-use stdlib via a small helper.
func base64StdDecode(s string) (string, error) {
	// Pad if needed.
	if mod := len(s) % 4; mod != 0 {
		s += strings.Repeat("=", 4-mod)
	}
	return stdB64Decode(s)
}
