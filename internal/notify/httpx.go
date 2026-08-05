package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPDoer is the minimal http.Client surface channels depend on.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

var (
	sharedOnce   sync.Once
	sharedClient *http.Client
)

// SharedHTTPClient returns the process-wide client for channel deliveries. It
// carries a sane overall timeout so a hung endpoint cannot wedge a delivery
// even if the caller forgot a ctx deadline.
func SharedHTTPClient() *http.Client {
	sharedOnce.Do(func() {
		sharedClient = &http.Client{Timeout: 20 * time.Second}
	})
	return sharedClient
}

// Retry policy for PostJSON.
const (
	maxAttempts    = 3
	initialBackoff = 500 * time.Millisecond
)

// PostJSON POSTs body to url with bounded retry: network errors, HTTP 5xx,
// and 429 retry with exponential backoff (respecting ctx); other non-2xx
// statuses fail immediately. name labels errors.
func PostJSON(ctx context.Context, client HTTPDoer, name, url string, headers map[string]string, body []byte) error {
	if client == nil {
		client = SharedHTTPClient()
	}
	backoff := initialBackoff
	var lastErr error
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		switch {
		case err != nil:
			lastErr = fmt.Errorf("%s: %w", name, err)
		default:
			// Drain (bounded) so the connection can be reused.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			status := resp.StatusCode
			resp.Body.Close()
			switch {
			case status/100 == 2:
				return nil
			case status >= 500 || status == http.StatusTooManyRequests:
				lastErr = fmt.Errorf("%s: HTTP %d", name, status)
			default:
				return fmt.Errorf("%s: HTTP %d", name, status)
			}
		}

		if attempt >= maxAttempts {
			return fmt.Errorf("%w (after %d attempts)", lastErr, attempt)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w (last error: %v)", name, ctx.Err(), lastErr)
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}
