// Package annotations posts per-event annotations to Grafana, Datadog,
// Dash0, or generic webhooks. Complements OTEL; not a replacement.
package annotations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/core/envref"
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
	if g.Client == nil {
		g.Client = http.DefaultClient
	}
	body := map[string]any{
		"time":    e.When.UnixMilli(),
		"timeEnd": e.When.UnixMilli(),
		"tags": append([]string{"reeve", string(e.Type), e.Project, e.Env},
			tagSlice(e.Tags)...),
		"text": summary(e),
	}
	buf, _ := json.Marshal(body)
	url := strings.TrimRight(envref.Expand(g.BaseURL), "/") + "/api/annotations"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+envref.Expand(g.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("grafana %d", resp.StatusCode)
	}
	return nil
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
	if d.Client == nil {
		d.Client = http.DefaultClient
	}
	body := map[string]any{
		"title":            summary(e),
		"text":             e.Message,
		"alert_type":       alertTypeFor(e.Type, e.Outcome),
		"date_happened":    e.When.Unix(),
		"tags":             append([]string{"reeve", "type:" + string(e.Type), "project:" + e.Project, "env:" + e.Env}, tagSlice(e.Tags)...),
		"source_type_name": "reeve",
	}
	buf, _ := json.Marshal(body)
	base := envref.Expand(d.BaseURL)
	if base == "" {
		base = "https://api.datadoghq.com"
	}
	url := strings.TrimRight(base, "/") + "/api/v1/events"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("DD-API-KEY", envref.Expand(d.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("datadog %d", resp.StatusCode)
	}
	return nil
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
	if w.Client == nil {
		w.Client = http.DefaultClient
	}
	buf, _ := json.Marshal(e)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, envref.Expand(w.Endpoint), bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.Headers {
		req.Header.Set(k, envref.Expand(v))
	}
	resp, err := w.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %d", resp.StatusCode)
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
