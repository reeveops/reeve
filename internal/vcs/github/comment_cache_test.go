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
	comments, err := c.listIssueComments(context.Background(), 12)
	if err != nil || len(comments) != 2 {
		t.Fatalf("listIssueComments = (%d comments, %v), want (2, nil)", len(comments), err)
	}
	if lists != 1 || edits != 3 {
		t.Fatalf("list requests = %d, edits = %d; want 1 and 3", lists, edits)
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
