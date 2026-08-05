package drift

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
)

func TestStoredReport(t *testing.T) {
	ctx := context.Background()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Two runs; the lexically-later run ID must win.
	older := &RunOutput{
		RunID: "drift-20260419T120000Z",
		Items: []Item{{Project: "api", Stack: "prod", Outcome: OutcomeNoDrift}},
	}
	newer := &RunOutput{
		RunID: "drift-20260420T120000Z",
		Items: []Item{
			{Project: "api", Stack: "prod", Outcome: OutcomeDriftDetected, Fingerprint: "fp1"},
			{Project: "web", Stack: "prod", Outcome: OutcomeNoDrift},
		},
	}
	if err := WriteArtifacts(ctx, store, older, "# old report"); err != nil {
		t.Fatal(err)
	}
	if err := WriteArtifacts(ctx, store, newer, "# new report"); err != nil {
		t.Fatal(err)
	}

	t.Run("markdown returns latest report verbatim", func(t *testing.T) {
		got, err := StoredReport(ctx, store, "markdown")
		if err != nil {
			t.Fatal(err)
		}
		if got != "# new report" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("json emits manifest and items", func(t *testing.T) {
		got, err := StoredReport(ctx, store, "json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			RunID    string            `json:"run_id"`
			Manifest map[string]any    `json:"manifest"`
			Items    []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(got), &doc); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, got)
		}
		if doc.RunID != newer.RunID {
			t.Fatalf("run_id = %q, want %q", doc.RunID, newer.RunID)
		}
		if doc.Manifest["run_id"] != newer.RunID {
			t.Fatalf("manifest run_id = %v", doc.Manifest["run_id"])
		}
		if len(doc.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(doc.Items))
		}
		if !strings.Contains(got, "fp1") {
			t.Fatalf("items should carry stored results: %s", got)
		}
	})

	t.Run("unknown format errors", func(t *testing.T) {
		if _, err := StoredReport(ctx, store, "yaml"); err == nil {
			t.Fatal("expected error for unknown format")
		}
	})
}

func TestStoredReportNoRuns(t *testing.T) {
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = StoredReport(context.Background(), store, "markdown")
	if !errors.Is(err, ErrNoRuns) {
		t.Fatalf("expected ErrNoRuns, got %v", err)
	}
}

func TestParseEventName(t *testing.T) {
	for _, name := range KnownEventNames() {
		if _, ok := ParseEventName(name); !ok {
			t.Fatalf("known event %q must parse", name)
		}
	}
	for _, bad := range []string{"", "drift", "resolved", "drift_detcted"} {
		if _, ok := ParseEventName(bad); ok {
			t.Fatalf("unknown event %q must not parse", bad)
		}
	}
}
