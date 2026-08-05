package github

import (
	"context"
	"strings"

	gh "github.com/google/go-github/v66/github"
)

// Issue operations. These satisfy internal/notify.IssueClient (the
// consumer-defined surface of the github_issue channel) so the channel never
// imports go-github directly.

// FindIssueByMarker returns the number of the first open issue whose body
// contains marker, or found=false if none.
func (c *Client) FindIssueByMarker(ctx context.Context, marker string) (int, bool, error) {
	opt := &gh.IssueListByRepoOptions{State: "open", ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		issues, resp, err := c.gh.Issues.ListByRepo(ctx, c.owner, c.repo, opt)
		if err != nil {
			return 0, false, err
		}
		for _, i := range issues {
			if strings.Contains(i.GetBody(), marker) {
				return i.GetNumber(), true, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return 0, false, nil
}

// CreateIssue opens a new issue and returns its number.
func (c *Client) CreateIssue(ctx context.Context, title, body string, labels, assignees []string) (int, error) {
	issue, _, err := c.gh.Issues.Create(ctx, c.owner, c.repo, &gh.IssueRequest{
		Title:     gh.String(title),
		Body:      gh.String(body),
		Labels:    &labels,
		Assignees: &assignees,
	})
	if err != nil {
		return 0, err
	}
	return issue.GetNumber(), nil
}

// UpdateIssue rewrites an existing issue's title and body.
func (c *Client) UpdateIssue(ctx context.Context, number int, title, body string) error {
	_, _, err := c.gh.Issues.Edit(ctx, c.owner, c.repo, number, &gh.IssueRequest{
		Title: gh.String(title),
		Body:  gh.String(body),
	})
	return err
}

// CloseIssue closes an issue, rewriting its body.
func (c *Client) CloseIssue(ctx context.Context, number int, body string) error {
	_, _, err := c.gh.Issues.Edit(ctx, c.owner, c.repo, number, &gh.IssueRequest{
		State: gh.String("closed"),
		Body:  gh.String(body),
	})
	return err
}
