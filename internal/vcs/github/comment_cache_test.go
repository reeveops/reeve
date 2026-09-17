package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	gh "github.com/google/go-github/v66/github"
)

func TestUpsertCommentReusesOneSnapshotAcrossMarkers(t *testing.T) {
	var lists, edits int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		lists++
		fmt.Fprint(w, `[
			{"id":1,"body":"<!-- marker:one -->"},
			{"id":2,"body":"<!-- marker:two -->"}
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
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":9,"body":"<!-- marker:posted --> created"}`)
		default:
			t.Fatalf("method = %s, want GET or POST", r.Method)
		}
	})
	mux.HandleFunc("/repos/o/r/issues/comments/9", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		edits++
		fmt.Fprint(w, `{"id":9,"body":"<!-- marker:posted --> updated"}`)
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
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists++
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			creates++
			fmt.Fprint(w, `{"id":9,"body":"<!-- marker:new --> created"}`)
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

func TestUpsertCommentRediscoversAfterCachedEditNotFound(t *testing.T) {
	var lists, edits int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/12/comments", func(w http.ResponseWriter, _ *http.Request) {
		lists++
		id := 1
		if lists > 1 {
			id = 2
		}
		fmt.Fprintf(w, `[{"id":%d,"body":"<!-- marker:stale -->"}]`, id)
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
