package run

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/discovery"
)

// manifestFailStore fails Put only for apply-run manifests, standing in for
// a bucket outage at terminal persistence time. The error the store returns
// carries a credential-shaped value - the user-controlled error text must
// not reach the PR comment, the timeline, or any other sink unredacted.
type manifestFailStore struct {
	blob.Store
	fail error
}

func (f *manifestFailStore) Put(ctx context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	if strings.Contains(key, "/apply-") && strings.HasSuffix(key, "manifest.json") {
		return nil, f.fail
	}
	return f.Store.Put(ctx, key, r)
}

// TestManifestErrorIsRedactedInSinks pins the sink invariant at the apply
// boundary: the manifest persistence error is written to the PR comment and
// the timeline redacted. Before the fix the raw error - which can echo
// bucket paths, endpoints, or credential-shaped values - landed verbatim in
// all three sinks.
func TestManifestErrorIsRedactedInSinks(t *testing.T) {
	ctx := context.Background()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const secret = "AKIAIOSFODNN7EXAMPLE"
	store := &manifestFailStore{
		Store: blobStore,
		fail:  errors.New("put runs/pr-18/apply-7/manifest.json: bucket=https://key " + secret),
	}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if out == nil {
		t.Fatal("Apply must still return its outcome alongside the persistence error")
	}
	if err == nil || !strings.Contains(err.Error(), "post-apply persistence failed") {
		t.Fatalf("the run must exit nonzero with the persistence error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the returned error leaked the credential-shaped value: %v", err)
	}
	if out.Failed {
		t.Fatalf("a manifest persistence failure is not a stack failure: %+v", out)
	}

	comments := fv.allComments()
	if len(comments) == 0 {
		t.Fatal("apply must post its outcome comments even when the manifest write fails")
	}
	for _, want := range []string{"Run manifest persistence failed"} {
		if !strings.Contains(comments, want) {
			t.Fatalf("comment must still report the persistence failure:\nwant: %s\ncomments:\n%s", want, comments)
		}
	}
	if strings.Contains(comments, secret) {
		t.Fatalf("credential-shaped value leaked into comments:\n%s", comments)
	}
	if !strings.Contains(comments, "[redacted]") {
		t.Fatalf("comment must show the masked value:\n%s", comments)
	}
}

// TestManifestPersistsTypedCause pins that the redacted persistence error
// stays unwrappable: callers must be able to errors.Is/errors.As the cause
// while the printed text is masked - the old fmt.Errorf inlined the redacted
// string and destroyed the error chain.
func TestManifestPersistsTypedCause(t *testing.T) {
	ctx := context.Background()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The cause carries a credential-shaped value: its TEXT must be masked
	// while the error chain stays intact.
	cause := errors.New("put runs/pr-18/apply-7/manifest.json: token ghp_" + strings.Repeat("A", 36) + " EOF from backend")
	store := &manifestFailStore{Store: blobStore, fail: cause}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if err == nil {
		t.Fatal("expected the persistence failure")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("the redacted persistence error must unwrap to its cause: %v", err)
	}
	if strings.Contains(err.Error(), "ghp_AAAA") {
		t.Fatalf("the printed text leaked the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("the printed text must carry the redaction marker: %v", err)
	}
	if !strings.Contains(err.Error(), "post-apply persistence failed") {
		t.Fatalf("the printed text must keep the run context: %v", err)
	}
	if out == nil || out.CommentBody == "" {
		t.Fatal("the outcome must still be returned alongside the error")
	}
}
