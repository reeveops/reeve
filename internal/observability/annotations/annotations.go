// Package annotations posts per-event annotations to Grafana, Datadog,
// Dash0, or generic webhooks. Complements OTEL; not a replacement.
package annotations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/core/envref"
)

// EventType is the subscription key channels filter on.
type EventType string

const (
	EventApplyStarted   EventType = "apply_started"
	EventApplyCompleted EventType = "apply_completed"
	EventApplyFailed    EventType = "apply_failed"
	EventDriftDetected  EventType = "drift_detected"
	EventDriftResolved  EventType = "drift_resolved"
)

// Event is the common shape posted to emitters.
type Event struct {
	Type      EventType
	When      time.Time
	Project   string
	Stack     string
	Env       string
	PR        int
	CommitSHA string
	Outcome   string
	Message   string
	Tags      map[string]string
}

// Emitter delivers annotations to a single backend.
type Emitter interface {
	Name() string
	Subscribes() []EventType
	Post(ctx context.Context, e Event) error
}

// Dispatch posts one event to every interested emitter.
func Dispatch(ctx context.Context, emitters []Emitter, e Event) []error {
	var errs []error
	for _, em := range emitters {
		if !subscribed(em, e.Type) {
			continue
		}
		if err := em.Post(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", em.Name(), err))
		}
	}
	return errs
}

func subscribed(e Emitter, t EventType) bool {
	for _, s := range e.Subscribes() {
		if s == t {
			return true
		}
	}
	return false
}

// --- Grafana ---

// Grafana posts to /api/annotations.
type Grafana struct {
	BaseURL string
	APIKey  string
	Events  []EventType
	Client  *http.Client
}

func (g *Grafana) Name() string            { return "grafana" }
func (g *Grafana) Subscribes() []EventType { return g.Events }
func (g *Grafana) Post(ctx context.Context, e Event) error {
	body := map[string]any{
		"time":    e.When.UnixMilli(),
		"timeEnd": e.When.UnixMilli(),
		"tags": append([]string{"reeve", string(e.Type), e.Project, e.Env},
			tagSlice(e.Tags)...),
		"text": summary(e),
	}
	url := strings.TrimRight(envref.Expand(g.BaseURL), "/") + "/api/annotations"
	return postJSON(ctx, g.Client, "grafana", url, body, map[string]string{
		"Authorization": "Bearer " + envref.Expand(g.APIKey),
	})
}

// --- Datadog ---

// Datadog posts to /api/v1/events.
type Datadog struct {
	BaseURL string // e.g. https://api.datadoghq.com
	APIKey  string
	Events  []EventType
	Client  *http.Client
}

func (d *Datadog) Name() string            { return "datadog" }
func (d *Datadog) Subscribes() []EventType { return d.Events }
func (d *Datadog) Post(ctx context.Context, e Event) error {
	body := map[string]any{
		"title":            summary(e),
		"text":             e.Message,
		"alert_type":       alertTypeFor(e.Type, e.Outcome),
		"date_happened":    e.When.Unix(),
		"tags":             append([]string{"reeve", "type:" + string(e.Type), "project:" + e.Project, "env:" + e.Env}, tagSlice(e.Tags)...),
		"source_type_name": "reeve",
	}
	base := envref.Expand(d.BaseURL)
	if base == "" {
		base = "https://api.datadoghq.com"
	}
	url := strings.TrimRight(base, "/") + "/api/v1/events"
	return postJSON(ctx, d.Client, "datadog", url, body, map[string]string{
		"DD-API-KEY": envref.Expand(d.APIKey),
	})
}

// --- Dash0 / generic webhook ---

// Webhook posts a JSON Event to an arbitrary endpoint. Used by Dash0
// and as the default "unknown type" fallback.
type Webhook struct {
	Name_    string
	Endpoint string
	Headers  map[string]string
	Events   []EventType
	Client   *http.Client
}

func (w *Webhook) Name() string            { return w.Name_ }
func (w *Webhook) Subscribes() []EventType { return w.Events }
func (w *Webhook) Post(ctx context.Context, e Event) error {
	headers := make(map[string]string, len(w.Headers))
	for k, v := range w.Headers {
		headers[k] = envref.Expand(v)
	}
	return postJSON(ctx, w.Client, "webhook", envref.Expand(w.Endpoint), e, headers)
}

// --- transport ---

// sharedClient bounds every annotation POST. http.DefaultClient has no
// timeout, so a hung or black-holed collector would stall the run that
// emitted the event. 20s matches internal/notify's shared client.
var sharedClient = &http.Client{Timeout: 20 * time.Second}

// httpClient resolves the client for an emitter without writing back to it.
// The emitters used to assign http.DefaultClient to their own Client field
// on first use; Deps.Emitters is a single slice shared across channels that
// dispatch concurrently, so that lazy assignment was a data race.
func httpClient(c *http.Client) *http.Client {
	if c == nil {
		return sharedClient
	}
	return c
}

// postJSON marshals body and POSTs it, classifying the response. label names
// the backend in status errors. The three emitters differ only in URL, body
// and headers, so the request plumbing lives here once - previously each
// carried its own copy, and each copy discarded the marshal and
// request-construction errors.
func postJSON(ctx context.Context, c *http.Client, label, url string, body any, headers map[string]string) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient(c).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %d", label, resp.StatusCode)
	}
	return nil
}

// --- helpers ---

func summary(e Event) string {
	ref := e.Project
	if e.Stack != "" {
		ref += "/" + e.Stack
	}
	switch e.Type {
	case EventApplyStarted:
		return fmt.Sprintf("reeve apply started on %s", ref)
	case EventApplyCompleted:
		return fmt.Sprintf("reeve apply completed on %s", ref)
	case EventApplyFailed:
		return fmt.Sprintf("reeve apply FAILED on %s: %s", ref, e.Message)
	case EventDriftDetected:
		return fmt.Sprintf("drift detected on %s", ref)
	case EventDriftResolved:
		return fmt.Sprintf("drift resolved on %s", ref)
	}
	return fmt.Sprintf("reeve %s on %s", e.Type, ref)
}

func alertTypeFor(t EventType, outcome string) string {
	switch t {
	case EventApplyFailed:
		return "error"
	case EventDriftDetected:
		return "warning"
	case EventApplyCompleted, EventDriftResolved:
		return "success"
	}
	if outcome == "failed" {
		return "error"
	}
	return "info"
}

func tagSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+":"+v)
	}
	return out
}
