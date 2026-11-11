package main

import (
	"strings"
	"testing"
)

// TestReleaseGatesMasterDispatchOnly pins the release trust boundary: a
// workflow_dispatch release from any ref but master must be refused by the
// FIRST step - before the tap token, tags, or GoReleaser exist. Tag pushes
// are the other initiation path and are exempt.
func TestReleaseGatesMasterDispatchOnly(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	refusalStep := strings.Index(data, "- name: Refuse non-master dispatch")
	if refusalStep < 0 {
		t.Fatal("release.yml must carry the non-master dispatch refusal step")
	}
	if !strings.Contains(data, "github.event_name == 'workflow_dispatch' && github.ref != 'refs/heads/master'") {
		t.Fatal("the refusal condition must trigger only for workflow_dispatch on a non-master ref")
	}
	if !strings.Contains(data, "::error::workflow_dispatch releases require the master ref") {
		t.Fatal("the refusal must say what was refused and what to do instead")
	}
	// Nothing meaningful may run before the refusal: the first real work is
	// the tap-token mint.
	tokenStep := strings.Index(data, "- name: Mint tap token")
	if tokenStep < 0 || refusalStep > tokenStep {
		t.Fatalf("the refusal step (%d) must run before any token minting (%d)", refusalStep, tokenStep)
	}
}

// TestReleaseRunsRepositoryChecks pins the release-evidence gate: the
// release job runs `mise run check` (the repo's own fmt/vet/lint/security/
// test gates) BEFORE minting a tag, and the mise action is commit-pinned
// like every other external action.
func TestReleaseRunsRepositoryChecks(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	checkIdx := strings.Index(data, "Run release checks")
	if checkIdx < 0 {
		t.Fatal("release.yml must run the repository's check suite before releasing")
	}
	if !strings.Contains(data, "run: mise run check") {
		t.Fatal("the release check step must run the repository's own check suite")
	}
	tagIdx := strings.Index(data, "Resolve release tag")
	if tagIdx < 0 || checkIdx > tagIdx {
		t.Fatalf("checks (%d) must run before the release tag is minted (%d)", checkIdx, tagIdx)
	}
	if !strings.Contains(data, "uses: jdx/mise-action@b44d12e7d0d8ee7461bb893e1b1993500433185b") {
		t.Fatal("the mise action must be pinned to its full commit SHA")
	}
}
