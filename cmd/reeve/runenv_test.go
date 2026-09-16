package main

import (
	"path/filepath"
	"testing"
)

func TestRepoPathForRoot(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("GITHUB_WORKSPACE", workspace)

	tests := []struct {
		name string
		root string
		want string
	}{
		{name: "workspace", root: workspace, want: "."},
		{name: "nested", root: filepath.Join(workspace, "tf"), want: "tf"},
		{name: "deep nested", root: filepath.Join(workspace, "infra", "tf"), want: "infra/tf"},
		{name: "outside workspace", root: t.TempDir(), want: "."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoPathForRoot(tc.root); got != tc.want {
				t.Fatalf("repoPathForRoot(%q) = %q, want %q", tc.root, got, tc.want)
			}
		})
	}
}

func TestRepoPathForRootOutsideGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_WORKSPACE", "")
	if got := repoPathForRoot("/some/root"); got != "." {
		t.Fatalf("repoPathForRoot() = %q, want .", got)
	}
}
