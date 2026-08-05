package run

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
)

// faultStore wraps a blob.Store and fails writes whose key matches a
// predicate, simulating a storage outage scoped to one artifact family.
type faultStore struct {
	blob.Store
	failPut     func(key string) bool
	failIfMatch func(key string) bool
}

func (f *faultStore) Put(ctx context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	if f.failPut != nil && f.failPut(key) {
		return nil, errors.New("injected put failure")
	}
	return f.Store.Put(ctx, key, r)
}

func (f *faultStore) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	if f.failIfMatch != nil && f.failIfMatch(key) {
		return nil, errors.New("injected conditional-put failure")
	}
	return f.Store.PutIfMatch(ctx, key, r, ifMatch)
}

// TestManifestFailureStillAuditsAndCommentsAndExitsNonzero: a successful
// apply whose manifest write fails must still write the audit entry, post
// the PR comment (with a loud persistence warning), emit the timeline
// event, and return an error so the CLI exits nonzero.
func TestManifestFailureStillAuditsAndCommentsAndExitsNonzero(t *testing.T) {
	ctx := context.Background()
	engine := &failEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	fs, _ := filesystem.New(t.TempDir())
	store := &faultStore{Store: fs, failPut: func(key string) bool {
		return strings.HasSuffix(key, "/manifest.json") && strings.Contains(key, "apply-")
	}}
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "persistence failed") {
		t.Fatalf("manifest failure must surface as a run error, got %v", err)
	}
	if out == nil || len(engine.applied) != 1 {
		t.Fatalf("the apply itself ran: out=%v applied=%v", out, engine.applied)
	}

	// Audit entry still written, with the real apply outcome.
	e := readAuditEntry(t, fs)
	if e.Outcome != "success" {
		t.Fatalf("audit outcome = %q, want success (the apply shipped)", e.Outcome)
	}

	// PR comment posted and carries the persistence warning.
	all := fv.allComments()
	if !strings.Contains(all, "manifest persistence failed") && !strings.Contains(all, "Run manifest persistence failed") {
		t.Fatalf("PR comments must state the manifest persistence failure:\n%s", all)
	}
}

// TestBreakGlassIntentAuditWrittenBeforeApply: a break-glass run leaves an
// intent audit entry, and the completion entry still lands afterwards.
func TestBreakGlassIntentAuditWrittenBeforeApply(t *testing.T) {
	ctx := context.Background()
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
	}), store)

	out, err := Apply(ctx, in)
	if err != nil || out.Blocked {
		t.Fatalf("Apply: err=%v", err)
	}
	intent := readIntentEntry(t, store)
	if intent.Outcome != "break_glass_intent" || intent.BreakGlass == nil {
		t.Fatalf("intent entry malformed: %+v", intent)
	}
	if intent.BreakGlass.Justification != "prod is down" || intent.BreakGlass.AuthorizedVia != "internal_list" {
		t.Fatalf("intent break_glass mismatch: %+v", intent.BreakGlass)
	}
	if !strings.HasSuffix(intent.RunID, "-intent") {
		t.Fatalf("intent run id = %q", intent.RunID)
	}
	// Completion entry still present alongside.
	if e := readAuditEntry(t, store); e.Outcome != "success" {
		t.Fatalf("completion outcome = %q, want success", e.Outcome)
	}
}

// TestBreakGlassAuditWriteFailureRefusesToStart: if the intent audit entry
// cannot be written, the break-glass apply must not run the engine at all.
func TestBreakGlassAuditWriteFailureRefusesToStart(t *testing.T) {
	ctx := context.Background()
	engine, fv := newBGFixture()
	fs, _ := filesystem.New(t.TempDir())
	store := &faultStore{Store: fs, failIfMatch: func(key string) bool {
		return strings.HasPrefix(key, "audit/")
	}}
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
	}), store)

	_, err := Apply(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "intent audit write failed") {
		t.Fatalf("want intent-audit refusal, got %v", err)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("engine must not run when the intent audit cannot be written: %v", engine.applied)
	}
	// No lock may be left held either.
	l, _, err := in.Locks.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Fatalf("no lock should have been acquired: %+v", l.Holder)
	}
}
