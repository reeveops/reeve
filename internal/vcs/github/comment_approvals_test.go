package github

import (
	"testing"
	"time"

	gh "github.com/google/go-github/v66/github"

	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/vcs"
)

func issueComment(login, assoc, body, typ string, at time.Time) *gh.IssueComment {
	return &gh.IssueComment{
		User:              &gh.User{Login: gh.String(login), Type: gh.String(typ)},
		AuthorAssociation: gh.String(assoc),
		Body:              gh.String(body),
		CreatedAt:         &gh.Timestamp{Time: at},
	}
}

func defaultTrigger(t *testing.T) (prefixes []string, verb string, allowed map[string]struct{}) {
	t.Helper()
	cfg := vcs.CommentApprovalConfig{
		CommandPrefixes:     []string{"/reeve", "@acme-bot"},
		AllowedAssociations: []string{"OWNER", "MEMBER", "COLLABORATOR"},
	}
	p, v := parseCommentTrigger(cfg)
	return p, v, normalizeAssociations(cfg.AllowedAssociations)
}

const headSHA = "abcdef1234567890abcdef1234567890abcdef12"

func TestCommentApprovals_AuthorizedNonBotCounts(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("reviewer", "MEMBER", "/reeve approve", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("want 1 approval, got %d (%+v)", len(out), out)
	}
	if out[0].Approver != "reviewer" || out[0].Source != approvals.SourcePRComment {
		t.Fatalf("unexpected approval: %+v", out[0])
	}
	// A bare `/reeve approve` is unpinned: it carries no commit binding, so
	// dismiss_on_new_commit (evaluated downstream) can invalidate it.
	if out[0].Pinned {
		t.Fatalf("bare /reeve approve must be unpinned, got Pinned=true (%+v)", out[0])
	}
}

func TestCommentApprovals_ExplicitSHAPinsToHead(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		// Short SHA prefix of HEAD, mixed case, with trailing chatter.
		issueComment("reviewer", "MEMBER", "/reeve approve ABCDEF1 looks good", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("want 1 approval, got %+v", out)
	}
	if !out[0].Pinned {
		t.Fatalf("explicit SHA matching HEAD must be pinned, got %+v", out[0])
	}
	if out[0].CommitSHA != headSHA {
		t.Fatalf("pinned approval must carry the full HEAD SHA, got %q", out[0].CommitSHA)
	}
}

func TestCommentApprovals_ExplicitStaleSHAPinnedButNotHead(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("reviewer", "MEMBER", "/reeve approve deadbeefdeadbeef", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("want 1 approval, got %+v", out)
	}
	// Pinned to a SHA that is not HEAD: downstream dismiss_on_new_commit drops it.
	if !out[0].Pinned {
		t.Fatalf("explicit SHA must be pinned even when stale, got %+v", out[0])
	}
	if out[0].CommitSHA == headSHA {
		t.Fatalf("a non-HEAD SHA must not resolve to HEAD, got %q", out[0].CommitSHA)
	}
}

func TestCommentApprovals_ShortNonMatchingSHANotHead(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		// Below minShortSHA and not a HEAD prefix: bound to the literal token,
		// which cannot equal HEAD.
		issueComment("reviewer", "MEMBER", "/reeve approve zzz", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 || !out[0].Pinned || out[0].CommitSHA == headSHA {
		t.Fatalf("short non-matching SHA must be pinned and not HEAD, got %+v", out)
	}
}

func TestCommentApprovals_UnauthorizedAssociationRejected(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		// NONE / CONTRIBUTOR are not in the allowlist: must NOT count.
		issueComment("outsider", "NONE", "/reeve approve", "User", time.Unix(2000, 0)),
		issueComment("drive-by", "CONTRIBUTOR", "@acme-bot approve", "User", time.Unix(2001, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 0 {
		t.Fatalf("unauthorized commenters must not approve, got %+v", out)
	}
}

func TestCommentApprovals_BotRejected(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("reeve[bot]", "MEMBER", "/reeve approve", "Bot", time.Unix(2000, 0)),
		issueComment("github-actions[bot]", "MEMBER", "/reeve approve", "User", time.Unix(2001, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 0 {
		t.Fatalf("bot comments must not approve (self-trigger guard), got %+v", out)
	}
}

func TestCommentApprovals_NonCommandIgnored(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("a", "MEMBER", "looks good, approving", "User", time.Unix(2000, 0)),
		issueComment("b", "MEMBER", "/reeve preview", "User", time.Unix(2001, 0)),
		issueComment("c", "MEMBER", "please /reeve approve this", "User", time.Unix(2002, 0)), // prefix not first token
		issueComment("d", "MEMBER", "/reeve", "User", time.Unix(2003, 0)),                     // no verb
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 0 {
		t.Fatalf("only a first-token `<prefix> approve` counts, got %+v", out)
	}
}

func TestCommentApprovals_MentionPrefixAndCaseInsensitiveVerb(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("m", "OWNER", "@acme-bot Approve", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("@acme-bot Approve should count, got %+v", out)
	}
}

func TestCommentApprovals_DedupPerCommenter(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("reviewer", "MEMBER", "/reeve approve", "User", time.Unix(2000, 0)),
		issueComment("reviewer", "MEMBER", "/reeve approve", "User", time.Unix(3000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("same commenter approving twice must yield one approval, got %+v", out)
	}
}

// The re-approval scenario: a commenter approves an old SHA, a new commit
// lands, and they re-approve naming the new HEAD. The LATEST comment must win
// so its approval carries the current HEAD - otherwise dismiss_on_new_commit
// dismisses the stale one and the gate can never be re-satisfied by comment.
func TestCommentApprovals_LatestCommentWinsAfterNewCommit(t *testing.T) {
	prefixes, verb, allowed := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("reviewer", "MEMBER", "/reeve approve deadbeefdeadbeef", "User", time.Unix(2000, 0)), // stale SHA
		issueComment("reviewer", "MEMBER", "/reeve approve "+headSHA, "User", time.Unix(3000, 0)),         // current HEAD
	}
	out := commentApprovals(comments, prefixes, verb, allowed, headSHA)
	if len(out) != 1 {
		t.Fatalf("want one approval, got %+v", out)
	}
	if out[0].CommitSHA != headSHA {
		t.Fatalf("latest comment must win (bound to HEAD), got %q", out[0].CommitSHA)
	}
}

func TestCommentApprovals_EmptyAllowlistDeniesAll(t *testing.T) {
	prefixes, verb, _ := defaultTrigger(t)
	comments := []*gh.IssueComment{
		issueComment("owner", "OWNER", "/reeve approve", "User", time.Unix(2000, 0)),
	}
	out := commentApprovals(comments, prefixes, verb, map[string]struct{}{}, headSHA)
	if len(out) != 0 {
		t.Fatalf("empty allowlist must deny everything (fail closed), got %+v", out)
	}
}

func TestResolveApprovedCommit(t *testing.T) {
	cases := []struct {
		name       string
		arg        string
		head       string
		wantSHA    string
		wantPinned bool
	}{
		{"bare is unpinned", "", headSHA, "", false},
		{"head prefix pins to full head", "abcdef1234", headSHA, headSHA, true},
		{"mixed case prefix", "ABCDEF1", headSHA, headSHA, true},
		{"full head pins", headSHA, headSHA, headSHA, true},
		{"non-matching long sha is pinned-stale", "0000000deadbeef", headSHA, "0000000deadbeef", true},
		{"too-short prefix is not treated as head match", "abc", headSHA, "abc", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sha, pinned := resolveApprovedCommit(c.arg, c.head)
			if sha != c.wantSHA || pinned != c.wantPinned {
				t.Fatalf("resolveApprovedCommit(%q,%q) = (%q,%v), want (%q,%v)", c.arg, c.head, sha, pinned, c.wantSHA, c.wantPinned)
			}
		})
	}
}

// The zero-config fallback accepts the slash style only. "@reeve" is a real
// GitHub account, so a default that accepted it made every mention-style
// command notify a stranger who has nothing to do with the repo.
func TestParseCommentTrigger_DefaultsToSlashStyleOnly(t *testing.T) {
	prefixes, verb := parseCommentTrigger(vcs.CommentApprovalConfig{})
	if verb != "approve" {
		t.Fatalf("default verb = %q, want approve", verb)
	}
	if len(prefixes) != 1 || prefixes[0] != "/reeve" {
		t.Fatalf("default prefixes = %v, want [/reeve] only", prefixes)
	}
}

// Opting the mention style back in still works - the default changed, the
// capability did not.
func TestParseCommentTrigger_MentionStyleIsOptIn(t *testing.T) {
	prefixes, _ := parseCommentTrigger(vcs.CommentApprovalConfig{
		CommandPrefixes: []string{"/reeve", "@acme-bot"},
	})
	got := map[string]bool{}
	for _, p := range prefixes {
		got[p] = true
	}
	if !got["@acme-bot"] || !got["/reeve"] {
		t.Fatalf("configured prefixes must be honored, got %v", prefixes)
	}
}

func TestParseCommentTrigger_CustomCommand(t *testing.T) {
	cfg := vcs.CommentApprovalConfig{Command: "!bot ok", CommandPrefixes: []string{"/reeve"}}
	prefixes, verb := parseCommentTrigger(cfg)
	if verb != "ok" {
		t.Fatalf("verb from custom command = %q, want ok", verb)
	}
	got := map[string]bool{}
	for _, p := range prefixes {
		got[p] = true
	}
	if !got["!bot"] || !got["/reeve"] {
		t.Fatalf("prefixes should union command's first token and CommandPrefixes, got %v", prefixes)
	}
}
