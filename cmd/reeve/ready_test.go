package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
)

func TestReadySkipsUnavailablePreviewHistory(t *testing.T) {
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sha = "abc1234def5678"
	key := "runs/pr-18/run-1-" + sha + "/manifest.json"
	if _, err := store.Put(t.Context(), key, strings.NewReader("{not-json")); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if readyPlanSucceeded(t.Context(), store, 18, sha, &schemas.Shared{}, &stderr) {
		t.Fatal("unavailable preview history must not send a ready notification")
	}
	if !strings.Contains(stderr.String(), "preview history unavailable") || !strings.Contains(stderr.String(), "skipping ready notification") {
		t.Fatalf("diagnostic = %q", stderr.String())
	}
}
