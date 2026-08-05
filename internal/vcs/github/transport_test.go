package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSleepClient returns an *http.Client using retryTransport with a
// recording no-op sleeper.
func fakeSleepClient(slept *[]time.Duration) *http.Client {
	rt := newRetryTransport(nil)
	rt.sleep = func(_ context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return nil
	}
	return &http.Client{Transport: rt}
}

func TestRetryTransport429ThenOK(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `ok`)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept = %v, want [2s] (Retry-After honored)", slept)
	}
}

func TestRetryTransportPrimaryLimit403GETRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `ok`)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d, want 200 after 2 calls", resp.StatusCode, calls.Load())
	}
	// No Retry-After / usable reset: exponential fallback backoff.
	if len(slept) != 1 || slept[0] != rlBaseBackoff {
		t.Fatalf("slept = %v, want [%v]", slept, rlBaseBackoff)
	}
}

func TestRetryTransportPrimaryLimit403POSTSurfaces(t *testing.T) {
	// A primary-limit 403 on a non-idempotent request must NOT be retried
	// blindly - only 429 / secondary limit (Retry-After) are documented as
	// rejected-before-processing.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d, want the 403 surfaced after 1 call", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportSecondaryLimitPOSTRetriesWithBody(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusForbidden) // secondary limit shape
			return
		}
		_, _ = io.WriteString(w, `ok`)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Post(srv.URL, "application/json", strings.NewReader(`{"body":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d, want 200 after retry", resp.StatusCode, calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies[0] != bodies[1] || bodies[1] != `{"body":"hi"}` {
		t.Fatalf("retry did not replay the body: %q vs %q", bodies[0], bodies[1])
	}
}

func TestRetryTransportGivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the final 429 surfaced", resp.StatusCode)
	}
	if calls.Load() != rlMaxAttempts {
		t.Fatalf("calls = %d, want %d", calls.Load(), rlMaxAttempts)
	}
}

func TestRetryTransportLongRetryAfterSurfaces(t *testing.T) {
	// A reset window beyond rlMaxWait must surface immediately, not stall.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls.Load() != 1 || len(slept) != 0 {
		t.Fatalf("status=%d calls=%d slept=%v, want immediate surface", resp.StatusCode, calls.Load(), slept)
	}
}

func TestRetryTransportPlainForbiddenNotRetried(t *testing.T) {
	// 403 without rate-limit signals is permission denied - no retry.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	var slept []time.Duration
	resp, err := fakeSleepClient(&slept).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (plain 403 must not retry)", calls.Load())
	}
}

func TestNewHonorsGitHubAPIURL(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "https://ghe.example.com/api/v3")
	c, err := New(context.Background(), "tok", "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.gh.BaseURL.String(); got != "https://ghe.example.com/api/v3/" {
		t.Fatalf("BaseURL = %q, want the GHES endpoint", got)
	}
}

func TestNewDefaultsToPublicAPI(t *testing.T) {
	for _, v := range []string{"", "https://api.github.com"} {
		t.Setenv("GITHUB_API_URL", v)
		c, err := New(context.Background(), "tok", "o", "r")
		if err != nil {
			t.Fatal(err)
		}
		if got := c.gh.BaseURL.String(); got != "https://api.github.com/" {
			t.Fatalf("GITHUB_API_URL=%q: BaseURL = %q, want public API", v, got)
		}
	}
}
