package slack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(srv *httptest.Server) *Client {
	c := New("xoxb-test")
	c.baseURL = srv.URL + "/"
	c.baseBackoff = time.Millisecond // keep tests fast
	return c
}

func TestCallRetries429ThenOK(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// No usable Retry-After → exponential fallback backoff.
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"ok":true,"ts":"111.222","channel":"C1"}`)
	}))
	defer srv.Close()

	c := testClient(srv)
	res, err := c.call(context.Background(), "chat.postMessage", Message{Channel: "C1", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TS != "111.222" || calls.Load() != 2 {
		t.Fatalf("ts=%q calls=%d, want retry then success", res.TS, calls.Load())
	}
}

func TestCallHonorsRetryAfterSeconds(t *testing.T) {
	var calls atomic.Int32
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"ok":true,"ts":"1.2","channel":"C1"}`)
	}))
	defer srv.Close()

	if _, err := testClient(srv).call(context.Background(), "chat.postMessage", Message{Channel: "C1"}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Second {
		t.Fatalf("returned after %s, want >= 1s (Retry-After honored)", d)
	}
}

func TestCallSurfaces429AfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := testClient(srv).call(context.Background(), "chat.postMessage", Message{Channel: "C1"})
	if err == nil {
		t.Fatal("want the final 429 surfaced as an error")
	}
	if calls.Load() != rlMaxAttempts {
		t.Fatalf("calls = %d, want %d", calls.Load(), rlMaxAttempts)
	}
}

func TestCallDoesNotRetryAPIErrors(t *testing.T) {
	// ok:false is Slack rejecting the payload - retrying cannot help.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"ok":false,"error":"channel_not_found"}`)
	}))
	defer srv.Close()

	_, err := testClient(srv).call(context.Background(), "chat.postMessage", Message{Channel: "C1"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d, want a single failing call", err, calls.Load())
	}
}
