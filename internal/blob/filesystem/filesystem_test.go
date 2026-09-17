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
	"github.com/reeveops/reeve/internal/blob/blobtest"
)

func TestContract(t *testing.T) {
	blobtest.RunContract(t, blobtest.Subject{
		NewStore: func(t *testing.T) blob.Store {
			t.Helper()
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatalf("new filesystem store: %v", err)
			}
			return store
		},
	})
}

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

func TestGetDoesNotCreateLockfiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runs", "manifest.json"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Get(t.Context(), "runs/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if _, err := os.Stat(filepath.Join(dir, lockNamespace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get created lock namespace: %v", err)
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

func TestListMetadataAndConditionalDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "runs/pr-1/old/manifest.json"
	if _, err := s.Put(ctx, key, strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	objects, err := s.ListMetadata(ctx, "runs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 {
		t.Fatalf("listed %d objects, want 1", len(objects))
	}
	listed := objects[0]
	if listed.Key != key || listed.Version == "" || listed.LastModified == 0 || listed.Size != 3 {
		t.Fatalf("listed object = %+v", listed)
	}

	if _, err := s.Put(ctx, key, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIfMatch(ctx, key, listed.Version); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("delete replaced object = %v, want ErrPreconditionFailed", err)
	}
	objects, err = s.ListMetadata(ctx, key)
	if err != nil || len(objects) != 1 {
		t.Fatalf("relist: objects=%v err=%v", objects, err)
	}
	if err := s.DeleteIfMatch(ctx, key, objects[0].Version); err != nil {
		t.Fatalf("delete current object: %v", err)
	}
	if _, _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get deleted object = %v, want ErrNotFound", err)
	}
}

func TestDeleteIfMatchRejectsIdenticalRewrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "runs/pr-1/old/manifest.json"
	if _, err := s.Put(ctx, key, strings.NewReader("same")); err != nil {
		t.Fatal(err)
	}
	objects, err := s.ListMetadata(ctx, key)
	if err != nil || len(objects) != 1 {
		t.Fatalf("list: objects=%v err=%v", objects, err)
	}
	staleVersion := objects[0].Version

	if _, err := s.Put(ctx, key, strings.NewReader("same")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIfMatch(ctx, key, staleVersion); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("delete identically rewritten object = %v, want ErrPreconditionFailed", err)
	}
	data, _, err := ReadBytes(ctx, s, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "same" {
		t.Fatalf("replacement content = %q, want same", data)
	}
}

func TestArtifactChurnUsesBoundedLockShards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	const artifactCount = 500
	for i := range artifactCount {
		key := fmt.Sprintf("runs/pr-1/history-%04d/manifest.json", i)
		if _, err := s.Put(t.Context(), key, strings.NewReader("manifest")); err != nil {
			t.Fatalf("put artifact %d: %v", i, err)
		}
	}
	objects, err := s.ListMetadata(t.Context(), "runs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != artifactCount {
		t.Fatalf("listed %d artifacts, want %d", len(objects), artifactCount)
	}
	for i, object := range objects {
		if err := s.DeleteIfMatch(t.Context(), object.Key, object.Version); err != nil {
			t.Fatalf("delete artifact %d: %v", i, err)
		}
	}

	locks, err := os.ReadDir(filepath.Join(dir, lockNamespace))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) > lockShardCount {
		t.Fatalf("artifact churn left %d lockfiles, want at most %d", len(locks), lockShardCount)
	}
	for _, lock := range locks {
		if !strings.HasPrefix(lock.Name(), "shard-") {
			t.Fatalf("artifact churn left per-key lockfile %q", lock.Name())
		}
	}
	generations, err := os.ReadDir(filepath.Join(dir, generationNamespace))
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 0 {
		t.Fatalf("artifact churn left %d generation files, want 0", len(generations))
	}
	keys, err := s.List(t.Context(), "runs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("artifact churn left objects: %v", keys)
	}
}

func TestConditionalWriteChurnUsesBoundedLockShards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	const recordCount = 500
	for i := range recordCount {
		key := fmt.Sprintf("audit/2026/09/17/run-%04d.json", i)
		if _, err := s.PutIfMatch(t.Context(), key, strings.NewReader("audit"), ""); err != nil {
			t.Fatalf("write audit record %d: %v", i, err)
		}
	}

	locks, err := os.ReadDir(filepath.Join(dir, lockNamespace))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) > lockShardCount {
		t.Fatalf("conditional-write churn left %d lockfiles, want at most %d", len(locks), lockShardCount)
	}
	for _, lock := range locks {
		if !strings.HasPrefix(lock.Name(), "shard-") {
			t.Fatalf("conditional-write churn left per-key lockfile %q", lock.Name())
		}
	}
}

func TestListMetadataSkipsTransientFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(t.Context(), "runs/pr-1/manifest.json", strings.NewReader("manifest")); err != nil {
		t.Fatal(err)
	}
	tempPath := filepath.Join(dir, "runs", "pr-1", temporaryPrefix+"deadbeef")
	if err := os.WriteFile(tempPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	objects, err := s.ListMetadata(t.Context(), "runs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].Key != "runs/pr-1/manifest.json" {
		t.Fatalf("ListMetadata = %+v, want only manifest", objects)
	}
	keys, err := s.List(t.Context(), "runs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "runs/pr-1/manifest.json" {
		t.Fatalf("List = %v, want only manifest", keys)
	}

	root, err := s.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var listed []blob.ListedObject
	if err := appendListedObject(root, "runs/pr-1/already-gone", &listed); err != nil {
		t.Fatalf("disappeared object aborted listing: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("disappeared object was listed: %+v", listed)
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
		if isInternalKey(k) {
			t.Fatalf("List returned internal metadata: %q (all keys: %v)", k, keys)
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

	lockPath := filepath.Join(dir, osKey(lockPathFor("locks/api/prod.json")))
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
	if !errors.Is(err, blob.ErrPreconditionFailed) &&
		!strings.Contains(err.Error(), "read current object") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Returning an error is not enough: the object must be untouched. A
	// regression that writes and then reports failure would satisfy the
	// assertion above while having already destroyed the data.
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("object was modified by a failed write: %q, want %q", got, "original")
	}
}

// TestLockNamespaceIsReserved pins that lockfiles live outside the object
// key space entirely.
//
// They used to sit beside their target as "<key>.lock", which put them IN
// the key space: an object legitimately named "foo.lock" collided with the
// lock for "foo", and List had to classify by suffix - hiding real objects
// that happened to end in .lock.
func TestLockNamespaceIsReserved(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// An object may legitimately be named "<something>.lock" and must
	// round-trip and list like any other key.
	if _, err := s.PutIfMatch(ctx, "runs/foo", strings.NewReader("object"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfMatch(ctx, "runs/foo.lock", strings.NewReader("also an object"), ""); err != nil {
		t.Fatalf("an object named *.lock was rejected: %v", err)
	}
	keys, err := s.List(ctx, "runs/")
	if err != nil {
		t.Fatal(err)
	}
	var sawDotLock bool
	for _, k := range keys {
		if k == "runs/foo.lock" {
			sawDotLock = true
		}
	}
	if !sawDotLock {
		t.Fatalf("List hid a real object ending in .lock: %v", keys)
	}

	// The reserved namespace itself is not addressable as an object.
	if _, err := s.PutIfMatch(ctx, lockNamespace+"/anything", strings.NewReader("x"), ""); err == nil {
		t.Fatal("writing into the reserved lock namespace was allowed")
	}
	if _, _, err := s.Get(ctx, lockNamespace+"/anything"); err == nil {
		t.Fatal("reading from the reserved lock namespace was allowed")
	}
	if _, err := s.Put(ctx, generationNamespace+"/anything", strings.NewReader("x")); err == nil {
		t.Fatal("writing into the reserved generation namespace was allowed")
	}
	if _, err := s.Put(ctx, "runs/"+temporaryPrefix+"anything", strings.NewReader("x")); err == nil {
		t.Fatal("writing an internal temporary key was allowed")
	}
	// ...and never surfaces from a whole-bucket walk.
	all, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range all {
		if isInternalKey(k) {
			t.Fatalf("internal namespace leaked into List: %q", k)
		}
	}
}
