package main

import (
	"strings"
	"testing"
)

// TestReleaseTagJobDispatchesOnTag pins the release signing trust boundary:
// a workflow_dispatch run on master ONLY computes and pushes the version
// tag and then starts the release run on that tag ref. It never builds or
// signs on master, so a version-tagged artifact carries the cosign identity
// of a run on refs/tags/<version> - a run on master cannot produce it.
func TestReleaseTagJobDispatchesOnTag(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	tagIdx := strings.Index(data, "  tag:")
	releaseIdx := strings.Index(data, "  release:\n")
	if tagIdx < 0 || releaseIdx < 0 || tagIdx > releaseIdx {
		t.Fatalf("release.yml must carry a tag: job before the release: job (tag=%d release=%d)", tagIdx, releaseIdx)
	}

	// The tag job runs only for a dispatch ON MASTER: the dispatched
	// release run is itself a workflow_dispatch (on the tag ref), so a
	// master-only condition keeps that run's tag job skipped instead of
	// failed.
	tagBlock := ""
	tagBlockEnd := strings.Index(data[tagIdx:], "  # The build/sign job.")
	if tagBlockEnd > 0 {
		tagBlock = data[tagIdx : tagIdx+tagBlockEnd]
	} else {
		tagBlock = data[tagIdx:]
	}
	if !strings.Contains(tagBlock, "github.event_name == 'workflow_dispatch'") {
		t.Fatalf("tag job must run for dispatches:\n%s", tagBlock)
	}
	if !strings.Contains(tagBlock, "github.ref == 'refs/heads/master'") {
		t.Fatalf("tag job must run only on the master ref (the dispatched run happens on a tag):\n%s", tagBlock)
	}
	for _, want := range []string{
		"actions: write",  // start the release run
		"contents: write", // push the version tag
		`gh workflow run release.yml --ref "$TAG"`,
	} {
		if !strings.Contains(tagBlock, want) {
			t.Fatalf("tag job must dispatch the release on the tag ref, missing %q:\n%s", want, tagBlock)
		}
	}
	// The tag job carries NO signing grants and no tap token: it cannot sign.
	for _, banned := range []string{"id-token: write", "Mint tap token", "GoReleaser", "GHCR"} {
		if strings.Contains(tagBlock, banned) {
			t.Fatalf("tag job must not carry signing capability %q:\n%s", banned, tagBlock)
		}
	}
	if !strings.Contains(tagBlock, "workflow_dispatch tag computation requires the master ref") {
		t.Fatalf("tag job must refuse a non-master dispatch with the seen ref:\n%s", tagBlock)
	}
	refusalIdx := strings.Index(tagBlock, "- name: Refuse non-master dispatch")
	computeIdx := strings.Index(tagBlock, "- name: Compute and push release tag")
	if refusalIdx < 0 || computeIdx < 0 || refusalIdx > computeIdx {
		t.Fatalf("the non-master refusal must run before the tag is computed (%d vs %d)", refusalIdx, computeIdx)
	}
}

// TestReleaseRefusesBadDispatchRefs pins the dispatch refusal surface: a
// dispatch on a non-tag ref other than master fails a dedicated job naming
// the ref it saw and the two forms a release may start on. A dispatch on a
// version tag runs the release job only - no failed job in the run.
func TestReleaseRefusesBadDispatchRefs(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	guardIdx := strings.Index(data, "  refuse-bad-dispatch-ref:")
	if guardIdx < 0 {
		t.Fatal("release.yml must carry the refuse-bad-dispatch-ref guard job")
	}
	condStart := strings.Index(data[guardIdx:], "github.event_name == 'workflow_dispatch'")
	if condStart < 0 {
		t.Fatal("the guard job must be dispatch-only")
	}
	cond := data[guardIdx : guardIdx+strings.Index(data[guardIdx:], "runs-on:")]
	// The guard must trigger for non-master dispatches on non-tag refs only.
	for _, want := range []string{
		"github.event_name == 'workflow_dispatch'",
		"github.ref != 'refs/heads/master'",
		"!startsWith(github.ref, 'refs/tags/v')",
	} {
		if !strings.Contains(cond, want) {
			t.Fatalf("guard condition must include %q:\n%s", want, cond)
		}
	}
	if !strings.Contains(data[guardIdx:], "workflow_dispatch releases start on refs/heads/master") {
		t.Fatal("the guard refusal must name the two start forms and the seen ref")
	}
	if !strings.Contains(data[guardIdx:], "STARTED_FROM: ${{ github.ref }}") {
		t.Fatal("the guard refusal must name the ref the run started on")
	}
}

// TestReleaseRequiresFullVersionTag pins the release ref form: a full
// vX.Y.Z (optionally with a prerelease suffix) is required; anything else -
// branches, and tags like the floating v0/v1 major aliases - is refused
// naming the ref seen and the form required, before any token or checkout.
func TestReleaseRequiresFullVersionTag(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	if !strings.Contains(data, "if: startsWith(github.ref, 'refs/tags/v')") {
		t.Fatal("the release job must be gated on a version tag ref")
	}
	guardStep := strings.Index(data, "- name: Refuse non-version-tag ref")
	if guardStep < 0 {
		t.Fatal("the release job must carry the version-form refusal step")
	}
	rest := data[guardStep:]
	if !strings.Contains(rest, `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`) {
		t.Fatal("the refusal must require the exact full version form vX.Y.Z(-prerelease)")
	}
	if !strings.Contains(rest, "requires a full version tag ref") {
		t.Fatal("the refusal must say what is required")
	}
	if !strings.Contains(rest, "The floating major tags (v0, v1, ...) are pointers, not releases") {
		t.Fatal("the refusal must explain why a non-version tag is refused")
	}
	if !strings.Contains(rest, "STARTED_FROM: ${{ github.ref }}") {
		t.Fatal("the refusal must name the ref the run started on")
	}
	tokenIdx := strings.Index(rest, "- name: Mint tap token")
	if tokenIdx < 0 || !strings.Contains(rest[:tokenIdx], "exit 1") {
		t.Fatalf("the refusal must precede the tap token mint and fail closed")
	}
}

// TestReleaseJobsAreMinimallyPermissive pins the permission split: the
// baseline is read-only and each job grants only what it needs, so the
// tag-computation run never holds signing identity.
func TestReleaseJobsAreMinimallyPermissive(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	jobsIdx := strings.Index(data, "\njobs:")
	if jobsIdx < 0 {
		t.Fatal("no jobs block")
	}
	top := data[:jobsIdx]
	topPerms := strings.Index(top, "permissions:")
	if topPerms < 0 {
		t.Fatal("release.yml must declare a minimal top-level permissions block")
	}
	sub := top[topPerms:]
	block := sub[:strings.Index(sub, "concurrency:")]
	if strings.Contains(block, "write") {
		t.Fatalf("top-level permissions must be read-only:\n%s", block)
	}
	if !strings.Contains(block, "contents: read") {
		t.Fatalf("top-level permissions must include contents: read:\n%s", block)
	}
	// The guard job is dispatch-only and holds no write grants.
	guardIdx := strings.Index(data, "  refuse-bad-dispatch-ref:")
	guardBlock := data[guardIdx:]
	if !strings.Contains(guardBlock, "permissions:\n      contents: read") {
		t.Fatalf("the guard job must be minimal: %s", guardBlock[:strings.Index(guardBlock, "steps:")])
	}
}
