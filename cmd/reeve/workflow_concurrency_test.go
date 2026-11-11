package main

import (
	"strings"
	"testing"
)

// TestPreviewConcurrencyGroupIncludesRoot pins the multi-root fix: the
// preview concurrency group must key on the root input, so previews for
// different roots of one PR do not cancel each other. The runtime behavior
// of a concurrency group cannot be exercised in a unit test; what CAN be
// pinned is that the group expression carries the root key.
func TestPreviewConcurrencyGroupIncludesRoot(t *testing.T) {
	data := readRepoFile(t, ".github", "workflows", "reeve.yml")
	if !strings.Contains(data, "reeve-preview-{0}-{1}-{2}") {
		t.Fatalf("preview concurrency group must include the third (root) key:\n%s", data)
	}
	// The group expression must read the workflow's root input, and the
	// expression must stay scoped to the pull_request preview branch.
	if !strings.Contains(data, "github.event.pull_request.number, inputs.root") {
		t.Fatal("the third group key must come from inputs.root so multi-root previews get distinct groups")
	}
}
