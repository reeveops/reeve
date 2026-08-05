package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v66/github"

	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/vcs"
)

// ListApprovals returns the reviewers whose current stance is APPROVED.
// GitHub keeps every historical review, so a reviewer who approves and later
// requests changes still has an APPROVED record on the PR; counting it would
// let a withdrawn approval satisfy the gate. Only a reviewer's most recent
// decisive review counts - see latestApprovals. Submission time and commit
// SHA are preserved so dismiss_on_new_commit can be evaluated.
func (c *Client) ListApprovals(ctx context.Context, pr approvals.PR) ([]approvals.Approval, error) {
	var revs []*gh.PullRequestReview
	opt := &gh.ListOptions{PerPage: 100}
	for {
		page, resp, err := c.gh.PullRequests.ListReviews(ctx, c.owner, c.repo, pr.Number, opt)
		if err != nil {
			return nil, err
		}
		revs = append(revs, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return latestApprovals(revs), nil
}

// latestApprovals reduces a chronologically-ordered review list (GitHub
// returns reviews oldest-first) to the reviewers whose most recent decisive
// review is APPROVED. APPROVED / CHANGES_REQUESTED / DISMISSED are decisive;
// COMMENTED and PENDING reviews do not change a reviewer's stance and are
// ignored. Later reviews by the same user supersede earlier ones.
func latestApprovals(revs []*gh.PullRequestReview) []approvals.Approval {
	type entry struct {
		state string
		appr  approvals.Approval
	}
	latest := map[string]*entry{}
	var order []string
	for _, r := range revs {
		state := r.GetState()
		if state == "COMMENTED" || state == "PENDING" {
			continue
		}
		login := r.GetUser().GetLogin()
		if login == "" {
			continue
		}
		if _, ok := latest[login]; !ok {
			order = append(order, login)
		}
		latest[login] = &entry{
			state: state,
			appr: approvals.Approval{
				Source:      "pr_review",
				Approver:    login,
				SubmittedAt: r.GetSubmittedAt().Time,
				CommitSHA:   r.GetCommitID(),
				// GitHub records the review's commit_id authoritatively, so a
				// review is always pinned to the commit it approved.
				Pinned: true,
			},
		}
	}
	var out []approvals.Approval
	for _, login := range order {
		if latest[login].state == "APPROVED" {
			out = append(out, latest[login].appr)
		}
	}
	return out
}

func (*Client) Name() string { return "pr_review" }

// maxOverlapScanPRs caps how many open PRs the overlap scan inspects.
// Each inspected PR costs a per-PR file listing (GitHub has no batch
// endpoint for this), so an unbounded scan on a busy monorepo is an API
// budget hazard. PRs beyond the cap (newest first, GitHub's default list
// order) are reported as unchecked rather than silently skipped.
const maxOverlapScanPRs = 100

// ListOpenPRsTouchingPaths powers drift-overlap surfacing and "blocked by
// PR #X" queries. Lists open PRs, then intersects each PR's changed files
// with paths.
//
// The scan degrades, never lies: a PR whose file list cannot be fetched
// (or that falls beyond maxOverlapScanPRs) is reported in the returned
// *approvals.OverlapScanError alongside the PARTIAL result set - callers
// surface "could not check PR #N" instead of treating a failed fetch as
// "no overlap".
func (c *Client) ListOpenPRsTouchingPaths(ctx context.Context, paths []string) ([]approvals.PR, error) {
	var prs []*gh.PullRequest
	var moreBeyondCap bool
	opt := &gh.PullRequestListOptions{State: "open", ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		page, resp, err := c.gh.PullRequests.List(ctx, c.owner, c.repo, opt)
		if err != nil {
			return nil, err
		}
		prs = append(prs, page...)
		if len(prs) >= maxOverlapScanPRs {
			// Stopped at the cap. If GitHub has another page, or this page
			// overshot the cap, there are open PRs we will not scan - record
			// that so the caller can warn instead of implying "no overlap".
			moreBeyondCap = resp.NextPage != 0 || len(prs) > maxOverlapScanPRs
			break
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	var unchecked []int
	if len(prs) > maxOverlapScanPRs {
		for _, p := range prs[maxOverlapScanPRs:] {
			unchecked = append(unchecked, p.GetNumber())
		}
		prs = prs[:maxOverlapScanPRs]
	}

	wanted := make(map[string]bool, len(paths))
	for _, p := range paths {
		wanted[p] = true
	}

	var out []approvals.PR
	var firstErr error
	for _, p := range prs {
		files, err := c.listFilesStrings(ctx, p.GetNumber())
		if err != nil {
			// A failed file fetch must not become "this PR doesn't overlap".
			unchecked = append(unchecked, p.GetNumber())
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		hit := false
		for _, f := range files {
			if wanted[f] || anyPrefixIn(f, paths) {
				hit = true
				break
			}
		}
		if hit {
			out = append(out, approvals.PR{
				Number:   p.GetNumber(),
				HeadSHA:  p.GetHead().GetSHA(),
				Author:   p.GetUser().GetLogin(),
				BaseRef:  p.GetBase().GetRef(),
				OpenedAt: p.GetCreatedAt().Time,
				Changed:  files,
			})
		}
	}
	if len(unchecked) > 0 || moreBeyondCap {
		return out, &approvals.OverlapScanError{Unchecked: unchecked, MoreBeyondCap: moreBeyondCap, Err: firstErr}
	}
	return out, nil
}

func (c *Client) listFilesStrings(ctx context.Context, pr int) ([]string, error) {
	return c.ListChangedFiles(ctx, pr)
}

func anyPrefixIn(file string, paths []string) bool {
	for _, p := range paths {
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// ChecksGreen reports whether required status checks are passing for a
// commit. See vcs.ChecksGreenOpts for self-skip semantics.
func (c *Client) ChecksGreen(ctx context.Context, sha string, opts vcs.ChecksGreenOpts) (bool, []string, error) {
	if sha == "" {
		return false, nil, errors.New("sha required")
	}
	logger := slog.With("op", "checks_green", "sha", shortSHA(sha))

	ignoreURLFragment := ""
	if opts.IgnoreRunID > 0 {
		ignoreURLFragment = fmt.Sprintf("/runs/%d/", opts.IgnoreRunID)
	}
	ignoreNames := make(map[string]struct{}, len(opts.IgnoreNames))
	for _, n := range opts.IgnoreNames {
		if n == "" {
			continue
		}
		ignoreNames[n] = struct{}{}
	}

	var failing []string
	checkOpt := &gh.ListCheckRunsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		runs, resp, err := c.gh.Checks.ListCheckRunsForRef(ctx, c.owner, c.repo, sha, checkOpt)
		if err != nil {
			return false, nil, fmt.Errorf("list check runs: %w", err)
		}
		for _, r := range runs.CheckRuns {
			name, status, conclusion, url := r.GetName(), r.GetStatus(), r.GetConclusion(), r.GetDetailsURL()
			logger.Debug("check_run inspected",
				"name", name, "status", status, "conclusion", conclusion, "url", url)

			// Skip the current workflow run - it cannot be green while running.
			if ignoreURLFragment != "" && strings.Contains(url, ignoreURLFragment) {
				logger.Debug("check_run skipped: current run", "name", name)
				continue
			}
			// Skip reeve's own check_runs from prior workflow runs on this
			// SHA. Without this, a single failed apply pins the gate red
			// forever because the failed check_run lives on the SHA, not the
			// run.
			if _, self := ignoreNames[name]; self {
				logger.Debug("check_run skipped: self by name", "name", name)
				continue
			}
			// A check that hasn't concluded is pending, not passing. GitHub's
			// own required-checks gate blocks on pending; skipping these let
			// an apply pass while a failing-destined check was still running
			// (reeve's own current run is already skipped above).
			if status != "completed" {
				logger.Debug("check_run pending: not yet concluded", "name", name, "status", status)
				failing = append(failing, name+":"+status+" (still running)")
				continue
			}
			switch conclusion {
			case "success", "skipped", "neutral":
				continue
			case "":
				failing = append(failing, name+":pending (still running)")
			default:
				failing = append(failing, name+":"+conclusion)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		checkOpt.Page = resp.NextPage
	}

	// Commit statuses (legacy, separate from check runs). A combined state of
	// "pending" must block just like a non-completed check_run: a status that
	// hasn't reported yet is a check still running, not a passing one.
	// GitHub's own required-checks gate blocks on pending statuses too. The
	// one subtlety: GitHub reports "pending" with an EMPTY status list when a
	// commit has no statuses at all, so an empty list never blocks.
	var state string
	var statuses []*gh.RepoStatus
	stOpt := &gh.ListOptions{PerPage: 100}
	for {
		st, resp, err := c.gh.Repositories.GetCombinedStatus(ctx, c.owner, c.repo, sha, stOpt)
		if err != nil {
			return false, nil, fmt.Errorf("combined status: %w", err)
		}
		state = st.GetState()
		statuses = append(statuses, st.Statuses...)
		if resp.NextPage == 0 {
			break
		}
		stOpt.Page = resp.NextPage
	}
	logger.Debug("combined_status", "state", state, "n", len(statuses))
	if (state == "failure" || state == "error" || state == "pending") && len(statuses) > 0 {
		before := len(failing)
		for _, s := range statuses {
			switch s.GetState() {
			case "success":
			case "pending":
				failing = append(failing, s.GetContext()+":pending (checks still running)")
			default:
				failing = append(failing, s.GetContext()+":"+s.GetState())
			}
		}
		if len(failing) == before {
			// Non-success combined state but every enumerated status looked
			// fine (statuses are fully paginated, so this should not happen).
			// Fail closed with the combined verdict rather than passing.
			failing = append(failing, "combined status:"+state)
		}
	}

	green := len(failing) == 0
	logger.Debug("verdict", "green", green, "failing", failing)
	return green, failing, nil
}

func shortSHA(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

// CompareBranches reports how many commits HEAD is behind base.
func (c *Client) CompareBranches(ctx context.Context, base, head string) (int, error) {
	cmp, _, err := c.gh.Repositories.CompareCommits(ctx, c.owner, c.repo, base, head, &gh.ListOptions{PerPage: 1})
	if err != nil {
		return 0, err
	}
	// BehindBy: commits base has that head doesn't.
	return cmp.GetBehindBy(), nil
}

// FetchCodeowners returns the raw CODEOWNERS file contents from the
// repo's default branch. Returns "" and nil error if absent. Only a 404
// is treated as absence - auth/transport/rate-limit errors propagate so
// the codeowners gate fails closed instead of silently evaluating with no
// owners.
func (c *Client) FetchCodeowners(ctx context.Context) (string, error) {
	for _, path := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		f, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, path, nil)
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				continue
			}
			return "", fmt.Errorf("get %s: %w", path, err)
		}
		if f == nil {
			continue
		}
		decoded, err := f.GetContent()
		if err != nil {
			return "", err
		}
		return decoded, nil
	}
	return "", nil
}

// ListTeamMembers expands a team slug "org/team" to member logins. Used
// by approval rules when the allow-list is a team rather than individuals.
func (c *Client) ListTeamMembers(ctx context.Context, slug string) ([]string, error) {
	slug = strings.TrimPrefix(slug, "@")
	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 {
		return nil, errors.New("team slug must be org/team")
	}
	org, team := parts[0], parts[1]
	opt := &gh.TeamListTeamMembersOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	var out []string
	for {
		users, resp, err := c.gh.Teams.ListTeamMembersBySlug(ctx, org, team, opt)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			out = append(out, u.GetLogin())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}
