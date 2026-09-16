// Package blobtest is the executable form of the blob.Store contract.
//
// Every adapter runs through the same lifecycle so provider behavior cannot
// drift behind a shared interface. Subjects must return a new isolated store
// for every call, which also makes the suite safe for real cloud buckets.
package blobtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob"
)

// Subject constructs one isolated store for each contract assertion.
type Subject struct {
	NewStore func(t *testing.T) blob.Store
}

// IsolatedPrefix returns a fresh child prefix for one contract store.
func IsolatedPrefix(t *testing.T, base string) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("create isolated blob prefix: %v", err)
	}
	parts := []string{strings.Trim(base, "/"), "reeve-contract", hex.EncodeToString(suffix[:])}
	return strings.Trim(strings.Join(parts, "/"), "/")
}

// RunContract runs the provider-independent blob.Store assertions.
func RunContract(t *testing.T, subject Subject) {
	t.Helper()
	if subject.NewStore == nil {
		t.Fatal("blobtest.Subject.NewStore is required")
	}
	t.Run("MissingObject", func(t *testing.T) { testMissingObject(t, subject) })
	t.Run("PutGetOverwrite", func(t *testing.T) { testPutGetOverwrite(t, subject) })
	t.Run("ConditionalCreate", func(t *testing.T) { testConditionalCreate(t, subject) })
	t.Run("ConditionalUpdate", func(t *testing.T) { testConditionalUpdate(t, subject) })
	t.Run("ConcurrentCreate", func(t *testing.T) { testConcurrentCreate(t, subject) })
	t.Run("RecursiveList", func(t *testing.T) { testRecursiveList(t, subject) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, subject) })
}

func testMissingObject(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	r, md, err := store.Get(t.Context(), "missing/object.json")
	if r != nil || md != nil {
		t.Errorf("Get missing object returned reader=%v metadata=%v", r, md)
	}
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get missing object = %v, want ErrNotFound", err)
	}
}

func testPutGetOverwrite(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	const key = "runs/pr-1/run-1/manifest.json"
	put(t, store, key, "first")
	first, firstMD := get(t, store, key)
	if first != "first" {
		t.Fatalf("Get after Put = %q, want %q", first, "first")
	}
	assertMetadata(t, firstMD, int64(len(first)))

	put(t, store, key, "second-value")
	second, secondMD := get(t, store, key)
	if second != "second-value" {
		t.Fatalf("Get after overwrite = %q, want %q", second, "second-value")
	}
	assertMetadata(t, secondMD, int64(len(second)))
	if firstMD.ETag == secondMD.ETag {
		t.Fatalf("ETag did not change after overwrite: %q", firstMD.ETag)
	}
}

func testConditionalCreate(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	const key = "locks/project/stack.json"
	if _, err := store.PutIfMatch(t.Context(), key, strings.NewReader("owner-1"), ""); err != nil {
		t.Fatalf("initial conditional create: %v", err)
	}
	if _, err := store.PutIfMatch(t.Context(), key, strings.NewReader("owner-2"), ""); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("duplicate conditional create = %v, want ErrPreconditionFailed", err)
	}
	if got, _ := get(t, store, key); got != "owner-1" {
		t.Fatalf("failed conditional create changed object to %q", got)
	}
}

func testConditionalUpdate(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	const key = "notifications/pr-1/timeline.json"
	put(t, store, key, "v1")
	_, firstMD := get(t, store, key)
	if _, err := store.PutIfMatch(t.Context(), key, strings.NewReader("v2"), firstMD.ETag); err != nil {
		t.Fatalf("matched conditional update: %v", err)
	}
	_, secondMD := get(t, store, key)
	if firstMD.ETag == secondMD.ETag {
		t.Fatalf("ETag did not change after conditional update: %q", firstMD.ETag)
	}
	if _, err := store.PutIfMatch(t.Context(), key, strings.NewReader("stale"), firstMD.ETag); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("stale conditional update = %v, want ErrPreconditionFailed", err)
	}
	if got, _ := get(t, store, key); got != "v2" {
		t.Fatalf("stale conditional update changed object to %q", got)
	}
}

func testConcurrentCreate(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	const writers = 8
	ctx := t.Context()
	var mu sync.Mutex
	wins := 0
	var unexpected error
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PutIfMatch(ctx, "locks/race/stack.json", strings.NewReader(fmt.Sprintf("writer-%d", i)), "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, blob.ErrPreconditionFailed):
			default:
				if unexpected == nil {
					unexpected = err
				}
			}
		}()
	}
	wg.Wait()
	if unexpected != nil {
		t.Fatalf("concurrent conditional create: %v", unexpected)
	}
	if wins != 1 {
		t.Fatalf("concurrent conditional create winners = %d, want 1", wins)
	}
}

func testRecursiveList(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	for _, key := range []string{
		"runs/pr-1/run-1/manifest.json",
		"runs/pr-1/run-1/api/summary.json",
		"runs/pr-1/run-2/manifest.json",
		"runs/pr-2/run-1/manifest.json",
	} {
		put(t, store, key, key)
	}
	got, err := store.List(t.Context(), "runs/pr-1/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(got)
	want := []string{
		"runs/pr-1/run-1/api/summary.json",
		"runs/pr-1/run-1/manifest.json",
		"runs/pr-1/run-2/manifest.json",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("List = %q, want %q", got, want)
	}
}

func testDelete(t *testing.T, subject Subject) {
	store := newStore(t, subject)
	const key = "audit/2026/09/16/run.json"
	put(t, store, key, "audit")
	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatalf("Delete existing object: %v", err)
	}
	if _, _, err := store.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get deleted object = %v, want ErrNotFound", err)
	}
	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatalf("Delete missing object: %v", err)
	}
}

func newStore(t *testing.T, subject Subject) blob.Store {
	t.Helper()
	store := subject.NewStore(t)
	if store == nil {
		t.Fatal("blobtest.Subject.NewStore returned nil")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		keys, err := store.List(ctx, "")
		if err != nil {
			t.Errorf("list contract namespace during cleanup: %v", err)
			return
		}
		for _, key := range keys {
			if err := store.Delete(ctx, key); err != nil {
				t.Errorf("delete contract object %q during cleanup: %v", key, err)
			}
		}
	})
	return store
}

func put(t *testing.T, store blob.Store, key, value string) {
	t.Helper()
	if _, err := store.Put(t.Context(), key, strings.NewReader(value)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func get(t *testing.T, store blob.Store, key string) (string, *blob.Metadata) {
	t.Helper()
	r, md, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if r == nil {
		t.Fatalf("Get(%q) returned a nil reader", key)
	}
	data, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		t.Fatalf("read %q: %v", key, readErr)
	}
	if closeErr != nil {
		t.Fatalf("close %q: %v", key, closeErr)
	}
	return string(data), md
}

func assertMetadata(t *testing.T, md *blob.Metadata, wantSize int64) {
	t.Helper()
	if md == nil {
		t.Fatal("Get returned nil metadata")
	}
	if md.ETag == "" {
		t.Fatal("Get returned an empty ETag")
	}
	if md.Size != wantSize {
		t.Fatalf("Get metadata size = %d, want %d", md.Size, wantSize)
	}
}
