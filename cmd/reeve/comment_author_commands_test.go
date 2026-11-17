package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	gh "github.com/reeveops/reeve/internal/vcs/github"
)

var errStopAtGitHubClient = errors.New("stop at GitHub client construction")

func TestCommentAuthorWiringReachesEveryCommandClient(t *testing.T) {
	fakeTTY(t, true)
	root := pulumiRepo(t)
	if out, err := runReeve(t, "init", "-n"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	sharedPath := filepath.Join(root, ".reeve", "shared.yaml")
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "comments:\n", "comments:\n  author: cfg-author[bot]\n", 1)
	if updated == string(data) {
		t.Fatal("scaffolded comments block not found")
	}
	mustWrite(t, sharedPath, updated)

	commands := []struct {
		name string
		new  func() *cobra.Command
		run  func(*cobra.Command) error
		args []string
	}{
		{name: "ready", new: newReadyCmd, run: func(cmd *cobra.Command) error {
			return runReady(cmd, nil)
		}, args: []string{"--pr=18"}},
		{name: "pr-help", new: newHelpCmd, run: func(cmd *cobra.Command) error {
			return runHelp(cmd, nil)
		}, args: []string{"--pr=18"}},
		{name: "explain", new: newExplainCmd, run: func(cmd *cobra.Command) error {
			return runExplain(cmd, nil)
		}, args: []string{"--pr=18"}},
	}

	for _, command := range commands {
		for _, source := range []string{"config", "flag"} {
			t.Run(command.name+"/"+source, func(t *testing.T) {
				t.Setenv("REEVE_COMMENT_AUTHOR", "")
				wantAuthor := "cfg-author[bot]"
				args := append([]string{}, command.args...)
				args = append(args, "--repo=org/repo", "--token=test-token", "--root="+root)
				if source == "flag" {
					wantAuthor = "flag-author[bot]"
					args = append(args, "--comment-author="+wantAuthor)
				}
				cmd := command.new()
				if err := cmd.Flags().Parse(args); err != nil {
					t.Fatal(err)
				}

				original := gitHubNew
				defer func() { gitHubNew = original }()
				var gotAuthor string
				gitHubNew = func(_ context.Context, token, owner, repo string) (*gh.Client, error) {
					gotAuthor = os.Getenv("REEVE_COMMENT_AUTHOR")
					if token != "test-token" || owner != "org" || repo != "repo" {
						t.Fatalf("client identity = (%q, %q, %q)", token, owner, repo)
					}
					return nil, errStopAtGitHubClient
				}

				if err := command.run(cmd); !errors.Is(err, errStopAtGitHubClient) {
					t.Fatalf("command did not reach the client constructor: %v", err)
				}
				if gotAuthor != wantAuthor {
					t.Fatalf("client comment author = %q, want %q", gotAuthor, wantAuthor)
				}
			})
		}
	}
}
