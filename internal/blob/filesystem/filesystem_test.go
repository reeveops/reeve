package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
