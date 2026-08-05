package factory

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/config/schemas"
)

func TestOpenFilesystemEndToEnd(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	store, err := Open(ctx, schemas.BucketConfig{Type: "filesystem", Name: "./.reeve-state"}, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Relative bucket names resolve under root.
	if _, err := store.Put(ctx, "locks/proj/dev.json", strings.NewReader(`{"a":1}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve-state", "locks", "proj", "dev.json")); err != nil {
		t.Errorf("blob not materialized under root: %v", err)
	}

	rc, md, err := store.Get(ctx, "locks/proj/dev.json")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != `{"a":1}` {
		t.Errorf("round-trip = %q", data)
	}
	if md.ETag == "" {
		t.Error("filesystem store must produce ETags for CAS")
	}

	keys, err := store.List(ctx, "locks/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "locks/proj/dev.json" {
		t.Errorf("List = %v", keys)
	}

	if _, _, err := store.Get(ctx, "missing"); err == nil || !strings.Contains(err.Error(), blob.ErrNotFound.Error()) {
		t.Errorf("missing key error = %v, want ErrNotFound", err)
	}
}

func TestOpenEmptyTypeDefaultsToFilesystem(t *testing.T) {
	root := t.TempDir()
	store, err := Open(context.Background(), schemas.BucketConfig{Name: "state"}, root)
	if err != nil {
		t.Fatalf("Open with empty type: %v", err)
	}
	if _, err := store.Put(context.Background(), "k", strings.NewReader("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "k")); err != nil {
		t.Errorf("empty type should behave as filesystem: %v", err)
	}
}

func TestOpenFilesystemAbsolutePathIsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "abs-state")
	store, err := Open(context.Background(), schemas.BucketConfig{Type: "filesystem", Name: dir}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "k", strings.NewReader("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "k")); err != nil {
		t.Errorf("absolute bucket path not honored: %v", err)
	}
}

func TestOpenValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     schemas.BucketConfig
		wantSub string
	}{
		{"filesystem without name", schemas.BucketConfig{Type: "filesystem"}, "bucket.name required"},
		{"unknown type", schemas.BucketConfig{Type: "ftp", Name: "x"}, `unknown bucket.type "ftp"`},
		{"azblob without service url", schemas.BucketConfig{Type: "azblob", Name: "container"}, "bucket.region must be the service URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(context.Background(), tc.cfg, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}
