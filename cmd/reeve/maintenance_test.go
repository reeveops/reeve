package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corelocks "github.com/reeveops/reeve/internal/core/locks"
)

func TestMaintenanceRunReapsLocksAndPrunesArtifacts(t *testing.T) {
	root, lockStore := lockRepo(t)
	past := time.Now().Add(-60 * 24 * time.Hour)

	lock := corelocks.Lock{
		Project: "proj", Stack: "dev",
		Holder: &corelocks.Holder{
			PR: 7, Actor: "alice", RunID: "r1",
			AcquiredAt: past.UTC().Format(time.RFC3339),
			ExpiresAt:  past.UTC().Format(time.RFC3339),
		},
		UpdatedAt: past.UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	lockDir := filepath.Join(root, ".reeve-state", "locks", "proj")
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(lockDir, "dev.json"), string(raw))

	artifact := filepath.Join(root, ".reeve-state", "runs", "pr-1", "old", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, artifact, "{}")
	if err := os.Chtimes(artifact, past, past); err != nil {
		t.Fatal(err)
	}

	out, err := runReeve(t, "maintenance", "run")
	if err != nil {
		t.Fatalf("maintenance run: %v\n%s", err, out)
	}
	for _, want := range []string{"expired locks reaped: 1", "expired run artifacts pruned: 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	got, _, err := lockStore.Get(context.Background(), "proj", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != nil {
		t.Fatalf("expired holder survived maintenance: %+v", got.Holder)
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatalf("expired artifact still exists: %v", err)
	}
}

func TestMaintenanceRunReportsDisabledRetention(t *testing.T) {
	root, _ := lockRepo(t)
	shared := filepath.Join(root, ".reeve", "shared.yaml")
	data, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(data) + "\nretention:\n  max_age: 0\n"
	mustWrite(t, shared, updated)

	out, err := runReeve(t, "maintenance", "run")
	if err != nil {
		t.Fatalf("maintenance run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run artifact retention: disabled") {
		t.Fatalf("missing disabled-retention status:\n%s", out)
	}
}
