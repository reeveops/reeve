// Package slack is the shared Slack client. HTTP-only (no SDK dep), Block
// Kit JSON passthrough, message upsert via chat.update or chat.postMessage.
// Consumed by the slack notification channel (internal/notify/channels/slack).
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client posts and edits Slack messages via the Web API.
type Client struct {
	httpClient  *http.Client
	token       string
	baseURL     string        // "https://slack.com/api/"; overridable in tests
	baseBackoff time.Duration // initial 429 backoff; shrunk in tests
}

// New returns a Client. token is a bot user OAuth token ("xoxb-...").
func New(token string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		token:       token,
		baseURL:     "https://slack.com/api/",
		baseBackoff: rlBaseBackoff,
	}
}

// Attachment wraps a colored sidebar attachment containing Block Kit blocks.
type Attachment struct {
	Color  string  `json:"color,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`
}

// Message is the minimal post/update payload.
type Message struct {
	Channel     string       `json:"channel"`
	Text        string       `json:"text,omitempty"`        // fallback for notifications
	Blocks      []Block      `json:"blocks,omitempty"`      // top-level Block Kit (no color bar)
	Attachments []Attachment `json:"attachments,omitempty"` // colored sidebar attachments
	TS          string       `json:"ts,omitempty"`          // set on update
	ThreadTS    string       `json:"thread_ts,omitempty"`
}

// Block is opaque - users construct Block Kit JSON and stash it here.
type Block = json.RawMessage

// PostResult returns the TS of the posted message so the caller can
// persist it for later upserts.
type PostResult struct {
	TS      string `json:"ts"`
	Channel string `json:"channel"`
}

// Post publishes a new message. Returns the message TS.
func (c *Client) Post(ctx context.Context, m Message) (*PostResult, error) {
	return c.call(ctx, "chat.postMessage", m)
}

// Update edits an existing message identified by channel + ts.
func (c *Client) Update(ctx context.Context, m Message) (*PostResult, error) {
	if m.TS == "" {
		return nil, errors.New("slack update: ts required")
	}
	return c.call(ctx, "chat.update", m)
}

// Upsert posts or updates: if ts is empty, posts; otherwise updates.
func (c *Client) Upsert(ctx context.Context, channel, ts, text string, blocks []Block) (*PostResult, error) {
	m := Message{Channel: channel, Text: text, Blocks: blocks, TS: ts}
	if ts == "" {
		return c.Post(ctx, m)
	}
	return c.Update(ctx, m)
}

// PostThread publishes a reply in the thread of parentTS.
func (c *Client) PostThread(ctx context.Context, channel, parentTS, text string, blocks []Block) (*PostResult, error) {
	return c.Post(ctx, Message{Channel: channel, ThreadTS: parentTS, Text: text, Blocks: blocks})
}

// Rate-limit retry policy: Slack answers 429 + Retry-After (seconds) when a
// method is rate-limited; the request was rejected, not processed, so a
// bounded retry cannot double-post. Waits the server asks for beyond
// rlMaxWait surface the 429 instead of stalling the run.
const (
	rlMaxAttempts = 3
	rlBaseBackoff = 1 * time.Second
	rlMaxWait     = 30 * time.Second
)

func (c *Client) call(ctx context.Context, method string, m Message) (*PostResult, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	backoff := c.baseBackoff
	if backoff <= 0 {
		backoff = rlBaseBackoff
	}
	for attempt := 1; ; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			c.baseURL+method, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < rlMaxAttempts {
			wait := backoff
			if s := resp.Header.Get("Retry-After"); s != "" {
				if secs, aerr := strconv.Atoi(s); aerr == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			if wait <= rlMaxWait {
				backoff *= 2
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("slack %s: %w", method, ctx.Err())
				case <-time.After(wait):
				}
				continue
			}
			// Retry-After beyond our budget - surface the 429.
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("slack %s %d: %s", method, resp.StatusCode, string(data))
		}
		var out struct {
			OK      bool   `json:"ok"`
			TS      string `json:"ts"`
			Channel string `json:"channel"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		if !out.OK {
			return nil, fmt.Errorf("slack %s failed: %s", method, out.Error)
		}
		return &PostResult{TS: out.TS, Channel: out.Channel}, nil
	}
}
