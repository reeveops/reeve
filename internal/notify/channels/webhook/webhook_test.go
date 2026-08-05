package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
)

func driftPayload() notify.Payload {
	return notify.Payload{
		Event: notify.EventDriftDetected,
		Drift: &notify.DriftPayload{
			Project: "net", Stack: "prod", Env: "prod", Outcome: "drift_detected",
			Add: 1, Change: 2, Delete: 3, Replace: 4,
			Fingerprint: "fp", Error: "", RunID: "drift-9",
		},
	}
}

func build(t *testing.T, cfg schemas.ChannelYAML) notify.Channel {
	t.Helper()
	s, err := New(context.Background(), cfg, notify.Deps{HTTP: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDriftWireFormatUnchanged(t *testing.T) {
	var body []byte
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		header = r.Header.Get("X-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := build(t, schemas.ChannelYAML{
		Type: "webhook", URL: srv.URL,
		Headers: map[string]string{"X-Token": "secret"},
		On:      []string{"drift_detected"},
	})
	if err := s.Deliver(context.Background(), driftPayload()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if header != "secret" {
		t.Fatalf("custom header missing: %q", header)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	// Exact key parity with the previous drift-only channel.
	for _, k := range []string{"event", "project", "stack", "env", "outcome", "counts", "fingerprint", "error", "run_id"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %q in %v", k, got)
		}
	}
	if got["event"] != "drift_detected" || got["project"] != "net" || got["run_id"] != "drift-9" {
		t.Fatalf("payload: %v", got)
	}
	counts := got["counts"].(map[string]any)
	if counts["add"].(float64) != 1 || counts["replace"].(float64) != 4 {
		t.Fatalf("counts: %v", counts)
	}
}

func TestGroupedDriftWireFormat(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := build(t, schemas.ChannelYAML{
		Type: "webhook", URL: srv.URL, On: []string{"drift_detected"},
		Grouping: notify.GroupingByEnvironment,
	})
	err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftDetected, GroupKey: "prod",
		Drift: &notify.DriftPayload{Project: "net", Stack: "a", Env: "prod", RunID: "drift-9"},
		Group: []notify.DriftPayload{
			{Project: "net", Stack: "a", Env: "prod", Change: 1, RunID: "drift-9"},
			{Project: "net", Stack: "c", Env: "prod", Add: 2, RunID: "drift-9"},
		},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "drift_detected" || got["group"] != "prod" || got["run_id"] != "drift-9" {
		t.Fatalf("grouped payload: %v", got)
	}
	stacks, ok := got["stacks"].([]any)
	if !ok || len(stacks) != 2 {
		t.Fatalf("grouped payload must carry 2 stacks, got %v", got["stacks"])
	}
}

func TestPRPayloadShape(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := build(t, schemas.ChannelYAML{Type: "webhook", URL: srv.URL, On: []string{"applied"}})
	err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventApplied,
		PR: &notify.PRPayload{
			PR: 12, RepoFull: "org/repo", CommitSHA: "abc", RunURL: "https://ci",
			Stacks: []notify.StackResult{{Project: "app", Stack: "prod", Status: "planned", Add: 2}},
		},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "applied" || got["pr"].(float64) != 12 || got["repo"] != "org/repo" {
		t.Fatalf("payload: %v", got)
	}
	if len(got["stacks"].([]any)) != 1 {
		t.Fatalf("stacks: %v", got["stacks"])
	}
}

func TestDeliverRetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := build(t, schemas.ChannelYAML{Type: "webhook", URL: srv.URL, On: []string{"drift_detected"}})
	if err := s.Deliver(context.Background(), driftPayload()); err != nil {
		t.Fatalf("Deliver should retry through the 503: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("attempts: %d", calls.Load())
	}
}
