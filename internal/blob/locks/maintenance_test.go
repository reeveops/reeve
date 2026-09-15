package locks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
)

type maintenanceCounter struct {
	blob.Store
	reads, writes int
	beforeWrite   func(string)
}

func (c *maintenanceCounter) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	if strings.HasPrefix(key, "locks/") && strings.HasSuffix(key, ".json") {
		c.reads++
	}
	return c.Store.Get(ctx, key)
}

func (c *maintenanceCounter) PutIfMatch(ctx context.Context, key string, r io.Reader, etag string) (*blob.Metadata, error) {
	if strings.HasPrefix(key, "locks/") && strings.HasSuffix(key, ".json") {
		c.writes++
		if c.beforeWrite != nil {
			fn := c.beforeWrite
			c.beforeWrite = nil
			fn(key)
		}
	}
	return c.Store.PutIfMatch(ctx, key, r, etag)
}

func TestMaintenanceOperationCounts(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		expired                 bool
		operation               string
		pr                      int
		runID                   string
		force                   bool
		writes, changes, active int
	}{
		{name: "list", operation: "list"},
		{name: "reap-active", operation: "reap"},
		{name: "reap-expired", operation: "reap", expired: true, writes: 1, changes: 1},
		{name: "unlock-unrelated", operation: "unlock", pr: 99, force: true},
		{name: "unlock-other-run", operation: "unlock", pr: 9, runID: "finished", force: true},
		{name: "unlock-active-refused", operation: "unlock", pr: 9, active: 1},
		{name: "unlock-matching-run", operation: "unlock", pr: 9, runID: "running", force: true, writes: 1, changes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			s := newStore(t, now)
			if _, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 9, RunID: "running"}, time.Hour); err != nil || !ok {
				t.Fatalf("seed: acquired=%v err=%v", ok, err)
			}
			_, before, err := s.Get(ctx, "api", "prod")
			if err != nil {
				t.Fatal(err)
			}
			if tc.expired {
				s.Now = func() time.Time { return now.Add(2 * time.Hour) }
			} else {
				s.Now = func() time.Time { return now.Add(time.Minute) }
			}
			counter := &maintenanceCounter{Store: s.store}
			s.store = counter
			var changes int
			switch tc.operation {
			case "list":
				locks, err := s.ListAll(ctx)
				if err != nil || len(locks) != 1 {
					t.Fatalf("list: len=%d err=%v", len(locks), err)
				}
			case "reap":
				changes, err = s.ReapAll(ctx, time.Hour)
			case "unlock":
				var active []string
				changes, active, err = s.UnlockPRAll(ctx, tc.pr, tc.runID, time.Hour, tc.force)
				if len(active) != tc.active {
					t.Fatalf("active=%v", active)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if counter.reads != 1 || counter.writes != tc.writes || changes != tc.changes {
				t.Fatalf("reads=%d writes=%d changes=%d; want 1/%d/%d", counter.reads, counter.writes, changes, tc.writes, tc.changes)
			}
			_, after, err := s.Get(ctx, "api", "prod")
			if err != nil {
				t.Fatal(err)
			}
			if tc.writes == 0 && after != before {
				t.Fatal("Unchanged maintenance rewrote the lock version")
			}
		})
	}
}

func TestMaintenanceConflictReevaluatesCurrentState(t *testing.T) {
	for _, operation := range []string{"reap", "unlock", "identity-mismatch", "deleted"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			s := newStore(t, now)
			initial, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 9, RunID: "running"}, time.Hour)
			if err != nil || !ok {
				t.Fatalf("seed: acquired=%v err=%v", ok, err)
			}
			s.Now = func() time.Time { return now.Add(2 * time.Hour) }
			base := s.store
			counter := &maintenanceCounter{Store: base}
			counter.beforeWrite = func(key string) {
				if operation == "deleted" {
					if err := base.Delete(ctx, key); err != nil {
						t.Fatal(err)
					}
					return
				}
				current := initial
				holder := *initial.Holder
				current.Holder = &holder
				current.Holder.ExpiresAt = now.Add(3 * time.Hour).Format(time.RFC3339)
				if operation == "unlock" {
					current.Holder.RunID = "new-live-run"
				}
				if operation == "identity-mismatch" {
					current.Project = "other-project"
				}
				data, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := base.Put(ctx, key, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			s.store = counter
			var changed int
			if operation == "unlock" {
				changed, _, err = s.UnlockPRAll(ctx, 9, "running", time.Hour, true)
			} else {
				changed, err = s.ReapAll(ctx, time.Hour)
			}
			if operation == "identity-mismatch" {
				if err == nil {
					t.Fatal("Identity change during retry did not fail closed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if changed != 0 || counter.reads != 2 || counter.writes != 1 {
				t.Fatalf("changes=%d reads=%d writes=%d; want 0/2/1", changed, counter.reads, counter.writes)
			}
			if operation == "deleted" {
				rc, _, err := base.Get(ctx, s.key("api", "prod"))
				if err == nil {
					rc.Close()
					t.Fatal("Maintenance recreated a deleted lock")
				}
				if !errors.Is(err, blob.ErrNotFound) {
					t.Fatal(err)
				}
				return
			}
			current, _, err := s.Get(ctx, "api", "prod")
			if err != nil {
				t.Fatal(err)
			}
			if current.Holder == nil || current.Holder.ExpiresAt != now.Add(3*time.Hour).Format(time.RFC3339) {
				t.Fatal("Concurrent holder update was lost")
			}
			if operation == "unlock" && current.Holder.RunID != "new-live-run" {
				t.Fatal("Cleanup removed another run's holder")
			}
		})
	}
}

func TestReapRejectsUnconditionalStoreBeforeChangingLock(t *testing.T) {
	ctx := context.Background()
	base := newUnconditionalStore()
	s := New(base)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	l := corelocks.NewLock("api", "prod", now)
	l.Holder = &corelocks.Holder{PR: 1, RunID: "expired", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Put(ctx, s.key("api", "prod"), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	_, err = s.ReapAll(ctx, time.Hour)
	if !errors.Is(err, blob.ErrConditionalWritesUnsupported) {
		t.Fatalf("Expected unsupported CAS error, got %v", err)
	}
	current, _, err := s.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if current.Holder == nil || current.Holder.RunID != "expired" {
		t.Fatal("Reaper changed an unprotected lock")
	}
}
