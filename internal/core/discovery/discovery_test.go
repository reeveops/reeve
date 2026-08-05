package discovery

import (
	"reflect"
	"sort"
	"testing"
)

func TestResolveLiteralAndPattern(t *testing.T) {
	enum := []Stack{
		{Project: "api", Path: "projects/api", Name: "dev"},
		{Project: "api", Path: "projects/api", Name: "prod"},
		{Project: "worker", Path: "services/worker", Name: "prod"},
		{Project: "scratch", Path: "projects/sandbox/scratch", Name: "dev"},
	}
	decls := []Declaration{
		{Project: "api", Path: "projects/api", Stacks: []string{"dev", "prod"}},
		{Pattern: "services/*", Stacks: []string{"prod"}},
	}
	filter := Filter{PathPatterns: []string{"projects/sandbox/**"}}

	got := Resolve(enum, decls, filter)
	want := []Stack{
		{Project: "api", Path: "projects/api", Name: "dev"},
		{Project: "api", Path: "projects/api", Name: "prod"},
		{Project: "worker", Path: "services/worker", Name: "prod"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestResolveStackPatternExclude(t *testing.T) {
	enum := []Stack{
		{Project: "api", Path: "projects/api", Name: "prod"},
		{Project: "api", Path: "projects/api", Name: "scratch"},
	}
	decls := []Declaration{{Project: "api", Path: "projects/api", Stacks: []string{"prod", "scratch"}}}
	filter := Filter{StackPatterns: []string{"*/scratch"}}
	got := Resolve(enum, decls, filter)
	if len(got) != 1 || got[0].Name != "prod" {
		t.Fatalf("expected only prod, got %+v", got)
	}
}

func TestAffectedByPath(t *testing.T) {
	stacks := []Stack{
		{Project: "api", Path: "projects/api", Name: "dev"},
		{Project: "worker", Path: "services/worker", Name: "prod"},
	}
	changed := []string{"projects/api/index.ts", "README.md"}
	cm := ChangeMapping{IgnoreChanges: []string{"**/*.md"}}
	got := Affected(stacks, changed, cm)
	if len(got) != 1 || got[0].Project != "api" {
		t.Fatalf("expected only api, got %+v", got)
	}
}

func TestAffectedByExtraTrigger(t *testing.T) {
	stacks := []Stack{
		{Project: "api", Path: "projects/api", Name: "prod"},
	}
	changed := []string{"shared/types/user.ts"}
	cm := ChangeMapping{ExtraTriggers: map[string][]string{"api": {"shared/types/**"}}}
	got := Affected(stacks, changed, cm)
	if len(got) != 1 {
		t.Fatalf("expected api to be triggered, got %+v", got)
	}
}

// Shared-dir layout: many stacks live in one directory, each with its own
// Pulumi.<name>.yaml. A change to one stack's config must not pull in siblings.
func sharedDirStacks() []Stack {
	return []Stack{
		{Project: "edge", Path: "projects/edge", Name: "alpha", Env: "alpha"},
		{Project: "edge", Path: "projects/edge", Name: "beta", Env: "beta"},
		{Project: "edge", Path: "projects/edge", Name: "gamma", Env: "gamma"},
	}
}

func names(got []Stack) []string {
	out := make([]string, 0, len(got))
	for _, s := range got {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func TestAffectedSharedDirPerStackConfig(t *testing.T) {
	got := Affected(sharedDirStacks(),
		[]string{"projects/edge/Pulumi.beta.yaml"}, ChangeMapping{})
	if g := names(got); len(g) != 1 || g[0] != "beta" {
		t.Fatalf("expected only beta, got %v", g)
	}
}

func TestAffectedSharedDirSiblingConfigsDistinct(t *testing.T) {
	got := Affected(sharedDirStacks(),
		[]string{
			"projects/edge/Pulumi.alpha.yaml",
			"projects/edge/Pulumi.beta.yaml",
		}, ChangeMapping{})
	if g := names(got); len(g) != 2 || g[0] != "alpha" || g[1] != "beta" {
		t.Fatalf("expected alpha + beta, got %v", g)
	}
}

func TestAffectedSharedDirSharedProgramFile(t *testing.T) {
	// A non-config file in the dir is shared program code -> all stacks.
	got := Affected(sharedDirStacks(),
		[]string{"projects/edge/index.ts"}, ChangeMapping{})
	if g := names(got); len(g) != 3 {
		t.Fatalf("expected all 3 stacks, got %v", g)
	}
}

func TestAffectedSharedDirSharedPulumiYaml(t *testing.T) {
	// The shared project file affects every stack in the dir.
	got := Affected(sharedDirStacks(),
		[]string{"projects/edge/Pulumi.yaml"}, ChangeMapping{})
	if g := names(got); len(g) != 3 {
		t.Fatalf("expected all 3 stacks, got %v", g)
	}
}

func TestAffectedSharedDirNestedFile(t *testing.T) {
	// A file in a subdirectory is shared program code -> all stacks.
	got := Affected(sharedDirStacks(),
		[]string{"projects/edge/components/bucket.ts"}, ChangeMapping{})
	if g := names(got); len(g) != 3 {
		t.Fatalf("expected all 3 stacks, got %v", g)
	}
}

func TestAffectedDocsOnlySkip(t *testing.T) {
	stacks := []Stack{{Project: "api", Path: "projects/api", Name: "prod"}}
	for _, f := range []string{"README.md", "docs/guide.adoc", "NOTES.txt", "x.rst", "logo.png", "LICENSE"} {
		res := AffectedDetailed(stacks, []string{f}, ChangeMapping{})
		if res.Reason != ReasonDocsOnly || len(res.Stacks) != 0 {
			t.Errorf("%s: want docs-only/no stacks, got reason=%v stacks=%v", f, res.Reason, res.Stacks)
		}
	}
}

func TestAffectedMixedDocsAndCodeStillMatches(t *testing.T) {
	stacks := []Stack{{Project: "api", Path: "projects/api", Name: "prod"}}
	res := AffectedDetailed(stacks, []string{"README.md", "projects/api/main.go"}, ChangeMapping{})
	if res.Reason != ReasonMatched || len(res.Stacks) != 1 {
		t.Fatalf("md dropped, code matches api: reason=%v stacks=%v", res.Reason, res.Stacks)
	}
}

func TestAffectedBroadenOnUnmapped(t *testing.T) {
	stacks := []Stack{
		{Project: "api", Path: "projects/api", Name: "prod"},
		{Project: "web", Path: "projects/web", Name: "prod"},
	}
	// Shared lib outside any stack dir -> preview all (auto default).
	res := AffectedDetailed(stacks, []string{"shared/provider/aws.go"}, ChangeMapping{})
	if res.Reason != ReasonBroadened || len(res.Stacks) != 2 {
		t.Fatalf("unmapped code should broaden to all: reason=%v stacks=%v", res.Reason, res.Stacks)
	}
	if len(res.Unmapped) != 1 || res.Unmapped[0] != "shared/provider/aws.go" {
		t.Errorf("unmapped list wrong: %v", res.Unmapped)
	}
}

func TestAffectedPulumiOnlyNoBroaden(t *testing.T) {
	stacks := []Stack{{Project: "api", Path: "projects/api", Name: "prod"}}
	res := AffectedDetailed(stacks, []string{"shared/provider/aws.go"},
		ChangeMapping{Scope: ScopePulumiOnly})
	if res.Reason != ReasonMatched || len(res.Stacks) != 0 {
		t.Fatalf("pulumi_only must not broaden: reason=%v stacks=%v", res.Reason, res.Stacks)
	}
}

func TestAffectedMappedCodeDoesNotBroaden(t *testing.T) {
	stacks := []Stack{
		{Project: "api", Path: "projects/api", Name: "prod"},
		{Project: "web", Path: "projects/web", Name: "prod"},
	}
	// Change is in api's dir -> only api, no broaden.
	res := AffectedDetailed(stacks, []string{"projects/api/main.go"}, ChangeMapping{})
	if res.Reason != ReasonMatched || len(res.Stacks) != 1 || res.Stacks[0].Project != "api" {
		t.Fatalf("mapped code must not broaden: reason=%v stacks=%v", res.Reason, res.Stacks)
	}
}

func TestAffectedNoChangesAllIgnored(t *testing.T) {
	stacks := []Stack{{Project: "api", Path: "projects/api", Name: "prod"}}
	changed := []string{"README.md", "docs/x.md"}
	cm := ChangeMapping{IgnoreChanges: []string{"**/*.md"}}
	got := Affected(stacks, changed, cm)
	if len(got) != 0 {
		t.Fatalf("expected no stacks, got %+v", got)
	}
}

// Broadening replaces the precise matches with every stack. That is right for
// preview ("what might be affected") and wrong for apply, so the precise set
// has to survive on Matched - otherwise a caller that must not widen its
// blast radius has nothing to fall back to.
func TestBroadeningPreservesPreciseMatches(t *testing.T) {
	stacks := []Stack{
		{Project: "payments", Path: "platform-edge/credova-payments", Name: "prod"},
		{Project: "ledger", Path: "platform-core/ledger", Name: "prod"},
	}
	changed := []string{
		"platform-edge/credova-payments/main.tf", // maps to one stack
		"shared/provider-versions.hcl",           // maps to none
	}

	res := AffectedDetailed(stacks, changed, ChangeMapping{Scope: ScopeAuto})
	if res.Reason != ReasonBroadened {
		t.Fatalf("expected broadening from the unmapped file, got %v", res.Reason)
	}
	if len(res.Stacks) != 2 {
		t.Errorf("broadened Stacks should be every stack, got %d", len(res.Stacks))
	}
	if len(res.Matched) != 1 || res.Matched[0].Project != "payments" {
		t.Fatalf("Matched must keep only the demonstrably affected stack, got %+v", res.Matched)
	}
}

// With no unmapped files there is nothing to broaden, and Matched equals
// Stacks so callers can use either.
func TestMatchedEqualsStacksWhenNotBroadened(t *testing.T) {
	stacks := []Stack{{Project: "payments", Path: "edge/payments", Name: "prod"}}
	res := AffectedDetailed(stacks, []string{"edge/payments/main.tf"}, ChangeMapping{Scope: ScopeAuto})
	if res.Reason != ReasonMatched {
		t.Fatalf("expected ReasonMatched, got %v", res.Reason)
	}
	if len(res.Matched) != len(res.Stacks) {
		t.Errorf("Matched (%d) should equal Stacks (%d) when nothing broadened",
			len(res.Matched), len(res.Stacks))
	}
}
