package azurefed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Secret-shaped fixtures so redaction assertions catch leaks into error
// strings.
const (
	fakeOIDCToken   = "oidc-assertion-value-do-not-leak"
	fakeAccessToken = "azure-access-token-do-not-leak"
	testTenant      = "00000000-0000-0000-0000-000000000001"
	testClient      = "00000000-0000-0000-0000-000000000002"
	testSub         = "00000000-0000-0000-0000-000000000003"
)

func setOIDCEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", url)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-bearer")
}

func oidcServer(t *testing.T) (*httptest.Server, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		_, _ = w.Write([]byte(`{"value":"` + fakeOIDCToken + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func setLoginBase(t *testing.T, url string) {
	t.Helper()
	orig := loginBase
	loginBase = url
	t.Cleanup(func() { loginBase = orig })
}

func TestNewDefaults(t *testing.T) {
	p := New("az", testTenant, testClient, testSub, "", 0)
	if p.audience != "api://AzureADTokenExchange" {
		t.Errorf("audience = %q, want api://AzureADTokenExchange default", p.audience)
	}
	if p.duration != time.Hour {
		t.Errorf("duration = %v, want 1h default", p.duration)
	}
	if p.Name() != "az" || p.Type() != "azure_federated" {
		t.Errorf("Name/Type = %q/%q", p.Name(), p.Type())
	}
	if custom := New("az", testTenant, testClient, testSub, "api://custom", 0); custom.audience != "api://custom" {
		t.Errorf("audience override ignored: %q", custom.audience)
	}
}

func TestAcquireExchangesAssertionForToken(t *testing.T) {
	oidc, oidcReq := oidcServer(t)
	setOIDCEnv(t, oidc.URL)

	var tokenPath string
	var form map[string]string
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenPath = r.URL.Path
		_ = r.ParseForm()
		form = map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		_, _ = w.Write([]byte(`{"access_token":"` + fakeAccessToken + `","expires_in":3600}`))
	}))
	defer login.Close()
	setLoginBase(t, login.URL)

	before := time.Now()
	p := New("az", testTenant, testClient, testSub, "", 0)
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// OIDC request carries the AzureADTokenExchange audience by default.
	if got := oidcReq.URL.Query().Get("audience"); got != "api://AzureADTokenExchange" {
		t.Errorf("oidc audience = %q", got)
	}

	// Token exchange shape: tenant-scoped v2 endpoint, federated client
	// credentials grant with jwt-bearer assertion.
	if want := "/" + testTenant + "/oauth2/v2.0/token"; tokenPath != want {
		t.Errorf("token path = %q, want %q", tokenPath, want)
	}
	wantForm := map[string]string{
		"grant_type":            "client_credentials",
		"client_id":             testClient,
		"client_assertion_type": "urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
		"client_assertion":      fakeOIDCToken,
		"scope":                 "https://management.azure.com/.default",
	}
	for k, v := range wantForm {
		if form[k] != v {
			t.Errorf("form %s = %q, want %q", k, form[k], v)
		}
	}

	// Credential shaping: both AZURE_* and ARM_* variants.
	wantEnv := map[string]string{
		"AZURE_TENANT_ID":       testTenant,
		"AZURE_CLIENT_ID":       testClient,
		"AZURE_SUBSCRIPTION_ID": testSub,
		"ARM_TENANT_ID":         testTenant,
		"ARM_CLIENT_ID":         testClient,
		"ARM_SUBSCRIPTION_ID":   testSub,
		"AZURE_ACCESS_TOKEN":    fakeAccessToken,
		"ARM_ACCESS_TOKEN":      fakeAccessToken,
		"AZURE_USE_OIDC":        "true",
	}
	for k, v := range wantEnv {
		if cred.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, cred.Env[k], v)
		}
	}
	if cred.Kind != "azure-aad" || cred.Source != "az" {
		t.Errorf("kind/source = %q/%q", cred.Kind, cred.Source)
	}
	if cred.ExpiresAt.Before(before.Add(59*time.Minute)) || cred.ExpiresAt.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("ExpiresAt = %v, want ~1h from now", cred.ExpiresAt)
	}
}

func TestAcquireOutsideActionsFailsClosed(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	p := New("az", testTenant, testClient, testSub, "", 0)
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL") {
		t.Fatalf("expected clear outside-Actions error, got %v", err)
	}
}

func TestAcquireExchangeFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"non-2xx", 401, `{"error":"invalid_client"}`, "azure token exchange 401"},
		{"malformed json", 200, `{"access_token":`, "unexpected end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oidc, _ := oidcServer(t)
			setOIDCEnv(t, oidc.URL)
			login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer login.Close()
			setLoginBase(t, login.URL)

			p := New("az", testTenant, testClient, testSub, "", 0)
			_, err := p.Acquire(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
			// Redaction contract: the OIDC assertion never appears in
			// error strings.
			if strings.Contains(err.Error(), fakeOIDCToken) {
				t.Errorf("error leaks the assertion: %v", err)
			}
		})
	}
}
