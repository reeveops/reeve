package main

import (
	"strings"
	"testing"
)

// TestEdgeBuildPublishRunsInCI pins the script test placement: the edge
// publish decision (skip / replace / fail) is load-bearing for the release
// fast-path, so its suite runs in CI next to the other script tests.
func TestEdgeBuildPublishRunsInCI(t *testing.T) {
	ci := readRepoFile(t, ".github", "workflows", "ci.yml")
	if !strings.Contains(ci, "bash .github/scripts/edge-publish_test.sh") {
		t.Fatal("ci.yml must run .github/scripts/edge-publish_test.sh next to the other script tests")
	}
	if !strings.Contains(ci, "bash .github/scripts/fetch-binary_test.sh") {
		t.Fatal("ci.yml must keep running the other script tests")
	}
}

// TestEdgeBuildSerializesPerCommit pins the concurrency guard: a push and a
// manual dispatch of the SAME commit share a group keyed on branch+commit
// and never cancel each other, so neither can delete or replace assets
// mid-upload while the other is publishing.
func TestEdgeBuildSerializesPerCommit(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "edge-build.yml")

	groupIdx := strings.Index(data, "concurrency:")
	if groupIdx < 0 {
		t.Fatal("edge-build.yml must carry a concurrency group for push-vs-dispatch serialization")
	}
	block := data[groupIdx:]
	if strings.Contains(block, "cancel-in-progress: true") {
		t.Fatal("a cancelled mid-upload run is one of the inconsistent states the publish script refuses; concurrency must not cancel")
	}
	if !strings.Contains(block, "cancel-in-progress: false") {
		t.Fatal("the concurrency group must set cancel-in-progress explicitly")
	}
	if !strings.Contains(block, "edge-build-${{ github.ref_name }}-${{ github.sha }}") {
		t.Fatalf("the group must be keyed on branch and commit:\n%s", block[:strings.Index(block, "cancel-in-progress:")])
	}
	// The two-call upload rule the guard protects: the publish script's own
	// suite (run in CI) covers the calls; this asserts the workflow still
	// delegates to it.
	if !strings.Contains(data, ".github/scripts/edge-publish.sh") {
		t.Fatal("edge-build.yml must publish through .github/scripts/edge-publish.sh")
	}
}
