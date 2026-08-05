package run

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// previewWithApplied runs a preview at a SHA that already has an applied-state
// pointer and returns the rendered comment body.
func previewWithApplied(t *testing.T, force bool) string {
	t.Helper()
	ctx := context.Background()
	const sha = "head-sha-applied"

	store, _ := filesystem.New(t.TempDir())
	// Seed a prior applied pointer for this PR + SHA.
	if err := writeAppliedState(ctx, store, AppliedState{
		CommitSHA: sha, RunID: "apply-9-headsha", RunNumber: 9,
		AppliedAt: "2026-06-23T00:00:00Z", PR: 1,
	}); err != nil {
		t.Fatal(err)
	}

	engine := &fakeEngine{
		enum:    []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
		results: map[string]iac.PreviewResult{"api/dev": {Counts: summary.Counts{Add: 1}}},
	}
	fvcs := &fakeVCS{changed: []string{"projects/api/main.ts"}, headSHA: sha}

	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: sha,
		RunNumber: 10,
		RepoRoot:  "/nope",
		Force:     force,
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.CommentBody
}

func TestPreviewNoticeWhenAlreadyApplied(t *testing.T) {
	body := previewWithApplied(t, false)
	if !strings.Contains(body, "already applied on run #9") {
		t.Errorf("expected already-applied notice, got:\n%s", body)
	}
}

func TestPreviewNoNoticeWhenForced(t *testing.T) {
	body := previewWithApplied(t, true)
	if strings.Contains(body, "already applied") {
		t.Errorf("force should suppress the notice, got:\n%s", body)
	}
}
