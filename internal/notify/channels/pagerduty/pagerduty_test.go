package pagerduty

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
)

func testServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		got = append(got, m)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func newTestChannel(t *testing.T, cfg schemas.ChannelYAML, url string) *Channel {
	t.Helper()
	s, err := New(context.Background(), cfg, notify.Deps{HTTP: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	channel := s.(*Channel)
	channel.endpoint = url
	return channel
}

func TestDriftEventWireFormat(t *testing.T) {
	srv, got := testServer(t)
	s := newTestChannel(t, schemas.ChannelYAML{
		Type: "pagerduty", IntegrationKey: "key123",
		SeverityMap: map[string]string{"prod": "critical"},
		On:          []string{"drift_detected", "drift_resolved"},
	}, srv.URL)

	err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftDetected,
		Drift: &notify.DriftPayload{Project: "net", Stack: "prod", Env: "prod", RunID: "r1", Add: 1},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	m := (*got)[0]
	if m["routing_key"] != "key123" || m["event_action"] != "trigger" || m["dedup_key"] != "reeve-drift-net/prod" {
		t.Fatalf("event: %v", m)
	}
	payload := m["payload"].(map[string]any)
	if payload["severity"] != "critical" {
		t.Fatalf("severity_map not applied: %v", payload)
	}

	// Resolution maps to event_action resolve with the same dedup key.
	err = s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftResolved,
		Drift: &notify.DriftPayload{Project: "net", Stack: "prod", Env: "dev"},
	})
	if err != nil {
		t.Fatalf("Deliver resolved: %v", err)
	}
	m = (*got)[1]
	if m["event_action"] != "resolve" || m["dedup_key"] != "reeve-drift-net/prod" {
		t.Fatalf("resolve event: %v", m)
	}
	if m["payload"].(map[string]any)["severity"] != "warning" {
		t.Fatalf("default severity: %v", m)
	}
}

func TestPREventsTriggerAndResolve(t *testing.T) {
	srv, got := testServer(t)
	s := newTestChannel(t, schemas.ChannelYAML{
		Type: "pagerduty", IntegrationKey: "key123",
		On: []string{"failed", "applied", "applying"},
	}, srv.URL)

	pr := &notify.PRPayload{PR: 4, RepoFull: "org/repo"}
	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventFailed, PR: pr}); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventApplied, PR: pr}); err != nil {
		t.Fatal(err)
	}
	// Intermediate lifecycle events are no-ops.
	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventApplying, PR: pr}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("want 2 PD events, got %d", len(*got))
	}
	if (*got)[0]["event_action"] != "trigger" || (*got)[1]["event_action"] != "resolve" {
		t.Fatalf("actions: %v", *got)
	}
	if (*got)[0]["dedup_key"] != "reeve-pr-org/repo-4" || (*got)[1]["dedup_key"] != (*got)[0]["dedup_key"] {
		t.Fatalf("dedup keys: %v", *got)
	}
}

// TestCheckEventsUseDistinctIncidentStream is the dedup-stomping
// regression: check_failed/check_recovered must ride their own dedup key,
// never the drift incident's, and a recovery must resolve the failure
// incident.
func TestCheckEventsUseDistinctIncidentStream(t *testing.T) {
	srv, got := testServer(t)
	s := newTestChannel(t, schemas.ChannelYAML{
		Type: "pagerduty", IntegrationKey: "key123",
		On: []string{"drift_detected", "drift_resolved", "check_failed"},
	}, srv.URL)

	drift := &notify.DriftPayload{Project: "net", Stack: "prod", Env: "prod", RunID: "r1", Error: "auth exploded"}
	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventCheckFailed, Drift: drift}); err != nil {
		t.Fatalf("Deliver check_failed: %v", err)
	}
	m := (*got)[0]
	if m["event_action"] != "trigger" || m["dedup_key"] != "reeve-drift-check::net/prod" {
		t.Fatalf("check_failed must trigger on its own dedup key: %v", m)
	}
	if m["dedup_key"] == "reeve-drift-net/prod" {
		t.Fatal("check_failed stomped the drift incident key")
	}

	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventCheckRecovered, Drift: drift}); err != nil {
		t.Fatalf("Deliver check_recovered: %v", err)
	}
	m = (*got)[1]
	if m["event_action"] != "resolve" || m["dedup_key"] != "reeve-drift-check::net/prod" {
		t.Fatalf("check_recovered must resolve the check incident: %v", m)
	}
}

// TestCheckFailedSubscriptionImpliesRecovered: a config that predates the
// recovery event must still get its incidents resolved.
func TestCheckFailedSubscriptionImpliesRecovered(t *testing.T) {
	srv, _ := testServer(t)
	s := newTestChannel(t, schemas.ChannelYAML{
		Type: "pagerduty", IntegrationKey: "k",
		On: []string{"check_failed"},
	}, srv.URL)
	found := false
	for _, ev := range s.Subscribes() {
		if ev == notify.EventCheckRecovered {
			found = true
		}
	}
	if !found {
		t.Fatal("check_failed subscription must imply check_recovered")
	}
}
