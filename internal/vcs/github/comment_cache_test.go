package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v66/github"
)

// withSelfIdentity points the fake client's identity endpoint at a known
// login, matching what every marker comment fixture below is authored by.
func withSelfIdentity(mux *http.ServeMux) {
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fmt.Fprint(w, `{"login":"reeve-bot"}`)
	})
}

func TestUpsertCommentReusesOneSnapshotAcrossMarkers(t *testing.T) {
	var lists, edits int
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		lists++
		fmt.Fprint(w, `[
			{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:one -->"},
			{"id":2,"user":{"login":"reeve-bot"},"body":"<!-- marker:two -->"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		edits++
		var in struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/repos/o/r/issues/comments/"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "body": in.Body})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	for _, tc := range []struct {
		body   string
		marker string
	}{
		{"<!-- marker:one --> first", "<!-- marker:one -->"},
		{"<!-- marker:one --> second", "<!-- marker:one -->"},
		{"<!-- marker:two --> third", "<!-- marker:two -->"},
	} {
		if err := c.UpsertComment(context.Background(), 12, tc.body, tc.marker); err != nil {
			t.Fatalf("UpsertComment(%s): %v", tc.marker, err)
		}
	}
	c.commentMu.Lock()
	comments, err := c.issueCommentsLocked(context.Background(), 12, false)
	c.commentMu.Unlock()
	if err != nil || len(comments) != 2 {
		t.Fatalf("listIssueComments = (%d comments, %v), want (2, nil)", len(comments), err)
	}
	if lists != 1 || edits != 3 {
		t.Fatalf("list requests = %d, edits = %d; want 1 and 3", lists, edits)
	}
}

func TestCommentApprovalReadRefreshesSnapshot(t *testing.T) {
	var lists int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		lists++
		fmt.Fprintf(w, `[{"id":%d,"body":"version-%d"}]`, lists, lists)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	c.commentMu.Lock()
	first, err := c.issueCommentsLocked(context.Background(), 12, false)
	c.commentMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.listIssueComments(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if lists != 2 || first[0].GetBody() != "version-1" || second[0].GetBody() != "version-2" {
		t.Fatalf("lists=%d first=%q second=%q, want fresh second snapshot", lists, first[0].GetBody(), second[0].GetBody())
	}
}

func TestPostCommentUpdatesExistingSnapshot(t *testing.T) {
	var lists, creates, edits int
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":9,"user":{"login":"reeve-bot"},"body":"<!-- marker:posted --> created"}`)
		default:
			t.Fatalf("method = %s, want GET or POST", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/9", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		edits++
		fmt.Fprint(w, `{"id":9,"user":{"login":"reeve-bot"},"body":"<!-- marker:posted --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	c.commentMu.Lock()
	_, err := c.issueCommentsLocked(context.Background(), 12, false)
	c.commentMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PostComment(context.Background(), 12, "<!-- marker:posted --> created"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:posted --> updated", "<!-- marker:posted -->"); err != nil {
		t.Fatal(err)
	}
	if lists != 1 || creates != 1 || edits != 1 {
		t.Fatalf("list requests = %d, creates = %d, edits = %d; want 1, 1, 1", lists, creates, edits)
	}
}

func TestIssueCommentSnapshotDoesNotAliasCacheMutations(t *testing.T) {
	c := &Client{commentCache: map[int][]*gh.IssueComment{
		12: {
			{ID: gh.Int64(1), Body: gh.String("old")},
			{ID: gh.Int64(2), Body: gh.String("keep")},
		},
	}}
	c.commentMu.Lock()
	snapshot, err := c.issueCommentsLocked(context.Background(), 12, false)
	if err != nil {
		c.commentMu.Unlock()
		t.Fatal(err)
	}
	c.updateCachedCommentLocked(12, 1, "new", nil)
	c.removeCachedCommentLocked(12, 1)
	c.commentMu.Unlock()

	if len(snapshot) != 2 || snapshot[0].GetID() != 1 || snapshot[0].GetBody() != "old" || snapshot[1].GetID() != 2 {
		t.Fatalf("retained snapshot changed with cache: %#v", snapshot)
	}
	if got := c.commentCache[12]; len(got) != 1 || got[0].GetID() != 2 {
		t.Fatalf("cache mutation did not apply independently: %#v", got)
	}
}

func TestUpsertCommentCachesCreatedComment(t *testing.T) {
	var lists, creates, edits int
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":9,"user":{"login":"reeve-bot"},"body":"<!-- marker:new --> created"}`)
		default:
			t.Fatalf("method = %s, want GET or POST", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/9", func(w http.ResponseWriter, r *http.Request) {
		edits++
		fmt.Fprint(w, `{"id":9,"body":"<!-- marker:new --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	for _, body := range []string{"<!-- marker:new --> created", "<!-- marker:new --> updated"} {
		if err := c.UpsertComment(context.Background(), 12, body, "<!-- marker:new -->"); err != nil {
			t.Fatalf("UpsertComment: %v", err)
		}
	}
	if lists != 1 || creates != 1 || edits != 1 {
		t.Fatalf("list requests = %d, creates = %d, edits = %d; want 1, 1, 1", lists, creates, edits)
	}
}

// TestUpsertCommentSkipsForeignMarkerComment pins the author boundary: a
// comment by ANOTHER author that merely contains the marker is not
// overwritten. The previous behavior matched any comment whose body
// contained the marker and rewrote it, destroying someone else's text -
// e.g. a maintainer quoting reeve's board.
func TestUpsertCommentSkipsForeignMarkerComment(t *testing.T) {
	var lists, creates, edits int
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	// A foreign comment quoting the marker. Nothing of reeve's exists.
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[{"id":1,"user":{"login":"maintainer"},"body":"<!-- marker:one --> quoted board"}]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":2,"user":{"login":"reeve-bot"},"body":"<!-- marker:one --> recreated"}`)
		default:
			t.Fatalf("method = %s", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		t.Error("a foreign comment must never be edited")
		fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:one --> fresh", "<!-- marker:one -->"); err != nil {
		t.Fatal(err)
	}
	if edits != 0 || creates != 1 {
		t.Fatalf("edits = %d, creates = %d; want 0 edits and 1 create", edits, creates)
	}
}

// TestUpsertCommentEditsOnlyOwnComment: two comments carry the marker, one
// foreign and one authored by the token's identity; only the own one is
// edited.
func TestUpsertCommentEditsOnlyOwnComment(t *testing.T) {
	var edited int64
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[
			{"id":1,"user":{"login":"maintainer"},"body":"<!-- marker:one --> quoted"},
			{"id":2,"user":{"login":"reeve-bot"},"body":"<!-- marker:one --> reeve board"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("foreign comment must not be edited")
	})
	mux.HandleFunc("/repos/o/r/issues/comments/2", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&edited, 1)
		fmt.Fprint(w, `{"id":2,"user":{"login":"reeve-bot"},"body":"<!-- marker:one --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:one --> updated", "<!-- marker:one -->"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&edited) != 1 {
		t.Fatalf("own comment edits = %d, want 1", edited)
	}
}

// TestUpsertCommentIdentityFailureFailsClosed: when the token's identity
// cannot be established, the upsert must not touch any comment - neither
// editing nor creating - instead of guessing which comments are reeve's.
func TestUpsertCommentIdentityFailureFailsClosed(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	var edits int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:one -->"}]`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		t.Error("no comment may be edited when the token identity is unknown")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	err := c.UpsertComment(context.Background(), 12, "<!-- marker:one --> updated", "<!-- marker:one -->")
	if err == nil {
		t.Fatal("an unresolvable token identity must fail the upsert")
	}
	if edits != 0 {
		t.Fatalf("identity failure must edit nothing: edits=%d", edits)
	}
}

func TestUpsertCommentRediscoversAfterCachedEditNotFound(t *testing.T) {
	var lists, edits int
	mux := http.NewServeMux()
	withSelfIdentity(mux)
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		lists++
		id := 1
		if lists > 1 {
			id = 2
		}
		fmt.Fprintf(w, `[{"id":%d,"user":{"login":"reeve-bot"},"body":"<!-- marker:stale -->"}]`, id)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/2", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		fmt.Fprint(w, `{"id":2,"body":"<!-- marker:stale --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)

	err := c.UpsertComment(context.Background(), 12,
		"<!-- marker:stale --> updated", "<!-- marker:stale -->")
	if err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if lists != 2 || edits != 2 {
		t.Fatalf("list requests = %d, edits = %d; want 2 and 2", lists, edits)
	}
}

func TestDeleteCommentsReusesAndUpdatesSnapshot(t *testing.T) {
	var lists, creates, deletes int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"login":"reeve-bot"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[{"id":2,"body":"<!-- reeve:pr-comment:v1:part2 -->","user":{"login":"reeve-bot"}}]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":3,"body":"<!-- reeve:pr-comment:v1:part2 -->"}`)
		default:
			t.Fatalf("method = %s, want GET or POST", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/2", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			fmt.Fprint(w, `{"id":2,"body":"<!-- reeve:pr-comment:v1:part2 -->","user":{"login":"reeve-bot"}}`)
		case http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("method = %s, want PATCH or DELETE", r.Method)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newFakeClient(t, srv)
	const marker = "<!-- reeve:pr-comment:v1:part2 -->"

	if err := c.UpsertComment(context.Background(), 12, marker, marker); err != nil {
		t.Fatalf("UpsertComment before delete: %v", err)
	}
	if _, err := c.DeleteCommentsByMarkerPrefix(context.Background(), 12,
		"<!-- reeve:pr-comment:v1", 1); err != nil {
		t.Fatalf("DeleteCommentsByMarkerPrefix: %v", err)
	}
	if err := c.UpsertComment(context.Background(), 12, marker, marker); err != nil {
		t.Fatalf("UpsertComment after delete: %v", err)
	}
	if lists != 1 || deletes != 1 || creates != 1 {
		t.Fatalf("list requests = %d, deletes = %d, creates = %d; want 1, 1, 1", lists, deletes, creates)
	}
}

// newAuthorClient builds a Client that mirrors New's operator-override
// loading (comments.author / --comment-author folded to REEVE_COMMENT_AUTHOR)
// against the test server's direct mux.
func newAuthorClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := newFakeClient(t, srv)
	if a := strings.TrimSpace(os.Getenv("REEVE_COMMENT_AUTHOR")); a != "" {
		c.commentAuthorOverride.Store(&a)
	}
	return c
}

// TestUpsertCommentExplicitAuthorWinsOverDefaultToken pins the precedence:
// the explicit setting (comments.author / --comment-author) outranks the
// action-supplied default-token knowledge even when the token IS the
// runner's default github.token. The declared author reeve-bot owns the
// marker comment; had the default-token author (github-actions[bot]) been
// used, the upsert would create a duplicate instead of editing.
func TestUpsertCommentExplicitAuthorWinsOverDefaultToken(t *testing.T) {
	var userCalls, edits, creates int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		userCalls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `[{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:auth -->"}]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":2,"user":{"login":"reeve-bot"},"body":"created"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		fmt.Fprint(w, `{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:auth --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("REEVE_TOKEN_IS_DEFAULT", "true")
	t.Setenv("REEVE_COMMENT_AUTHOR", "reeve-bot")
	c := newAuthorClient(t, srv)

	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:auth --> updated", "<!-- marker:auth -->"); err != nil {
		t.Fatal(err)
	}
	if userCalls != 0 {
		t.Fatalf("an explicit setting must bypass /user entirely: userCalls=%d", userCalls)
	}
	if creates != 0 || edits != 1 {
		t.Fatalf("the declared author must win over the default-token author: edits=%d creates=%d", edits, creates)
	}
}

// TestUpsertCommentExplicitAuthorWinsWithCustomToken: a custom token whose
// /user fails is fine when the author is declared explicitly - the write
// proceeds and /user is not consulted.
func TestUpsertCommentExplicitAuthorWinsWithCustomToken(t *testing.T) {
	var userCalls, edits int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		userCalls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:auth -->"}]`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		fmt.Fprint(w, `{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:auth --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("REEVE_TOKEN_IS_DEFAULT", "false")
	t.Setenv("REEVE_COMMENT_AUTHOR", "reeve-bot")
	c := newAuthorClient(t, srv)

	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:auth --> updated", "<!-- marker:auth -->"); err != nil {
		t.Fatal(err)
	}
	if userCalls != 0 || edits != 1 {
		t.Fatalf("the declared author must win without touching /user: userCalls=%d edits=%d", userCalls, edits)
	}
}

// TestUpsertCommentDefaultTokenWritesWithoutConfig pins the default setup:
// with the runner's default github.token (REEVE_TOKEN_IS_DEFAULT=true) and
// no operator setting, the author is KNOWN to be github-actions[bot] and
// reeve's comment is edited - /user is not called, nothing is refused.
func TestUpsertCommentDefaultTokenWritesWithoutConfig(t *testing.T) {
	var userCalls, edits, creates int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		userCalls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `[{"id":1,"user":{"login":"github-actions[bot]"},"body":"<!-- marker:auth -->"}]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":2,"user":{"login":"github-actions[bot]"},"body":"created"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		edits++
		fmt.Fprint(w, `{"id":1,"user":{"login":"github-actions[bot]"},"body":"<!-- marker:auth --> updated"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("REEVE_TOKEN_IS_DEFAULT", "true")
	// No REEVE_COMMENT_AUTHOR: the default setup must work with no config.
	_ = os.Unsetenv("REEVE_COMMENT_AUTHOR") // empty value = no override
	c := newAuthorClient(t, srv)

	if err := c.UpsertComment(context.Background(), 12, "<!-- marker:auth --> updated", "<!-- marker:auth -->"); err != nil {
		t.Fatalf("the default-token path must write comments with no config: %v", err)
	}
	if userCalls != 0 {
		t.Fatalf("the default-token author is known; /user must not be consulted: userCalls=%d", userCalls)
	}
	if creates != 0 || edits != 1 {
		t.Fatalf("the default-token author must match reeve's own comment: edits=%d creates=%d", edits, creates)
	}
}

// TestUpsertCommentCustomTokenFailingUserRefused pins the fail-closed rule:
// with a CUSTOM token (the action has not declared the default-token
// author) and a failing /user and no setting, the write is refused with the
// typed error naming the setting - never guessed.
func TestUpsertCommentCustomTokenFailingUserRefused(t *testing.T) {
	var userCalls, writes int
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		userCalls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":1,"user":{"login":"reeve-bot"},"body":"<!-- marker:auth -->"}]`)
	})
	mux.HandleFunc("/repos/o/r/issues/comments/1", func(w http.ResponseWriter, _ *http.Request) {
		writes++
		fmt.Fprint(w, `{"id":1,"user":{"login":"reeve-bot"},"body":"edited"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("REEVE_TOKEN_IS_DEFAULT", "false")
	_ = os.Unsetenv("REEVE_COMMENT_AUTHOR") // empty value = no override
	c := newAuthorClient(t, srv)

	err := c.UpsertComment(context.Background(), 12, "<!-- marker:auth --> updated", "<!-- marker:auth -->")
	var cae *CommentAuthorError
	if !errors.As(err, &cae) {
		t.Fatalf("a custom token with no setting and a failing /user must refuse, got %v", err)
	}
	if !strings.Contains(cae.Error(), "set comments.author or --comment-author") {
		t.Fatalf("the refusal must name the setting: %v", cae)
	}
	if userCalls != 1 || writes != 0 {
		t.Fatalf("/user must be consulted once and no comment written: userCalls=%d writes=%d", userCalls, writes)
	}
}
