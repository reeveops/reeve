package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// genKey generates a throwaway RSA key at test runtime (never a
// committed fixture) and returns it PEM-encoded in the requested format.
func genKey(t *testing.T, format string) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	switch format {
	case "pkcs1":
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	case "pkcs8":
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	default:
		t.Fatalf("unknown format %q", format)
	}
	return key, pem.EncodeToMemory(block)
}

func setAPIBase(t *testing.T, url string) {
	t.Helper()
	orig := apiBase
	apiBase = url
	t.Cleanup(func() { apiBase = orig })
}

func TestSignAppJWTClaims(t *testing.T) {
	for _, format := range []string{"pkcs1", "pkcs8"} {
		t.Run(format, func(t *testing.T) {
			key, pemBytes := genKey(t, format)
			before := time.Now()
			signed, err := signAppJWT(12345, pemBytes)
			if err != nil {
				t.Fatalf("signAppJWT(%s): %v", format, err)
			}

			// Verify with the public half; RS256 must be enforced.
			claims := jwt.MapClaims{}
			tok, err := jwt.ParseWithClaims(signed, claims, func(tk *jwt.Token) (any, error) {
				return &key.PublicKey, nil
			}, jwt.WithValidMethods([]string{"RS256"}))
			if err != nil || !tok.Valid {
				t.Fatalf("parse signed JWT: %v", err)
			}

			if iss, _ := claims["iss"].(float64); int64(iss) != 12345 {
				t.Errorf("iss = %v, want 12345", claims["iss"])
			}
			iat := int64(claims["iat"].(float64))
			exp := int64(claims["exp"].(float64))
			// iat is backdated 60s for clock skew; exp is 9m ahead (GitHub
			// caps app JWTs at 10m).
			wantIat := before.Add(-60 * time.Second).Unix()
			if iat < wantIat-2 || iat > wantIat+2 {
				t.Errorf("iat = %d, want ~%d (now-60s)", iat, wantIat)
			}
			if window := exp - iat; window != int64((9*time.Minute+60*time.Second)/time.Second) {
				t.Errorf("exp-iat = %ds, want 600s", window)
			}
		})
	}
}

func TestSignAppJWTBadPEM(t *testing.T) {
	cases := []struct {
		name string
		pem  []byte
	}{
		{"not pem", []byte("this is not a key")},
		{"empty", nil},
		{"wrong block type", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := signAppJWT(1, tc.pem); err == nil {
				t.Fatal("expected error for bad PEM")
			}
		})
	}
}

func TestAcquireExchangesJWTForInstallationToken(t *testing.T) {
	key, pemBytes := genKey(t, "pkcs1")

	var gotPath, gotAuth, gotAccept, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"token":"ghs_installation_token_do_not_leak","expires_at":"2030-01-02T03:04:05Z"}`))
	}))
	defer srv.Close()
	setAPIBase(t, srv.URL)

	p := New("gh", 12345, 678, pemBytes)
	if p.Name() != "gh" || p.Type() != "github_app" {
		t.Errorf("Name/Type = %q/%q", p.Name(), p.Type())
	}
	cred, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if gotPath != "/app/installations/678/access_tokens" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAccept != "application/vnd.github+json" || gotVersion != "2022-11-28" {
		t.Errorf("headers = %q / %q", gotAccept, gotVersion)
	}
	// The bearer token must be a JWT signed by our app key.
	bearer := strings.TrimPrefix(gotAuth, "Bearer ")
	if bearer == gotAuth {
		t.Fatalf("Authorization = %q, want Bearer", gotAuth)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(bearer, claims, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"})); err != nil {
		t.Fatalf("bearer is not a JWT signed with the app key: %v", err)
	}
	if iss, _ := claims["iss"].(float64); int64(iss) != 12345 {
		t.Errorf("iss = %v, want app id", claims["iss"])
	}

	if cred.Env["GITHUB_TOKEN"] != "ghs_installation_token_do_not_leak" {
		t.Errorf("GITHUB_TOKEN = %q", cred.Env["GITHUB_TOKEN"])
	}
	if cred.Kind != "github-app" || cred.Source != "gh" {
		t.Errorf("kind/source = %q/%q", cred.Kind, cred.Source)
	}
	if got := cred.ExpiresAt.UTC().Format(time.RFC3339); got != "2030-01-02T03:04:05Z" {
		t.Errorf("ExpiresAt = %s", got)
	}
}

func TestAcquireNon201(t *testing.T) {
	_, pemBytes := genKey(t, "pkcs8")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	setAPIBase(t, srv.URL)

	p := New("gh", 12345, 678, pemBytes)
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "github app token 404") {
		t.Fatalf("error = %v", err)
	}
	// Redaction contract: private key material never appears in errors.
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("error leaks key material: %v", err)
	}
}

func TestAcquireMalformedJSON(t *testing.T) {
	_, pemBytes := genKey(t, "pkcs1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"token":`))
	}))
	defer srv.Close()
	setAPIBase(t, srv.URL)

	p := New("gh", 1, 2, pemBytes)
	if _, err := p.Acquire(context.Background()); err == nil {
		t.Fatal("expected malformed-JSON error")
	}
}

func TestAcquireBadKeyFailsBeforeNetwork(t *testing.T) {
	// No server: a bad key must fail before any HTTP request is made.
	setAPIBase(t, "http://example.invalid")
	p := New("gh", 1, 2, []byte("garbage"))
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sign jwt") {
		t.Fatalf("error = %v, want sign jwt failure", err)
	}
}
