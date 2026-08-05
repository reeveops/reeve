package drift

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// enumeratingEngine reports fixed stacks, all drifted.
type enumeratingEngine struct{ stacks []discovery.Stack }

func (enumeratingEngine) Name() string { return "fake" }
func (e enumeratingEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return e.stacks, nil
}
func (enumeratingEngine) DriftCheck(context.Context, discovery.Stack, iac.PreviewOpts, bool) (iac.PreviewResult, error) {
	return iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn:x"},
	}, nil
}

// countingOverlap records calls and returns canned PRs plus an optional error.
type countingOverlap struct {
	mu    sync.Mutex
	calls [][]string
	prs   []OverlappingPR
	err   error
}

func (c *countingOverlap) FindOverlappingPRs(_ context.Context, paths []string) ([]OverlappingPR, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string(nil), paths...))
	return c.prs, c.err
}

func overlapRunOpts(t *testing.T, finder PROverlapFinder) Options {
	t.Helper()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stacks := []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "web", Path: "projects/web", Name: "prod", Env: "prod"},
	}
	return Options{
		Engine:   enumeratingEngine{stacks: stacks},
		RepoRoot: t.TempDir(),
		Decls: []discovery.Declaration{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "web", Path: "projects/web", Stacks: []string{"prod"}},
		},
		BootstrapMode: "alert_all",
		Redactor:      redact.New(),
		StateStore:    &StateStore{Blob: fs},
		PROverlap:     finder,
		Now:           func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) },
	}
}

func TestRunOverlapScansOnceAndMatchesPerStack(t *testing.T) {
	finder := &countingOverlap{prs: []OverlappingPR{
		{Number: 7, Author: "alice", Paths: []string{"projects/api/main.go"}},
		{Number: 9, Author: "bob", Paths: []string{"projects/web/index.ts"}},
	}}
	out, err := Run(context.Background(), overlapRunOpts(t, finder))
	if err != nil {
		t.Fatal(err)
	}
	if len(finder.calls) != 1 {
		t.Fatalf("overlap scan must run once per drift run (was N+1 per stack), got %d calls", len(finder.calls))
	}
	if len(finder.calls[0]) != 2 {
		t.Fatalf("single call must carry every drifted path, got %v", finder.calls[0])
	}
	byRef := map[string][]OverlappingPR{}
	for _, it := range out.Items {
		byRef[it.Ref()] = it.OverlappingPRs
	}
	if len(byRef["api/prod"]) != 1 || byRef["api/prod"][0].Number != 7 {
		t.Fatalf("api/prod overlap: %+v", byRef["api/prod"])
	}
	if len(byRef["web/prod"]) != 1 || byRef["web/prod"][0].Number != 9 {
		t.Fatalf("web/prod overlap: %+v", byRef["web/prod"])
	}
	if out.OverlapWarning != "" {
		t.Fatalf("clean scan must not warn: %q", out.OverlapWarning)
	}
}

func TestRunOverlapScanErrorSurfacesWarning(t *testing.T) {
	// Partial scan: PR 9 found, PR 4 could not be checked. The warning must
	// name the unchecked PR; the partial result still attaches.
	finder := &countingOverlap{
		prs: []OverlappingPR{{Number: 9, Author: "bob", Paths: []string{"projects/web/index.ts"}}},
		err: &approvals.OverlapScanError{Unchecked: []int{4}, Err: errors.New("boom")},
	}
	out, err := Run(context.Background(), overlapRunOpts(t, finder))
	if err != nil {
		t.Fatal(err)
	}
	if out.OverlapWarning == "" || !strings.Contains(out.OverlapWarning, "#4") {
		t.Fatalf("scan failure must surface a warning naming the unchecked PR, got %q", out.OverlapWarning)
	}
	found := false
	for _, it := range out.Items {
		if it.Ref() == "web/prod" && len(it.OverlappingPRs) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("partial overlap results must still attach")
	}
	report := ReportMarkdown(out)
	if !strings.Contains(report, "#4") {
		t.Fatal("report must carry the overlap warning")
	}
}
