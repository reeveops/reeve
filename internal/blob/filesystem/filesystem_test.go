package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
)

func TestPutGet(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	md, err := s.Put(ctx, "runs/pr-1/manifest.json", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if md.ETag == "" {
		t.Fatal("expected ETag")
	}

	data, _, err := ReadBytes(ctx, s, "runs/pr-1/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("roundtrip mismatch: %s", data)
	}
}

func TestPutIfMatchOnlyIfAbsent(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	// empty ifMatch => only if absent
	if _, err := s.PutIfMatch(ctx, "locks/a.json", strings.NewReader("v1"), ""); err != nil {
		t.Fatalf("first put: %v", err)
	}
	_, err := s.PutIfMatch(ctx, "locks/a.json", strings.NewReader("v2"), "")
	if !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed, got %v", err)
	}
}

func TestPutIfMatchWithETag(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	md, err := s.Put(ctx, "locks/b.json", strings.NewReader("v1"))
	if err != nil {
		t.Fatal(err)
	}
	// wrong etag
	_, err = s.PutIfMatch(ctx, "locks/b.json", strings.NewReader("v2"), "wrong")
	if !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("expected mismatch rejection, got %v", err)
	}
	// right etag
	if _, err := s.PutIfMatch(ctx, "locks/b.json", strings.NewReader("v2"), md.ETag); err != nil {
		t.Fatalf("if-match with correct etag failed: %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	_, _, err := s.Get(ctx, "missing")
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	_, err := s.Put(ctx, "../escape", strings.NewReader("nope"))
	if err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("expected path-escape rejection, got %v", err)
	}
}

// TestSymlinkEscapeRejected covers the case the old string-based containment
// check could not see: the key is lexically clean, but a directory under the
// bucket is a symlink pointing outside it. The string check passed such a key
// and the write landed wherever the link went; os.Root refuses to traverse it.
func TestSymlinkEscapeRejected(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(bucket, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	s, err := New(bucket)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Put(ctx, "escape/pwned.json", strings.NewReader("nope")); err == nil {
		t.Error("Put through a symlinked directory must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.json")); err == nil {
		t.Fatal("write escaped the bucket root")
	}

	// Reads must be refused for the same reason.
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, "escape/secret.json"); err == nil {
		t.Error("Get through a symlinked directory must be refused")
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	for _, k := range []string{"runs/pr-1/a.json", "runs/pr-1/b.json", "runs/pr-2/c.json"} {
		if _, err := s.Put(ctx, k, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.List(ctx, "runs/pr-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys under pr-1, got %v", keys)
	}
}

// TestPutIfMatchSerializesConcurrentCreates is the race the lockfile exists
// to prevent. Many goroutines race to create the same key with
// If-None-Match; exactly one may win.
//
// release() used to unlink the lockfile, which let a waiter already blocked
// on the old inode and a newcomer creating a fresh one both hold "the"
// lock, so their compare-and-write bodies interleaved.
func TestPutIfMatchSerializesConcurrentCreates(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 24
	var wins int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := s.PutIfMatch(context.Background(), "locks/api/prod.json",
				strings.NewReader(fmt.Sprintf("writer-%d", n)), "")
			switch {
			case err == nil:
				atomic.AddInt64(&wins, 1)
			case errors.Is(err, blob.ErrPreconditionFailed):
				// expected loser
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&wins); got != 1 {
		t.Fatalf("%d goroutines created the same key; exactly 1 may win", got)
	}
}

// TestListHidesLockfiles pins that internal lockfiles never surface as
// objects. They are no longer unlinked on release, and even before that a
// concurrent List could catch one mid-flight - PruneRunArtifacts would then
// try to read it as a run artifact.
func TestListHidesLockfiles(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.PutIfMatch(ctx, "runs/pr-1/manifest.json", strings.NewReader("{}"), ""); err != nil {
		t.Fatal(err)
	}
	keys, err := s.List(ctx, "runs/")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.HasSuffix(k, lockSuffix) {
			t.Fatalf("List returned an internal lockfile: %q (all keys: %v)", k, keys)
		}
	}
	if len(keys) != 1 || keys[0] != "runs/pr-1/manifest.json" {
		t.Fatalf("List = %v, want just the manifest", keys)
	}
}

// TestLockfileSurvivesRelease pins the property that makes the flock race
// impossible: the lockfile inode must stay put across a release/acquire
// cycle, so every waiter contends on the same one.
//
// The race itself needs a three-way interleaving (a waiter blocked on the
// old inode while the holder unlinks and a newcomer creates a fresh one)
// that is not reproducible on demand in a unit test - so this asserts the
// invariant instead of trying to catch the symptom.
func TestLockfileSurvivesRelease(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.PutIfMatch(ctx, "locks/api/prod.json", strings.NewReader("{}"), ""); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(dir, "locks", "api", "prod.json"+lockSuffix)
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("lockfile missing after a completed PutIfMatch: %v", err)
	}

	// A second conditional write releases and re-acquires the same lock.
	_, err = s.PutIfMatch(ctx, "locks/api/prod.json", strings.NewReader("{}"), "")
	if err != nil && !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("lockfile was unlinked on release: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lockfile inode changed across release/acquire; waiters would contend on different inodes")
	}
}

// TestPutIfMatchCreateFailsClosedOnReadError pins that only "not there" may
// be read as absence. An unreadable existing object used to fall through
// the create-if-absent branch and overwrite whatever was really there.
func TestPutIfMatchCreateFailsClosedOnReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.PutIfMatch(ctx, "runs/secret.json", strings.NewReader("original"), ""); err != nil {
		t.Fatal(err)
	}
	// Make the existing object unreadable, so hashing it fails with
	// something other than "not exist".
	target := filepath.Join(dir, "runs", "secret.json")
	if err := os.Chmod(target, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })

	_, err = s.PutIfMatch(ctx, "runs/secret.json", strings.NewReader("clobbered"), "")
	if err == nil {
		t.Fatal("create-if-absent succeeded over an existing but unreadable object (fail-open)")
	}
	if errors.Is(err, blob.ErrPreconditionFailed) {
		return // also acceptable: refused
	}
	if !strings.Contains(err.Error(), "read current object") {
		t.Fatalf("unexpected error: %v", err)
	}
}
