package annotations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

func testEvent() Event {
	return Event{
		Type:      EventApplyCompleted,
		When:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Project:   "proj",
		Stack:     "dev",
		Env:       "dev",
		PR:        7,
		CommitSHA: "0000000",
		Outcome:   "ok",
		Message:   "applied",
		Tags:      map[string]string{"team": "platform"},
	}
}

// capture records a single annotated POST.
type capture struct {
	path   string
	header http.Header
	body   []byte
}

func captureServer(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.header = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestGrafanaPost(t *testing.T) {
	srv, got := captureServer(t, 200)
	g := &Grafana{BaseURL: srv.URL + "/", APIKey: "grafana-key-do-not-leak", Events: []EventType{EventApplyCompleted}}
	if err := g.Post(context.Background(), testEvent()); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.path != "/api/annotations" {
		t.Errorf("path = %q", got.path)
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer grafana-key-do-not-leak" {
		t.Errorf("Authorization = %q", auth)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	wantMilli := float64(testEvent().When.UnixMilli())
	if body["time"] != wantMilli || body["timeEnd"] != wantMilli {
		t.Errorf("time fields = %v/%v, want %v", body["time"], body["timeEnd"], wantMilli)
	}
	if text, _ := body["text"].(string); text != "reeve apply completed on proj/dev" {
		t.Errorf("text = %q", text)
	}
	tags := toStrings(t, body["tags"])
	for _, want := range []string{"reeve", "apply_completed", "proj", "dev", "team:platform"} {
		if !containsStr(tags, want) {
			t.Errorf("tags missing %q: %v", want, tags)
		}
	}
}

func TestGrafanaNon2xx(t *testing.T) {
	srv, _ := captureServer(t, 500)
	g := &Grafana{BaseURL: srv.URL, APIKey: "grafana-key-do-not-leak"}
	err := g.Post(context.Background(), testEvent())
	if err == nil || !strings.Contains(err.Error(), "grafana 500") {
		t.Fatalf("error = %v", err)
	}
	// Redaction contract: the API key never appears in error strings.
	if strings.Contains(err.Error(), "grafana-key-do-not-leak") {
		t.Errorf("error leaks API key: %v", err)
	}
}

func TestDatadogPost(t *testing.T) {
	srv, got := captureServer(t, 202)
	d := &Datadog{BaseURL: srv.URL, APIKey: "dd-key-do-not-leak", Events: []EventType{EventApplyFailed}}
	e := testEvent()
	e.Type = EventApplyFailed
	e.Message = "boom"
	if err := d.Post(context.Background(), e); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.path != "/api/v1/events" {
		t.Errorf("path = %q", got.path)
	}
	if key := got.header.Get("DD-API-KEY"); key != "dd-key-do-not-leak" {
		t.Errorf("DD-API-KEY = %q", key)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["alert_type"] != "error" {
		t.Errorf("alert_type = %v, want error for apply_failed", body["alert_type"])
	}
	if body["date_happened"] != float64(e.When.Unix()) {
		t.Errorf("date_happened = %v", body["date_happened"])
	}
	if body["source_type_name"] != "reeve" {
		t.Errorf("source_type_name = %v", body["source_type_name"])
	}
	tags := toStrings(t, body["tags"])
	for _, want := range []string{"reeve", "type:apply_failed", "project:proj", "env:dev", "team:platform"} {
		if !containsStr(tags, want) {
			t.Errorf("tags missing %q: %v", want, tags)
		}
	}
}

func TestDatadogNon2xxDoesNotLeakKey(t *testing.T) {
	srv, _ := captureServer(t, 403)
	d := &Datadog{BaseURL: srv.URL, APIKey: "dd-key-do-not-leak"}
	err := d.Post(context.Background(), testEvent())
	if err == nil || !strings.Contains(err.Error(), "datadog 403") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "dd-key-do-not-leak") {
		t.Errorf("error leaks API key: %v", err)
	}
}

func TestWebhookPost(t *testing.T) {
	srv, got := captureServer(t, 204)
	t.Setenv("REEVE_TEST_WEBHOOK_TOKEN", "webhook-token")
	w := &Webhook{
		Name_:    "dash0",
		Endpoint: srv.URL + "/hook",
		Headers:  map[string]string{"Authorization": "${env:REEVE_TEST_WEBHOOK_TOKEN}"},
	}
	if err := w.Post(context.Background(), testEvent()); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.path != "/hook" {
		t.Errorf("path = %q", got.path)
	}
	if got.header.Get("Authorization") != "webhook-token" {
		t.Errorf("header env expansion failed: %q", got.header.Get("Authorization"))
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", got.header.Get("Content-Type"))
	}
	var e Event
	if err := json.Unmarshal(got.body, &e); err != nil {
		t.Fatal(err)
	}
	if e.Project != "proj" || e.Type != EventApplyCompleted || e.PR != 7 {
		t.Errorf("event round-trip wrong: %+v", e)
	}
}

func TestWebhookNon2xx(t *testing.T) {
	srv, _ := captureServer(t, 400)
	w := &Webhook{Name_: "webhook", Endpoint: srv.URL}
	if err := w.Post(context.Background(), testEvent()); err == nil || !strings.Contains(err.Error(), "webhook 400") {
		t.Fatalf("error = %v", err)
	}
}

// recordingEmitter is an in-memory emitter for Dispatch tests.
type recordingEmitter struct {
	name   string
	events []EventType
	got    []Event
	err    error
}

func (r *recordingEmitter) Name() string            { return r.name }
func (r *recordingEmitter) Subscribes() []EventType { return r.events }
func (r *recordingEmitter) Post(_ context.Context, e Event) error {
	r.got = append(r.got, e)
	return r.err
}

func TestDispatchFiltersBySubscription(t *testing.T) {
	subscribed := &recordingEmitter{name: "a", events: []EventType{EventDriftDetected}}
	other := &recordingEmitter{name: "b", events: []EventType{EventApplyFailed}}
	e := testEvent()
	e.Type = EventDriftDetected

	errs := Dispatch(context.Background(), []Emitter{subscribed, other}, e)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(subscribed.got) != 1 {
		t.Error("subscribed emitter did not receive the event")
	}
	if len(other.got) != 0 {
		t.Error("unsubscribed emitter received the event")
	}
}

func TestDispatchCollectsNamedErrors(t *testing.T) {
	fail := &recordingEmitter{name: "grafana", events: []EventType{EventApplyCompleted}, err: errors.New("boom")}
	ok := &recordingEmitter{name: "dash0", events: []EventType{EventApplyCompleted}}

	errs := Dispatch(context.Background(), []Emitter{fail, ok}, testEvent())
	if len(errs) != 1 {
		t.Fatalf("errs = %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "grafana:") {
		t.Errorf("error should be prefixed with emitter name: %v", errs[0])
	}
	if len(ok.got) != 1 {
		t.Error("one emitter failing must not stop the others")
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		typ  EventType
		want string
	}{
		{EventApplyStarted, "reeve apply started on proj/dev"},
		{EventApplyCompleted, "reeve apply completed on proj/dev"},
		{EventApplyFailed, "reeve apply FAILED on proj/dev: applied"},
		{EventDriftDetected, "drift detected on proj/dev"},
		{EventDriftResolved, "drift resolved on proj/dev"},
		{EventType("custom"), "reeve custom on proj/dev"},
	}
	for _, tc := range cases {
		e := testEvent()
		e.Type = tc.typ
		if got := summary(e); got != tc.want {
			t.Errorf("summary(%s) = %q, want %q", tc.typ, got, tc.want)
		}
	}
	// Project-only ref when stack is empty.
	e := testEvent()
	e.Stack = ""
	if got := summary(e); got != "reeve apply completed on proj" {
		t.Errorf("summary without stack = %q", got)
	}
}

func TestAlertTypeFor(t *testing.T) {
	cases := []struct {
		typ     EventType
		outcome string
		want    string
	}{
		{EventApplyFailed, "", "error"},
		{EventDriftDetected, "", "warning"},
		{EventApplyCompleted, "", "success"},
		{EventDriftResolved, "", "success"},
		{EventApplyStarted, "failed", "error"},
		{EventApplyStarted, "", "info"},
	}
	for _, tc := range cases {
		if got := alertTypeFor(tc.typ, tc.outcome); got != tc.want {
			t.Errorf("alertTypeFor(%s, %q) = %q, want %q", tc.typ, tc.outcome, got, tc.want)
		}
	}
}

func TestBuild(t *testing.T) {
	if got := Build(nil); got != nil {
		t.Errorf("nil config = %v, want nil", got)
	}

	cfg := &schemas.Observability{Annotations: []schemas.AnnotationConfig{
		{Type: "grafana", URL: "https://grafana.example.com", APIKey: "k", Events: []string{"apply_completed"}},
		{Type: "datadog", URL: "https://dd.example.com", APIKey: "k", Events: []string{"apply_failed"}},
		{Type: "dash0", Endpoint: "https://dash0.example.com/hook", Events: []string{"drift_detected"}},
		{Type: "dash0", URL: "https://dash0.example.com/url-fallback"},
		{Type: "webhook", URL: "https://hook.example.com", Headers: map[string]string{"X-K": "v"}},
		{Type: "unknown-kind"},
	}}
	out := Build(cfg)
	if len(out) != 5 {
		t.Fatalf("Build returned %d emitters, want 5 (unknown types skipped)", len(out))
	}

	g, ok := out[0].(*Grafana)
	if !ok || g.BaseURL != "https://grafana.example.com" || g.APIKey != "k" {
		t.Errorf("grafana emitter wrong: %#v", out[0])
	}
	if got := g.Subscribes(); len(got) != 1 || got[0] != EventApplyCompleted {
		t.Errorf("grafana events = %v", got)
	}

	if d, ok := out[1].(*Datadog); !ok || d.BaseURL != "https://dd.example.com" {
		t.Errorf("datadog emitter wrong: %#v", out[1])
	}

	d0, ok := out[2].(*Webhook)
	if !ok || d0.Name() != "dash0" || d0.Endpoint != "https://dash0.example.com/hook" {
		t.Errorf("dash0 emitter wrong: %#v", out[2])
	}
	// dash0 falls back to url when endpoint is unset.
	if fb, ok := out[3].(*Webhook); !ok || fb.Endpoint != "https://dash0.example.com/url-fallback" {
		t.Errorf("dash0 url fallback wrong: %#v", out[3])
	}

	if wh, ok := out[4].(*Webhook); !ok || wh.Name() != "webhook" || wh.Headers["X-K"] != "v" {
		t.Errorf("webhook emitter wrong: %#v", out[4])
	}
}

func toStrings(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("expected string slice, got %T", v)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, fmt.Sprint(item))
	}
	sort.Strings(out)
	return out
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
