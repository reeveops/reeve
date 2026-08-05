package factory

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// testKeyPEM generates a throwaway PKCS1 key at test runtime; nothing
// real-looking is ever committed.
func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func TestBuildNilConfigYieldsEmptyRegistry(t *testing.T) {
	r, err := Build(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("anything"); ok {
		t.Error("nil config must produce an empty registry")
	}
}

func TestBuildResolvesEveryProviderType(t *testing.T) {
	keyPEM := testKeyPEM(t)
	cases := []struct {
		name     string
		decl     schemas.ProviderYAML
		wantType string
	}{
		{"aws", schemas.ProviderYAML{Type: "aws_oidc", RoleARN: "arn:aws:iam::000000000000:role/x"}, "aws_oidc"},
		{"gcp", schemas.ProviderYAML{Type: "gcp_wif", WorkloadIdentityProvider: "projects/0/locations/global/workloadIdentityPools/p/providers/x", ServiceAccount: "sa@example-project.iam.gserviceaccount.com"}, "gcp_wif"},
		{"azure", schemas.ProviderYAML{Type: "azure_federated", TenantID: "t", ClientID: "c", SubscriptionID: "s"}, "azure_federated"},
		{"ghapp", schemas.ProviderYAML{Type: "github_app", AppID: 1, InstallationID: 2, PrivateKey: keyPEM}, "github_app"},
		{"awssm", schemas.ProviderYAML{Type: "aws_secrets_manager", SecretID: "s", Region: "us-east-1"}, "aws_secrets_manager"},
		{"awsssm", schemas.ProviderYAML{Type: "aws_ssm_parameter", Parameter: "/p", Region: "us-east-1"}, "aws_ssm_parameter"},
		{"gcpsm", schemas.ProviderYAML{Type: "gcp_secret_manager", GCPName: "projects/p/secrets/s/versions/latest"}, "gcp_secret_manager"},
		{"azkv", schemas.ProviderYAML{Type: "azure_key_vault", VaultName: "v", SecretName: "s"}, "azure_key_vault"},
		{"ghsecret", schemas.ProviderYAML{Type: "github_secret", EnvVar: "X"}, "github_secret"},
		{"awsprofile", schemas.ProviderYAML{Type: "aws_profile", Profile: "dev"}, "aws_profile"},
		{"awssso", schemas.ProviderYAML{Type: "aws_sso", Profile: "dev"}, "aws_sso"},
		{"adc", schemas.ProviderYAML{Type: "gcloud_adc"}, "gcloud_adc"},
		{"passthrough", schemas.ProviderYAML{Type: "env_passthrough", IUnderstandThisIsDangerous: true}, "env_passthrough"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{"p1": tc.decl}}
			r, err := Build(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			p, ok := r.Get("p1")
			if !ok {
				t.Fatal("provider not registered under its config name")
			}
			if p.Name() != "p1" {
				t.Errorf("Name() = %q, want config key p1", p.Name())
			}
			if p.Type() != tc.wantType {
				t.Errorf("Type() = %q, want %q", p.Type(), tc.wantType)
			}
		})
	}
}

func TestBuildUnknownTypeFailsClosed(t *testing.T) {
	cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"mystery": {Type: "vault_dynamic"},
	}}
	_, err := Build(context.Background(), cfg)
	if err == nil {
		t.Fatal("unknown provider type must fail, not be skipped")
	}
	if !strings.Contains(err.Error(), `provider "mystery"`) || !strings.Contains(err.Error(), `unknown provider type "vault_dynamic"`) {
		t.Errorf("error should name provider and type: %v", err)
	}
}

func TestBuildGitHubAppFieldValidation(t *testing.T) {
	keyPEM := testKeyPEM(t)
	cases := []struct {
		name    string
		decl    schemas.ProviderYAML
		wantSub string
	}{
		{"missing app_id", schemas.ProviderYAML{Type: "github_app", InstallationID: 2, PrivateKey: keyPEM}, "app_id"},
		{"missing installation_id", schemas.ProviderYAML{Type: "github_app", AppID: 1, PrivateKey: keyPEM}, "installation_id"},
		{"missing private_key", schemas.ProviderYAML{Type: "github_app", AppID: 1, InstallationID: 2}, "private_key required"},
		{"garbage private_key", schemas.ProviderYAML{Type: "github_app", AppID: 1, InstallationID: 2, PrivateKey: "!not/base64!"}, "not PEM, file, or base64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{"gh": tc.decl}}
			_, err := Build(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestAnyToInt64(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    int64
		wantErr bool
	}{
		{"int", 42, 42, false},
		{"int64", int64(42), 42, false},
		{"float64 from yaml", float64(42), 42, false},
		{"numeric string", "42", 42, false},
		{"nil is required", nil, 0, true},
		{"non-numeric string", "forty-two", 0, true},
		{"bool unsupported", true, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := anyToInt64(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoadPrivateKeyForms(t *testing.T) {
	pemStr := testKeyPEM(t)

	t.Run("literal pem", func(t *testing.T) {
		got, err := loadPrivateKey(pemStr)
		if err != nil || string(got) != pemStr {
			t.Fatalf("literal PEM not passed through: %v", err)
		}
	})
	t.Run("file path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.pem")
		if err := os.WriteFile(path, []byte(pemStr), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := loadPrivateKey(path)
		if err != nil || string(got) != pemStr {
			t.Fatalf("file form failed: %v", err)
		}
	})
	t.Run("base64 blob", func(t *testing.T) {
		got, err := loadPrivateKey(base64.StdEncoding.EncodeToString([]byte(pemStr)))
		if err != nil || string(got) != pemStr {
			t.Fatalf("base64 form failed: %v", err)
		}
	})
	t.Run("empty required", func(t *testing.T) {
		if _, err := loadPrivateKey(""); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		if _, err := loadPrivateKey("!definitely not a key!"); err == nil {
			t.Fatal("expected error for unrecognized form")
		}
	})
}

func TestParseDurationOrZero(t *testing.T) {
	if d, err := parseDurationOrZero(""); err != nil || d != 0 {
		t.Errorf("empty = (%v, %v), want (0, nil)", d, err)
	}
	if d, err := parseDurationOrZero("90m"); err != nil || d != 90*time.Minute {
		t.Errorf("90m = (%v, %v)", d, err)
	}
	if _, err := parseDurationOrZero("soon"); err == nil {
		t.Error("invalid duration should error")
	}
}

// decodeAuthYAML mirrors the loader's strict decode so these tests exercise
// the real YAML → schema → factory path (the EnvMap passthrough bug was
// invisible to tests that injected provider structs directly).
func decodeAuthYAML(t *testing.T, doc string) *schemas.Auth {
	t.Helper()
	var a schemas.Auth
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(&a); err != nil {
		t.Fatalf("decode auth yaml: %v", err)
	}
	return &a
}

// isolateAWS points the AWS SDK at the given endpoint and keeps it away
// from host config, IMDS, and real endpoints.
func isolateAWS(t *testing.T, service, url string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "example-secret")
	t.Setenv("AWS_ENDPOINT_URL_"+service, url)
}

// TestGitHubSecretEnvMapFromYAML: the env_map declared in auth.yaml must
// flow schema → factory → provider and produce the mapped env var. This is
// the regression test for the dropped-EnvMap bug (schema field missing +
// factory never passing it = every secret provider exported nothing).
func TestGitHubSecretEnvMapFromYAML(t *testing.T) {
	t.Setenv("REEVE_TEST_UPSTREAM", "hush-token")
	cfg := decodeAuthYAML(t, `
version: 1
config_type: auth
providers:
  custom-token:
    type: github_secret
    env_var: REEVE_TEST_UPSTREAM
    env_map:
      MY_TOKEN: ""
`)
	r, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p, ok := r.Get("custom-token")
	if !ok {
		t.Fatal("provider not registered")
	}
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if cred.Env["MY_TOKEN"] != "hush-token" {
		t.Fatalf("env_map not honored through the YAML path: %+v", cred.Env)
	}
}

// TestAWSSecretsManagerEnvMapFromYAML covers the same passthrough for a
// JSON-bundle secret with per-field mapping, plus the fail-closed error
// when a mapped field is absent.
func TestAWSSecretsManagerEnvMapFromYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:000000000000:secret:app","Name":"app","SecretString":"{\"api_key\":\"k-1\",\"db_password\":\"hunter2-bundle\"}"}`))
	}))
	defer srv.Close()
	isolateAWS(t, "SECRETS_MANAGER", srv.URL)

	cfg := decodeAuthYAML(t, `
version: 1
config_type: auth
providers:
  cloudflare-token:
    type: aws_secrets_manager
    secret_id: app
    region: us-east-1
    env_map:
      CLOUDFLARE_API_TOKEN: api_key
`)
	r, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p, _ := r.Get("cloudflare-token")
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if cred.Env["CLOUDFLARE_API_TOKEN"] != "k-1" {
		t.Fatalf("env_map field extraction failed: %+v", cred.Env)
	}
	if len(cred.Env) != 1 {
		t.Fatalf("only mapped vars may be exported, got %+v", cred.Env)
	}

	// Missing field: hard error, whole bundle never exported.
	bad := decodeAuthYAML(t, `
version: 1
config_type: auth
providers:
  cloudflare-token:
    type: aws_secrets_manager
    secret_id: app
    region: us-east-1
    env_map:
      CLOUDFLARE_API_TOKEN: typo_field
`)
	rb, err := Build(context.Background(), bad)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pb, _ := rb.Get("cloudflare-token")
	cred, err = pb.Acquire(context.Background())
	if err == nil {
		t.Fatalf("missing field must fail closed, got %+v", cred)
	}
	if !strings.Contains(err.Error(), `"typo_field"`) {
		t.Errorf("error should name the missing field: %v", err)
	}
	if strings.Contains(err.Error(), "hunter2-bundle") {
		t.Errorf("error leaks the secret bundle: %v", err)
	}
}

func TestValidateLint(t *testing.T) {
	t.Run("nil config passes", func(t *testing.T) {
		if err := ValidateLint(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("env_passthrough without flag fails closed", func(t *testing.T) {
		cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
			"leak": {Type: "env_passthrough"},
		}}
		err := ValidateLint(cfg, nil)
		if err == nil || !strings.Contains(err.Error(), "i_understand_this_is_dangerous") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("env_passthrough with flag passes with warning", func(t *testing.T) {
		cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
			"leak": {Type: "env_passthrough", IUnderstandThisIsDangerous: true},
		}}
		if err := ValidateLint(cfg, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("secret provider without env_map is a lint error", func(t *testing.T) {
		for _, typ := range []string{"aws_secrets_manager", "aws_ssm_parameter", "gcp_secret_manager", "azure_key_vault", "github_secret"} {
			cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
				"dead": {Type: typ},
			}}
			err := ValidateLint(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), "env_map is required") {
				t.Errorf("%s: err = %v, want env_map-required error", typ, err)
			}
		}
	})
	t.Run("secret provider with env_map passes lint", func(t *testing.T) {
		cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
			"ok": {Type: "aws_secrets_manager", SecretID: "s", Region: "us-east-1",
				EnvMap: map[string]string{"TOKEN": ""}},
		}}
		if err := ValidateLint(cfg, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("binding referencing unknown provider fails", func(t *testing.T) {
		cfg := &schemas.Auth{
			Providers: map[string]schemas.ProviderYAML{
				"aws": {Type: "aws_oidc", RoleARN: "arn:aws:iam::000000000000:role/x"},
			},
			Bindings: []schemas.BindingYAML{
				{Match: schemas.BindingMatch{Stack: "proj/*"}, Providers: []string{"missing"}},
			},
		}
		if err := ValidateLint(cfg, []string{"proj/dev"}); err == nil {
			t.Fatal("binding to undeclared provider should fail validation")
		}
	})
}

func TestBuildMalformedDurationFailsClosed(t *testing.T) {
	// A typo'd duration/ttl used to be silently swallowed and replaced by
	// the provider default; it must be a build error.
	_, err := Build(context.Background(), &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"aws": {Type: "aws_oidc", RoleARN: "arn:aws:iam::000000000000:role/x", Duration: "30minutes"},
	}})
	if err == nil || !strings.Contains(err.Error(), "30minutes") {
		t.Fatalf("malformed duration must fail Build, got %v", err)
	}
	_, err = Build(context.Background(), &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"sm": {Type: "aws_secrets_manager", SecretID: "s", TTL: "5 min"},
	}})
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("malformed ttl must fail Build, got %v", err)
	}
}

func TestValidateLintMalformedDurationFails(t *testing.T) {
	cfg := &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"aws": {Type: "aws_oidc", RoleARN: "arn:aws:iam::000000000000:role/x", Duration: "4hours"},
	}}
	if err := ValidateLint(cfg, nil); err == nil || !strings.Contains(err.Error(), "4hours") {
		t.Fatalf("malformed duration must fail lint, got %v", err)
	}
	cfg = &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
		"sm": {Type: "aws_oidc", RoleARN: "arn:aws:iam::000000000000:role/x", TTL: "1hr"},
	}}
	if err := ValidateLint(cfg, nil); err == nil || !strings.Contains(err.Error(), "1hr") {
		t.Fatalf("malformed ttl must fail lint, got %v", err)
	}
}
