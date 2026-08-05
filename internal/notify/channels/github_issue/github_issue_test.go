package github_issue

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
)

type fakeIssues struct {
	byMarker  map[string]int
	created   []string // titles
	updated   []int
	closed    []int
	lastBody  string
	labels    []string
	assignees []string
}

func (f *fakeIssues) FindIssueByMarker(_ context.Context, marker string) (int, bool, error) {
	n, ok := f.byMarker[marker]
	return n, ok, nil
}

func (f *fakeIssues) CreateIssue(_ context.Context, title, body string, labels, assignees []string) (int, error) {
	f.created = append(f.created, title)
	f.lastBody = body
	f.labels = labels
	f.assignees = assignees
	return 101, nil
}

func (f *fakeIssues) UpdateIssue(_ context.Context, number int, _, body string) error {
	f.updated = append(f.updated, number)
	f.lastBody = body
	return nil
}

func (f *fakeIssues) CloseIssue(_ context.Context, number int, body string) error {
	f.closed = append(f.closed, number)
	f.lastBody = body
	return nil
}

func payload(ev notify.Event) notify.Payload {
	return notify.Payload{Event: ev, Drift: &notify.DriftPayload{
		Project: "net", Stack: "prod", Env: "prod",
		Add: 1, Change: 0, Delete: 0, Replace: 0,
		PlanSummary: "+ aws:ec2 sg", RunID: "drift-1",
	}}
}

func newChannel(t *testing.T, issues notify.IssueClient) notify.Channel {
	t.Helper()
	s, err := New(context.Background(), schemas.ChannelYAML{
		Type: "github_issue", On: []string{"drift_detected", "drift_resolved"},
		Labels: []string{"drift"}, Assignees: []string{"sre"},
	}, notify.Deps{Issues: issues})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSkippedWithoutIssueClient(t *testing.T) {
	s, err := New(context.Background(), schemas.ChannelYAML{Type: "github_issue"}, notify.Deps{})
	if err != nil || s != nil {
		t.Fatalf("want skip, got %v %v", s, err)
	}
}

func TestCreatesIssueWithMarker(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{}}
	s := newChannel(t, f)
	if err := s.Deliver(context.Background(), payload(notify.EventDriftDetected)); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || f.created[0] != "drift: net/prod" {
		t.Fatalf("created: %v", f.created)
	}
	if !strings.Contains(f.lastBody, "<!-- reeve:drift:net/prod -->") {
		t.Fatalf("marker missing: %q", f.lastBody)
	}
	if !strings.Contains(f.lastBody, "## Drift detected on `net/prod`") {
		t.Fatalf("body changed: %q", f.lastBody)
	}
	if len(f.labels) != 1 || f.labels[0] != "drift" || len(f.assignees) != 1 {
		t.Fatalf("labels/assignees: %v %v", f.labels, f.assignees)
	}
}

func TestUpdatesExistingIssue(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{"<!-- reeve:drift:net/prod -->": 42}}
	s := newChannel(t, f)
	if err := s.Deliver(context.Background(), payload(notify.EventDriftOngoing)); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.updated) != 1 || f.updated[0] != 42 {
		t.Fatalf("update path: created=%v updated=%v", f.created, f.updated)
	}
}

func TestResolvedClosesIssue(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{"<!-- reeve:drift:net/prod -->": 42}}
	s := newChannel(t, f)
	if err := s.Deliver(context.Background(), payload(notify.EventDriftResolved)); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 1 || f.closed[0] != 42 {
		t.Fatalf("close path: %v", f.closed)
	}
	// Resolved with no existing issue: no-op.
	f2 := &fakeIssues{byMarker: map[string]int{}}
	s2 := newChannel(t, f2)
	if err := s2.Deliver(context.Background(), payload(notify.EventDriftResolved)); err != nil {
		t.Fatal(err)
	}
	if len(f2.created)+len(f2.closed)+len(f2.updated) != 0 {
		t.Fatal("resolved without issue must be a no-op")
	}
}

// github_issue is intentionally not a Grouper: it never receives grouped
// payloads, and even if grouping is configured each stack gets its own
// per-stack issue (no shared per-env issue that would lose stacks).
func TestGithubIssueIsNotGroupable(t *testing.T) {
	if _, ok := interface{}(&Channel{}).(notify.Grouper); ok {
		t.Fatal("github_issue must NOT implement notify.Grouper")
	}

	f := &fakeIssues{byMarker: map[string]int{}}
	s, err := New(context.Background(), schemas.ChannelYAML{
		Type: "github_issue", On: []string{"drift_detected"},
		Grouping: notify.GroupingByEnvironment, // present but ignored
	}, notify.Deps{Issues: f})
	if err != nil {
		t.Fatal(err)
	}
	// Two stacks in the same env, delivered per-stack (as the fixed dispatch
	// path does for a non-Grouper): two distinct issues, each with its own
	// per-stack marker - no shared per-env issue, no data loss.
	for _, d := range []notify.DriftPayload{
		{Project: "net", Stack: "a", Env: "prod", Change: 1, RunID: "drift-1"},
		{Project: "net", Stack: "c", Env: "prod", Add: 2, RunID: "drift-1"},
	} {
		dd := d
		if err := s.Deliver(context.Background(), notify.Payload{
			Event: notify.EventDriftDetected, Drift: &dd,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.created) != 2 {
		t.Fatalf("want one issue per stack (2), got %v", f.created)
	}
	if f.created[0] != "drift: net/a" || f.created[1] != "drift: net/c" {
		t.Fatalf("per-stack titles expected: %v", f.created)
	}
	if strings.Contains(f.lastBody, "group:") {
		t.Fatalf("no group marker expected: %q", f.lastBody)
	}
}

func TestPRPayloadIsNoOp(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{}}
	s := newChannel(t, f)
	if err := s.Deliver(context.Background(), notify.Payload{Event: notify.EventApplied, PR: &notify.PRPayload{}}); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 {
		t.Fatal("PR payloads must not create issues")
	}
}

// TestCheckFailedUsesSeparateIssue is the marker-stomping regression: a
// check_failed event must never overwrite (or be closed by) the per-stack
// drift issue.
func TestCheckFailedUsesSeparateIssue(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{
		"<!-- reeve:drift:net/prod -->": 7, // real drift issue already open
	}}
	s, err := New(context.Background(), schemas.ChannelYAML{
		Type: "github_issue", On: []string{"drift_detected", "check_failed"},
	}, notify.Deps{Issues: f})
	if err != nil {
		t.Fatal(err)
	}

	p := payload(notify.EventCheckFailed)
	p.Drift.Error = "auth exploded"
	if err := s.Deliver(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(f.updated) != 0 {
		t.Fatalf("check_failed must not touch the drift issue, updated %v", f.updated)
	}
	if len(f.created) != 1 || f.created[0] != "drift check failed: net/prod" {
		t.Fatalf("want a dedicated check-failure issue, created %v", f.created)
	}
	if !strings.Contains(f.lastBody, "<!-- reeve:drift-check:net/prod -->") {
		t.Fatalf("check issue must carry its own marker: %q", f.lastBody)
	}
	if !strings.Contains(f.lastBody, "auth exploded") {
		t.Fatalf("check issue must carry the error: %q", f.lastBody)
	}
}

func TestCheckRecoveredClosesCheckIssue(t *testing.T) {
	f := &fakeIssues{byMarker: map[string]int{
		"<!-- reeve:drift-check:net/prod -->": 8,
		"<!-- reeve:drift:net/prod -->":       7,
	}}
	s, err := New(context.Background(), schemas.ChannelYAML{
		Type: "github_issue", On: []string{"check_failed"}, // recovery implied
	}, notify.Deps{Issues: f})
	if err != nil {
		t.Fatal(err)
	}
	subs := s.Subscribes()
	implied := false
	for _, ev := range subs {
		if ev == notify.EventCheckRecovered {
			implied = true
		}
	}
	if !implied {
		t.Fatal("check_failed subscription must imply check_recovered")
	}

	if err := s.Deliver(context.Background(), payload(notify.EventCheckRecovered)); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 1 || f.closed[0] != 8 {
		t.Fatalf("check_recovered must close the check issue (not the drift issue): closed %v", f.closed)
	}
}
