package github

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Rate-limit retry policy. Small and bounded: GitHub asks clients to honor
// Retry-After / X-RateLimit-Reset and back off; anything the policy cannot
// absorb within rlMaxWait surfaces the original error response instead of
// stalling a CI run behind a long reset window.
const (
	rlMaxAttempts = 3
	rlBaseBackoff = 1 * time.Second
	rlMaxWait     = 30 * time.Second
)

// retryTransport is a RoundTripper that retries GitHub rate-limit responses
// (429, and the 403 shapes the rate limiter uses) with bounded backoff,
// honoring Retry-After / X-RateLimit-Reset when present. Every reeve GitHub
// call (PR reads, comment upserts, issue channels) goes through one shared
// instance via New.
//
// Non-idempotent requests are never retried blindly: a POST/PATCH retries
// only on 429 or a secondary rate limit (403 + Retry-After), where GitHub
// documents the request was rejected before processing. Any other non-2xx
// surfaces to the caller unchanged.
type retryTransport struct {
	base http.RoundTripper
	// sleep waits between attempts; injectable so tests don't sleep.
	sleep func(ctx context.Context, d time.Duration) error
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{base: base, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	backoff := rlBaseBackoff
	for attempt := 1; ; attempt++ {
		r := req
		if attempt > 1 {
			// RoundTrippers must not mutate the caller's request; clone it
			// and rewind the body for the retry.
			r = req.Clone(req.Context())
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				r.Body = body
			}
		}
		resp, err := base.RoundTrip(r)
		if err != nil {
			return resp, err
		}
		if attempt >= rlMaxAttempts || !retryableRateLimit(req, resp) {
			return resp, nil
		}
		wait, ok := retryDelay(resp, backoff)
		if !ok {
			// The reset is further out than we are willing to stall; let the
			// caller see the rate-limit response and fail loudly.
			return resp, nil
		}
		backoff *= 2
		// Drain a little so the connection can be reused, then wait.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		if serr := t.sleep(req.Context(), wait); serr != nil {
			return nil, serr
		}
	}
}

// retryableRateLimit reports whether resp is a rate-limit rejection that is
// safe to retry for this request.
func retryableRateLimit(req *http.Request, resp *http.Response) bool {
	limited := false
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		limited = true
	case http.StatusForbidden:
		// 403 is also plain "permission denied"; only the rate limiter's
		// shapes count (secondary limit sends Retry-After, primary sends
		// X-RateLimit-Remaining: 0).
		limited = resp.Header.Get("Retry-After") != "" ||
			resp.Header.Get("X-Ratelimit-Remaining") == "0"
	}
	if !limited {
		return false
	}
	// A body we cannot replay cannot be retried.
	if req.Body != nil && req.GetBody == nil {
		return false
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		// Non-idempotent: retry only where GitHub documents the request was
		// rejected before processing - 429, or a secondary rate limit
		// (403 + Retry-After). A primary-limit 403 on a write surfaces.
		return resp.StatusCode == http.StatusTooManyRequests ||
			resp.Header.Get("Retry-After") != ""
	}
}

// retryDelay picks how long to wait before the next attempt: Retry-After
// (seconds) when present, else time until X-RateLimit-Reset when the quota
// is exhausted, else the exponential fallback. ok is false when the server
// asks for longer than rlMaxWait - the caller then surfaces the response.
func retryDelay(resp *http.Response, fallback time.Duration) (time.Duration, bool) {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil {
			d := time.Duration(secs) * time.Second
			if d <= 0 {
				d = fallback
			}
			return d, d <= rlMaxWait
		}
	}
	if s := resp.Header.Get("X-Ratelimit-Reset"); s != "" && resp.Header.Get("X-Ratelimit-Remaining") == "0" {
		if epoch, err := strconv.ParseInt(s, 10, 64); err == nil {
			d := time.Until(time.Unix(epoch, 0)) + time.Second
			if d <= 0 {
				d = fallback
			}
			return d, d <= rlMaxWait
		}
	}
	return fallback, true
}
