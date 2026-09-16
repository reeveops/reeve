package run

import (
	"context"
	"log/slog"

	"github.com/reeveops/reeve/internal/vcs"
)

// prHeadReader is the minimal VCS surface needed to resolve the PR head SHA.
type prHeadReader interface {
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
}

// resolvePR returns the PR snapshot and uses its head SHA when available.
// The snapshot is returned so the caller can reuse its title and author
// without mixing metadata from a second API read later in the invocation.
//
// On pull_request events $GITHUB_SHA is the ephemeral merge commit, not the
// branch tip. Apply and preview must key manifests to the same SHA, so both
// call this before doing any SHA-sensitive work.
func resolvePR(ctx context.Context, v prHeadReader, prNumber int, sha string) (string, *vcs.PR) {
	if v == nil || prNumber == 0 {
		return sha, nil
	}
	pr, err := v.GetPR(ctx, prNumber)
	if err != nil || pr == nil {
		return sha, nil
	}
	if pr.HeadSHA == "" || pr.HeadSHA == sha {
		return sha, pr
	}
	slog.Info("commit sha overridden from PR head",
		"env_sha", sha, "pr_head_sha", pr.HeadSHA, "pr", prNumber)
	return pr.HeadSHA, pr
}
