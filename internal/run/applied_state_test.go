package run

import (
	"context"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
)

func TestAppliedStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())

	// Missing => nil, no error.
	got, err := readAppliedState(ctx, store, 7, "deadbeef")
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing pointer, got %+v", got)
	}

	st := AppliedState{CommitSHA: "deadbeef", RunID: "apply-3-deadbee", RunNumber: 3, AppliedAt: "2026-06-23T00:00:00Z", PR: 7}
	if err := writeAppliedState(ctx, store, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = readAppliedState(ctx, store, 7, "deadbeef")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got == nil || got.RunNumber != 3 || got.CommitSHA != "deadbeef" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Different SHA must not collide.
	other, _ := readAppliedState(ctx, store, 7, "cafebabe")
	if other != nil {
		t.Fatalf("unexpected pointer for different sha: %+v", other)
	}
}

func TestAppliedStateNilStore(t *testing.T) {
	ctx := context.Background()
	if err := writeAppliedState(ctx, nil, AppliedState{CommitSHA: "x"}); err != nil {
		t.Fatalf("nil store write should no-op: %v", err)
	}
	got, err := readAppliedState(ctx, nil, 1, "x")
	if err != nil || got != nil {
		t.Fatalf("nil store read should be (nil,nil): %+v %v", got, err)
	}
}
