package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/config/schemas"
	gh "github.com/reeveops/reeve/internal/vcs/github"
)

// gitHubNew is indirect so command wiring tests can stop at client
// construction and observe the exact author environment the real client gets.
var gitHubNew = gh.New

// newGitHubClient applies comment settings before constructing the client:
// gh.New captures REEVE_COMMENT_AUTHOR, so constructing it first would drop
// both comments.author and --comment-author.
func newGitHubClient(ctx context.Context, cmd *cobra.Command, token, repoFull string, shared *schemas.Shared) (*gh.Client, error) {
	if err := applyCommentFlags(cmd, shared); err != nil {
		return nil, err
	}
	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid --repo %q: want owner/name", repoFull)
	}
	return gitHubNew(ctx, token, parts[0], parts[1])
}
