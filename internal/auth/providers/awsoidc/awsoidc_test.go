package awsoidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The OIDC token is deliberately secret-shaped so redaction assertions
// catch any leak into error strings.
const fakeOIDCToken = "oidc-token-value-do-not-leak"

// oidcServer stands in for the GitHub Actions token service. It records
// the last request for assertions.
func oidcServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// isolateAWS keeps the SDK away from host config, IMDS, and real
// endpoints. Static creds are fake values for request signing only.
func isolateAWS(t *testing.T, stsURL string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "example-secret")
	t.Setenv("AWS_ENDPOINT_URL_STS", stsURL)
}

const stsSuccessXML = `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>ASIAEXAMPLEONLY</AccessKeyId>
      <SecretAccessKey>example-session-secret</SecretAccessKey>
      <SessionToken>example-session-token</SessionToken>
      <Expiration>2030-01-02T03:04:05Z</Expiration>
    </Credentials>
  </AssumeRoleWithWebIdentityResult>
  <ResponseMetadata><RequestId>00000000-0000-0000-0000-000000000000</RequestId></ResponseMetadata>
</AssumeRoleWithWebIdentityResponse>`

func TestNewDefaults(t *testing.T) {
	p := New("ci", "arn:aws:iam::000000000000:role/example", "", "", "", 0)
	if p.sessionName != "reeve" {
		t.Errorf("sessionName = %q, want default reeve", p.sessionName)
	}
	if p.duration != time.Hour {
		t.Errorf("duration = %v, want 1h default", p.duration)
	}
	if p.audience != "sts.amazonaws.com" {
		t.Errorf("audience = %q, want sts.amazonaws.com default", p.audience)
	}
	if p.Name() != "ci" || p.Type() != "aws_oidc" {
		t.Errorf("Name/Type = %q/%q", p.Name(), p.Type())
	}
}

func TestAcquireExchangesTokenForSTSCreds(t *testing.T) {
	oidc, oidcReq := oidcServer(t, 200, `{"value":"`+fakeOIDCToken+`"}`)

	var stsForm map[string]string
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse sts form: %v", err)
		}
		stsForm = map[string]string{}
		for k := range r.PostForm {
			stsForm[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(stsSuccessXML))
	}))
	defer sts.Close()

	isolateAWS(t, sts.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidc.URL+"/token?api-version=2")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-bearer")

	p := New("ci", "arn:aws:iam::000000000000:role/example", "deploy", "us-east-1", "", 30*time.Minute)
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// OIDC exchange shape: bearer auth, api-version accept, audience
	// appended with & because the URL already carries a query.
	if got := oidcReq.Header.Get("Authorization"); got != "Bearer runner-bearer" {
		t.Errorf("oidc Authorization = %q", got)
	}
	if got := oidcReq.Header.Get("Accept"); got != "application/json; api-version=2.0" {
		t.Errorf("oidc Accept = %q", got)
	}
	if got := oidcReq.URL.Query().Get("audience"); got != "sts.amazonaws.com" {
		t.Errorf("oidc audience = %q, want sts.amazonaws.com", got)
	}
	if oidcReq.URL.Query().Get("api-version") != "2" {
		t.Errorf("existing query params dropped: %s", oidcReq.URL.RawQuery)
	}

	// STS exchange shape.
	want := map[string]string{
		"Action":           "AssumeRoleWithWebIdentity",
		"RoleArn":          "arn:aws:iam::000000000000:role/example",
		"RoleSessionName":  "deploy",
		"WebIdentityToken": fakeOIDCToken,
		"DurationSeconds":  "1800",
	}
	for k, v := range want {
		if stsForm[k] != v {
			t.Errorf("sts form %s = %q, want %q", k, stsForm[k], v)
		}
	}

	// Credential shaping.
	if cred.Kind != "aws-sts" || cred.Source != "ci" {
		t.Errorf("kind/source = %q/%q", cred.Kind, cred.Source)
	}
	wantEnv := map[string]string{
		"AWS_ACCESS_KEY_ID":     "ASIAEXAMPLEONLY",
		"AWS_SECRET_ACCESS_KEY": "example-session-secret",
		"AWS_SESSION_TOKEN":     "example-session-token",
		"AWS_REGION":            "us-east-1",
		"AWS_DEFAULT_REGION":    "us-east-1",
	}
	for k, v := range wantEnv {
		if cred.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, cred.Env[k], v)
		}
	}
	if got := cred.ExpiresAt.UTC().Format(time.RFC3339); got != "2030-01-02T03:04:05Z" {
		t.Errorf("ExpiresAt = %s", got)
	}
}

func TestAcquireCustomAudience(t *testing.T) {
	oidc, oidcReq := oidcServer(t, 200, `{"value":"`+fakeOIDCToken+`"}`)
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(stsSuccessXML))
	}))
	defer sts.Close()

	isolateAWS(t, sts.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidc.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-bearer")

	p := New("ci", "arn:aws:iam::000000000000:role/example", "", "us-east-1", "custom.example.com", 0)
	if _, err := p.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := oidcReq.URL.Query().Get("audience"); got != "custom.example.com" {
		t.Errorf("audience = %q, want custom.example.com", got)
	}
}

func TestAcquireOutsideActionsFailsClosed(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	p := New("ci", "arn:aws:iam::000000000000:role/example", "", "", "", 0)
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL") {
		t.Fatalf("expected clear outside-Actions error, got %v", err)
	}
}

func TestAcquireOIDCFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"non-2xx", 403, `{"message":"denied"}`, "oidc token service 403"},
		{"malformed json", 200, `{"value":`, "fetch oidc token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oidc, _ := oidcServer(t, tc.status, tc.body)
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidc.URL)
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-bearer")

			p := New("ci", "arn:aws:iam::000000000000:role/example", "", "", "", 0)
			_, err := p.Acquire(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
			// Redaction contract: the runner bearer token must never
			// surface in errors.
			if strings.Contains(err.Error(), "runner-bearer") {
				t.Errorf("error leaks request token: %v", err)
			}
		})
	}
}

func TestAcquireSTSFailureDoesNotLeakToken(t *testing.T) {
	oidc, _ := oidcServer(t, 200, `{"value":"`+fakeOIDCToken+`"}`)
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>not authorized</Message></Error><RequestId>0</RequestId></ErrorResponse>`))
	}))
	defer sts.Close()

	isolateAWS(t, sts.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidc.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-bearer")

	p := New("ci", "arn:aws:iam::000000000000:role/example", "", "us-east-1", "", 0)
	_, err := p.Acquire(context.Background())
	if err == nil {
		t.Fatal("expected STS failure")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error should carry the STS code: %v", err)
	}
	if strings.Contains(err.Error(), fakeOIDCToken) {
		t.Errorf("error leaks the OIDC token: %v", err)
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"a?b", "?", true},
		{"ab", "?", false},
		{"", "", true},
		{"abc", "abc", true},
		{"abc", "abcd", false},
	}
	for _, tc := range cases {
		if got := contains(tc.s, tc.sub); got != tc.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}
