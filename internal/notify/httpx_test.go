package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func retryClient() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

func TestPostJSONRetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := PostJSON(context.Background(), retryClient(), "t", srv.URL, nil, []byte(`{}`)); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("want 3 attempts, got %d", calls.Load())
	}
}

func TestPostJSONGivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := PostJSON(context.Background(), retryClient(), "t", srv.URL, nil, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
	if calls.Load() != maxAttempts {
		t.Fatalf("want %d attempts, got %d", maxAttempts, calls.Load())
	}
}

func TestPostJSONDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	err := PostJSON(context.Background(), retryClient(), "t", srv.URL, nil, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("want HTTP 400 error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("want 1 attempt, got %d", calls.Load())
	}
}

func TestPostJSONRetriesNetworkError(t *testing.T) {
	// A server that is immediately closed produces connection-refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	start := time.Now()
	err := PostJSON(context.Background(), retryClient(), "t", url, nil, []byte(`{}`))
	if err == nil {
		t.Fatal("want network error")
	}
	// 2 backoffs (500ms + 1s) prove retries happened.
	if d := time.Since(start); d < 1200*time.Millisecond {
		t.Fatalf("returned too fast for %d attempts: %s", maxAttempts, d)
	}
}

func TestPostJSONHonorsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := PostJSON(ctx, retryClient(), "t", srv.URL, nil, []byte(`{}`))
	if err == nil {
		t.Fatal("want error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not honor ctx cancellation during backoff")
	}
}

func TestPostJSONSetsHeaders(t *testing.T) {
	var gotCT, gotX string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotX = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := PostJSON(context.Background(), retryClient(), "t", srv.URL, map[string]string{"X-Custom": "yes"}, []byte(`{}`)); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if gotCT != "application/json" || gotX != "yes" {
		t.Fatalf("headers: ct=%q x=%q", gotCT, gotX)
	}
}
