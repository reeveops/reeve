package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/reeveops/reeve/internal/vcs"
)

var (
	// ErrExpectedPRHeadInvalid means the immutable checkout identity supplied
	// by the maintained action is not a full Git commit SHA.
	ErrExpectedPRHeadInvalid = errors.New("expected PR head must be a full lowercase commit SHA")
	// ErrPRHeadMismatch means the checked-out revision is no longer the PR head.
	ErrPRHeadMismatch = errors.New("checked-out revision does not match the current PR head")
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
func resolvePR(ctx context.Context, v prHeadReader, prNumber int, sha, expectedHead string) (string, *vcs.PR, error) {
	if err := validateExpectedPRHead(expectedHead); err != nil {
		return "", nil, err
	}
	if v == nil || prNumber == 0 {
		if expectedHead != "" {
			return "", nil, fmt.Errorf("%w: PR metadata is unavailable", ErrPRHeadMismatch)
		}
		return sha, nil, nil
	}
	pr, err := v.GetPR(ctx, prNumber)
	if err != nil || pr == nil {
		if expectedHead != "" {
			if err == nil {
				err = errors.New("empty PR response")
			}
			return "", nil, fmt.Errorf("verify expected PR head: %w", err)
		}
		return sha, nil, nil
	}
	if expectedHead != "" {
		if err := comparePRHead(pr, expectedHead); err != nil {
			return "", nil, err
		}
		return expectedHead, pr, nil
	}
	if pr.HeadSHA == "" || pr.HeadSHA == sha {
		return sha, pr, nil
	}
	slog.Info("commit sha overridden from PR head",
		"env_sha", sha, "pr_head_sha", pr.HeadSHA, "pr", prNumber)
	return pr.HeadSHA, pr, nil
}

// revalidatePRHead takes one fresh metadata snapshot at the state-change
// boundary. It is a no-op for direct callers that did not receive an immutable
// checkout identity from the maintained action.
func revalidatePRHead(ctx context.Context, v prHeadReader, prNumber int, expectedHead string) error {
	if expectedHead == "" {
		return nil
	}
	if err := validateExpectedPRHead(expectedHead); err != nil {
		return err
	}
	if v == nil || prNumber == 0 {
		return fmt.Errorf("%w: PR metadata is unavailable", ErrPRHeadMismatch)
	}
	pr, err := v.GetPR(ctx, prNumber)
	if err != nil {
		return fmt.Errorf("revalidate PR head: %w", err)
	}
	return comparePRHead(pr, expectedHead)
}

func comparePRHead(pr *vcs.PR, expectedHead string) error {
	if pr == nil || pr.HeadSHA == "" {
		return fmt.Errorf("%w: current head is unavailable", ErrPRHeadMismatch)
	}
	if pr.HeadSHA != expectedHead {
		return fmt.Errorf("%w: expected %s, current %s", ErrPRHeadMismatch, expectedHead, pr.HeadSHA)
	}
	return nil
}

// VerifyExpectedPRHead validates and compares an action-supplied immutable
// checkout identity with one PR metadata snapshot.
func VerifyExpectedPRHead(pr *vcs.PR, expectedHead string) error {
	if err := validateExpectedPRHead(expectedHead); err != nil {
		return err
	}
	if expectedHead == "" {
		return nil
	}
	return comparePRHead(pr, expectedHead)
}

func validateExpectedPRHead(sha string) error {
	if sha == "" {
		return nil
	}
	if len(sha) != 40 || sha != strings.ToLower(sha) || strings.TrimSpace(sha) != sha {
		return ErrExpectedPRHeadInvalid
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ErrExpectedPRHeadInvalid
		}
	}
	return nil
}
