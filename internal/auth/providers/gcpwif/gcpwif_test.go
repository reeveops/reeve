package gcpwif

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Secret-shaped fixtures so redaction assertions catch leaks into error
// strings.
const (
	fakeOIDCToken = "oidc-token-value-do-not-leak"
	fakeSTSToken  = "sts-federated-token-do-not-leak"
	fakeSAToken   = "sa-access-token-do-not-leak"
	testWIP       = "projects/000000000000/locations/global/workloadIdentityPools/pool/providers/gh"
	testSA        = "deployer@example-project.iam.gserviceaccount.com"
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

func setSTSEndpoint(t *testing.T, url string) {
	t.Helper()
	orig := stsEndpoint
	stsEndpoint = url
	t.Cleanup(func() { stsEndpoint = orig })
}

func setIAMBase(t *testing.T, url string) {
	t.Helper()
	orig := iamCredentialsBase
	iamCredentialsBase = url
	t.Cleanup(func() { iamCredentialsBase = orig })
}

func TestNewDefaults(t *testing.T) {
	p := New("gcp", testWIP, testSA, "", 0)
	if p.duration != time.Hour {
		t.Errorf("duration = %v, want 1h default", p.duration)
	}
	if want := "//iam.googleapis.com/" + testWIP; p.audience != want {
		t.Errorf("audience = %q, want %q", p.audience, want)
	}
	if p.Name() != "gcp" || p.Type() != "gcp_wif" {
		t.Errorf("Name/Type = %q/%q", p.Name(), p.Type())
	}
	if custom := New("gcp", testWIP, testSA, "custom-aud", 0); custom.audience != "custom-aud" {
		t.Errorf("audience override ignored: %q", custom.audience)
	}
}

func TestAcquireRunsFullWIFDance(t *testing.T) {
	oidc, oidcReq := oidcServer(t)
	setOIDCEnv(t, oidc.URL)

	var stsForm map[string]string
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		stsForm = map[string]string{}
		for k := range r.PostForm {
			stsForm[k] = r.PostForm.Get(k)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("sts Content-Type = %q", ct)
		}
		_, _ = w.Write([]byte(`{"access_token":"` + fakeSTSToken + `"}`))
	}))
	defer sts.Close()
	setSTSEndpoint(t, sts.URL)

	var iamPath, iamAuth string
	var iamBody map[string]any
	iam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iamPath = r.URL.Path
		iamAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &iamBody)
		_, _ = w.Write([]byte(`{"accessToken":"` + fakeSAToken + `","expireTime":"2030-01-02T03:04:05Z"}`))
	}))
	defer iam.Close()
	setIAMBase(t, iam.URL)

	p := New("gcp", testWIP, testSA, "", 30*time.Minute)
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// 1. GitHub OIDC request carries the WIF audience.
	if got := oidcReq.URL.Query().Get("audience"); got != "//iam.googleapis.com/"+testWIP {
		t.Errorf("oidc audience = %q", got)
	}
	if got := oidcReq.Header.Get("Authorization"); got != "Bearer runner-bearer" {
		t.Errorf("oidc Authorization = %q", got)
	}

	// 2. STS token-exchange shape.
	wantForm := map[string]string{
		"audience":             "//iam.googleapis.com/" + testWIP,
		"grant_type":           "urn:ietf:params:oauth:grant-type:token-exchange",
		"requested_token_type": "urn:ietf:params:oauth:token-type:access_token",
		"scope":                "https://www.googleapis.com/auth/cloud-platform",
		"subject_token_type":   "urn:ietf:params:oauth:token-type:jwt",
		"subject_token":        fakeOIDCToken,
	}
	for k, v := range wantForm {
		if stsForm[k] != v {
			t.Errorf("sts form %s = %q, want %q", k, stsForm[k], v)
		}
	}

	// 3. Impersonation call shape.
	if want := "/v1/projects/-/serviceAccounts/" + testSA + ":generateAccessToken"; iamPath != want {
		t.Errorf("iam path = %q, want %q", iamPath, want)
	}
	if iamAuth != "Bearer "+fakeSTSToken {
		t.Errorf("iam Authorization = %q", iamAuth)
	}
	if got := iamBody["lifetime"]; got != "1800s" {
		t.Errorf("iam lifetime = %v, want 1800s", got)
	}

	// Credential shaping + ambient file.
	if cred.Kind != "gcp-sa" || cred.Source != "gcp" {
		t.Errorf("kind/source = %q/%q", cred.Kind, cred.Source)
	}
	if cred.Env["CLOUDSDK_AUTH_ACCESS_TOKEN"] != fakeSAToken || cred.Env["GOOGLE_OAUTH_ACCESS_TOKEN"] != fakeSAToken {
		t.Errorf("token env vars wrong: %+v", cred.Env)
	}
	if got := cred.ExpiresAt.UTC().Format(time.RFC3339); got != "2030-01-02T03:04:05Z" {
		t.Errorf("ExpiresAt = %s", got)
	}
	credPath := cred.Env["GOOGLE_APPLICATION_CREDENTIALS"]
	raw, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("ambient creds not written: %v", err)
	}
	var creds map[string]any
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatalf("ambient creds not JSON: %v", err)
	}
	if creds["type"] != "external_account_authorized_user" || creds["access_token"] != fakeSAToken {
		t.Errorf("ambient creds shape wrong: %v", creds)
	}

	// Cleanup removes the temp dir so tokens do not outlive the run.
	if err := cred.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Error("Cleanup left the ambient credentials file behind")
	}
}

func TestAcquireOutsideActionsFailsClosed(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	p := New("gcp", testWIP, testSA, "", 0)
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL") {
		t.Fatalf("expected clear outside-Actions error, got %v", err)
	}
}

func TestAcquireSTSFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"non-2xx", 400, `{"error":"invalid_grant"}`, "sts 400"},
		{"malformed json", 200, `{"access_token":`, "sts exchange"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oidc, _ := oidcServer(t)
			setOIDCEnv(t, oidc.URL)
			sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer sts.Close()
			setSTSEndpoint(t, sts.URL)

			p := New("gcp", testWIP, testSA, "", 0)
			_, err := p.Acquire(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
			// Redaction contract: the OIDC subject token never appears in
			// error strings.
			if strings.Contains(err.Error(), fakeOIDCToken) {
				t.Errorf("error leaks OIDC token: %v", err)
			}
		})
	}
}

func TestAcquireImpersonationFailure(t *testing.T) {
	oidc, _ := oidcServer(t)
	setOIDCEnv(t, oidc.URL)
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"` + fakeSTSToken + `"}`))
	}))
	defer sts.Close()
	setSTSEndpoint(t, sts.URL)
	iam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"status":"PERMISSION_DENIED"}}`))
	}))
	defer iam.Close()
	setIAMBase(t, iam.URL)

	p := New("gcp", testWIP, testSA, "", 0)
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "generate sa token") || !strings.Contains(err.Error(), "iamcredentials 403") {
		t.Fatalf("error = %v", err)
	}
	// Redaction contract: neither exchanged token appears in errors.
	for _, secret := range []string{fakeOIDCToken, fakeSTSToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
}

func TestWriteAmbientCreds(t *testing.T) {
	path, dir, err := writeAmbientCreds("myprov", "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if !strings.HasPrefix(path, dir) {
		t.Errorf("file %q not inside returned dir %q", path, dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 0600", perm)
	}
}
