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
// repository's check suite runs in a dedicated minimal-permission job that
// the release job needs, so a release never starts (never mints a tap
// token, never touches id-token signing) on a red check. The mise action is
// commit-pinned like every other external action.
func TestReleaseRunsRepositoryChecks(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "release.yml")

	// The check job exists, runs the repo's suite, and holds only
	// contents: read.
	checkJob := strings.Index(data, "  check:")
	if checkJob < 0 {
		t.Fatal("release.yml must carry a dedicated check job")
	}
	releaseJob := strings.Index(data, "  release:")
	if releaseJob < 0 || checkJob > releaseJob {
		t.Fatalf("the check job (%d) must precede the release job (%d)", checkJob, releaseJob)
	}
	checkBlock := data[checkJob:releaseJob]
	for _, want := range []string{
		"run: mise run check",
		"permissions:",
		"contents: read",
		"uses: jdx/mise-action@b44d12e7d0d8ee7461bb893e1b1993500433185b",
	} {
		if !strings.Contains(checkBlock, want) {
			t.Fatalf("check job block missing %q:\n%s", want, checkBlock)
		}
	}
	// The check job must not carry signing or write permissions.
	for _, banned := range []string{"id-token: write", "packages: write", "actions: write", "Mint tap token"} {
		if strings.Contains(checkBlock, banned) {
			t.Fatalf("minimal-permission check job must not contain %q:\n%s", banned, checkBlock)
		}
	}
	// The release job must wait for the check job.
	if !strings.Contains(data[releaseJob:], "needs: check") {
		t.Fatal("the release job must declare needs: check")
	}
	// The check suite must run before the release tag is minted: the check
	// job precedes the release job entirely.
	tagIdx := strings.Index(data, "Resolve release tag")
	if tagIdx < 0 || checkJob > tagIdx {
		t.Fatalf("checks (%d) must run before the release tag is minted (%d)", checkJob, tagIdx)
	}
}
