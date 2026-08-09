package githuboidc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve stands in for the Actions token service and records what it was
// asked for.
func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-token")
}

func TestFetchReturnsToken(t *testing.T) {
	var gotAuth, gotAccept string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"value":"the-id-token"}`))
	})

	tok, err := Fetch(t.Context(), "", "aws_oidc")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "the-id-token" {
		t.Fatalf("token = %q", tok)
	}
	if gotAuth != "Bearer runner-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "api-version=2.0") {
		t.Fatalf("Accept = %q", gotAccept)
	}
}

func TestFetchEscapesAudience(t *testing.T) {
	// audience comes from the provider's `audience:` config override, so it
	// is operator input. Concatenating it into the query string let a value
	// containing & or = forge extra parameters; it must arrive as exactly
	// one value.
	var got string
	var rawQuery string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		vals := r.URL.Query()["audience"]
		if len(vals) == 1 {
			got = vals[0]
		}
		rawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"value":"t"}`))
	})

	const hostile = "api://reeve&sub=attacker"
	if _, err := Fetch(t.Context(), hostile, "gcp_wif"); err != nil {
		t.Fatal(err)
	}
	if got != hostile {
		t.Fatalf("audience arrived as %q, want %q (raw query %q)", got, hostile, rawQuery)
	}
	if strings.Contains(rawQuery, "sub=attacker") {
		t.Fatalf("audience forged a second query parameter: %q", rawQuery)
	}
}

func TestFetchPreservesExistingQuery(t *testing.T) {
	// The runner's URL already carries its own parameters; adding an
	// audience must not drop them.
	var q map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(`{"value":"t"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-token")

	if _, err := Fetch(t.Context(), "my-aud", "azure_federated"); err != nil {
		t.Fatal(err)
	}
	if len(q["api-version"]) != 1 || q["api-version"][0] != "2.0" {
		t.Fatalf("existing query parameter lost: %v", q)
	}
	if len(q["audience"]) != 1 || q["audience"][0] != "my-aud" {
		t.Fatalf("audience not set: %v", q)
	}
}

func TestFetchOutsideActionsNamesTheProvider(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	_, err := Fetch(t.Context(), "", "azure_federated")
	if err == nil {
		t.Fatal("expected an error outside GitHub Actions")
	}
	// The operator needs to know which binding to give id-token: write.
	if !strings.Contains(err.Error(), "azure_federated") {
		t.Fatalf("error does not name the provider: %v", err)
	}
}

func TestFetchNon200IsAnError(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("no id-token permission"))
	})

	_, err := Fetch(t.Context(), "", "aws_oidc")
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should carry the status: %v", err)
	}
}

func TestFetchEmptyTokenIsAnError(t *testing.T) {
	// A 200 with no token must not be handed to the credential exchange as
	// an empty bearer.
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})

	if _, err := Fetch(t.Context(), "", "gcp_wif"); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}
