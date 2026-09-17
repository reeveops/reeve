package run

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
)

type snapshotBudgetStore struct {
	blob.Store
	lists map[string]int
	gets  map[string]int
}

func (s *snapshotBudgetStore) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	if strings.HasSuffix(key, "/manifest.json") {
		s.gets[key]++
	}
	return s.Store.Get(ctx, key)
}

func (s *snapshotBudgetStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.lists[prefix]++
	return s.Store.List(ctx, prefix)
}

func TestApplyReadsLargePreviewHistoryOnce(t *testing.T) {
	t.Parallel()
	base, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &snapshotBudgetStore{Store: base, lists: map[string]int{}, gets: map[string]int{}}

	const stackCount = 20
	stacks := make([]discovery.Stack, 0, stackCount)
	decls := make([]schemas.StackDecl, 0, stackCount)
	changed := make([]string, 0, stackCount)
	preview := make([]summary.StackSummary, 0, stackCount)
	for i := range stackCount {
		project := fmt.Sprintf("stack-%02d", i)
		path := "projects/" + project
		stacks = append(stacks, discovery.Stack{Project: project, Path: path, Name: "prod", Env: "prod"})
		decls = append(decls, schemas.StackDecl{Project: project, Path: path, Stacks: []string{"prod"}})
		changed = append(changed, path+"/main.tf")
		preview = append(preview, summary.StackSummary{Project: project, Stack: "prod", Env: "prod", Status: summary.StatusPlanned})
	}

	engine := &bgEngine{enum: stacks}
	vcsClient := &bgVCS{changed: changed, headSHA: bgSHA}
	in := plainApplyInput(t, engine, vcsClient, store)
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{Type: "pulumi", Stacks: decls}}

	// plainApplyInput writes one candidate. Add 98 manifests for other commits
	// and one newer candidate for a total history of 100 objects.
	for i := 0; i < 98; i++ {
		putManifest(t, store, in.PRNumber, fmt.Sprintf("historical-%03d", i), fmt.Sprintf("other-%03d", i),
			"2026-01-01T00:00:00Z", preview[:1])
	}
	putManifest(t, store, in.PRNumber, "preview-latest", bgSHA, "2099-01-01T00:00:00Z", preview)
	store.lists = map[string]int{}
	store.gets = map[string]int{}

	out, err := Apply(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed || out.Blocked || len(engine.applied) != stackCount {
		t.Fatalf("apply result = %+v, applied stacks = %d", out, len(engine.applied))
	}
	prefix := fmt.Sprintf("runs/pr-%d/", in.PRNumber)
	if store.lists[prefix] != 1 {
		t.Fatalf("manifest list calls = %d, want 1", store.lists[prefix])
	}
	totalReads := 0
	for key, reads := range store.gets {
		if reads > 1 {
			t.Fatalf("manifest %s read %d times, want at most 1", key, reads)
		}
		totalReads += reads
	}
	if totalReads != 2 {
		t.Fatalf("manifest content reads = %d, want 2 matching candidates", totalReads)
	}
}
