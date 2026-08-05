package locks

import (
	"context"
	"testing"
	"time"

	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
)

func TestForceUnlockClearsHolderAndPromotes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	s := newStore(t, now)

	if _, _, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 1}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 2}, time.Hour); err != nil {
		t.Fatal(err)
	}

	l, err := s.ForceUnlock(ctx, "api", "prod", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.PR != 2 {
		t.Fatalf("expected pr 2 promoted: %+v", l.Holder)
	}
	if len(l.Queue) != 0 {
		t.Fatalf("queue should be empty: %+v", l.Queue)
	}
}

func TestForceUnlockEmptyQueueReleases(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	s := newStore(t, now)

	if _, _, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 1}, time.Hour); err != nil {
		t.Fatal(err)
	}
	l, err := s.ForceUnlock(ctx, "api", "prod", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Fatalf("expected free lock: %+v", l.Holder)
	}
}
