// Package github implements the VCS adapter for GitHub. Phase 1 scope:
// read PR metadata, list changed files, upsert a marker-identified PR
// comment. ListReviews / checks / CODEOWNERS land in Phase 2.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	gh "github.com/google/go-github/v66/github"
	"golang.org/x/oauth2"

	"github.com/reeveops/reeve/internal/vcs"
)

// Client wraps a go-github Client. Authentication is via personal access
// token / GITHUB_TOKEN. GitHub App auth lands in Phase 4.
type Client struct {
	gh    *gh.Client
	owner string
	repo  string

	commentMu    sync.Mutex
	commentCache map[int][]*gh.IssueComment
}

// publicAPIURL is github.com's REST endpoint - the default when
// GITHUB_API_URL is unset (or points at github.com, as Actions sets it).
const publicAPIURL = "https://api.github.com"

// New returns a Client. If token is empty, an unauthenticated client is
// returned - useful only for public-repo reads in tests.
//
// The API base URL honors GITHUB_API_URL (set by the Actions runner on both
// github.com and GitHub Enterprise Server), so GHES installs work without
// extra config. All requests go through a rate-limit-aware transport - see
// retryTransport.
func New(ctx context.Context, token, owner, repo string) (*Client, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("github: owner and repo required")
	}
	var httpClient *http.Client
	if token != "" {
		src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient = oauth2.NewClient(ctx, src)
		// Wrap OUTSIDE oauth2 so each retry re-passes through auth.
		httpClient.Transport = newRetryTransport(httpClient.Transport)
	} else {
		httpClient = &http.Client{Transport: newRetryTransport(nil)}
	}
	ghc := gh.NewClient(httpClient)
	if base := apiBaseURL(); base != "" {
		var err error
		// Upload URL is unused by reeve; passing the API base keeps the
		// client consistent if an upload path ever appears.
		ghc, err = ghc.WithEnterpriseURLs(base, base)
		if err != nil {
			return nil, fmt.Errorf("github: invalid GITHUB_API_URL %q: %w", os.Getenv("GITHUB_API_URL"), err)
		}
	}
	return &Client{gh: ghc, owner: owner, repo: repo}, nil
}

// apiBaseURL returns the non-default API base URL from GITHUB_API_URL, or
// "" when the public github.com endpoint applies.
func apiBaseURL() string {
	u := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_API_URL")), "/")
	if u == "" || u == publicAPIURL {
		return ""
	}
	return u
}

// Capabilities returns GitHub's supported comment features.
func (c *Client) Capabilities() vcs.CommentCapabilities {
	return vcs.CommentCapabilities{SupportsEdit: true}
}

// GetPR returns the normalized PR shape.
func (c *Client) GetPR(ctx context.Context, number int) (*vcs.PR, error) {
	pr, _, err := c.gh.PullRequests.Get(ctx, c.owner, c.repo, number)
	if err != nil {
		return nil, err
	}
	out := &vcs.PR{
		Number:   pr.GetNumber(),
		HeadSHA:  pr.GetHead().GetSHA(),
		BaseRef:  pr.GetBase().GetRef(),
		Title:    pr.GetTitle(),
		Author:   pr.GetUser().GetLogin(),
		IsDraft:  pr.GetDraft(),
		OpenedAt: pr.GetCreatedAt().Format("2006-01-02T15:04:05Z"),
		URL:      pr.GetHTMLURL(),
	}
	// IsFork: head repo full name differs from base repo full name.
	if pr.GetHead() != nil && pr.GetHead().GetRepo() != nil && pr.GetBase() != nil && pr.GetBase().GetRepo() != nil {
		out.IsFork = pr.GetHead().GetRepo().GetFullName() != pr.GetBase().GetRepo().GetFullName()
	}
	// RepoPrivate: the base repo's visibility gates the approvals safety
	// default. GetPrivate() is false for public (and unset) repos, which is
	// the fail-closed (stricter) direction.
	if pr.GetBase() != nil && pr.GetBase().GetRepo() != nil {
		out.RepoPrivate = pr.GetBase().GetRepo().GetPrivate()
	}
	return out, nil
}

// ListChangedFiles returns the file paths changed in the PR.
func (c *Client) ListChangedFiles(ctx context.Context, number int) ([]string, error) {
	var out []string
	opt := &gh.ListOptions{PerPage: 100}
	for {
		files, resp, err := c.gh.PullRequests.ListFiles(ctx, c.owner, c.repo, number, opt)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.GetFilename() != "" {
				out = append(out, f.GetFilename())
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// PostComment always creates a new PR comment.
func (c *Client) PostComment(ctx context.Context, number int, body string) error {
	c.commentMu.Lock()
	defer c.commentMu.Unlock()
	created, _, err := c.gh.Issues.CreateComment(ctx, c.owner, c.repo, number, &gh.IssueComment{Body: gh.String(body)})
	if err != nil {
		return err
	}
	if _, cached := c.commentCache[number]; !cached {
		return nil
	}
	if created == nil || created.GetID() == 0 {
		delete(c.commentCache, number)
		return nil
	}
	c.commentCache[number] = append(c.commentCache[number], cloneIssueComment(created))
	return nil
}

// UpsertComment finds reeve's existing PR comment by marker substring and
// edits it; creates a new one if none exists.
func (c *Client) UpsertComment(ctx context.Context, number int, body, marker string) error {
	if marker == "" {
		return errors.New("marker is required")
	}
	c.commentMu.Lock()
	defer c.commentMu.Unlock()
	return c.upsertCommentLocked(ctx, number, body, marker, true)
}

func (c *Client) upsertCommentLocked(ctx context.Context, number int, body, marker string, refreshOnNotFound bool) error {
	comments, err := c.issueCommentsLocked(ctx, number, false)
	if err != nil {
		return err
	}
	for _, cm := range comments {
		if !strings.Contains(cm.GetBody(), marker) {
			continue
		}
		edited, resp, err := c.gh.Issues.EditComment(ctx, c.owner, c.repo, cm.GetID(), &gh.IssueComment{Body: gh.String(body)})
		if err == nil {
			c.updateCachedCommentLocked(number, cm.GetID(), body, edited)
			return nil
		}
		if refreshOnNotFound && resp != nil && resp.StatusCode == http.StatusNotFound {
			if _, err := c.issueCommentsLocked(ctx, number, true); err != nil {
				return err
			}
			return c.upsertCommentLocked(ctx, number, body, marker, false)
		}
		return err
	}
	created, _, err := c.gh.Issues.CreateComment(ctx, c.owner, c.repo, number, &gh.IssueComment{Body: gh.String(body)})
	if err != nil {
		return err
	}
	if created == nil || created.GetID() == 0 {
		delete(c.commentCache, number)
		return nil
	}
	c.commentCache[number] = append(c.commentCache[number], cloneIssueComment(created))
	return nil
}

// issueCommentsLocked returns one cached, paginated comment snapshot per PR.
// The caller must hold commentMu.
func (c *Client) issueCommentsLocked(ctx context.Context, number int, refresh bool) ([]*gh.IssueComment, error) {
	if !refresh && c.commentCache != nil {
		if comments, ok := c.commentCache[number]; ok {
			return cloneIssueComments(comments), nil
		}
	}
	var out []*gh.IssueComment
	opt := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := c.gh.Issues.ListComments(ctx, c.owner, c.repo, number, opt)
		if err != nil {
			return nil, fmt.Errorf("list comments: %w", err)
		}
		out = append(out, comments...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	if c.commentCache == nil {
		c.commentCache = make(map[int][]*gh.IssueComment)
	}
	c.commentCache[number] = cloneIssueComments(out)
	return cloneIssueComments(out), nil
}

// DeleteCommentsByMarkerPrefix deletes the PR comments whose body carries a
// marker starting with prefix and whose part ordinal is above keepThrough.
//
// A board that shrinks - fewer stacks, so fewer comments - must remove the parts
// it no longer writes. A stale part left behind keeps claiming stacks the
// current run does not have, and nothing else would ever touch it: markers are
// found by match, and a marker nobody writes is a marker nobody edits.
func (c *Client) DeleteCommentsByMarkerPrefix(ctx context.Context, number int, prefix string, keepThrough int) (int, error) {
	if prefix == "" {
		return 0, errors.New("marker prefix is required")
	}
	author, err := c.authenticatedCommentAuthor(ctx)
	if err != nil {
		return 0, err
	}
	c.commentMu.Lock()
	defer c.commentMu.Unlock()
	var stale []int64
	comments, err := c.issueCommentsLocked(ctx, number, false)
	if err != nil {
		return 0, err
	}
	for _, cm := range comments {
		if !strings.EqualFold(cm.GetUser().GetLogin(), author) {
			continue
		}
		part, ok := partOrdinal(cm.GetBody(), prefix)
		if ok && part > keepThrough {
			stale = append(stale, cm.GetID())
		}
	}
	deleted := 0
	for _, id := range stale {
		if _, err := c.gh.Issues.DeleteComment(ctx, c.owner, c.repo, id); err != nil {
			// Report what did get removed alongside the failure: a partial
			// sweep still narrows the stale set, and the caller logs rather
			// than failing the run over it.
			return deleted, fmt.Errorf("delete comment %d: %w", id, err)
		}
		c.removeCachedCommentLocked(number, id)
		deleted++
	}
	return deleted, nil
}

func (c *Client) removeCachedCommentLocked(number int, id int64) {
	comments := c.commentCache[number]
	for i, cm := range comments {
		if cm.GetID() == id {
			updated := make([]*gh.IssueComment, 0, len(comments)-1)
			updated = append(updated, comments[:i]...)
			updated = append(updated, comments[i+1:]...)
			c.commentCache[number] = updated
			return
		}
	}
}

func (c *Client) updateCachedCommentLocked(number int, id int64, body string, edited *gh.IssueComment) {
	comments, ok := c.commentCache[number]
	if !ok {
		return
	}
	for i, cm := range comments {
		if cm.GetID() != id {
			continue
		}
		updated := cloneIssueComment(cm)
		updated.Body = gh.String(body)
		if edited != nil && edited.User != nil {
			updated.User = edited.User
		}
		comments[i] = updated
		return
	}
}

func cloneIssueComments(comments []*gh.IssueComment) []*gh.IssueComment {
	cloned := make([]*gh.IssueComment, len(comments))
	for i, comment := range comments {
		cloned[i] = cloneIssueComment(comment)
	}
	return cloned
}

func cloneIssueComment(comment *gh.IssueComment) *gh.IssueComment {
	if comment == nil {
		return nil
	}
	cloned := *comment
	return &cloned
}

// authenticatedCommentAuthor identifies the account whose token writes reeve
// comments. User tokens expose it through /user; GitHub Actions installation
// tokens write as the platform's fixed github-actions[bot] identity.
func (c *Client) authenticatedCommentAuthor(ctx context.Context) (string, error) {
	user, _, err := c.gh.Users.Get(ctx, "")
	if err == nil && user.GetLogin() != "" {
		return user.GetLogin(), nil
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return "github-actions[bot]", nil
	}
	if err == nil {
		err = errors.New("authenticated user response had no login")
	}
	return "", fmt.Errorf("identify authenticated comment author: %w", err)
}

// partOrdinal extracts a board part's ordinal from a comment body, given the
// board's marker prefix. Part 1 carries the bare marker and reports 1.
func partOrdinal(body, prefix string) (int, bool) {
	i := strings.Index(body, prefix)
	if i < 0 {
		return 0, false
	}
	rest := body[i+len(prefix):]
	end := strings.Index(rest, "-->")
	if end < 0 {
		return 0, false
	}
	suffix := strings.TrimSpace(rest[:end])
	if suffix == "" {
		return 1, true // the bare board marker: part 1
	}
	var n int
	if _, err := fmt.Sscanf(suffix, ":part%d", &n); err != nil || n < 2 {
		return 0, false
	}
	return n, true
}
