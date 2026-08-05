package run

import (
	"context"
	"log/slog"

	"github.com/FynxLabs/reeve/internal/vcs"
)

// prHeadReader is the minimal VCS surface needed to resolve the PR head SHA.
type prHeadReader interface {
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
}

// resolvePRHeadSHA returns the PR head SHA if it differs from sha, logging
// when an override occurs. Returns sha unchanged if vcs is nil, prNumber is 0,
// or the PR head SHA is empty or equal to sha.
//
// On pull_request events $GITHUB_SHA is the ephemeral merge commit, not the
// branch tip. Apply and preview must key manifests to the same SHA, so both
// call this before doing any SHA-sensitive work.
func resolvePRHeadSHA(ctx context.Context, v prHeadReader, prNumber int, sha string) string {
	if v == nil || prNumber == 0 {
		return sha
	}
	pr, err := v.GetPR(ctx, prNumber)
	if err != nil || pr == nil || pr.HeadSHA == "" || pr.HeadSHA == sha {
		return sha
	}
	slog.Info("commit sha overridden from PR head",
		"env_sha", sha, "pr_head_sha", pr.HeadSHA, "pr", prNumber)
	return pr.HeadSHA
}
