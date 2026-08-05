package run

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
)

func runPreviewWith(t *testing.T, changed []string, scope string) string {
	t.Helper()
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{
			{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"},
			{Project: "web", Path: "projects/web", Name: "dev", Env: "dev"},
		},
	}
	fvcs := &fakeVCS{changed: changed, headSHA: "head-sha"}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{
				{Project: "api", Path: "projects/api", Stacks: []string{"dev"}},
				{Project: "web", Path: "projects/web", Stacks: []string{"dev"}},
			},
			ChangeMapping: schemas.ChangeMap{Scope: scope},
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

func TestPreviewDocsOnlyNote(t *testing.T) {
	body := runPreviewWith(t, []string{"README.md", "CHANGELOG.md"}, "")
	if !strings.Contains(body, "Documentation/asset-only") {
		t.Errorf("expected docs-only note:\n%s", body)
	}
}

func TestPreviewBroadenNote(t *testing.T) {
	body := runPreviewWith(t, []string{"shared/provider/aws.go"}, "")
	if !strings.Contains(body, "Previewing all stacks") {
		t.Errorf("expected broaden note:\n%s", body)
	}
	// Both stacks previewed.
	if !strings.Contains(body, "api/dev") || !strings.Contains(body, "web/dev") {
		t.Errorf("broaden should preview all stacks:\n%s", body)
	}
}

func TestPreviewPulumiOnlyNoBroaden(t *testing.T) {
	body := runPreviewWith(t, []string{"shared/provider/aws.go"}, "pulumi_only")
	if strings.Contains(body, "Previewing all stacks") {
		t.Errorf("pulumi_only must not broaden:\n%s", body)
	}
}
