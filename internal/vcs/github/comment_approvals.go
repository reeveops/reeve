package github

import (
	"context"
	"strings"

	gh "github.com/google/go-github/v66/github"

	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/vcs"
)

// ListCommentApprovals implements the opt-in pr_comment approval source: an
// authorized, non-bot commenter posting `/reeve approve` on the PR counts as an
// approval. It reads historical issue comments directly from the API, so it
// re-enforces the SAME author_association gate action.yml applies to command
// dispatch - nothing upstream vouches for a past comment. The non-author rule,
// freshness, and dismiss_on_new_commit are applied downstream by
// approvals.Evaluate, uniformly with pr_review approvals.
//
// Commit binding: a pr_review carries an authoritative commit_id from GitHub,
// so dismiss_on_new_commit can tell a stale review from a current one. A
// comment carries no such record - and the SHA that was HEAD when a comment was
// posted CANNOT be reconstructed from the API, because git committer timestamps
// are attacker-settable (a pushed commit can be backdated to appear "older"
// than the comment). So a comment approval is bound to a commit only when the
// commenter names it explicitly: `/reeve approve <sha>`. A bare `/reeve approve`
// is left unpinned, and approvals.Evaluate dismisses unpinned approvals whenever
// dismiss_on_new_commit is enabled (its default).
func (c *Client) ListCommentApprovals(ctx context.Context, pr approvals.PR, cfg vcs.CommentApprovalConfig) ([]approvals.Approval, error) {
	prefixes, verb := parseCommentTrigger(cfg)
	allowed := normalizeAssociations(cfg.AllowedAssociations)

	comments, err := c.listIssueComments(ctx, pr.Number)
	if err != nil {
		return nil, err
	}

	return commentApprovals(comments, prefixes, verb, allowed, pr.HeadSHA), nil
}

// commentApprovals is the pure core of the pr_comment source: it turns issue
// comments into approvals, applying the prefix/verb parse, the bot guard, the
// author_association gate, and the explicit-SHA commit binding. Kept
// side-effect-free so it can be tested without an HTTP mock. The non-author
// rule, freshness, and dismissal are left to approvals.Evaluate.
func commentApprovals(comments []*gh.IssueComment, prefixes []string, verb string, allowed map[string]struct{}, headSHA string) []approvals.Approval {
	var out []approvals.Approval
	// One approval per commenter, keeping the LATEST qualifying comment.
	// Comments arrive oldest-first; if we kept the first, a re-approval naming
	// a newer SHA would be discarded in favor of an earlier one, which
	// dismiss_on_new_commit would then invalidate. Overwriting in place keeps
	// output order stable at first appearance while carrying the latest
	// comment's timestamp + commit binding.
	idx := map[string]int{} // login -> position in out
	for _, cm := range comments {
		login := cm.GetUser().GetLogin()
		if login == "" {
			continue
		}
		// Self-trigger guard: never treat reeve's own (or any bot's) comment as
		// an approval. Mirrors the action.yml dispatch guard.
		if cm.GetUser().GetType() == "Bot" || strings.HasSuffix(login, "[bot]") {
			continue
		}
		ok, shaArg := parseApproveCommand(cm.GetBody(), prefixes, verb)
		if !ok {
			continue
		}
		// Authorization gate: identical to the action.yml command gate. An
		// unauthorized commenter's `/reeve approve` MUST NOT count.
		if !associationAllowed(cm.GetAuthorAssociation(), allowed) {
			continue
		}
		sha, pinned := resolveApprovedCommit(shaArg, headSHA)
		ap := approvals.Approval{
			Source:      approvals.SourcePRComment,
			Approver:    login,
			SubmittedAt: cm.GetCreatedAt().Time,
			CommitSHA:   sha,
			Pinned:      pinned,
		}
		if i, ok := idx[login]; ok {
			out[i] = ap // later comment supersedes the earlier one
			continue
		}
		idx[login] = len(out)
		out = append(out, ap)
	}
	return out
}

// minShortSHA is the shortest commit prefix accepted in `/reeve approve <sha>`.
// Matches git's conventional abbreviated-SHA length and stops a 1-2 character
// argument from matching HEAD by accident.
const minShortSHA = 7

// resolveApprovedCommit binds a comment approval to the commit it names.
//
// A bare `/reeve approve` (arg == "") is UNPINNED: reeve cannot know, from the
// comment alone, which commit was HEAD when it was posted, so it must not be
// trusted to identify approved code. approvals.Evaluate dismisses an unpinned
// approval whenever dismiss_on_new_commit is enabled.
//
// `/reeve approve <sha>` is PINNED: if <sha> is a prefix (>= minShortSHA chars)
// of the current HEAD it binds to the full HEAD SHA and counts; otherwise it
// binds to the literal <sha>, which never equals HEAD and is dismissed as stale.
func resolveApprovedCommit(arg, headSHA string) (sha string, pinned bool) {
	if arg == "" {
		return "", false
	}
	if len(arg) >= minShortSHA && strings.HasPrefix(strings.ToLower(headSHA), strings.ToLower(arg)) {
		return headSHA, true
	}
	return arg, true
}

// listIssueComments returns all issue comments on the PR (paginated).
func (c *Client) listIssueComments(ctx context.Context, number int) ([]*gh.IssueComment, error) {
	var out []*gh.IssueComment
	opt := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		page, resp, err := c.gh.Issues.ListComments(ctx, c.owner, c.repo, number, opt)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// parseCommentTrigger derives the accepted command prefixes and the verb from
// the source config. The verb is the second token of cfg.Command (default
// "/reeve approve" -> verb "approve"); the first token of cfg.Command is added
// to the configured prefix list.
func parseCommentTrigger(cfg vcs.CommentApprovalConfig) (prefixes []string, verb string) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = approvals.DefaultCommentCommand
	}
	fields := strings.Fields(command)
	verb = "approve"
	if len(fields) >= 2 {
		verb = strings.ToLower(fields[1])
	}
	set := map[string]struct{}{}
	if len(fields) >= 1 {
		set[fields[0]] = struct{}{}
	}
	for _, p := range cfg.CommandPrefixes {
		p = strings.TrimSpace(p)
		if p != "" {
			set[p] = struct{}{}
		}
	}
	if len(set) == 0 {
		// Slash style only. "@reeve" is a real GitHub account, so accepting it
		// by default made every mention-style command notify a stranger.
		set["/reeve"] = struct{}{}
	}
	for p := range set {
		prefixes = append(prefixes, p)
	}
	return prefixes, verb
}

// parseApproveCommand reports whether the first line of a comment body is a
// `<prefix> <verb> [sha]` approval command, and returns the optional third
// token (the SHA the commenter is approving). Parsed the same way action.yml
// parses commands: the first whitespace-delimited token must exactly match an
// accepted prefix and the second token must be the approve verb. A third token,
// if present, is taken as the approved commit SHA; further tokens are ignored.
func parseApproveCommand(body string, prefixes []string, verb string) (ok bool, sha string) {
	line := body
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return false, ""
	}
	prefixOK := false
	for _, p := range prefixes {
		if fields[0] == p {
			prefixOK = true
			break
		}
	}
	if !prefixOK || !strings.EqualFold(fields[1], verb) {
		return false, ""
	}
	if len(fields) >= 3 {
		return true, fields[2]
	}
	return true, ""
}

// normalizeAssociations upper-cases and trims the allowlist for comparison.
func normalizeAssociations(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, a := range in {
		a = strings.ToUpper(strings.TrimSpace(a))
		if a != "" {
			out[a] = struct{}{}
		}
	}
	return out
}

// associationAllowed reports whether a comment's author_association is in the
// allowlist. An empty allowlist denies everything (fail closed): if no
// association is authorized, no comment can approve.
func associationAllowed(assoc string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[strings.ToUpper(strings.TrimSpace(assoc))]
	return ok
}
