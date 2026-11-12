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
	releaseCommentIdx := strings.Index(data, "  # The build/sign job.")
	releaseIdx := strings.Index(data, "  release:\n")
	if tagIdx < 0 || releaseIdx < 0 || tagIdx > releaseIdx {
		t.Fatalf("release.yml must carry a tag: job before the release: job (tag=%d release=%d)", tagIdx, releaseIdx)
	}
	releaseBlock := data[releaseCommentIdx:]

	// The tag job runs only for dispatches, pushes the tag, and dispatches
	// the release run ON THE TAG REF - never builds or signs.
	tagBlock := data[tagIdx:releaseCommentIdx]
	for _, want := range []string{
		"if: github.event_name == 'workflow_dispatch'",
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
	// The non-master dispatch refusal names the seen ref and the fix, and
	// runs before anything exists.
	if !strings.Contains(tagBlock, "workflow_dispatch releases require the master ref") {
		t.Fatalf("tag job must refuse a non-master dispatch with the seen ref:\n%s", tagBlock)
	}
	refusalIdx := strings.Index(tagBlock, "- name: Refuse non-master dispatch")
	computeIdx := strings.Index(tagBlock, "- name: Compute and push release tag")
	if refusalIdx < 0 || computeIdx < 0 || refusalIdx > computeIdx {
		t.Fatalf("the non-master refusal must run before the tag is computed (%d vs %d)", refusalIdx, computeIdx)
	}

	// The build/sign job runs ONLY on a version tag ref.
	if !strings.Contains(releaseBlock, "if: startsWith(github.ref, 'refs/tags/v')") {
		t.Fatal("the release job must be gated on a version tag ref")
	}
	// The tap token and the signing grant exist only in the release job.
	for _, want := range []string{"id-token: write", "Mint tap token", "TAP_GITHUB_TOKEN"} {
		if !strings.Contains(releaseBlock, want) {
			t.Fatalf("release job must carry %q:\n%s", want, releaseBlock)
		}
	}
	// Defense in depth: a non-tag run that reaches the job fails naming the
	// ref it saw and the ref required, BEFORE the tap token is minted.
	refusalStep := strings.Index(releaseBlock, "- name: Refuse non-version-tag ref")
	if refusalStep < 0 {
		t.Fatal("the release job must refuse a non-tag ref naming the ref it saw and the required form")
	}
	if !strings.Contains(releaseBlock[refusalStep:], "release build/sign requires a version tag ref (refs/tags/v*)") {
		t.Fatalf("the refusal must say the required ref form:\n%s", releaseBlock[refusalStep:])
	}
	if !strings.Contains(releaseBlock[refusalStep:], "STARTED_FROM: ${{ github.ref }}") {
		t.Fatalf("the refusal must name the ref the run started on:\n%s", releaseBlock[refusalStep:])
	}
	tokenIdx := strings.Index(releaseBlock, "- name: Mint tap token")
	if tokenIdx < 0 || refusalStep > tokenIdx {
		t.Fatalf("the refusal step (%d) must precede the tap token mint (%d)", refusalStep, tokenIdx)
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
}
